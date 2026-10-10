package tui

import (
	"context"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/clipimg"
	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/termimg"
	"github.com/dev-dami/deck/internal/tui/logo"
	"github.com/dev-dami/deck/internal/tui/motion"
	"github.com/dev-dami/deck/internal/tui/theme"
	"github.com/dev-dami/deck/internal/tui/yolo"
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
	// Notify raises a desktop notification. Default: OSC 777 and OSC 9 to
	// stdout, which terminals that support them turn into one.
	Notify func(title, body string) tea.Cmd
	// Images is how the terminal draws pictures. With kitty graphics the
	// agents' marks are their real logos. Default: none (text marks).
	Images termimg.Protocol
	// Dir is the working directory for new threads. Default: os.Getwd.
	Dir string
	// KeyClock stamps key presses for paste-burst detection. Default: time.Now.
	KeyClock func() time.Time
	// ClipSave reads an image off the clipboard into a file. Default: clipimg.Save.
	ClipSave func(ctx context.Context, dir string) (string, error)
}

// model is the app. It is used through a pointer.
type model struct {
	mgr  *session.Manager
	cfg  config.Config
	opts Options

	w, h  int
	dark  bool
	now   time.Time
	nowFn func() time.Time // the clock; tests replace it
	home  string
	kinds []string

	clock  *motion.Clock
	fps    int
	budget *motion.Budget
	md     *mdCache

	threads []session.ThreadInfo // every live thread
	rows    []session.ThreadInfo // the ones the sidebar lists
	agents  []session.AgentInfo  // every member of every thread
	views   map[string]*threadView
	sel     string // the selected thread

	newKind  string // kind an empty thread starts with
	newDir   string // directory of new threads
	newModel map[string]string
	// newDefault marks kinds whose next agent was told to use the CLI's own
	// default model, over whatever the config says.
	newDefault map[string]bool

	focus   focusArea
	comp    composer
	menu    menu
	ov      overlay
	prompt  promptMode
	pending []*pendingApproval
	toast   toast

	pick       picker                // the model picker
	fight      fightState            // the yolo animation, while it plays
	modeFlash  motion.Fade           // the chip glowing after it flips to yolo
	noted      map[string]agent.Mode // per thread: the last mode the transcript announced
	seenGlobal map[string]bool       // global notices already shown

	barY       motion.Spring
	switchX    motion.Spring
	switchFade motion.Fade

	// the empty state's logo
	logo        *logo.Model
	logoStarted bool // the intro has been started for this showing of the empty state
	logoMoving  bool // the last Advance (or Start, or Mouse) said it is not at rest
	logoX       int  // screen cell of the logo's top-left
	logoY       int
	plan        emptyPlan
	introTag    motion.Fade

	// paste bursts, attachments, queue, editing (attach.go, queue.go)
	keyNow       func() time.Time
	lastKeyAt    time.Time
	inBurst      bool
	prevTyped    bool // the previous key was a typed character
	prevPasteKey bool // the previous key was an Enter or Tab taken as paste
	burstText    bool // characters arrived in a burst since the last check
	burstArmed   bool
	noBurstTick  bool // tests call burstCheckMsg by hand
	attach       []attachment

	// thread lifecycle (threads.go)
	drafts       map[string]draft // composer text per thread you left
	unread       map[string]bool  // threads with news you have not seen
	delArm       time.Time        // a first /delete, waiting for the second
	delThread    string
	files        map[string][]string // @file: each folder's files
	filesLoading map[string]bool
	wantFiles    string    // a folder whose files the menu is waiting for
	unisoArm     time.Time // a refused /unisolate, waiting for the second
	unisoThread  string
	kittyIDs     map[string]uint32 // agent kind -> its logo's image id (logos.go)
	kittyPending bool              // the logos still have to be sent
	winTitle     string            // the terminal title last set
	blurred      bool              // the terminal window does not have focus
	clipSave     func(ctx context.Context, dir string) (string, error)
	clipWarned   bool
	queue        map[string][]session.QueuedItem
	steering     map[string]bool
	qFocus       int // focused queue row, -1 for none
	edit         *editState

	// the changes panel and the header's summary per thread (changes.go)
	chg       changesPanel
	chgSum    map[string]changeSum
	chgSumSeq map[string]int

	quitArmed time.Time
	g         geom
	noListen  bool // tests drive the manager by hand
}

// New returns the Deck app model.
func New(m *session.Manager, cfg config.Config) tea.Model {
	return NewWithOptions(m, cfg, Options{Images: termimg.Detect(os.Getenv)})
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
	if opts.KeyClock == nil {
		opts.KeyClock = time.Now
	}
	if opts.ClipSave == nil {
		opts.ClipSave = clipimg.Save
	}
	dark := lipgloss.HasDarkBackground()
	m := &model{
		mgr:          mgr,
		cfg:          cfg,
		opts:         opts,
		dark:         dark,
		now:          time.Now(),
		nowFn:        time.Now,
		home:         home,
		kinds:        mgr.Kinds(),
		clock:        motion.NewClock(60),
		fps:          60,
		budget:       motion.NewBudget(),
		views:        map[string]*threadView{},
		drafts:       map[string]draft{},
		files:        map[string][]string{},
		filesLoading: map[string]bool{},
		unread:       map[string]bool{},
		newKind:      cfg.DefaultAgent,
		newDir:       dir,
		newModel:     map[string]string{},
		newDefault:   map[string]bool{},
		noted:        map[string]agent.Mode{},
		seenGlobal:   map[string]bool{},
		fight:        fightState{anim: yolo.New(dark)},
		modeFlash:    motion.NewFade(flashLife, motion.EaseOutCubic),
		barY:         motion.Glide.New(60),
		switchX:      motion.Snappy.New(60),
		switchFade:   motion.NewFade(150*time.Millisecond, motion.EaseOutCubic),
		logo:         logo.New(dark),
		keyNow:       opts.KeyClock,
		clipSave:     opts.ClipSave,
		queue:        map[string][]session.QueuedItem{},
		steering:     map[string]bool{},
		qFocus:       -1,
		chgSum:       map[string]changeSum{},
		chgSumSeq:    map[string]int{},
		w:            80,
		h:            24,
	}
	m.setupLogos(opts.Images)
	m.menu.h = motion.Soft.New(60)
	m.ov.h = motion.Soft.New(60)
	m.pick.h = motion.Soft.New(60)
	m.md = newMD(m.dark)
	m.introTag = motion.NewFade(introTag, motion.EaseOutCubic)
	if m.newKind == "" {
		m.newKind = "claude"
	}
	if !motion.Reduced() && cfg.Motion == "off" {
		motion.SetReduced(true)
	}
	m.refresh()
	m.g = m.geo()
	return m
}

// Run runs the app full-screen with mouse support. All-motion mouse reporting
// is on so the logo can ripple under the pointer; moves are ignored cheaply
// everywhere else.
func Run(mgr *session.Manager, cfg config.Config) error {
	p := tea.NewProgram(New(mgr, cfg), tea.WithAltScreen(), tea.WithMouseAllMotion(), tea.WithReportFocus())
	_, err := p.Run()
	return err
}

// Init implements tea.Model.
func (m *model) Init() tea.Cmd {
	m.now = m.nowFn()
	go m.md.warm()
	return tea.Batch(m.listen(), m.wake())
}

func (m *model) listen() tea.Cmd {
	if m.noListen {
		return nil
	}
	return m.mgr.Listen()
}

// ---- state ----

// refresh reloads the threads and members from the manager, keeps the selection
// valid and decides which threads the sidebar lists: the ones somebody has
// joined, and the selected one.
func (m *model) refresh() {
	m.threads = m.mgr.Threads()
	m.agents = m.agents[:0:0]
	for _, t := range m.threads {
		m.agents = append(m.agents, t.Members...)
	}
	if m.sel != "" {
		found := false
		for _, t := range m.threads {
			if t.ID == m.sel {
				found = true
			}
		}
		if !found {
			m.sel = ""
		}
	}
	if m.sel == "" {
		for _, t := range m.threads {
			if len(t.Members) > 0 {
				m.sel = t.ID
				break
			}
		}
		if m.sel == "" && len(m.threads) > 0 {
			m.sel = m.threads[len(m.threads)-1].ID
		}
	}
	m.rows = m.rows[:0:0]
	for _, t := range m.threads {
		if len(t.Members) > 0 || t.ID == m.sel {
			m.rows = append(m.rows, t)
		}
	}
	sortRows(m.rows)
}

func (m *model) hasContent() bool {
	for _, t := range m.threads {
		if len(t.Members) > 0 {
			return true
		}
	}
	return false
}

// selThread is the selected thread, if there is one.
func (m *model) selThread() (session.ThreadInfo, bool) {
	for _, t := range m.threads {
		if t.ID == m.sel {
			return t, true
		}
	}
	return session.ThreadInfo{}, false
}

func (m *model) curThread() session.ThreadInfo {
	th, _ := m.selThread()
	return th
}

// curView is the transcript on screen, nil in the empty state.
func (m *model) curView() *threadView {
	if m.emptyVisible() {
		return nil
	}
	return m.view(m.sel)
}

func (m *model) view(id string) *threadView {
	v := m.views[id]
	if v == nil {
		v = newView(id)
		m.views[id] = v
	}
	return v
}

func (m *model) pendingFor(threadID string) []*pendingApproval {
	var out []*pendingApproval
	for _, p := range m.pending {
		if p.thread == threadID {
			out = append(out, p)
		}
	}
	return out
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
	qY, qRows    int // queue rows above the attachment line
	attY, attH   int // the attachment line (attH 0 or 1)
	footY        int
	sidebar      bool
}

func (m *model) geo() geom {
	g := geom{w: m.w, h: m.h}
	if m.w >= sidebarMinWidth && m.hasContent() {
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
	// the queue and the attachment line take rows from the transcript, but never
	// all of it
	room := max(0, g.chipY-1-g.topY-3)
	if len(m.attach) > 0 && room > 0 {
		g.attH = 1
		room--
	}
	if !m.emptyVisible() {
		g.qRows = min(m.queueRowCount(), room)
	}
	g.attY = g.chipY - g.attH
	g.qY = g.attY - g.qRows
	g.ruleY = g.qY - 1
	g.trH = g.ruleY - g.topY
	if g.trH < 1 {
		g.trH = 1
	}
	return g
}

// settle recomputes everything derived from state after any change.
func (m *model) settle() {
	m.stepFight()
	m.refresh()
	m.g = m.geo()
	g := m.g

	// sidebar bar target
	if i := m.selIndex(); i >= 0 {
		m.barY.Set(float64(i))
	}
	if m.ov.open {
		m.clampOverlay()
		m.ov.h.Set(float64(m.overlayTargetHeight(g.mainW, max(3, g.trH-1))))
	}
	if m.emptyVisible() {
		p := m.planEmpty(g.mainW, g.trH)
		m.logoX, m.logoY = g.mainX+p.left, g.topY+p.top
		if !m.logoStarted {
			m.startIntro(m.now)
		}
		return
	}
	m.logoStarted, m.logoMoving = false, false
	if v := m.curView(); v != nil {
		m.compose(v, m.curThread(), g.mainW-1)
		v.layoutScroll(g.trH)
	}
}

// ---- motion ----

// motionState reports whether anything needs fast (spring) frames, the logo's
// frames, or only slow ambient ones (spinner, pulse, cursor).
func (m *model) motionState() (fast, lg, ambient bool) {
	now := m.now
	if m.barY.Moving() || m.switchX.Moving() || m.switchFade.Moving(now) || m.menu.h.Moving() ||
		m.ov.h.Moving() || m.introMoving() || m.toast.moving(now) ||
		m.pick.h.Moving() || m.fight.active || m.modeFlash.Moving(now) {
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
	if m.emptyVisible() && m.logoMoving {
		lg = true
	}
	for _, a := range m.agents {
		if working(a) {
			ambient = true
		}
	}
	return fast, lg, ambient
}

func fpsFor(fast, lg bool) int {
	switch {
	case fast:
		return 60
	case lg:
		return 30
	}
	return 15
}

// wake starts the clock if anything is moving.
func (m *model) wake() tea.Cmd {
	fast, lg, amb := m.motionState()
	if !fast && !lg && !amb {
		return nil
	}
	want := fpsFor(fast, lg)
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
	m.pick.h.Update()
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
	m.now = m.nowFn()
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height

	case tea.KeyMsg:
		if m.emptyVisible() { // any key settles the logo; the composer owns the keyboard
			m.logo.Calm(m.now)
		}
		m.skipFight() // any key ends the yolo fight; the key still does its own job
		cmds = append(cmds, m.handleKey(msg)...)

	case tea.MouseMsg:
		switch msg.Action {
		case tea.MouseActionMotion:
			return m, m.logoMouse(msg, false) // cheap: nothing else cares about moves
		case tea.MouseActionRelease:
			return m, nil
		}
		m.skipFight()
		cmds = append(cmds, m.handleMouse(msg)...)

	case motion.FrameMsg:
		if !m.clock.Accept(msg) {
			return m, nil
		}
		m.advance()
		if m.emptyVisible() && m.logoStarted {
			m.logoMoving = m.logo.Advance(msg.At)
		}
		m.settle()
		fast, lg, amb := m.motionState()
		if fast || lg || amb {
			if want := fpsFor(fast, lg); want != m.fps {
				m.fps = want
				m.clock.SetFPS(want)
			}
			m.clock.SetMoving(true)
		} else {
			m.clock.SetMoving(false)
		}
		return m, m.clock.Tick()

	case burstCheckMsg:
		cmds = append(cmds, m.onBurstCheck()...)

	case clipImgMsg:
		cmds = append(cmds, m.onClipImg(msg)...)

	case steerDoneMsg:
		cmds = append(cmds, m.onSteerDone(msg)...)

	case session.EventMsg, session.RelayMsg, session.PausedMsg, session.ContinuedMsg,
		session.MemberAddedMsg, session.NoticeMsg, session.ApprovalMsg, session.ApprovalGoneMsg,
		session.ModeChangedMsg, session.ModelChangedMsg, session.AutoApprovedMsg, session.AutoDeniedMsg,
		session.QueueChangedMsg, session.EditedMsg, session.RevertedMsg, session.ThreadChangedMsg:
		cmds = append(cmds, m.dispatch(msg, false)...)
		cmds = append(cmds, m.listen())

	case changesMsg:
		m.onChanges(msg)

	case changeSumMsg:
		m.onChangeSum(msg)

	case revertDoneMsg:
		cmds = append(cmds, m.onRevertDone(msg)...)

	case modelsMsg:
		m.onModels(msg)

	case sentMsg:
		if msg.err != nil {
			cmds = append(cmds, m.toast.show(msg.err.Error(), true, m.now))
		}

	case resumedMsg:
		cmds = append(cmds, m.onResumed(msg)...)
	case forkedMsg:
		cmds = append(cmds, m.onForked(msg)...)
	case movedMsg:
		cmds = append(cmds, m.onMoved(msg)...)
	case appliedMsg:
		cmds = append(cmds, m.onApplied(msg)...)
	case filesMsg:
		m.onFiles(msg)

	case toastFadeMsg:
		// the fade itself is time-based; this only wakes the clock
	case toastExpireMsg:
		if msg.seq == m.toast.seq {
			m.toast.on = false
		}

	case quitDisarmMsg:
		// nothing: armed state expires by time

	case tea.QuitMsg:

	case tea.FocusMsg:
		m.blurred = false
	case tea.BlurMsg:
		m.blurred = true
	}

	m.settle()
	if c := m.titleCmd(); c != nil {
		cmds = append(cmds, c)
	}
	if c := m.filesCmd(); c != nil {
		cmds = append(cmds, c)
	}
	cmds = append(cmds, m.wake())
	return m, tea.Batch(cmds...)
}

// ---- View ----

// View implements tea.Model.
func (m *model) View() string {
	start := time.Now()
	defer func() {
		if !m.fightShown() { // the yolo blast is heavy on purpose; it must not turn motion off
			m.budget.Observe(time.Since(start))
		}
	}()

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
	if m.kittyPending && len(rows) > 0 { // send the logos once, on this screen
		m.kittyPending = false
		rows[0] = m.kittyTransmit() + rows[0]
	}
	screen := strings.Join(rows, "\n")
	if m.fightShown() { // switching to yolo: the blast wave covers the whole screen
		return m.fight.anim.Apply(screen, m.w, m.now)
	}
	return screen
}

func (m *model) headerLine(g geom) string {
	left := " " + sAccent.Render(theme.GlyphAgent) + " " + sBold.Render(theme.Wordmark)
	if m.emptyVisible() { // the empty state draws its own logo
		left = " "
	}
	right, _, _ := m.headerRight()
	if right == "" {
		return trunc(left, m.w)
	}
	if left == " " {
		return padTo("", m.w-sw(right)-1) + right + " "
	}
	return rightAlign(left, right+" ", m.w)
}

// headerRight is the right side of the header, and the cells [x0, x1) of it
// the changes summary takes (x1 is 0 when it is not shown).
func (m *model) headerRight() (right string, x0, x1 int) {
	nWorking, waiting := 0, 0
	cost := 0.0
	for _, a := range m.agents {
		if working(a) {
			nWorking++
		}
		waiting += a.Pending
		cost += a.CostUSD
	}
	var parts []string
	switch {
	case nWorking > 0:
		parts = append(parts, sMuted.Render(plural(nWorking, "agent working", "agents working")))
	case len(m.agents) > 0 && waiting == 0:
		parts = append(parts, sMuted.Render("idle"))
	}
	if waiting > 0 {
		parts = append(parts, sWarn.Render(plural(waiting, "waiting", "waiting")))
	}
	sum := -1
	if s := m.changeSumText(); s != "" {
		sum = len(parts)
		parts = append(parts, s)
	}
	if c := fmtCost(cost); c != "" {
		parts = append(parts, sMuted.Render(c))
	}
	sep := sFaint.Render(" " + glyphDot + " ")
	for i, p := range parts {
		if i == sum {
			x0 = sw(right)
			x1 = x0 + sw(p)
		}
		if i > 0 {
			right += sep
			if i == sum {
				x0, x1 = x0+sw(sep), x1+sw(sep)
			}
		}
		right += p
	}
	return right, x0, x1
}

// overChangeSum reports whether screen column x of the header is on the
// changes summary.
func (m *model) overChangeSum(x int) bool {
	right, x0, x1 := m.headerRight()
	if x1 == 0 {
		return false
	}
	start := m.w - 1 - sw(right)
	return x >= start+x0 && x < start+x1
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
		if pill := m.newPill(v, g.trH); pill != "" && len(tr) > 0 {
			last := len(tr) - 1
			tr[last] = padTo(trunc(tr[last], w-sw(pill)-2), w-sw(pill)-1) + pill
		}
	}
	// overlays rise from the composer
	var ov []string
	if m.ov.visible() {
		ov = m.overlayLines(w, max(3, g.trH-1))
	} else if m.pick.visible() {
		ov = m.pickLines(w)
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
	out = append(out, m.ruleStyle().Render(strings.Repeat(theme.GlyphRule, w)))
	if g.qRows > 0 {
		out = append(out, m.queueLines(w)[:g.qRows]...)
	}
	if g.attH > 0 {
		al, _ := m.attachLayout(w)
		out = append(out, al)
	}
	out = append(out, m.chipLine(w))
	out = append(out, m.composerLines(w)...)
	return out
}

// applySwitch slides and fades the transcript when the selected thread changes.
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
	if m.newDefault[kind] {
		return ""
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
	if m.pick.open {
		prompt = sAccent.Render(theme.GlyphAgent + " ")
	}
	return m.comp.View(w, prompt, ph, m.focus == focusComposer && !m.ov.open)
}

// defaultTarget is who an unaddressed message goes to, mirroring Post.
func (m *model) defaultTarget() (session.AgentInfo, bool) {
	th, _ := m.selThread()
	for _, a := range th.Members {
		if a.ID == th.LastSpeaker && a.Status != session.StatusExited {
			return a, true
		}
	}
	for _, a := range th.Members {
		if a.Status != session.StatusExited {
			return a, true
		}
	}
	return session.AgentInfo{}, false
}

func (m *model) placeholder() string {
	switch {
	case m.prompt.active():
		return m.prompt.placeholder()
	case m.pick.open && m.pick.stage == pickThreads:
		return "find a thread"
	case m.pick.open:
		return "filter, or type a model id"
	case m.emptyVisible():
		return "ask " + m.newKind + "…"
	}
	if a, ok := m.defaultTarget(); ok {
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
	th := m.curThread()
	switch {
	case m.ov.open && m.ov.kind == ovResume:
		return h("↑↓ select", "enter resume", "esc close")
	case m.ov.open && m.ov.kind == ovChanges && m.chg.set.Git:
		return h("↑↓ file", "u undo", "esc close")
	case m.ov.open && m.ov.kind == ovChanges:
		return h("↑↓ file", "esc close")
	case m.ov.open && m.ov.kind == ovKeys:
		return h("any key closes")
	case m.ov.open:
		return h("y allow", "n deny", "a always", "esc close")
	case m.prompt.active():
		return h("enter send", "esc cancel")
	case m.edit != nil:
		return h("enter resend", "esc cancel")
	case m.qFocus >= 0:
		return h("enter steer", "del cancel", "⌥↑↓ rows", "esc back")
	case m.pick.open && m.pick.stage == pickThreads:
		return h("↑↓ select", "enter open", "1-9 jump", "type to filter", "esc close")
	case m.pick.open:
		return h("↑↓ select", "enter pick", "1-9 jump", "type to filter or any model id", "esc close")
	case m.menu.open():
		return h("↑↓ select", "tab complete", "esc close")
	case m.focus == focusTranscript:
		return h("enter fold", "e edit", "d changes", "p pin", "x resolve", "r rename", "? keys", "tab compose")
	case m.emptyVisible():
		return h("enter start "+m.newKind, "@ agent", "/ commands", "⇧tab mode")
	}
	if len(m.pendingFor(m.sel)) > 0 && m.comp.Empty() {
		return h("y allow", "n deny", "a always", "i inbox")
	}
	if th.Paused && m.comp.Empty() {
		return h("enter continue", "@ agent", "/ commands", "^c stop", "⇧tab mode")
	}
	if len(m.pending) > 0 {
		return h("@ agent", "/ commands", "i inbox", "^c stop", "⇧tab mode")
	}
	if q := len(m.queueOf(m.sel)); q > 0 {
		return h("@ agent", "/ commands", "⌥↑ queue: steer", "tab browse", "^c stop", "⇧tab mode", "^v image")
	}
	if m.comp.Empty() && lastUserItem(m.curView()) != nil {
		return h("@ agent", "/ commands", "↑ edit last", "⌥↑↓ threads", "tab browse", "^c stop", "⇧tab mode", "^v image")
	}
	return h("@ agent", "/ commands", "tab browse", "^c stop", "⇧tab mode", "^v image")
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

// selectThread makes id the selected thread and starts the switch animation.
func (m *model) selectThread(id string) {
	if id == m.sel {
		return
	}
	if m.fight.active && m.fight.thread != id { // the fight belongs to the thread you leave
		m.skipFight()
	}
	m.leaveThread(m.sel)
	m.sel = id
	m.enterThread(id)
	m.refresh()
	if i := m.selIndex(); i >= 0 {
		m.barY.Set(float64(i))
	}
	m.switchX.Pos = 6
	m.switchX.Set(0)
	m.switchFade.Start(m.now)
	m.view(id)
}
