package session

import "strings"

// Sigil starts a mention of an agent or a person: "@codex review this".
// AltSigil, "$", does the same, as it always has. Files and other context
// take "#" in the clients, which insert them as "@path": a name followed by
// "/" or by "." and more is never a mention (scanMention).
const (
	Sigil    = '@'
	AltSigil = '$'
)

// isSigil reports whether c starts a mention: "@" or "$".
func isSigil(c byte) bool { return c == Sigil || c == AltSigil }

// defuse turns the sigils in text that could start a mention into their
// full-width look-alikes, so that text (a teammate's) cannot pick or add
// agents. Every "$" goes; an "@" goes only where a name follows it as a
// mention (scanMention), so emails and "@path" file references stay as they
// are.
func defuse(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); i++ {
		switch c := text[i]; {
		case c == AltSigil:
			b.WriteString("＄")
		case c == Sigil && mentionAt(text, i):
			b.WriteString("＠")
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// mentionAt reports whether a mention could start at s[i].
func mentionAt(s string, i int) bool {
	_, _, ok := scanMention(s, i)
	return ok
}

// TrimSigil drops a leading "@" or "$" from an agent name.
func TrimSigil(name string) string {
	return strings.TrimLeft(strings.TrimSpace(name), "$@")
}

// MemberRef is what addressing needs to know about a thread member.
type MemberRef struct {
	ID, Name, Kind string
	Exited         bool
	// Busy, Waiting and Last pick among several members of one kind: busy
	// in a turn, how many messages wait for it, and whether it spoke last.
	Busy    bool
	Waiting int
	Last    bool
}

// suffixAll on a kind name, "claude*", is how an agent reaches every live
// member of that kind (thread.findAll). The user's messages have no
// suffixes: "@claude*" or "@claude+" there is plain text, and a race starts
// from /race in the clients (Manager.Race).
const suffixAll = '*'

// choose picks who a kind name reaches among refs: a live member of that kind,
// idle before busy, then the one with fewer messages waiting; among equals the
// one who spoke last, then the first to join. ok is false when the kind has no
// live member.
func choose(refs []MemberRef, kind string) (MemberRef, bool) {
	var best MemberRef
	found := false
	better := func(m MemberRef) bool {
		if m.Busy != best.Busy {
			return !m.Busy
		}
		if m.Waiting != best.Waiting {
			return m.Waiting < best.Waiting
		}
		return m.Last && !best.Last
	}
	for _, m := range refs {
		if m.Exited || !strings.EqualFold(m.Kind, kind) {
			continue
		}
		if !found || better(m) {
			best, found = m, true
		}
	}
	return best, found
}

// Addressing is the result of ParseAddressing.
type Addressing struct {
	// Members are the existing members addressed, by id, in order of first mention.
	Members []string
	// NewKinds are kinds named (@claude, @codex) that have no live member in
	// the thread: Post adds one member for each.
	NewKinds []string
	// NewProfiles are profiles named (@charles) that no member of the thread
	// is called: Post adds a member from each, named after it.
	NewProfiles []string
	// All is true when $all was used.
	All bool
	// Body is the message without its leading mentions ("$codex review this" ->
	// "review this"). Mentions further in (at the start of a later clause) stay.
	Body string
	// Unknown lists $names that matched no member and no kind.
	Unknown []string
}

// Mentioned reports whether the text addressed anyone explicitly.
func (a Addressing) Mentioned() bool {
	return a.All || len(a.Members) > 0 || len(a.NewKinds) > 0 || len(a.NewProfiles) > 0
}

// ParseAddressing finds the mentions in a message the user typed. "@name"
// and "$name" are the same mention.
//
// A mention counts when it starts a clause: at the very start of the text, at
// the start of a line, after . ! ? ; : or a comma, after "then" / "and" / "also"
// / "plus", or directly after another mention. "ask @codex" (mid-clause),
// "user@example.com", "@codex.go" and "@src/main.go" are plain text. A name
// resolves in this order: "all" addresses every member; a kind ("claude",
// "codex") addresses one live member of that kind (see choose), or - when
// the thread has none - asks for a new one; then a member's name or id
// (case-insensitively); then one of profiles, the named agents, which asks
// for a member from it. Anything else is plain text, "@claude+", "@claude*"
// and "@claude×3" among it: more agents and races are the clients' to start
// (Spawn, Race), not something typed.
func ParseAddressing(text string, members []MemberRef, kinds []string, profiles ...string) Addressing {
	text = strings.TrimSpace(text)
	var out Addressing
	// add records a mention; a name that is no member, kind, profile or
	// "all" is plain text ($HOME, $PATH, @Override), so it is not taken.
	add := func(name string) bool {
		if strings.EqualFold(name, "all") {
			out.All = true
			return true
		}
		id, kind, prof, ok := resolveName(name, members, kinds, profiles)
		switch {
		case !ok:
			return false
		case id != "":
			out.Members = appendUnique(out.Members, id)
		case prof != "":
			out.NewProfiles = appendUnique(out.NewProfiles, prof)
		default:
			out.NewKinds = appendUnique(out.NewKinds, kind)
		}
		return true
	}

	// the leading run of mentions
	p := 0
	for {
		for p < len(text) && (text[p] == ' ' || text[p] == '\t' || text[p] == '\n' || text[p] == '\r' || text[p] == ',') {
			p++
		}
		if p < len(text) && isSigil(text[p]) {
			if name, end, ok := scanMention(text, p); ok && add(name) {
				p = end
				continue
			}
		}
		break
	}
	rest := text[p:]
	if p > 0 {
		rest = strings.TrimLeft(rest, " \t\r\n:,;")
		out.Body = strings.TrimSpace(rest)
	} else {
		out.Body = text
	}

	// mentions that start a later clause
	for i := p; i < len(text); i++ {
		if !isSigil(text[i]) || !clauseStart(text, i) {
			continue
		}
		if name, end, ok := scanMention(text, i); ok {
			add(name)
			i = end - 1
		}
	}
	return out
}

// resolveName maps a mention to an existing member id, a kind to add or a
// profile to add a member from.
func resolveName(tok string, members []MemberRef, kinds, profiles []string) (id, newKind, newProfile string, ok bool) {
	// A kind first: it reaches the pool of that kind, whatever the members
	// are called.
	if k, isKind := kindOf(tok, kinds); isKind {
		if m, ok := choose(members, k); ok {
			return m.ID, "", "", true
		}
		return "", k, "", true
	}
	// Then names: an id (unique across the deck) can look like another
	// member's name in this thread.
	for _, m := range members {
		if strings.EqualFold(m.Name, tok) {
			return m.ID, "", "", true
		}
	}
	for _, m := range members {
		if strings.EqualFold(m.ID, tok) {
			return m.ID, "", "", true
		}
	}
	// Then a profile no member is called yet.
	if p, isProfile := kindOf(tok, profiles); isProfile {
		return "", "", p, true
	}
	return "", "", "", false
}

// kindOf returns the kind tok names, if any (or the profile, given profiles).
func kindOf(tok string, kinds []string) (string, bool) {
	for _, k := range kinds {
		if strings.EqualFold(k, tok) {
			return k, true
		}
	}
	return "", false
}

// splitSuffix splits "claude*" into "claude" and '*'; suf is 0 when the
// name has no suffix.
func splitSuffix(name string) (base string, suf byte) {
	if n := len(name); n > 1 && name[n-1] == suffixAll {
		return name[:n-1], name[n-1]
	}
	return name, 0
}

func isNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

func isSpaceByte(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// scanMention reads "@name" (or "$name") at s[i] and returns the name and the
// index after it.
// The name may end in one "*" (see suffixAll) and must be followed by the
// end of the text, whitespace, one of , : ; ! ? or a sentence-ending ".".
func scanMention(s string, i int) (name string, end int, ok bool) {
	j := i + 1
	for j < len(s) && isNameByte(s[j]) {
		j++
	}
	if j == i+1 {
		return "", 0, false
	}
	if j < len(s) && s[j] == suffixAll {
		j++
	}
	if j < len(s) {
		switch c := s[j]; {
		case isSpaceByte(c), c == ',', c == ':', c == ';', c == '!', c == '?':
		case c == '.':
			if j+1 < len(s) && !isSpaceByte(s[j+1]) {
				return "", 0, false
			}
		default:
			return "", 0, false
		}
	}
	return s[i+1 : j], j, true
}

// clauseStart reports whether the sigil at s[i] begins a clause.
func clauseStart(s string, i int) bool {
	j := i
	for j > 0 && (s[j-1] == ' ' || s[j-1] == '\t') {
		j--
	}
	if j == 0 {
		return true
	}
	switch s[j-1] {
	case '\n', '\r', '.', '!', '?', ';', ':', ',':
		return true
	}
	k := j
	for k > 0 && (s[k-1] >= 'a' && s[k-1] <= 'z' || s[k-1] >= 'A' && s[k-1] <= 'Z') {
		k--
	}
	switch strings.ToLower(s[k:j]) {
	case "then", "and", "also", "plus":
		return true
	}
	return false
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return list
		}
	}
	return append(list, v)
}

// mention is one relay requested by a line of an agent's reply.
type mention struct {
	Names []string // targets as written (without the sigil); may contain "all"
	Text  string
}

// parseMentions finds the lines of an agent's final reply that start with
// @name or $name. Fenced code blocks are ignored. A mention's text is the rest of its
// line plus the lines that follow up to a blank line, the next mention or the
// end. Mentions with the same targets are merged.
func parseMentions(reply string) []mention {
	lines := strings.Split(strings.ReplaceAll(reply, "\r\n", "\n"), "\n")
	var out []mention
	fence := false
	cur := -1
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			fence = !fence
			cur = -1
			continue
		}
		if fence {
			continue
		}
		if trim == "" {
			cur = -1
			continue
		}
		if isSigil(trim[0]) {
			var names []string
			p := 0
			for p < len(trim) && isSigil(trim[p]) {
				name, end, ok := scanMention(trim, p)
				if !ok {
					break
				}
				names = append(names, name)
				p = end
				for p < len(trim) && (trim[p] == ' ' || trim[p] == ',') {
					p++
				}
			}
			if len(names) > 0 {
				rest := strings.TrimSpace(strings.TrimLeft(trim[p:], ":;- "))
				out = append(out, mention{Names: names, Text: rest})
				cur = len(out) - 1
				continue
			}
		}
		if cur >= 0 {
			if out[cur].Text != "" {
				out[cur].Text += "\n"
			}
			out[cur].Text += trim
		}
	}
	// merge mentions with identical targets, drop empty ones
	var merged []mention
	for _, m := range out {
		if strings.TrimSpace(m.Text) == "" {
			continue
		}
		key := strings.ToLower(strings.Join(m.Names, ","))
		found := false
		for i := range merged {
			if strings.ToLower(strings.Join(merged[i].Names, ",")) == key {
				merged[i].Text += "\n\n" + m.Text
				found = true
				break
			}
		}
		if !found {
			merged = append(merged, m)
		}
	}
	return merged
}
