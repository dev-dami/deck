// Package remotetest is a fake remote.Backend for the server's tests: an
// in-memory Deck with sessions, messages, approvals and a queue, every id
// checked against its session (R10), and an event injector.
package remotetest

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// Sent is one chat.send the fake received.
type Sent struct {
	SessionID string
	Text      string
	Images    []remote.Image
}

// Session is the fake's state for one session.
type Session struct {
	Session   wire.Session
	Messages  []wire.Message
	Queue     []wire.QueuedItem
	Approvals []wire.Approval
	Roster    []wire.AgentLine
	Mode      string
}

// Fake is the fake Backend. Its exported fields may be set before the
// server starts; after that use the methods.
type Fake struct {
	Info_ remote.Info

	mu       sync.Mutex
	sessions map[string]*Session
	order    []string
	events   chan remote.Event
	sent     []Sent
	answered map[string]string
	modes    []string
	edits    []string
	calls    []string
	next     int
	SendErr  error
}

// New returns a fake with one session "s1" and the given info.
func New() *Fake {
	f := &Fake{
		Info_: remote.Info{
			Version: "test", Project: "proj", Kinds: []string{"claude", "codex"},
			Theme: wire.Theme{Palette: map[string]string{"primary": "#5fd7af"}, Kinds: map[string]wire.KindTone{"claude": {Tint: "#d97757", Mark: "✻"}}},
		},
		sessions: map[string]*Session{},
		events:   make(chan remote.Event, 256),
		answered: map[string]string{},
	}
	f.AddSession("s1", "One")
	return f
}

// AddSession adds a session in mode ask.
func (f *Fake) AddSession(id, title string) *Session {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := &Session{Session: wire.Session{ID: id, Title: title, Mode: wire.ModeAsk, UpdatedAt: time.Unix(1760000000, 0).UTC()}, Mode: wire.ModeAsk}
	f.sessions[id] = s
	f.order = append(f.order, id)
	return s
}

// Get returns a session's state for inspection or setup.
func (f *Fake) Get(id string) *Session {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessions[id]
}

// Inject delivers an event to the server as if Deck raised it.
func (f *Fake) Inject(ev remote.Event) { f.events <- ev }

// Sent returns what chat.send delivered so far.
func (f *Fake) Sent() []Sent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Sent(nil), f.sent...)
}

// Answered maps approval ids to the decision taken.
func (f *Fake) Answered() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]string{}
	for k, v := range f.answered {
		out[k] = v
	}
	return out
}

// Calls lists the mutating calls made, as "method:arg".
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *Fake) note(s string) { f.calls = append(f.calls, s) }

func (f *Fake) session(id string) (*Session, error) {
	s, ok := f.sessions[id]
	if !ok {
		return nil, remote.ErrNotFound
	}
	return s, nil
}

func (f *Fake) Info() remote.Info { return f.Info_ }

func (f *Fake) Sessions(context.Context) ([]wire.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]wire.Session, 0, len(f.order))
	for _, id := range f.order {
		if s, ok := f.sessions[id]; ok {
			out = append(out, s.Session)
		}
	}
	return out, nil
}

func (f *Fake) CreateSession(_ context.Context, title string) (wire.Session, error) {
	f.mu.Lock()
	f.next++
	id := "new" + strconv.Itoa(f.next)
	f.mu.Unlock()
	return f.AddSession(id, title).Session, nil
}

func (f *Fake) RenameSession(_ context.Context, id, title string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, err := f.session(id)
	if err != nil {
		return err
	}
	s.Session.Title = title
	f.note("rename:" + id)
	return nil
}

func (f *Fake) DeleteSession(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.session(id); err != nil {
		return err
	}
	delete(f.sessions, id)
	f.note("delete:" + id)
	return nil
}

// Messages pages from the end: the cursor is the index of the oldest row
// of the previous page.
func (f *Fake) Messages(_ context.Context, sessionID, before string, limit int) ([]wire.Message, bool, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, err := f.session(sessionID)
	if err != nil {
		return nil, false, "", err
	}
	end := len(s.Messages)
	if before != "" {
		n, err := strconv.Atoi(before)
		if err != nil || n < 0 || n > end {
			return nil, false, "", remote.ErrInvalid
		}
		end = n
	}
	start := end - limit
	if start < 0 {
		start = 0
	}
	page := append([]wire.Message(nil), s.Messages[start:end]...)
	if start == 0 {
		return page, false, "", nil
	}
	return page, true, strconv.Itoa(start), nil
}

func (f *Fake) Send(_ context.Context, sessionID, text string, images []remote.Image) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, err := f.session(sessionID)
	if err != nil {
		return "", err
	}
	if f.SendErr != nil {
		return "", f.SendErr
	}
	f.sent = append(f.sent, Sent{SessionID: sessionID, Text: text, Images: images})
	id := fmt.Sprintf("m%d", len(f.sent))
	s.Messages = append(s.Messages, wire.Message{ID: id, SessionID: sessionID, Role: wire.RoleUser, Finished: true, Parts: []wire.Part{{Type: wire.PartText, Text: text}}})
	return id, nil
}

func (f *Fake) EditMessage(_ context.Context, sessionID, messageID, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, err := f.session(sessionID)
	if err != nil {
		return err
	}
	for i := range s.Messages {
		if s.Messages[i].ID == messageID {
			if !s.Messages[i].Editable {
				return remote.ErrInvalid
			}
			s.Messages[i].Parts = []wire.Part{{Type: wire.PartText, Text: text}}
			s.Messages[i].Edited = true
			f.edits = append(f.edits, messageID)
			return nil
		}
	}
	return remote.ErrNotFound
}

func (f *Fake) Cancel(sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.session(sessionID); err != nil {
		return err
	}
	f.note("cancel:" + sessionID)
	return nil
}

func (f *Fake) Interrupt(sessionID, agent string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, err := f.session(sessionID)
	if err != nil {
		return err
	}
	for _, a := range s.Roster {
		if a.ID == agent {
			f.note("interrupt:" + agent)
			return nil
		}
	}
	return remote.ErrNotFound
}

func (f *Fake) Continue(sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.session(sessionID); err != nil {
		return err
	}
	f.note("continue:" + sessionID)
	return nil
}

func (f *Fake) Queue(sessionID string) ([]wire.QueuedItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, err := f.session(sessionID)
	if err != nil {
		return nil, err
	}
	return append([]wire.QueuedItem(nil), s.Queue...), nil
}

func (f *Fake) queued(sessionID, itemID string) (*Session, int, error) {
	s, err := f.session(sessionID)
	if err != nil {
		return nil, 0, err
	}
	for i, q := range s.Queue {
		if q.ID == itemID {
			return s, i, nil
		}
	}
	return nil, 0, remote.ErrNotFound
}

func (f *Fake) Steer(sessionID, itemID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, _, err := f.queued(sessionID, itemID); err != nil {
		return err
	}
	f.note("steer:" + itemID)
	return nil
}

func (f *Fake) CancelQueued(sessionID, itemID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, i, err := f.queued(sessionID, itemID)
	if err != nil {
		return err
	}
	s.Queue = append(s.Queue[:i], s.Queue[i+1:]...)
	f.note("cancelqueued:" + itemID)
	return nil
}

func (f *Fake) Roster(sessionID string) ([]wire.AgentLine, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, err := f.session(sessionID)
	if err != nil {
		return nil, err
	}
	return append([]wire.AgentLine(nil), s.Roster...), nil
}

// fakeKinds are the agent CLIs the fake pretends are installed.
var fakeKinds = []wire.AgentKind{{Kind: "claude", Model: "claude-big"}, {Kind: "codex", Model: "codex-big"}}

func (f *Fake) AgentKinds(sessionID string) ([]wire.AgentKind, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, err := f.session(sessionID)
	if err != nil {
		return nil, err
	}
	out := append([]wire.AgentKind(nil), fakeKinds...)
	for i := range out {
		for _, l := range s.Roster {
			if l.Kind == out[i].Kind {
				out[i].Joined = true
			}
		}
	}
	return out, nil
}

// StartRace implements remote.Racer: it notes "race:<agent>/<model>,…"
// and answers task "t1". An entrant of an unknown kind is refused.
func (f *Fake) StartRace(_ context.Context, sessionID, brief string, entrants []wire.RaceEntrant, judge bool) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.session(sessionID); err != nil {
		return "", err
	}
	if len(entrants) < 2 {
		return "", &wire.Error{Code: wire.CodeInvalid, Msg: "a race has 2 to 6 entries, not 1"}
	}
	var who []string
	for _, e := range entrants {
		known := false
		for _, k := range fakeKinds {
			known = known || k.Kind == e.Agent
		}
		if !known {
			return "", remote.ErrInvalid
		}
		who = append(who, e.Agent+"/"+e.Model)
	}
	f.note("race:" + strings.Join(who, ",") + map[bool]string{true: " judged"}[judge] + ": " + brief)
	return "t1", nil
}

func (f *Fake) AddAgent(sessionID, kind, model string) (wire.AgentLine, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, err := f.session(sessionID)
	if err != nil {
		return wire.AgentLine{}, err
	}
	var known *wire.AgentKind
	for i := range fakeKinds {
		if fakeKinds[i].Kind == kind {
			known = &fakeKinds[i]
		}
	}
	if known == nil {
		return wire.AgentLine{}, remote.ErrInvalid
	}
	if model == "" {
		model = known.Model
	}
	name := kind
	for n := 2; f.hasAgent(s, name); n++ {
		name = fmt.Sprintf("%s%d", kind, n)
	}
	line := wire.AgentLine{ID: name, Name: name, Kind: kind, Model: model}
	s.Roster = append(s.Roster, line)
	f.note("add:" + name + "=" + model)
	return line, nil
}

func (f *Fake) hasAgent(s *Session, name string) bool {
	for _, l := range s.Roster {
		if l.ID == name {
			return true
		}
	}
	return false
}

func (f *Fake) Models(_ context.Context, sessionID, kind string) ([]wire.ModelChoice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if sessionID == "" {
		return []wire.ModelChoice{{ID: kind + "-big", Label: "Big", Efforts: []string{"low", "high"}}}, nil
	}
	if _, err := f.session(sessionID); err != nil {
		return nil, err
	}
	return []wire.ModelChoice{{ID: kind + "-big", Label: "Big", Efforts: []string{"low", "high"}, Selected: true}}, nil
}

// RefreshModels implements remote.ModelRefresher.
func (f *Fake) RefreshModels() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("models:refresh")
}

func (f *Fake) SetModel(sessionID, kind, model, effort string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.session(sessionID); err != nil {
		return err
	}
	f.note("model:" + kind + "=" + model + "/" + effort)
	return nil
}

func (f *Fake) Mode(sessionID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, err := f.session(sessionID)
	if err != nil {
		return "", err
	}
	return s.Mode, nil
}

func (f *Fake) SetMode(sessionID, mode string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, err := f.session(sessionID)
	if err != nil {
		return err
	}
	s.Mode, s.Session.Mode = mode, mode
	f.modes = append(f.modes, sessionID+"="+mode)
	return nil
}

// Modes lists every mode.set applied, as "session=mode".
func (f *Fake) Modes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.modes...)
}

func (f *Fake) Approvals(sessionID string) ([]wire.Approval, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []wire.Approval
	if sessionID != "" {
		s, err := f.session(sessionID)
		if err != nil {
			return nil, err
		}
		return append(out, s.Approvals...), nil
	}
	for _, id := range f.order {
		if s, ok := f.sessions[id]; ok {
			out = append(out, s.Approvals...)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (f *Fake) Answer(sessionID, id, decision string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, err := f.session(sessionID)
	if err != nil {
		return false, err
	}
	for i, a := range s.Approvals {
		if a.ID == id {
			s.Approvals = append(s.Approvals[:i], s.Approvals[i+1:]...)
			f.answered[id] = decision
			return true, nil
		}
	}
	return false, remote.ErrNotFound
}

func (f *Fake) Changes(_ context.Context, sessionID string) ([]wire.ChangedFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.session(sessionID); err != nil {
		return nil, err
	}
	return []wire.ChangedFile{{Path: "a.go", Added: 1, Diff: "+x\n"}}, nil
}

func (f *Fake) Events(ctx context.Context) <-chan remote.Event {
	out := make(chan remote.Event)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-f.events:
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

var _ remote.Backend = (*Fake)(nil)
