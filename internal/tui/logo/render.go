package logo

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dev-dami/deck/internal/tui/motion"
	"github.com/dev-dami/deck/internal/tui/theme"
)

const nLevels = 32

type palEntry struct{ pre, suf string }

// palette lazily builds the colour ramp: Faint -> Accent (at baseLevel) ->
// highlight. It is rebuilt when the colour mode changes.
func (m *Model) palette() []palEntry {
	if m.pal != nil && m.palDark == m.dark {
		return m.pal
	}
	hi := lipgloss.AdaptiveColor{
		Light: string(motion.Blend(theme.Accent, theme.Fg, 0.8, false)),
		Dark:  string(motion.Blend(theme.Accent, theme.Fg, 0.8, true)),
	}
	pal := make([]palEntry, nLevels)
	for i := range pal {
		l := float64(i) / float64(nLevels-1)
		var c lipgloss.Color
		if l <= baseLevel {
			c = motion.Blend(theme.Faint, theme.Accent, l/baseLevel, m.dark)
		} else {
			c = motion.Blend(theme.Accent, hi, (l-baseLevel)/(1-baseLevel), m.dark)
		}
		pal[i] = splitStyle(lipgloss.NewStyle().Foreground(c))
	}
	m.pal, m.palDark = pal, m.dark
	return pal
}

// splitStyle returns the escape sequences a style wraps around text.
func splitStyle(s lipgloss.Style) palEntry {
	out := s.Render("X")
	i := strings.IndexByte(out, 'X')
	if i < 0 {
		return palEntry{}
	}
	return palEntry{pre: out[:i], suf: out[i+1:]}
}

// View returns the logo as Bounds() cells: every line has the same display
// width, coloured with lipgloss. Static frames are cached.
func (m *Model) View() string {
	if !m.dirty && m.cache != "" {
		return m.cache
	}
	switch m.kind {
	case kindNone:
		m.cache = ""
	case kindTiny:
		col := func(c lipgloss.AdaptiveColor) lipgloss.Style {
			return lipgloss.NewStyle().Foreground(motion.Blend(c, c, 0, m.dark))
		}
		m.cache = col(theme.Accent).Render(theme.GlyphAgent) + " " + col(theme.Fg).Bold(true).Render(theme.Wordmark)
	default:
		m.compose()
		m.cache = m.emit()
	}
	m.dirty = false
	return m.cache
}

// emit turns the composed cell grid into a string.
func (m *Model) emit() string {
	pal := m.palette()
	b := m.buf[:0]
	for cy := 0; cy < m.rows; cy++ {
		cur := -1
		for cx := 0; cx < m.cols; cx++ {
			c := cy*m.cols + cx
			bits := m.bits[c]
			if bits == 0 {
				b = append(b, ' ')
				continue
			}
			idx := int(m.sum[c]/float32(m.cnt[c])*(nLevels-1) + 0.5)
			if idx != cur {
				b = append(b, pal[idx].pre...)
				cur = idx
			}
			// U+2800 + bits, encoded as UTF-8 (E2 A0..A3 80..BF).
			v := 0x2800 + rune(bits)
			b = append(b, 0xE0|byte(v>>12), 0x80|byte(v>>6)&0x3F, 0x80|byte(v)&0x3F)
		}
		if cur >= 0 {
			b = append(b, pal[cur].suf...)
		}
		if cy < m.rows-1 {
			b = append(b, '\n')
		}
	}
	m.buf = b
	return string(b)
}
