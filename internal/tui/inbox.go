package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/store"
	"github.com/dev-dami/deck/internal/tui/motion"
	"github.com/dev-dami/deck/internal/tui/theme"
)

type ovKind int

const (
	ovNone ovKind = iota
	ovInbox
	ovResume
	ovChanges
	ovKeys // the ? list of keys
)

// overlay is a list that rises from the composer: the approval inbox or the
// thread picker. It is drawn over the bottom of the transcript.
type overlay struct {
	kind    ovKind
	open    bool
	sel     int
	h       motion.Spring
	threads []store.Thread
}

func (o *overlay) visible() bool { return o.kind != ovNone && (o.open || o.h.Pos > 0.5) }

func (m *model) hasOverlay() bool { return m.ov.open }

func (m *model) openOverlay(k ovKind) {
	m.ov.kind, m.ov.open, m.ov.sel = k, true, 0
	if k == ovResume {
		m.ov.threads = m.mgr.Saved()
	}
	m.menu.kind = 0
	m.menu.h.Set(0)
}

func (m *model) closeOverlay() {
	m.ov.open = false
	m.ov.h.Set(0)
}

func (m *model) overlayCount() int {
	switch m.ov.kind {
	case ovInbox:
		return len(m.pending)
	case ovResume:
		return len(m.ov.threads)
	}
	return 0
}

func (m *model) moveOverlay(d int) {
	n := m.overlayCount()
	if n == 0 {
		return
	}
	m.ov.sel = max(0, min(n-1, m.ov.sel+d))
}

func (m *model) clampOverlay() {
	n := m.overlayCount()
	if m.ov.sel >= n {
		m.ov.sel = max(0, n-1)
	}
}

func ruleTitle(title string, w int) string {
	left := sFaint.Render(theme.GlyphRule+theme.GlyphRule+theme.GlyphRule) + " " + sFaint.Render(title) + " "
	n := w - sw(left)
	if n < 0 {
		n = 0
	}
	return " " + left + st(theme.Rule).Render(strings.Repeat(theme.GlyphRule, n))
}

// overlayFull is the whole overlay content; only its bottom rows are revealed
// while it rises.
func (m *model) overlayFull(w, maxRows int) []string {
	switch m.ov.kind {
	case ovInbox:
		return m.inboxLines(w, maxRows)
	case ovResume:
		return m.resumeLines(w, maxRows)
	case ovChanges:
		return m.changesLines(w, maxRows)
	case ovKeys:
		return m.keysLines(w, maxRows)
	}
	return nil
}

func (m *model) overlayTargetHeight(w, maxRows int) int {
	return len(m.overlayFull(w, maxRows))
}

// overlayLines returns the revealed rows.
func (m *model) overlayLines(w, maxRows int) []string {
	if !m.ov.visible() {
		return nil
	}
	full := m.overlayFull(w, maxRows)
	n := int(math.Round(m.ov.h.Pos))
	n = max(1, min(n, len(full)))
	if !m.ov.open && n < 1 {
		return nil
	}
	return full[len(full)-n:]
}

func window(sel, total, rows int) (start, end int) {
	if total <= rows {
		return 0, total
	}
	start = max(0, min(sel-rows/2, total-rows))
	return start, start + rows
}

func (m *model) inboxLines(w, maxRows int) []string {
	out := []string{ruleTitle("inbox", w-1)}
	rows := max(1, maxRows-3)
	s, e := window(m.ov.sel, len(m.pending), rows)
	for i := s; i < e; i++ {
		r := m.pending[i].req
		bar := " "
		name := tintStyle(m.kindOf(r.AgentID))
		if i == m.ov.sel {
			bar = sAccent.Render(glyphBarSel)
			name = tintStyle(m.kindOf(r.AgentID)).Bold(true)
		}
		what := r.Summary
		verb := "run"
		right := ""
		switch r.Kind {
		case agent.ApproveEdit:
			verb = "edit"
			if r.Diff != nil {
				right = diffStat(r.Diff)
			}
		case agent.ApproveOther:
			verb = displayName(r.ToolName)
		}
		if right == "" && r.Cwd != "" {
			right = sFaint.Render(tilde(r.Cwd, m.home))
		}
		left := " " + bar + sWarn.Render(glyphWarn) + " " + name.Render(padTo(trunc(m.agentName(r.AgentID), 10), 8)) + " " +
			sFaint.Render(padTo(verb, 4)) + " " + sFg.Render(oneLine(what))
		out = append(out, rightAlign(left, right, w))
	}
	out = append(out, "", " "+hintFit(w-2, "y allow", "n deny", "a always", "enter open", "esc close"))
	return out
}

func (m *model) resumeLines(w, maxRows int) []string {
	out := []string{ruleTitle("resume", w-1)}
	if len(m.ov.threads) == 0 {
		out = append(out, " "+sFaint.Render("  no saved threads yet"))
	}
	rows := max(1, maxRows-3)
	s, e := window(m.ov.sel, len(m.ov.threads), rows)
	for i := s; i < e; i++ {
		t := m.ov.threads[i]
		bar := " "
		name := sMuted
		if i == m.ov.sel {
			bar = sAccent.Render(glyphBarSel)
			name = sBold
		}
		right := sFaint.Render(ago(t.Updated, m.now))
		if c := fmtCost(t.CostUSD); c != "" {
			right += sFaint.Render("  " + c)
		}
		nm := t.Name
		if nm == "" {
			nm = t.AgentKind
		}
		left := " " + bar + " " + name.Render(padTo(trunc(nm, 12), 12)) + " " + sFg.Render(oneLine(t.Title))
		out = append(out, rightAlign(left, right, w))
	}
	out = append(out, "", " "+hintFit(w-2, "enter resume", "esc close"))
	return out
}

// hintFit shows as many hints as fit in w cells.
func hintFit(w int, parts ...string) string {
	for n := len(parts); n > 1; n-- {
		if s := hintLine(parts[:n]...); sw(s) <= w {
			return s
		}
	}
	return trunc(hintLine(parts[:1]...), w)
}

func hintLine(parts ...string) string {
	var hs []string
	for _, p := range parts {
		k, rest, _ := strings.Cut(p, " ")
		hs = append(hs, sMuted.Render(k)+" "+sFaint.Render(rest))
	}
	return strings.Join(hs, "   ")
}

func ago(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case t.IsZero():
		return ""
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
