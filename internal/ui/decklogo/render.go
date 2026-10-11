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

// Palette is the colours the wordmark is drawn in. The letterforms run on
// a ramp from Low (the faintest dots) up to Accent at rest, and on towards
// High where the shimmer and ripples brighten them. Name is the "deck" of
// the one-line form, beside a ◆ in Accent.
type Palette struct {
	Low    color.Color
	Accent color.Color
	High   color.Color
	Name   color.Color
}

// DefaultPalette is Deck's own mint wordmark for a dark or light ground.
func DefaultPalette(dark bool) Palette {
	if dark {
		return Palette{Low: tone.Faint, Accent: tone.Mint, High: tone.Seafoam, Name: tone.Fg}
	}
	return Palette{Low: tone.LightFaint, Accent: tone.LightMint, High: tone.DeepTeal, Name: tone.LightFg}
}

// fill returns p with its unset colours taken from def.
func (p Palette) fill(def Palette) Palette {
	if p.Low == nil {
		p.Low = def.Low
	}
	if p.Accent == nil {
		p.Accent = def.Accent
	}
	if p.High == nil {
		p.High = def.High
	}
	if p.Name == nil {
		p.Name = def.Name
	}
	return p
}

// equal reports whether two palettes draw the same colours.
func (p Palette) equal(o Palette) bool {
	return sameColor(p.Low, o.Low) && sameColor(p.Accent, o.Accent) &&
		sameColor(p.High, o.High) && sameColor(p.Name, o.Name)
}

func sameColor(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

// palette lazily builds the colour ramp. It is rebuilt when the colours
// change.
func (m *Model) palette() []palEntry {
	if m.pal != nil {
		return m.pal
	}
	lo, mid, hi := m.colors.Low, m.colors.Accent, m.colors.High
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
	m.pal = pal
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
		m.cache = lipgloss.NewStyle().Foreground(m.colors.Accent).Render("◆") + " " +
			lipgloss.NewStyle().Foreground(m.colors.Name).Bold(true).Render("deck")
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
	return StaticPalette(w, h, DefaultPalette(dark))
}

// StaticPalette is Static drawn in the given colours.
func StaticPalette(w, h int, p Palette) string {
	m := NewWithPalette(p)
	m.SetSize(w, h)
	return m.View()
}
