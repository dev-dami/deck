package model

import (
	"fmt"
	"image"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/csync"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/ui/attachments"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/dialog"
	"github.com/dev-dami/deck/internal/ui/styles"
	"github.com/stretchr/testify/require"
)

// fakePane is a pane for strip tests: a fixed width and line count, an
// optional key it handles itself, and the clicks it got.
type fakePane struct {
	pid    paneID
	w      int
	lines  int
	handle string
	clicks [][2]int
}

func (p *fakePane) id() paneID          { return p.pid }
func (p *fakePane) title(*UI) string    { return string(p.pid) }
func (*fakePane) badge(*UI) string      { return "" }
func (p *fakePane) width() int          { return p.w }
func (p *fakePane) height(*UI, int) int { return p.lines }

func (p *fakePane) render(*UI, int, int, bool) string {
	lines := make([]string, p.lines)
	for i := range lines {
		lines[i] = fmt.Sprintf("%s %d", p.pid, i)
	}
	return strings.Join(lines, "\n")
}

func (p *fakePane) key(_ *UI, msg tea.KeyPressMsg) (bool, tea.Cmd) {
	return p.handle != "" && msg.String() == p.handle, nil
}

func (p *fakePane) click(_ *UI, x, y int) tea.Cmd {
	p.clicks = append(p.clicks, [2]int{x, y})
	return nil
}

// fakeFactories registers a fake pane per id for one test, without
// touching the global paneFactories (tests run in parallel).
func fakeFactories(panes ...*fakePane) map[paneID]func() pane {
	out := map[paneID]func() pane{}
	for _, p := range panes {
		out[p.pid] = func() pane { return p }
	}
	return out
}

// testStrip builds a strip from cols over fake panes named in cols.
func testStrip(cols ...[]paneID) (*paneStrip, map[paneID]*fakePane) {
	fakes := map[paneID]*fakePane{}
	var list []*fakePane
	for _, c := range cols {
		for _, id := range c {
			p := &fakePane{pid: id, lines: 3}
			fakes[id] = p
			list = append(list, p)
		}
	}
	saved := config.SidebarLayout{}
	for _, c := range cols {
		var ids []string
		for _, id := range c {
			ids = append(ids, string(id))
		}
		saved.Columns = append(saved.Columns, ids)
	}
	return newPaneStripFrom(saved, fakeFactories(list...)), fakes
}

func stripIDs(s *paneStrip) [][]paneID {
	out := [][]paneID{}
	for _, c := range s.cols {
		out = append(out, append([]paneID(nil), c.panes...))
	}
	return out
}

func TestStripDefaultSkipsUnregisteredPanes(t *testing.T) {
	t.Parallel()
	f := fakeFactories(&fakePane{pid: paneSession}, &fakePane{pid: paneAgents}, &fakePane{pid: paneTasks})
	s := newPaneStripFrom(config.SidebarLayout{}, f)
	// paneGit and paneCommands are not registered: the git column goes
	// and the tasks column keeps only tasks.
	require.Equal(t, [][]paneID{{paneSession, paneAgents}, {paneTasks}}, stripIDs(s))
	require.Empty(t, s.closed)
}

func TestStripRestoreDropsUnknownIDs(t *testing.T) {
	t.Parallel()
	f := fakeFactories(&fakePane{pid: paneSession}, &fakePane{pid: paneAgents}, &fakePane{pid: paneTasks})
	s := newPaneStripFrom(config.SidebarLayout{
		Columns: [][]string{{"bogus", "session"}, {"git"}},
		Widths:  []int{40, 50},
		Closed:  []string{"agents", "nope"},
	}, f)
	// Unknown and unregistered ids are dropped, the empty git column with
	// them; tasks, registered but not mentioned, gets a column of its own.
	require.Equal(t, [][]paneID{{paneSession}, {paneTasks}}, stripIDs(s))
	require.Equal(t, 40, s.cols[0].width)
	require.Equal(t, 0, s.cols[1].width)
	require.Equal(t, []paneID{paneAgents}, s.closed)
}

func TestStripRestoreAllUnknownFallsBackToDefault(t *testing.T) {
	t.Parallel()
	f := fakeFactories(&fakePane{pid: paneSession}, &fakePane{pid: paneAgents})
	s := newPaneStripFrom(config.SidebarLayout{Columns: [][]string{{"x"}}, Closed: []string{"y"}}, f)
	require.Equal(t, [][]paneID{{paneSession, paneAgents}}, stripIDs(s))
}

func TestStripPersistenceRoundTrip(t *testing.T) {
	t.Parallel()
	s, fakes := testStrip([]paneID{"a", "b"}, []paneID{"c"}, []paneID{"d"})
	s.cols[0].row = 1
	require.True(t, s.movePane(1))           // [[a] [b] [c] [d]]
	require.True(t, s.resizeColumn(-4, 100)) // b: 30
	require.True(t, s.focusColumn(2))        // d
	_, ok := s.closeFocused()                // [[a] [b] [c]], closed d
	require.True(t, ok)
	saved := s.layout()
	require.Equal(t, [][]string{{"a"}, {"b"}, {"c"}}, saved.Columns)
	require.Equal(t, []int{0, 30, 0}, saved.Widths)
	require.Equal(t, []string{"d"}, saved.Closed)

	var list []*fakePane
	for _, p := range fakes {
		list = append(list, p)
	}
	back := newPaneStripFrom(saved, fakeFactories(list...))
	require.Equal(t, stripIDs(s), stripIDs(back))
	require.Equal(t, s.closed, back.closed)
	require.Equal(t, 30, back.cols[1].width)
}

func TestStripCloseMovesFocusToNeighbour(t *testing.T) {
	t.Parallel()
	s, _ := testStrip([]paneID{"a", "b"}, []paneID{"c"})
	s.cols[0].row = 1
	id, ok := s.closeFocused()
	require.True(t, ok)
	require.Equal(t, paneID("b"), id)
	require.Equal(t, [][]paneID{{"a"}, {"c"}}, stripIDs(s))
	require.Equal(t, 0, s.col)
	require.Equal(t, 0, s.focusedRow())

	// Closing a column's last pane drops the column; the next one takes
	// its place.
	s.closeFocused()
	require.Equal(t, [][]paneID{{"c"}}, stripIDs(s))
	got, _ := s.focused()
	require.Equal(t, paneID("c"), got)
	require.Equal(t, []paneID{"b", "a"}, s.closed)
	require.Equal(t, []paneID{"a", "b"}, s.reopenList())

	s.closeFocused()
	require.Empty(t, s.cols)
	_, ok = s.focused()
	require.False(t, ok)
	_, ok = s.closeFocused()
	require.False(t, ok)
}

func TestStripCloseLastColumnFocusesLeft(t *testing.T) {
	t.Parallel()
	s, _ := testStrip([]paneID{"a"}, []paneID{"b"})
	s.col = 1
	s.closeFocused()
	require.Equal(t, 0, s.col)
	got, _ := s.focused()
	require.Equal(t, paneID("a"), got)
}

func TestStripConsumeAndExpel(t *testing.T) {
	t.Parallel()
	s, _ := testStrip([]paneID{"a", "b"}, []paneID{"c"})
	s.cols[0].row = 1

	// b shares its column: ] expels it into a new column on the right.
	require.True(t, s.movePane(1))
	require.Equal(t, [][]paneID{{"a"}, {"b"}, {"c"}}, stripIDs(s))
	require.Equal(t, 1, s.col)

	// b is alone: ] stacks it under c.
	require.True(t, s.movePane(1))
	require.Equal(t, [][]paneID{{"a"}, {"c", "b"}}, stripIDs(s))
	require.Equal(t, 1, s.col)
	require.Equal(t, 1, s.focusedRow())

	// [ expels it to the left of c's column.
	require.True(t, s.movePane(-1))
	require.Equal(t, [][]paneID{{"a"}, {"b"}, {"c"}}, stripIDs(s))
	require.Equal(t, 1, s.col)

	// [ again stacks it under a.
	require.True(t, s.movePane(-1))
	require.Equal(t, [][]paneID{{"a", "b"}, {"c"}}, stripIDs(s))
	require.Equal(t, 0, s.col)
	require.Equal(t, 1, s.focusedRow())

	// A lone pane at the edge has nowhere to go.
	s.col = 1
	require.False(t, s.movePane(1))
}

func TestStripMoveColumn(t *testing.T) {
	t.Parallel()
	s, _ := testStrip([]paneID{"a"}, []paneID{"b"}, []paneID{"c"})
	require.False(t, s.moveColumn(-1))
	require.True(t, s.moveColumn(1))
	require.Equal(t, [][]paneID{{"b"}, {"a"}, {"c"}}, stripIDs(s))
	require.Equal(t, 1, s.col)
	require.True(t, s.moveColumn(1))
	require.Equal(t, [][]paneID{{"b"}, {"c"}, {"a"}}, stripIDs(s))
	require.False(t, s.moveColumn(1))
}

func TestStripResizeColumnClamps(t *testing.T) {
	t.Parallel()
	s, _ := testStrip([]paneID{"a"})
	require.True(t, s.resizeColumn(-stripColumnStep, 100))
	require.Equal(t, stripColumnDefault-stripColumnStep, s.cols[0].width)
	for range 10 {
		s.resizeColumn(-stripColumnStep, 100)
	}
	require.Equal(t, stripColumnMin, s.cols[0].width)
	for range 30 {
		s.resizeColumn(stripColumnStep, 60)
	}
	require.Equal(t, 60, s.cols[0].width)
	require.False(t, s.resizeColumn(stripColumnStep, 60))
}

func TestStripOpen(t *testing.T) {
	t.Parallel()
	s, _ := testStrip([]paneID{"a"}, []paneID{"b"}, []paneID{"c"})
	s.col = 1
	s.closeFocused() // b closed, focus on c (index 1)
	s.col = 0
	require.True(t, s.open("b"))
	require.Equal(t, [][]paneID{{"a"}, {"b"}, {"c"}}, stripIDs(s))
	require.Equal(t, 1, s.col)
	require.Empty(t, s.closed)

	// Opening an open pane only focuses it.
	require.False(t, s.open("c"))
	require.Equal(t, 2, s.col)

	// An unregistered pane cannot open.
	require.False(t, s.open("nope"))
	require.Equal(t, 2, s.col)
}

func TestStripColumnWidths(t *testing.T) {
	t.Parallel()
	wide := &fakePane{pid: "wide", w: 50}
	narrow := &fakePane{pid: "narrow", w: 10}
	s := newPaneStripFrom(config.SidebarLayout{Columns: [][]string{{"wide", "narrow"}, {"narrow2"}}},
		fakeFactories(wide, narrow, &fakePane{pid: "narrow2"}))
	// The widest pane sets the column; no column is under the default.
	require.Equal(t, 50, s.colWidth(0, 100))
	require.Equal(t, stripColumnDefault, s.colWidth(1, 100))
	// Columns never get wider than the strip.
	require.Equal(t, 31, s.colWidth(0, 31))
	require.Equal(t, 50+stripGap+stripColumnDefault, s.totalWidth())
	// A strip too narrow for both shows the widest single run.
	require.Equal(t, 50, s.fittedWidth(60))
	require.Equal(t, s.totalWidth(), s.fittedWidth(200))
}

func TestStripVisibleColumnsAndScroll(t *testing.T) {
	t.Parallel()
	w := []int{34, 34, 34, 34}
	avail := 34*2 + stripGap
	require.Equal(t, 2, fitCount(w, 0, avail))
	require.Equal(t, 2, fitCount(w, 2, avail))
	require.Equal(t, 1, fitCount(w, 3, avail))
	// The first column always counts, even when wider than the strip.
	require.Equal(t, 1, fitCount([]int{90}, 0, 30))

	// Focus in view: nothing moves.
	require.Equal(t, 0, scrollToShow(w, 0, 1, avail))
	// Focus off to the right: scroll just enough.
	require.Equal(t, 1, scrollToShow(w, 0, 2, avail))
	require.Equal(t, 2, scrollToShow(w, 0, 3, avail))
	// Focus off to the left: it becomes the first column.
	require.Equal(t, 1, scrollToShow(w, 3, 1, avail))

	// A strip scrolled further than needed is pulled back.
	require.Equal(t, 2, clampFirst(w, 3, avail))
	require.Equal(t, 0, clampFirst(w, 3, 1000))
	require.Equal(t, 0, clampFirst(nil, 5, avail))
}

func TestDistributeHeights(t *testing.T) {
	t.Parallel()
	sum := func(a []int) int {
		n := 0
		for _, v := range a {
			n += v
		}
		return n
	}

	// Everything fits: each gets what it needs.
	require.Equal(t, []int{3, 5}, distributeHeights(20, []int{3, 5}, -1))

	// A small pane gets all it needs; the big one takes the rest.
	got := distributeHeights(10, []int{3, 20}, -1)
	require.Equal(t, []int{3, 7}, got)

	// Two big panes share evenly...
	got = distributeHeights(20, []int{30, 30}, -1)
	require.Equal(t, []int{10, 10}, got)
	// ...unless one is focused, which then gets the larger part.
	got = distributeHeights(22, []int{30, 30}, 1)
	require.Equal(t, 22, sum(got))
	require.Greater(t, got[1], got[0])

	// A collapsed pane keeps its header.
	got = distributeHeights(10, []int{1, 30}, -1)
	require.Equal(t, []int{1, 9}, got)

	// Not even the headers fit: the focused pane keeps its header.
	require.Equal(t, []int{0, 0, 1}, distributeHeights(1, []int{5, 5, 5}, 2))
	require.Equal(t, []int{1, 1, 0}, distributeHeights(2, []int{5, 5, 5}, -1))

	require.Empty(t, distributeHeights(10, nil, -1))
}

func TestStripHeaderFillsWidth(t *testing.T) {
	t.Parallel()
	sty := styles.DeckDark()
	u := &UI{com: &common.Common{Styles: &sty}}
	st := u.stripStyles()
	for _, w := range []int{3, 10, 34, 50} {
		h := stripHeader(st, "Session", "3 changed", w, 2, true, true)
		require.LessOrEqual(t, ansi.StringWidth(h), w)
		if w > 10 {
			require.Equal(t, w, ansi.StringWidth(h))
			require.True(t, strings.HasSuffix(ansi.Strip(h), "×"))
		}
	}
	line := stripIndicator(st, 4, 1, 2, 1, 30)
	require.Equal(t, 30, ansi.StringWidth(line))
	require.Contains(t, ansi.Strip(line), "‹ 1")
	require.Contains(t, ansi.Strip(line), "1 ›")
}

// newStripTestUI is a chat UI with a session and the given strip.
func newStripTestUI(t *testing.T, s *paneStrip, width int) *UI {
	t.Helper()
	sty := styles.DeckDark()
	ws := &testWorkspace{cfg: &config.Config{
		Providers: csync.NewMap[string, config.ProviderConfig](),
	}}
	com := &common.Common{Workspace: ws, Styles: &sty}
	keyMap := DefaultKeyMap()
	att := attachments.New(nil, attachments.Keymap{
		DeleteMode: keyMap.Editor.AttachmentDeleteMode,
		DeleteAll:  keyMap.Editor.DeleteAllAttachments,
		Escape:     keyMap.Editor.Escape,
	})
	u := &UI{
		com:         com,
		keyMap:      keyMap,
		state:       uiChat,
		focus:       uiFocusSidebar,
		session:     &session.Session{ID: "sess-1"},
		chat:        NewChat(com, config.ScrollbarDefault),
		textarea:    textarea.New(),
		dialog:      dialog.NewOverlay(),
		attachments: att,
		width:       width,
		height:      45,
		stripState:  s,
	}
	u.status = NewStatus(com, u)
	u.updateLayoutAndSize()
	return u
}

func TestLayoutStripGeometry(t *testing.T) {
	t.Parallel()
	s, fakes := testStrip([]paneID{"a", "b"}, []paneID{"c"}, []paneID{"d"})
	fakes["b"].lines = 40
	u := newStripTestUI(t, s, 200)

	area := image.Rect(0, 0, 34*2+stripGap, 20)
	u.layoutStrip(area)
	g := s.geo
	// Two whole columns fit; the third is off to the right, so the
	// indicator row is taken from the bottom.
	require.Len(t, g.cols, 2)
	require.Equal(t, image.Rect(0, 19, area.Dx(), 20), g.indicator)
	a, b := g.cols[0].panes[0], g.cols[0].panes[1]
	require.Equal(t, image.Rect(0, 0, 34, 1), a.header)
	require.Equal(t, 3, a.body.Dy())
	require.Equal(t, 4, b.header.Min.Y)
	require.Equal(t, 19-5, b.body.Dy())
	require.Equal(t, 34+stripGap, g.cols[1].x)

	// Focusing the last column scrolls just far enough to show it.
	s.focusColumn(1)
	s.focusColumn(1)
	u.layoutStrip(area)
	require.Equal(t, 1, s.first)
	require.Equal(t, 2, s.geo.cols[1].index)
}

func TestStripKeys(t *testing.T) {
	t.Parallel()
	s, fakes := testStrip([]paneID{"a", "b"}, []paneID{"c"})
	u := newStripTestUI(t, s, 200)
	press := func(r rune) {
		u.handleKeyPressMsg(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	// j moves down a column when the body cannot scroll.
	press('j')
	require.Equal(t, 1, s.focusedRow())
	// ] expels b into its own column.
	press(']')
	require.Equal(t, [][]paneID{{"a"}, {"b"}, {"c"}}, stripIDs(s))
	// x closes it.
	press('x')
	require.Equal(t, [][]paneID{{"a"}, {"c"}}, stripIDs(s))
	require.Equal(t, []paneID{"b"}, s.closed)
	// o then enter reopens it.
	press('o')
	require.NotNil(t, s.chooser)
	u.handleKeyPressMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, s.chooser)
	require.True(t, s.isOpen("b"))

	// A key the focused pane handles never reaches the strip.
	id, _ := s.focused()
	fakes[id].handle = "x"
	press('x')
	require.True(t, s.isOpen(id))
	fakes[id].handle = ""

	// h from the first column goes back to the chat; esc to the editor.
	s.col = 0
	press('h')
	require.Equal(t, uiFocusMain, u.focus)
	u.focusSidebar()
	u.handleKeyPressMsg(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Equal(t, uiFocusEditor, u.focus)

	// alt+l moves the strip's focus without leaving the editor.
	u.handleKeyPressMsg(tea.KeyPressMsg{Code: 'l', Mod: tea.ModAlt})
	require.Equal(t, uiFocusEditor, u.focus)
	require.Equal(t, 1, s.col)
}

func TestFocusPaneReopensAndFocuses(t *testing.T) {
	t.Parallel()
	s, _ := testStrip([]paneID{"a"}, []paneID{"b"})
	u := newStripTestUI(t, s, 200)
	u.focus = uiFocusEditor
	s.col = 1
	s.closeFocused()
	s.col = 0

	u.focusPane("b")
	require.Equal(t, uiFocusSidebar, u.focus)
	require.Equal(t, [][]paneID{{"a"}, {"b"}}, stripIDs(s))
	got, _ := s.focused()
	require.Equal(t, paneID("b"), got)

	// An unregistered pane is ignored.
	require.Nil(t, u.focusPane("nope"))
}

func TestStripClickFocusesAndCloses(t *testing.T) {
	t.Parallel()
	s, fakes := testStrip([]paneID{"a", "b"})
	u := newStripTestUI(t, s, 200)
	u.focus = uiFocusEditor
	u.layoutStrip(image.Rect(10, 5, 44, 25))
	b, _ := s.geo.paneGeomOf("b")

	// A click in b's body focuses the sidebar and b, and reaches b with
	// body-relative coordinates.
	u.handleStripClick(tea.MouseClickMsg{X: b.body.Min.X + 2, Y: b.body.Min.Y + 1, Button: tea.MouseLeft})
	require.Equal(t, uiFocusSidebar, u.focus)
	got, _ := s.focused()
	require.Equal(t, paneID("b"), got)
	require.Equal(t, [][2]int{{2, 1}}, fakes["b"].clicks)

	// A click on the × closes it.
	u.handleStripClick(tea.MouseClickMsg{X: b.header.Max.X - 1, Y: b.header.Min.Y, Button: tea.MouseLeft})
	require.False(t, s.isOpen("b"))
}

func TestStripBodyScroll(t *testing.T) {
	t.Parallel()
	s, fakes := testStrip([]paneID{"a"})
	fakes["a"].lines = 50
	u := newStripTestUI(t, s, 200)
	u.layoutStrip(image.Rect(0, 0, 34, 11))
	// Draw records how many lines the body rendered.
	s.geo.cols[0].panes[0].lines = 50
	require.False(t, s.canScrollBody("a", -1))
	require.True(t, s.canScrollBody("a", 1))
	require.True(t, s.scrollBody("a", 100))
	require.Equal(t, 40, s.scroll["a"])
	require.False(t, s.canScrollBody("a", 1))
	require.True(t, s.scrollBody("a", -100))
	require.Equal(t, 0, s.scroll["a"])
}

func TestSidebarWidth(t *testing.T) {
	t.Parallel()
	s, _ := testStrip([]paneID{"a"}, []paneID{"b"}, []paneID{"c"})
	u := newStripTestUI(t, s, 140)
	require.Equal(t, sidebarNarrowWidth, u.sidebarWidth(138))

	u.width = 200
	w := u.sidebarWidth(198)
	require.LessOrEqual(t, w, 198*sidebarWidePercent/100)
	// Two columns side by side, no wasted space.
	require.Equal(t, 34*2+stripGap+1, w)
}

func TestStripSaveDebounce(t *testing.T) {
	t.Parallel()
	s, _ := testStrip([]paneID{"a"})
	u := newStripTestUI(t, s, 200)
	require.NotNil(t, u.saveStripLayout())
	require.NotNil(t, u.saveStripLayout())
	// Only the latest scheduled save writes.
	require.Nil(t, u.handleStripSave(stripSaveMsg{seq: 1}))
	require.NotNil(t, u.handleStripSave(stripSaveMsg{seq: 2}))
}
