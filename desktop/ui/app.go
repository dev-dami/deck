// Package ui is the Deck desktop window, drawn with Gio and the gio-x
// (markdown, richtext, menus, tooltips, resizer, notifications) and
// gioview (file tree) components: a rail of projects and threads, the chat
// with approvals and a composer, a resizable side panel (changes, files,
// agents, queue) and Settings. Everything it shows comes from state;
// everything it does is a call to the project's engine over gRPC.
package ui

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gioui.org/app"
	"gioui.org/io/clipboard"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"gioui.org/x/component"
	"gioui.org/x/markdown"
	"gioui.org/x/notify"
	"gioui.org/x/richtext"
	"github.com/oligo/gioview/navi"

	"github.com/dev-dami/deck/desktop/backend"
	"github.com/dev-dami/deck/desktop/platform"
	"github.com/dev-dami/deck/desktop/settings"
	"github.com/dev-dami/deck/desktop/state"
	"github.com/dev-dami/deck/pkg/remote/wire"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

type (
	C = layout.Context
	D = layout.Dimensions
)

const (
	callTimeout   = 15 * time.Second
	maxImageBytes = 16 << 20
	toastFor      = 6 * time.Second
	maxPreview    = 512 << 10
)

type page int

const (
	pageChat page = iota
	pageSettings
)

type tab int

const (
	tabChanges tab = iota
	tabFiles
	tabAgents
	tabQueue
)

var tabNames = []string{tabChanges: "changes", tabFiles: "files", tabAgents: "agents", tabQueue: "queue"}

func tabNamed(n string) tab {
	for i, s := range tabNames {
		if s == n {
			return tab(i)
		}
	}
	return tabChanges
}

type attachment struct {
	name, mime string
	data       []byte
}

// popup is a gio-x context menu: a ContextArea and the menu it opens.
type popup struct {
	area  component.ContextArea
	state component.MenuState
}

// App is the window and what it drives.
type App struct {
	version string
	w       *app.Window
	th      *material.Theme
	col     Colors
	pool    *backend.Pool
	store   *state.Store
	ctx     context.Context
	hwnd    uintptr
	focused atomic.Bool

	mu       sync.Mutex
	set      settings.Settings
	projects map[string]*backend.Project
	opening  map[string]bool
	toast    string
	toastErr bool
	toastAt  time.Time
	proj     string // selected project key
	sess     string // selected session id
	// pendingAttach is picked images on their way to attach.
	pendingAttach []attachment

	// UI goroutine only.
	page       page
	tab        tab
	title      string
	attach     []attachment
	renaming   bool
	confirm    string // session id awaiting delete confirmation
	expanded   map[string]bool
	pathEd     widget.Editor
	searchEd   widget.Editor
	composer   widget.Editor
	renameEd   widget.Editor
	deckEd     widget.Editor
	rail       widget.List
	chat       widget.List
	panel      widget.List
	settingsL  widget.List
	split      component.Resize
	clicks     map[string]*widget.Clickable
	tips       map[string]*component.TipArea
	popups     map[string]*popup
	rich       map[string]*richtext.InteractiveText
	md         *markdown.Renderer
	proseCache map[string]proseCache
	codeCache  map[string]codeCache
	trees      map[string]*navi.NavTree
	copyQueue  []string
	preview    string // file shown in the Files tab
	previewTx  string
	reopen     widget.Bool
	atLogin    widget.Bool
}

// Run opens the window, starts an engine for each of dirs (and the
// projects open at the last quit, if Settings say so) and returns when the
// window closes, after stopping every engine.
func Run(version string, dirs []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &App{
		version: version, w: new(app.Window), pool: backend.NewPool(), store: &state.Store{}, ctx: ctx,
		set: settings.Load(), projects: map[string]*backend.Project{}, opening: map[string]bool{},
		expanded: map[string]bool{}, clicks: map[string]*widget.Clickable{}, tips: map[string]*component.TipArea{},
		popups: map[string]*popup{}, rich: map[string]*richtext.InteractiveText{},
		proseCache: map[string]proseCache{}, codeCache: map[string]codeCache{}, trees: map[string]*navi.NavTree{},
		split: component.Resize{Ratio: 0.7},
	}
	a.applyTheme()
	a.split.Ratio = a.set.PanelRatio
	a.tab = tabNamed(a.set.PanelTab)
	a.store.Changed = a.w.Invalidate
	a.pathEd = widget.Editor{SingleLine: true, Submit: true}
	a.searchEd = widget.Editor{SingleLine: true}
	a.renameEd = widget.Editor{SingleLine: true, Submit: true}
	a.deckEd = widget.Editor{SingleLine: true, Submit: true}
	a.deckEd.SetText(a.set.DeckPath)
	a.composer = widget.Editor{Submit: true}
	a.rail.Axis = layout.Vertical
	a.panel.Axis = layout.Vertical
	a.settingsL.Axis = layout.Vertical
	a.chat.Axis = layout.Vertical
	a.chat.ScrollToEnd = true
	a.reopen.Value = a.set.ReopenProjects
	a.atLogin.Value = platform.LaunchAtLogin()
	a.focused.Store(true)
	// Maximised, like other desktop apps: a fixed size overflows small screens.
	a.w.Option(app.Title("Deck"), app.Size(unit.Dp(1180), unit.Dp(720)), app.MinSize(unit.Dp(820), unit.Dp(480)), app.Maximized.Option())

	if a.set.ReopenProjects {
		dirs = append(dirs, a.set.Open...)
	}
	for _, d := range dirs {
		a.open(d)
	}
	defer func() {
		a.mu.Lock()
		a.set.Open = a.set.Open[:0]
		for d := range a.projects {
			a.set.Open = append(a.set.Open, d)
		}
		slices.Sort(a.set.Open)
		a.set.PanelRatio = a.split.Ratio
		_ = a.set.Save()
		a.mu.Unlock()
		a.pool.CloseAll()
	}()

	var ops op.Ops
	for {
		switch e := a.w.Event().(type) {
		case app.Win32ViewEvent:
			a.hwnd = e.HWND
		case app.ConfigEvent:
			a.focused.Store(e.Config.Focused)
		case app.DestroyEvent:
			return e.Err
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			a.layout(gtx)
			e.Frame(gtx.Ops)
		}
	}
}

func (a *App) applyTheme() {
	a.col = dark
	if a.set.Theme == "light" {
		a.col = light
	}
	a.th = newTheme(a.col, a.set.TextSize)
	// Cached spans carry the old colours and sizes.
	a.proseCache = map[string]proseCache{}
	a.codeCache = map[string]codeCache{}
}

// saveSettings edits and persists the settings.
func (a *App) saveSettings(f func(s *settings.Settings)) {
	a.mu.Lock()
	f(&a.set)
	err := a.set.Save()
	a.mu.Unlock()
	if err != nil {
		a.fail(fmt.Errorf("could not save settings: %w", err))
	}
	a.w.Invalidate()
}

func (a *App) click(key string) *widget.Clickable {
	c := a.clicks[key]
	if c == nil {
		c = new(widget.Clickable)
		a.clicks[key] = c
	}
	return c
}

func (a *App) popup(key string) *popup {
	p := a.popups[key]
	if p == nil {
		p = new(popup)
		a.popups[key] = p
	}
	return p
}

// note shows a short message in the corner for a few seconds.
func (a *App) note(s string, isErr bool) {
	a.mu.Lock()
	a.toast, a.toastErr, a.toastAt = s, isErr, time.Now()
	a.mu.Unlock()
	a.w.Invalidate()
	go func() { time.Sleep(toastFor + 50*time.Millisecond); a.w.Invalidate() }()
}

func (a *App) fail(err error) {
	if err != nil {
		a.note(err.Error(), true)
	}
}

func (a *App) copyText(s string) {
	a.copyQueue = append(a.copyQueue, s)
	a.note("Copied to the clipboard", false)
}

func itoa(n int) string { return strconv.Itoa(n) }

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

// notify raises a system notification while the window is not in front.
func (a *App) notify(title, text string) {
	if a.focused.Load() {
		return
	}
	go func() { _, _ = notify.Push(title, text) }()
}

// pickFolder shows the system folder picker and opens what was chosen.
func (a *App) pickFolder() {
	go func() {
		dir, err := platform.PickFolder(a.hwnd, "Open a project folder")
		switch {
		case err == platform.ErrCancelled:
		case err == platform.ErrUnsupported:
			a.note("Paste the folder's path in the box on the left.", false)
		case err != nil:
			a.fail(err)
		default:
			a.open(dir)
		}
	}()
}

// open starts (or reuses) the engine for dir in the background.
func (a *App) open(dir string) {
	abs, err := filepath.Abs(strings.Trim(strings.TrimSpace(dir), `"`))
	if err != nil {
		a.fail(err)
		return
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		a.note("Not a folder: "+abs, true)
		return
	}
	a.mu.Lock()
	if a.opening[abs] || a.projects[abs] != nil {
		if a.projects[abs] != nil {
			a.proj, a.sess = abs, ""
		}
		a.mu.Unlock()
		return
	}
	a.opening[abs] = true
	override := a.set.DeckPath
	a.mu.Unlock()
	a.w.Invalidate()
	go func() {
		bin, err := backend.FindDeck(override)
		var p *backend.Project
		if err == nil {
			p, err = a.pool.Open(a.ctx, bin, abs, a.onEvent)
		}
		a.mu.Lock()
		delete(a.opening, abs)
		if err != nil {
			a.mu.Unlock()
			a.note(filepath.Base(abs)+": "+err.Error(), true)
			return
		}
		a.projects[p.Dir] = p
		a.proj, a.sess = p.Dir, ""
		a.set.Touch(p.Dir)
		_ = a.set.Save()
		a.mu.Unlock()
		a.store.Add(p.Dir, p.Info)
		a.refetch(p)
	}()
}

// closeProject stops a project's engine and forgets its state.
func (a *App) closeProject(key string) {
	a.mu.Lock()
	delete(a.projects, key)
	if a.proj == key {
		a.proj, a.sess = "", ""
		for k := range a.projects {
			a.proj = k
			break
		}
	}
	a.mu.Unlock()
	delete(a.trees, key)
	a.store.Remove(key)
	go a.pool.Close(key)
}

func (a *App) onEvent(p *backend.Project, ev *deckv1.Event) {
	if a.store.Apply(p.Dir, ev) {
		go a.refetch(p)
	}
	switch k := ev.GetKind().(type) {
	case *deckv1.Event_ApprovalRequested:
		ap := k.ApprovalRequested
		a.notify("Deck: "+ap.GetAgent()+" needs your OK", ap.GetDescription())
	case *deckv1.Event_Busy:
		if k.Busy.GetBusy() {
			break
		}
		// A turn ended: what it changed is worth a look.
		if proj, sess := a.selected(); proj == p.Dir && sess == k.Busy.GetSessionId() {
			go a.loadChanges(p, sess)
		}
		a.notify("Deck", "The agents finished in "+filepath.Base(p.Dir)+".")
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
		a.fail(err)
	}
	if r, err := p.Client.Roster(ctx, ref); err == nil {
		a.store.SetRoster(p.Dir, sid, r.GetAgents())
		a.loadModels(ctx, p, sid, r.GetAgents())
	}
	if r, err := p.Client.Queue(ctx, ref); err == nil {
		a.store.SetQueue(p.Dir, sid, r.GetItems())
	}
	a.loadChanges(p, sid)
}

func (a *App) loadModels(ctx context.Context, p *backend.Project, sid string, roster []*deckv1.AgentLine) {
	seen := map[string]bool{}
	for _, ag := range roster {
		if seen[ag.GetKind()] {
			continue
		}
		seen[ag.GetKind()] = true
		if r, err := p.Client.Models(ctx, &deckv1.ModelsRequest{SessionId: sid, Kind: ag.GetKind()}); err == nil {
			a.store.SetModels(p.Dir, sid, ag.GetKind(), r.GetModels())
		}
	}
}

func (a *App) loadChanges(p *backend.Project, sid string) {
	ctx, cancel := a.call()
	defer cancel()
	if r, err := p.Client.Changes(ctx, &deckv1.SessionRef{SessionId: sid}); err == nil {
		a.store.SetChanges(p.Dir, sid, r.GetFiles())
	}
}

func (a *App) selectSession(proj, sid string) {
	a.mu.Lock()
	a.proj, a.sess = proj, sid
	p := a.projects[proj]
	a.mu.Unlock()
	a.page = pageChat
	a.renaming = false
	a.confirm = ""
	a.attach = nil
	if p != nil {
		go a.loadSession(p, sid)
	}
}

func (a *App) newThread(proj string) {
	p := a.project(proj)
	if p == nil {
		a.note("Open a project first.", false)
		return
	}
	a.mu.Lock()
	mode := a.set.DefaultMode
	a.mu.Unlock()
	a.page = pageChat
	go func() {
		ctx, cancel := a.call()
		defer cancel()
		s, err := p.Client.CreateSession(ctx, &deckv1.CreateSessionRequest{})
		if err != nil {
			a.fail(err)
			return
		}
		if mode != "" {
			if _, err := p.Client.SetMode(ctx, &deckv1.SetModeRequest{SessionId: s.GetId(), Mode: mode}); err == nil {
				s.Mode = mode
			}
		}
		a.store.Apply(p.Dir, &deckv1.Event{Kind: &deckv1.Event_SessionCreated{SessionCreated: s}})
		a.mu.Lock()
		a.proj, a.sess = p.Dir, s.GetId()
		a.mu.Unlock()
		a.loadSession(p, s.GetId())
	}()
}

// do runs one call against a project in the background and reports its
// error; after, if set, runs once it succeeded.
func (a *App) do(proj string, f func(ctx context.Context, c deckv1.DeckClient) error, after ...func(p *backend.Project)) {
	p := a.project(proj)
	if p == nil {
		return
	}
	go func() {
		ctx, cancel := a.call()
		defer cancel()
		if err := f(ctx, p.Client); err != nil {
			a.fail(err)
			return
		}
		for _, g := range after {
			g(p)
		}
	}()
}

func (a *App) deleteThread(proj, sid string) {
	a.do(proj, func(ctx context.Context, c deckv1.DeckClient) error {
		_, err := c.DeleteSession(ctx, &deckv1.SessionRef{SessionId: sid})
		return err
	}, func(p *backend.Project) {
		a.mu.Lock()
		if a.sess == sid {
			a.sess = ""
		}
		a.mu.Unlock()
		a.refetch(p)
	})
}

func mimeOf(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	return ""
}

// pickImages adds images to the next message.
func (a *App) pickImages() {
	go func() {
		paths, err := platform.PickFiles(a.hwnd, "Attach images", platform.Filter{Name: "Images", Pattern: "*.png;*.jpg;*.jpeg;*.gif;*.webp"})
		if err == platform.ErrCancelled {
			return
		}
		if err != nil {
			a.fail(err)
			return
		}
		var add []attachment
		for _, p := range paths {
			m := mimeOf(p)
			if m == "" {
				a.note(filepath.Base(p)+" is not an image Deck can send", true)
				continue
			}
			if st, err := os.Stat(p); err != nil || st.Size() > maxImageBytes {
				a.note(filepath.Base(p)+" is larger than 16 MB", true)
				continue
			}
			b, err := os.ReadFile(p)
			if err != nil {
				a.fail(err)
				continue
			}
			add = append(add, attachment{name: filepath.Base(p), mime: m, data: b})
		}
		a.mu.Lock()
		a.pendingAttach = append(a.pendingAttach, add...)
		a.mu.Unlock()
		a.w.Invalidate()
	}()
}

func (a *App) send(proj, sid string) {
	text := strings.TrimSpace(a.composer.Text())
	if text == "" && len(a.attach) == 0 {
		return
	}
	images := make([]*deckv1.Image, 0, len(a.attach))
	for _, at := range a.attach {
		images = append(images, &deckv1.Image{Name: at.name, Mime: at.mime, Data: at.data})
	}
	a.composer.SetText("")
	a.attach = nil
	a.do(proj, func(ctx context.Context, c deckv1.DeckClient) error {
		_, err := c.Send(ctx, &deckv1.SendRequest{SessionId: sid, Text: text, Images: images})
		return err
	})
}

// openPreview loads a project file for the Files tab.
func (a *App) openPreview(path string) {
	st, err := os.Stat(path)
	if err != nil {
		a.fail(err)
		return
	}
	if st.Size() > maxPreview {
		a.preview, a.previewTx = path, fmt.Sprintf("(%s is %d KB; open it in your editor)", filepath.Base(path), st.Size()>>10)
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		a.fail(err)
		return
	}
	a.preview, a.previewTx = path, string(b)
}

// windowTitle shows how many approvals wait, so a minimised Deck says it
// needs you.
func (a *App) windowTitle(waiting int) {
	t := "Deck"
	if waiting > 0 {
		t = fmt.Sprintf("Deck (%d waiting)", waiting)
	}
	if t != a.title {
		a.title = t
		a.w.Option(app.Title(t))
	}
}

// flushClipboard writes what copyText queued; clipboard writes need a
// frame's context.
func (a *App) flushClipboard(gtx C) {
	for _, s := range a.copyQueue {
		gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(s))})
	}
	a.copyQueue = nil
}
