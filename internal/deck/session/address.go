package session

import (
	"strings"
)

// Sigil starts a mention of an agent: "$codex review this". "@" is left
// for files.
const Sigil = '$'

// TrimSigil drops a leading "$" (or "@", as agents sometimes write it) from
// an agent name.
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

// Suffixes on a kind name: "$claude*" addresses every live member of that
// kind, "$claude+" adds another one.
const (
	suffixAll = '*'
	suffixNew = '+'
)

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
	// NewKinds are kinds named ($claude, $codex) that have no live member in the
	// thread, or asked for with "+" ($claude+): Post adds one member for each
	// entry, so "$claude+ $claude+" adds two.
	NewKinds []string
	// All is true when $all was used.
	All bool
	// Body is the message without its leading mentions ("$codex review this" ->
	// "review this"). Mentions further in (at the start of a later clause) stay.
	Body string
	// Unknown lists $names that matched no member and no kind.
	Unknown []string
}

// Mentioned reports whether the text addressed anyone explicitly.
func (a Addressing) Mentioned() bool { return a.All || len(a.Members) > 0 || len(a.NewKinds) > 0 }

// ParseAddressing finds the @mentions in a message the user typed.
//
// A mention counts when it starts a clause: at the very start of the text, at
// the start of a line, after . ! ? ; : or a comma, after "then" / "and" / "also"
// / "plus", or directly after another mention. "ask $codex" (mid-clause) and
// "foo@bar" or "$codex.go" are plain text. A kind ("claude", "codex") addresses
// one live member of that kind (see choose), or - when the thread has none -
// asks for a new one; "$claude*" addresses all of them and "$claude+" always
// asks for a new one. Other names match a member's name or id
// (case-insensitively). "$all" addresses every member.
func ParseAddressing(text string, members []MemberRef, kinds []string) Addressing {
	text = strings.TrimSpace(text)
	var out Addressing
	// add records a mention; a name that is no member, kind or "all" is
	// plain text ($HOME, $PATH), so it is not taken.
	add := func(name string) bool {
		if strings.EqualFold(name, "all") {
			out.All = true
			return true
		}
		if base, suf := splitSuffix(name); suf != 0 {
			kind, ok := kindOf(base, kinds)
			if !ok {
				return false
			}
			if suf == suffixNew {
				out.NewKinds = append(out.NewKinds, kind)
				return true
			}
			n := 0
			for _, m := range members {
				if !m.Exited && strings.EqualFold(m.Kind, kind) {
					out.Members = appendUnique(out.Members, m.ID)
					n++
				}
			}
			if n == 0 {
				out.NewKinds = appendUnique(out.NewKinds, kind)
			}
			return true
		}
		id, kind, ok := resolveName(name, members, kinds)
		switch {
		case !ok:
			return false
		case id != "":
			out.Members = appendUnique(out.Members, id)
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
		if p < len(text) && text[p] == Sigil {
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
		if text[i] != Sigil || !clauseStart(text, i) {
			continue
		}
		if name, end, ok := scanMention(text, i); ok {
			add(name)
			i = end - 1
		}
	}
	return out
}

// resolveName maps a $token to an existing member id, or to a kind to add.
func resolveName(tok string, members []MemberRef, kinds []string) (id, newKind string, ok bool) {
	// A kind first: it reaches the pool of that kind, whatever the members
	// are called.
	if k, isKind := kindOf(tok, kinds); isKind {
		if m, ok := choose(members, k); ok {
			return m.ID, "", true
		}
		return "", k, true
	}
	// Then names: an id (unique across the deck) can look like another
	// member's name in this thread.
	for _, m := range members {
		if strings.EqualFold(m.Name, tok) {
			return m.ID, "", true
		}
	}
	for _, m := range members {
		if strings.EqualFold(m.ID, tok) {
			return m.ID, "", true
		}
	}
	return "", "", false
}

// kindOf returns the kind tok names, if any.
func kindOf(tok string, kinds []string) (string, bool) {
	for _, k := range kinds {
		if strings.EqualFold(k, tok) {
			return k, true
		}
	}
	return "", false
}

// splitSuffix splits "claude*" into "claude" and '*' ("+" likewise); suf is
// 0 when the name has no suffix.
func splitSuffix(name string) (base string, suf byte) {
	if n := len(name); n > 1 && (name[n-1] == suffixAll || name[n-1] == suffixNew) {
		return name[:n-1], name[n-1]
	}
	return name, 0
}

func isNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

func isSpaceByte(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// scanMention reads "$name" at s[i] and returns the name and the index after it.
// The name may end in one "*" or "+" (see suffixAll) and must be followed by
// the end of the text, whitespace, one of , : ; ! ? or a sentence-ending ".".
func scanMention(s string, i int) (name string, end int, ok bool) {
	j := i + 1
	for j < len(s) && isNameByte(s[j]) {
		j++
	}
	if j == i+1 {
		return "", 0, false
	}
	if j < len(s) && (s[j] == suffixAll || s[j] == suffixNew) {
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

// clauseStart reports whether the '$' at s[i] begins a clause.
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
	Names []string // targets as written (without $); may contain "all"
	Text  string
}

// parseMentions finds the lines of an agent's final reply that start with
// $name. Fenced code blocks are ignored. A mention's text is the rest of its
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
		if strings.HasPrefix(trim, string(Sigil)) {
			var names []string
			p := 0
			for p < len(trim) && trim[p] == Sigil {
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
