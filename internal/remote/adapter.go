package remote

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dev-dami/deck/internal/app"
	deckagent "github.com/dev-dami/deck/internal/deck/agent"
	decks "github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/message"
	"github.com/dev-dami/deck/internal/permission"
	"github.com/dev-dami/deck/internal/pubsub"
	"github.com/dev-dami/deck/internal/remote/wire"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/workspace"
)

// coalesceEvery is how often streaming message updates are flushed: a token at
// a time would drown a phone on a cellular link.
const coalesceEvery = 200 * time.Millisecond

// appSource is how the adapter reaches the app's event stream; *DeckWorkspace
// has it through its embedded AppWorkspace.
type appSource interface{ App() *app.App }

type pending struct {
	req permission.PermissionRequest
	at  int64
}

// WorkspaceBackend serves a Deck workspace to the remote server. It must sit
// on the workspace, not on the session manager: the manager has one reader.
type WorkspaceBackend struct {
	ws     workspace.Workspace
	rc     workspace.RemoteControl
	picker workspace.AgentPicker
	roster workspace.Roster
	app    *app.App
	info   Info

	mu      sync.Mutex
	waiting map[string]pending // approvals by permission request id
}

// NewWorkspaceBackend wraps ws, which must be the Deck workspace (it needs
// the remote-control capabilities).
func NewWorkspaceBackend(ws workspace.Workspace, info Info) (*WorkspaceBackend, error) {
	rc, ok := ws.(workspace.RemoteControl)
	if !ok {
		return nil, fmt.Errorf("remote control needs the Deck workspace, got %T", ws)
	}
	src, ok := ws.(appSource)
	if !ok {
		return nil, fmt.Errorf("workspace %T does not expose its app", ws)
	}
	b := &WorkspaceBackend{ws: ws, rc: rc, app: src.App(), info: info, waiting: map[string]pending{}}
	b.picker, _ = ws.(workspace.AgentPicker)
	b.roster, _ = ws.(workspace.Roster)
	if len(b.info.Kinds) == 0 && b.roster != nil {
		// With no session, the roster lists the installed agent CLIs.
		for _, l := range b.roster.Roster("") {
			b.info.Kinds = append(b.info.Kinds, l.Kind)
		}
	}
	return b, nil
}

func (b *WorkspaceBackend) Info() Info { return b.info }

func (b *WorkspaceBackend) pendingFor(sessionID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, p := range b.waiting {
		if p.req.SessionID == sessionID {
			n++
		}
	}
	return n
}

func (b *WorkspaceBackend) wireSession(s session.Session) wire.Session {
	return toSession(s, b.ws.AgentIsSessionBusy(s.ID), b.pendingFor(s.ID))
}

func (b *WorkspaceBackend) Sessions(ctx context.Context) ([]wire.Session, error) {
	all, err := b.ws.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]wire.Session, 0, len(all))
	for _, s := range all {
		if s.ParentSessionID != "" {
			continue // task sessions belong to a tool call, not the list
		}
		out = append(out, b.wireSession(s))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out, nil
}

func (b *WorkspaceBackend) CreateSession(ctx context.Context, title string) (wire.Session, error) {
	if strings.TrimSpace(title) == "" {
		title = "New Session"
	}
	s, err := b.ws.CreateSession(ctx, title)
	if err != nil {
		return wire.Session{}, err
	}
	return b.wireSession(s), nil
}

func (b *WorkspaceBackend) RenameSession(ctx context.Context, id, title string) error {
	s, err := b.ws.GetSession(ctx, id)
	if err != nil {
		return ErrNotFound
	}
	s.Title = title
	_, err = b.ws.SaveSession(ctx, s)
	return err
}

func (b *WorkspaceBackend) DeleteSession(ctx context.Context, id string) error {
	if _, err := b.ws.GetSession(ctx, id); err != nil {
		return ErrNotFound
	}
	return b.ws.DeleteSession(ctx, id)
}

func (b *WorkspaceBackend) Messages(ctx context.Context, sessionID string) ([]wire.Message, error) {
	if _, err := b.ws.GetSession(ctx, sessionID); err != nil {
		return nil, ErrNotFound
	}
	rows, err := b.ws.ListMessages(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].CreatedAt < rows[j].CreatedAt })
	out := make([]wire.Message, 0, len(rows))
	for _, m := range rows {
		out = append(out, b.decorate(toMessage(m)))
	}
	return out, nil
}

// decorate adds what only the workspace knows to a converted row.
func (b *WorkspaceBackend) decorate(m wire.Message) wire.Message {
	if m.Role == wire.RoleUser {
		m.Editable = b.rc.EditableMessage(m.ID)
		m.Edited = b.rc.EditedMessage(m.ID)
	}
	return m
}

func (b *WorkspaceBackend) EditMessage(ctx context.Context, sessionID, messageID, text string) error {
	err := b.rc.EditMessage(ctx, sessionID, messageID, text)
	if errors.Is(err, workspace.ErrNotEditable) {
		return fmt.Errorf("%w: %s", ErrInvalid, err)
	}
	return err
}

func (b *WorkspaceBackend) Send(ctx context.Context, sessionID, text string, images []Image) error {
	if _, err := b.ws.GetSession(ctx, sessionID); err != nil {
		return ErrNotFound
	}
	atts := make([]message.Attachment, 0, len(images))
	for _, im := range images {
		atts = append(atts, message.Attachment{FileName: im.Name, MimeType: im.Mime, Content: im.Data})
	}
	return b.ws.AgentRun(ctx, sessionID, text, atts...)
}

func (b *WorkspaceBackend) Cancel(sessionID string)           { b.ws.AgentCancel(sessionID) }
func (b *WorkspaceBackend) Interrupt(sid, agent string) error { return b.rc.InterruptAgent(sid, agent) }
func (b *WorkspaceBackend) Continue(sessionID string) error   { return b.rc.ContinueThread(sessionID) }
func (b *WorkspaceBackend) Steer(itemID string) error         { return b.rc.SteerQueued(itemID) }
func (b *WorkspaceBackend) CancelQueued(itemID string) error  { return b.rc.CancelQueued(itemID) }
func (b *WorkspaceBackend) Mode() string                      { return b.rc.RemoteMode() }
func (b *WorkspaceBackend) SetMode(mode string) error         { return b.rc.SetRemoteMode(mode) }

func toQueue(items []decks.QueuedItem) []wire.QueuedItem {
	out := make([]wire.QueuedItem, 0, len(items))
	for _, q := range items {
		out = append(out, wire.QueuedItem{ID: q.ID, Agent: q.AgentID, Text: q.Text, At: q.At.Unix()})
	}
	return out
}

func (b *WorkspaceBackend) Queue(sessionID string) []wire.QueuedItem {
	return toQueue(b.rc.QueueItems(sessionID))
}

func busyStatus(s string) bool { return s == "working" || s == "tool" || s == "approval" }

func (b *WorkspaceBackend) Roster(sessionID string) []wire.AgentLine {
	if b.roster == nil {
		return nil
	}
	lines := b.roster.Roster(sessionID)
	out := make([]wire.AgentLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, wire.AgentLine{Name: l.ID, Kind: l.Kind, Model: l.Model, Effort: l.Effort, Busy: busyStatus(l.Status)})
	}
	return out
}

func (b *WorkspaceBackend) Models(ctx context.Context, sessionID, kind string) ([]wire.ModelChoice, error) {
	if b.picker == nil {
		return nil, nil
	}
	choices, err := b.picker.AgentModelChoices(ctx, sessionID)
	if err != nil {
		return nil, err
	}
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
	return out, nil
}

func (b *WorkspaceBackend) SetModel(sessionID, kind, model, effort string) error {
	if b.picker == nil {
		return fmt.Errorf("model switching is not available")
	}
	return b.picker.SetAgentModel(sessionID, kind, model, effort)
}

func (b *WorkspaceBackend) Approvals() []wire.Approval {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]wire.Approval, 0, len(b.waiting))
	for _, p := range b.waiting {
		out = append(out, toApproval(p.req, p.at))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

func (b *WorkspaceBackend) Answer(id, decision string) (bool, error) {
	b.mu.Lock()
	p, ok := b.waiting[id]
	b.mu.Unlock()
	if !ok {
		return false, ErrNotFound
	}
	var resolved bool
	switch decision {
	case wire.DecisionAllow:
		resolved = b.ws.PermissionGrant(p.req)
	case wire.DecisionAllowSession:
		resolved = b.ws.PermissionGrantPersistent(p.req)
	case wire.DecisionDeny:
		resolved = b.ws.PermissionDeny(p.req)
	default:
		return false, fmt.Errorf("unknown decision %q", decision)
	}
	return resolved, nil
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

func (b *WorkspaceBackend) Changes(ctx context.Context, sessionID string) ([]wire.ChangedFile, error) {
	cs, err := b.rc.ChangedFiles(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]wire.ChangedFile, 0, len(cs.Files))
	for _, f := range cs.Files {
		cf := wire.ChangedFile{Path: f.Path, Added: f.Added, Removed: f.Removed}
		if !f.Binary {
			cf.Diff, cf.Truncated = truncate(unified(f.FileDiff), wire.MaxDiffBytes)
		}
		out = append(out, cf)
	}
	return out, nil
}

// Events merges the app's event stream and Deck's own changes into wire events.
func (b *WorkspaceBackend) Events(ctx context.Context) <-chan Event {
	out := make(chan Event, 256)
	go func() {
		defer close(out)
		appCh := b.app.Events(ctx)
		deckCh := b.rc.SubscribeDeck(ctx)
		dirty := map[string]message.Message{}
		tick := time.NewTicker(coalesceEvery)
		defer tick.Stop()

		emit := func(e Event) {
			select {
			case out <- e:
			case <-ctx.Done():
			}
		}
		flush := func() {
			for id, m := range dirty {
				delete(dirty, id)
				emit(Event{Name: wire.EvMessageUpdated, SessionID: m.SessionID, Scoped: true, Payload: b.decorate(toMessage(m))})
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				flush()
			case ev, ok := <-appCh:
				if !ok {
					return
				}
				b.onApp(ev, dirty, emit)
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

func (b *WorkspaceBackend) onApp(msg tea.Msg, dirty map[string]message.Message, emit func(Event)) {
	switch ev := msg.(type) {
	case pubsub.Event[message.Message]:
		m := ev.Payload
		if ev.Type == pubsub.DeletedEvent {
			delete(dirty, m.ID)
			emit(Event{Name: wire.EvMessageDeleted, SessionID: m.SessionID, Scoped: true, Payload: map[string]string{"id": m.ID, "session_id": m.SessionID}})
			return
		}
		name := wire.EvMessageUpdated
		if ev.Type == pubsub.CreatedEvent {
			name = wire.EvMessageCreated
		}
		// Streaming deltas are batched; the first row, a finished row and
		// anything a person wrote go out at once.
		streaming := ev.Type == pubsub.UpdatedEvent && m.Role == message.Assistant && !m.IsFinished()
		if streaming {
			dirty[m.ID] = m
			return
		}
		delete(dirty, m.ID)
		emit(Event{Name: name, SessionID: m.SessionID, Scoped: true, Payload: b.decorate(toMessage(m))})
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
		emit(Event{Name: name, SessionID: s.ID, Payload: b.wireSession(s)})
	case pubsub.Event[permission.PermissionRequest]:
		r := ev.Payload
		at := time.Now().Unix()
		b.mu.Lock()
		b.waiting[r.ID] = pending{req: r, at: at}
		b.mu.Unlock()
		emit(Event{Name: wire.EvApprovalRequested, SessionID: r.SessionID, Payload: toApproval(r, at)})
		b.emitSessionState(r.SessionID, emit)
	case pubsub.Event[permission.PermissionNotification]:
		n := ev.Payload
		b.mu.Lock()
		var gone []pending
		for id, p := range b.waiting {
			if p.req.ToolCallID == n.ToolCallID {
				gone = append(gone, p)
				delete(b.waiting, id)
			}
		}
		b.mu.Unlock()
		for _, p := range gone {
			emit(Event{Name: wire.EvApprovalResolved, SessionID: p.req.SessionID, Payload: map[string]any{"id": p.req.ID, "granted": n.Granted}})
			b.emitSessionState(p.req.SessionID, emit)
		}
	}
}

// emitSessionState refreshes a session's summary after its pending count moved.
func (b *WorkspaceBackend) emitSessionState(sessionID string, emit func(Event)) {
	s, err := b.ws.GetSession(context.Background(), sessionID)
	if err != nil {
		return
	}
	emit(Event{Name: wire.EvSessionUpdated, SessionID: sessionID, Payload: b.wireSession(s)})
}

func (b *WorkspaceBackend) onDeck(c workspace.DeckChange, emit func(Event)) {
	switch c.Kind {
	case workspace.ChangeBusy:
		emit(Event{Name: wire.EvBusy, SessionID: c.SessionID, Payload: map[string]any{"session_id": c.SessionID, "busy": c.Busy}})
		b.emitSessionState(c.SessionID, emit)
	case workspace.ChangeQueue:
		emit(Event{Name: wire.EvQueueChanged, SessionID: c.SessionID, Payload: map[string]any{"session_id": c.SessionID, "items": toQueue(c.Queue)}})
	case workspace.ChangeMode:
		emit(Event{Name: wire.EvModeChanged, Payload: wire.ModeBody{Mode: c.Mode}})
	case workspace.ChangeRoster:
		emit(Event{Name: wire.EvRosterChanged, SessionID: c.SessionID, Payload: map[string]string{"session_id": c.SessionID}})
	case workspace.ChangeNotice:
		emit(Event{Name: wire.EvNotice, SessionID: c.SessionID, Payload: map[string]string{"session_id": c.SessionID, "text": c.Text}})
	}
}
