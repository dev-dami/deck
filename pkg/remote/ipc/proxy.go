package ipc

import (
	"context"
	"encoding/json"
	"io"
	"sync"

	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// Proxy is a remote.Backend backed by a provider on the other end of a
// connection. When the connection ends every call returns ErrOffline and
// every Events channel closes; the daemon then marks the project offline.
type Proxy struct {
	w    *writer
	done chan struct{}

	mu      sync.Mutex
	next    uint64
	pending map[uint64]chan frame
	subs    map[*subscriber]struct{}
	info    remote.Info
	ui      bool
	closed  bool
}

type subscriber struct {
	ch  chan remote.Event
	ctx context.Context
}

// NewProxy starts reading conn and returns a Backend for the provider behind
// it. Close conn to end it; Done reports when it has.
func NewProxy(conn io.ReadWriter) (*Proxy, error) {
	p := &Proxy{
		w:       &writer{w: conn},
		done:    make(chan struct{}),
		pending: map[uint64]chan frame{},
		subs:    map[*subscriber]struct{}{},
	}
	go p.read(conn)

	ctx, cancel := context.WithTimeout(context.Background(), helloTimeout)
	defer cancel()
	var ib infoBody
	if err := p.call(ctx, "info", args{}, &ib); err != nil {
		return nil, err
	}
	p.info = remote.Info{Version: ib.Version, Project: ib.Project, Kinds: ib.Kinds, Theme: ib.Theme}
	p.ui = ib.UI
	return p, nil
}

// Done is closed when the provider's connection ends.
func (p *Proxy) Done() <-chan struct{} { return p.done }

func (p *Proxy) read(conn io.Reader) {
	sc := scanner(conn)
	for sc.Scan() {
		var f frame
		if json.Unmarshal(sc.Bytes(), &f) != nil {
			continue
		}
		switch f.T {
		case "ret":
			p.mu.Lock()
			ch := p.pending[f.ID]
			delete(p.pending, f.ID)
			p.mu.Unlock()
			if ch != nil {
				ch <- f
			}
		case "ev":
			if f.Ev != nil {
				p.publish(remote.Event{Name: f.Ev.Name, SessionID: f.Ev.SessionID, Scoped: f.Ev.Scoped, Payload: f.Ev.Payload})
			}
		}
	}
	p.mu.Lock()
	p.closed = true
	for id, ch := range p.pending {
		close(ch)
		delete(p.pending, id)
	}
	for s := range p.subs {
		close(s.ch)
		delete(p.subs, s)
	}
	p.mu.Unlock()
	close(p.done)
}

func (p *Proxy) publish(ev remote.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for s := range p.subs {
		if s.ctx.Err() != nil {
			close(s.ch)
			delete(p.subs, s)
			continue
		}
		select {
		case s.ch <- ev:
		default:
			// A consumer that cannot keep up loses its stream rather than
			// stalling the provider; the hub resyncs the phone.
			close(s.ch)
			delete(p.subs, s)
		}
	}
}

func (p *Proxy) call(ctx context.Context, m string, a args, out any) error {
	ch := make(chan frame, 1)
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrOffline
	}
	p.next++
	id := p.next
	p.pending[id] = ch
	p.mu.Unlock()

	forget := func() {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
	}
	if err := p.w.send(frame{T: "call", ID: id, M: m, A: &a}); err != nil {
		forget()
		return ErrOffline
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	select {
	case f, ok := <-ch:
		if !ok {
			return ErrOffline
		}
		if f.E != nil {
			return fromErrBody(f.E)
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(f.R, out)
	case <-ctx.Done():
		forget()
		return ctx.Err()
	}
}

// HasUI reports whether a person is looking at the provider (a terminal UI).
func (p *Proxy) HasUI() bool { return p.ui }

// Notify shows text to the person at the provider. It fails when the
// provider is gone; it does nothing on one without a screen.
func (p *Proxy) Notify(text string) error {
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return ErrOffline
	}
	if !p.ui {
		return nil
	}
	return p.w.send(frame{T: "notice", N: text})
}

// Info returns what the provider reported when it connected.
func (p *Proxy) Info() remote.Info { return p.info }

func (p *Proxy) Sessions(ctx context.Context) ([]wire.Session, error) {
	var out []wire.Session
	return out, p.call(ctx, "sessions", args{}, &out)
}

func (p *Proxy) CreateSession(ctx context.Context, title string) (wire.Session, error) {
	var out wire.Session
	return out, p.call(ctx, "create_session", args{Title: title}, &out)
}

func (p *Proxy) RenameSession(ctx context.Context, id, title string) error {
	return p.call(ctx, "rename_session", args{ID: id, Title: title}, nil)
}

func (p *Proxy) DeleteSession(ctx context.Context, id string) error {
	return p.call(ctx, "delete_session", args{ID: id}, nil)
}

func (p *Proxy) Messages(ctx context.Context, sessionID, before string, limit int) ([]wire.Message, bool, string, error) {
	var out messagesBody
	err := p.call(ctx, "messages", args{SessionID: sessionID, Before: before, Limit: limit}, &out)
	return out.Messages, out.More, out.Next, err
}

func (p *Proxy) Send(ctx context.Context, sessionID, text string, images []remote.Image) (string, error) {
	imgs := make([]image, len(images))
	for i, im := range images {
		imgs[i] = image{Name: im.Name, Mime: im.Mime, Data: im.Data}
	}
	var id string
	return id, p.call(ctx, "send", args{SessionID: sessionID, Text: text, Images: imgs}, &id)
}

func (p *Proxy) EditMessage(ctx context.Context, sessionID, messageID, text string) error {
	return p.call(ctx, "edit", args{SessionID: sessionID, MessageID: messageID, Text: text}, nil)
}

func (p *Proxy) Cancel(sessionID string) error {
	return p.call(context.Background(), "cancel", args{SessionID: sessionID}, nil)
}

func (p *Proxy) Interrupt(sessionID, agent string) error {
	return p.call(context.Background(), "interrupt", args{SessionID: sessionID, Agent: agent}, nil)
}

func (p *Proxy) Continue(sessionID string) error {
	return p.call(context.Background(), "continue", args{SessionID: sessionID}, nil)
}

func (p *Proxy) Queue(sessionID string) ([]wire.QueuedItem, error) {
	var out []wire.QueuedItem
	return out, p.call(context.Background(), "queue", args{SessionID: sessionID}, &out)
}

func (p *Proxy) Steer(sessionID, itemID string) error {
	return p.call(context.Background(), "steer", args{SessionID: sessionID, ID: itemID}, nil)
}

func (p *Proxy) CancelQueued(sessionID, itemID string) error {
	return p.call(context.Background(), "cancel_queued", args{SessionID: sessionID, ID: itemID}, nil)
}

func (p *Proxy) Roster(sessionID string) ([]wire.AgentLine, error) {
	var out []wire.AgentLine
	return out, p.call(context.Background(), "roster", args{SessionID: sessionID}, &out)
}

func (p *Proxy) AgentKinds(sessionID string) ([]wire.AgentKind, error) {
	var out []wire.AgentKind
	return out, p.call(context.Background(), "agent_kinds", args{SessionID: sessionID}, &out)
}

func (p *Proxy) AddAgent(sessionID, kind, model string) (wire.AgentLine, error) {
	var out wire.AgentLine
	return out, p.call(context.Background(), "add_agent", args{SessionID: sessionID, Kind: kind, Model: model}, &out)
}

func (p *Proxy) Models(ctx context.Context, sessionID, kind string) ([]wire.ModelChoice, error) {
	var out []wire.ModelChoice
	return out, p.call(ctx, "models", args{SessionID: sessionID, Kind: kind}, &out)
}

func (p *Proxy) SetModel(sessionID, kind, model, effort string) error {
	return p.call(context.Background(), "set_model", args{SessionID: sessionID, Kind: kind, Model: model, Effort: effort}, nil)
}

func (p *Proxy) Mode(sessionID string) (string, error) {
	var out string
	return out, p.call(context.Background(), "mode", args{SessionID: sessionID}, &out)
}

func (p *Proxy) SetMode(sessionID, mode string) error {
	return p.call(context.Background(), "set_mode", args{SessionID: sessionID, Mode: mode}, nil)
}

func (p *Proxy) Approvals(sessionID string) ([]wire.Approval, error) {
	var out []wire.Approval
	return out, p.call(context.Background(), "approvals", args{SessionID: sessionID}, &out)
}

func (p *Proxy) Answer(sessionID, id, decision string, answers []wire.QuestionAnswer) (bool, error) {
	var out bool
	return out, p.call(context.Background(), "answer", args{SessionID: sessionID, ID: id, Decision: decision, Answers: answers}, &out)
}

func (p *Proxy) Changes(ctx context.Context, sessionID string) ([]wire.ChangedFile, error) {
	var out []wire.ChangedFile
	return out, p.call(ctx, "changes", args{SessionID: sessionID}, &out)
}

// Events returns a stream that closes when ctx ends or the provider goes.
func (p *Proxy) Events(ctx context.Context) <-chan remote.Event {
	s := &subscriber{ch: make(chan remote.Event, eventBuffer), ctx: ctx}
	p.mu.Lock()
	if p.closed {
		close(s.ch)
	} else {
		p.subs[s] = struct{}{}
	}
	p.mu.Unlock()
	return s.ch
}

var _ remote.Backend = (*Proxy)(nil)
