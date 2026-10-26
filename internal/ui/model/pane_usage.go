package model

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dev-dami/deck/internal/ui/dialog"
	"github.com/dev-dami/deck/internal/ui/graph"
	"github.com/dev-dami/deck/internal/ui/tone"
	"github.com/dev-dami/deck/internal/usage"
	"github.com/dev-dami/deck/internal/workspace"
)

// The Usage pane: for each kind of agent in the session, its plan windows as
// meters of what is left, today's tokens as a sparkline, what it is doing
// now and its last turn. enter opens the full Usage view (alt+u). Plan
// limits are read from memory as they come in; today's tokens and the last
// turns come from Deck's local record, read off the UI loop every
// usagePaneInterval.

func init() { registerPane(paneUsage, func() pane { return &usagePane{} }) }

const (
	// usagePaneWidth is the column the pane asks for: room for a meter with
	// its reset time.
	usagePaneWidth = 44
	// usagePaneInterval is how often the pane rereads the record.
	usagePaneInterval = 30 * time.Second
	// usagePaneTimeout bounds one read.
	usagePaneTimeout = 5 * time.Second
	// usagePaneLabel is the width of the labels left of the meters.
	usagePaneLabel = 7
)

// usagePaneMsg carries what the pane reads from the record.
type usagePaneMsg struct {
	today  usage.Series // today's tokens per agent kind, by hour
	limits []usage.LimitSnapshot
	turns  []usage.Turn // the latest turns, newest first
	habits usage.Habits // today's habits
	err    error
}

// usagePaneTickMsg asks the pane to read the record again.
type usagePaneTickMsg struct{}

type usagePane struct {
	cache paneText
	data  usagePaneMsg // the last good read

	theme    graph.Theme
	themeKey string
}

func (*usagePane) id() paneID       { return paneUsage }
func (*usagePane) title(*UI) string { return "Usage" }
func (*usagePane) width() int       { return usagePaneWidth }

// badge is the least left of any window shown, e.g. "38% left".
func (p *usagePane) badge(m *UI) string {
	left := -1.0
	for _, a := range p.limits(m) {
		for _, w := range a.Windows {
			if l := 100 - w.UsedPct; left < 0 || l < left {
				left = l
			}
		}
	}
	if left < 0 {
		return ""
	}
	return fmt.Sprintf("%d%% left", int(math.Round(max(0, left))))
}

func (p *usagePane) height(m *UI, width int) int {
	return p.cache.lines(width, func() string { return p.text(m, width, time.Now()) })
}

func (p *usagePane) render(m *UI, width, _ int, _ bool) string {
	return p.cache.get(width, func() string { return p.text(m, width, time.Now()) })
}

func (*usagePane) key(m *UI, msg tea.KeyPressMsg) (bool, tea.Cmd) {
	if msg.String() == "enter" {
		return true, m.openUsageDialog()
	}
	return false, nil
}

func (*usagePane) click(*UI, int, int) tea.Cmd { return nil }

// kinds are the session's kinds of agent, in roster order.
func (*usagePane) kinds(m *UI) []string {
	var out []string
	for _, a := range m.sessionRoster() {
		if a.Kind != "" && !slices.Contains(out, a.Kind) {
			out = append(out, a.Kind)
		}
	}
	return out
}

// limits are the session's agents' plan limits, live over stored.
func (p *usagePane) limits(m *UI) []dialog.AgentLimits {
	kinds := p.kinds(m)
	if len(kinds) == 0 {
		return nil
	}
	var live map[string]workspace.LiveLimits
	if r, ok := m.com.Workspace.(workspace.UsageReader); ok {
		live = r.LiveLimits()
	}
	out := dialog.MergeLimits(kinds, p.data.limits, live)
	return slices.DeleteFunc(out, func(a dialog.AgentLimits) bool { return !slices.Contains(kinds, a.Kind) })
}

// graphTheme is the chart theme, rebuilt when Deck's theme changes.
func (p *usagePane) graphTheme(m *UI) graph.Theme {
	t := m.com.Styles
	k := fmt.Sprint(t.IsDark, t.Background, t.Dialog.TitleGradFromColor, t.Dialog.NormalItem.GetForeground())
	if k != p.themeKey {
		p.themeKey, p.theme = k, graph.FromStyles(t)
	}
	return p.theme
}

func (p *usagePane) text(m *UI, width int, now time.Time) string {
	if width <= 0 {
		return ""
	}
	st := m.stripStyles()
	kinds := p.kinds(m)
	if len(kinds) == 0 {
		return st.muted.Render("No agents yet")
	}
	th := p.graphTheme(m)
	roster := m.sessionRoster()
	label := func(s string) string {
		return st.muted.Render(s + strings.Repeat(" ", max(0, usagePaneLabel-ansi.StringWidth(s))))
	}
	tint := func(kind string) lipgloss.Style { return lipgloss.NewStyle().Foreground(tone.Tint(kind)) }
	barW := max(1, width-usagePaneLabel)

	var lines []string
	for i, a := range p.limits(m) {
		if i > 0 {
			lines = append(lines, "")
		}
		head := tint(a.Kind).Render(tone.Mark(a.Kind) + " " + a.Kind)
		if a.Plan != "" {
			head += st.muted.Render(" · " + a.Plan)
		}
		lines = append(lines, ansi.Truncate(head, width, "…"))
		for _, w := range a.Windows {
			lines = append(lines, label(ansi.Truncate(w.Name(), usagePaneLabel-1, "…"))+graph.Meter(th, barW, w.UsedPct, w.Mins, w.ResetsAt, now))
		}
		if len(a.Windows) == 0 {
			lines = append(lines, st.muted.Render(ansi.Truncate(dialog.LimitsText(a), width, "…")))
		}

		// Today's tokens, by hour.
		if vals := p.today(a.Kind); len(vals) > 0 {
			lines = append(lines, label("today")+graph.Spark(vals, barW, th, tone.Tint(a.Kind), graph.WithLastValue()))
		}

		// What its agents are doing now, then its last turn.
		for _, r := range roster {
			if r.Kind != a.Kind || r.Since.IsZero() {
				continue
			}
			doing := r.ID + " " + fmtElapsed(now.Sub(r.Since).Truncate(time.Second))
			if r.ContextTokens > 0 {
				doing += " · ctx " + graph.Compact(float64(r.ContextTokens))
			}
			lines = append(lines, label("now")+ansi.Truncate(doing, barW, "…"))
		}
		lines = append(lines, p.contextLines(roster, a.Kind, th, label, barW)...)
		if t, ok := p.last(a.Kind); ok {
			last := graph.Compact(float64(t.Tokens())) + " tok"
			if t.TTFT > 0 {
				last += " · ttft " + graph.Millis(t.TTFT.Milliseconds())
			}
			last += " · " + graph.Millis(t.Duration.Milliseconds())
			lines = append(lines, label("last")+st.muted.Render(ansi.Truncate(last, barW, "…")))
		}
	}
	if h := p.habitLine(); h != "" {
		lines = append(lines, "", label("you")+st.muted.Render(ansi.Truncate(h, barW, "…")))
	}
	lines = append(lines, "", st.muted.Render(ansi.Truncate("enter or "+dialog.UsageKey+" for the full view", width, "…")))
	return strings.Join(lines, "\n")
}

// contextLines are a meter of each of kind's agents' context, split into
// what its session started with (baseline, neutral), the conversation (the
// agent's tint), Deck's own text (the accent) and what is free, with a
// legend under it. The baseline and Deck's share are Deck's estimates; an
// agent whose fill or window is not known gets no meter.
func (p *usagePane) contextLines(roster []workspace.AgentLine, kind string, th graph.Theme, label func(string) string, barW int) []string {
	var agents []workspace.AgentLine
	for _, r := range roster {
		if r.Kind == kind {
			agents = append(agents, r)
		}
	}
	muted := lipgloss.NewStyle().Foreground(th.Subtle)
	var lines []string
	for _, r := range agents {
		base, conv, deck, _, ok := r.ContextParts()
		if !ok {
			continue
		}
		pct, approx, _ := r.ContextPct()
		status := fmt.Sprintf("%d%%", pct)
		if approx {
			status = "~" + status
		}
		tint := tone.Tint(kind)
		parts := []graph.Part{
			{Label: "base", Value: float64(base), Color: th.Subtle},
			{Label: "chat", Value: float64(conv), Color: tint},
			{Label: "deck", Value: float64(deck), Color: th.Accent},
		}
		w := max(1, barW-len(status)-1)
		lines = append(lines, label("ctx")+graph.Stack(th, w, parts, float64(r.ContextWindow))+" "+status)

		var legend []string
		if len(agents) > 1 {
			legend = append(legend, muted.Render(r.ID))
		}
		for _, part := range parts {
			if part.Value <= 0 && part.Label != "chat" {
				continue // not known (or none): no key for it
			}
			key := lipgloss.NewStyle().Foreground(part.Color).Render("■")
			legend = append(legend, key+" "+muted.Render(part.Label+" "+graph.Compact(part.Value)))
		}
		lines = append(lines, label("")+ansi.Truncate(strings.Join(legend, "  "), barW, "…"))
	}
	return lines
}

// today is kind's tokens per hour today, or nil when it has none.
func (p *usagePane) today(kind string) []float64 {
	vs := p.data.today.Tokens[kind]
	if len(vs) == 0 {
		return nil
	}
	out := make([]float64, len(vs))
	for i, v := range vs {
		out[i] = float64(v)
	}
	return out
}

// last is kind's latest recorded turn.
func (p *usagePane) last(kind string) (usage.Turn, bool) {
	for _, t := range p.data.turns {
		if t.Kind == kind {
			return t, true
		}
	}
	return usage.Turn{}, false
}

// usagePane is the strip's Usage pane.
func (m *UI) usagePane() *usagePane {
	p, _ := m.strip().live[paneUsage].(*usagePane)
	return p
}

// loadUsagePane reads what the Usage pane shows from the record, off the
// UI loop. Without a record there is nothing to read, ever.
func (m *UI) loadUsagePane() tea.Cmd {
	if m.com == nil || m.com.Workspace == nil {
		return nil
	}
	r, ok := m.com.Workspace.(workspace.UsageReader)
	if !ok {
		return nil
	}
	svc := r.UsageService()
	if svc == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), usagePaneTimeout)
		defer cancel()
		var msg usagePaneMsg
		msg.today, msg.err = svc.Series(ctx, usage.Since(1, time.Now()), usage.Hourly)
		if msg.err == nil {
			msg.limits, msg.err = svc.Limits(ctx)
		}
		if msg.err == nil {
			msg.turns, msg.err = svc.Traces(ctx, 20)
		}
		if msg.err == nil {
			msg.habits, msg.err = svc.Habits(ctx, usage.Since(1, time.Now()))
		}
		return msg
	}
}

// setUsagePane keeps what the pane read and schedules the next read. A
// failed read keeps what it had.
func (m *UI) setUsagePane(msg usagePaneMsg) tea.Cmd {
	if p := m.usagePane(); p != nil && msg.err == nil {
		p.data = msg
	}
	return tea.Tick(usagePaneInterval, func(time.Time) tea.Msg { return usagePaneTickMsg{} })
}

// usagePaneTick rereads the record while the pane can be seen, and
// otherwise waits for the next tick.
func (m *UI) usagePaneTick() tea.Cmd {
	if m.state == uiChat && !m.isCompact && m.strip().isOpen(paneUsage) {
		return m.loadUsagePane()
	}
	return tea.Tick(usagePaneInterval, func(time.Time) tea.Msg { return usagePaneTickMsg{} })
}

// habitLine is today's habits in a line: the median wait to review a turn
// and to answer an approval, and how many turns were cancelled. "" when
// there is nothing to say.
func (p *usagePane) habitLine() string {
	h := p.data.habits
	var parts []string
	if h.Review.N > 0 {
		parts = append(parts, "review "+graph.Millis(h.Review.Median.Milliseconds()))
	}
	if h.Approvals.N > 0 {
		parts = append(parts, "approve "+graph.Millis(h.Approvals.Median.Milliseconds()))
	}
	if n := interruptTotal(h); n > 0 {
		parts = append(parts, fmt.Sprintf("%d cancelled", n))
	}
	return strings.Join(parts, " · ")
}

func interruptTotal(h usage.Habits) int {
	n := 0
	for _, in := range h.Interrupts {
		n += in.Count
	}
	return n
}
