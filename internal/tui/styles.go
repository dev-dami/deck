// Package tui is Deck's terminal UI: a calm, dense Bubble Tea app that shows
// many coding agents side by side. All colours and glyphs come from
// internal/tui/theme and all movement from internal/tui/motion.
package tui

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dev-dami/deck/internal/tui/motion"
	"github.com/dev-dami/deck/internal/tui/theme"
)

// Extra glyphs that are structural rather than themed.
const (
	glyphThink  = "∴"
	glyphPrompt = "› "
	glyphBarSel = "▎"
	glyphWarn   = theme.GlyphApproval
	glyphErr    = "✗"
	glyphMinus  = "−"
	glyphDot    = "·"
)

// maxTextWidth caps prose width so wide terminals stay readable.
const maxTextWidth = 100

func sw(s string) int { return ansi.StringWidth(s) }

// trunc cuts s to n cells, ending with an ellipsis when shortened.
func trunc(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if sw(s) <= n {
		return s
	}
	return ansi.Truncate(s, n, "…")
}

// padTo pads s with spaces to n cells (never truncates).
func padTo(s string, n int) string {
	if d := n - sw(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// fit truncates then pads to exactly n cells.
func fit(s string, n int) string { return padTo(trunc(s, n), n) }

// rightAlign puts r at the right edge of an n-cell line after l; l is cut first.
func rightAlign(l, r string, n int) string {
	rw := sw(r)
	if rw >= n {
		return trunc(r, n)
	}
	l = trunc(l, n-rw-1)
	gap := n - sw(l) - rw
	if gap < 1 {
		gap = 1
	}
	return l + strings.Repeat(" ", gap) + r
}

func centre(s string, n int) string {
	if d := n - sw(s); d > 1 {
		return strings.Repeat(" ", d/2) + s
	}
	return s
}

var tabRe = regexp.MustCompile(`\t`)

// clean makes arbitrary agent text safe to lay out: no escapes, no tabs, no CRs.
func clean(s string) string {
	s = ansi.Strip(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return tabRe.ReplaceAllString(s, "    ")
}

func oneLine(s string) string { return strings.Join(strings.Fields(clean(s)), " ") }

// wrapText word-wraps plain text to n cells, keeping explicit newlines.
func wrapText(s string, n int) []string {
	if n < 1 {
		n = 1
	}
	s = strings.TrimRight(clean(s), "\n ")
	if s == "" {
		return nil
	}
	return strings.Split(ansi.Wrap(s, n, ""), "\n")
}

func indent(lines []string, n int) []string {
	pad := strings.Repeat(" ", n)
	out := make([]string, len(lines))
	for i, l := range lines {
		if l == "" {
			out[i] = ""
			continue
		}
		out[i] = pad + l
	}
	return out
}

func tilde(p, home string) string {
	if p == "" {
		return ""
	}
	if home != "" {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
			if rel == "." {
				return "~"
			}
			return "~/" + filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(p)
}

// shortPath keeps the tail of a long path: ~/a/b/c/d -> …/c/d.
func shortPath(p string, n int) string {
	if sw(p) <= n || n < 4 {
		return trunc(p, n)
	}
	parts := strings.Split(p, "/")
	out := parts[len(parts)-1]
	for i := len(parts) - 2; i >= 0; i-- {
		next := parts[i] + "/" + out
		if sw(next)+2 > n {
			break
		}
		out = next
	}
	return trunc("…/"+out, n)
}

func fmtCost(c float64) string {
	switch {
	case c < 0.005:
		return ""
	case c >= 100:
		return fmt.Sprintf("$%.0f", c)
	case c >= 10:
		return fmt.Sprintf("$%.1f", c)
	}
	return fmt.Sprintf("$%.2f", c)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// st builds a foreground style from a theme token.
func st(c lipgloss.AdaptiveColor) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

var (
	sFg     = theme.Text
	sMuted  = theme.Dim
	sFaint  = theme.Ghost
	sAccent = theme.Accented
	sBold   = theme.Bold
	sWarn   = theme.Warning
	sDanger = theme.Error
	sAdd    = st(theme.Add)
	sItalic = theme.Ghost.Italic(true)
)

// ramp is a style blended between two tokens.
func (m *model) ramp(a, b lipgloss.AdaptiveColor, t float64) lipgloss.Style {
	return motion.Ramp(a, b, t, m.dark)
}

// blend returns the blended colour as a style foreground.
func (m *model) blend(a, b lipgloss.AdaptiveColor, t float64) lipgloss.Color {
	return motion.Blend(a, b, t, m.dark)
}

// dimTitle is the colour of finished tool titles: between Muted and Faint.
func (m *model) dimTitle() lipgloss.Style { return m.ramp(theme.Muted, theme.Faint, 0.4) }

// trimTrailing removes padding spaces glamour adds at the end of a line while
// keeping any trailing escape sequences intact.
var trailRe = regexp.MustCompile(`^(.*?)( +)((?:\x1b\[[0-9;]*m)*)$`)

func trimTrailing(l string) string {
	if m := trailRe.FindStringSubmatch(l); m != nil {
		return m[1] + m[3]
	}
	return l
}

func ansiStrip(s string) string { return ansi.Strip(s) }
