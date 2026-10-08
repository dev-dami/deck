package tui

import (
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/tui/motion"
	"github.com/dev-dami/deck/internal/tui/theme"
)

type itemKind int

const (
	kUser itemKind = iota
	kText
	kReasoning
	kTool
	kError
	kNote
	kExit
	kRead   // an agent read the shared thread
	kRelay  // one agent's message to others
	kPause  // the hop limit paused the thread
	kJoin   // an agent joined
	kNotice // a note from Deck itself
	kMode   // the permission mode changed
	kModel  // a member switched model
	kAuto   // Deck answered an approval itself (yolo, plan)
)

type pauseState int

const (
	pauseActive pauseState = iota // waiting for Continue
	pauseDone                     // continued
	pauseStale                    // replayed from history; nothing is waiting any more
)

// item is one block of a transcript.
type item struct {
	kind itemKind
	text string

	// speaker is the agent id of everything an agent says or does; empty for
	// the user's messages and for the thread's own lines (relays, pauses, ...).
	speaker string

	to       []string // user messages: members it was addressed to
	nmembers int      // user messages: how many members the thread had then

	tool agent.Tool
	diff *agent.FileDiff

	from string   // relays: the sender
	rto  []string // relays: the receivers
	via  string
	held bool

	hops   int
	pstate pauseState

	joinID string // join lines: who joined; model lines: whose model

	mode   agent.Mode // mode lines
	denied bool       // auto lines: declined rather than approved

	streaming bool // text/reasoning still arriving
	expanded  bool
	overflow  bool          // relays: the text does not fit its collapsed height
	h         motion.Spring // visible body lines
	mdFade    motion.Fade

	ver   int
	cache lineCache
	body  lineCache
}

type lineCacheKey struct {
	w, ver, hpos int
	dark, cursor bool
	tag          string
}

type lineCache struct {
	key   lineCacheKey
	lines []string
	ok    bool
}

func newItem(k itemKind) *item {
	return &item{kind: k, h: motion.Snappy.New(60), mdFade: motion.NewFade(120*time.Millisecond, motion.EaseOutCubic)}
}

func (it *item) bump() { it.ver++ }

// rowLike items are the small one-line rows that sit under their message.
func (it *item) rowLike() bool {
	switch it.kind {
	case kTool, kReasoning, kError, kNote, kExit, kRead, kAuto:
		return true
	}
	return false
}

// quiet items are the thread's own faint one-liners.
func (it *item) quiet() bool {
	return it.kind == kJoin || it.kind == kNotice || it.kind == kMode || it.kind == kModel
}

// foldable rows toggle on Enter or click.
func (it *item) foldable() bool {
	switch it.kind {
	case kTool, kReasoning:
		return true
	case kExit:
		return strings.Contains(strings.TrimSpace(it.text), "\n")
	case kRelay:
		return it.overflow || it.expanded
	}
	return false
}

// moving reports whether the item is mid-animation.
func (it *item) moving(now time.Time) bool {
	return it.h.Moving() || it.mdFade.Moving(now)
}

// ambient reports whether the item shows something that changes with the clock
// alone (spinner, cursor).
func (it *item) ambient() bool {
	switch it.kind {
	case kText:
		return it.streaming
	case kTool:
		return it.tool.Status == agent.ToolRunning
	}
	return false
}

// rctx carries everything an item needs to render.
type rctx struct {
	m      *model
	th     session.ThreadInfo
	w      int // width of a transcript line
	tw     int // width of prose, including its 2-col indent
	now    time.Time
	cursor bool // this item holds the row cursor in a focused transcript
}

func (m *model) newRctx(w int, th session.ThreadInfo) *rctx {
	tw := w
	if tw > maxTextWidth {
		tw = maxTextWidth
	}
	return &rctx{m: m, th: th, w: w, tw: tw, now: m.now}
}

// render returns the item's styled lines.
func (it *item) render(c *rctx) []string {
	switch it.kind {
	case kUser:
		return it.renderUser(c)
	case kText:
		return it.renderText(c)
	case kReasoning, kTool, kExit:
		return it.renderFold(c)
	case kError:
		ls := wrapText(it.text, c.tw-4)
		out := make([]string, 0, len(ls))
		for i, l := range ls {
			g := "  "
			if i == 0 {
				g = glyphErr + " "
			}
			out = append(out, "  "+sDanger.Render(g+l))
		}
		return out
	case kNote:
		return []string{"  " + sFaint.Render(trunc(oneLine(it.text), c.w-2))}
	case kRead:
		return []string{"  " + sFaint.Render(glyphThink+" read the thread")}
	case kRelay:
		return it.renderRelay(c)
	case kPause:
		return it.renderPause(c)
	case kJoin:
		return []string{it.renderJoin(c)}
	case kNotice:
		ls := wrapText(it.text, c.tw-2)
		out := make([]string, len(ls))
		for i, l := range ls {
			out[i] = "  " + sItalic.Render(l)
		}
		return out
	case kMode:
		return it.renderMode(c)
	case kModel:
		return it.renderModel(c)
	case kAuto:
		return it.renderAuto(c)
	}
	return nil
}

// addressee is the faint "→ codex" after the user's name: who the message went
// to. A one-member thread has nothing to say.
func (c *rctx) addressee(it *item) string {
	if it.nmembers < 2 || len(it.to) == 0 {
		return ""
	}
	if len(it.to) >= it.nmembers {
		return "all"
	}
	return strings.Join(c.m.agentNames(it.to), ", ")
}

func (it *item) renderUser(c *rctx) []string {
	name, to := c.m.cfg.UserName, c.addressee(it)
	key := lineCacheKey{w: c.w, ver: it.ver, dark: c.m.dark, tag: name + "\x00" + to}
	if it.cache.ok && it.cache.key == key {
		return it.cache.lines
	}
	label := sBold.Render(trunc(name, c.w-2))
	if to != "" {
		label += sFaint.Render(" " + trunc("→ "+to, max(4, c.w-4-sw(name))))
	}
	out := []string{"  " + label}
	for _, l := range wrapText(it.text, c.tw-2) {
		out = append(out, "  "+sFg.Render(l))
	}
	it.cache = lineCache{key: key, lines: out, ok: true}
	return out
}

func (it *item) renderText(c *rctx) []string {
	now := c.now
	if it.streaming {
		return it.renderStreaming(c)
	}
	fading := it.mdFade.Started() && !it.mdFade.Done(now)
	key := lineCacheKey{w: c.w, ver: it.ver, dark: c.m.dark}
	if !fading && it.cache.ok && it.cache.key == key {
		return it.cache.lines
	}
	ls := c.m.md.lines(it.text, c.tw-2)
	if ls == nil {
		for _, l := range wrapText(it.text, c.tw-2) {
			ls = append(ls, sFg.Render(l))
		}
	}
	out := make([]string, len(ls))
	if fading {
		s := c.m.ramp(theme.Faint, theme.Fg, it.mdFade.Progress(now))
		for i, l := range ls {
			out[i] = "  " + s.Render(ansi.Strip(l))
		}
		return out
	}
	for i, l := range ls {
		if l == "" {
			continue
		}
		out[i] = "  " + l
	}
	it.cache = lineCache{key: key, lines: out, ok: true}
	return out
}

func (it *item) renderStreaming(c *rctx) []string {
	ls := wrapText(it.text, c.tw-2)
	cur := c.m.cursorGlyph()
	if len(ls) == 0 {
		return []string{"  " + cur}
	}
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = "  " + sFg.Render(l)
	}
	if strings.HasSuffix(it.text, "\n") {
		return append(out, "  "+cur)
	}
	out[len(out)-1] += cur
	return out
}

// cursorGlyph is the streaming cursor: an accent bar that breathes gently.
func (m *model) cursorGlyph() string {
	p := motion.Pulse(m.now, motion.PulsePeriod)
	return lipglossFg(m.blend(theme.Faint, theme.Accent, 0.35+0.65*p)).Render(theme.GlyphCursor)
}

// ---- thread view ----

// turnStats is what one agent's running turn has done so far, for the toast.
type turnStats struct {
	cost  float64
	files map[string]bool
}

// threadView is everything the UI keeps for one thread.
type threadView struct {
	id string

	items  []*item
	open   map[string]*item // agent id -> its streaming assistant text
	openR  map[string]*item // agent id -> its streaming reasoning
	tools  map[string]*item // agent id/tool id -> row
	hide   map[string]bool  // agent id/tool id of tools that are not shown
	cursor *item

	scroll motion.Spring
	follow bool
	laid   bool

	lines  []string
	owners []*item

	flash motion.Fade

	stats map[string]*turnStats
	known map[string]string // agent id -> kind, for everyone who has joined
	order []string          // join order
}

func newView(id string) *threadView {
	return &threadView{
		id:     id,
		open:   map[string]*item{},
		openR:  map[string]*item{},
		tools:  map[string]*item{},
		hide:   map[string]bool{},
		scroll: motion.Glide.New(60),
		follow: true,
		flash:  motion.NewFade(700*time.Millisecond, motion.EaseOutCubic),
		stats:  map[string]*turnStats{},
		known:  map[string]string{},
	}
}

func (v *threadView) turn(id string) *turnStats {
	s := v.stats[id]
	if s == nil {
		s = &turnStats{files: map[string]bool{}}
		v.stats[id] = s
	}
	return s
}

func (v *threadView) add(it *item) *item {
	v.items = append(v.items, it)
	return it
}

func (v *threadView) foldables() []*item {
	var out []*item
	for _, it := range v.items {
		if it.foldable() {
			out = append(out, it)
		}
	}
	return out
}

func (v *threadView) cursorItem() *item {
	fs := v.foldables()
	if len(fs) == 0 {
		return nil
	}
	for _, f := range fs {
		if f == v.cursor {
			return f
		}
	}
	return fs[len(fs)-1]
}

// moveCursor moves the row cursor by d foldable rows and keeps it on screen.
func (v *threadView) moveCursor(d int) *item {
	fs := v.foldables()
	if len(fs) == 0 {
		return nil
	}
	cur := v.cursorItem()
	idx := 0
	for i, f := range fs {
		if f == cur {
			idx = i
		}
	}
	idx = max(0, min(len(fs)-1, idx+d))
	v.cursor = fs[idx]
	return v.cursor
}

func (v *threadView) anyMoving(now time.Time) bool {
	if v.scroll.Moving() || v.flash.Moving(now) {
		return true
	}
	for _, it := range v.items {
		if it.moving(now) {
			return true
		}
	}
	return false
}

func (v *threadView) anyAmbient() bool {
	for i := len(v.items) - 1; i >= 0 && i >= len(v.items)-40; i-- {
		if v.items[i].ambient() {
			return true
		}
	}
	return false
}

func (v *threadView) advance() {
	v.scroll.Update()
	for _, it := range v.items {
		it.h.Update()
	}
}

// gap reports whether one blank line separates two consecutive items. Rows
// (tools, thinking, notes) sit directly under their own speaker's message and
// under each other; the thread's quiet one-liners sit together.
func gap(prev, cur *item) bool {
	if cur.rowLike() && cur.speaker == prev.speaker && (prev.rowLike() || prev.kind == kText) {
		return false
	}
	if cur.quiet() && prev.quiet() {
		return false
	}
	return true
}

// compose lays out the whole transcript for the view at width w.
func (m *model) compose(v *threadView, th session.ThreadInfo, w int) {
	c := m.newRctx(w, th)
	var lines []string
	var owners []*item
	add := func(ls []string, it *item) {
		for _, l := range ls {
			lines = append(lines, l)
			owners = append(owners, it)
		}
	}
	// blank adds the one faint blank line between messages, never two in a row.
	blank := func() {
		if n := len(lines); n > 0 && lines[n-1] != "" {
			add([]string{""}, nil)
		}
	}
	focusedCursor := m.focus == focusTranscript && !m.hasOverlay()
	cur := v.cursorItem()

	var prev *item
	last := "" // the speaker of the block at the bottom
	for _, it := range v.items {
		if prev != nil && gap(prev, it) {
			blank()
		}
		if it.speaker != "" && (prev == nil || prev.speaker != it.speaker) {
			add([]string{m.speakerLabel(it.speaker, w)}, nil)
		}
		c.cursor = focusedCursor && it == cur
		ls := it.render(c)
		if c.cursor && len(ls) > 0 {
			ls = append([]string(nil), ls...)
			ls[0] = sAccent.Render(glyphBarSel) + strings.TrimPrefix(ls[0], " ")
		}
		add(ls, it)
		prev = it
		last = it.speaker
	}
	// members that are working but have nothing at the bottom yet
	for _, a := range th.Members {
		if !working(a) || a.ID == last {
			continue
		}
		blank()
		add([]string{m.speakerLabel(a.ID, w)}, nil)
		add([]string{"  " + sMuted.Render(motion.NewSpinner().Frame(m.now)) + sFaint.Render(" working…")}, nil)
		last = a.ID
	}
	if pend := m.pendingFor(v.id); len(pend) > 0 {
		blank()
		if who := pend[0].req.AgentID; who != last {
			add([]string{m.speakerLabel(who, w)}, nil)
		}
		add(m.renderApproval(pend[0], w, true, len(pend)), nil)
	}
	v.lines, v.owners = lines, owners
}

// layoutScroll updates the scroll target for the current content and viewport.
func (v *threadView) layoutScroll(vh int) {
	maxTop := max(0, len(v.lines)-vh)
	if !v.laid {
		v.scroll.Target = float64(maxTop)
		v.scroll.Snap()
		v.laid = true
	}
	if v.follow {
		v.scroll.Set(float64(maxTop))
	} else {
		v.scroll.Set(math.Max(0, math.Min(v.scroll.Target, float64(maxTop))))
	}
	if v.scroll.Pos > float64(maxTop) {
		v.scroll.Pos = float64(maxTop)
	}
	if v.scroll.Pos < 0 {
		v.scroll.Pos = 0
	}
}

func (v *threadView) top() int { return int(math.Round(v.scroll.Pos)) }

// scrollBy moves the scroll target by n lines; reaching the bottom re-follows.
func (v *threadView) scrollBy(n, vh int) {
	maxTop := max(0, len(v.lines)-vh)
	t := int(math.Round(v.scroll.Target)) + n
	t = max(0, min(maxTop, t))
	v.follow = t >= maxTop
	v.scroll.Set(float64(t))
}

func (v *threadView) scrollTo(bottom bool, vh int) {
	maxTop := max(0, len(v.lines)-vh)
	if bottom {
		v.follow = true
		v.scroll.Set(float64(maxTop))
		return
	}
	v.follow = maxTop == 0
	v.scroll.Set(0)
}

// ensureVisible scrolls so line index li is on screen.
func (v *threadView) ensureVisible(li, vh int) {
	top := int(math.Round(v.scroll.Target))
	switch {
	case li < top:
		v.follow = false
		v.scroll.Set(float64(li))
	case li >= top+vh:
		t := li - vh + 1
		v.follow = t >= max(0, len(v.lines)-vh)
		v.scroll.Set(float64(t))
	}
}

func (v *threadView) lineOf(it *item) int {
	for i, o := range v.owners {
		if o == it {
			return i
		}
	}
	return -1
}

// window returns exactly vh lines of the transcript.
func (v *threadView) window(vh int) []string {
	top, pad := v.top(), v.padTop(vh)
	out := make([]string, vh)
	for i := pad; i < vh; i++ {
		if li := top + i - pad; li >= 0 && li < len(v.lines) {
			out[i] = v.lines[li]
		}
	}
	return out
}

// padTop is how many blank rows sit above a transcript shorter than the pane:
// the transcript is bottom-anchored so the latest message rests on the rule.
func (v *threadView) padTop(vh int) int { return max(0, vh-len(v.lines)) }
