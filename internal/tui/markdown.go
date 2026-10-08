package tui

import (
	"regexp"
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"

	"github.com/dev-dami/deck/internal/tui/theme"
)

// mdCache renders final assistant text. Prose goes through glamour (one
// renderer per width); fenced code blocks are drawn by Deck itself, with the
// same Chroma highlighter the diffs use and a faint gutter instead of a
// background block.
type mdCache struct {
	mu   sync.Mutex
	dark bool
	r    map[int]*glamour.TermRenderer
}

func newMD(dark bool) *mdCache { return &mdCache{dark: dark, r: map[int]*glamour.TermRenderer{}} }

func strp(s string) *string { return &s }
func boolp(b bool) *bool    { return &b }

// mdStyle derives glamour's dark or light style and bends it to Deck's tokens:
// no document margin, bold Fg headings without "#" prefixes or blocks, inline
// code in Accent with no padding or background, muted "•" bullets.
func mdStyle(dark bool) ansi.StyleConfig {
	cfg := styles.LightStyleConfig
	if dark {
		cfg = styles.DarkStyleConfig
	}
	zero := uint(0)
	fg := pick(theme.Fg, dark)

	cfg.Document.Margin = &zero
	cfg.Document.BlockPrefix = ""
	cfg.Document.BlockSuffix = ""
	cfg.Document.Color = strp(fg)

	cfg.Heading.BlockSuffix = ""
	cfg.Heading.Color = strp(fg)
	cfg.Heading.Bold = boolp(true)
	for _, h := range []*ansi.StyleBlock{&cfg.H1, &cfg.H2, &cfg.H3, &cfg.H4, &cfg.H5, &cfg.H6} {
		h.Prefix, h.Suffix, h.BlockPrefix, h.BlockSuffix = "", "", "", ""
		h.BackgroundColor = nil
		h.Color = strp(fg)
		h.Bold = boolp(true)
		h.Margin = &zero
	}

	cfg.Code = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{
		Color: strp(pick(theme.Accent, dark)),
	}}
	cfg.CodeBlock.Margin = &zero

	// The bullet carries its own colour so list text keeps the document colour.
	bullet := lipgloss.NewStyle().Foreground(lipgloss.Color(pick(theme.Muted, dark))).Render("• ")
	cfg.Item = ansi.StylePrimitive{BlockPrefix: bullet}
	return cfg
}

func (c *mdCache) renderer(width int) *glamour.TermRenderer {
	if r, ok := c.r[width]; ok {
		return r
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(mdStyle(c.dark)),
		glamour.WithWordWrap(width),
		glamour.WithColorProfile(lipgloss.ColorProfile()),
	)
	if err != nil {
		c.r[width] = nil
		return nil
	}
	c.r[width] = r
	return r
}

type mdSeg struct {
	code bool
	lang string
	text string
}

var fenceRe = regexp.MustCompile("^[ 	]*(```+|~~~+)[ 	]*([A-Za-z0-9_+#.-]*)")

// splitFences separates fenced code blocks from prose. An unclosed fence runs
// to the end of the text.
func splitFences(text string) []mdSeg {
	var segs []mdSeg
	var cur []string
	flush := func(code bool, lang string) {
		if len(cur) > 0 || code {
			segs = append(segs, mdSeg{code: code, lang: lang, text: strings.Join(cur, "\n")})
		}
		cur = nil
	}
	inCode, marker, lang := false, "", ""
	for _, l := range strings.Split(text, "\n") {
		m := fenceRe.FindStringSubmatch(l)
		switch {
		case !inCode && m != nil:
			flush(false, "")
			inCode, marker, lang = true, m[1][:3], m[2]
		case inCode && m != nil && strings.HasPrefix(m[1], marker) && m[2] == "":
			flush(true, lang)
			inCode = false
		default:
			cur = append(cur, l)
		}
	}
	flush(inCode, lang)
	return segs
}

// lines renders markdown to styled lines of at most width cells. It returns nil
// if rendering fails, and the caller falls back to plain text.
func (c *mdCache) lines(text string, width int) []string {
	if width < 8 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, seg := range splitFences(clean(text)) {
		var ls []string
		if seg.code {
			ls = c.codeBlock(seg, width)
		} else {
			if strings.TrimSpace(seg.text) == "" {
				continue
			}
			var ok bool
			if ls, ok = c.prose(seg.text, width); !ok {
				return nil
			}
		}
		if len(ls) == 0 {
			continue
		}
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, ls...)
	}
	return out
}

func (c *mdCache) prose(text string, width int) ([]string, bool) {
	r := c.renderer(width)
	if r == nil {
		return nil, false
	}
	out, err := r.Render(text)
	if err != nil {
		return nil, false
	}
	ls := strings.Split(strings.Trim(out, "\n"), "\n")
	for i, l := range ls {
		l = trimTrailing(l)
		if strings.TrimSpace(ansiStrip(l)) == "" {
			l = ""
		}
		ls[i] = l
	}
	// glamour separates blocks with one blank line; never keep two
	var dedup []string
	for _, l := range ls {
		if l == "" && len(dedup) > 0 && dedup[len(dedup)-1] == "" {
			continue
		}
		dedup = append(dedup, l)
	}
	for len(dedup) > 0 && dedup[0] == "" {
		dedup = dedup[1:]
	}
	for len(dedup) > 0 && dedup[len(dedup)-1] == "" {
		dedup = dedup[:len(dedup)-1]
	}
	return dedup, true
}

// codeBlock draws a fenced block: Chroma colours, a faint "│" gutter, no background.
func (c *mdCache) codeBlock(seg mdSeg, width int) []string {
	body := strings.TrimRight(seg.text, "\n")
	if strings.TrimSpace(body) == "" {
		return nil
	}
	lx := lexerByLang(seg.lang)
	tone := pick(theme.Fg, c.dark)
	gutter := lipgloss.NewStyle().Foreground(lipgloss.Color(pick(theme.Faint, c.dark))).Render(theme.GlyphBar + " ")
	var out []string
	for _, l := range strings.Split(body, "\n") {
		l = strings.TrimRight(l, " ")
		h := highlight(l, lx, tone, "", 0.2, c.dark)
		if sw(h) > width-2 {
			h = trunc(h, width-2)
		}
		out = append(out, gutter+h)
	}
	return out
}

// warm pays glamour's one-time setup cost off the UI goroutine.
func (c *mdCache) warm() {
	c.lines("warm `x`\n\n```go\nx := 1\n```\n\n- a\n", 40)
}
