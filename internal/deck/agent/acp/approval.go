package acp

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/codex"
)

// handleServerRequest answers a request the agent sends. Permission requests
// become Deck approvals; anything else is declined (Deck offers the agent no
// file system or terminal of its own). It must not block the event loop.
func (r *run) handleServerRequest(m codex.Message) {
	if m.Method != reqRequestPermission {
		_ = r.p.client.RespondError(m.ID, -32601, "method not supported by Deck")
		return
	}
	var p permissionParams
	if json.Unmarshal(m.Params, &p) != nil {
		_ = r.p.client.RespondError(m.ID, -32602, "invalid params")
		return
	}
	r.trMu.Lock()
	tool := r.tr.agentTool(r.tr.merge(p.ToolCall).call)
	diff := r.tr.diffFor(p.ToolCall)
	r.trMu.Unlock()

	req := agent.ApprovalRequest{
		ID:       p.ToolCall.ToolCallID,
		AgentID:  r.spec.ID,
		Kind:     agent.ApproveOther,
		ToolName: tool.Name,
		Summary:  tool.Title,
		Cwd:      r.spec.Dir,
		Diff:     diff,
	}
	if req.ID == "" {
		req.ID = string(m.ID)
	}
	switch p.ToolCall.Kind {
	case "execute":
		req.Kind = agent.ApproveCommand
	case "edit", "delete", "move":
		req.Kind = agent.ApproveEdit
	}
	if req.Summary == "" {
		req.Summary = tool.Name
	}
	choose := func(d agent.Decision) permissionOutcome { return pick(p.Options, d) }
	if isChoice(p.Options) {
		// Not a permission but a choice (pi-acp's extension select): ask it
		// as a question whose options are the agent's.
		q := choiceQuestion(p)
		req.Kind, req.Diff, req.Summary = agent.ApproveQuestion, nil, q.Text
		req.Questions = []agent.Question{q}
		choose = func(d agent.Decision) permissionOutcome { return chosen(p.Options, q.ID, d) }
	}
	var once sync.Once
	req.Respond = func(d agent.Decision) {
		once.Do(func() {
			_ = r.p.client.Respond(m.ID, permissionResult{Outcome: choose(d)})
		})
	}
	r.pushers.Add(1)
	go func() {
		defer r.pushers.Done()
		select {
		case r.approvals <- req:
		case <-r.closing:
			req.Respond(agent.Decision{Allow: false})
		}
	}()
}

// isChoice reports whether a permission request is a choice among options
// rather than a yes or no: nothing to reject, and several options of one
// kind (pi-acp asks an extension's select prompt as one allow_once option
// per choice).
func isChoice(opts []permissionOption) bool {
	seen := map[string]bool{}
	repeated := false
	for _, o := range opts {
		if strings.HasPrefix(o.Kind, "reject") {
			return false
		}
		repeated = repeated || seen[o.Kind]
		seen[o.Kind] = true
	}
	return repeated
}

// choiceQuestion is a choice request as a question: the tool call's title
// (and message, when it has one) asks, the options' names are the choices.
func choiceQuestion(p permissionParams) agent.Question {
	var in struct {
		Title   string `json:"title"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(p.ToolCall.RawInput, &in)
	text := strings.TrimSpace(p.ToolCall.Title)
	if text == "" {
		text = strings.TrimSpace(in.Title)
	}
	if m := strings.TrimSpace(in.Message); m != "" && m != text {
		text = strings.TrimSpace(text + "\n" + m)
	}
	if text == "" {
		text = "Choose one"
	}
	q := agent.Question{ID: p.ToolCall.ToolCallID, Text: text}
	if q.ID == "" {
		q.ID = "choice"
	}
	for _, o := range p.Options {
		name := o.Name
		if name == "" {
			name = o.OptionID
		}
		q.Options = append(q.Options, agent.QuestionOption{Label: name})
	}
	return q
}

// chosen is the option the person picked for question id, or cancelled.
func chosen(opts []permissionOption, id string, d agent.Decision) permissionOutcome {
	if d.Allow {
		for _, label := range d.Answers[id].Selected {
			for _, o := range opts {
				if o.Name == label || (o.Name == "" && o.OptionID == label) {
					return permissionOutcome{Outcome: "selected", OptionID: o.OptionID}
				}
			}
		}
	}
	return permissionOutcome{Outcome: "cancelled"}
}

// pick chooses the permission option that matches a decision.
func pick(opts []permissionOption, d agent.Decision) permissionOutcome {
	want := []string{"reject_once", "reject_always"}
	if d.Allow {
		want = []string{"allow_once", "allow_always"}
		if d.Always {
			want = []string{"allow_always", "allow_once"}
		}
	}
	for _, k := range want {
		for _, o := range opts {
			if o.Kind == k {
				return permissionOutcome{Outcome: "selected", OptionID: o.OptionID}
			}
		}
	}
	return permissionOutcome{Outcome: "cancelled"}
}
