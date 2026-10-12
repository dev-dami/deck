package dialog

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	deckconfig "github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/ui/graph"
	"github.com/dev-dami/deck/internal/ui/tone"
	"github.com/dev-dami/deck/internal/usage"
	"github.com/dev-dami/deck/internal/workspace"
)

// The Usage view's pages. Each returns its body lines at width cw, with
// room lines to show them in; the view scrolls what does not fit.

const (
	// usageHeatWidth is the width of a 26-week heatmap: its weekday gutter
	// and a cell and gap per week. Narrower, the heatmap is left out.
	usageHeatWidth = 4 + 2*usageHeatWeeks - 1
	// usageTileWidth is the narrowest an Overview stat tile gets.
	usageTileWidth = 14
)

// usageText are the few text styles every page uses.
type usageText struct {
	heading, text, muted, bold lipgloss.Style
}

func (u *Usage) styles() usageText {
	t := u.com.Styles
	return usageText{
		heading: t.Dialog.PrimaryText.UnsetPadding().Bold(true),
		text:    t.Dialog.ListItem.InfoFocused,
		muted:   t.Dialog.ListItem.InfoBlurred,
		bold:    lipgloss.NewStyle().Foreground(u.theme.Fg).Bold(true),
	}
}

// tint is a style in an agent kind's colour.
func tint(kind string) lipgloss.Style { return lipgloss.NewStyle().Foreground(tone.Tint(kind)) }

// splitLines splits a drawn block into its lines; nothing for "".
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// tabLines is the body of the page shown.
func (u *Usage) tabLines(cw, room int) []string {
	u.selLine = -1
	st := u.styles()
	if u.reader == nil {
		return []string{st.muted.Render("Usage is only counted when Deck runs its agents.")}
	}
	if u.tab == UsageTabLimits {
		// Live limits need no database.
		return u.limitsLines(cw, room)
	}
	switch {
	case u.svc == nil:
		return []string{st.muted.Render("Deck keeps no usage record here: it has no database.")}
	case u.err != nil:
		return []string{u.com.Styles.Dialog.TitleError.Render("Could not read usage: " + u.err.Error())}
	case u.data == nil:
		return []string{u.spinner.View() + " " + st.muted.Render("Counting…")}
	}
	if u.tab != UsageTabBehaviour {
		if u.data.overview.AllTurns == 0 {
			return []string{st.muted.Render(usageEmptyText)}
		}
		if u.data.overview.Turns == 0 {
			return []string{st.muted.Render("Nothing counted " + rangePhrase(usageRange{days: usagePeriods[u.period].days}) + ".")}
		}
	}
	switch u.tab {
	case UsageTabBehaviour:
		return u.behaviourLines(cw)
	case UsageTabTraces:
		return u.traceLines(cw, room)
	case UsageTabModels:
		return u.modelLines(cw)
	case UsageTabCost:
		return u.costLines(cw)
	}
	return u.overviewLines(cw, room)
}

// tile is one headline figure.
type tile struct{ label, value string }

// tileLines lays tiles out in rows as wide as fit in cw, two lines each: a
// quiet label over a bold value, behind an accent rule.
func (u *Usage) tileLines(cw int, tiles []tile) []string {
	st := u.styles()
	const gap = 2
	per := max(1, min(len(tiles), (cw+gap)/(usageTileWidth+gap)))
	w := max(1, (cw-gap*(per-1))/per)
	rule := lipgloss.NewStyle().Foreground(u.theme.Accent).Render("▍")
	var lines []string
	for start := 0; start < len(tiles); start += per {
		var top, bottom strings.Builder
		for i, tl := range tiles[start:min(start+per, len(tiles))] {
			if i > 0 {
				top.WriteString(strings.Repeat(" ", gap))
				bottom.WriteString(strings.Repeat(" ", gap))
			}
			top.WriteString(rule + st.muted.Render(padRight(ansi.Truncate(" "+tl.label, w-1, "…"), w-1)))
			bottom.WriteString(rule + st.bold.Render(padRight(ansi.Truncate(" "+tl.value, w-1, "…"), w-1)))
		}
		lines = append(lines, top.String(), bottom.String())
	}
	return lines
}

// ttftText is a time to first token, "—" when none was measured.
func ttftText(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	return graph.Millis(d.Milliseconds())
}

// overviewLines is the Overview: headline tiles, tokens per agent over
// time with a crosshair, the share of each token type and the heatmap.
func (u *Usage) overviewLines(cw, room int) []string {
	st := u.styles()
	d := u.data
	o := d.overview
	lines := u.tileLines(cw, []tile{
		{"Tokens", graph.Compact(float64(o.Tokens()))},
		{"Turns", fmt.Sprint(o.Turns)},
		{"Agent time", formatSpan(o.Duration)},
		{"Cache hit", fmt.Sprintf("%.0f%%", o.CacheHitPct)},
		{"TTFT avg", ttftText(o.TTFTAvg)},
		{"TTFT p95", ttftText(o.TTFTP95)},
	})
	if n, f := o.ByStatus[usage.StatusInterrupted], o.ByStatus[usage.StatusFailed]; n+f > 0 {
		lines = append(lines, st.muted.Render(fmt.Sprintf("%s interrupted · %s failed", plural(n, "turn", "turns"), fmt.Sprint(f))))
	}

	// Tokens per agent over the period, with the crosshair.
	legend := ""
	for _, k := range d.series.Kinds {
		legend += "  " + tint(k).Render("■") + " " + st.muted.Render(k)
	}
	lines = append(lines, "", st.heading.Render("Tokens by agent")+legend)
	if len(d.series.Kinds) == 0 {
		lines = append(lines, st.muted.Render("No tokens in this period."))
	} else {
		h := max(6, min(14, room/3))
		if u.chart.Width() != cw || u.chart.Height() != h {
			u.chart.Resize(cw, h)
		}
		u.chartLine = len(lines)
		lines = append(lines, splitLines(u.chart.View())...)
	}

	// Token types and the heatmap: side by side when there is room.
	share := append([]string{st.heading.Render("Token types")}, splitLines(u.share(cw))...)
	var heat []string
	if cw >= usageHeatWidth && len(d.heat) > 0 {
		vals := make([]float64, len(d.heat))
		for i, day := range d.heat {
			vals[i] = float64(day.Tokens)
		}
		end := d.heat[len(d.heat)-1].Day
		heat = append([]string{st.heading.Render("Last 26 weeks")}, splitLines(graph.Heatmap(u.theme, usageHeatWidth, end, vals))...)
	}
	const between = 4
	if shareW := cw - usageHeatWidth - between; heat != nil && shareW >= 36 {
		share = append([]string{st.heading.Render("Token types")}, splitLines(u.share(shareW))...)
		lines = append(lines, "")
		for i := range max(len(share), len(heat)) {
			l, r := "", ""
			if i < len(share) {
				l = share[i]
			}
			if i < len(heat) {
				r = heat[i]
			}
			lines = append(lines, padRight(l, shareW)+strings.Repeat(" ", between)+r)
		}
		return lines
	}
	lines = append(lines, "")
	lines = append(lines, share...)
	if heat != nil {
		lines = append(lines, "")
		lines = append(lines, heat...)
	}
	return lines
}

// share is the token-type share bar at width.
func (u *Usage) share(width int) string {
	o := u.data.overview
	return graph.Share(u.theme, width, []graph.Part{
		{Label: "input", Value: float64(o.InputTokens), Color: u.theme.Accent},
		{Label: "cached", Value: float64(o.CachedTokens), Color: u.pick(tone.Sky, tone.LightSky)},
		{Label: "cache write", Value: float64(o.CacheWriteTokens), Color: u.pick(tone.Amber, tone.LightAmber)},
		{Label: "output", Value: float64(o.OutputTokens), Color: u.pick(tone.Moss, tone.LightMoss)},
		{Label: "reasoning", Value: float64(o.ReasoningTokens), Color: u.pick(tone.Plum, tone.LightPlum)},
	})
}

// agentLimits is each agent kind's plan limits, live over stored, in the
// order Deck lists the agents.
func (u *Usage) agentLimits() []AgentLimits {
	var stored []usage.LimitSnapshot
	if u.data != nil {
		stored = u.data.limits
	}
	var live map[string]workspace.LiveLimits
	if u.reader != nil {
		live = u.reader.LiveLimits()
	}
	kinds := u.kinds
	if len(kinds) == 0 {
		kinds = deckconfig.Kinds
	}
	return MergeLimits(kinds, stored, live)
}

// flatWindows is every agent's windows in the order the page lists them.
func (u *Usage) flatWindows() []PlanWindow {
	var out []PlanWindow
	for _, a := range u.agentLimits() {
		out = append(out, a.Windows...)
	}
	return out
}

// selectedWindow is the window j/k selected on the Limits page.
func (u *Usage) selectedWindow() (PlanWindow, bool) {
	ws := u.flatWindows()
	if len(ws) == 0 {
		return PlanWindow{}, false
	}
	return ws[max(0, min(u.limitSel, len(ws)-1))], true
}

// limitsLines is the Limits page: each agent's plan and a meter per window
// of what is left, then the selected window's history.
func (u *Usage) limitsLines(cw, room int) []string {
	st := u.styles()
	now := u.now()
	var lines []string
	idx := 0
	const labelW = 10
	for _, a := range u.agentLimits() {
		head := tint(a.Kind).Render(tone.Mark(a.Kind)+" "+a.Kind) + " "
		if a.Plan != "" {
			head += st.muted.Render("· " + a.Plan + " plan")
		}
		lines = append(lines, head)
		if len(a.Windows) == 0 {
			lines = append(lines, "  "+st.muted.Render(LimitsText(a)))
		}
		for _, w := range a.Windows {
			cursor := "  "
			if idx == u.limitSel {
				cursor = lipgloss.NewStyle().Foreground(u.theme.Accent).Render("▸ ")
				u.selLine = len(lines)
			}
			meter := graph.Meter(u.theme, max(1, cw-2-labelW-1), w.UsedPct, w.Mins, w.ResetsAt, now)
			lines = append(lines, cursor+st.text.Render(padRight(ansi.Truncate(w.Name(), labelW, "…"), labelW))+" "+meter)
			idx++
		}
		lines = append(lines, "")
	}

	w, ok := u.selectedWindow()
	if !ok {
		return lines
	}
	lines = append(lines, st.heading.Render("History")+" "+st.muted.Render("· "+w.Kind+" "+w.Name()+" used, "+rangePhrase(usageRange{days: usagePeriods[u.period].days})))
	if len(u.history) < 2 {
		return append(lines, st.muted.Render("Not enough history yet: a point is kept each time the share used moves."))
	}
	h := max(5, min(10, room/3))
	if u.histChart.Width() != cw || u.histChart.Height() != h {
		u.histChart.Resize(cw, h)
	}
	return append(lines, splitLines(u.histChart.View())...)
}

// behaviourRow is one line of the Behaviour page.
type behaviourRow struct {
	label, kind string
	split       bool // list the count by detail (manual/auto, tool/mention)
}

var behaviourRows = []behaviourRow{
	{"Cancels", usage.BehaviorInterrupt, false},
	{"Steers", usage.BehaviorSteer, false},
	{"Queue cancels", usage.BehaviorQueueCancel, false},
	{"Queue edits", usage.BehaviorQueueEdit, false},
	{"Approvals allowed", usage.BehaviorApproveAllow, true},
	{"Approvals denied", usage.BehaviorApproveDeny, true},
	{"Relays", usage.BehaviorRelay, true},
	{"Hop pauses", usage.BehaviorHopPause, false},
	{"Continues", usage.BehaviorContinue, false},
	{"Errors", usage.BehaviorError, false},
	{"Retries", usage.BehaviorRetry, false},
	{"Mode changes", usage.BehaviorModeChange, false},
	{"Model changes", usage.BehaviorModelChange, false},
	{"Effort changes", usage.BehaviorEffortChange, false},
	{"Task steers", usage.BehaviorTaskSteer, true},
	{"Task retries", usage.BehaviorTaskRetry, false},
	{"Task failures", usage.BehaviorTaskFail, false},
}

// behaviourLines is the Behaviour page: the rates, then a count and a
// daily sparkline per kind of behaviour.
func (u *Usage) behaviourLines(cw int) []string {
	st := u.styles()
	b := u.data.behavior
	pct := func(f float64) string { return fmt.Sprintf("%.0f%%", 100*f) }
	lines := u.tileLines(cw, []tile{
		{"Cancel rate", pct(b.CancelRate)},
		{"Deny rate", pct(b.DenyRate)},
		{"Relay rate", pct(b.RelayRate)},
		{"Turns", fmt.Sprint(b.Turns)},
		{"Interrupted", fmt.Sprint(b.Interrupted)},
		{"Failed", fmt.Sprint(b.Failed)},
	})
	lines = append(lines, "")

	const labelW, countW = 18, 6
	detailW := 24
	if cw < 60 {
		detailW = 0
	}
	sparkW := max(0, cw-labelW-1-countW-1-detailW-1)
	accent := u.theme.Accent
	for _, r := range behaviourRows {
		n := b.Count(r.kind)
		row := st.text.Render(padRight(r.label, labelW)) + " "
		countStyle := st.bold
		if n == 0 {
			countStyle = st.muted
		}
		row += countStyle.Render(fmt.Sprintf("%*d", countW, n)) + " "
		if detailW > 0 {
			detail := ""
			if r.split {
				var parts []string
				for _, c := range b.Counts {
					if c.Kind == r.kind && c.Count > 0 {
						d := c.Detail
						if d == "" {
							d = "other"
						}
						parts = append(parts, fmt.Sprintf("%d %s", c.Count, d))
					}
				}
				detail = strings.Join(parts, " · ")
			}
			row += st.muted.Render(padRight(ansi.Truncate(detail, detailW, "…"), detailW)) + " "
		}
		if days := b.Daily[r.kind]; sparkW >= 4 && len(days) > 0 && n > 0 {
			vals := make([]float64, len(days))
			for i, d := range days {
				vals[i] = float64(d.Count)
			}
			row += graph.Spark(vals, sparkW, u.theme, accent)
		}
		lines = append(lines, row)
	}
	return append(lines, u.habitLines(cw)...)
}

// ago is how long before now at was, short: now, 4m, 3h, Jan 2.
func ago(now, at time.Time) string {
	d := now.Sub(at)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return at.Format("Jan 2")
}

// traceLines is the Traces page: the recent turns, and the selected one's
// spans as a waterfall once enter opens it.
func (u *Usage) traceLines(cw, room int) []string {
	st := u.styles()
	turns := u.data.turns
	now := u.now()
	const markW, tokW, ttftW, durW, statusW, agoW = 3, 7, 7, 8, 14, 7
	modelW := max(4, cw-markW-tokW-ttftW-durW-statusW-agoW)
	cols := func(mark, model, tok, ttft, dur, status, when string) string {
		return padRight(mark, markW) + padRight(ansi.Truncate(model, modelW-1, "…"), modelW) +
			fmt.Sprintf("%*s%*s%*s", tokW, tok, ttftW, ttft, durW, dur) + "  " +
			padRight(status, statusW-2) + fmt.Sprintf("%*s", agoW, when)
	}
	lines := []string{st.muted.Render(cols("", "model", "tokens", "ttft", "time", "status", "ago"))}

	// With a waterfall open the list shrinks to a window round the
	// selection, so both fit.
	first, last := 0, len(turns)
	if u.traceOpen != "" {
		n := max(3, room/3)
		first = max(0, min(u.traceSel-n/2, len(turns)-n))
		last = min(len(turns), first+n)
	}
	errStyle := u.com.Styles.Dialog.TitleError
	for i := first; i < last; i++ {
		tr := turns[i]
		model := tr.Model
		if model == "" {
			model = tr.Kind
		}
		status := tr.Status
		if status == "" {
			status = usage.StatusDone
		}
		row := cols(tone.Mark(tr.Kind), model, graph.Compact(float64(tr.Tokens())), ttftText(tr.TTFT),
			graph.Millis(tr.Duration.Milliseconds()), status, ago(now, tr.At))
		if i == u.traceSel {
			u.selLine = len(lines)
			lines = append(lines, u.com.Styles.Dialog.SelectedItem.UnsetPadding().Render(padRight(row, cw)))
			continue
		}
		mark := tint(tr.Kind).Render(padRight(tone.Mark(tr.Kind), markW))
		rest := ansi.Cut(row, markW, ansi.StringWidth(row))
		if status != usage.StatusDone {
			lines = append(lines, mark+st.text.Render(ansi.Cut(rest, 0, modelW+tokW+ttftW+durW+2))+
				errStyle.Render(ansi.Cut(rest, modelW+tokW+ttftW+durW+2, ansi.StringWidth(rest))))
			continue
		}
		lines = append(lines, mark+st.text.Render(rest))
	}
	if len(turns) == 0 {
		lines = append(lines, st.muted.Render("No turns recorded in this period."))
	}
	if u.traceOpen == "" {
		return lines
	}

	lines = append(lines, "", st.heading.Render("Spans")+" "+st.muted.Render("· span tokens are estimates"))
	switch {
	case u.spansErr != nil:
		return append(lines, errStyle.Render("Could not read the trace: "+u.spansErr.Error()))
	case u.spans == nil:
		return append(lines, u.spinner.View()+" "+st.muted.Render("Reading…"))
	case len(u.spans) == 0:
		return append(lines, st.muted.Render("No spans were recorded for this turn."))
	}
	spans := make([]graph.Span, 0, len(u.spans))
	for _, s := range u.spans {
		name := s.Name
		if name == "" {
			name = map[string]string{usage.SpanThink: "thinking", usage.SpanText: "writing", usage.SpanWait: "waiting"}[s.Kind]
		}
		spans = append(spans, graph.Span{Kind: s.Kind, Name: name, StartMs: s.Start.Milliseconds(), DurMs: s.Dur.Milliseconds(), Tokens: s.Tokens})
	}
	return append(lines, splitLines(graph.Waterfall(u.theme, cw, spans))...)
}

// modelLines is the Models page: a row per model, and the selected one's
// trend over its recent turns.
func (u *Usage) modelLines(cw int) []string {
	st := u.styles()
	models := u.data.models
	const markW, tokW, cacheW, ttftW, turnsW = 3, 9, 8, 8, 7
	modelW := max(4, cw-markW-tokW-cacheW-ttftW-turnsW)
	cols := func(mark, model, tok, cache, ttft, turns string) string {
		return padRight(mark, markW) + padRight(ansi.Truncate(model, modelW-1, "…"), modelW) +
			fmt.Sprintf("%*s%*s%*s%*s", tokW, tok, cacheW, cache, ttftW, ttft, turnsW, turns)
	}
	lines := []string{st.muted.Render(cols("", "model", "tokens", "cache", "ttft", "turns"))}
	name := func(m usage.ModelStat) string {
		if m.Model == "" {
			return m.Kind + " (default model)"
		}
		return m.Model
	}
	for i, m := range models {
		row := cols(tone.Mark(m.Kind), name(m), graph.Compact(float64(m.Tokens())),
			fmt.Sprintf("%.0f%%", m.CacheHitPct), ttftText(m.TTFTAvg), fmt.Sprint(m.Turns))
		if i == u.modelSel {
			u.selLine = len(lines)
			lines = append(lines, u.com.Styles.Dialog.SelectedItem.UnsetPadding().Render(padRight(row, cw)))
			continue
		}
		lines = append(lines, tint(m.Kind).Render(padRight(tone.Mark(m.Kind), markW))+st.text.Render(ansi.Cut(row, markW, ansi.StringWidth(row))))
	}
	if len(models) == 0 {
		return lines
	}

	// The trend comes from the recent turns loaded with the view.
	m := models[u.modelSel]
	var vals []float64
	for i := len(u.data.turns) - 1; i >= 0; i-- {
		if tr := u.data.turns[i]; tr.Kind == m.Kind && tr.Model == m.Model {
			vals = append(vals, float64(tr.Tokens()))
		}
	}
	lines = append(lines, "", st.heading.Render("Trend")+" "+st.muted.Render("· "+name(m)+", tokens per turn"))
	if len(vals) < 2 {
		return append(lines, st.muted.Render("Not enough recent turns for a trend."))
	}
	return append(lines,
		graph.Spark(vals, cw, u.theme, tone.Tint(m.Kind), graph.WithLastValue()),
		st.muted.Render(fmt.Sprintf("last %s, oldest first", plural(len(vals), "turn", "turns"))))
}

// formatCost writes dollars to the cent; a cost too small to show a cent is
// "<$0.01", not "$0.00". Only the Cost page shows dollars.
func formatCost(c float64) string {
	if c > 0 && c < 0.005 {
		return "<$0.01"
	}
	return fmt.Sprintf("$%.2f", c)
}

// costLines is the Cost page, the only place dollars appear: what each CLI
// reported its turns cost. One that reports none shows "—", not $0.
func (u *Usage) costLines(cw int) []string {
	st := u.styles()
	lines := []string{
		st.heading.Render("Reported by each CLI"),
		st.muted.Render("Deck has no pricing of its own; this is what each agent's CLI said."),
		"",
	}
	var bars []graph.Bar
	var none []usage.KindCost
	total, turns, costTurns := 0.0, 0, 0
	labelW := 0
	for _, c := range u.data.cost {
		labelW = max(labelW, len(c.Kind))
		turns += c.Turns
		if !c.Reported {
			none = append(none, c)
			continue
		}
		total += c.CostUSD
		costTurns += c.CostTurns
		bars = append(bars, graph.Bar{Label: c.Kind, Value: c.CostUSD, Color: tone.Tint(c.Kind), Text: formatCost(c.CostUSD)})
	}
	if len(bars) > 0 {
		lines = append(lines, splitLines(graph.Bars(u.theme, cw, bars))...)
	}
	for _, c := range none {
		lines = append(lines, tint(c.Kind).Render(padRight(c.Kind, labelW))+" "+
			st.bold.Render("—")+" "+st.muted.Render("no cost reported · "+plural(c.Turns, "turn", "turns")))
	}
	if len(bars) == 0 && len(none) == 0 {
		return append(lines, st.muted.Render("No turns in this period."))
	}
	lines = append(lines, "")
	if len(bars) > 0 {
		lines = append(lines, st.text.Render("Total reported ")+st.bold.Render(formatCost(total))+
			st.muted.Render(fmt.Sprintf(" · over %d of %s", costTurns, plural(turns, "turn", "turns"))))
	}
	return lines
}

// habitLines is the Habits section of the Behaviour page: how the user works,
// from when they work to how fast they approve and which agent gets what.
func (u *Usage) habitLines(cw int) []string {
	st := u.styles()
	h := u.data.habits
	if h.UserTurns == 0 && h.Approvals.N == 0 && h.Prompt.N == 0 {
		return nil
	}
	dur := func(d time.Duration, n int) string {
		if n == 0 {
			return "—"
		}
		return graph.Millis(d.Milliseconds())
	}
	pct := func(f float64) string { return fmt.Sprintf("%.0f%%", 100*f) }
	lines := []string{"", st.heading.Render("Habits")}
	tiles := []tile{
		{"Active days", fmt.Sprintf("%d · streak %d", h.ActiveDays, h.Streak)},
		{"Review time", dur(h.Review.Median, h.Review.N) + " med"},
		{"Approval wait", dur(h.Approvals.Median, h.Approvals.N) + " med"},
		{"Rubber stamps", fmt.Sprintf("%.0f%%", h.Approvals.FastPct)},
		{"Redirect rate", pct(h.RedirectRate())},
		{"Follow-up rate", pct(h.FollowUpRate())},
		{"Prompt length", fmt.Sprintf("%d chars", h.Prompt.Median)},
		{"Reverts", fmt.Sprint(h.Reverts)},
	}
	if h.EditTurns > 0 {
		tiles = append(tiles, tile{"Edits / turn", fmt.Sprintf("%.1f files", float64(h.EditFiles)/float64(h.EditTurns))})
	}
	lines = append(lines, u.tileLines(cw, tiles)...)

	if wd, hr, n := h.Peak(); n > 0 {
		var byHour [24]float64
		for d := range h.Hours {
			for i, c := range h.Hours[d] {
				byHour[i] += float64(c)
			}
		}
		line := st.text.Render(padRight("Busiest", 18)) + " " +
			st.bold.Render(fmt.Sprintf("%s %02d:00", time.Weekday((wd + 1) % 7).String()[:3], hr)) +
			st.muted.Render(fmt.Sprintf(" · %d turns", n))
		lines = append(lines, "", line)
		if w := min(24, cw-19); w >= 12 {
			lines = append(lines, st.text.Render(padRight("By hour (0-23)", 18))+" "+graph.Spark(byHour[:], w, u.theme, u.theme.Accent))
		}
	}

	if len(h.Interrupts) > 0 {
		where := map[string]int{}
		total := 0
		for _, in := range h.Interrupts {
			where[in.Where] += in.Count
			total += in.Count
		}
		var parts []string
		for _, k := range []string{usage.SpanWait, usage.SpanThink, usage.SpanText, usage.SpanTool, "none"} {
			if n := where[k]; n > 0 {
				label := k
				if k == "none" {
					label = "other"
				}
				parts = append(parts, fmt.Sprintf("%d %s", n, label))
			}
		}
		lines = append(lines, st.text.Render(padRight("Cancelled during", 18))+" "+st.muted.Render(ansi.Truncate(strings.Join(parts, " · "), max(10, cw-19), "…")))
	}

	type mix struct{ read, edit, run, other int }
	byKind := map[string]*mix{}
	var kinds []string
	for _, f := range h.Fit {
		m := byKind[f.Kind]
		if m == nil {
			m = &mix{}
			byKind[f.Kind] = m
			kinds = append(kinds, f.Kind)
		}
		switch f.Class {
		case usage.ClassRead:
			m.read += f.Calls
		case usage.ClassEdit:
			m.edit += f.Calls
		case usage.ClassRun:
			m.run += f.Calls
		default:
			m.other += f.Calls
		}
	}
	for _, k := range kinds {
		m := byKind[k]
		all := float64(m.read + m.edit + m.run + m.other)
		if all == 0 {
			continue
		}
		share := fmt.Sprintf("read %.0f%% · edit %.0f%% · run %.0f%%", 100*float64(m.read)/all, 100*float64(m.edit)/all, 100*float64(m.run)/all)
		lines = append(lines, tint(k).Render(padRight("Your "+k+" work", 18))+" "+st.muted.Render(ansi.Truncate(share, max(10, cw-19), "…")))
	}
	return lines
}
