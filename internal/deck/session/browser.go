package session

import (
	"context"
	"encoding/json"
	"github.com/dev-dami/deck/internal/deck/agent"
	pb "github.com/dev-dami/deck/pkg/rpc/deckv1"
	"google.golang.org/protobuf/proto"
	"strings"
)

func (m *Manager) SetBrowserAction(run func(context.Context, string, string, *pb.BrowserCommand) *pb.BrowserActionResponse) {
	m.mu.Lock()
	m.browserAction = run
	m.mu.Unlock()
}

// browserActs are the ops that act on a page and can change things out in the
// world: plan mode lets an agent read pages, not act on them.
var browserActs = map[string]bool{"click": true, "type": true, "select": true, "press": true}

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
	if a := m.get(owner); a != nil && browserActs[c.Action] && a.thread.getMode() == agent.ModePlan {
		return agent.TextResult("plan_mode: plan mode lets you read pages, not act on them; the user can switch the mode.", true)
	}
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
		// What the tab says its agent is doing is for the person's window.
		tab := proto.Clone(response.Tab).(*pb.BrowserTab)
		tab.Activity = ""
		meta, _ := json.Marshal(tab)
		r.Text = "UNTRUSTED BROWSER DATA (including page title and URL):\ntab=" + string(meta) + "\n" + r.Text
	}
	return r
}
