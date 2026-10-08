package tui

import (
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/tui/motion"
	"github.com/dev-dami/deck/internal/tui/theme"
)

const (
	introType = 400 * time.Millisecond
	introTag  = 450 * time.Millisecond
)

// startIntro begins the one-time wordmark animation.
func (m *model) startIntro(now time.Time) {
	m.intro = motion.NewTypewriter(theme.GlyphAgent+" "+theme.Wordmark, introType)
	m.intro.Start(now)
	m.introTag = motion.NewFade(introTag, motion.EaseOutCubic)
	m.introTag.Start(now.Add(introType * 3 / 4))
}

func (m *model) introMoving() bool {
	return m.intro.Moving(m.now) || m.introTag.Moving(m.now)
}

// emptyLines draws the centred wordmark, tagline and hints in a w x h area.
func (m *model) emptyLines(w, h int) []string {
	vis := m.intro.Visible(m.now)
	mark := ""
	if vis != "" {
		rs := []rune(vis)
		mark = sAccent.Render(string(rs[0]))
		if len(rs) > 1 {
			mark += sBold.Render(string(rs[1:]))
		}
	}
	tagCol := m.ramp(theme.Faint, theme.Muted, m.introTag.Progress(m.now))
	tag := tagCol.Render(theme.Tagline)

	kind := m.newKind
	dir := tilde(m.newDir, m.home)
	first := "type to start " + kind + " in " + dir
	if m.newMode && len(m.agents) > 0 {
		first = "type to start a new " + kind + " in " + dir
	}
	other := "codex"
	if kind == "codex" {
		other = "claude"
	}
	hints := []string{first, "@" + other + " to pick an agent", "/ for commands"}

	maxHint := 0
	for _, hline := range hints {
		maxHint = max(maxHint, sw(hline))
	}
	var block []string
	block = append(block, strings.Repeat(" ", max(0, (w-sw(theme.GlyphAgent+" "+theme.Wordmark))/2))+mark)
	block = append(block, centre(tag, w))
	block = append(block, "")
	hl := max(0, (w-maxHint)/2)
	for _, hline := range hints {
		block = append(block, strings.Repeat(" ", hl)+sFaint.Render(trunc(hline, w)))
	}

	top := max(0, (h-len(block))/2-1)
	out := make([]string, 0, h)
	for i := 0; i < top; i++ {
		out = append(out, "")
	}
	out = append(out, block...)
	for len(out) < h {
		out = append(out, "")
	}
	return out[:h]
}
