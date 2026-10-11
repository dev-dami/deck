package hub

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/store"
)

// projectThreads are the store's threads that belong to env's project, the
// most recently updated first.
func projectThreads(env *Env) ([]store.Thread, error) {
	if env.Store == nil {
		return nil, errors.New("Deck keeps no thread history here")
	}
	all, err := env.Store.List()
	if err != nil {
		return nil, err
	}
	roots := env.Roots
	if len(roots) == 0 && env.Dir != "" {
		roots = []string{env.Dir}
	}
	var out []store.Thread
	for _, t := range all {
		if t.ID == env.ThreadID || inProject(t, roots) {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
}

func inProject(t store.Thread, roots []string) bool {
	dirs := []string{t.Dir}
	if t.Worktree != nil {
		dirs = append(dirs, t.Worktree.Main, t.Worktree.Path)
	}
	for _, d := range dirs {
		for _, r := range roots {
			if d != "" && r != "" && samePath(d, r) {
				return true
			}
		}
	}
	return false
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func agentsOf(t store.Thread) string {
	var names []string
	for _, m := range t.Members {
		n := m.Name
		if n == "" {
			n = m.ID
		}
		names = append(names, n)
	}
	if len(names) == 0 {
		return t.Name
	}
	return strings.Join(names, ", ")
}

func titleOf(t store.Thread) string {
	if s := strings.TrimSpace(t.Title); s != "" {
		return clip(s, 60)
	}
	return t.ID
}

func opHistoryThreads(_ context.Context, env *Env, a args) (string, error) {
	ts, err := projectThreads(env)
	if err != nil {
		return "", err
	}
	limit := a.num("limit", 20, 1, 200)
	var b strings.Builder
	fmt.Fprintf(&b, "%d Deck threads in this project (newest first):\n", len(ts))
	for i, t := range ts {
		if i == limit {
			fmt.Fprintf(&b, "... %d more (pass a larger limit)\n", len(ts)-limit)
			break
		}
		mark := ""
		if t.ID == env.ThreadID {
			mark = " (this thread)"
		}
		fmt.Fprintf(&b, "  %s  %s  [%s]  %s%s\n", t.ID, t.Updated.Format("2006-01-02 15:04"), agentsOf(t), titleOf(t), mark)
	}
	return b.String(), nil
}

// line is one readable entry of a thread log.
type line struct {
	at      time.Time
	speaker string
	text    string
	weight  int // 2 for things said, 1 for tool calls
}

// readable turns a log into the lines worth showing or searching: what the
// user and the agents said, the messages between agents and the tool calls
// (name and title only: never tool input or output, nor raw protocol data).
func readable(entries []store.Entry, t store.Thread) []line {
	names := map[string]string{}
	for _, m := range t.Members {
		if m.Name != "" {
			names[m.ID] = m.Name
		}
	}
	name := func(id string) string {
		if n, ok := names[id]; ok {
			return n
		}
		if id == "" {
			return "agent"
		}
		return id
	}
	var out []line
	for i := range entries {
		e := &entries[i]
		// Only Time, Text, Kind and the tool's name and title are read, so the
		// raw protocol data and tool input are never looked at (or validated).
		ev := &e.Event
		switch e.Type {
		case store.EntryUser:
			sp := e.Speaker
			if sp == "" {
				sp = "user"
			}
			out = append(out, line{ev.Time, sp, ev.Text, 2})
		case store.EntryRelay:
			to := make([]string, len(e.To))
			for i, id := range e.To {
				to[i] = name(id)
			}
			out = append(out, line{ev.Time, name(e.Speaker) + " -> " + strings.Join(to, ","), ev.Text, 2})
		case store.EntryNotice:
			out = append(out, line{ev.Time, "deck", ev.Text, 1})
		case store.EntryEvent:
			switch {
			case ev.Kind == agent.KindAssistantText && !ev.Delta:
				out = append(out, line{ev.Time, name(ev.AgentID), ev.Text, 2})
			case ev.Kind == agent.KindToolCall && ev.Tool != nil:
				out = append(out, line{ev.Time, name(ev.AgentID), "[" + ev.Tool.Name + "] " + ev.Tool.Title, 1})
			}
		}
	}
	for i := range out {
		out[i].text = strings.TrimSpace(out[i].text)
	}
	return out
}

// threadLines are a thread's readable lines, kept while its log is unchanged.
type threadLines struct {
	key   logKey
	meta  store.Thread
	lines []line
	low   []string // each line's text in lower case, for search
	bytes int
}

// logKey identifies one state of a thread's log: its file's size and time,
// and the thread's Updated (which moves even when writes are buffered).
type logKey struct {
	size    int64
	mod     time.Time
	updated time.Time
}

func (k logKey) same(o logKey) bool {
	return k.size == o.size && k.mod.Equal(o.mod) && k.updated.Equal(o.updated)
}

// historyCache keeps the readable lines of recently searched threads, so a
// search re-reads only the logs that changed since the last one. It holds at
// most historyCacheBytes of text.
var historyCache = struct {
	sync.Mutex
	m     map[string]*threadLines
	bytes int
}{m: map[string]*threadLines{}}

const historyCacheBytes = 32 << 20

// logDir is implemented by a store that keeps each thread's log in
// <Dir>/<id>.jsonl (store.Store); history from any other store is read afresh
// every time.
type logDir interface{ Dir() string }

// threadReadable is readable(env.Store.LoadEntries(t.ID)) through the cache.
// The result is shared: callers must not modify it.
func threadReadable(env *Env, t store.Thread) (*threadLines, error) {
	load := func() (*threadLines, error) {
		entries, meta, err := env.Store.LoadEntries(t.ID)
		if err != nil {
			return nil, err
		}
		tl := &threadLines{meta: meta, lines: readable(entries, meta)}
		tl.low = make([]string, len(tl.lines))
		for i, l := range tl.lines {
			tl.low[i] = strings.ToLower(l.text)
			tl.bytes += len(l.text) + len(l.speaker) + 64
			if tl.low[i] != l.text {
				tl.bytes += len(tl.low[i])
			}
		}
		return tl, nil
	}
	d, ok := env.Store.(logDir)
	if !ok || d.Dir() == "" {
		return load()
	}
	path := filepath.Join(d.Dir(), t.ID+".jsonl")
	st, err := os.Stat(path)
	if err != nil {
		return load()
	}
	key := logKey{size: st.Size(), mod: st.ModTime(), updated: t.Updated}
	historyCache.Lock()
	c := historyCache.m[path]
	historyCache.Unlock()
	if c != nil && c.key.same(key) {
		return c, nil
	}
	tl, err := load()
	if err != nil {
		return nil, err
	}
	tl.key = key
	historyCache.Lock()
	if old := historyCache.m[path]; old != nil {
		historyCache.bytes -= old.bytes
		delete(historyCache.m, path)
	}
	if tl.bytes <= historyCacheBytes/4 {
		if historyCache.bytes+tl.bytes > historyCacheBytes {
			clear(historyCache.m)
			historyCache.bytes = 0
		}
		historyCache.m[path] = tl
		historyCache.bytes += tl.bytes
	}
	historyCache.Unlock()
	return tl, nil
}

// hit is one chat-history match.
type hit struct {
	thread, title, speaker, snippet string
	score                           int
	at                              time.Time
}

// searchHistory scores the readable lines of up to maxThreads of the
// project's threads against terms and returns the best limit hits.
func searchHistory(env *Env, terms []string, limit, maxThreads int) []hit {
	ts, err := projectThreads(env)
	if err != nil || len(terms) == 0 {
		return nil
	}
	if len(ts) > maxThreads {
		ts = ts[:maxThreads]
	}
	var hits []hit
	for _, t := range ts {
		tl, err := threadReadable(env, t)
		if err != nil {
			continue
		}
		meta := tl.meta
		for k, l := range tl.lines {
			low := tl.low[k]
			s, first := 0, -1
			for _, term := range terms {
				if i := strings.Index(low, term); i >= 0 {
					s += l.weight
					if first < 0 || i < first {
						first = i
					}
				}
			}
			if s == 0 {
				continue
			}
			hits = append(hits, hit{thread: t.ID, title: titleOf(meta), speaker: l.speaker, snippet: snippet(l.text, first), score: s, at: l.at})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].at.After(hits[j].at)
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// snippet is about 160 characters of text around byte offset at.
func snippet(text string, at int) string {
	const before, width = 50, 160
	start := max(at-before, 0)
	for start > 0 && start < len(text) && (text[start]&0xC0) == 0x80 {
		start--
	}
	s := text[start:]
	out := clip(s, width)
	if start > 0 {
		out = "…" + out
	}
	return out
}

func opHistorySearch(_ context.Context, env *Env, a args) (string, error) {
	terms := keywords(a.str("query"))
	if len(terms) == 0 {
		return "", errors.New("the query has no words to search for")
	}
	if _, err := projectThreads(env); err != nil {
		return "", err
	}
	hs := searchHistory(env, terms, a.num("limit", 10, 1, 50), 200)
	if len(hs) == 0 {
		return "No thread of this project mentions " + strings.Join(terms, ", ") + ".", nil
	}
	var b strings.Builder
	for _, h := range hs {
		fmt.Fprintf(&b, "%s [%s] %s %s: %s\n", h.title, h.thread, h.at.Format("2006-01-02"), h.speaker, h.snippet)
	}
	b.WriteString("(execute history.read {thread} for more)\n")
	return b.String(), nil
}

func opHistoryRead(_ context.Context, env *Env, a args) (string, error) {
	ts, err := projectThreads(env)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(a.str("thread"))
	var meta *store.Thread
	for i := range ts {
		if ts[i].ID == id {
			meta = &ts[i]
			break
		}
	}
	if meta == nil {
		return "", fmt.Errorf("no thread %q in this project (execute history.threads)", id)
	}
	tl, err := threadReadable(env, *meta)
	if err != nil {
		return "", err
	}
	ls, t := tl.lines, tl.meta
	last := a.num("last", 60, 1, 400)
	start := max(len(ls)-last, 0)
	var b strings.Builder
	fmt.Fprintf(&b, "Thread %s %q with %s, in %s.", t.ID, titleOf(t), agentsOf(t), t.Dir)
	if start > 0 {
		fmt.Fprintf(&b, " Last %d of %d entries.", len(ls)-start, len(ls))
	}
	b.WriteString("\n\n")
	var body []string
	for _, l := range ls[start:] {
		body = append(body, fmt.Sprintf("[%s] %s: %s", l.at.Format("01-02 15:04"), l.speaker, clipLong(l.text, 2000)))
	}
	text := strings.Join(body, "\n")
	// Keep the tail when it is too long: the latest entries matter most.
	if room := MaxOutput - b.Len() - 200; len(text) > room {
		cut := len(text) - room
		for cut < len(text) && (text[cut]&0xC0) == 0x80 {
			cut++
		}
		text = "[... earlier entries omitted; pass a smaller last ...]\n" + text[cut:]
	}
	b.WriteString(text)
	return b.String(), nil
}

// clipLong shortens s to n runes, keeping its line breaks.
func clipLong(s string, n int) string {
	if len(s) <= n { // no more runes than bytes
		return s
	}
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + " …"
	}
	return s
}
