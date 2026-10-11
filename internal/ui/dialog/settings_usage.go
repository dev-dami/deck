package dialog

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/dev-dami/deck/internal/ui/graph"
	"github.com/dev-dami/deck/internal/usage"
)

// The Settings dialog's Usage page: a compact token summary (totals, a bar
// per agent, a daily sparkline). Everything else, cost included, is in the
// full Usage view (alt+u).

// usageEmptyText is shown before any turn has been counted.
const usageEmptyText = "No usage yet — it's counted from now on."

// usageFullViewHint points from the summary to the full view.
const usageFullViewHint = "alt+u for the full view: limits, behaviour, traces, models, cost"

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
	hint := line(t.Dialog.ListItem.InfoBlurred.Render(usageFullViewHint))

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
		return append(lines, "", muted.Width(width).Render(msg), "", hint)
	}

	th := graph.FromStyles(t)
	totals := t.Dialog.PrimaryText.Bold(true).UnsetPadding().Render(formatTokens(r.Totals.Tokens())+" tokens") +
		t.Dialog.ListItem.InfoFocused.Render("  "+plural(r.Totals.Turns, "turn", "turns")+" · "+formatSpan(r.Totals.Duration)+" of agent time")
	lines = append(lines, "", line(totals),
		line(t.Dialog.ListItem.InfoBlurred.Render(fmt.Sprintf("%s in · %s out · %s cached",
			formatTokens(r.Totals.InputTokens), formatTokens(r.Totals.OutputTokens), formatTokens(r.Totals.CachedTokens)))))

	if len(r.Kinds) > 0 {
		bars := make([]graph.Bar, 0, len(r.Kinds))
		for _, g := range r.Kinds {
			bars = append(bars, graph.Bar{Label: g.Kind, Value: float64(g.Tokens())})
		}
		lines = append(lines, "", t.Dialog.PrimaryText.Render("By agent"))
		for _, l := range strings.Split(graph.Bars(th, text, bars), "\n") {
			lines = append(lines, " "+l)
		}
	}

	if len(r.Days) > 1 {
		lines = append(lines, "")
		lines = append(lines, s.dailyLines(r.Days, th, width)...)
	}
	return append(lines, "", hint)
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

// dailyLines draws the per-day token sparkline with its span and peak.
func (s *Settings) dailyLines(days []usage.Day, th graph.Theme, width int) []string {
	t := s.com.Styles
	text := max(0, width-2)
	values := make([]float64, len(days))
	peak, peakDay := 0.0, days[0].Day
	for i, d := range days {
		values[i] = float64(d.Tokens)
		if values[i] > peak {
			peak, peakDay = values[i], d.Day
		}
	}
	span := days[0].Day.Format("Jan 2") + " – today"
	if peak > 0 {
		span += " · peak " + formatTokens(int64(peak)) + " on " + peakDay.Format("Jan 2")
	}
	return []string{
		t.Dialog.PrimaryText.Render("Daily tokens"),
		" " + graph.Spark(values, text, th, th.Accent),
		" " + t.Dialog.ListItem.InfoBlurred.Render(ansi.Truncate(span, text, "…")),
	}
}
