package tui

import (
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"

	"github.com/dev-dami/deck/internal/tui/theme"
)

// mdCache renders final assistant text with glamour, one renderer per width.
type mdCache struct {
	mu   sync.Mutex
	dark bool
	r    map[int]*glamour.TermRenderer
}

func newMD(dark bool) *mdCache { return &mdCache{dark: dark, r: map[int]*glamour.TermRenderer{}} }

// mdStyle is glamour's stock style with Deck's tokens: no margins or heading
// blocks, headings in Accent.
func mdStyle(dark bool) ansi.StyleConfig {
	cfg := styles.LightStyleConfig
	if dark {
		cfg = styles.DarkStyleConfig
	}
	zero := uint(0)
	cfg.Document.Margin = &zero
	cfg.Document.BlockPrefix = ""
	cfg.Document.BlockSuffix = ""
	cfg.CodeBlock.Margin = &zero
	accent := pick(theme.Accent, dark)
	bold := true
	for _, h := range []*ansi.StyleBlock{&cfg.H1, &cfg.H2, &cfg.H3, &cfg.H4, &cfg.H5, &cfg.H6} {
		h.BackgroundColor = nil
		h.Color = &accent
		h.Bold = &bold
	}
	cfg.H1.Prefix, cfg.H1.Suffix = "", ""
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

// lines renders markdown to styled lines of at most width cells. It returns nil
// if rendering fails, and the caller falls back to plain text.
func (c *mdCache) lines(text string, width int) []string {
	if width < 8 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.renderer(width)
	if r == nil {
		return nil
	}
	out, err := r.Render(clean(text))
	if err != nil {
		return nil
	}
	out = strings.Trim(out, "\n")
	ls := strings.Split(out, "\n")
	for i, l := range ls {
		ls[i] = trimTrailing(l)
	}
	// glamour pads blank lines with escape codes only; collapse them.
	for i, l := range ls {
		if strings.TrimSpace(ansiStrip(l)) == "" {
			ls[i] = ""
		}
	}
	return ls
}

// warm pays glamour's one-time setup cost off the UI goroutine.
func (c *mdCache) warm() {
	c.lines("warm `x`\n\n```go\nx := 1\n```\n\n- a\n", 40)
}
