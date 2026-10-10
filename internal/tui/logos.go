package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/dev-dami/deck/internal/brand"
	"github.com/dev-dami/deck/internal/termimg"
)

// Real logos inside the TUI: only where the terminal keeps an image in
// ordinary text cells (kitty's Unicode placeholders, in kitty and Ghostty), so
// redraws can never smear it. Each logo is sent once, in the first frame, and
// every mark of that agent is then two placeholder cells whose colour names
// the image. Elsewhere the marks stay text (✻, >_).

// logoCells is how wide an agent's logo is, in cells (one row high).
const logoCells = 2

// kittyLogoIDs name the logos to the terminal; the id is also the 24-bit
// colour of their placeholder cells.
var kittyLogoIDs = map[string]uint32{"claude": 0x7A0001, "codex": 0x7A0002}

// setupLogos turns the logos on when the terminal can show them.
func (m *model) setupLogos(p termimg.Protocol) {
	if p != termimg.Kitty || lipgloss.ColorProfile() != termenv.TrueColor {
		return // the placeholder colours must reach the terminal exactly
	}
	m.kittyIDs = map[string]uint32{}
	for kind, id := range kittyLogoIDs {
		if _, ok := brand.Logo(kind); ok {
			m.kittyIDs[kind] = id
		}
	}
	m.kittyPending = len(m.kittyIDs) > 0
}

// kittyTransmit sends every logo with a virtual placement of logoCells x 1.
func (m *model) kittyTransmit() string {
	var s string
	for kind, id := range m.kittyIDs {
		if img, ok := brand.Logo(kind); ok {
			s += termimg.KittyTransmit(id, img, logoCells, 1)
		}
	}
	return s
}
