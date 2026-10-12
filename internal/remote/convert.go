package remote

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/aymanbagabas/go-udiff"

	"github.com/dev-dami/deck/internal/message"
	"github.com/dev-dami/deck/internal/permission"
	"github.com/dev-dami/deck/internal/remote/wire"
	"github.com/dev-dami/deck/internal/session"
	tools "github.com/dev-dami/deck/internal/toolspec"
)

// truncate cuts s to at most max bytes on a rune boundary.
func truncate(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	s = s[:max]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s, true
}

// summaryKeys are the tool-input fields worth showing on a collapsed tool card,
// most telling first.
var summaryKeys = []string{"command", "file_path", "path", "pattern", "url", "query", "description"}

func toolSummary(input string) string {
	var obj map[string]any
	if json.Unmarshal([]byte(input), &obj) == nil {
		for _, k := range summaryKeys {
			if v, ok := obj[k].(string); ok && v != "" {
				s, _ := truncate(v, 200)
				return s
			}
		}
	}
	s, _ := truncate(strings.TrimSpace(input), 120)
	return s
}

func unifiedDiff(path, oldC, newC string) (string, bool) {
	d := udiff.Unified("a/"+path, "b/"+path, oldC, newC)
	return truncate(d, wire.MaxDiffBytes)
}

func role(r message.MessageRole) string {
	switch r {
	case message.User:
		return wire.RoleUser
	case message.System:
		return wire.RoleSystem
	}
	return wire.RoleAssistant
}

// toMessage converts a stored row. Rows Deck writes for itself carry provider
// "deck"; agent rows carry the agent's id in Model.
func toMessage(m message.Message) wire.Message {
	out := wire.Message{
		ID: m.ID, SessionID: m.SessionID, Role: role(m.Role),
		CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
		Finished: m.IsFinished() || m.Role != message.Assistant,
		Parts:    []wire.Part{},
	}
	if m.Role == message.Assistant && m.Provider != "deck" {
		out.Agent = m.Model
	}
	for _, p := range m.Parts {
		switch v := p.(type) {
		case message.TextContent:
			if !v.Hidden && v.Text != "" {
				out.Parts = append(out.Parts, wire.Part{Type: wire.PartText, Text: v.Text})
			}
		case message.ReasoningContent:
			if v.Thinking != "" {
				out.Parts = append(out.Parts, wire.Part{Type: wire.PartReasoning, Text: v.Thinking})
			}
		case message.ToolCall:
			out.Parts = append(out.Parts, wire.Part{Type: wire.PartToolCall, ID: v.ID, Name: v.Name, Summary: toolSummary(v.Input)})
		case message.ToolResult:
			part := wire.Part{Type: wire.PartToolResult, ToolCallID: v.ToolCallID, Name: v.Name, IsError: v.IsError}
			part.Output, part.Truncated = truncate(v.Content, wire.MaxOutputBytes)
			var meta tools.EditResponseMetadata
			if v.Metadata != "" && json.Unmarshal([]byte(v.Metadata), &meta) == nil && (meta.OldContent != "" || meta.NewContent != "") {
				part.Diff, _ = unifiedDiff(v.Name, meta.OldContent, meta.NewContent)
			}
			out.Parts = append(out.Parts, part)
		case message.BinaryContent:
			out.Parts = append(out.Parts, wire.Part{Type: wire.PartImage, Name: v.Path, Mime: v.MIMEType})
		case message.ImageURLContent:
			out.Parts = append(out.Parts, wire.Part{Type: wire.PartImage, Name: "image"})
		}
	}
	return out
}

func toSession(s session.Session, busy bool, pending int) wire.Session {
	return wire.Session{ID: s.ID, Title: s.Title, UpdatedAt: s.UpdatedAt, Busy: busy, Pending: pending}
}

// toApproval converts a permission request into what a phone shows: the full
// command, or the full diff of an edit.
func toApproval(r permission.PermissionRequest, createdAt int64) wire.Approval {
	a := wire.Approval{
		ID: r.ID, SessionID: r.SessionID, ToolCallID: r.ToolCallID,
		Description: r.Description, Kind: wire.ApprovalOther, CreatedAt: createdAt,
	}
	switch p := r.Params.(type) {
	case tools.BashPermissionsParams:
		a.Kind, a.Command, a.Cwd, a.Agent = wire.ApprovalCommand, p.Command, p.WorkingDir, p.Description
	case tools.EditPermissionsParams:
		a.Kind, a.Path = wire.ApprovalEdit, p.FilePath
		a.Diff, _ = unifiedDiff(p.FilePath, p.OldContent, p.NewContent)
	case string:
		a.Raw, _ = truncate(p, wire.MaxRawBytes)
	default:
		if b, err := json.Marshal(p); err == nil {
			a.Raw, _ = truncate(string(b), wire.MaxRawBytes)
		}
	}
	return a
}
