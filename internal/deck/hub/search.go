package hub

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Search answers the search tool: the ops that match the query (every op for
// an empty one), the best skill matches and the best chat-history hits.
// Ranking is plain keyword scoring.
func Search(env Env, raw json.RawMessage) string {
	var in struct {
		Query string          `json:"query"`
		Limit json.RawMessage `json:"limit"`
	}
	_ = json.Unmarshal(raw, &in)
	limit := 5
	if n, err := strconv.Atoi(strings.Trim(string(in.Limit), `" `)); err == nil && n > 0 {
		limit = min(n, 25)
	}
	terms := keywords(in.Query)

	var b strings.Builder
	// (a) ops
	type scored struct {
		op    Op
		score int
	}
	var hits []scored
	for _, o := range ops {
		s := 1
		if len(terms) > 0 {
			s = score(terms, o.Name, o.Desc+" "+argText(o))
		}
		if s > 0 {
			hits = append(hits, scored{o, s})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	if len(terms) == 0 {
		b.WriteString("Deck ops (run one with execute {op, args}; * = required):\n")
	} else {
		b.WriteString("Ops (execute {op, args}; * = required):\n")
		if len(hits) > limit+3 {
			hits = hits[:limit+3]
		}
	}
	if len(hits) == 0 {
		b.WriteString("  (no op matches; search with an empty query lists them all)\n")
	}
	for _, h := range hits {
		fmt.Fprintf(&b, "  %s %s - %s\n", h.op.Name, h.op.Signature(), h.op.Desc)
	}

	// (b) skills
	sks := discoverSkills(&env)
	if len(terms) == 0 {
		fmt.Fprintf(&b, "\nSkills: %d visible to the agents here (execute skills.list; skills.read {name}).\n", len(sks))
	} else {
		type sk struct {
			s     skillInfo
			score int
		}
		var ss []sk
		for _, s := range sks {
			if sc := score(terms, s.Name, s.Description+" "+s.Agent); sc > 0 {
				ss = append(ss, sk{s, sc})
			}
		}
		sort.SliceStable(ss, func(i, j int) bool { return ss[i].score > ss[j].score })
		if len(ss) > limit {
			ss = ss[:limit]
		}
		b.WriteString("\nSkills (execute skills.read {name}):\n")
		if len(ss) == 0 {
			b.WriteString("  (none match)\n")
		}
		for _, s := range ss {
			fmt.Fprintf(&b, "  %s [%s] - %s\n", s.s.Name, s.s.Source(), clip(s.s.Description, 160))
		}
	}

	// (c) chat history
	if len(terms) > 0 && env.Store != nil {
		hs := searchHistory(&env, terms, limit, 50)
		b.WriteString("\nHistory (execute history.read {thread}):\n")
		if len(hs) == 0 {
			b.WriteString("  (no thread of this project matches)\n")
		}
		for _, h := range hs {
			fmt.Fprintf(&b, "  %s [%s] %s: %s\n", h.title, h.thread, h.speaker, h.snippet)
		}
	} else if env.Store != nil {
		b.WriteString("History: execute history.threads, or search with words to find past threads.\n")
	}
	return capOut(b.String(), 0)
}

func argText(o Op) string {
	var parts []string
	for _, a := range o.Args {
		parts = append(parts, a.Name, a.Desc)
	}
	return strings.Join(parts, " ")
}

// stop words never count as search terms.
var stop = map[string]bool{
	"a": true, "an": true, "the": true, "of": true, "to": true, "in": true, "on": true, "for": true,
	"and": true, "or": true, "is": true, "are": true, "my": true, "me": true, "i": true, "what": true,
	"how": true, "do": true, "with": true, "from": true, "it": true, "this": true, "that": true,
}

// keywords splits a query into lower-case terms; dots and dashes split too,
// so "git.log" matches the op of that name.
func keywords(q string) []string {
	f := strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	var out []string
	seen := map[string]bool{}
	for _, w := range f {
		if stop[w] || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

// score counts the terms found in name (weight 3) and text (weight 1). A
// term that only prefixes a word scores less than a whole-word hit.
func score(terms []string, name, text string) int {
	nw := keywords(name)
	tw := keywords(text)
	s := 0
	for _, t := range terms {
		s += 3 * wordHit(t, nw)
		s += wordHit(t, tw)
	}
	return s
}

// wordHit is 2 for a whole-word match, 1 for a prefix match, else 0.
func wordHit(t string, words []string) int {
	best := 0
	for _, w := range words {
		switch {
		case w == t:
			return 2
		case len(t) >= 3 && strings.HasPrefix(w, t):
			best = 1
		}
	}
	return best
}

// nearestOps are op names sharing a word with name.
func nearestOps(name string, n int) []string {
	terms := keywords(name)
	type sc struct {
		name  string
		score int
	}
	var out []sc
	for _, o := range ops {
		if s := score(terms, o.Name, ""); s > 0 {
			out = append(out, sc{o.Name, s})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	var names []string
	for i := 0; i < len(out) && i < n; i++ {
		names = append(names, out[i].name)
	}
	return names
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
