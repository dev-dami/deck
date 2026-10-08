package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	cstyles "github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	colorful "github.com/lucasb-eyer/go-colorful"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/tui/theme"
)

// bodyCap is the most body lines an expanded row or approval card shows.
const bodyCap = 12

var lexerCache sync.Map // base filename -> chroma.Lexer

func lexerFor(path string) chroma.Lexer {
	name := filepath.Base(path)
	if v, ok := lexerCache.Load(name); ok {
		return v.(chroma.Lexer)
	}
	lx := lexers.Match(name)
	if lx == nil {
		lx = lexers.Fallback
	}
	lx = chroma.Coalesce(lx)
	lexerCache.Store(name, lx)
	return lx
}

func pick(c lipgloss.AdaptiveColor, dark bool) string {
	if dark {
		return c.Dark
	}
	return c.Light
}

// syntaxful reports whether a token type keeps its Chroma colour on a diff
// line; everything else takes the line's tone so +/- lines stay legible.
func syntaxful(t chroma.TokenType) bool {
	switch {
	case t.InCategory(chroma.Keyword), t.InCategory(chroma.LiteralString),
		t.InCategory(chroma.LiteralNumber), t.InCategory(chroma.Comment):
		return true
	}
	switch t {
	case chroma.NameFunction, chroma.NameClass, chroma.NameBuiltin, chroma.NameDecorator,
		chroma.NameTag, chroma.NameConstant, chroma.NameAttribute:
		return true
	}
	return false
}

// highlight returns text coloured by Chroma with the given tone and optional
// background (bg == "" means none).
func highlight(text, path string, tone, bg string, strength float64, dark bool) string {
	base := lipgloss.NewStyle()
	if bg != "" {
		base = base.Background(lipgloss.Color(bg))
	}
	plain := base.Foreground(lipgloss.Color(tone))
	if text == "" {
		return ""
	}
	name := "github"
	if dark {
		name = "github-dark"
	}
	cs := cstyles.Get(name)
	it, err := lexerFor(path).Tokenise(nil, text)
	if err != nil {
		return plain.Render(text)
	}
	toneC, terr := colorful.Hex(tone)
	var b strings.Builder
	for tok := it(); tok != chroma.EOF; tok = it() {
		val := strings.TrimRight(tok.Value, "\n")
		if val == "" {
			continue
		}
		s := plain
		if syntaxful(tok.Type) {
			e := cs.Get(tok.Type)
			if e.Colour.IsSet() {
				col := e.Colour.String()
				if sc, err := colorful.Hex(col); err == nil && terr == nil {
					col = sc.BlendLab(toneC, strength).Clamped().Hex()
				}
				s = base.Foreground(lipgloss.Color(col))
				if e.Bold == chroma.Yes {
					s = s.Bold(true)
				}
				if e.Italic == chroma.Yes {
					s = s.Italic(true)
				}
			}
		}
		b.WriteString(s.Render(val))
	}
	return b.String()
}

// renderDiff renders a unified diff as at most capLines lines (plus a "… N
// more" line) each at most w cells: faint line numbers, Chroma-highlighted
// code, additions on AddBg and deletions on DelBg.
func renderDiff(d *agent.FileDiff, w int, dark bool, capLines int) []string {
	if d == nil || w < 12 {
		return nil
	}
	maxNum := 0
	for _, h := range d.Hunks {
		maxNum = max(maxNum, h.OldStart+h.OldLines, h.NewStart+h.NewLines)
	}
	gw := len(fmt.Sprint(maxNum))
	if gw < 3 {
		gw = 3
	}
	codeW := w - gw - 3
	faint := lipgloss.NewStyle().Foreground(lipgloss.Color(pick(theme.Faint, dark)))

	var all []string
	for _, h := range d.Hunks {
		all = append(all, faint.Render(trunc(fmt.Sprintf("@@ -%d,%d +%d,%d @@", h.OldStart, h.OldLines, h.NewStart, h.NewLines), w)))
		oldN, newN := h.OldStart, h.NewStart
		for _, l := range h.Lines {
			var (
				num      int
				marker   string
				tone, bg string
				strength float64
			)
			switch l.Op {
			case agent.OpAdd:
				num, marker = newN, "+"
				tone, bg, strength = pick(theme.Add, dark), pick(theme.AddBg, dark), 0.45
				newN++
			case agent.OpDel:
				num, marker = oldN, "-"
				tone, bg, strength = pick(theme.Danger, dark), pick(theme.DelBg, dark), 0.45
				oldN++
			default:
				num, marker = newN, " "
				tone, strength = pick(theme.Muted, dark), 0.3
				oldN++
				newN++
			}
			code := strings.ReplaceAll(clean(l.Text), "\n", " ")
			base := lipgloss.NewStyle()
			if bg != "" {
				base = base.Background(lipgloss.Color(bg))
			}
			gut := base.Foreground(lipgloss.Color(pick(theme.Faint, dark))).Render(fmt.Sprintf("%*d ", gw, num))
			mk := base.Foreground(lipgloss.Color(tone)).Render(marker + " ")
			body := highlight(code, d.Path, tone, bg, strength, dark)
			if sw(body) > codeW {
				body = ansi.Truncate(body, codeW, "…")
			}
			line := gut + mk + body
			if bg != "" {
				if pad := w - sw(line); pad > 0 {
					line += base.Render(strings.Repeat(" ", pad))
				}
			}
			all = append(all, line)
		}
	}
	return capLinesWithMore(all, capLines)
}

// renderOutput renders command output: muted, no wrapping, capped.
func renderOutput(text string, w int, capLines int) []string {
	text = strings.TrimRight(clean(text), "\n ")
	if text == "" {
		return nil
	}
	var all []string
	for _, l := range strings.Split(text, "\n") {
		all = append(all, sMuted.Render(trunc(strings.TrimRight(l, " "), w)))
	}
	return capLinesWithMore(all, capLines)
}

func capLinesWithMore(all []string, capLines int) []string {
	if capLines <= 0 || len(all) <= capLines {
		return all
	}
	out := append([]string(nil), all[:capLines]...)
	return append(out, sFaint.Render(fmt.Sprintf("… %d more", len(all)-capLines)))
}

func diffStat(d *agent.FileDiff) string {
	if d == nil {
		return ""
	}
	return sAdd.Render(fmt.Sprintf("+%d", d.Added)) + " " + sDanger.Render(fmt.Sprintf("%s%d", glyphMinus, d.Removed))
}
