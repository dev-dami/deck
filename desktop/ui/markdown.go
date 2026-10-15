package ui

import (
	"fmt"
	"io"
	"strings"

	"gioui.org/font"
	"gioui.org/io/clipboard"
	"gioui.org/layout"
	"gioui.org/widget/material"
)

type blockKind int

const (
	bPara blockKind = iota
	bHeading
	bBullet
	bCode
	bQuote
)

type block struct {
	kind  blockKind
	text  string
	level int // heading level, or bullet indent
	lang  string
}

// parseMarkdown splits text into the blocks agents write: paragraphs,
// headings, bullets, quotes and fenced code. Inline marks are dropped.
func parseMarkdown(s string) []block {
	var out []block
	var para []string
	flush := func() {
		if len(para) > 0 {
			out = append(out, block{kind: bPara, text: inline(strings.Join(para, " "))})
			para = nil
		}
	}
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		t := strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(t, "```"):
			flush()
			lang := strings.TrimSpace(strings.TrimPrefix(t, "```"))
			var code []string
			for i++; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```"); i++ {
				code = append(code, lines[i])
			}
			out = append(out, block{kind: bCode, text: strings.Join(code, "\n"), lang: lang})
		case t == "":
			flush()
		case strings.HasPrefix(t, "#"):
			flush()
			lvl := len(t) - len(strings.TrimLeft(t, "#"))
			out = append(out, block{kind: bHeading, level: lvl, text: inline(strings.TrimSpace(t[lvl:]))})
		case strings.HasPrefix(t, "- "), strings.HasPrefix(t, "* "), strings.HasPrefix(t, "+ "):
			flush()
			indent := (len(ln) - len(strings.TrimLeft(ln, " \t"))) / 2
			out = append(out, block{kind: bBullet, level: indent, text: inline(t[2:])})
		case numbered(t) > 0:
			flush()
			n := numbered(t)
			out = append(out, block{kind: bBullet, level: -1, text: t[:n] + " " + inline(strings.TrimSpace(t[n:]))})
		case strings.HasPrefix(t, ">"):
			flush()
			out = append(out, block{kind: bQuote, text: inline(strings.TrimSpace(strings.TrimPrefix(t, ">")))})
		default:
			para = append(para, t)
		}
	}
	flush()
	return out
}

// numbered is the length of a "12." list marker at the start of t, or 0.
func numbered(t string) int {
	i := 0
	for i < len(t) && t[i] >= '0' && t[i] <= '9' {
		i++
	}
	if i > 0 && i < len(t)-1 && (t[i] == '.' || t[i] == ')') && t[i+1] == ' ' {
		return i + 1
	}
	return 0
}

func inline(s string) string {
	s = strings.NewReplacer("**", "", "__", "").Replace(s)
	// A line wrapped whole in _…_ or *…* is emphasis, not text.
	for _, m := range []string{"_", "*"} {
		if len(s) > 2 && strings.HasPrefix(s, m) && strings.HasSuffix(s, m) {
			s = s[1 : len(s)-1]
		}
	}
	return s
}

// layoutMarkdown draws text as blocks; key keeps the selection state of
// each block between frames.
func (a *App) layoutMarkdown(gtx C, key, text string) D {
	blocks := parseMarkdown(text)
	kids := make([]layout.FlexChild, 0, len(blocks))
	for i, b := range blocks {
		bk := fmt.Sprintf("%s/%d", key, i)
		kids = append(kids, layout.Rigid(func(gtx C) D {
			return layout.Inset{Bottom: 6}.Layout(gtx, func(gtx C) D { return a.layoutBlock(gtx, bk, b) })
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
}

func (a *App) selectable(key, text string, size float32) material.LabelStyle {
	l := a.label(sp(size), a.col.Fg, text)
	l.State = a.sel(key, text)
	return l
}

func (a *App) layoutBlock(gtx C, key string, b block) D {
	switch b.kind {
	case bHeading:
		size := float32(17)
		if b.level >= 3 {
			size = 15
		}
		l := a.selectable(key, b.text, size)
		l.Font.Weight = font.Bold
		return layout.Inset{Top: 4}.Layout(gtx, l.Layout)
	case bBullet:
		mark := "•"
		if b.level < 0 {
			mark = ""
		}
		left := 4 + 14*max(b.level, 0)
		return layout.Inset{Left: unitDp(left)}.Layout(gtx, func(gtx C) D {
			return layout.Flex{}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					if mark == "" {
						return D{}
					}
					return layout.Inset{Right: 8}.Layout(gtx, a.label(sp(14), a.col.Muted, mark).Layout)
				}),
				layout.Flexed(1, a.selectable(key, b.text, 14).Layout),
			)
		})
	case bQuote:
		l := a.selectable(key, "▍ "+b.text, 14)
		l.Color = a.col.Muted
		return layout.Inset{Left: 4}.Layout(gtx, l.Layout)
	case bCode:
		copyBtn := a.click(key + "/copy")
		if copyBtn.Clicked(gtx) {
			gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(b.text))})
			a.setStatus("✓ Copied to the clipboard")
		}
		return card(gtx, a.col.Code, 10, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					return layout.Flex{}.Layout(gtx,
						layout.Flexed(1, a.label(sp(11), a.col.Muted, b.lang).Layout),
						layout.Rigid(func(gtx C) D {
							return material.Clickable(gtx, copyBtn, a.label(sp(11), a.col.Accent, "Copy").Layout)
						}),
					)
				}),
				layout.Rigid(func(gtx C) D {
					l := a.selectable(key, b.text, 13)
					l.Font.Typeface = "Go Mono"
					return layout.Inset{Top: 4}.Layout(gtx, l.Layout)
				}),
			)
		})
	}
	return a.selectable(key, b.text, 14).Layout(gtx)
}
