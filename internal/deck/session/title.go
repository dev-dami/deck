package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/store"
)

// Thread titles. The first prompt, tidied, stands in at once (Post). When the
// first turn ends, the default agent's model is asked, in a one-shot run that
// is not a thread member and leaves nothing in the log, for a short title that
// replaces it. A title the user typed is never replaced.

const (
	titleTimeout   = 20 * time.Second
	titlePromptMax = 1000 // runes of the first prompt shown to the model
	titleReplyMax  = 600  // runes of the first reply shown to the model

	titleInstructions = "You name conversations. Reply with only a title of 3 to 6 words " +
		"that says what the conversation is about. No quotes, no trailing punctuation, " +
		"no preamble. Do not use any tools."
)

// maybeAutoTitle starts the model-written title for t once, after its first
// finished turn. reply is that turn's final text.
func (m *Manager) maybeAutoTitle(t *thread, reply string) {
	if !m.config().AutoTitle {
		return
	}
	t.mu.Lock()
	if t.titleSrc != "" || t.titling {
		t.mu.Unlock()
		return
	}
	prompt := firstUserText(t)
	if prompt == "" {
		t.mu.Unlock()
		return
	}
	t.titling = true
	t.mu.Unlock()

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		title := m.generateTitle(prompt, reply)
		t.mu.Lock()
		t.titling = false
		t.mu.Unlock()
		if title == "" {
			return
		}
		m.applyAutoTitle(t, title)
	}()
}

// applyAutoTitle sets title unless the user renamed the thread (or an earlier
// automatic title won) while the model was thinking.
func (m *Manager) applyAutoTitle(t *thread, title string) {
	t.mu.Lock()
	if t.titleSrc != "" || t.gone.Load() {
		t.mu.Unlock()
		return
	}
	t.title, t.titleSrc = title, titleAuto
	t.mu.Unlock()
	m.persistMeta(t)
	m.emit(ThreadChangedMsg{ThreadID: t.id})
}

// Title sources (store.Thread.TitleSrc).
const (
	titleAuto = "auto"
	titleUser = "user"
)

// firstUserText is the text of the thread's first user message. t.mu is held.
func firstUserText(t *thread) string {
	for _, e := range t.log {
		if e.Type == store.EntryUser && strings.TrimSpace(e.Text) != "" {
			return e.Text
		}
	}
	return ""
}

// generateTitle asks the default agent's model for a title. It returns "" on
// any failure, so the caller keeps the placeholder.
func (m *Manager) generateTitle(prompt, reply string) string {
	kind := m.defaultKind()
	ad := m.adapters[kind]
	if ad == nil {
		return ""
	}
	cfg := m.config()
	dir := filepath.Join(os.TempDir(), "deck-title")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	spec := agent.Spec{
		ID: "title", Dir: dir, Model: m.defaultModel(cfg, kind), Mode: agent.ModePlan,
		Instructions: titleInstructions,
	}
	if b := cfg.Binary(kind); b != kind {
		spec.Binary = b
	}

	ctx, cancel := context.WithTimeout(m.ctx, titleTimeout)
	defer cancel()
	run, err := ad.Start(ctx, spec)
	if err != nil {
		return ""
	}
	defer run.Close()
	if err := run.Send(titleAsk(prompt, reply)); err != nil {
		return ""
	}

	var text strings.Builder
	for {
		select {
		case <-ctx.Done():
			return ""
		case req := <-run.Approvals():
			if req.Respond != nil {
				req.Respond(agent.Decision{Reason: "titles need no tools"})
			}
		case ev, ok := <-run.Events():
			if !ok {
				return cleanTitle(text.String())
			}
			switch ev.Kind {
			case agent.KindAssistantText:
				if ev.Delta {
					text.WriteString(ev.Text)
				} else {
					text.Reset()
					text.WriteString(ev.Text)
				}
			case agent.KindTurnDone:
				return cleanTitle(text.String())
			case agent.KindError, agent.KindExited:
				return cleanTitle(text.String())
			}
		}
	}
}

// titleAsk is the one message the title run is given.
func titleAsk(prompt, reply string) string {
	var b strings.Builder
	b.WriteString("Conversation to title.\n\nUser:\n")
	b.WriteString(clipRunes(prompt, titlePromptMax))
	if reply = strings.TrimSpace(reply); reply != "" {
		b.WriteString("\n\nAssistant:\n")
		b.WriteString(clipRunes(reply, titleReplyMax))
	}
	b.WriteString("\n\nTitle (3-6 words):")
	return b.String()
}

func clipRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n])
}

// cleanTitle reduces a model reply to a one-line title: the first non-empty
// line, without a "Title:" label, quotes, markdown or trailing punctuation. It
// returns "" when nothing usable is left.
func cleanTitle(s string) string {
	line := ""
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "```") {
			continue
		}
		line = l
		break
	}
	if i := strings.IndexByte(line, ':'); i >= 0 && i < 8 && strings.EqualFold(strings.TrimSpace(line[:i]), "title") {
		line = line[i+1:]
	}
	line = strings.Trim(line, " \t\"'`*_#“”‘’")
	line = strings.TrimRightFunc(line, func(r rune) bool { return unicode.IsPunct(r) && r != ')' && r != ']' })
	line = oneLine(line)
	if line == "" || len(strings.Fields(line)) > 12 {
		return "" // not a title: the model rambled
	}
	return truncateRunes(line, 60)
}

// truncateRunes clips s to n runes, ending in an ellipsis when it was cut.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// placeholderTitle is the title a thread gets from its first prompt: one line,
// without code fences, leading mentions or filler such as "please" or "can you".
func placeholderTitle(text string) string {
	var kept []string
	inFence := false
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			inFence = !inFence
			continue
		}
		if !inFence {
			kept = append(kept, l)
		}
	}
	s := oneLine(strings.Join(kept, " "))
	if s == "" {
		s = oneLine(text) // all code: better than nothing
	}
	words := strings.Fields(s)
	for len(words) > 1 && isLeadFiller(words[0]) {
		words = words[1:]
	}
	return strings.Join(words, " ")
}

// isLeadFiller reports whether w is a mention or a polite opener that adds
// nothing to a title.
func isLeadFiller(w string) bool {
	if strings.HasPrefix(w, "$") || strings.HasPrefix(w, "@") {
		return true
	}
	switch strings.ToLower(strings.Trim(w, ",.!?")) {
	case "please", "pls", "can", "could", "would", "will", "you", "hey", "hi", "hello", "ok", "okay", "so", "just", "kindly", "i", "want", "need", "to":
		return true
	}
	return false
}
