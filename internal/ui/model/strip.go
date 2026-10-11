package model

import (
	"slices"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/dev-dami/deck/internal/config"
)

// The strip: the sidebar's columns of panes (see pane.go). This file holds
// its state and the pure layout and editing logic; strip_draw.go paints it
// and strip_keys.go wires keys, mouse and persistence into the UI.

const (
	// stripColumnDefault is a column's width when no pane asks for more.
	stripColumnDefault = 34
	// stripColumnMin is the narrowest a column can be made with -.
	stripColumnMin = 20
	// stripColumnStep is how much - and = change a column's width.
	stripColumnStep = 4
	// stripGap is the space between two columns, a rule in its middle.
	stripGap = 3

	// sidebarNarrowWidth is the sidebar's width on ordinary terminals.
	sidebarNarrowWidth = 32
	// sidebarWideBreakpoint is the terminal width from which the sidebar
	// grows to show more than one column.
	sidebarWideBreakpoint = 160
	// sidebarWidePercent caps a wide sidebar's share of the window.
	sidebarWidePercent = 45
	// chatMinWidth is the narrowest the chat gets next to a wide sidebar.
	chatMinWidth = 80
	// sidebarLogoMinHeight is the sidebar height from which the logo is
	// drawn above the strip.
	sidebarLogoMinHeight = 30
)

// stripColumn is one column of the strip.
type stripColumn struct {
	panes []paneID
	// width is the width the user set with - and =, or 0 for the
	// column's natural width.
	width int
	// row is the column's focused pane, kept while focus is elsewhere so
	// coming back lands where the user was (as niri does).
	row int
}

// paneStrip is the sidebar's state.
type paneStrip struct {
	cols []stripColumn
	// live holds one pane per registered kind, open or closed, so a pane
	// keeps its state (cursor, expanded rows) across close and reopen.
	live map[paneID]pane
	// closed lists the closed panes, most recently closed last.
	closed []paneID
	// col is the focused column.
	col int
	// first is the leftmost visible column.
	first int
	// reveal asks the next layout pass to scroll the focused column into
	// view; focus changes set it, sideways wheel scrolling does not.
	reveal bool
	// scroll is the body offset of panes the strip scrolls (pane.go).
	scroll map[paneID]int
	// chooser is the reopen list while it is open.
	chooser *stripChooser
	// geo is the geometry of the last drawn frame, for the mouse and for
	// scrolling bodies.
	geo stripGeom
	// saveSeq debounces writing the layout to the config.
	saveSeq int
}

// stripChooser is the inline list of closed panes opened with o.
type stripChooser struct {
	sel int
}

// stripGeom is where the last frame put things.
type stripGeom struct {
	area uv.Rectangle // the strip, below the logo
	cols []colGeom    // the visible columns
	// indicator is the row showing columns off to the sides; empty when
	// every column is visible.
	indicator uv.Rectangle
	// chooser holds the reopen list's item rows while it is open.
	chooser uv.Rectangle
}

type colGeom struct {
	index int
	x, w  int
	panes []paneGeom
}

type paneGeom struct {
	id     paneID
	header uv.Rectangle
	body   uv.Rectangle
	// lines is how many lines the body rendered; more than body.Dy()
	// means the strip scrolls it.
	lines int
}

// newPaneStrip builds the strip from a saved layout, or from
// defaultColumns when there is none. Ids that are unknown or whose pane is
// not registered (not built into this binary) are dropped, and registered
// panes the layout does not mention are added as new columns, so a newly
// added kind of pane shows up for everyone.
func newPaneStrip(saved config.SidebarLayout) *paneStrip {
	return newPaneStripFrom(saved, paneFactories)
}

// newPaneStripFrom is newPaneStrip with the panes built by factories.
func newPaneStripFrom(saved config.SidebarLayout, factories map[paneID]func() pane) *paneStrip {
	s := &paneStrip{
		live:   make(map[paneID]pane, len(factories)),
		scroll: map[paneID]int{},
	}
	for id, f := range factories {
		s.live[id] = f()
	}
	if len(saved.Columns) == 0 && len(saved.Closed) == 0 {
		s.fromColumns(defaultColumns, nil, nil)
		return s
	}
	cols := make([][]paneID, 0, len(saved.Columns))
	for _, c := range saved.Columns {
		ids := make([]paneID, 0, len(c))
		for _, id := range c {
			ids = append(ids, paneID(id))
		}
		cols = append(cols, ids)
	}
	closed := make([]paneID, 0, len(saved.Closed))
	known := false
	for _, id := range saved.Closed {
		closed = append(closed, paneID(id))
		known = known || s.live[paneID(id)] != nil
	}
	for _, c := range cols {
		for _, id := range c {
			known = known || s.live[id] != nil
		}
	}
	if !known {
		// Nothing in the saved layout exists any more. Checked before
		// fromColumns, which gives every unmentioned pane its own column.
		s.fromColumns(defaultColumns, nil, nil)
		return s
	}
	s.fromColumns(cols, saved.Widths, closed)
	return s
}

// fromColumns sets the columns, skipping unregistered and repeated ids and
// columns left empty, then adds every registered pane that is neither
// placed nor closed as a column of its own.
func (s *paneStrip) fromColumns(cols [][]paneID, widths []int, closed []paneID) {
	seen := map[paneID]bool{}
	s.cols = nil
	for i, c := range cols {
		var col stripColumn
		for _, id := range c {
			if s.live[id] == nil || seen[id] {
				continue
			}
			seen[id] = true
			col.panes = append(col.panes, id)
		}
		if len(col.panes) == 0 {
			continue
		}
		if i < len(widths) && widths[i] > 0 {
			col.width = max(widths[i], stripColumnMin)
		}
		s.cols = append(s.cols, col)
	}
	s.closed = nil
	for _, id := range closed {
		if s.live[id] == nil || seen[id] {
			continue
		}
		seen[id] = true
		s.closed = append(s.closed, id)
	}
	for _, id := range s.registeredOrder() {
		if !seen[id] {
			s.cols = append(s.cols, stripColumn{panes: []paneID{id}})
		}
	}
	s.col, s.first = 0, 0
	s.reveal = true
}

// paneOrder is the order new kinds of panes are added in.
var paneOrder = []paneID{paneSession, paneAgents, paneGit, paneTasks, paneCommands, paneNotices, paneUsage}

// registeredOrder lists the registered panes, known kinds first in
// paneOrder, then any others by name.
func (s *paneStrip) registeredOrder() []paneID {
	out := make([]paneID, 0, len(s.live))
	for _, id := range paneOrder {
		if s.live[id] != nil {
			out = append(out, id)
		}
	}
	var rest []paneID
	for id := range s.live {
		if !slices.Contains(paneOrder, id) {
			rest = append(rest, id)
		}
	}
	slices.Sort(rest)
	return append(out, rest...)
}

// layout is the strip as it is saved.
func (s *paneStrip) layout() config.SidebarLayout {
	var l config.SidebarLayout
	custom := false
	for _, c := range s.cols {
		ids := make([]string, 0, len(c.panes))
		for _, id := range c.panes {
			ids = append(ids, string(id))
		}
		l.Columns = append(l.Columns, ids)
		l.Widths = append(l.Widths, c.width)
		custom = custom || c.width > 0
	}
	if !custom {
		l.Widths = nil
	}
	for _, id := range s.closed {
		l.Closed = append(l.Closed, string(id))
	}
	return l
}

// reset puts the default layout back, keeping the panes' state.
func (s *paneStrip) reset() {
	s.fromColumns(defaultColumns, nil, nil)
	s.scroll = map[paneID]int{}
	s.chooser = nil
}

// focusedRow is the focused pane's row in the focused column.
func (s *paneStrip) focusedRow() int {
	if s.col < 0 || s.col >= len(s.cols) {
		return 0
	}
	return s.cols[s.col].row
}

// focused is the focused pane's id, or false when the strip is empty.
func (s *paneStrip) focused() (paneID, bool) {
	if len(s.cols) == 0 {
		return "", false
	}
	s.clampFocus()
	c := s.cols[s.col]
	return c.panes[c.row], true
}

// focusedPane is the focused pane, or nil.
func (s *paneStrip) focusedPane() pane {
	id, ok := s.focused()
	if !ok {
		return nil
	}
	return s.live[id]
}

// clampFocus keeps the focus on an existing column and pane.
func (s *paneStrip) clampFocus() {
	if len(s.cols) == 0 {
		s.col, s.first = 0, 0
		return
	}
	s.col = max(0, min(s.col, len(s.cols)-1))
	c := &s.cols[s.col]
	c.row = max(0, min(c.row, len(c.panes)-1))
}

// find returns the column and row holding id.
func (s *paneStrip) find(id paneID) (col, row int, ok bool) {
	for ci, c := range s.cols {
		if ri := slices.Index(c.panes, id); ri >= 0 {
			return ci, ri, true
		}
	}
	return 0, 0, false
}

// focusOn focuses the pane id when it is open.
func (s *paneStrip) focusOn(id paneID) bool {
	ci, ri, ok := s.find(id)
	if !ok {
		return false
	}
	s.col = ci
	s.cols[ci].row = ri
	s.reveal = true
	return true
}

// focusColumn moves the focus dir columns sideways. It reports whether it
// moved.
func (s *paneStrip) focusColumn(dir int) bool {
	next := s.col + dir
	if len(s.cols) == 0 || next < 0 || next >= len(s.cols) {
		return false
	}
	s.col = next
	s.clampFocus()
	s.reveal = true
	return true
}

// focusRow moves the focus dir panes up or down its column. It reports
// whether it moved.
func (s *paneStrip) focusRow(dir int) bool {
	if len(s.cols) == 0 {
		return false
	}
	s.clampFocus()
	c := &s.cols[s.col]
	next := c.row + dir
	if next < 0 || next >= len(c.panes) {
		return false
	}
	c.row = next
	return true
}

// open shows the pane id: focuses it when it is open, otherwise takes it
// off the closed list and inserts it as a new column right of the focused
// one. It reports whether the layout changed; false with the pane not
// registered means there is nothing to show.
func (s *paneStrip) open(id paneID) (changed bool) {
	if s.live[id] == nil {
		return false
	}
	if s.focusOn(id) {
		return false
	}
	s.closed = slices.DeleteFunc(s.closed, func(c paneID) bool { return c == id })
	at := 0
	if len(s.cols) > 0 {
		s.clampFocus()
		at = s.col + 1
	}
	s.cols = slices.Insert(s.cols, at, stripColumn{panes: []paneID{id}})
	s.col = at
	s.scroll[id] = 0
	s.reveal = true
	return true
}

// reopenList is the closed panes as the reopen list shows them, most
// recently closed first.
func (s *paneStrip) reopenList() []paneID {
	out := slices.Clone(s.closed)
	slices.Reverse(out)
	return out
}

// isOpen reports whether id is in a column.
func (s *paneStrip) isOpen(id paneID) bool {
	_, _, ok := s.find(id)
	return ok
}

// closeFocused closes the focused pane. Its column goes when it empties,
// and the focus moves to the column that takes its place (or the one on
// the left at the end of the strip).
func (s *paneStrip) closeFocused() (paneID, bool) {
	id, ok := s.focused()
	if !ok {
		return "", false
	}
	c := &s.cols[s.col]
	c.panes = slices.Delete(c.panes, c.row, c.row+1)
	if len(c.panes) == 0 {
		s.cols = slices.Delete(s.cols, s.col, s.col+1)
	}
	s.closed = slices.DeleteFunc(s.closed, func(c paneID) bool { return c == id })
	s.closed = append(s.closed, id)
	s.clampFocus()
	s.reveal = true
	return id, true
}

// movePane is niri's consume-or-expel: a pane sharing its column leaves it
// for a new column on the dir side; a pane alone in its column joins the
// neighbouring column on that side, at the bottom. It reports whether
// anything moved.
func (s *paneStrip) movePane(dir int) bool {
	id, ok := s.focused()
	if !ok || dir == 0 {
		return false
	}
	c := &s.cols[s.col]
	if len(c.panes) > 1 {
		c.panes = slices.Delete(c.panes, c.row, c.row+1)
		at := s.col
		if dir > 0 {
			at = s.col + 1
		}
		s.cols = slices.Insert(s.cols, at, stripColumn{panes: []paneID{id}})
		s.col = at
		s.reveal = true
		return true
	}
	target := s.col + dir
	if target < 0 || target >= len(s.cols) {
		return false
	}
	t := &s.cols[target]
	t.panes = append(t.panes, id)
	t.row = len(t.panes) - 1
	s.cols = slices.Delete(s.cols, s.col, s.col+1)
	if target > s.col {
		target--
	}
	s.col = target
	s.reveal = true
	return true
}

// moveColumn swaps the focused column with its neighbour on the dir side.
func (s *paneStrip) moveColumn(dir int) bool {
	target := s.col + dir
	if len(s.cols) == 0 || dir == 0 || target < 0 || target >= len(s.cols) {
		return false
	}
	s.cols[s.col], s.cols[target] = s.cols[target], s.cols[s.col]
	s.col = target
	s.reveal = true
	return true
}

// resizeColumn widens (delta > 0) or narrows the focused column, clamped
// between stripColumnMin and limit (the widest the strip can get).
func (s *paneStrip) resizeColumn(delta, limit int) bool {
	if len(s.cols) == 0 || delta == 0 {
		return false
	}
	s.clampFocus()
	limit = max(limit, stripColumnMin)
	cur := min(s.naturalWidth(s.col), limit)
	next := max(stripColumnMin, min(cur+delta, limit))
	if next == cur {
		return false
	}
	s.cols[s.col].width = next
	s.reveal = true
	return true
}

// naturalWidth is column i's width before clamping: what the user set, or
// the widest its panes ask for, but never under stripColumnDefault.
func (s *paneStrip) naturalWidth(i int) int {
	c := s.cols[i]
	if c.width > 0 {
		return c.width
	}
	w := stripColumnDefault
	for _, id := range c.panes {
		if p := s.live[id]; p != nil {
			w = max(w, p.width())
		}
	}
	return w
}

// colWidth is column i's width in a strip avail cells wide.
func (s *paneStrip) colWidth(i, avail int) int {
	return max(1, min(s.naturalWidth(i), avail))
}

// widths are all columns' widths in a strip avail cells wide.
func (s *paneStrip) widths(avail int) []int {
	out := make([]int, len(s.cols))
	for i := range s.cols {
		out[i] = s.colWidth(i, avail)
	}
	return out
}

// totalWidth is the width every column needs side by side.
func (s *paneStrip) totalWidth() int {
	total := 0
	for i := range s.cols {
		if i > 0 {
			total += stripGap
		}
		total += s.naturalWidth(i)
	}
	return total
}

// fittedWidth is the width the strip needs in at most limit cells: all
// columns side by side when they fit, else the widest run of whole columns
// that fits, so a wide sidebar wastes no space whichever columns show.
func (s *paneStrip) fittedWidth(limit int) int {
	if s.totalWidth() <= limit {
		return s.totalWidth()
	}
	widths := s.widths(limit)
	best := 0
	for first := range widths {
		best = max(best, spanWidth(widths, first, fitCount(widths, first, limit)))
	}
	return best
}

// spanWidth is the width of count columns from first, gaps included.
func spanWidth(widths []int, first, count int) int {
	w := 0
	for i := first; i < first+count && i < len(widths); i++ {
		if i > first {
			w += stripGap
		}
		w += widths[i]
	}
	return w
}

// fitCount is how many whole columns from first fit in avail cells. The
// first one always counts, as it is never wider than the strip.
func fitCount(widths []int, first, avail int) int {
	n, used := 0, 0
	for i := first; i < len(widths); i++ {
		w := widths[i]
		if n > 0 {
			w += stripGap
		}
		if n > 0 && used+w > avail {
			break
		}
		used += w
		n++
	}
	return n
}

// clampFirst keeps first in range and pulls the strip back when the
// columns from first leave room for earlier ones (after a resize or a
// close), so no space is wasted on the right.
func clampFirst(widths []int, first, avail int) int {
	if len(widths) == 0 {
		return 0
	}
	first = max(0, min(first, len(widths)-1))
	for first > 0 && first-1+fitCount(widths, first-1, avail) >= len(widths) {
		first--
	}
	return first
}

// scrollToShow returns the first visible column that shows the focus with
// as little scrolling as possible: nothing moves while it is in view.
func scrollToShow(widths []int, first, focus, avail int) int {
	if len(widths) == 0 {
		return 0
	}
	focus = max(0, min(focus, len(widths)-1))
	if focus < first {
		return focus
	}
	for first < focus && focus >= first+fitCount(widths, first, avail) {
		first++
	}
	return first
}

// distributeHeights splits total rows between a column's panes. needs are
// the rows each wants, header included. Every pane gets its header first;
// panes that need less than an even share get what they need; the focused
// pane (focus, or -1) then takes the larger part of what is left, and the
// others share the rest evenly. When even the headers do not fit, the
// focused pane keeps its header and the panes from the top get the rest.
func distributeHeights(total int, needs []int, focus int) []int {
	n := len(needs)
	alloc := make([]int, n)
	if n == 0 || total <= 0 {
		return alloc
	}
	if total < n {
		left := total
		if focus >= 0 && focus < n {
			alloc[focus] = 1
			left--
		}
		for i := 0; i < n && left > 0; i++ {
			if alloc[i] == 0 {
				alloc[i] = 1
				left--
			}
		}
		return alloc
	}
	want := make([]int, n)
	var open []int
	for i, need := range needs {
		alloc[i] = 1
		want[i] = max(0, need-1)
		if want[i] > 0 {
			open = append(open, i)
		}
	}
	rem := total - n
	// Satisfy the panes that want no more than an even share, until no
	// one is left who does.
	for rem > 0 && len(open) > 0 {
		share := rem / len(open)
		var next []int
		for _, i := range open {
			if want[i] <= share {
				alloc[i] += want[i]
				rem -= want[i]
			} else {
				next = append(next, i)
			}
		}
		if len(next) == len(open) {
			break
		}
		open = next
	}
	if rem <= 0 || len(open) == 0 {
		return alloc
	}
	// The focused pane goes first.
	if fi := slices.Index(open, focus); fi >= 0 {
		give := rem
		if len(open) > 1 {
			give = max(rem/len(open), rem*3/5)
		}
		give = min(give, want[focus])
		alloc[focus] += give
		rem -= give
		open = slices.Delete(open, fi, fi+1)
	}
	if len(open) == 0 || rem <= 0 {
		return alloc
	}
	share, extra := rem/len(open), rem%len(open)
	for k, i := range open {
		g := share
		if k < extra {
			g++
		}
		alloc[i] += min(g, want[i])
	}
	return alloc
}

// paneAt is the pane geometry at screen point (x, y), header or body.
func (g *stripGeom) paneAt(x, y int) (colGeom, paneGeom, bool) {
	for _, c := range g.cols {
		if x < c.x || x >= c.x+c.w {
			continue
		}
		for _, p := range c.panes {
			if y == p.header.Min.Y || (y >= p.body.Min.Y && y < p.body.Max.Y) {
				return c, p, true
			}
		}
	}
	return colGeom{}, paneGeom{}, false
}

// paneGeomOf is the geometry the last frame gave pane id.
func (g *stripGeom) paneGeomOf(id paneID) (paneGeom, bool) {
	for _, c := range g.cols {
		for _, p := range c.panes {
			if p.id == id {
				return p, true
			}
		}
	}
	return paneGeom{}, false
}

// scrollBody moves pane id's body offset by delta lines within what the
// last frame rendered. It reports whether the offset changed.
func (s *paneStrip) scrollBody(id paneID, delta int) bool {
	g, ok := s.geo.paneGeomOf(id)
	if !ok {
		return false
	}
	maxOff := max(0, g.lines-g.body.Dy())
	cur := min(s.scroll[id], maxOff)
	next := max(0, min(cur+delta, maxOff))
	s.scroll[id] = next
	return next != cur
}

// canScrollBody reports whether pane id's body can scroll dir (-1 up, 1
// down).
func (s *paneStrip) canScrollBody(id paneID, dir int) bool {
	g, ok := s.geo.paneGeomOf(id)
	if !ok {
		return false
	}
	maxOff := max(0, g.lines-g.body.Dy())
	off := min(s.scroll[id], maxOff)
	if dir < 0 {
		return off > 0
	}
	return off < maxOff
}

// scrollSideways moves the visible window dir columns, without moving the
// focus.
func (s *paneStrip) scrollSideways(dir int) bool {
	avail := s.geo.area.Dx()
	if avail <= 0 || len(s.cols) == 0 {
		return false
	}
	widths := s.widths(avail)
	maxFirst := clampFirst(widths, len(widths)-1, avail)
	next := max(0, min(s.first+dir, maxFirst))
	if next == s.first {
		return false
	}
	s.first = next
	return true
}
