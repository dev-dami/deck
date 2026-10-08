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
)

// item is one block of a transcript.
type item struct {
	kind itemKind
	text string

	tool agent.Tool
	diff *agent.FileDiff

	streaming bool // text/reasoning still arriving
	expanded  bool
	h         motion.Spring // visible body lines
	mdFade    motion.Fade

	ver   int
	cache lineCache
	body  lineCache
}

type lineCacheKey struct {
	w, ver, hpos int
	dark, cursor bool
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

func (it *item) agentSide() bool {
	return it.kind == kText || it.kind == kReasoning || it.kind == kTool
}

func (it *item) rowLike() bool {
	switch it.kind {
	case kTool, kReasoning, kError, kNote, kExit:
		return true
	}
	return false
}

// foldable rows toggle on Enter or click.
func (it *item) foldable() bool {
	switch it.kind {
	case kTool, kReasoning:
		return true
	case kExit:
		return strings.Contains(strings.TrimSpace(it.text), "\n")
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
	w      int // width of a transcript line
	tw     int // width of prose, including its 2-col indent
	now    time.Time
	cursor bool // this item holds the row cursor in a focused transcript
}

func (m *model) newRctx(w int) *rctx {
	tw := w
	if tw > maxTextWidth {
		tw = maxTextWidth
	}
	return &rctx{m: m, w: w, tw: tw, now: m.now}
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
	}
	return nil
}

func (it *item) renderUser(c *rctx) []string {
	key := lineCacheKey{w: c.w, ver: it.ver, dark: c.m.dark}
	if it.cache.ok && it.cache.key == key {
		return it.cache.lines
	}
	out := []string{"  " + sBold.Render(trunc(c.m.cfg.UserName, c.w-2))}
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

// ---- agent view ----

// agentView is everything the UI keeps for one agent.
type agentView struct {
	id string

	items  []*item
	open   *item // streaming assistant text
	openR  *item // streaming reasoning
	tools  map[string]*item
	cursor *item

	scroll motion.Spring
	follow bool
	laid   bool

	lines  []string
	owners []*item

	flash motion.Fade

	turnCost float64
	files    map[string]bool
}

func newView(id string) *agentView {
	return &agentView{
		id:     id,
		tools:  map[string]*item{},
		scroll: motion.Glide.New(60),
		follow: true,
		flash:  motion.NewFade(700*time.Millisecond, motion.EaseOutCubic),
		files:  map[string]bool{},
	}
}

func (v *agentView) add(it *item) *item {
	v.items = append(v.items, it)
	return it
}

func (v *agentView) foldables() []*item {
	var out []*item
	for _, it := range v.items {
		if it.foldable() {
			out = append(out, it)
		}
	}
	return out
}

func (v *agentView) cursorItem() *item {
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
func (v *agentView) moveCursor(d int) *item {
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

func (v *agentView) anyMoving(now time.Time) bool {
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

func (v *agentView) anyAmbient() bool {
	for i := len(v.items) - 1; i >= 0 && i >= len(v.items)-40; i-- {
		if v.items[i].ambient() {
			return true
		}
	}
	return false
}

func (v *agentView) advance() {
	v.scroll.Update()
	for _, it := range v.items {
		it.h.Update()
	}
}

func gap(prev, cur *item) bool { return !(prev.rowLike() && cur.rowLike()) }

// compose lays out the whole transcript for the view at width w.
func (m *model) compose(v *agentView, info session.AgentInfo, w int) {
	c := m.newRctx(w)
	var lines []string
	var owners []*item
	add := func(ls []string, it *item) {
		for _, l := range ls {
			lines = append(lines, l)
			owners = append(owners, it)
		}
	}
	agentLabel := sAccent.Render(theme.GlyphAgent) + " " + sBold.Render(trunc(info.Name, w-2))
	focusedCursor := m.focus == focusTranscript && !m.hasOverlay()
	cur := v.cursorItem()

	var prev *item
	for _, it := range v.items {
		if prev != nil && gap(prev, it) {
			add([]string{""}, nil)
		}
		if it.agentSide() && (prev == nil || prev.kind == kUser) {
			add([]string{agentLabel}, nil)
		}
		c.cursor = focusedCursor && it == cur
		ls := it.render(c)
		if c.cursor && len(ls) > 0 {
			ls = append([]string(nil), ls...)
			ls[0] = sAccent.Render(glyphBarSel) + strings.TrimPrefix(ls[0], " ")
		}
		add(ls, it)
		prev = it
	}
	working := info.Status == session.StatusWorking || info.Status == session.StatusTool
	if working && (prev == nil || prev.kind == kUser) {
		if prev != nil {
			add([]string{"", agentLabel}, nil)
		}
		add([]string{"  " + sMuted.Render(motion.NewSpinner().Frame(m.now)) + sFaint.Render(" working…")}, nil)
	}
	if pend := m.pendingFor(v.id); len(pend) > 0 {
		if len(lines) > 0 {
			add([]string{""}, nil)
		}
		add(m.renderApproval(pend[0], w, true, len(pend)), nil)
	}
	v.lines, v.owners = lines, owners
}

// layoutScroll updates the scroll target for the current content and viewport.
func (v *agentView) layoutScroll(vh int) {
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

func (v *agentView) top() int { return int(math.Round(v.scroll.Pos)) }

// scrollBy moves the scroll target by n lines; reaching the bottom re-follows.
func (v *agentView) scrollBy(n, vh int) {
	maxTop := max(0, len(v.lines)-vh)
	t := int(math.Round(v.scroll.Target)) + n
	t = max(0, min(maxTop, t))
	v.follow = t >= maxTop
	v.scroll.Set(float64(t))
}

func (v *agentView) scrollTo(bottom bool, vh int) {
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
func (v *agentView) ensureVisible(li, vh int) {
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

func (v *agentView) lineOf(it *item) int {
	for i, o := range v.owners {
		if o == it {
			return i
		}
	}
	return -1
}

// window returns exactly vh lines of the transcript.
func (v *agentView) window(vh int) []string {
	top := v.top()
	out := make([]string, vh)
	for i := 0; i < vh; i++ {
		if top+i >= 0 && top+i < len(v.lines) {
			out[i] = v.lines[top+i]
		}
	}
	return out
}
