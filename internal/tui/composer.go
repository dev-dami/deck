package tui

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// maxComposerRows is how tall the composer may grow.
const maxComposerRows = 6

// composer is a small multi-line text editor that grows from 1 to 6 rows. It is
// hand-rolled (rather than bubbles/textarea) so the cursor, wrapping and key
// handling are fully under our control and goldens stay stable.
type composer struct {
	text    []rune
	cur     int
	vscroll int
}

type span struct{ s, e int }

func runeW(r rune) int { return ansi.StringWidth(string(r)) }

func (c *composer) Value() string { return string(c.text) }
func (c *composer) Empty() bool   { return len(c.text) == 0 }

func (c *composer) SetValue(s string) {
	c.text = []rune(normalize(s))
	c.cur = len(c.text)
	c.vscroll = 0
}

func (c *composer) Reset() { c.text, c.cur, c.vscroll = nil, 0, 0 }

// normalize makes pasted text safe: LF newlines, tabs as spaces, no controls.
func normalize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\t", "    ")
	return strings.Map(func(r rune) rune {
		if r == '\n' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, s)
}

func (c *composer) Insert(s string) {
	in := []rune(normalize(s))
	if len(in) == 0 {
		return
	}
	out := make([]rune, 0, len(c.text)+len(in))
	out = append(out, c.text[:c.cur]...)
	out = append(out, in...)
	out = append(out, c.text[c.cur:]...)
	c.text = out
	c.cur += len(in)
}

func (c *composer) Backspace() {
	if c.cur == 0 {
		return
	}
	c.text = append(c.text[:c.cur-1], c.text[c.cur:]...)
	c.cur--
}

func (c *composer) Delete() {
	if c.cur >= len(c.text) {
		return
	}
	c.text = append(c.text[:c.cur], c.text[c.cur+1:]...)
}

func (c *composer) Left() {
	if c.cur > 0 {
		c.cur--
	}
}

func (c *composer) Right() {
	if c.cur < len(c.text) {
		c.cur++
	}
}

func (c *composer) lineStart() int {
	i := c.cur
	for i > 0 && c.text[i-1] != '\n' {
		i--
	}
	return i
}

func (c *composer) lineEnd() int {
	i := c.cur
	for i < len(c.text) && c.text[i] != '\n' {
		i++
	}
	return i
}

func (c *composer) Home() { c.cur = c.lineStart() }
func (c *composer) End()  { c.cur = c.lineEnd() }

func (c *composer) wordLeft() int {
	i := c.cur
	for i > 0 && unicode.IsSpace(c.text[i-1]) {
		i--
	}
	for i > 0 && !unicode.IsSpace(c.text[i-1]) {
		i--
	}
	return i
}

func (c *composer) wordRight() int {
	i := c.cur
	for i < len(c.text) && unicode.IsSpace(c.text[i]) {
		i++
	}
	for i < len(c.text) && !unicode.IsSpace(c.text[i]) {
		i++
	}
	return i
}

func (c *composer) WordLeft()  { c.cur = c.wordLeft() }
func (c *composer) WordRight() { c.cur = c.wordRight() }

func (c *composer) DeleteWordBack() {
	i := c.wordLeft()
	c.text = append(c.text[:i], c.text[c.cur:]...)
	c.cur = i
}

func (c *composer) DeleteToStart() {
	i := c.lineStart()
	c.text = append(c.text[:i], c.text[c.cur:]...)
	c.cur = i
}

func (c *composer) DeleteToEnd() {
	end := c.lineEnd()
	c.text = append(c.text[:c.cur], c.text[end:]...)
}

// rows lays the text out in rows of at most avail cells.
func (c *composer) rows(avail int) []span {
	if avail < 1 {
		avail = 1
	}
	var out []span
	i, n := 0, len(c.text)
	for {
		le := i
		for le < n && c.text[le] != '\n' {
			le++
		}
		out = append(out, wrapLine(c.text, i, le, avail)...)
		if le >= n {
			break
		}
		i = le + 1
	}
	return out
}

func wrapLine(t []rune, s, e, avail int) []span {
	if s == e {
		return []span{{s, s}}
	}
	var out []span
	pos := s
	for pos < e {
		width, j := 0, pos
		for j < e {
			rw := runeW(t[j])
			if width+rw > avail && j > pos {
				break
			}
			width += rw
			j++
		}
		if j < e { // wrapped: prefer breaking after a space
			k := j - 1
			for k > pos && t[k] != ' ' {
				k--
			}
			if k > pos && t[k] == ' ' {
				j = k + 1
			}
		}
		out = append(out, span{pos, j})
		pos = j
	}
	return out
}

// cursorAt returns the row index and cell column of the cursor.
func (c *composer) cursorAt(rows []span) (row, col int) {
	n := len(c.text)
	for i, sp := range rows {
		lastOfLine := sp.e == n || c.text[sp.e] == '\n'
		if c.cur < sp.e || (c.cur == sp.e && lastOfLine) {
			if c.cur >= sp.s {
				for _, r := range c.text[sp.s:c.cur] {
					col += runeW(r)
				}
				return i, col
			}
		}
	}
	return len(rows) - 1, 0
}

func textAvail(w int) int {
	a := w - 2 - 1 // prompt, and one cell reserved for the cursor
	if a < 1 {
		a = 1
	}
	return a
}

// Height is how many rows the composer needs at width w (1..6).
func (c *composer) Height(w int) int {
	h := len(c.rows(textAvail(w)))
	if h < 1 {
		h = 1
	}
	if h > maxComposerRows {
		h = maxComposerRows
	}
	return h
}

// MoveVertical moves the cursor one visual row up (-1) or down (+1) and
// reports whether it moved.
func (c *composer) MoveVertical(dir, w int) bool {
	rows := c.rows(textAvail(w))
	row, col := c.cursorAt(rows)
	nr := row + dir
	if nr < 0 || nr >= len(rows) {
		return false
	}
	sp := rows[nr]
	x, pos := 0, sp.s
	for pos < sp.e {
		rw := runeW(c.text[pos])
		if x+rw > col {
			break
		}
		x += rw
		pos++
	}
	c.cur = pos
	return true
}

// View renders the composer into Height(w) rows.
func (c *composer) View(w int, prompt string, placeholder string, focused bool) []string {
	avail := textAvail(w)
	rows := c.rows(avail)
	h := len(rows)
	if h > maxComposerRows {
		h = maxComposerRows
	}
	if h < 1 {
		h = 1
	}
	crow, ccol := c.cursorAt(rows)
	if crow < c.vscroll {
		c.vscroll = crow
	}
	if crow >= c.vscroll+h {
		c.vscroll = crow - h + 1
	}
	if c.vscroll > len(rows)-h {
		c.vscroll = len(rows) - h
	}
	if c.vscroll < 0 {
		c.vscroll = 0
	}
	cursor := lipgloss.NewStyle().Reverse(true)
	out := make([]string, 0, h)
	for i := 0; i < h; i++ {
		ri := c.vscroll + i
		pre := "  "
		if ri == 0 {
			pre = prompt
		}
		if len(c.text) == 0 {
			ph := trunc(placeholder, avail)
			line := pre
			if focused && ph != "" {
				r := []rune(ph)
				line += cursor.Render(string(r[0])) + sFaint.Render(string(r[1:]))
			} else if focused {
				line += cursor.Render(" ")
			} else {
				line += sFaint.Render(ph)
			}
			out = append(out, line)
			continue
		}
		sp := rows[ri]
		seg := c.text[sp.s:sp.e]
		var line string
		if focused && ri == crow {
			col := 0
			idx := 0
			for idx < len(seg) && col+runeW(seg[idx]) <= ccol {
				col += runeW(seg[idx])
				idx++
			}
			before := string(seg[:idx])
			cell := " "
			after := ""
			if idx < len(seg) {
				cell = string(seg[idx])
				after = string(seg[idx+1:])
			}
			line = sFg.Render(before) + cursor.Render(cell) + sFg.Render(after)
		} else {
			line = sFg.Render(string(seg))
		}
		out = append(out, pre+line)
	}
	return out
}
