package tui

import (
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/tui/motion"
	"github.com/dev-dami/deck/internal/tui/theme"
)

// Showing that agents are at work, wherever you look: a live row under each
// working agent's last block (for how long, how much), a spinner by
// its chip, and a band of light running along the rule above the composer
// while anyone in the thread works.

// activityLine is the live row under a working agent: a spinner in its tint,
// how long its turn has run and what it has written so far. What it is doing
// shows just above (the thinking row, the running tool), so it is not repeated.
func (m *model) activityLine(v *threadView, a session.AgentInfo, w int) string {
	spin := tintStyle(a.Kind).Render(motion.NewSpinner().Frame(m.now))
	line := "  " + spin + " " + sMuted.Render("working")
	var facts []string
	if s := v.stats[a.ID]; s != nil && !s.start.IsZero() {
		if d := m.now.Sub(s.start); d >= time.Second {
			facts = append(facts, fmtDur(d))
		}
		if s.tokens > 0 {
			facts = append(facts, fmtTokens(s.tokens)+" tokens")
		}
	}
	if len(facts) > 0 {
		line += sFaint.Render(" " + glyphDot + " " + strings.Join(facts, " "+glyphDot+" "))
	}
	return trunc(line, w)
}

// threadWorking reports whether anyone in the selected thread is at work.
func (m *model) threadWorking() bool {
	th, ok := m.selThread()
	if !ok {
		return false
	}
	for _, a := range th.Members {
		if working(a) {
			return true
		}
	}
	return false
}

// shimmerPeriod is how long the band takes to cross the rule.
const shimmerPeriod = 2200 * time.Millisecond

// ruleLine is the rule above the composer. While the thread works a soft band
// of the accent colour runs along it; with reduced motion the rule is tinted
// instead.
func (m *model) ruleLine(w int) string {
	if !m.threadWorking() || w <= 0 {
		return m.ruleStyle().Render(strings.Repeat(theme.GlyphRule, w))
	}
	yolo := m.curMode() == agent.ModeYolo
	colour := func(level float64) lipgloss.Style {
		if yolo {
			return m.ramp(theme.Faint, theme.Danger, 0.5+0.5*level)
		}
		return m.ramp(theme.Rule, theme.Accent, level)
	}
	if motion.Reduced() {
		return colour(0.45).Render(strings.Repeat(theme.GlyphRule, w))
	}
	band := max(8, w/5)
	span := float64(w + 2*band)
	phase := float64(m.now.UnixMilli()%shimmerPeriod.Milliseconds()) / float64(shimmerPeriod.Milliseconds())
	centre := phase*span - float64(band)
	const steps = 6
	var b strings.Builder
	run, runLevel := 0, -1
	flush := func() {
		if run > 0 {
			b.WriteString(colour(float64(runLevel) / steps).Render(strings.Repeat(theme.GlyphRule, run)))
		}
	}
	for x := 0; x < w; x++ {
		d := math.Abs(float64(x)-centre) / float64(band)
		level := 0
		if d < 1 {
			level = int(math.Round((0.5 + 0.5*math.Cos(d*math.Pi)) * steps))
		}
		if level != runLevel {
			flush()
			run, runLevel = 0, level
		}
		run++
	}
	flush()
	return b.String()
}
