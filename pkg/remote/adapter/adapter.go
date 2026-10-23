// Package adapter is remote.Backend over internal/workspace: the only
// part of pkg/remote that knows Deck's own types. It converts sessions,
// messages, approvals, the queue, the roster and changed files to wire
// types and checks every id against its session (R10).
package adapter

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
	"github.com/dev-dami/deck/internal/message"
	"github.com/dev-dami/deck/internal/permission"
	"github.com/dev-dami/deck/internal/pubsub"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/workspace"
	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// Core is what the adapter uses of workspace.Workspace; the test fake
// implements it alone.
type Core interface {
	CreateSession(ctx context.Context, title string) (session.Session, error)
	GetSession(ctx context.Context, sessionID string) (session.Session, error)
	ListSessions(ctx context.Context) ([]session.Session, error)
	SaveSession(ctx context.Context, sess session.Session) (session.Session, error)
	DeleteSession(ctx context.Context, sessionID string) error
	ListMessages(ctx context.Context, sessionID string) ([]message.Message, error)
	AgentCancel(sessionID string)
	AgentIsSessionBusy(sessionID string) bool
	PermissionGrant(perm permission.PermissionRequest) bool
	PermissionGrantPersistent(perm permission.PermissionRequest) bool
	PermissionDeny(perm permission.PermissionRequest) bool
	WorkingDir() string
}

// Events is the app's event stream, as app.App.Events.
type Events func(ctx context.Context) <-chan pubsub.Event[tea.Msg]

// appSource is how the adapter reaches the app's event stream;
// *DeckWorkspace has it through its embedded AppWorkspace.
type appSource interface{ App() *app.App }

type pending struct {
	req permission.PermissionRequest
	at  time.Time
}

// Backend serves a Deck workspace to the remote server.
type Backend struct {
	core   Core
	rc     workspace.RemoteControl
	picker workspace.AgentPicker
	adder  workspace.AgentAdder
	roster workspace.Roster
	events Events

	version, project string
	// themePoll is how often the palette is compared for theme.changed.
	themePoll time.Duration

	mu      sync.Mutex
	waiting map[string]pending // approvals by permission request id
}

// New wraps ws, which must be the Deck workspace.
func New(ws workspace.Workspace, version, project string) (*Backend, error) {
	rc, ok := ws.(workspace.RemoteControl)
	if !ok {
		return nil, fmt.Errorf("remote control needs the Deck workspace, got %T", ws)
	}
	src, ok := ws.(appSource)
	if !ok {
		return nil, fmt.Errorf("workspace %T does not expose its app", ws)
	}
	picker, _ := ws.(workspace.AgentPicker)
	roster, _ := ws.(workspace.Roster)
	return NewWith(ws, rc, picker, roster, src.App().Events, version, project), nil
}

// NewWith builds a Backend from its parts; tests use it with fakes.
func NewWith(core Core, rc workspace.RemoteControl, picker workspace.AgentPicker, roster workspace.Roster, events Events, version, project string) *Backend {
	// The workspace that picks models also adds agents; fakes may not.
	adder, _ := picker.(workspace.AgentAdder)
	return &Backend{
		core: core, rc: rc, picker: picker, adder: adder, roster: roster, events: events,
		version: version, project: project, themePoll: 5 * time.Second,
		waiting: map[string]pending{},
	}
}

func (b *Backend) Info() remote.Info {
	info := remote.Info{Version: b.version, Project: b.project, Theme: toTheme(b.rc.Theme())}
	if b.roster != nil {
		for _, l := range b.roster.Roster("") {
			info.Kinds = append(info.Kinds, l.Kind)
		}
	}
	return info
}

// check returns ErrNotFound for an unknown or task session.
func (b *Backend) check(ctx context.Context, sessionID string) (session.Session, error) {
	if sessionID == "" {
		return session.Session{}, remote.ErrNotFound
	}
	s, err := b.core.GetSession(ctx, sessionID)
	if err != nil || s.ParentSessionID != "" {
		return session.Session{}, remote.ErrNotFound
	}
	return s, nil
}

// mapErr turns workspace sentinels into the backend's.
func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, workspace.ErrNotFound):
		return remote.ErrNotFound
	case errors.Is(err, workspace.ErrNotEditable):
		return remote.ErrInvalid
	}
	return err
}

func (b *Backend) pendingFor(sessionID string) int {
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

func (b *Backend) wireSession(s session.Session) wire.Session {
	return toSession(s, b.core.AgentIsSessionBusy(s.ID), b.pendingFor(s.ID), b.rc.RemoteMode(s.ID))
}

func (b *Backend) Sessions(ctx context.Context) ([]wire.Session, error) {
	all, err := b.core.ListSessions(ctx)
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
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

func (b *Backend) CreateSession(ctx context.Context, title string) (wire.Session, error) {
	if strings.TrimSpace(title) == "" {
		title = "New Session"
	}
	s, err := b.core.CreateSession(ctx, title)
	if err != nil {
		return wire.Session{}, err
	}
	return b.wireSession(s), nil
}

func (b *Backend) RenameSession(ctx context.Context, id, title string) error {
	s, err := b.check(ctx, id)
	if err != nil {
		return err
	}
	s.Title = title
	_, err = b.core.SaveSession(ctx, s)
	return err
}

func (b *Backend) DeleteSession(ctx context.Context, id string) error {
	if _, err := b.check(ctx, id); err != nil {
		return err
	}
	return b.core.DeleteSession(ctx, id)
}

// Messages pages the session's rows from the newest: the cursor names the
// oldest row of the page before.
func (b *Backend) Messages(ctx context.Context, sessionID, before string, limit int) ([]wire.Message, bool, string, error) {
	if _, err := b.check(ctx, sessionID); err != nil {
		return nil, false, "", err
	}
	if limit <= 0 {
		limit = wire.MaxPageLimit
	}
	rows, err := b.core.ListMessages(ctx, sessionID)
	if err != nil {
		return nil, false, "", err
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].CreatedAt != rows[j].CreatedAt {
			return rows[i].CreatedAt < rows[j].CreatedAt
		}
		return rows[i].ID < rows[j].ID
	})
	end := len(rows)
	if before != "" {
		end = -1
		for i, m := range rows {
			if cursorOf(m) == before {
				end = i
				break
			}
		}
		if end < 0 {
			return nil, false, "", remote.ErrInvalid
		}
	}
	start := end - limit
	if start < 0 {
		start = 0
	}
	out := make([]wire.Message, 0, end-start)
	for _, m := range rows[start:end] {
		out = append(out, b.decorate(toMessage(m, b.core.WorkingDir())))
	}
	if start == 0 {
		return out, false, "", nil
	}
	return out, true, cursorOf(rows[start]), nil
}

// cursorOf is a row's opaque cursor.
func cursorOf(m message.Message) string { return fmt.Sprintf("%d:%s", m.CreatedAt, m.ID) }

// decorate adds what only the workspace knows to a converted row.
func (b *Backend) decorate(m wire.Message) wire.Message {
	if m.Role == wire.RoleUser {
		m.Editable = b.rc.EditableMessage(m.SessionID, m.ID)
	}
	return m
}

func (b *Backend) Send(ctx context.Context, sessionID, text string, images []remote.Image) (string, error) {
	if _, err := b.check(ctx, sessionID); err != nil {
		return "", err
	}
	atts := make([]message.Attachment, 0, len(images))
	for _, im := range images {
		atts = append(atts, message.Attachment{FileName: im.Name, MimeType: im.Mime, Content: im.Data})
	}
	return b.rc.PostMessage(ctx, sessionID, text, atts...)
}

func (b *Backend) EditMessage(ctx context.Context, sessionID, messageID, text string) error {
	if _, err := b.check(ctx, sessionID); err != nil {
		return err
	}
	return mapErr(b.rc.EditMessage(ctx, sessionID, messageID, text))
}

func (b *Backend) Cancel(sessionID string) error {
	if _, err := b.check(context.Background(), sessionID); err != nil {
		return err
	}
	b.core.AgentCancel(sessionID)
	return nil
}

func (b *Backend) Interrupt(sessionID, agent string) error {
	if _, err := b.check(context.Background(), sessionID); err != nil {
		return err
	}
	return mapErr(b.rc.InterruptAgent(sessionID, agent))
}

func (b *Backend) Continue(sessionID string) error {
	if _, err := b.check(context.Background(), sessionID); err != nil {
		return err
	}
	return mapErr(b.rc.ContinueThread(sessionID))
}

func (b *Backend) Queue(sessionID string) ([]wire.QueuedItem, error) {
	if _, err := b.check(context.Background(), sessionID); err != nil {
		return nil, err
	}
	return toQueue(b.rc.QueueItems(sessionID)), nil
}

func (b *Backend) Steer(sessionID, itemID string) error {
	if _, err := b.check(context.Background(), sessionID); err != nil {
		return err
	}
	return mapErr(b.rc.SteerQueued(sessionID, itemID))
}

func (b *Backend) CancelQueued(sessionID, itemID string) error {
	if _, err := b.check(context.Background(), sessionID); err != nil {
		return err
	}
	return mapErr(b.rc.CancelQueued(sessionID, itemID))
}

func (b *Backend) Roster(sessionID string) ([]wire.AgentLine, error) {
	if _, err := b.check(context.Background(), sessionID); err != nil {
		return nil, err
	}
	if b.roster == nil {
		return nil, nil
	}
	return toRoster(b.roster.Roster(sessionID)), nil
}

func (b *Backend) AgentKinds(sessionID string) ([]wire.AgentKind, error) {
	if _, err := b.check(context.Background(), sessionID); err != nil {
		return nil, err
	}
	if b.adder == nil {
		return nil, nil
	}
	return toKinds(b.adder.JoinableKinds(sessionID)), nil
}

func (b *Backend) AddAgent(sessionID, kind, model string) (wire.AgentLine, error) {
	if _, err := b.check(context.Background(), sessionID); err != nil {
		return wire.AgentLine{}, err
	}
	if b.adder == nil {
		return wire.AgentLine{}, remote.ErrInvalid
	}
	known := false
	for _, k := range b.adder.JoinableKinds(sessionID) {
		known = known || k.Kind == kind
	}
	if !known {
		return wire.AgentLine{}, remote.ErrInvalid
	}
	id, err := b.adder.AddAgent(sessionID, kind, model)
	if err != nil {
		return wire.AgentLine{}, mapErr(err)
	}
	if b.roster != nil {
		for _, l := range toRoster(b.roster.Roster(sessionID)) {
			if l.ID == id {
				return l, nil
			}
		}
	}
	return wire.AgentLine{ID: id, Name: id, Kind: kind, Model: model}, nil
}

func (b *Backend) Models(ctx context.Context, sessionID, kind string) ([]wire.ModelChoice, error) {
	if _, err := b.check(ctx, sessionID); err != nil {
		return nil, err
	}
	if b.picker == nil {
		return nil, nil
	}
	choices, err := b.picker.AgentModelChoices(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return toModels(choices, kind), nil
}

func (b *Backend) SetModel(sessionID, kind, model, effort string) error {
	if _, err := b.check(context.Background(), sessionID); err != nil {
		return err
	}
	if b.picker == nil {
		return remote.ErrInvalid
	}
	return b.picker.SetAgentModel(sessionID, kind, model, effort)
}

func (b *Backend) Mode(sessionID string) (string, error) {
	if _, err := b.check(context.Background(), sessionID); err != nil {
		return "", err
	}
	return b.rc.RemoteMode(sessionID), nil
}

func (b *Backend) SetMode(sessionID, mode string) error {
	if _, err := b.check(context.Background(), sessionID); err != nil {
		return err
	}
	return b.rc.SetRemoteMode(sessionID, mode)
}

func (b *Backend) Approvals(sessionID string) ([]wire.Approval, error) {
	if sessionID != "" {
		if _, err := b.check(context.Background(), sessionID); err != nil {
			return nil, err
		}
	}
	root := b.core.WorkingDir()
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]wire.Approval, 0, len(b.waiting))
	for _, p := range b.waiting {
		if sessionID == "" || p.req.SessionID == sessionID {
			out = append(out, toApproval(p.req, p.at, root))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (b *Backend) Answer(sessionID, id, decision string) (bool, error) {
	b.mu.Lock()
	p, ok := b.waiting[id]
	b.mu.Unlock()
	if !ok || p.req.SessionID != sessionID {
		return false, remote.ErrNotFound
	}
	var won bool
	switch decision {
	case wire.DecisionAllow:
		won = b.core.PermissionGrant(p.req)
	case wire.DecisionAllowSession:
		won = b.core.PermissionGrantPersistent(p.req)
	case wire.DecisionDeny:
		won = b.core.PermissionDeny(p.req)
	default:
		return false, remote.ErrInvalid
	}
	if !won {
		// Decided elsewhere or withdrawn: the service has no such request,
		// so neither may the waiting set.
		b.mu.Lock()
		delete(b.waiting, id)
		b.mu.Unlock()
	}
	return won, nil
}

func (b *Backend) Changes(ctx context.Context, sessionID string) ([]wire.ChangedFile, error) {
	if _, err := b.check(ctx, sessionID); err != nil {
		return nil, err
	}
	cs, err := b.rc.ChangedFiles(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return toChanges(cs), nil
}

var _ remote.Backend = (*Backend)(nil)
