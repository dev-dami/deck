// Package ui is the Deck desktop window, drawn with Gio: a rail of
// projects and threads, the chat, approvals and a composer. Everything it
// shows comes from state; everything it does is a call to the project's
// engine over gRPC.
package ui

import (
	"cmp"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"gioui.org/app"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/dev-dami/deck/desktop/backend"
	"github.com/dev-dami/deck/desktop/state"
	"github.com/dev-dami/deck/pkg/remote/wire"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

type (
	C = layout.Context
	D = layout.Dimensions
)

const callTimeout = 15 * time.Second

// App is the window and what it drives.
type App struct {
	w     *app.Window
	th    *material.Theme
	col   Colors
	pool  *backend.Pool
	store *state.Store
	ctx   context.Context

	mu       sync.Mutex
	projects map[string]*backend.Project
	opening  map[string]bool
	status   string
	proj     string // selected project key
	sess     string // selected session id
	recents  []string

	pathEd   widget.Editor
	composer widget.Editor
	rail     widget.List
	chat     widget.List
	sendBtn  widget.Clickable
	stopBtn  widget.Clickable
	clicks   map[string]*widget.Clickable
}

// Run opens the window, starts an engine for each of dirs and returns when
// the window closes, after stopping every engine.
func Run(dirs []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &App{
		w: new(app.Window), col: dark, pool: backend.NewPool(), store: &state.Store{}, ctx: ctx,
		projects: map[string]*backend.Project{}, opening: map[string]bool{}, clicks: map[string]*widget.Clickable{},
	}
	a.th = newTheme(a.col)
	a.store.Changed = a.w.Invalidate
	a.pathEd = widget.Editor{SingleLine: true, Submit: true}
	a.composer = widget.Editor{Submit: true}
	a.rail.Axis = layout.Vertical
	a.chat.Axis = layout.Vertical
	a.chat.ScrollToEnd = true
	a.recents = loadRecents()
	a.w.Option(app.Title("Deck"), app.Size(unit.Dp(1280), unit.Dp(820)), app.MinSize(unit.Dp(760), unit.Dp(480)))
	for _, d := range dirs {
		a.open(d)
	}
	defer a.pool.CloseAll()

	var ops op.Ops
	for {
		switch e := a.w.Event().(type) {
		case app.DestroyEvent:
			return e.Err
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			a.layout(gtx)
			e.Frame(gtx.Ops)
		}
	}
}

func (a *App) click(key string) *widget.Clickable {
	c := a.clicks[key]
	if c == nil {
		c = new(widget.Clickable)
		a.clicks[key] = c
	}
	return c
}

func (a *App) setStatus(s string) {
	a.mu.Lock()
	a.status = s
	a.mu.Unlock()
	a.w.Invalidate()
}

func (a *App) selected() (proj, sess string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.proj, a.sess
}

func (a *App) project(key string) *backend.Project {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.projects[key]
}

func (a *App) call() (context.Context, context.CancelFunc) {
	return context.WithTimeout(a.ctx, callTimeout)
}

// open starts (or reuses) the engine for dir in the background.
func (a *App) open(dir string) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		a.setStatus(err.Error())
		return
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		a.setStatus("Not a folder: " + abs)
		return
	}
	a.mu.Lock()
	if a.opening[abs] || a.projects[abs] != nil {
		a.mu.Unlock()
		return
	}
	a.opening[abs] = true
	a.status = "Starting Deck in " + filepath.Base(abs) + "…"
	a.mu.Unlock()
	a.w.Invalidate()
	go func() {
		p, err := a.pool.Open(a.ctx, abs, a.onEvent)
		a.mu.Lock()
		delete(a.opening, abs)
		if err != nil {
			a.status = err.Error()
			a.mu.Unlock()
			a.w.Invalidate()
			return
		}
		a.projects[p.Dir] = p
		a.status = ""
		if a.proj == "" {
			a.proj = p.Dir
		}
		a.recents = append([]string{p.Dir}, slices.DeleteFunc(a.recents, func(d string) bool { return d == p.Dir })...)
		if len(a.recents) > 12 {
			a.recents = a.recents[:12]
		}
		saveRecents(a.recents)
		a.mu.Unlock()
		a.store.Add(p.Dir, p.Info)
		a.refetch(p)
	}()
}

func (a *App) onEvent(p *backend.Project, ev *deckv1.Event) {
	if a.store.Apply(p.Dir, ev) {
		go a.refetch(p)
	}
}

// refetch reloads a project's sessions and approvals, and the selected
// session when it is this project's.
func (a *App) refetch(p *backend.Project) {
	ctx, cancel := a.call()
	defer cancel()
	if r, err := p.Client.ListSessions(ctx, &deckv1.Empty{}); err == nil {
		a.store.SetSessions(p.Dir, r.GetSessions())
		a.mu.Lock()
		if a.proj == p.Dir && a.sess == "" && len(r.GetSessions()) > 0 {
			newest := slices.MaxFunc(r.GetSessions(), func(x, y *deckv1.Session) int { return cmp.Compare(x.GetUpdatedAt(), y.GetUpdatedAt()) })
			a.sess = newest.GetId()
		}
		a.mu.Unlock()
	}
	if r, err := p.Client.Approvals(ctx, &deckv1.SessionRef{}); err == nil {
		a.store.SetApprovals(p.Dir, r.GetApprovals())
	}
	if proj, sess := a.selected(); proj == p.Dir && sess != "" {
		a.loadSession(p, sess)
	}
}

func (a *App) loadSession(p *backend.Project, sid string) {
	ctx, cancel := a.call()
	defer cancel()
	ref := &deckv1.SessionRef{SessionId: sid}
	if r, err := p.Client.ListMessages(ctx, &deckv1.ListMessagesRequest{SessionId: sid, Limit: wire.MaxPageLimit}); err == nil {
		a.store.SetMessages(p.Dir, sid, r.GetMessages())
	} else {
		a.setStatus(err.Error())
	}
	if r, err := p.Client.Roster(ctx, ref); err == nil {
		a.store.SetRoster(p.Dir, sid, r.GetAgents())
	}
	if r, err := p.Client.Queue(ctx, ref); err == nil {
		a.store.SetQueue(p.Dir, sid, r.GetItems())
	}
}

func (a *App) selectSession(proj, sid string) {
	a.mu.Lock()
	a.proj, a.sess = proj, sid
	p := a.projects[proj]
	a.mu.Unlock()
	if p != nil {
		go a.loadSession(p, sid)
	}
}

func (a *App) newThread(proj string) {
	p := a.project(proj)
	if p == nil {
		return
	}
	go func() {
		ctx, cancel := a.call()
		defer cancel()
		s, err := p.Client.CreateSession(ctx, &deckv1.CreateSessionRequest{})
		if err != nil {
			a.setStatus(err.Error())
			return
		}
		a.store.Apply(p.Dir, &deckv1.Event{Kind: &deckv1.Event_SessionCreated{SessionCreated: s}})
		a.selectSession(p.Dir, s.GetId())
		a.w.Invalidate()
	}()
}

// do runs one call against the selected project in the background and
// reports its error.
func (a *App) do(proj string, f func(ctx context.Context, c deckv1.DeckClient) error) {
	p := a.project(proj)
	if p == nil {
		return
	}
	go func() {
		ctx, cancel := a.call()
		defer cancel()
		if err := f(ctx, p.Client); err != nil {
			a.setStatus(err.Error())
		}
	}()
}

func recentsPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "Deck", "recent.json")
}

func loadRecents() []string {
	var out []string
	if p := recentsPath(); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			_ = json.Unmarshal(b, &out)
		}
	}
	return out
}

func saveRecents(r []string) {
	p := recentsPath()
	if p == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	b, _ := json.Marshal(r)
	_ = os.WriteFile(p, b, 0o600)
}
