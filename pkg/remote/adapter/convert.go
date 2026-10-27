package adapter

import (
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/aymanbagabas/go-udiff"

	deckagent "github.com/dev-dami/deck/internal/deck/agent"
	decks "github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/message"
	"github.com/dev-dami/deck/internal/permission"
	"github.com/dev-dami/deck/internal/session"
	tools "github.com/dev-dami/deck/internal/toolspec"
	"github.com/dev-dami/deck/internal/workspace"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// Conversions to wire types. Caps are the server's job (R12); here every
// string goes through whole.

func unix(sec int64) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0).UTC()
}

// summaryKeys are the tool-input fields worth showing on a collapsed tool
// card, most telling first.
var summaryKeys = []string{"command", "file_path", "path", "pattern", "url", "query", "description"}

// toolSummary picks the most telling input field; a path is shown
// relative to root, so the phone never sees the machine's layout.
func toolSummary(input, root string) string {
	var obj map[string]any
	if json.Unmarshal([]byte(input), &obj) == nil {
		for _, k := range summaryKeys {
			if v, ok := obj[k].(string); ok && v != "" {
				if k == "file_path" || k == "path" {
					return relPath(root, v)
				}
				return v
			}
		}
	}
	return strings.TrimSpace(input)
}

func unifiedDiff(path, oldC, newC string) string {
	return udiff.Unified("a/"+path, "b/"+path, oldC, newC)
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

// toMessage converts a stored row. Rows Deck writes for itself carry
// provider "deck"; agent rows carry the agent's id in Model.
func toMessage(m message.Message, root string) wire.Message {
	out := wire.Message{
		ID: m.ID, SessionID: m.SessionID, Role: role(m.Role),
		CreatedAt: unix(m.CreatedAt), UpdatedAt: unix(m.UpdatedAt),
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
			out.Parts = append(out.Parts, wire.Part{Type: wire.PartToolCall, ID: v.ID, Name: v.Name, Summary: toolSummary(v.Input, root)})
		case message.ToolResult:
			part := wire.Part{Type: wire.PartToolResult, ToolCallID: v.ToolCallID, Name: v.Name, IsError: v.IsError, Output: v.Content}
			var meta tools.EditResponseMetadata
			if v.Metadata != "" && json.Unmarshal([]byte(v.Metadata), &meta) == nil && (meta.OldContent != "" || meta.NewContent != "") {
				part.Diff = unifiedDiff(v.Name, meta.OldContent, meta.NewContent)
			}
			out.Parts = append(out.Parts, part)
		case message.BinaryContent:
			out.Parts = append(out.Parts, wire.Part{Type: wire.PartImage, Image: &wire.Image{Name: displayName(v.Path), Mime: v.MIMEType}})
		case message.ImageURLContent:
			out.Parts = append(out.Parts, wire.Part{Type: wire.PartImage, Image: &wire.Image{Name: "image"}})
		}
	}
	return out
}

// displayName is a file's name, never its path.
func displayName(p string) string {
	name := filepath.Base(strings.ReplaceAll(p, "\\", "/"))
	if name == "." || name == "/" || name == "" {
		return "image"
	}
	return name
}

func toSession(s session.Session, busy bool, pending int, mode string) wire.Session {
	return wire.Session{ID: s.ID, Title: s.Title, UpdatedAt: unix(s.UpdatedAt), Busy: busy, Pending: pending, Mode: mode}
}

// relPath makes p relative to root when it is under it; an absolute path
// elsewhere is reduced to its name, so no path leaves the desktop.
func relPath(root, p string) string {
	sp := filepath.ToSlash(p)
	sr := strings.TrimSuffix(filepath.ToSlash(root), "/")
	if sr != "" && len(sp) > len(sr)+1 && strings.EqualFold(sp[:len(sr)+1], sr+"/") {
		return sp[len(sr)+1:]
	}
	if filepath.IsAbs(p) || strings.HasPrefix(sp, "/") {
		return path.Base(sp)
	}
	return sp
}

// toApproval converts a permission request into what a phone shows: the
// full command, or the full diff of an edit. The agent id rides in the
// bash params' Description, as the workspace sets it.
func toApproval(r permission.PermissionRequest, at time.Time, root string) wire.Approval {
	a := wire.Approval{
		ID: r.ID, SessionID: r.SessionID, ToolCallID: r.ToolCallID,
		Description: r.Description, Kind: wire.ApprovalOther, CreatedAt: at.UTC(),
	}
	switch p := r.Params.(type) {
	case tools.BashPermissionsParams:
		a.Kind, a.Command, a.Agent = wire.ApprovalCommand, p.Command, p.Description
	case tools.EditPermissionsParams:
		a.Kind, a.Path = wire.ApprovalEdit, relPath(root, p.FilePath)
		a.Diff = unifiedDiff(a.Path, p.OldContent, p.NewContent)
	case string:
		a.Raw = p
		var raw struct {
			Agent string `json:"agent"`
		}
		if json.Unmarshal([]byte(p), &raw) == nil {
			a.Agent = raw.Agent
		}
	default:
		if b, err := json.Marshal(p); err == nil {
			a.Raw = string(b)
		}
	}
	if a.Agent == "" {
		a.Agent = agentFromDescription(r.Description)
	}
	return a
}

// agentFromDescription reads the agent id off "<agent> wants to ...".
func agentFromDescription(d string) string {
	if i := strings.Index(d, " wants to "); i > 0 {
		return d[:i]
	}
	return ""
}

func toQueue(items []decks.QueuedItem) []wire.QueuedItem {
	out := make([]wire.QueuedItem, 0, len(items))
	for _, q := range items {
		out = append(out, wire.QueuedItem{ID: q.ID, Agent: q.AgentID, Text: q.Text, At: q.At.UTC()})
	}
	return out
}

func busyStatus(s string) bool { return s == "working" || s == "tool" || s == "approval" }

func toRoster(lines []workspace.AgentLine) []wire.AgentLine {
	out := make([]wire.AgentLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, wire.AgentLine{ID: l.ID, Name: l.ID, Kind: l.Kind, Model: l.Model, Effort: l.Effort, Busy: busyStatus(l.Status),
			Profile: l.Profile, Role: l.Role, Color: l.Color})
	}
	return out
}

func toKinds(kinds []workspace.AgentKind) []wire.AgentKind {
	out := make([]wire.AgentKind, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, wire.AgentKind{Kind: k.Kind, Model: k.Model, Joined: k.Joined})
	}
	return out
}

func toModels(choices []workspace.ModelChoice, kind string) []wire.ModelChoice {
	out := make([]wire.ModelChoice, 0, len(choices))
	for _, c := range choices {
		if kind != "" && c.Kind != kind {
			continue
		}
		mc := wire.ModelChoice{ID: c.ID, Label: c.Name, Selected: c.Current}
		for _, e := range c.Efforts {
			mc.Efforts = append(mc.Efforts, e.Level)
		}
		out = append(out, mc)
	}
	return out
}

// unified renders a file's hunks as unified-diff text.
func unified(d deckagent.FileDiff) string {
	var sb strings.Builder
	for _, h := range d.Hunks {
		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
		for _, l := range h.Lines {
			sb.WriteRune(rune(l.Op))
			sb.WriteString(strings.TrimSuffix(l.Text, "\n"))
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

func toChanges(cs decks.ChangeSet) []wire.ChangedFile {
	out := make([]wire.ChangedFile, 0, len(cs.Files))
	for _, f := range cs.Files {
		cf := wire.ChangedFile{Path: relPath(cs.Root, f.Path), Added: f.Added, Removed: f.Removed}
		if !f.Binary {
			cf.Diff = unified(f.FileDiff)
		}
		out = append(out, cf)
	}
	return out
}

func toTheme(t workspace.Theme) wire.Theme {
	out := wire.Theme{Palette: map[string]string{}, Kinds: map[string]wire.KindTone{}}
	for k, v := range t.Palette {
		out.Palette[k] = v
	}
	for k, v := range t.Kinds {
		out.Kinds[k] = wire.KindTone{Tint: v.Tint, Mark: v.Mark}
	}
	return out
}
