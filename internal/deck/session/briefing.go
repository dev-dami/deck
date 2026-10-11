package session

import (
	"fmt"
	"strings"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/brand"
	"github.com/dev-dami/deck/internal/deck/store"
)

const (
	maxEntryChars    = 6000  // one briefed message is cut here
	maxBriefingChars = 40000 // a whole briefing keeps its tail
	readThreadLast   = 200   // entries thread.read covers
	maxInstrChars    = 1400  // Spec.Instructions stays well under 1.5 KB
)

// viewer says whose point of view a rendering takes.
type viewer struct {
	self        string // member id; "" for none
	skipSelf    bool   // leave out the viewer's own events (a briefing)
	includeOpen bool   // include turns still in flight (thread.read)
}

// line is one rendered block of a briefing.
type line struct {
	text      string
	addressed bool // a user/relay message addressed to the viewer
	user      bool
	raw       string        // the bare message text (user lines)
	images    []agent.Image // images attached to an addressed user message
}

// walk renders t.log[start:end] from v's point of view. in decides whether the
// entry at index i takes part (a briefing skips what the member already has).
// The second result maps a speaker to the first index of its turn that is still
// in flight, which the caller must hold back and offer again later. t.mu is held.
func (m *Manager) walk(t *thread, v viewer, start, end int, in func(i int, e store.Entry) bool) ([]line, map[string]int) {
	cfg := m.config()
	var out []line
	hold := map[string]int{}
	edited := t.editedTexts()

	type acc struct {
		files, cmds, edits []string
		text               string
	}
	accs := map[string]*acc{}
	var order []string
	flush := func(sp string) {
		ac := accs[sp]
		if ac == nil {
			return
		}
		delete(accs, sp)
		who := t.label(sp, v.self)
		files := ac.files
		if len(files) == 0 {
			files = ac.edits
		}
		if len(files) > 0 {
			out = append(out, line{text: fmt.Sprintf("[%s changed %s: %s]", who, plural(len(files), "file"), listMax(files, 5))})
		}
		if len(ac.cmds) > 0 {
			out = append(out, line{text: fmt.Sprintf("[%s ran: %s]", who, listMax(ac.cmds, 3))})
		}
		if ac.text != "" {
			out = append(out, line{text: "[" + who + "] " + clip(ac.text, maxEntryChars)})
		}
	}

	for i := start; i < end && i < len(t.log); i++ {
		e := t.log[i]
		if !in(i, e) {
			continue
		}
		switch e.Type {
		case store.EntryEvent:
			if e.Delta || e.Kind == agent.KindUserText {
				continue
			}
			sp := e.AgentID
			if sp == v.self && v.skipSelf {
				continue
			}
			if !v.includeOpen && sp != v.self && i >= t.boundary[sp] {
				if _, ok := hold[sp]; !ok {
					hold[sp] = i
				}
				continue
			}
			ac := accs[sp]
			if ac == nil {
				ac = &acc{}
				accs[sp] = ac
				order = append(order, sp)
			}
			switch e.Kind {
			case agent.KindAssistantText:
				if strings.TrimSpace(e.Text) != "" {
					ac.text = strings.TrimSpace(e.Text)
				}
			case agent.KindFileChange:
				if e.Diff != nil {
					ac.files = addUnique(ac.files, e.Diff.Path)
				}
			case agent.KindToolResult:
				if e.Tool == nil {
					continue
				}
				switch {
				case isCommandTool(e.Tool.Name):
					ac.cmds = append(ac.cmds, clip(oneLine(e.Tool.Title), 80))
				case isEditTool(e.Tool.Name) && e.Tool.Status != agent.ToolError && e.Tool.Title != "":
					ac.edits = addUnique(ac.edits, oneLine(e.Tool.Title))
				}
			case agent.KindTurnDone, agent.KindExited:
				flush(sp)
			}
		case store.EntryUser:
			if v.self != "" && t.skip[skipKey{i, v.self}] != 0 {
				continue // cancelled, or already delivered as a steer
			}
			raw := strings.TrimSpace(e.Text)
			if txt, ok := edited[e.ID]; ok {
				raw = strings.TrimSpace(txt)
			}
			l := line{user: true, raw: raw}
			who := userLabel(cfg.TellAgentsName, e.Speaker)
			if e.Steer {
				// A steer reaches only the member it redirected; the others read the
				// queued message it came from.
				if !contains(e.To, v.self) {
					continue
				}
				l.user = false
				l.addressed = v.skipSelf
				l.text = steerText(raw, e.Old)
				if v.skipSelf {
					l.images = t.imagesOf(e)
				}
			} else if v.skipSelf && contains(e.To, v.self) {
				l.addressed = true
				l.text = "[" + who + "] " + clip(raw, maxEntryChars)
				l.images = t.imagesOf(e)
			} else {
				l.text = "[" + who + t.arrow(e.To, "", v.self) + "] " + clip(raw, maxEntryChars)
			}
			out = append(out, l)
		case store.EntryEdit:
			if v.self == "" || !contains(e.To, v.self) || t.skip[skipKey{i, v.self}] != 0 {
				continue
			}
			out = append(out, line{addressed: v.skipSelf, text: editText(e.Old, e.Text)})
		case store.EntryRelay:
			if e.Speaker == v.self && v.skipSelf {
				continue
			}
			l := line{text: "[" + t.label(e.Speaker, v.self) + t.arrow(e.To, e.Speaker, v.self) + "] " + clip(strings.TrimSpace(e.Text), maxEntryChars)}
			if v.skipSelf && contains(e.To, v.self) {
				l.addressed = true
				l.text = "[" + t.label(e.Speaker, v.self) + " → " + t.name(v.self) + "] " + clip(strings.TrimSpace(e.Text), maxEntryChars)
			}
			out = append(out, l)
		case store.EntryMember:
			if e.Speaker == v.self && v.skipSelf {
				continue
			}
			who := t.name(e.Speaker)
			if who != e.Text {
				who += " (" + e.Text + ")"
			}
			if e.Speaker == v.self {
				who = "you (" + who + ")"
			}
			out = append(out, line{text: "[deck] " + who + " joined the thread"})
		case store.EntryPause:
			out = append(out, line{text: "[deck] agent-to-agent messages paused after " + e.Text + " hops; waiting for the user"})
		case store.EntryContinue:
			out = append(out, line{text: "[deck] the user let agent-to-agent messages continue"})
		case store.EntryRevert, store.EntryUnrevert:
			out = append(out, line{text: revertText(e, revertSince(t.log, e))})
		}
	}
	for _, sp := range order {
		flush(sp)
	}
	return out, hold
}

// buildPrompt renders the prompt for the next turn of a: what it missed since
// its last turn, then the messages addressed to it. A lone user message with
// nothing to brief is passed through unchanged, so a one-member thread behaves
// exactly as it always did. It returns the member's new cursor and held-back
// speakers. t.mu is held.
func (m *Manager) buildPrompt(t *thread, a *agentState) (prompt string, images []agent.Image, cursor int, hold map[string]int) {
	start := a.cursor
	for _, h := range a.holdFrom {
		if h < start {
			start = h
		}
	}
	end := len(t.log)
	in := func(i int, e store.Entry) bool {
		if i >= a.cursor {
			return true
		}
		if e.Type != store.EntryEvent {
			return false
		}
		h, ok := a.holdFrom[e.AgentID]
		return ok && i >= h
	}
	lines, hold := m.walk(t, viewer{self: a.id, skipSelf: true}, start, end, in)

	var brief, addressed []line
	for _, l := range lines {
		if l.addressed {
			addressed = append(addressed, l)
			images = append(images, l.images...)
		} else {
			brief = append(brief, l)
		}
	}
	if len(brief) == 0 && len(addressed) == 1 && addressed[0].user {
		return addressed[0].raw, images, end, hold
	}
	var b strings.Builder
	if len(brief) > 0 {
		b.WriteString("[deck] Thread activity since your last turn:\n\n")
		b.WriteString(tail(joinLines(brief), maxBriefingChars))
		if len(addressed) > 0 {
			b.WriteString("\n\n[deck] Addressed to you:\n\n")
		}
	}
	b.WriteString(joinLines(addressed))
	return b.String(), images, end, hold
}

// readThread renders a's whole thread (the last readThreadLast entries) as the
// plain text the thread.read op returns.
func (m *Manager) readThread(agentID string) string {
	a := m.get(agentID)
	if a == nil {
		return ""
	}
	t := a.thread
	t.mu.Lock()
	defer t.mu.Unlock()
	start := len(t.log) - readThreadLast
	if start < 0 {
		start = 0
	}
	lines, _ := m.walk(t, viewer{self: a.id, includeOpen: true}, start, len(t.log), func(int, store.Entry) bool { return true })
	var names []string
	for _, o := range t.members {
		names = append(names, o.getName())
	}
	head := fmt.Sprintf("Thread with %s (you are %s). ", strings.Join(names, ", "), a.getName())
	if start > 0 {
		head += fmt.Sprintf("Showing the last %d of %d entries.", len(t.log)-start, len(t.log))
	}
	if len(lines) == 0 {
		return head + "\n\n(nothing has been said yet)"
	}
	return head + "\n\n" + tail(joinLines(lines), maxBriefingChars)
}

// instructions is the short briefing every member starts with. It carries no
// thread history (that goes in prompts) and stays under 1.5 KB because it
// travels on a command line.
func (m *Manager) instructions(t *thread, a *agentState, tools bool) string {
	cfg := m.config()
	user := "the user"
	if cfg.TellAgentsName && cfg.UserName != "" {
		user = cfg.UserName
	}
	t.mu.Lock()
	var others []string
	for _, o := range t.members {
		if o != a {
			others = append(others, o.getName())
		}
	}
	t.mu.Unlock()

	who := user
	if len(others) > 0 {
		who = strings.Join(others, ", ") + " and " + user
	}
	var b strings.Builder
	fmt.Fprintf(&b, "You are %q in a shared Deck thread with %s. Everything said in the thread is visible to all of them.\n", clip(a.getName(), 30), clip(who, 200))
	b.WriteString("What others said since your last turn is summarised first; messages from other agents are prefixed with their name, like \"[codex] ...\".\n")
	if tools {
		b.WriteString("Deck's MCP server has two tools: search finds Deck ops, skills other agents have and this project's past threads; execute runs one op.\n")
		b.WriteString("To message another agent, call execute with op \"thread.send\" and args {to: an agent name or \"all\", message}, or start a line of your final reply with $name. Op \"thread.read\" shows the whole thread.\n")
	} else {
		b.WriteString("To message another agent, start a line of your final reply with $name (or $all).\n")
	}
	b.WriteString("Keep messages to other agents short and actionable. Do not repeat what the user can already see.\n")
	fmt.Fprintf(&b, "Write plan files in %s.", brand.PlansDir())
	// Deck's own attribution, whichever CLI does the work (Settings → General).
	if cfg.DeckAttribution {
		fmt.Fprintf(&b, " Commits you make end with the trailer %q and no other AI attribution; pull requests end with %q.",
			brand.CommitTrailer, brand.PRFooter)
	}
	return clip(b.String(), maxInstrChars)
}

// ---- small helpers ----

const steerHeader = "[deck] The user is redirecting you mid-task:"

// steerText is the prompt text of a steer; old is non-empty when the steer
// delivers an edit of an earlier message (raw is then the new text).
func steerText(raw, old string) string {
	if old != "" {
		return steerHeader + "\n" + editBody(old, raw)
	}
	return steerHeader + "\n" + clip(raw, maxEntryChars)
}

// editText is the prompt text that tells a member the user edited a message.
func editText(old, new string) string { return "[deck] " + editBody(old, new) }

func editBody(old, new string) string {
	return "The user edited their earlier message.\nBefore: " + clip(strings.TrimSpace(old), maxEntryChars) +
		"\nAfter: " + clip(strings.TrimSpace(new), maxEntryChars) +
		"\nTreat the edited version as what they meant."
}

// editedTexts maps a user entry id to its latest edited text. t.mu is held.
func (t *thread) editedTexts() map[string]string {
	var out map[string]string
	for _, e := range t.log {
		if e.Type == store.EntryEdit {
			if out == nil {
				out = map[string]string{}
			}
			out[e.Ref] = e.Text
		}
	}
	return out
}

// name is a member's current display name (its id when unknown).
func (t *thread) name(id string) string {
	for _, o := range t.members {
		if o.id == id {
			return o.getName()
		}
	}
	return id
}

func (t *thread) label(id, self string) string {
	name := t.name(id)
	if id == self && self != "" {
		return name + " (you)"
	}
	return name
}

// arrow renders the " → names" suffix of a message header; "" when the message
// went to everyone but its sender.
func (t *thread) arrow(to []string, from, self string) string {
	n := 0
	for _, o := range t.members {
		if o.id != from {
			n++
		}
	}
	if len(to) == 0 || len(to) >= n {
		if from == "" {
			return ""
		}
		return " → all"
	}
	var names []string
	for _, id := range to {
		names = append(names, t.label(id, self))
	}
	return " → " + strings.Join(names, ", ")
}

func userLabel(tell bool, name string) string {
	if tell && name != "" {
		return name
	}
	return "user"
}

func joinLines(ls []line) string {
	parts := make([]string, len(ls))
	for i, l := range ls {
		parts[i] = l.text
	}
	return strings.Join(parts, "\n\n")
}

func isCommandTool(name string) bool {
	switch name {
	case "Bash", "PowerShell", "command_execution", "shell", "exec":
		return true
	}
	return false
}

func isEditTool(name string) bool {
	switch name {
	case "Edit", "MultiEdit", "Write", "NotebookEdit", "apply_patch":
		return true
	}
	return false
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func listMax(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:n], ", ") + fmt.Sprintf(" (+%d more)", len(items)-n)
}

func addUnique(list []string, v string) []string {
	if v == "" {
		return list
	}
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func contains(list []string, v string) bool {
	if v == "" {
		return false
	}
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "...[cut]"
}

func tail(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "[... earlier activity omitted; execute thread.read shows more ...]\n\n" + string(r[len(r)-n:])
}
