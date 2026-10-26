package session

import (
	"unicode/utf8"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// The context breakdown: of what a member's CLI session holds, how much it
// started with (the baseline: system prompt, tools, instruction files), how
// much is Deck's own text (instructions, briefings of what others did,
// steers, mode lines), and the rest, the conversation itself. The CLIs report
// none of this, so Deck estimates it from what it sends, at about four
// characters a token (good to about a third either way).

// estTokens is the rough token count of n characters.
func estTokens(n int) int { return (max(0, n) + charsPerToken - 1) / charsPerToken }

// beginContext starts counting for the CLI session spec starts: a new one
// (no ResumeID) begins with only the instructions and a baseline to
// measure; a resumed one keeps what it had, plus the instructions when the
// adapter sends them into the conversation again.
func (a *agentState) beginContext(spec agent.Spec, caps agent.ContextCaps) {
	instr := estTokens(utf8.RuneCountInString(spec.Instructions))
	a.mu.Lock()
	defer a.mu.Unlock()
	if spec.ResumeID == "" {
		// The prompt being delivered, if this start is for it, is the new
		// session's first.
		a.ctxBaseline, a.ctxFresh = 0, true
		a.deckTokens, a.sentTokens = instr+a.inflight[0], instr+a.inflight[1]
		return
	}
	a.ctxFresh, a.sentTokens = false, 0
	if caps.InstructionSlot == agent.InstrFirstPrompt && !spec.Briefed {
		a.deckTokens += instr
	}
}

// notePrompt counts a prompt Deck is about to deliver: all of it toward the
// baseline estimate, and what is not the user's own words (user characters)
// as Deck's. It is counted before it is sent, since the CLI may report the
// context it fills before the send returns; donePrompt settles it.
func (a *agentState) notePrompt(prompt string, user int) {
	n := utf8.RuneCountInString(prompt)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.inflight = [2]int{estTokens(n - user), estTokens(n)}
	a.deckTokens += a.inflight[0]
	if a.ctxFresh {
		a.sentTokens += a.inflight[1]
	}
}

// donePrompt settles the prompt notePrompt counted: it is taken back when it
// could not be delivered (ok false).
func (a *agentState) donePrompt(ok bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !ok {
		a.deckTokens = max(0, a.deckTokens-a.inflight[0])
		if a.ctxFresh {
			a.sentTokens = max(0, a.sentTokens-a.inflight[1])
		}
	}
	a.inflight = [2]int{}
}

// measureBaselineLocked takes the baseline from the first context figure of
// a session Deck started: the fill less what Deck has sent it so far. A CLI
// that reports its own (Usage.BaselineTokens) is taken at its word. a.mu is
// held.
func (a *agentState) measureBaselineLocked(u *agent.Usage) {
	switch {
	case u.BaselineTokens > 0:
		a.ctxBaseline, a.ctxFresh = u.BaselineTokens, false
	case a.ctxFresh && a.ctxBaseline == 0:
		a.ctxBaseline, a.ctxFresh = max(0, u.ContextTokens-a.sentTokens), false
	}
}
