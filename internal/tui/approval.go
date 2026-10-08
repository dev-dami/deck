package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/tui/motion"
	"github.com/dev-dami/deck/internal/tui/theme"
)

// badgePulses is how long a new approval's sidebar badge pulses before it holds steady.
const badgePulses = 1200 * time.Millisecond

// pendingApproval is a permission request waiting for the user.
type pendingApproval struct {
	req    agent.ApprovalRequest
	thread string
	at     time.Time
	slide  motion.Spring // lines the card is still sliding up by (2 -> 0)
	fade   motion.Fade   // the warning glyph fading up
}

func newPending(req agent.ApprovalRequest, thread string, now time.Time) *pendingApproval {
	p := &pendingApproval{
		req:    req,
		thread: thread,
		at:     now,
		slide:  motion.Snappy.New(60),
		fade:   motion.NewFade(350*time.Millisecond, motion.EaseOutCubic),
	}
	p.slide.Pos = 2
	p.slide.Set(0)
	p.fade.Start(now)
	return p
}

func (p *pendingApproval) moving(now time.Time) bool {
	return p.slide.Moving() || p.fade.Moving(now)
}

// badgePulsing reports whether the sidebar badge is still in its two pulses.
func (p *pendingApproval) badgePulsing(now time.Time) bool {
	return !motion.Reduced() && now.Sub(p.at) < badgePulses
}

// badgeLevel is 0..1: two pulses after arrival, then steady at 1.
func (p *pendingApproval) badgeLevel(now time.Time) float64 {
	age := now.Sub(p.at)
	if motion.Reduced() || age >= badgePulses {
		return 1
	}
	return motion.Pulse(time.Unix(0, int64(age)), badgePulses/2)
}

func approvalVerb(r agent.ApprovalRequest) string {
	switch r.Kind {
	case agent.ApproveCommand:
		return "wants to run"
	case agent.ApproveEdit:
		return "wants to edit"
	}
	if r.ToolName != "" {
		return "wants to use " + displayName(r.ToolName)
	}
	return "needs approval"
}

// approvalLines are the body lines of a card (without the "│ " gutter), at
// most w cells wide.
func (m *model) approvalBody(r agent.ApprovalRequest, w int) []string {
	var body []string
	switch {
	case r.Kind == agent.ApproveEdit && r.Diff != nil:
		body = renderDiff(r.Diff, w, m.dark, bodyCap)
	case r.Kind == agent.ApproveEdit:
		// no diff available: the header already names the files
	default:
		ls := wrapText(r.Summary, w)
		if len(ls) > 4 {
			ls = append(ls[:3], "…")
		}
		for _, l := range ls {
			body = append(body, sFg.Render(l))
		}
	}
	if r.Cwd != "" && r.Kind != agent.ApproveEdit {
		body = append(body, sFaint.Render(trunc("in "+tilde(r.Cwd, m.home), w)))
	}
	return body
}

// renderApproval draws the card. total > 1 adds "1 of N" and a trailing hint.
func (m *model) renderApproval(p *pendingApproval, w int, withSlide bool, total int) []string {
	r := p.req
	gutter := sFaint.Render(theme.GlyphBar + " ")
	bare := sFaint.Render(theme.GlyphBar)

	glyph := m.ramp(theme.Faint, theme.Warn, p.fade.Progress(m.now)).Render(glyphWarn)
	verb := approvalVerb(r)
	head := glyph + " " + tintStyle(m.kindOf(r.AgentID)).Bold(true).Render(m.agentName(r.AgentID)) + sWarn.Render(" "+verb)
	if r.Kind == agent.ApproveEdit && r.Summary != "" {
		head += " " + sWarn.Render(trunc(oneLine(r.Summary), max(10, w-sw(head)-14)))
	}
	right := ""
	if r.Diff != nil && r.Kind == agent.ApproveEdit {
		right = diffStat(r.Diff)
	}
	if total > 1 {
		if right != "" {
			right += "  "
		}
		right += sFaint.Render(fmt.Sprintf("1 of %d", total))
	}
	if right != "" {
		head = rightAlign(head, right, w)
	} else {
		head = trunc(head, w)
	}

	out := []string{head}
	for _, l := range m.approvalBody(r, w-2) {
		out = append(out, gutter+l)
	}
	out = append(out, bare)
	keys := []string{"y allow", "n deny", "a always", "e edit"}
	var hint []string
	for _, k := range keys {
		hint = append(hint, sAccent.Render(k[:1])+sMuted.Render(k[1:]))
	}
	out = append(out, gutter+strings.Join(hint, "   "))
	if withSlide {
		n := int(p.slide.Pos + 0.5)
		if n > 0 && n < len(out) {
			out = out[:len(out)-n]
		}
	}
	return out
}
