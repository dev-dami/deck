package ui

import (
	"image/color"
	"strings"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/x/styledtext"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

var mono = font.Font{Typeface: "Go Mono"}

// maxCodeLines bounds what one code view shapes.
const maxCodeLines = 800

func capLines(s string, n int) (string, int) {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s, 0
	}
	return strings.Join(lines[:n], "\n"), len(lines) - n
}

// highlight tokenises code with chroma (by language name, else by file
// name, else by sniffing) into coloured spans.
func (a *App) highlight(code, lang, filename string, size unit.Sp) []styledtext.SpanStyle {
	var lx chroma.Lexer
	if lang != "" {
		lx = lexers.Get(lang)
	}
	if lx == nil && filename != "" {
		lx = lexers.Match(filename)
	}
	if lx == nil {
		lx = lexers.Analyse(code)
	}
	if lx == nil {
		lx = lexers.Fallback
	}
	lx = chroma.Coalesce(lx)
	st := styles.Get(a.col.Chroma)
	it, err := lx.Tokenise(nil, code)
	if err != nil {
		return []styledtext.SpanStyle{{Font: mono, Size: size, Color: a.col.Fg, Content: code}}
	}
	var spans []styledtext.SpanStyle
	for tok := it(); tok != chroma.EOF; tok = it() {
		e := st.Get(tok.Type)
		c := a.col.Fg
		if e.Colour.IsSet() {
			c = color.NRGBA{R: e.Colour.Red(), G: e.Colour.Green(), B: e.Colour.Blue(), A: 0xff}
		}
		f := mono
		if e.Bold == chroma.Yes {
			f.Weight = font.Bold
		}
		if e.Italic == chroma.Yes {
			f.Style = font.Italic
		}
		spans = append(spans, styledtext.SpanStyle{Font: f, Size: size, Color: c, Content: tok.Value})
	}
	return spans
}

// diffSpans colours a unified diff by line.
func (a *App) diffSpans(diff string, size unit.Sp) []styledtext.SpanStyle {
	var spans []styledtext.SpanStyle
	for _, ln := range strings.SplitAfter(diff, "\n") {
		c := a.col.Fg
		switch {
		case strings.HasPrefix(ln, "+++"), strings.HasPrefix(ln, "---"), strings.HasPrefix(ln, "diff "), strings.HasPrefix(ln, "index "):
			c = a.col.Muted
		case strings.HasPrefix(ln, "+"):
			c = a.col.Add
		case strings.HasPrefix(ln, "-"):
			c = a.col.Del
		case strings.HasPrefix(ln, "@@"):
			c = a.col.Accent
		}
		spans = append(spans, styledtext.SpanStyle{Font: mono, Size: size, Color: c, Content: ln})
	}
	return spans
}

// cachedSpans keeps shaped-input spans per key until the text changes, so
// a frame does not re-tokenise every code block.
func (a *App) cachedSpans(key, text string, build func() []styledtext.SpanStyle) []styledtext.SpanStyle {
	if c, ok := a.codeCache[key]; ok && c.text == text {
		return c.spans
	}
	s := build()
	a.codeCache[key] = codeCache{text: text, spans: s}
	return s
}

type codeCache struct {
	text  string
	spans []styledtext.SpanStyle
}

func (a *App) codeText(gtx C, spans []styledtext.SpanStyle) D {
	// styledtext lays spans out once; copies keep the cache intact.
	cp := make([]styledtext.SpanStyle, len(spans))
	copy(cp, spans)
	return styledtext.Text(a.th.Shaper, cp...).Layout(gtx, nil)
}

// codeView is a code block: a header with the language and Copy, then the
// highlighted code.
func (a *App) codeView(gtx C, key, code, lang, filename string) D {
	code = strings.TrimRight(code, "\n")
	shown, more := capLines(code, maxCodeLines)
	spans := a.cachedSpans(key, shown+"\x00"+lang+filename, func() []styledtext.SpanStyle {
		return a.highlight(shown, lang, filename, a.sp(12.5))
	})
	if a.click(key + "/copy").Clicked(gtx) {
		a.copyText(code)
	}
	return outlined(gtx, a.col.Line, a.col.Code, 8, layout.Inset{}, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return layout.Inset{Top: 4, Left: 12, Right: 4}.Layout(gtx, func(gtx C) D {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, a.line(11, a.col.Muted, strings.ToLower(lang))),
						layout.Rigid(func(gtx C) D { return a.iconBtn(gtx, key+"/copy", icCopy, "Copy", a.col.Muted) }),
					)
				})
			}),
			layout.Rigid(func(gtx C) D {
				return layout.Inset{Left: 12, Right: 12, Bottom: 10}.Layout(gtx, func(gtx C) D { return a.codeText(gtx, spans) })
			}),
			layout.Rigid(func(gtx C) D {
				if more == 0 {
					return D{}
				}
				return layout.Inset{Left: 12, Bottom: 8}.Layout(gtx, a.line(11, a.col.Muted, "… "+itoa(more)+" more lines"))
			}),
		)
	})
}

// diffView is a coloured unified diff.
func (a *App) diffView(gtx C, key, diff string) D {
	shown, more := capLines(strings.TrimRight(diff, "\n"), maxCodeLines)
	spans := a.cachedSpans(key, shown, func() []styledtext.SpanStyle { return a.diffSpans(shown, a.sp(12)) })
	return outlined(gtx, a.col.Line, a.col.Code, 8, layout.UniformInset(10), func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D { return a.codeText(gtx, spans) }),
			layout.Rigid(func(gtx C) D {
				if more == 0 {
					return D{}
				}
				return layout.Inset{Top: 6}.Layout(gtx, a.line(11, a.col.Muted, "… "+itoa(more)+" more lines"))
			}),
		)
	})
}
