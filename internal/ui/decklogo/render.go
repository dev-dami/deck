package decklogo

import (
	"image/color"
	"os"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/dev-dami/deck/internal/ui/tone"
)

// reduced reports whether the user asked for less motion
// (DECK_REDUCED_MOTION=1): the logo then appears at once and stays still.
func reduced() bool { return os.Getenv("DECK_REDUCED_MOTION") == "1" }

const nLevels = 32

type palEntry struct{ pre, suf string }

// ramp is the colour of the logo at a level from 0 to 1: Faint up to the
// accent at baseLevel, then on towards a pale highlight.
func (m *Model) ramp() (lo, mid, hi color.Color) {
	if m.dark {
		return tone.Faint, tone.Mint, tone.Seafoam
	}
	return tone.LightFaint, tone.LightMint, tone.DeepTeal
}

// palette lazily builds the colour ramp. It is rebuilt when the colour mode
// changes.
func (m *Model) palette() []palEntry {
	if m.pal != nil && m.palDark == m.dark {
		return m.pal
	}
	lo, mid, hi := m.ramp()
	low := lipgloss.Blend1D(101, lo, mid)
	high := lipgloss.Blend1D(101, mid, hi)
	pal := make([]palEntry, nLevels)
	for i := range pal {
		l := float64(i) / float64(nLevels-1)
		var c color.Color
		if l <= baseLevel {
			c = low[int(l/baseLevel*100+0.5)]
		} else {
			c = high[int((l-baseLevel)/(1-baseLevel)*100+0.5)]
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
// width. Static frames are cached.
func (m *Model) View() string {
	if !m.dirty && m.cache != "" {
		return m.cache
	}
	switch m.kind {
	case kindNone:
		m.cache = ""
	case kindTiny:
		accent, fg := tone.Mint, tone.Fg
		if !m.dark {
			accent, fg = tone.LightMint, tone.LightFg
		}
		m.cache = lipgloss.NewStyle().Foreground(accent).Render("◆") + " " +
			lipgloss.NewStyle().Foreground(fg).Bold(true).Render("deck")
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

// Static renders the finished logo at the size that fits w×h cells, without
// any motion: for places drawn once (the exit banner, a narrow header).
func Static(w, h int, dark bool) string {
	m := New(dark)
	m.SetSize(w, h)
	return m.View()
}
