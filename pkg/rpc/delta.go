package rpc

import (
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// CapAppendText is the Subscribe capability for message_append_text, and
// the Info capability that says this engine sends it.
const (
	CapAppendText     = "append_text"
	infoCapAppendText = "AppendText"
)

// maxTracked bounds how many streaming messages deltas remembers. Past
// it the memory is dropped and the next update of each goes out whole,
// which is always correct.
const maxTracked = 512

// deltas remembers the last full form of each message the feed sent, so
// a streaming update that only appended text can also go out as a
// message_append_text. It is used from the server's single event pump.
type deltas struct {
	last map[string]*deckv1.Message
}

func newDeltas() *deltas { return &deltas{last: map[string]*deckv1.Message{}} }

// reset forgets everything: a new epoch starts from snapshots.
func (d *deltas) reset() { clear(d.last) }

// observe notes ev and returns the append-text form of it, or nil when it
// has none.
func (d *deltas) observe(ev *deckv1.Event) *deckv1.Event {
	switch {
	case ev.GetMessageCreated() != nil:
		d.remember(ev.GetMessageCreated())
	case ev.GetMessageDeleted() != nil:
		delete(d.last, ev.GetMessageDeleted().GetId())
	case ev.GetMessageUpdated() != nil:
		m := ev.GetMessageUpdated()
		prev := d.last[m.GetId()]
		d.remember(m)
		if a := appendOf(prev, m); a != nil {
			return &deckv1.Event{Kind: &deckv1.Event_MessageAppendText{MessageAppendText: a}}
		}
	}
	return nil
}

func (d *deltas) remember(m *deckv1.Message) {
	if m.GetFinished() {
		// Nothing streams into a finished message; a later edit goes
		// out whole.
		delete(d.last, m.GetId())
		return
	}
	if _, ok := d.last[m.GetId()]; !ok && len(d.last) >= maxTracked {
		clear(d.last)
	}
	d.last[m.GetId()] = m
}

// appendOf is next as text appended to one text or reasoning part of
// prev, or nil when next differs from prev in any other way.
func appendOf(prev, next *deckv1.Message) *deckv1.MessageAppendText {
	if prev == nil || len(prev.GetParts()) != len(next.GetParts()) {
		return nil
	}
	// Everything but the parts and updated_at must be the same, fields
	// added later included.
	if !proto.Equal(without(prev, "parts", "updated_at"), without(next, "parts", "updated_at")) {
		return nil
	}
	grown := -1
	for i, p := range next.GetParts() {
		q := prev.GetParts()[i]
		if proto.Equal(p, q) {
			continue
		}
		if grown >= 0 || (p.GetType() != "text" && p.GetType() != "reasoning") {
			return nil
		}
		if !proto.Equal(without(p, "text"), without(q, "text")) || !strings.HasPrefix(p.GetText(), q.GetText()) {
			return nil
		}
		grown = i
	}
	if grown < 0 {
		return nil
	}
	old := prev.GetParts()[grown].GetText()
	return &deckv1.MessageAppendText{
		Id: next.GetId(), SessionId: next.GetSessionId(), Part: uint32(grown),
		Offset: uint64(len(old)), Text: next.GetParts()[grown].GetText()[len(old):],
		UpdatedAt: next.GetUpdatedAt(),
	}
}

// without is a shallow copy of m with the named fields left out, for
// comparing the rest. It shares m's nested values, so it is read only.
func without[M proto.Message](m M, skip ...protoreflect.Name) proto.Message {
	src := m.ProtoReflect()
	dst := src.New()
	src.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if !slices.Contains(skip, fd.Name()) {
			dst.Set(fd, v)
		}
		return true
	})
	return dst.Interface()
}
