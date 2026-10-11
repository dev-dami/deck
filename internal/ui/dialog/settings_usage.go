package dialog

import (
	"fmt"
	"math"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dev-dami/deck/internal/ui/tone"
	"github.com/dev-dami/deck/internal/usage"
)

// usageEmptyText is shown before any turn has been counted.
const usageEmptyText = "No usage yet — it's counted from now on."

// maxUsageModels caps the per-model bars.
const maxUsageModels = 5

var (
	sparkRunes = []rune("▁▂▃▄▅▆▇█")
	// barEighths are the partial blocks a bar ends on, by eighths.
	barEighths = []rune(" ▏▎▍▌▋▊▉")
)

// sparkline draws values, oldest first, as one row of blocks scaled to the
// largest; it keeps the newest width of them. A zero is the lowest block
// and anything above zero at least the second, so a quiet day and a light
// one look different.
func sparkline(values []float64, width int) string {
	if width <= 0 || len(values) == 0 {
		return ""
	}
	if len(values) > width {
		values = values[len(values)-width:]
	}
	top := 0.0
	for _, v := range values {
		top = max(top, v)
	}
	var b strings.Builder
	for _, v := range values {
		i := 0
		if v > 0 && top > 0 {
			i = min(len(sparkRunes)-1, max(1, int(math.Ceil(v/top*float64(len(sparkRunes)-1)))))
		}
		b.WriteRune(sparkRunes[i])
	}
	return b.String()
}

// usageBar draws value against top as a bar of width cells, to the eighth
// of a cell, padded with spaces to width.
func usageBar(value, top float64, width int) string {
	if width <= 0 {
		return ""
	}
	eighths := 0
	if top > 0 && value > 0 {
		eighths = max(1, int(math.Round(min(1, value/top)*float64(width*8))))
	}
	full, part := eighths/8, eighths%8
	var b strings.Builder
	b.WriteString(strings.Repeat("█", full))
	cells := full
	if part > 0 {
		b.WriteRune(barEighths[part])
		cells++
	}
	b.WriteString(strings.Repeat(" ", width-cells))
	return b.String()
}

// formatTokens writes a token count short: 950, 12.3k, 4.5M, 1.2B.
func formatTokens(n int64) string {
	f := float64(n)
	switch {
	case n >= 1_000_000_000:
		return trimZero(fmt.Sprintf("%.1f", f/1e9)) + "B"
	case n >= 1_000_000:
		return trimZero(fmt.Sprintf("%.1f", f/1e6)) + "M"
	case n >= 1_000:
		return trimZero(fmt.Sprintf("%.1f", f/1e3)) + "k"
	}
	return fmt.Sprintf("%d", n)
}

func trimZero(s string) string { return strings.TrimSuffix(s, ".0") }

// formatCost writes dollars to the cent; a cost too small to show a cent is
// "<$0.01", not "$0.00".
func formatCost(c float64) string {
	if c > 0 && c < 0.005 {
		return "<$0.01"
	}
	return fmt.Sprintf("$%.2f", c)
}

// formatSpan writes agent time short: 45s, 12m, 3h 05m.
func formatSpan(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// usageLines renders the Usage page.
func (s *Settings) usageLines(width int) []string {
	t := s.com.Styles
	muted := t.Dialog.SecondaryText
	text := max(0, width-2)
	line := func(str string) string { return " " + ansi.Truncate(str, text, "…") }

	head := s.rangeView()
	if s.loading && s.report != nil {
		head += " " + s.spinner.View()
	}
	lines := []string{line(head)}
	switch {
	case s.reader == nil:
		return append(lines, "", muted.Width(width).Render("Usage is only counted when Deck runs its agents."))
	case s.usageErr != nil:
		return append(lines, "", t.Dialog.TitleError.PaddingLeft(1).Render(ansi.Truncate("Could not read usage: "+s.usageErr.Error(), text, "…")))
	case s.report == nil:
		return append(lines, "", line(s.spinner.View()+" "+t.Dialog.ListItem.InfoBlurred.Render("Counting…")))
	}
	r := *s.report
	if r.Empty() {
		msg := usageEmptyText
		if r.AllTurns > 0 {
			msg = "Nothing counted " + strings.ToLower(rangePhrase(usageRanges[s.rng])) + "."
		}
		return append(lines, "", muted.Width(width).Render(msg))
	}

	byCost := r.Totals.CostUSD > 0
	totals := t.Dialog.PrimaryText.Bold(true).UnsetPadding().Render(formatCost(r.Totals.CostUSD)) +
		t.Dialog.ListItem.InfoFocused.Render("  "+plural(r.Totals.Turns, "turn", "turns")+" · "+formatSpan(r.Totals.Duration)+" of agent time")
	lines = append(lines, "", line(totals),
		line(t.Dialog.ListItem.InfoBlurred.Render(fmt.Sprintf("%s in · %s out · %s cached",
			formatTokens(r.Totals.InputTokens), formatTokens(r.Totals.OutputTokens), formatTokens(r.Totals.CachedTokens)))))

	lines = append(lines, "", t.Dialog.PrimaryText.Render("By agent"))
	lines = append(lines, s.groupLines(r.Kinds, false, byCost, text)...)

	if len(r.Models) > 0 {
		lines = append(lines, "", t.Dialog.PrimaryText.Render("By model"))
		models := r.Models
		if len(models) > maxUsageModels {
			models = models[:maxUsageModels]
		}
		lines = append(lines, s.groupLines(models, true, byCost, text)...)
		if more := len(r.Models) - len(models); more > 0 {
			lines = append(lines, line(t.Dialog.ListItem.InfoBlurred.Render(fmt.Sprintf("    and %d more", more))))
		}
	}

	if len(r.Days) > 1 {
		lines = append(lines, "")
		lines = append(lines, s.dailyLines(r.Days, byCost, width)...)
	}

	if len(r.Sessions) > 0 {
		lines = append(lines, "", t.Dialog.PrimaryText.Render("Top sessions"))
		for _, ss := range r.Sessions {
			lines = append(lines, s.sessionLine(ss, byCost, text))
		}
	}
	return lines
}

// rangeView renders the range selector.
func (s *Settings) rangeView() string {
	t := s.com.Styles
	var b strings.Builder
	b.WriteString(t.Dialog.ListItem.InfoBlurred.Render("Range"))
	for i, r := range usageRanges {
		bullet := t.Radio.Off
		if i == s.rng {
			bullet = t.Radio.On
		}
		b.WriteString(" " + bullet.Render() + t.Radio.Label.Padding(0, 1).Render(r.label))
	}
	return b.String()
}

func rangePhrase(r usageRange) string {
	switch r.days {
	case 0:
		return "at all"
	case 1:
		return "today"
	}
	return fmt.Sprintf("in the last %d days", r.days)
}

// groupLines draws a bar per agent kind (or per model), tinted by kind and
// measured by cost when there is any, else by tokens.
func (s *Settings) groupLines(groups []usage.Group, models, byCost bool, width int) []string {
	t := s.com.Styles
	measure := func(g usage.Group) float64 {
		if byCost {
			return g.CostUSD
		}
		return float64(g.Tokens())
	}
	top := 0.0
	for _, g := range groups {
		top = max(top, measure(g))
	}
	nameW := 12
	if models {
		nameW = 20
	}
	const figW, restW = 8, 10
	barW := max(4, width-settingsMarkWidth-1-nameW-1-1-figW-1-restW)
	nameW = min(nameW, max(0, width-settingsMarkWidth-1-barW-1-1-figW-1-restW))

	lines := make([]string, 0, len(groups))
	for _, g := range groups {
		name := g.Kind
		if models {
			name = g.Model
			if name == "" {
				name = "default model"
			}
		}
		fig, rest := formatTokens(g.Tokens()), plural(g.Turns, "turn", "turns")
		if byCost {
			fig, rest = formatCost(g.CostUSD), formatTokens(g.Tokens())+" tok"
		}
		tint := lipgloss.NewStyle().Foreground(tone.Tint(g.Kind))
		row := tint.Render(padRight(tone.Mark(g.Kind), settingsMarkWidth)) + " " +
			t.Dialog.ListItem.InfoFocused.Render(padRight(ansi.Truncate(name, nameW, "…"), nameW)) + " " +
			tint.Render(usageBar(measure(g), top, barW)) + " " +
			t.Dialog.ListItem.InfoFocused.Render(fmt.Sprintf("%*s", figW, fig)) + " " +
			t.Dialog.ListItem.InfoBlurred.Render(ansi.Truncate(rest, restW, "…"))
		lines = append(lines, " "+row)
	}
	return lines
}

// dailyLines draws the per-day sparkline with its scale and span.
func (s *Settings) dailyLines(days []usage.Day, byCost bool, width int) []string {
	t := s.com.Styles
	text := max(0, width-2)
	values := make([]float64, len(days))
	peak, peakDay := 0.0, days[0].Day
	for i, d := range days {
		v := float64(d.Tokens)
		if byCost {
			v = d.CostUSD
		}
		values[i] = v
		if v > peak {
			peak, peakDay = v, d.Day
		}
	}
	shown := min(len(days), text)
	first := days[len(days)-shown].Day

	title, peakText := "Daily tokens", formatTokens(int64(peak))
	if byCost {
		title, peakText = "Daily cost", formatCost(peak)
	}
	spark := lipgloss.NewStyle().Foreground(t.Dialog.TitleGradFromColor).Render(sparkline(values, shown))
	span := first.Format("Jan 2") + " – today"
	if peak > 0 {
		span += " · peak " + peakText + " on " + peakDay.Format("Jan 2")
	}
	return []string{
		t.Dialog.PrimaryText.Render(title),
		" " + spark,
		" " + t.Dialog.ListItem.InfoBlurred.Render(ansi.Truncate(span, text, "…")),
	}
}

// sessionLine draws one of the costliest sessions.
func (s *Settings) sessionLine(ss usage.Session, byCost bool, width int) string {
	t := s.com.Styles
	const figW = 8
	fig := formatTokens(ss.Tokens)
	if byCost {
		fig = formatCost(ss.CostUSD)
	}
	title := ss.Title
	if title == "" {
		title = "(deleted session)"
	}
	turns := plural(ss.Turns, "turn", "turns")
	titleW := max(0, width-figW-1-1-ansi.StringWidth(turns))
	return " " + t.Dialog.ListItem.InfoFocused.Render(fmt.Sprintf("%*s", figW, fig)) + " " +
		t.Dialog.ListItem.InfoFocused.Render(padRight(ansi.Truncate(title, titleW, "…"), titleW)) + " " +
		t.Dialog.ListItem.InfoBlurred.Render(turns)
}
