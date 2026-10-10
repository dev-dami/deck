package tui

import (
	"strconv"
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
	pastes  []string // the blocks behind paste chips; see Paste
}

// A large paste sits in the composer as one chip, "[pasted #1 · 42 lines]",
// that the cursor steps over and backspace removes whole; the text is sent in
// its place. In the text a chip is one rune from a private-use plane.
const pasteRune = 0xF0000

// Pastes this big become a chip: more lines than this, or this many runes.
const (
	chipLines = 3
	chipRunes = 500
)

func (c *composer) chip(r rune) (int, bool) {
	i := int(r - pasteRune)
	return i, r >= pasteRune && i < len(c.pastes)
}

// label is what a chip shows.
func (c *composer) label(i int) string {
	n := strings.Count(c.pastes[i], "\n") + 1
	size := strconv.Itoa(n) + " lines"
	if n == 1 {
		size = strconv.Itoa(len([]rune(c.pastes[i]))) + " chars"
	}
	return "[pasted #" + strconv.Itoa(i+1) + " · " + size + "]"
}

// cw is the width of a rune as drawn: a chip is as wide as its label.
func (c *composer) cw(r rune) int {
	if i, ok := c.chip(r); ok {
		return ansi.StringWidth(c.label(i))
	}
	return runeW(r)
}

// Paste inserts pasted text, as a chip when it is large.
func (c *composer) Paste(s string) {
	s = normalize(s)
	if strings.Count(s, "\n") < chipLines && len([]rune(s)) < chipRunes {
		c.Insert(s)
		return
	}
	c.pastes = append(c.pastes, s)
	c.text = append(c.text[:c.cur], append([]rune{rune(pasteRune + len(c.pastes) - 1)}, c.text[c.cur:]...)...)
	c.cur++
}

type span struct{ s, e int }

func runeW(r rune) int { return ansi.StringWidth(string(r)) }

// Value is the text with every chip replaced by what was pasted.
func (c *composer) Value() string {
	if len(c.pastes) == 0 {
		return string(c.text)
	}
	var b strings.Builder
	for _, r := range c.text {
		if i, ok := c.chip(r); ok {
			b.WriteString(c.pastes[i])
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
func (c *composer) Empty() bool   { return len(c.text) == 0 }

func (c *composer) SetValue(s string) {
	c.text = []rune(normalize(s))
	c.cur = len(c.text)
	c.vscroll = 0
	c.pastes = nil
}

func (c *composer) Reset() { c.text, c.cur, c.vscroll, c.pastes = nil, 0, 0, nil }

// normalize makes pasted text safe: LF newlines, tabs as spaces, no controls.
func normalize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\t", "    ")
	return strings.Map(func(r rune) rune {
		if r >= pasteRune && r < pasteRune+0x10000 { // chips are made only by Paste
			return -1
		}
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
		out = append(out, c.wrapLine(i, le, avail)...)
		if le >= n {
			break
		}
		i = le + 1
	}
	return out
}

func (c *composer) wrapLine(s, e, avail int) []span {
	t := c.text
	if s == e {
		return []span{{s, s}}
	}
	var out []span
	pos := s
	for pos < e {
		width, j := 0, pos
		for j < e {
			rw := c.cw(t[j])
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
					col += c.cw(r)
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
		rw := c.cw(c.text[pos])
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
			for idx < len(seg) && col+c.cw(seg[idx]) <= ccol {
				col += c.cw(seg[idx])
				idx++
			}
			before := c.draw(seg[:idx])
			cell := " "
			after := ""
			if idx < len(seg) {
				cell = string(seg[idx])
				if i, ok := c.chip(seg[idx]); ok {
					cell = c.label(i)
				}
				after = c.draw(seg[idx+1:])
			}
			line = before + cursor.Render(cell) + after
		} else {
			line = c.draw(seg)
		}
		out = append(out, pre+line)
	}
	return out
}

// draw renders a run of text, chips in the accent colour.
func (c *composer) draw(seg []rune) string {
	var b strings.Builder
	from := 0
	for i, r := range seg {
		if k, ok := c.chip(r); ok {
			b.WriteString(sFg.Render(string(seg[from:i])))
			b.WriteString(sAccent.Render(c.label(k)))
			from = i + 1
		}
	}
	b.WriteString(sFg.Render(string(seg[from:])))
	return b.String()
}
