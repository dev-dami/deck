package ui

import (
	"fmt"
	"strings"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/x/markdown"
	"gioui.org/x/richtext"

	"github.com/dev-dami/deck/desktop/platform"
)

// A message is prose and fenced code. Prose goes through gio-x's markdown
// renderer (bold, italics, inline code, lists, headings, links) into
// richtext; code goes through chroma into a code view with Copy.

type segment struct {
	code bool
	lang string
	text string
}

// splitFences cuts text at ``` fences. An unclosed fence runs to the end.
func splitFences(s string) []segment {
	var out []segment
	var prose []string
	flush := func() {
		if t := strings.TrimSpace(strings.Join(prose, "\n")); t != "" {
			out = append(out, segment{text: t})
		}
		prose = nil
	}
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(t, "```") {
			prose = append(prose, lines[i])
			continue
		}
		flush()
		lang := strings.TrimSpace(strings.TrimPrefix(t, "```"))
		var code []string
		for i++; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```"); i++ {
			code = append(code, lines[i])
		}
		out = append(out, segment{code: true, lang: lang, text: strings.Join(code, "\n")})
	}
	flush()
	return out
}

type proseCache struct {
	text  string
	spans []richtext.SpanStyle
}

func (a *App) renderer() *markdown.Renderer {
	if a.md == nil {
		a.md = markdown.NewRenderer()
	}
	a.md.Config = markdown.Config{
		DefaultFont:      font.Font{Typeface: a.th.Face},
		MonospaceFont:    mono,
		DefaultSize:      a.sp(14),
		H1Size:           a.sp(20),
		H2Size:           a.sp(18),
		H3Size:           a.sp(16),
		H4Size:           a.sp(15),
		H5Size:           a.sp(14),
		H6Size:           a.sp(14),
		DefaultColor:     a.col.Fg,
		InteractiveColor: a.col.Accent,
	}
	return a.md
}

func (a *App) proseSpans(key, text string) []richtext.SpanStyle {
	if c, ok := a.proseCache[key]; ok && c.text == text {
		return c.spans
	}
	spans, err := a.renderer().Render([]byte(text))
	if err != nil || len(spans) == 0 {
		spans = []richtext.SpanStyle{{Font: font.Font{Typeface: a.th.Face}, Size: a.sp(14), Color: a.col.Fg, Content: text}}
	}
	a.proseCache[key] = proseCache{text: text, spans: spans}
	return spans
}

func (a *App) richState(key string) *richtext.InteractiveText {
	s := a.rich[key]
	if s == nil {
		s = new(richtext.InteractiveText)
		a.rich[key] = s
	}
	return s
}

// prose draws markdown prose; links open in the browser.
func (a *App) prose(gtx C, key, text string) D {
	st := a.richState(key)
	for {
		span, ev, ok := st.Update(gtx)
		if !ok {
			break
		}
		if ev.Type == richtext.Click {
			if u, ok := span.Get(markdown.MetadataURL).(string); ok {
				a.fail(platform.OpenURL(u))
			}
		}
	}
	spans := a.proseSpans(key, text)
	cp := make([]richtext.SpanStyle, len(spans))
	copy(cp, spans)
	return richtext.Text(st, a.th.Shaper, cp...).Layout(gtx)
}

// markdownView draws a message body.
func (a *App) markdownView(gtx C, key, text string) D {
	segs := splitFences(text)
	kids := make([]layout.FlexChild, 0, len(segs))
	for i, s := range segs {
		k := fmt.Sprintf("%s/%d", key, i)
		kids = append(kids, layout.Rigid(func(gtx C) D {
			return layout.Inset{Bottom: 8}.Layout(gtx, func(gtx C) D {
				if s.code {
					return a.codeView(gtx, k, s.text, s.lang, "")
				}
				return a.prose(gtx, k, s.text)
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
}
