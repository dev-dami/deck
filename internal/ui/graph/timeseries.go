package graph

import (
	"image/color"
	"math"
	"slices"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NimbleMarkets/ntcharts/v2/canvas"
	tslc "github.com/NimbleMarkets/ntcharts/v2/linechart/timeserieslinechart"
)

// Point is one value of a series at an instant.
type Point struct {
	T time.Time
	V float64
}

// Small charts below these sizes drop their axes.
const (
	minAxesWidth  = 16
	minAxesHeight = 4
)

const brailleBlank = 0x2800

// TimeSeries is a line chart over time with one braille line per series
// and a crosshair that reads every series at one instant. ntcharts plots the
// lines; TimeSeries draws its own quiet axes, gridlines and tooltip around
// them.
type TimeSeries struct {
	theme         Theme
	width, height int
	axes          bool
	focused       bool
	axisTime      func(time.Time) string // nil picks a format from the data
	tipTime       func(time.Time) string
	value         func(float64) string
	offX, offY    int // the chart's place on screen, for mouse events

	names  []string             // series, in draw order
	times  []time.Time          // every instant any series has, sorted
	values map[string][]float64 // series values aligned with times
	has    map[string][]bool    // whether a series has a point at times[i]
	yMax   float64
	cursor int // index into times; -1 hides the crosshair

	// Layout, from layout().
	gutter, plotW, plotH int
	base                 *grid // the chart without its crosshair
}

// TimeSeriesOption configures a TimeSeries.
type TimeSeriesOption func(*TimeSeries)

// WithoutAxes draws the lines alone, for charts too small for labels.
func WithoutAxes() TimeSeriesOption { return func(m *TimeSeries) { m.axes = false } }

// WithTimeFormat sets the axis and tooltip time labels.
func WithTimeFormat(axis, tip func(time.Time) string) TimeSeriesOption {
	return func(m *TimeSeries) { m.axisTime, m.tipTime = axis, tip }
}

// WithValueFormat sets how values are labelled; Compact by default.
func WithValueFormat(f func(float64) string) TimeSeriesOption {
	return func(m *TimeSeries) { m.value = f }
}

// NewTimeSeries is an empty chart width × height cells.
func NewTimeSeries(t Theme, width, height int, opts ...TimeSeriesOption) TimeSeries {
	m := TimeSeries{theme: t, width: width, height: height, axes: true, value: Compact, cursor: -1}
	for _, o := range opts {
		o(&m)
	}
	m.rebuild()
	return m
}

// SetData replaces the series. Points need not be sorted; a series with
// no point at some instant reads as zero there.
func (m *TimeSeries) SetData(data map[string][]Point) {
	m.names = make([]string, 0, len(data)) // fresh, as copies of m may share the old one
	at := map[int64]bool{}
	for n, ps := range data {
		m.names = append(m.names, n)
		for _, p := range ps {
			at[p.T.UnixNano()] = true
		}
	}
	sort.Strings(m.names)
	m.times = make([]time.Time, 0, len(at))
	var zone *time.Location
	for _, ps := range data {
		if len(ps) > 0 {
			zone = ps[0].T.Location()
			break
		}
	}
	for ns := range at {
		m.times = append(m.times, time.Unix(0, ns).In(zone))
	}
	slices.SortFunc(m.times, func(a, b time.Time) int { return a.Compare(b) })
	idx := make(map[int64]int, len(m.times))
	for i, t := range m.times {
		idx[t.UnixNano()] = i
	}
	m.values = make(map[string][]float64, len(data))
	m.has = make(map[string][]bool, len(data))
	peak := 0.0
	for n, ps := range data {
		vs, hs := make([]float64, len(m.times)), make([]bool, len(m.times))
		for _, p := range ps {
			i := idx[p.T.UnixNano()]
			vs[i] += p.V
			hs[i] = true
			peak = max(peak, vs[i])
		}
		m.values[n], m.has[n] = vs, hs
	}
	m.yMax = NiceCeil(peak)
	if m.cursor >= len(m.times) {
		m.cursor = len(m.times) - 1
	}
	m.rebuild()
}

// SetTheme redraws the chart in t.
func (m *TimeSeries) SetTheme(t Theme) { m.theme = t; m.rebuild() }

// Resize sets the chart's size in cells.
func (m *TimeSeries) Resize(width, height int) {
	m.width, m.height = width, height
	m.rebuild()
}

// SetOffset tells the chart where its top-left cell is on screen, so Update
// can map mouse events onto it.
func (m *TimeSeries) SetOffset(x, y int) { m.offX, m.offY = x, y }

// Focus lets Update take keys.
func (m *TimeSeries) Focus() { m.focused = true }

// Blur stops Update taking keys.
func (m *TimeSeries) Blur() { m.focused = false }

// Focused reports whether Update takes keys.
func (m TimeSeries) Focused() bool { return m.focused }

// Width is the chart's width in cells.
func (m TimeSeries) Width() int { return m.width }

// Height is the chart's height in cells.
func (m TimeSeries) Height() int { return m.height }

// YMax is the top of the value axis.
func (m TimeSeries) YMax() float64 { return m.yMax }

// Cursor is the crosshair's index into the chart's instants; -1 when hidden.
func (m TimeSeries) Cursor() int { return m.cursor }

// CursorTime is the instant under the crosshair.
func (m TimeSeries) CursorTime() (time.Time, bool) {
	if m.cursor < 0 || m.cursor >= len(m.times) {
		return time.Time{}, false
	}
	return m.times[m.cursor], true
}

// SetCursor moves the crosshair to instant i, clamped; i < 0 hides it.
func (m *TimeSeries) SetCursor(i int) {
	if i < 0 || len(m.times) == 0 {
		m.cursor = -1
		return
	}
	m.cursor = min(i, len(m.times)-1)
}

// HideCursor hides the crosshair.
func (m *TimeSeries) HideCursor() { m.cursor = -1 }

// Step moves the crosshair by d instants; a hidden one appears on the
// latest.
func (m *TimeSeries) Step(d int) {
	if len(m.times) == 0 {
		return
	}
	if m.cursor < 0 {
		m.cursor = len(m.times) - 1
		return
	}
	m.cursor = max(0, min(m.cursor+d, len(m.times)-1))
}

// HoverAt moves the crosshair to the instant nearest chart cell (x, y),
// in chart coordinates. It reports false, leaving the cursor, when the
// cell is outside the plot.
func (m *TimeSeries) HoverAt(x, y int) bool {
	px := x - m.gutter
	if len(m.times) == 0 || px < 0 || px >= m.plotW || y < 0 || y >= m.height {
		return false
	}
	best, dist := 0, math.MaxInt
	for i := range m.times {
		if d := abs(m.col(i) - px); d < dist {
			best, dist = i, d
		}
	}
	m.cursor = best
	return true
}

// Update moves the crosshair: ←/→ and h/l by one instant, home/end to
// either end when focused, and to the pointer on mouse motion or click.
func (m TimeSeries) Update(msg tea.Msg) (TimeSeries, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if !m.focused {
			break
		}
		switch msg.String() {
		case "left", "h":
			m.Step(-1)
		case "right", "l":
			m.Step(1)
		case "home":
			m.SetCursor(0)
		case "end":
			m.SetCursor(len(m.times) - 1)
		}
	case tea.MouseMotionMsg:
		mouse := msg.Mouse()
		m.HoverAt(mouse.X-m.offX, mouse.Y-m.offY)
	case tea.MouseClickMsg:
		mouse := msg.Mouse()
		m.HoverAt(mouse.X-m.offX, mouse.Y-m.offY)
	}
	return m, nil
}

// View is the chart, with the crosshair and its tooltip when shown.
func (m TimeSeries) View() string {
	if m.base == nil {
		return ""
	}
	if m.cursor < 0 || m.cursor >= len(m.times) || m.plotW <= 0 || m.plotH <= 0 {
		return m.base.render()
	}
	g := m.base.clone()
	m.drawCrosshair(g)
	return g.render()
}

// rebuild lays the chart out and draws everything but the crosshair.
func (m *TimeSeries) rebuild() {
	if m.width <= 0 || m.height <= 0 {
		m.base = nil
		return
	}
	m.layout()
	g := newGrid(m.width, m.height)
	m.base = g
	if m.yMax == 0 {
		m.yMax = 1
	}
	if m.gutter > 0 {
		m.drawAxes(g)
	}
	if len(m.times) > 0 && m.plotW > 0 && m.plotH > 0 {
		m.drawLines(g)
	}
}

func (m *TimeSeries) layout() {
	m.gutter, m.plotW, m.plotH = 0, m.width, m.height
	if !m.axes || m.width < minAxesWidth || m.height < minAxesHeight {
		return
	}
	w := 0
	for _, v := range []float64{0, m.yMax / 2, m.yMax} {
		w = max(w, len(m.value(v)))
	}
	m.gutter = w + 1
	m.plotW = m.width - m.gutter
	m.plotH = m.height - 2
}

// col is the plot column of instant i, as ntcharts' braille grid places it.
func (m *TimeSeries) col(i int) int {
	if len(m.times) < 2 || m.plotW <= 0 {
		return 0
	}
	first, last := m.times[0], m.times[len(m.times)-1]
	f := float64(m.times[i].Sub(first)) / float64(last.Sub(first))
	return int(math.Round(f*float64(2*m.plotW-1))) / 2
}

// row is the plot row value v falls on, as ntcharts' braille grid places it.
func (m *TimeSeries) row(v float64) int {
	dots := 4*m.plotH - 1
	d := int(math.Round(max(0, min(v/m.yMax, 1)) * float64(dots)))
	return (dots - d) / 4
}

// drawLines plots each series with ntcharts and copies the braille into g.
// Series are plotted one at a time and their dots OR'd together, so where
// two lines share a cell neither loses its dots.
func (m *TimeSeries) drawLines(g *grid) {
	first, last := m.times[0], m.times[len(m.times)-1]
	if !last.After(first) {
		last = first.Add(time.Hour)
	}
	c := tslc.New(m.plotW, m.plotH,
		tslc.WithXYSteps(0, 0),
		tslc.WithTimeRange(first, last),
		tslc.WithYRange(0, m.yMax),
	)
	for _, n := range m.names {
		col := m.theme.SeriesColor(n)
		c.SetDataSetStyle(n, lipgloss.NewStyle().Foreground(col))
		pushed := false
		for i, t := range m.times {
			if m.has[n][i] {
				c.PushDataSet(n, tslc.TimePoint{Time: t, Value: m.values[n][i]})
				pushed = true
			}
		}
		if !pushed {
			continue
		}
		// DrawBrailleDataSets clears the canvas first, so this is one
		// series alone.
		c.DrawBrailleDataSets([]string{n})
		for y := range m.plotH {
			for x := range m.plotW {
				r := c.Canvas.Cell(canvas.Point{X: x, Y: y}).Rune
				if r < brailleBlank || r > brailleBlank+0xff {
					continue
				}
				gx := m.gutter + x
				if prev := g.at(gx, y).r; prev >= brailleBlank && prev <= brailleBlank+0xff {
					r = brailleBlank + (r - brailleBlank | prev - brailleBlank)
				}
				g.set(gx, y, r, col)
			}
		}
	}
}

// drawAxes draws value labels with faint gridlines, the baseline and the
// time labels under it.
func (m *TimeSeries) drawAxes(g *grid) {
	th := m.theme
	label := func(y int, v float64) {
		s := m.value(v)
		g.text(m.gutter-1-len(s), y, s, th.Subtle, false)
	}
	for _, v := range []float64{m.yMax, m.yMax / 2} {
		if v == m.yMax/2 && m.plotH < 5 {
			continue
		}
		y := m.row(v)
		label(y, v)
		for x := range m.plotW {
			g.set(m.gutter+x, y, '┈', th.Grid)
		}
	}
	base := m.plotH
	label(base, 0)
	for x := range m.plotW {
		g.set(m.gutter+x, base, '─', th.Grid)
	}
	m.drawTimeLabels(g, base+1)
}

// drawTimeLabels labels as many instants as fit, evenly spread: the first
// flush left, the last flush right, the rest centred on their column.
func (m *TimeSeries) drawTimeLabels(g *grid, y int) {
	n := len(m.times)
	if n == 0 {
		return
	}
	axis := m.axisTime
	if axis == nil {
		axis, _ = m.autoTimeFormats()
	}
	w := len(axis(m.times[0]))
	k := min(n, max(2, m.plotW/(w+3)))
	if n == 1 {
		k = 1
	}
	type lbl struct {
		x int
		s string
	}
	var ls []lbl
	for j := range k {
		i := 0
		if k > 1 {
			i = int(math.Round(float64(j) * float64(n-1) / float64(k-1)))
		}
		s := axis(m.times[i])
		x := m.col(i) - len(s)/2
		switch {
		case j == 0:
			x = 0
		case j == k-1:
			x = m.plotW - len(s)
		}
		ls = append(ls, lbl{x, s})
	}
	// Keep the last label, then fill in from the left wherever there is
	// room for a label and a gap.
	lastX := m.plotW
	if len(ls) > 1 {
		lastX = ls[len(ls)-1].x
	}
	end := -2
	for j, l := range ls {
		if j < len(ls)-1 && (l.x < end+2 || l.x+len(l.s)+2 > lastX) {
			continue
		}
		if l.x < 0 || l.x+len(l.s) > m.plotW {
			continue
		}
		g.text(m.gutter+l.x, y, l.s, m.theme.Subtle, false)
		end = l.x + len(l.s)
	}
}

func (m *TimeSeries) autoTimeFormats() (axis, tip func(time.Time) string) {
	step, span := 24*time.Hour, time.Duration(0)
	if n := len(m.times); n > 1 {
		span = m.times[n-1].Sub(m.times[0])
		step = span
		for i := 1; i < n; i++ {
			step = min(step, m.times[i].Sub(m.times[i-1]))
		}
	}
	return timeFormats(step, span)
}

// drawCrosshair draws the cursor's column, a dot on each series and a
// tooltip box beside it, kept inside the chart.
func (m TimeSeries) drawCrosshair(g *grid) {
	th := m.theme
	i := m.cursor
	cx := m.gutter + m.col(i)
	for y := range m.plotH {
		if r := g.at(cx, y).r; r == 0 || r == '┈' {
			g.set(cx, y, '│', th.Dim)
		}
	}
	if m.gutter > 0 {
		g.set(cx, m.plotH, '┴', th.Dim)
	}
	for _, n := range m.names {
		if m.has[n][i] {
			g.set(cx, m.row(m.values[n][i]), '●', th.SeriesColor(n))
		}
	}
	m.drawTooltip(g, cx)
}

type tipLine struct {
	mark  color.Color // the series dot's colour; nil for none
	label string
	value string
	fg    color.Color
	bold  bool
}

func (m TimeSeries) drawTooltip(g *grid, cx int) {
	th := m.theme
	i := m.cursor
	tip := m.tipTime
	if tip == nil {
		_, tip = m.autoTimeFormats()
	}
	lines := []tipLine{{label: tip(m.times[i]), fg: th.Subtle}}
	total, shown := 0.0, 0
	for _, n := range m.names {
		v := m.values[n][i]
		total += v
		shown++
		lines = append(lines, tipLine{mark: th.SeriesColor(n), label: n, value: m.value(v), fg: th.Fg})
	}
	if shown > 1 {
		lines = append(lines, tipLine{label: "Total", value: m.value(total), fg: th.Fg, bold: true})
	}
	// The box stays over the plot, clear of the axis labels. Too short for
	// every line: drop the time first, then series from the bottom, keeping
	// the total.
	for len(lines)+2 > m.plotH && len(lines) > 1 {
		if lines[0].mark == nil && lines[0].value == "" {
			lines = lines[1:]
			continue
		}
		cut := len(lines) - 1
		if lines[cut].bold && cut > 0 {
			cut--
		}
		lines = slices.Delete(lines, cut, cut+1)
	}
	if len(lines)+2 > m.plotH {
		return
	}

	inner := 0
	for _, l := range lines {
		w := len([]rune(l.label))
		if l.mark != nil {
			w += 2
		}
		if l.value != "" {
			w += 2 + len(l.value)
		}
		inner = max(inner, w)
	}
	inner = min(inner, m.width-4)
	if inner <= 0 {
		return
	}
	bw, bh := inner+4, len(lines)+2

	// Right of the crosshair if it fits, else left, else wherever it fits.
	lo := 0
	if m.gutter+bw <= m.width {
		lo = m.gutter
	}
	x := cx + 2
	if x+bw > m.width {
		x = cx - 1 - bw
	}
	x = max(lo, min(x, m.width-bw))

	for yy := range bh {
		for xx := range bw {
			g.set(x+xx, yy, ' ', nil)
		}
	}
	border := lipgloss.RoundedBorder()
	for xx := 1; xx < bw-1; xx++ {
		g.text(x+xx, 0, border.Top, th.Dim, false)
		g.text(x+xx, bh-1, border.Bottom, th.Dim, false)
	}
	g.text(x, 0, border.TopLeft, th.Dim, false)
	g.text(x+bw-1, 0, border.TopRight, th.Dim, false)
	g.text(x, bh-1, border.BottomLeft, th.Dim, false)
	g.text(x+bw-1, bh-1, border.BottomRight, th.Dim, false)
	for j, l := range lines {
		y := j + 1
		g.text(x, y, border.Left, th.Dim, false)
		g.text(x+bw-1, y, border.Right, th.Dim, false)
		lx := x + 2
		room := inner
		if l.mark != nil {
			g.set(lx, y, '●', l.mark)
			lx += 2
			room -= 2
		}
		if l.value != "" {
			room -= 2 + len(l.value)
			vfg := l.fg
			if l.mark != nil {
				vfg = l.mark
			}
			g.text(x+2+inner-len(l.value), y, l.value, vfg, l.bold)
		}
		label := []rune(l.label)
		if len(label) > room {
			label = append(label[:max(room-1, 0)], '…')
		}
		if room > 0 {
			g.text(lx, y, string(label), l.fg, l.bold)
		}
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
