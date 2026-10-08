package tui

import (
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/tui/motion"
	"github.com/dev-dami/deck/internal/tui/theme"
)

type focusArea int

const (
	focusComposer focusArea = iota
	focusTranscript
)

const (
	sidebarMinWidth = 70
	minW, minH      = 30, 8
)

// Options tune the app for tests and demos.
type Options struct {
	// SaveConfig persists config changes (/name). Default: config.Save.
	SaveConfig func(config.Config) error
	// Bell rings the terminal. Default: write "\a" to stdout when cfg.Bell.
	Bell func() tea.Cmd
	// Dir is the working directory for new agents. Default: os.Getwd.
	Dir string
}

// model is the app. It is used through a pointer.
type model struct {
	mgr  *session.Manager
	cfg  config.Config
	opts Options

	w, h  int
	dark  bool
	now   time.Time
	home  string
	kinds []string

	clock  *motion.Clock
	fps    int
	budget *motion.Budget
	md     *mdCache

	agents []session.AgentInfo
	views  map[string]*agentView
	sel    string

	newMode  bool
	newKind  string
	newDir   string
	newModel map[string]string

	focus   focusArea
	comp    composer
	menu    menu
	ov      overlay
	prompt  promptMode
	pending []*pendingApproval
	toast   toast

	barY       motion.Spring
	switchX    motion.Spring
	switchFade motion.Fade
	intro      motion.Typewriter
	introTag   motion.Fade

	quitArmed time.Time
	g         geom
	noListen  bool // tests drive the manager by hand
}

// New returns the Deck app model.
func New(m *session.Manager, cfg config.Config) tea.Model {
	return NewWithOptions(m, cfg, Options{})
}

// NewWithOptions is New with overrides.
func NewWithOptions(mgr *session.Manager, cfg config.Config, opts Options) tea.Model {
	return newModel(mgr, cfg, opts)
}

func newModel(mgr *session.Manager, cfg config.Config, opts Options) *model {
	home, _ := os.UserHomeDir()
	dir := opts.Dir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if opts.SaveConfig == nil {
		opts.SaveConfig = config.Save
	}
	m := &model{
		mgr:        mgr,
		cfg:        cfg,
		opts:       opts,
		dark:       lipgloss.HasDarkBackground(),
		now:        time.Now(),
		home:       home,
		kinds:      mgr.Kinds(),
		clock:      motion.NewClock(60),
		fps:        60,
		budget:     motion.NewBudget(),
		views:      map[string]*agentView{},
		newKind:    cfg.DefaultAgent,
		newDir:     dir,
		newModel:   map[string]string{},
		barY:       motion.Glide.New(60),
		switchX:    motion.Snappy.New(60),
		switchFade: motion.NewFade(150*time.Millisecond, motion.EaseOutCubic),
		w:          80,
		h:          24,
	}
	m.menu.h = motion.Soft.New(60)
	m.ov.h = motion.Soft.New(60)
	m.md = newMD(m.dark)
	m.intro = motion.NewTypewriter(theme.GlyphAgent+" "+theme.Wordmark, introType)
	m.introTag = motion.NewFade(introTag, motion.EaseOutCubic)
	if m.newKind == "" {
		m.newKind = "claude"
	}
	if !motion.Reduced() && cfg.Motion == "off" {
		motion.SetReduced(true)
	}
	m.agents = mgr.Agents()
	m.g = m.geo()
	return m
}

// Run runs the app full-screen with mouse support.
func Run(mgr *session.Manager, cfg config.Config) error {
	p := tea.NewProgram(New(mgr, cfg), tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

// Init implements tea.Model.
func (m *model) Init() tea.Cmd {
	m.now = time.Now()
	m.startIntro(m.now)
	go m.md.warm()
	return tea.Batch(m.listen(), m.wake())
}

func (m *model) listen() tea.Cmd {
	if m.noListen {
		return nil
	}
	return m.mgr.Listen()
}

// ---- layout ----

type geom struct {
	w, h         int
	sbW          int // sidebar width including its left pad and right gap; 0 when hidden
	mainX, mainW int
	topY         int
	trH          int
	ruleY, chipY int
	compY, compH int
	footY        int
	sidebar      bool
}

func (m *model) geo() geom {
	g := geom{w: m.w, h: m.h}
	if m.w >= sidebarMinWidth && (len(m.agents) > 0) {
		g.sbW = 24
		if m.w >= 100 {
			g.sbW = 31
		}
		g.sidebar = true
	}
	g.mainX = 1
	if g.sbW > 0 {
		g.mainX = g.sbW
	}
	g.mainW = m.w - g.mainX - 1
	if g.mainW < 10 {
		g.mainW = 10
	}
	g.topY = 1
	if m.h >= 20 {
		g.topY = 2
	}
	g.footY = m.h - 1
	g.compH = m.comp.Height(g.mainW)
	g.compY = g.footY - g.compH
	g.chipY = g.compY - 1
	g.ruleY = g.chipY - 1
	g.trH = g.ruleY - g.topY
	if g.trH < 1 {
		g.trH = 1
	}
	return g
}

func (m *model) curView() *agentView {
	if m.sel == "" || m.newMode {
		return nil
	}
	return m.views[m.sel]
}

func (m *model) view(id string) *agentView {
	v := m.views[id]
	if v == nil {
		v = newView(id)
		m.views[id] = v
	}
	return v
}

func (m *model) info(id string) (session.AgentInfo, bool) {
	for _, a := range m.agents {
		if a.ID == id {
			return a, true
		}
	}
	return session.AgentInfo{}, false
}

func (m *model) pendingFor(id string) []*pendingApproval {
	var out []*pendingApproval
	for _, p := range m.pending {
		if p.req.AgentID == id {
			out = append(out, p)
		}
	}
	return out
}

// settle recomputes everything derived from state after any change.
func (m *model) settle() {
	m.agents = m.mgr.Agents()
	if m.sel != "" {
		if _, ok := m.info(m.sel); !ok {
			m.sel = ""
		}
	}
	if m.sel == "" && !m.newMode && len(m.agents) > 0 {
		m.sel = m.agents[0].ID
	}
	m.g = m.geo()
	g := m.g

	// sidebar bar target
	if m.newMode {
		m.barY.Set(float64(len(m.agents)))
	} else if i := m.selIndex(); i >= 0 {
		m.barY.Set(float64(i))
	}
	if m.ov.open {
		m.clampOverlay()
		m.ov.h.Set(float64(m.overlayTargetHeight(g.mainW, max(3, g.trH-1))))
	}
	if v := m.curView(); v != nil {
		if a, ok := m.info(v.id); ok {
			m.compose(v, a, g.mainW-1)
			v.layoutScroll(g.trH)
		}
	}
}

// ---- motion ----

// motionState reports whether anything needs fast (spring) frames or only
// slow ambient ones (spinner, pulse, cursor).
func (m *model) motionState() (fast, ambient bool) {
	now := m.now
	if m.barY.Moving() || m.switchX.Moving() || m.switchFade.Moving(now) || m.menu.h.Moving() ||
		m.ov.h.Moving() || m.introMoving() || m.toast.moving(now) {
		fast = true
	}
	for _, p := range m.pending {
		if p.moving(now) {
			fast = true
		}
		if p.badgePulsing(now) {
			ambient = true
		}
	}
	for _, v := range m.views {
		if v.flash.Moving(now) {
			fast = true
		}
	}
	if v := m.curView(); v != nil {
		if v.anyMoving(now) {
			fast = true
		}
		if v.anyAmbient() {
			ambient = true
		}
	}
	for _, a := range m.agents {
		if a.Status == session.StatusWorking || a.Status == session.StatusTool {
			ambient = true
		}
	}
	return fast, ambient
}

// wake starts the clock if anything is moving.
func (m *model) wake() tea.Cmd {
	fast, amb := m.motionState()
	if !fast && !amb {
		return nil
	}
	want := 15
	if fast {
		want = 60
	}
	if want != m.fps {
		m.fps = want
		m.clock.SetFPS(want)
		m.clock.Stop()
	}
	return m.clock.Wake()
}

// advance steps every spring one frame.
func (m *model) advance() {
	m.barY.Update()
	m.switchX.Update()
	m.menu.h.Update()
	m.ov.h.Update()
	if !m.ov.open && m.ov.h.Pos <= 0.5 {
		m.ov.kind = ovNone
	}
	for _, p := range m.pending {
		p.slide.Update()
	}
	for _, v := range m.views {
		v.advance()
	}
}

// ---- Update ----

// Update implements tea.Model.
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.now = time.Now()
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height

	case tea.KeyMsg:
		cmds = append(cmds, m.handleKey(msg)...)

	case tea.MouseMsg:
		cmds = append(cmds, m.handleMouse(msg)...)

	case motion.FrameMsg:
		if !m.clock.Accept(msg) {
			return m, nil
		}
		m.advance()
		m.settle()
		fast, amb := m.motionState()
		if fast || amb {
			want := 15
			if fast {
				want = 60
			}
			if want != m.fps {
				m.fps = want
				m.clock.SetFPS(want)
			}
			m.clock.SetMoving(true)
		} else {
			m.clock.SetMoving(false)
		}
		return m, m.clock.Tick()

	case session.EventMsg:
		cmds = append(cmds, m.onSessionEvent(msg.Event, false)...)
		cmds = append(cmds, m.listen())

	case session.ApprovalMsg:
		cmds = append(cmds, m.onApproval(msg.ApprovalRequest)...)
		cmds = append(cmds, m.listen())

	case session.ApprovalGoneMsg:
		m.dropApproval(msg.ID)
		cmds = append(cmds, m.listen())

	case startedMsg:
		cmds = append(cmds, m.onStarted(msg)...)

	case sentMsg:
		if msg.err != nil {
			cmds = append(cmds, m.toast.show(msg.err.Error(), true, m.now))
		}

	case resumedMsg:
		cmds = append(cmds, m.onResumed(msg)...)

	case toastFadeMsg:
		// the fade itself is time-based; this only wakes the clock
	case toastExpireMsg:
		if msg.seq == m.toast.seq {
			m.toast.on = false
		}

	case quitDisarmMsg:
		// nothing: armed state expires by time

	case tea.QuitMsg:
	}

	m.settle()
	cmds = append(cmds, m.wake())
	return m, tea.Batch(cmds...)
}

// ---- View ----

// View implements tea.Model.
func (m *model) View() string {
	start := time.Now()
	defer func() { m.budget.Observe(time.Since(start)) }()

	if m.w < minW || m.h < minH {
		return sFaint.Render(trunc("deck needs a bigger terminal", m.w))
	}
	g := m.g
	rows := make([]string, 0, m.h)
	rows = append(rows, m.headerLine(g))
	if g.topY == 2 {
		rows = append(rows, "")
	}

	main := m.mainLines(g)
	var side []string
	if g.sidebar {
		side = m.sidebarLines(g, len(main))
	}
	for i, l := range main {
		left := ""
		if g.sidebar {
			left = side[i]
		} else {
			left = " "
		}
		rows = append(rows, left+l)
	}
	rows = append(rows, m.footerLine(g))
	for i, r := range rows {
		rows[i] = trunc(r, m.w)
	}
	return strings.Join(rows, "\n")
}

func (m *model) headerLine(g geom) string {
	left := " " + sAccent.Render(theme.GlyphAgent) + " " + sBold.Render(theme.Wordmark)
	if m.curView() == nil { // the empty state draws its own wordmark
		left = " "
	}
	working, waiting := 0, 0
	cost := 0.0
	for _, a := range m.agents {
		if a.Status == session.StatusWorking || a.Status == session.StatusTool {
			working++
		}
		waiting += a.Pending
		cost += a.CostUSD
	}
	var parts []string
	switch {
	case working > 0:
		parts = append(parts, sMuted.Render(plural(working, "working", "working")))
	case len(m.agents) > 0 && waiting == 0:
		parts = append(parts, sMuted.Render("idle"))
	}
	if waiting > 0 {
		parts = append(parts, sWarn.Render(plural(waiting, "waiting", "waiting")))
	}
	if c := fmtCost(cost); c != "" {
		parts = append(parts, sMuted.Render(c))
	}
	sep := sFaint.Render(" " + glyphDot + " ")
	right := strings.Join(parts, sep)
	if right == "" {
		return trunc(left, m.w)
	}
	if left == " " {
		return padTo("", m.w-sw(right)-1) + right + " "
	}
	return rightAlign(left, right+" ", m.w)
}

func (m *model) mainLines(g geom) []string {
	w := g.mainW
	var tr []string
	v := m.curView()
	if v == nil {
		tr = m.emptyLines(w, g.trH)
	} else {
		tr = v.window(g.trH)
		tr = m.applySwitch(tr, w)
	}
	// overlays rise from the composer
	var ov []string
	if m.ov.visible() {
		ov = m.overlayLines(w, max(3, g.trH-1))
	} else if m.menu.visible() {
		ov = m.menuLines(w)
	}
	if len(ov) > len(tr) {
		ov = ov[len(ov)-len(tr):]
	}
	for i, l := range ov {
		tr[len(tr)-len(ov)+i] = l
	}
	for i := range tr {
		tr[i] = trunc(tr[i], w)
	}

	out := make([]string, 0, g.trH+2+g.compH)
	out = append(out, tr...)
	out = append(out, st(theme.Rule).Render(strings.Repeat(theme.GlyphRule, w)))
	out = append(out, m.chipLine(w))
	out = append(out, m.composerLines(w)...)
	return out
}

// applySwitch slides and fades the transcript when the selected agent changes.
func (m *model) applySwitch(tr []string, w int) []string {
	if motion.Reduced() {
		return tr
	}
	shift := int(m.switchX.Pos + 0.5)
	fading := m.switchFade.Started() && !m.switchFade.Done(m.now)
	if shift <= 0 && !fading {
		return tr
	}
	var s lipgloss.Style
	if fading {
		s = m.ramp(theme.Faint, theme.Fg, m.switchFade.Progress(m.now))
	}
	pad := strings.Repeat(" ", max(0, shift))
	for i, l := range tr {
		if fading {
			l = s.Render(ansiStrip(l))
		}
		tr[i] = trunc(pad+l, w)
	}
	return tr
}

func (m *model) chipLine(w int) string {
	if m.prompt.active() {
		return "  " + sWarn.Render(trunc(m.prompt.label(m), w-2))
	}
	kind, model, dir := m.newKind, m.newModelFor(m.newKind), m.newDir
	if a, ok := m.info(m.sel); ok && !m.newMode {
		kind, model, dir = a.Kind, a.Model, a.Dir
		if model == "" {
			model = m.defaultModel(kind)
		}
	} else if model == "" {
		model = m.defaultModel(kind)
	}
	if model == "" {
		model = "default"
	}
	sep := sFaint.Render(" " + glyphDot + " ")
	var parts []string
	if m.newMode && len(m.agents) > 0 {
		parts = append(parts, sFaint.Render("new"))
	}
	parts = append(parts, sMuted.Render(kind), sFaint.Render(model))
	d := tilde(dir, m.home)
	line := "  " + strings.Join(parts, sep)
	if next := m.newModel[kind]; next != "" && !m.newMode && next != model {
		line += sep + sFaint.Render("next "+next)
	}
	room := w - sw(line) - 3
	if room >= 4 {
		line += sep + sFaint.Render(shortPath(d, room))
	}
	return trunc(line, w)
}

func (m *model) defaultModel(kind string) string {
	switch kind {
	case "claude":
		return m.cfg.Models.Claude
	case "codex":
		return m.cfg.Models.Codex
	}
	return ""
}

func (m *model) newModelFor(kind string) string {
	if v := m.newModel[kind]; v != "" {
		return v
	}
	return m.defaultModel(kind)
}

func (m *model) composerLines(w int) []string {
	prompt := sFaint.Render(glyphPrompt)
	if m.focus == focusComposer {
		prompt = sAccent.Render(glyphPrompt)
	}
	ph := m.placeholder()
	if m.prompt.active() {
		prompt = sWarn.Render(m.prompt.glyph())
	}
	return m.comp.View(w, prompt, ph, m.focus == focusComposer && !m.ov.open)
}

func (m *model) placeholder() string {
	switch {
	case m.prompt.active():
		return m.prompt.placeholder()
	case m.newMode || len(m.agents) == 0:
		return "ask " + m.newKind + "…"
	}
	if a, ok := m.info(m.sel); ok {
		return "message " + a.Name + "…"
	}
	return "message…"
}

func (m *model) footerLine(g geom) string {
	hints := m.footerHints()
	t := m.renderToast()
	if t == "" {
		return " " + trunc(hints, m.w-2)
	}
	tw := sw(t)
	room := m.w - tw - 4
	left := ""
	if room > 8 {
		left = trunc(hints, room)
	}
	return " " + padTo(left, m.w-tw-3) + t + " "
}

func (m *model) footerHints() string {
	h := func(parts ...string) string { return hintFit(m.w-4, parts...) }
	switch {
	case m.ov.open && m.ov.kind == ovResume:
		return h("↑↓ select", "enter resume", "esc close")
	case m.ov.open:
		return h("y allow", "n deny", "a always", "esc close")
	case m.prompt.active():
		return h("enter send", "esc cancel")
	case m.menu.open():
		return h("↑↓ select", "tab complete", "esc close")
	case m.focus == focusTranscript:
		return h("enter fold", "[ ] row", "tab compose", "q quit")
	case len(m.agents) == 0:
		return h("enter start "+m.newKind, "@ agent", "/ commands")
	}
	if len(m.pendingFor(m.sel)) > 0 && m.comp.Empty() {
		return h("y allow", "n deny", "a always", "i inbox")
	}
	if len(m.pending) > 0 {
		return h("@ agent", "/ commands", "i inbox", "^c stop")
	}
	return h("@ agent", "/ commands", "tab browse", "^c stop")
}

// bell rings the terminal once.
func (m *model) bell() tea.Cmd {
	if !m.cfg.Bell {
		return nil
	}
	if m.opts.Bell != nil {
		return m.opts.Bell()
	}
	return func() tea.Msg {
		_, _ = os.Stdout.WriteString("\a")
		return nil
	}
}

// selectAgent makes id the selected agent and starts the switch animation.
func (m *model) selectAgent(id string) {
	if id == m.sel && !m.newMode {
		return
	}
	m.newMode = false
	m.sel = id
	m.agents = m.mgr.Agents()
	if i := m.selIndex(); i >= 0 {
		m.barY.Set(float64(i))
	}
	m.switchX.Pos = 6
	m.switchX.Set(0)
	m.switchFade.Start(m.now)
	m.view(id)
}
