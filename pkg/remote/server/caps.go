package server

import (
	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// Every string the server sends is capped (R12): the caps are applied here,
// to results and to event payloads, so no backend can exceed them.

func capMessage(m wire.Message) wire.Message {
	parts := make([]wire.Part, len(m.Parts))
	for i, p := range m.Parts {
		switch p.Type {
		case wire.PartReasoning:
			p.Text, p.TextTruncated = wire.Truncate(p.Text, wire.MaxReasoningBytes)
		default:
			p.Text, p.TextTruncated = wire.Truncate(p.Text, wire.MaxTextBytes)
		}
		p.Output, p.OutputTruncated = wire.Truncate(p.Output, wire.MaxOutputBytes)
		p.Diff, p.DiffTruncated = wire.Truncate(p.Diff, wire.MaxDiffBytes)
		p.Summary, _ = wire.Truncate(p.Summary, wire.MaxCommandBytes)
		parts[i] = p
	}
	m.Parts = parts
	return m
}

func capMessages(ms []wire.Message) []wire.Message {
	out := make([]wire.Message, len(ms))
	for i, m := range ms {
		out[i] = capMessage(m)
	}
	return out
}

func capApproval(a wire.Approval) wire.Approval {
	a.Command, a.CommandTruncated = wire.Truncate(a.Command, wire.MaxCommandBytes)
	a.Diff, a.DiffTruncated = wire.Truncate(a.Diff, wire.MaxDiffBytes)
	a.Raw, a.RawTruncated = wire.Truncate(a.Raw, wire.MaxCommandBytes)
	a.Description, _ = wire.Truncate(a.Description, wire.MaxCommandBytes)
	return a
}

func capApprovals(as []wire.Approval) []wire.Approval {
	out := make([]wire.Approval, len(as))
	for i, a := range as {
		out[i] = capApproval(a)
	}
	return out
}

func capQueued(q wire.QueuedItem) wire.QueuedItem {
	q.Text, q.TextTruncated = wire.Truncate(q.Text, wire.MaxTextBytes)
	return q
}

func capQueue(qs []wire.QueuedItem) []wire.QueuedItem {
	out := make([]wire.QueuedItem, len(qs))
	for i, q := range qs {
		out[i] = capQueued(q)
	}
	return out
}

func capChanged(c wire.ChangedFile) wire.ChangedFile {
	c.Diff, c.DiffTruncated = wire.Truncate(c.Diff, wire.MaxDiffBytes)
	return c
}

func capChanges(cs []wire.ChangedFile) []wire.ChangedFile {
	out := make([]wire.ChangedFile, len(cs))
	for i, c := range cs {
		out[i] = capChanged(c)
	}
	return out
}

func capSession(s wire.Session) wire.Session {
	s.LastText, _ = wire.Truncate(s.LastText, wire.MaxCommandBytes)
	if r := []rune(s.Title); len(r) > maxTitleRunes {
		s.Title = string(r[:maxTitleRunes])
	}
	return s
}

func capSessions(ss []wire.Session) []wire.Session {
	out := make([]wire.Session, len(ss))
	for i, s := range ss {
		out[i] = capSession(s)
	}
	return out
}

// capEvent caps an event's payload when it is one of the capped types.
func capEvent(ev remote.Event) remote.Event {
	switch p := ev.Payload.(type) {
	case wire.Message:
		ev.Payload = capMessage(p)
	case wire.Approval:
		ev.Payload = capApproval(p)
	case wire.QueueChanged:
		p.Items = capQueue(p.Items)
		ev.Payload = p
	case wire.Session:
		ev.Payload = capSession(p)
	case wire.Notice:
		p.Text, _ = wire.Truncate(p.Text, wire.MaxTextBytes)
		ev.Payload = p
	}
	return ev
}
