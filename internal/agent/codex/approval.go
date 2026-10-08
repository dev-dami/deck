package codex

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/dev-dami/deck/internal/agent"
)

// handleServerRequest answers (or surfaces to the user) a server-initiated
// request. It must not block the event loop.
func (r *run) handleServerRequest(m Message) {
	switch m.Method {
	case reqCommandApproval:
		var p commandApprovalParams
		if json.Unmarshal(m.Params, &p) != nil {
			_ = r.client.RespondError(m.ID, -32602, "invalid params")
			return
		}
		// Prefer the parsed inner command over the shell wrapper
		// (powershell.exe -Command '...').
		summary := commandTitle(threadItem{Command: p.Command, CommandActions: p.CommandActions})
		if summary == "" {
			summary = p.Reason
		}
		if p.Kind == "writeStdin" && summary == "" {
			summary = "send input to a running process"
		}
		req := agent.ApprovalRequest{
			ID:       approvalID(p.ApprovalID, p.ItemID),
			AgentID:  r.spec.ID,
			Kind:     agent.ApproveCommand,
			ToolName: "Bash",
			Summary:  summary,
			Cwd:      p.Cwd,
		}
		req.Respond = r.responder(m.ID, func(d agent.Decision) any {
			return decisionResponse{Decision: commandDecision(d)}
		})
		r.push(req)

	case reqFileChangeApproval:
		var p fileChangeApprovalParams
		if json.Unmarshal(m.Params, &p) != nil {
			_ = r.client.RespondError(m.ID, -32602, "invalid params")
			return
		}
		// The request itself carries no diff; the proposed changes arrived with
		// the item/started notification for the same item id.
		diffs := r.tr.fileDiffsFor(p.ItemID)
		paths := make([]string, 0, len(diffs))
		for _, d := range diffs {
			paths = append(paths, d.Path)
		}
		summary := strings.Join(paths, ", ")
		if summary == "" {
			summary = p.Reason
		}
		req := agent.ApprovalRequest{
			ID:       approvalID("", p.ItemID),
			AgentID:  r.spec.ID,
			Kind:     agent.ApproveEdit,
			ToolName: "Edit",
			Summary:  summary,
			Cwd:      r.spec.Dir,
		}
		if len(diffs) > 0 {
			d := diffs[0] // agent.ApprovalRequest holds a single FileDiff
			req.Diff = &d
		}
		req.Respond = r.responder(m.ID, func(d agent.Decision) any {
			return decisionResponse{Decision: fileChangeDecision(d)}
		})
		r.push(req)

	case reqPermissionsApproval:
		var p permissionsApprovalParams
		if json.Unmarshal(m.Params, &p) != nil {
			_ = r.client.RespondError(m.ID, -32602, "invalid params")
			return
		}
		summary := p.Reason
		if summary == "" {
			summary = "additional permissions: " + string(p.Permissions)
		}
		req := agent.ApprovalRequest{
			ID:       approvalID("", p.ItemID),
			AgentID:  r.spec.ID,
			Kind:     agent.ApproveOther,
			ToolName: "Permissions",
			Summary:  summary,
			Cwd:      p.Cwd,
		}
		req.Respond = r.responder(m.ID, func(d agent.Decision) any {
			if !d.Allow {
				return permissionsResponse{Permissions: json.RawMessage(`{}`), Scope: "turn"}
			}
			scope := "turn"
			if d.Always {
				scope = "session"
			}
			return permissionsResponse{Permissions: p.Permissions, Scope: scope}
		})
		r.push(req)

	case reqElicitation:
		_ = r.client.Respond(m.ID, map[string]any{"action": "decline"})

	default:
		// requestUserInput, dynamic tool calls, token refresh, attestation, ...
		// Deck does not support these; refuse rather than hang the turn. In
		// particular Deck never answers credential refresh requests.
		_ = r.client.RespondError(m.ID, -32601, "not supported by Deck: "+m.Method)
	}
}

// push hands a request to the consumer without blocking the event loop.
func (r *run) push(req agent.ApprovalRequest) {
	r.pushers.Add(1)
	go func() {
		defer r.pushers.Done()
		select {
		case r.approvals <- req:
		case <-r.closing:
		}
	}()
}

// responder builds a Respond func that answers the JSON-RPC request exactly once.
func (r *run) responder(id json.RawMessage, build func(agent.Decision) any) func(agent.Decision) {
	var once sync.Once
	return func(d agent.Decision) {
		once.Do(func() {
			_ = r.client.Respond(id, build(d))
		})
	}
}

func approvalID(approvalID, itemID string) string {
	if approvalID != "" {
		return approvalID
	}
	return itemID
}

// commandDecision maps a Decision to CommandExecutionApprovalDecision.
func commandDecision(d agent.Decision) string {
	switch {
	case !d.Allow:
		return decisionDecline
	case d.Always:
		return decisionAcceptForSession
	}
	return decisionAccept
}

// fileChangeDecision maps a Decision to FileChangeApprovalDecision.
func fileChangeDecision(d agent.Decision) string { return commandDecision(d) }
