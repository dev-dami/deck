package opencode

import (
	"encoding/json"
	"sync"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/codex"
)

// handleServerRequest answers a request OpenCode sends. Permission requests
// become Deck approvals; anything else is declined (Deck offers OpenCode no
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
	var once sync.Once
	req.Respond = func(d agent.Decision) {
		once.Do(func() {
			_ = r.p.client.Respond(m.ID, permissionResult{Outcome: pick(p.Options, d)})
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
