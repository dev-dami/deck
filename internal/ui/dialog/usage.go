package dialog

import (
	"context"
	"fmt"
	"image/color"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/graph"
	"github.com/dev-dami/deck/internal/usage"
	"github.com/dev-dami/deck/internal/workspace"
)

// The full Usage view (alt+u): what the agents used and did, read from
// Deck's local record, over six tabs. It sizes itself to nearly the whole
// screen and lays its sections out by the room it has. Dollars only ever
// appear on the Cost tab, labelled as what each CLI reported.

const (
	// UsageID is the identifier of the full Usage view.
	UsageID = "usage"
	// UsageKey opens the full Usage view from anywhere.
	UsageKey = "alt+u"

	// usageViewMaxWidth keeps lines readable on very wide terminals.
	usageViewMaxWidth = 180
	// usageTraceLimit is how many recent turns one load reads, for the
	// Traces list and the Models trend.
	usageTraceLimit = 200
	// usageHeatWeeks is how many weeks the Overview heatmap covers.
	usageHeatWeeks = 26
)

// UsageTab is one page of the Usage view.
type UsageTab int

const (
	UsageTabOverview UsageTab = iota
	UsageTabLimits
	UsageTabBehaviour
	UsageTabTraces
	UsageTabModels
	UsageTabCost
	usageTabCount
)

// String returns the tab's label.
func (t UsageTab) String() string {
	switch t {
	case UsageTabLimits:
		return "Limits"
	case UsageTabBehaviour:
		return "Behaviour"
	case UsageTabTraces:
		return "Traces"
	case UsageTabModels:
		return "Models"
	case UsageTabCost:
		return "Cost"
	}
	return "Overview"
}

// usagePeriod is one range the Usage view sums over.
type usagePeriod struct {
	key   string // the key that picks it
	label string
	days  int // 1 = today, by hour; 0 = all time
}

var usagePeriods = []usagePeriod{
	{"t", "Today", 1},
	{"w", "7 days", 7},
	{"m", "30 days", 30},
	{"a", "All time", 0},
}

// defaultUsagePeriod is the period the view opens on (7 days).
const defaultUsagePeriod = 1

// usageData is everything one load of the view reads.
type usageData struct {
	overview usage.Overview
	series   usage.Series
	heat     []usage.Day
	behavior usage.BehaviorReport
	habits   usage.Habits
	limits   []usage.LimitSnapshot // the newest stored row of each window
	turns    []usage.Turn          // newest first, within the period
	models   []usage.ModelStat
	cost     []usage.KindCost
}

// usageDataMsg carries one load; req tells a stale one apart.
type usageDataMsg struct {
	req  int
	data usageData
	err  error
}

// usageHistoryMsg carries one plan window's stored history.
type usageHistoryMsg struct {
	key    string // agent kind + "/" + window id
	points []usage.LimitSnapshot
	err    error
}

// usageTraceMsg carries one turn's spans.
type usageTraceMsg struct {
	turnID string
	spans  []usage.Span
	err    error
}

// Usage is the full Usage view.
type Usage struct {
	com     *common.Common
	help    help.Model
	spinner spinner.Model
	reader  workspace.UsageReader // nil when the workspace keeps no record
	svc     usage.Service         // nil when there is no database
	kinds   []string              // the agent CLIs Deck found, for Limits
	now     func() time.Time

	tab     UsageTab
	period  int
	data    *usageData
	err     error
	req     int
	loading bool

	// scroll is each tab's first body line shown.
	scroll [usageTabCount]int

	limitSel   int // index into every agent's windows, in order
	historyKey string
	history    []usage.LimitSnapshot

	traceSel  int
	traceOpen string // the turn whose waterfall shows; "" for none
	spans     []usage.Span
	spansErr  error

	modelSel int

	// The charts, kept so the crosshair and its place survive redraws.
	theme     graph.Theme
	themeKey  string
	chart     graph.TimeSeries // Overview: tokens per agent
	histChart graph.TimeSeries // Limits: the selected window's history
	// chartLine is the body line the Overview chart was last drawn on, for
	// the pointer; -1 when it was not. selLine is the body line of the
	// page's selection, kept in view; -1 on a page without one.
	chartLine, selLine int

	keyMap struct {
		Close, NextTab, PrevTab, GoTab, Period key.Binding
		Up, Down, PageUp, PageDown, Enter      key.Binding
		Refresh, Cross                         key.Binding
	}
}

var (
	_ Dialog        = (*Usage)(nil)
	_ LoadingDialog = (*Usage)(nil)
	_ HoverDialog   = (*Usage)(nil)
)

// NewUsage opens the Usage view on its Overview. Call [Usage.InitialCmd]
// to load it.
func NewUsage(com *common.Common) *Usage {
	u := &Usage{com: com, period: defaultUsagePeriod, now: time.Now, chartLine: -1}
	u.help = help.New()
	u.help.Styles = com.Styles.DialogHelpStyles()
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = com.Styles.Dialog.Spinner
	u.spinner = sp

	if com.Workspace != nil {
		u.reader, _ = com.Workspace.(workspace.UsageReader)
		if ed, ok := com.Workspace.(workspace.SettingsEditor); ok {
			u.kinds = ed.AgentKinds()
		}
	}
	if u.reader != nil {
		u.svc = u.reader.UsageService()
	}
	u.refreshTheme()
	u.chart = graph.NewTimeSeries(u.theme, 0, 0)
	u.chart.Focus()
	u.histChart = graph.NewTimeSeries(u.theme, 0, 0, graph.WithValueFormat(pctLabel))

	u.keyMap.Close = CloseKey
	u.keyMap.NextTab = key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next tab"))
	u.keyMap.PrevTab = key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "previous tab"))
	u.keyMap.GoTab = key.NewBinding(key.WithKeys("1", "2", "3", "4", "5", "6"), key.WithHelp("1-6", "tab"))
	u.keyMap.Period = key.NewBinding(key.WithKeys("t", "w", "m", "a"), key.WithHelp("t/w/m/a", "period"))
	u.keyMap.Up = key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑", "up"))
	u.keyMap.Down = key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓", "down"))
	u.keyMap.PageUp = key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "page up"))
	u.keyMap.PageDown = key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdn", "page down"))
	u.keyMap.Enter = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "spans"))
	u.keyMap.Refresh = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh"))
	u.keyMap.Cross = key.NewBinding(key.WithKeys("left", "right"), key.WithHelp("←/→", "inspect"))
	return u
}

// ID implements [Dialog].
func (*Usage) ID() string { return UsageID }

// Tab is the page shown.
func (u *Usage) Tab() UsageTab { return u.tab }

// HandlesHover implements [HoverDialog]: the Overview chart follows the
// pointer.
func (u *Usage) HandlesHover() bool { return u.tab == UsageTabOverview }

// InitialCmd loads the view.
func (u *Usage) InitialCmd() tea.Cmd { return u.load() }

// SetTab shows tab, loading what it needs.
func (u *Usage) SetTab(tab UsageTab) tea.Cmd {
	u.tab = (tab%usageTabCount + usageTabCount) % usageTabCount
	if u.tab == UsageTabLimits {
		return u.loadHistory()
	}
	return nil
}

// setPeriod switches to period i and reloads.
func (u *Usage) setPeriod(i int) tea.Cmd {
	if i == u.period && u.data != nil {
		return nil
	}
	u.period = i
	u.historyKey = ""
	u.scroll = [usageTabCount]int{}
	return u.load()
}

// HandleMsg implements [Dialog].
func (u *Usage) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case usageDataMsg:
		if msg.req != u.req {
			return nil // a period since left
		}
		u.StopLoading()
		u.err = msg.err
		if msg.err == nil {
			d := msg.data
			u.data = &d
			u.chart.SetData(seriesPoints(d.series))
			u.clampSelections()
		}
		if u.tab == UsageTabLimits {
			return ActionCmd{Cmd: u.loadHistory()}
		}
	case usageHistoryMsg:
		if msg.key != u.historyKey {
			return nil
		}
		u.history = msg.points
		pts := make([]graph.Point, 0, len(msg.points))
		for _, p := range msg.points {
			pts = append(pts, graph.Point{T: p.At, V: p.UsedPct})
		}
		u.histChart.SetData(map[string][]graph.Point{"used": pts})
	case usageTraceMsg:
		if msg.turnID != u.traceOpen {
			return nil
		}
		u.spans, u.spansErr = msg.spans, msg.err
	case spinner.TickMsg:
		if u.loading {
			var cmd tea.Cmd
			u.spinner, cmd = u.spinner.Update(msg)
			return ActionCmd{Cmd: cmd}
		}
	case tea.MouseMotionMsg, tea.MouseClickMsg:
		if u.tab == UsageTabOverview && u.chartLine >= 0 {
			u.chart, _ = u.chart.Update(msg)
		}
	case tea.KeyPressMsg:
		return u.handleKey(msg)
	}
	return nil
}

func (u *Usage) handleKey(msg tea.KeyPressMsg) Action {
	switch {
	case key.Matches(msg, u.keyMap.Close):
		return ActionClose{}
	case key.Matches(msg, u.keyMap.NextTab):
		return ActionCmd{Cmd: u.SetTab(u.tab + 1)}
	case key.Matches(msg, u.keyMap.PrevTab):
		return ActionCmd{Cmd: u.SetTab(u.tab - 1)}
	case key.Matches(msg, u.keyMap.GoTab):
		return ActionCmd{Cmd: u.SetTab(UsageTab(msg.String()[0] - '1'))}
	case key.Matches(msg, u.keyMap.Period):
		for i, p := range usagePeriods {
			if p.key == msg.String() {
				return ActionCmd{Cmd: u.setPeriod(i)}
			}
		}
	case key.Matches(msg, u.keyMap.Refresh):
		u.historyKey = ""
		return ActionCmd{Cmd: u.load()}
	case key.Matches(msg, u.keyMap.PageUp):
		u.scroll[u.tab] = max(0, u.scroll[u.tab]-10)
	case key.Matches(msg, u.keyMap.PageDown):
		u.scroll[u.tab] += 10
	}

	switch u.tab {
	case UsageTabOverview:
		u.chart, _ = u.chart.Update(msg)
		u.scrollKeys(msg)
	case UsageTabBehaviour, UsageTabCost:
		u.scrollKeys(msg)
	case UsageTabLimits:
		if d := u.moveKeys(msg); d != 0 {
			u.limitSel += d
			u.clampSelections()
			return ActionCmd{Cmd: u.loadHistory()}
		}
	case UsageTabTraces:
		if d := u.moveKeys(msg); d != 0 {
			u.traceSel += d
			u.clampSelections()
			if u.traceOpen != "" {
				return ActionCmd{Cmd: u.openTrace()}
			}
		}
		if key.Matches(msg, u.keyMap.Enter) {
			if u.traceOpen != "" {
				u.traceOpen, u.spans, u.spansErr = "", nil, nil
				return nil
			}
			return ActionCmd{Cmd: u.openTrace()}
		}
	case UsageTabModels:
		if d := u.moveKeys(msg); d != 0 {
			u.modelSel += d
			u.clampSelections()
		}
	}
	return nil
}

// moveKeys is -1 or 1 for an up or down key, else 0.
func (u *Usage) moveKeys(msg tea.KeyPressMsg) int {
	switch {
	case key.Matches(msg, u.keyMap.Up):
		return -1
	case key.Matches(msg, u.keyMap.Down):
		return 1
	}
	return 0
}

// scrollKeys scrolls the body of a page without a selection.
func (u *Usage) scrollKeys(msg tea.KeyPressMsg) {
	u.scroll[u.tab] = max(0, u.scroll[u.tab]+u.moveKeys(msg))
}

// clampSelections keeps every selection inside what is loaded.
func (u *Usage) clampSelections() {
	clamp := func(v, n int) int { return max(0, min(v, n-1)) }
	u.limitSel = clamp(u.limitSel, len(u.flatWindows()))
	if u.data != nil {
		u.traceSel = clamp(u.traceSel, len(u.data.turns))
		u.modelSel = clamp(u.modelSel, len(u.data.models))
	}
}

// load reads the current period, off the UI loop.
func (u *Usage) load() tea.Cmd {
	if u.svc == nil {
		return nil
	}
	u.req++
	req, p, svc, now := u.req, usagePeriods[u.period], u.svc, u.now()
	return tea.Batch(u.StartLoading(), func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), usageLoadTimeout)
		defer cancel()
		d, err := loadUsageData(ctx, svc, p, now)
		return usageDataMsg{req: req, data: d, err: err}
	})
}

// loadUsageData runs every query the view shows for period p.
func loadUsageData(ctx context.Context, svc usage.Service, p usagePeriod, now time.Time) (usageData, error) {
	var d usageData
	var err error
	since := usage.Since(p.days, now)
	g := usage.Daily
	if p.days == 1 {
		g = usage.Hourly
	}
	if d.overview, err = svc.Overview(ctx, since); err != nil {
		return d, err
	}
	if d.series, err = svc.Series(ctx, since, g); err != nil {
		return d, err
	}
	if d.heat, err = svc.Heat(ctx, usageHeatWeeks); err != nil {
		return d, err
	}
	if d.behavior, err = svc.Behavior(ctx, since); err != nil {
		return d, err
	}
	if d.habits, err = svc.Habits(ctx, since); err != nil {
		return d, err
	}
	if d.limits, err = svc.Limits(ctx); err != nil {
		return d, err
	}
	turns, err := svc.Traces(ctx, usageTraceLimit)
	if err != nil {
		return d, err
	}
	for _, t := range turns {
		if since.IsZero() || !t.At.Before(since) {
			d.turns = append(d.turns, t)
		}
	}
	if d.models, err = svc.Models(ctx, since); err != nil {
		return d, err
	}
	if d.cost, err = svc.Cost(ctx, since); err != nil {
		return d, err
	}
	return d, nil
}

// loadHistory reads the selected plan window's history, off the UI loop,
// unless it is the one shown.
func (u *Usage) loadHistory() tea.Cmd {
	w, ok := u.selectedWindow()
	if !ok || u.svc == nil {
		u.historyKey, u.history = "", nil
		return nil
	}
	k := w.Kind + "/" + w.ID
	if k == u.historyKey {
		return nil
	}
	u.historyKey, u.history = k, nil
	u.histChart.SetData(nil)
	svc, since := u.svc, usage.Since(usagePeriods[u.period].days, u.now())
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), usageLoadTimeout)
		defer cancel()
		pts, err := svc.LimitHistory(ctx, w.Kind, w.ID, since)
		return usageHistoryMsg{key: k, points: pts, err: err}
	}
}

// openTrace shows the selected turn's waterfall, reading its spans off
// the UI loop.
func (u *Usage) openTrace() tea.Cmd {
	if u.data == nil || len(u.data.turns) == 0 || u.svc == nil {
		return nil
	}
	id := u.data.turns[u.traceSel].ID
	if id == u.traceOpen && u.spans != nil {
		return nil
	}
	u.traceOpen, u.spans, u.spansErr = id, nil, nil
	svc := u.svc
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), usageLoadTimeout)
		defer cancel()
		spans, err := svc.Trace(ctx, id)
		return usageTraceMsg{turnID: id, spans: spans, err: err}
	}
}

// StartLoading implements [LoadingDialog].
func (u *Usage) StartLoading() tea.Cmd {
	if u.loading {
		return nil
	}
	u.loading = true
	return u.spinner.Tick
}

// StopLoading implements [LoadingDialog].
func (u *Usage) StopLoading() { u.loading = false }

// refreshTheme rebuilds the chart theme when Deck's theme has changed.
func (u *Usage) refreshTheme() {
	t := u.com.Styles
	k := fmt.Sprint(t.IsDark, t.Background, t.Dialog.TitleGradFromColor, t.Dialog.NormalItem.GetForeground())
	if k == u.themeKey {
		return
	}
	u.themeKey = k
	u.theme = graph.FromStyles(t)
	if u.chart.Width() > 0 || u.chart.Height() > 0 {
		u.chart.SetTheme(u.theme)
		u.histChart.SetTheme(u.theme)
	}
}

// Draw implements [Dialog].
func (u *Usage) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	u.refreshTheme()
	t := u.com.Styles
	view := t.Dialog.View
	width := max(0, min(usageViewMaxWidth, area.Dx()-2))
	height := max(0, area.Dy()-2)
	if area.Dx() < 60 {
		width = area.Dx()
	}
	if area.Dy() < 20 {
		height = area.Dy()
	}
	innerW := max(0, width-view.GetHorizontalFrameSize())
	titleH := t.Dialog.Title.GetVerticalFrameSize() + titleContentHeight
	helpH := t.Dialog.HelpView.GetVerticalFrameSize() + 1
	bodyH := max(0, height-view.GetVerticalFrameSize()-titleH-helpH)

	cw := max(0, innerW-2)
	u.chartLine = -1
	head := []string{u.periodLine(cw), ""}
	body := u.tabLines(cw, max(1, bodyH-len(head)))

	// Scroll the page so its selection (or the user's scroll) shows, then
	// cut it to the room left under the period line.
	room := max(0, bodyH-len(head))
	scroll := u.scroll[u.tab]
	if sel := u.selLine; sel >= 0 {
		if sel < scroll {
			scroll = sel
		} else if sel >= scroll+room {
			scroll = sel - room + 1
		}
	}
	scroll = max(0, min(scroll, len(body)-room))
	u.scroll[u.tab] = scroll
	if scroll < len(body) {
		body = body[scroll:]
	} else {
		body = nil
	}
	if len(body) > room {
		body = body[:room]
	}
	lines := append(head, body...)
	for i, l := range lines {
		lines[i] = " " + padRight(ansi.Truncate(l, cw, "…"), cw) + " "
	}
	for len(lines) < bodyH {
		lines = append(lines, strings.Repeat(" ", innerW))
	}

	rc := NewRenderContext(t, width)
	rc.Title = "Usage"
	rc.TitleInfo = u.tabsView(innerW)
	rc.AddPart(strings.Join(lines, "\n"))
	rc.Help = renderDialogHelp(t, &u.help, u, innerW)
	out := rc.Render()

	// Tell the chart where it is on screen, for the pointer.
	w, h := lipgloss.Size(out)
	origin := common.CenterRect(area, min(w, area.Dx()), min(h, area.Dy())).Min
	if u.chartLine >= 0 {
		y := u.chartLine - scroll
		u.chart.SetOffset(origin.X+view.GetBorderLeftSize()+view.GetPaddingLeft()+1,
			origin.Y+view.GetBorderTopSize()+view.GetPaddingTop()+titleH+len(head)+y)
	}
	DrawCenter(scr, area, out)
	return nil
}

// tabsView renders the tab selector on the title line, numbered.
func (u *Usage) tabsView(width int) string {
	t := u.com.Styles
	var b strings.Builder
	for tab := UsageTabOverview; tab < usageTabCount; tab++ {
		label := tab.String()
		if width >= 90 {
			label = fmt.Sprintf("%d %s", tab+1, label)
		}
		bullet := t.Radio.Off
		if tab == u.tab {
			bullet = t.Radio.On
		}
		b.WriteString(" " + bullet.Render() + t.Radio.Label.Padding(0, 1).Render(label))
	}
	return b.String()
}

// periodLine renders the period selector, with the spinner while loading.
func (u *Usage) periodLine(width int) string {
	t := u.com.Styles
	var b strings.Builder
	b.WriteString(t.Dialog.ListItem.InfoBlurred.Render("Period"))
	for i, p := range usagePeriods {
		bullet := t.Radio.Off
		if i == u.period {
			bullet = t.Radio.On
		}
		label := p.label
		if width >= 70 {
			label += " " + p.key
		}
		b.WriteString(" " + bullet.Render() + t.Radio.Label.Padding(0, 1).Render(label))
	}
	if u.loading {
		b.WriteString(" " + u.spinner.View())
	}
	return b.String()
}

// ShortHelp implements [help.KeyMap].
func (u *Usage) ShortHelp() []key.Binding {
	keys := []key.Binding{u.keyMap.NextTab, u.keyMap.Period}
	switch u.tab {
	case UsageTabOverview:
		keys = append(keys, u.keyMap.Cross)
	case UsageTabLimits, UsageTabModels:
		keys = append(keys, key.NewBinding(key.WithKeys("j", "k"), key.WithHelp("j/k", "select")))
	case UsageTabTraces:
		keys = append(keys, key.NewBinding(key.WithKeys("j", "k"), key.WithHelp("j/k", "select")), u.keyMap.Enter)
	}
	return append(keys, u.keyMap.Refresh, u.keyMap.Close)
}

// FullHelp implements [help.KeyMap].
func (u *Usage) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{u.keyMap.NextTab, u.keyMap.PrevTab, u.keyMap.GoTab, u.keyMap.Period},
		{u.keyMap.Up, u.keyMap.Down, u.keyMap.Cross, u.keyMap.Enter, u.keyMap.Refresh, u.keyMap.Close},
	}
}

// seriesPoints turns a usage series into chart points per agent kind.
func seriesPoints(s usage.Series) map[string][]graph.Point {
	out := make(map[string][]graph.Point, len(s.Kinds))
	for _, k := range s.Kinds {
		vs := s.Tokens[k]
		pts := make([]graph.Point, 0, len(s.Buckets))
		for i, at := range s.Buckets {
			v := 0.0
			if i < len(vs) {
				v = float64(vs[i])
			}
			pts = append(pts, graph.Point{T: at, V: v})
		}
		out[k] = pts
	}
	return out
}

// pctLabel labels a percentage axis.
func pctLabel(v float64) string { return fmt.Sprintf("%.0f%%", v) }

// pick is dark on a dark theme, else light.
func (u *Usage) pick(dark, light color.Color) color.Color {
	if u.com.Styles.IsDark {
		return dark
	}
	return light
}
