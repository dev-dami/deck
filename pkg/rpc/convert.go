package rpc

import (
	"time"

	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/wire"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// ms is t in Unix milliseconds, 0 for the zero time.
func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func each[T, U any](in []T, f func(T) *U) []*U {
	out := make([]*U, 0, len(in))
	for _, v := range in {
		out = append(out, f(v))
	}
	return out
}

func toTheme(t wire.Theme) *deckv1.Theme {
	out := &deckv1.Theme{Palette: t.Palette, Kinds: make(map[string]*deckv1.KindTone, len(t.Kinds))}
	for k, v := range t.Kinds {
		out.Kinds[k] = &deckv1.KindTone{Tint: v.Tint, Mark: v.Mark}
	}
	return out
}

func toInfo(i remote.Info) *deckv1.InfoResponse {
	return &deckv1.InfoResponse{Version: i.Version, Project: i.Project, Kinds: i.Kinds, Theme: toTheme(i.Theme)}
}

func toSession(s wire.Session) *deckv1.Session {
	return &deckv1.Session{
		Id: s.ID, Title: s.Title, UpdatedAt: ms(s.UpdatedAt), Busy: s.Busy,
		Pending: int32(s.Pending), Mode: s.Mode, LastText: s.LastText,
	}
}

func toPart(p wire.Part) *deckv1.Part {
	out := &deckv1.Part{
		Type: p.Type, Text: p.Text, Id: p.ID, Name: p.Name, Summary: p.Summary,
		ToolCallId: p.ToolCallID, IsError: p.IsError, Output: p.Output, Diff: p.Diff,
	}
	if p.Image != nil {
		out.Image = &deckv1.ImageRef{Name: p.Image.Name, Mime: p.Image.Mime}
	}
	return out
}

func toMessage(m wire.Message) *deckv1.Message {
	return &deckv1.Message{
		Id: m.ID, SessionId: m.SessionID, Role: m.Role, Agent: m.Agent,
		CreatedAt: ms(m.CreatedAt), UpdatedAt: ms(m.UpdatedAt), Finished: m.Finished,
		Editable: m.Editable, Edited: m.Edited, Parts: each(m.Parts, toPart),
	}
}

func toQueued(q wire.QueuedItem) *deckv1.QueuedItem {
	return &deckv1.QueuedItem{Id: q.ID, Agent: q.Agent, Text: q.Text, At: ms(q.At)}
}

func toAgent(a wire.AgentLine) *deckv1.AgentLine {
	return &deckv1.AgentLine{Id: a.ID, Name: a.Name, Kind: a.Kind, Model: a.Model, Effort: a.Effort, Busy: a.Busy}
}

func toModel(m wire.ModelChoice) *deckv1.ModelChoice {
	return &deckv1.ModelChoice{Id: m.ID, Label: m.Label, Efforts: m.Efforts, Selected: m.Selected}
}

func toApproval(a wire.Approval) *deckv1.Approval {
	return &deckv1.Approval{
		Id: a.ID, SessionId: a.SessionID, ToolCallId: a.ToolCallID, Agent: a.Agent, Kind: a.Kind,
		Description: a.Description, Command: a.Command, Path: a.Path, Diff: a.Diff, Raw: a.Raw,
		CreatedAt: ms(a.CreatedAt),
	}
}

func toChanged(c wire.ChangedFile) *deckv1.ChangedFile {
	return &deckv1.ChangedFile{Path: c.Path, Added: int32(c.Added), Removed: int32(c.Removed), Diff: c.Diff}
}

// toEvent converts a backend event; ok is false for one the desktop has no
// use for (device pairing belongs to the phone transport).
func toEvent(ev remote.Event) (out *deckv1.Event, ok bool) {
	out = &deckv1.Event{SessionId: ev.SessionID}
	switch p := ev.Payload.(type) {
	case wire.Session:
		switch ev.Name {
		case wire.EvSessionCreated:
			out.Kind = &deckv1.Event_SessionCreated{SessionCreated: toSession(p)}
		case wire.EvSessionUpdated:
			out.Kind = &deckv1.Event_SessionUpdated{SessionUpdated: toSession(p)}
		case wire.EvSessionDeleted:
			out.Kind = &deckv1.Event_SessionDeleted{SessionDeleted: toSession(p)}
		default:
			return nil, false
		}
	case wire.Message:
		switch ev.Name {
		case wire.EvMessageCreated:
			out.Kind = &deckv1.Event_MessageCreated{MessageCreated: toMessage(p)}
		case wire.EvMessageUpdated:
			out.Kind = &deckv1.Event_MessageUpdated{MessageUpdated: toMessage(p)}
		default:
			return nil, false
		}
	case wire.MessageDeleted:
		out.Kind = &deckv1.Event_MessageDeleted{MessageDeleted: &deckv1.MessageDeleted{Id: p.ID, SessionId: p.SessionID}}
	case wire.Approval:
		out.Kind = &deckv1.Event_ApprovalRequested{ApprovalRequested: toApproval(p)}
	case wire.ApprovalResolved:
		out.Kind = &deckv1.Event_ApprovalResolved{ApprovalResolved: &deckv1.ApprovalResolved{Id: p.ID, SessionId: p.SessionID, Granted: p.Granted}}
	case wire.BusyChanged:
		out.Kind = &deckv1.Event_Busy{Busy: &deckv1.Busy{SessionId: p.SessionID, Busy: p.Busy}}
	case wire.QueueChanged:
		out.Kind = &deckv1.Event_QueueChanged{QueueChanged: &deckv1.QueueChanged{SessionId: p.SessionID, Items: each(p.Items, toQueued)}}
	case wire.RosterChanged:
		out.Kind = &deckv1.Event_RosterChanged{RosterChanged: &deckv1.SessionRef{SessionId: p.SessionID}}
	case wire.ModeChanged:
		out.Kind = &deckv1.Event_ModeChanged{ModeChanged: &deckv1.ModeChanged{SessionId: p.SessionID, Mode: p.Mode}}
	case wire.Notice:
		out.Kind = &deckv1.Event_Notice{Notice: &deckv1.Notice{SessionId: p.SessionID, Text: p.Text}}
	case wire.Theme:
		out.Kind = &deckv1.Event_ThemeChanged{ThemeChanged: toTheme(p)}
	default:
		return nil, false
	}
	return out, true
}
