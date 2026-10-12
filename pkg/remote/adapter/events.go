package adapter

import (
	"context"
	"reflect"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dev-dami/deck/internal/message"
	"github.com/dev-dami/deck/internal/permission"
	"github.com/dev-dami/deck/internal/pubsub"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/workspace"
	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// coalesceEvery is how often streaming message updates are flushed: a
// token at a time would drown a phone on a cellular link.
const coalesceEvery = 200 * time.Millisecond

// Events merges the app's event stream and Deck's own changes into wire
// events, and watches the palette for theme.changed.
func (b *Backend) Events(ctx context.Context) <-chan remote.Event {
	out := make(chan remote.Event, 256)
	go func() {
		defer close(out)
		appCh := b.events(ctx)
		deckCh := b.rc.SubscribeDeck(ctx)
		dirty := map[string]message.Message{}
		tick := time.NewTicker(coalesceEvery)
		defer tick.Stop()
		themes := time.NewTicker(b.themePoll)
		defer themes.Stop()
		theme := b.rc.Theme()

		emit := func(e remote.Event) {
			select {
			case out <- e:
			case <-ctx.Done():
			}
		}
		flush := func() {
			for id, m := range dirty {
				delete(dirty, id)
				emit(remote.Event{Name: wire.EvMessageUpdated, SessionID: m.SessionID, Scoped: true, Payload: b.decorate(toMessage(m))})
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				flush()
			case <-themes.C:
				if t := b.rc.Theme(); !reflect.DeepEqual(t, theme) {
					theme = t
					emit(remote.Event{Name: wire.EvThemeChanged, Payload: toTheme(t)})
				}
			case ev, ok := <-appCh:
				if !ok {
					return
				}
				b.onApp(ev.Payload, dirty, emit)
			case ev, ok := <-deckCh:
				if !ok {
					return
				}
				b.onDeck(ev.Payload, emit)
			}
		}
	}()
	return out
}

// onApp turns one app event into wire events. Streaming deltas are
// batched in dirty; the first row, a finished row and anything a person
// wrote go out at once.
func (b *Backend) onApp(msg tea.Msg, dirty map[string]message.Message, emit func(remote.Event)) {
	switch ev := msg.(type) {
	case pubsub.Event[message.Message]:
		m := ev.Payload
		if ev.Type == pubsub.DeletedEvent {
			delete(dirty, m.ID)
			emit(remote.Event{Name: wire.EvMessageDeleted, SessionID: m.SessionID, Scoped: true, Payload: wire.MessageDeleted{ID: m.ID, SessionID: m.SessionID}})
			return
		}
		name := wire.EvMessageUpdated
		if ev.Type == pubsub.CreatedEvent {
			name = wire.EvMessageCreated
		}
		streaming := ev.Type == pubsub.UpdatedEvent && m.Role == message.Assistant && !m.IsFinished()
		if streaming {
			dirty[m.ID] = m
			return
		}
		delete(dirty, m.ID)
		emit(remote.Event{Name: name, SessionID: m.SessionID, Scoped: true, Payload: b.decorate(toMessage(m))})
	case pubsub.Event[session.Session]:
		s := ev.Payload
		if s.ParentSessionID != "" {
			return
		}
		name := wire.EvSessionUpdated
		switch ev.Type {
		case pubsub.CreatedEvent:
			name = wire.EvSessionCreated
		case pubsub.DeletedEvent:
			name = wire.EvSessionDeleted
		}
		emit(remote.Event{Name: name, SessionID: s.ID, Payload: b.wireSession(s)})
	case pubsub.Event[permission.PermissionRequest]:
		r := ev.Payload
		at := time.Now()
		b.mu.Lock()
		b.waiting[r.ID] = pending{req: r, at: at}
		b.mu.Unlock()
		emit(remote.Event{Name: wire.EvApprovalRequested, SessionID: r.SessionID, Payload: toApproval(r, at, b.core.WorkingDir())})
		b.emitSessionState(r.SessionID, emit)
	case pubsub.Event[permission.PermissionNotification]:
		n := ev.Payload
		b.mu.Lock()
		var gone []pending
		for id, p := range b.waiting {
			if p.req.ToolCallID == n.ToolCallID {
				gone = append(gone, p)
				delete(b.waiting, id) // answered: out of the waiting set at once (R14)
			}
		}
		b.mu.Unlock()
		for _, p := range gone {
			emit(remote.Event{Name: wire.EvApprovalResolved, SessionID: p.req.SessionID, Payload: wire.ApprovalResolved{ID: p.req.ID, SessionID: p.req.SessionID, Granted: n.Granted}})
			b.emitSessionState(p.req.SessionID, emit)
		}
	}
}

// emitSessionState refreshes a session's summary after its pending count
// or busy state moved.
func (b *Backend) emitSessionState(sessionID string, emit func(remote.Event)) {
	s, err := b.core.GetSession(context.Background(), sessionID)
	if err != nil {
		return
	}
	emit(remote.Event{Name: wire.EvSessionUpdated, SessionID: sessionID, Payload: b.wireSession(s)})
}

func (b *Backend) onDeck(c workspace.DeckChange, emit func(remote.Event)) {
	switch c.Kind {
	case workspace.ChangeBusy:
		emit(remote.Event{Name: wire.EvBusy, SessionID: c.SessionID, Payload: wire.BusyChanged{SessionID: c.SessionID, Busy: c.Busy}})
		b.emitSessionState(c.SessionID, emit)
	case workspace.ChangeQueue:
		emit(remote.Event{Name: wire.EvQueueChanged, SessionID: c.SessionID, Scoped: true, Payload: wire.QueueChanged{SessionID: c.SessionID, Items: toQueue(c.Queue)}})
	case workspace.ChangeMode:
		emit(remote.Event{Name: wire.EvModeChanged, SessionID: c.SessionID, Payload: wire.ModeChanged{SessionID: c.SessionID, Mode: c.Mode}})
		b.emitSessionState(c.SessionID, emit)
	case workspace.ChangeRoster:
		emit(remote.Event{Name: wire.EvRosterChanged, SessionID: c.SessionID, Payload: wire.RosterChanged{SessionID: c.SessionID}})
	case workspace.ChangeNotice:
		emit(remote.Event{Name: wire.EvNotice, SessionID: c.SessionID, Scoped: true, Payload: wire.Notice{SessionID: c.SessionID, Text: c.Text}})
	}
}
