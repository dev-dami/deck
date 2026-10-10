package tui

import (
	"fmt"
	"strings"

	"github.com/dev-dami/deck/internal/tui/theme"
)

// The thread's own lines: a relay between agents, the pause marker, a join.
// They are quiet on purpose - the agents' blocks carry the conversation.

// relayCap is how many lines of a relayed message show before it folds.
const relayCap = 4

// relayArrow is the faint arrow between sender and receivers.
const (
	glyphRelay = "↪"
	glyphArrow = "→"
)

// relayQuote wraps text as one quoted paragraph block in w cells: explicit
// lines are kept, empty ones dropped, the quote marks wrap the whole.
func relayQuote(text string, w int) []string {
	var lines []string
	for _, p := range wrapText(`"`+strings.TrimSpace(clean(text))+`"`, w) {
		if strings.TrimSpace(p) != "" {
			lines = append(lines, p)
		}
	}
	return lines
}

// tintedNames renders a list of agent names, each in its tint (or all faint).
func (m *model) tintedNames(ids []string, dim bool) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		if dim {
			parts[i] = sFaint.Render(m.agentName(id))
			continue
		}
		parts[i] = tintStyle(m.kindOf(id)).Render(m.agentName(id))
	}
	return strings.Join(parts, sFaint.Render(", "))
}

// renderRelay draws "↪ claude → codex  "can you review this plan?"" with the
// quote wrapped under itself. Long messages fold to relayCap lines.
func (it *item) renderRelay(c *rctx) []string {
	m := c.m
	tag := strings.Join(append([]string{it.from}, it.rto...), ",") + "|" + m.agentName(it.from) + "|" + strings.Join(m.agentNames(it.rto), ",")
	if it.held {
		tag += "|held"
	}
	if it.expanded {
		tag += "|open"
	}
	key := lineCacheKey{w: c.w, ver: it.ver, dark: m.dark, tag: tag}
	if it.cache.ok && it.cache.key == key {
		return it.cache.lines
	}

	dim := it.held
	head := "  " + sFaint.Render(glyphRelay) + " " + m.tintedNames([]string{it.from}, dim) +
		" " + sFaint.Render(glyphArrow) + " " + m.tintedNames(it.rto, dim)
	if dim {
		head += "  " + sWarn.Render("held")
	}
	hang := sw(head) + 2
	avail := c.tw - hang
	onOwnLine := false
	if avail < 30 { // a narrow pane: the quote gets its own, shallower lines
		hang, avail, onOwnLine = 4, c.tw-4, true
	}
	quote := sFg
	if dim {
		quote = sFaint
	}

	all := relayQuote(it.text, max(8, avail))
	lines, more := all, 0
	it.overflow = len(all) > relayCap
	if it.overflow && !it.expanded {
		more = len(all) - relayCap
		lines = all[:relayCap]
	}

	var out []string
	pad := strings.Repeat(" ", hang)
	if onOwnLine {
		out = append(out, head)
		for i, l := range lines {
			out = append(out, pad+quote.Render(l)+moreTail(more, i == len(lines)-1))
		}
	} else {
		for i, l := range lines {
			if i == 0 {
				out = append(out, head+"  "+quote.Render(l)+moreTail(more, len(lines) == 1))
				continue
			}
			out = append(out, pad+quote.Render(l)+moreTail(more, i == len(lines)-1))
		}
		if len(lines) == 0 {
			out = append(out, head)
		}
	}
	it.cache = lineCache{key: key, lines: out, ok: true}
	return out
}

func moreTail(more int, last bool) string {
	if more == 0 || !last {
		return ""
	}
	return sFaint.Render(fmt.Sprintf(" … +%d", more))
}

// renderPause is the hairline marker that the hop limit paused the thread.
func (it *item) renderPause(c *rctx) []string {
	switch it.pstate {
	case pauseDone:
		return []string{"  " + sFaint.Render("continued")}
	case pauseStale:
		return []string{"  " + sFaint.Render(trunc(fmt.Sprintf("%s paused after %s", glyphPause, plural(it.hops, "agent message", "agent messages")), c.w-2))}
	}
	text := fmt.Sprintf("%s paused after %s %s enter continue %s or type to steer",
		glyphPause, plural(it.hops, "agent message", "agent messages"), glyphDot, glyphDot)
	ls := wrapText(text, c.tw-2)
	out := make([]string, len(ls))
	for i, l := range ls {
		pre := "  "
		if i > 0 {
			pre = "    "
		}
		out[i] = pre + sWarn.Render(l)
	}
	if len(ls) == 1 {
		if room := c.tw - sw(out[0]) - 2; room >= 4 {
			out[0] += " " + c.m.ramp(theme.Rule, theme.Warn, 0.35).Render(strings.Repeat(theme.GlyphRule, room))
		}
	}
	return out
}

// renderJoin is the faint "◆codex joined".
func (it *item) renderJoin(c *rctx) string {
	m := c.m
	kind := m.kindOf(it.joinID)
	tint := m.ramp(theme.Tint(kind), theme.Faint, 0.35)
	return "  " + m.markR(kind, tint) + " " + tint.Render(m.agentName(it.joinID)) + sFaint.Render(" joined")
}

// ---- mention relays ----

// mentionRest parses a reply line that starts with "@name" (or "@a, @b") and
// returns the text after the names; ok is false for any other line.
func mentionRest(line string) (rest string, ok bool) {
	s := strings.TrimSpace(line)
	if !strings.HasPrefix(s, "@") {
		return "", false
	}
	p := 0
	for p < len(s) && s[p] == '@' {
		j := p + 1
		for j < len(s) && (s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z' || s[j] >= '0' && s[j] <= '9' || s[j] == '_' || s[j] == '-') {
			j++
		}
		if j == p+1 || (j < len(s) && !strings.ContainsRune(" \t,:;!?.", rune(s[j]))) {
			break
		}
		p = j
		for p < len(s) && (s[p] == ' ' || s[p] == ',') {
			p++
		}
	}
	if p == 0 {
		return "", false
	}
	return strings.TrimSpace(strings.TrimLeft(s[p:], ":;- ")), true
}

func squash(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }

// stripMentionLines removes from an agent's reply the "@name ..." blocks that
// became the relay `relayText`: a block is its first line plus the lines after
// it up to a blank line or the next mention. Blocks that were not relayed stay.
// Fenced code is left alone.
func stripMentionLines(reply, relayText string) (string, bool) {
	removed := false
	want := squash(relayText)
	lines := strings.Split(strings.ReplaceAll(reply, "\r\n", "\n"), "\n")
	var out []string
	fence := false
	for i := 0; i < len(lines); i++ {
		trim := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			fence = !fence
		}
		rest, ok := mentionRest(lines[i])
		if fence || !ok {
			out = append(out, lines[i])
			continue
		}
		end := i + 1
		block := rest
		for end < len(lines) {
			t := strings.TrimSpace(lines[end])
			if _, isMention := mentionRest(lines[end]); t == "" || isMention || strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
				break
			}
			block += " " + t
			end++
		}
		if b := squash(block); b != "" && strings.Contains(want, b) {
			i = end - 1 // routed: drop the block
			removed = true
			continue
		}
		out = append(out, lines[i])
	}
	// no more than one blank line in a row, none at the ends
	var tidy []string
	for _, l := range out {
		if strings.TrimSpace(l) == "" && (len(tidy) == 0 || tidy[len(tidy)-1] == "") {
			l = ""
			if len(tidy) == 0 {
				continue
			}
		}
		tidy = append(tidy, l)
	}
	for len(tidy) > 0 && tidy[len(tidy)-1] == "" {
		tidy = tidy[:len(tidy)-1]
	}
	return strings.Join(tidy, "\n"), removed
}

// absorbMention removes the routed mention lines from the sender's latest reply
// in the view: the relay line stands for them.
func (v *threadView) absorbMention(from, relayText string) {
	for i := len(v.items) - 1; i >= 0; i-- {
		it := v.items[i]
		if it.kind != kText || it.speaker != from || it.streaming {
			continue
		}
		text, removed := stripMentionLines(it.text, relayText)
		if !removed {
			continue // nothing in this one; an older reply may hold it
		}
		if strings.TrimSpace(text) == "" {
			v.items = append(v.items[:i], v.items[i+1:]...)
			if v.cursor == it {
				v.cursor = nil
			}
		} else {
			it.text = text
			it.bump()
		}
		return
	}
}
