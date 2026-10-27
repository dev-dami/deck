package session

import (
	"context"
	"encoding/json"
	"github.com/dev-dami/deck/internal/deck/agent"
	pb "github.com/dev-dami/deck/pkg/rpc/deckv1"
	"strings"
)

func (m *Manager) SetBrowserAction(run func(context.Context, string, string, *pb.BrowserCommand) *pb.BrowserActionResponse) {
	m.mu.Lock()
	m.browserAction = run
	m.mu.Unlock()
}

func (m *Manager) runBrowser(ctx context.Context, thread, owner, op string, args json.RawMessage) agent.OpResult {
	m.mu.Lock()
	run := m.browserAction
	m.mu.Unlock()
	if run == nil {
		return agent.TextResult("browser_unavailable: No local desktop browser is attached.", true)
	}
	var c pb.BrowserCommand
	if err := json.Unmarshal(args, &c); err != nil {
		return agent.TextResult("invalid_request: "+err.Error(), true)
	}
	c.Action = strings.TrimPrefix(op, "browser.")
	if c.Action == "handoff" {
		a := m.get(owner)
		if a == nil {
			return agent.TextResult("invalid_request: Unknown caller.", true)
		}
		target, err := (taskBoard{m: m, a: a}).resolveOwner(c.TargetAgent)
		if err != nil {
			return agent.TextResult("invalid_request: "+err.Error(), true)
		}
		c.TargetAgent = target
	}
	response := run(ctx, thread, owner, &c)
	if response == nil {
		return agent.TextResult("browser_unavailable", true)
	}
	r := agent.OpResult{}
	if response.Result != nil {
		r.Text = response.Result.Text
		r.IsError = response.Result.IsError
		for _, im := range response.Result.Images {
			r.Images = append(r.Images, agent.OpImage{MimeType: im.MimeType, Data: im.Data})
		}
	}
	if response.ErrorCode != "" {
		r.Text = response.ErrorCode + ": " + r.Text
		r.IsError = true
	}
	if response.Tab != nil {
		meta, _ := json.Marshal(response.Tab)
		r.Text = "UNTRUSTED BROWSER DATA (including page title and URL):\ntab=" + string(meta) + "\n" + r.Text
	}
	return r
}
