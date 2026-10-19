package session

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/dev-dami/deck/internal/deck/store"
)

// Leases: members of a thread that share a work tree claim the files they are
// about to change (lease.acquire), so the others know. A lease names paths or
// globs relative to the repository root, and ends when its member releases it
// or exits, or after its time (default 15 minutes). Leases do not stop an
// agent's own tools; they are enforced where Deck writes: sub.patch.apply,
// sub.patch.revert and sub.merge.resolve into the shared tree refuse a path
// another member holds exclusively. Overlap is reported too: when two members
// change the same file of the shared tree within overlapWindow, or one changes
// a file another holds, Deck says so in the thread, and both see it in their
// next prompt.
//
// shortcut: leases live in memory only; a restart of Deck restarts the agents
// too, so they ask again. Persist them if threads outlive agents.

const (
	leaseTTL      = 15 * time.Minute
	maxLeaseTTL   = 2 * time.Hour
	overlapWindow = 10 * time.Minute
	viaOverlap    = "overlap" // EntryNotice.Via of overlap notes, which briefings show
)

type lease struct {
	member    string
	paths     []string // repository-relative paths or globs
	exclusive bool
	reason    string
	until     time.Time
}

// edit is the last change one member made to a file of the shared tree.
type edit struct {
	member string
	at     time.Time
}

// LeaseInfo is a lease as the UI shows it.
type LeaseInfo struct {
	Member    string // member id
	Paths     []string
	Exclusive bool
	Reason    string
	Until     time.Time
}

// liveLeases drops the leases that ran out or whose member exited and returns
// the rest. t.mu is held.
func (t *thread) liveLeases(now time.Time) []lease {
	kept := t.leases[:0]
	for _, l := range t.leases {
		if a := t.member(l.member); a != nil && a.status() != StatusExited && now.Before(l.until) {
			kept = append(kept, l)
		}
	}
	t.leases = kept
	return kept
}

// globsOverlap reports whether paths or globs a and b can name the same file.
// shortcut: it compares each against the other as a pattern, so two globs that
// share files without either matching the other ("a/*.go", "a/x*") are missed.
func globsOverlap(a, b string) bool {
	if a == b {
		return true
	}
	if ok, _ := doublestar.Match(a, b); ok {
		return true
	}
	ok, _ := doublestar.Match(b, a)
	return ok
}

// cleanGlob checks a lease path: relative to the repository, inside it, and a
// valid pattern.
func cleanGlob(p string) (string, error) {
	p = path.Clean(strings.ReplaceAll(strings.TrimSpace(p), "\\", "/"))
	switch {
	case p == "." || p == "":
		return "", errors.New("an empty path")
	case path.IsAbs(p) || p == ".." || strings.HasPrefix(p, "../"):
		return "", fmt.Errorf("%q is not inside the repository: give paths relative to its root", p)
	case !doublestar.ValidatePattern(p):
		return "", fmt.Errorf("%q is not a valid glob", p)
	}
	return p, nil
}

func (l lease) line(t *thread) string {
	s := t.name(l.member) + " holds " + strings.Join(l.paths, ", ")
	if l.exclusive {
		s += " (exclusive)"
	}
	s += " until " + l.until.Format("15:04")
	if l.reason != "" {
		s += ": " + l.reason
	}
	return s
}

// acquireLease gives a a lease on paths and says which other leases overlap
// them; an overlap does not stop the lease (the members sort it out).
func (m *Manager) acquireLease(a *agentState, raw string, minutes int, exclusive bool, reason string) (string, error) {
	var paths []string
	for _, p := range strings.Split(raw, ",") {
		if strings.TrimSpace(p) == "" {
			continue
		}
		g, err := cleanGlob(p)
		if err != nil {
			return "", err
		}
		paths = append(paths, g)
	}
	if len(paths) == 0 {
		return "", errors.New("give paths, comma-separated, relative to the repository root")
	}
	ttl := leaseTTL
	if minutes > 0 {
		ttl = min(time.Duration(minutes)*time.Minute, maxLeaseTTL)
	}
	now := time.Now()
	l := lease{member: a.id, paths: paths, exclusive: exclusive, reason: clipText(oneLine(reason), 120), until: now.Add(ttl)}
	t := a.thread
	t.mu.Lock()
	var also []string
	for _, o := range t.liveLeases(now) {
		if o.member != a.id && leasesOverlap(o.paths, paths) {
			also = append(also, o.line(t))
		}
	}
	t.leases = append(t.leases, l)
	t.mu.Unlock()
	m.emit(LeasesChangedMsg{ThreadID: t.id})
	out := "leased " + strings.Join(paths, ", ") + " until " + l.until.Format("15:04")
	if exclusive {
		out += " (exclusive: Deck refuses to apply capsules from others there)"
	}
	if len(also) > 0 {
		out += "\nalso held, talk to them first:\n" + strings.Join(also, "\n")
	}
	if h := m.memHint(a, paths, ""); h != "" {
		out += "\n" + h
	}
	return out, nil
}

func leasesOverlap(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if globsOverlap(x, y) {
				return true
			}
		}
	}
	return false
}

// releaseLeases ends a's leases: all of them, or those naming one of paths.
func (m *Manager) releaseLeases(a *agentState, raw string) string {
	want := map[string]bool{}
	for _, p := range strings.Split(raw, ",") {
		if g, err := cleanGlob(p); err == nil {
			want[g] = true
		}
	}
	t := a.thread
	t.mu.Lock()
	n := 0
	kept := t.leases[:0]
	for _, l := range t.leases {
		drop := l.member == a.id && len(want) == 0
		for _, p := range l.paths {
			drop = drop || l.member == a.id && want[p]
		}
		if drop {
			n++
			continue
		}
		kept = append(kept, l)
	}
	t.leases = kept
	t.mu.Unlock()
	if n > 0 {
		m.emit(LeasesChangedMsg{ThreadID: t.id})
	}
	return fmt.Sprintf("released %d leases", n)
}

// listLeases renders the thread's live leases.
func (m *Manager) listLeases(a *agentState) string {
	t := a.thread
	t.mu.Lock()
	defer t.mu.Unlock()
	var lines []string
	for _, l := range t.liveLeases(time.Now()) {
		lines = append(lines, l.line(t))
	}
	if len(lines) == 0 {
		return "no leases"
	}
	return strings.Join(lines, "\n")
}

// Leases lists the live leases of a thread.
func (m *Manager) Leases(threadID string) []LeaseInfo {
	t := m.thread(threadID)
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []LeaseInfo
	for _, l := range t.liveLeases(time.Now()) {
		out = append(out, LeaseInfo{Member: l.member, Paths: append([]string(nil), l.paths...), Exclusive: l.exclusive, Reason: l.reason, Until: l.until})
	}
	return out
}

// leaseBlock is the exclusive lease of a member other than a that covers one
// of the repository-relative paths, as the error a write by a is refused
// with; nil when there is none. t.mu is held.
func (t *thread) leaseBlock(a *agentState, paths []string) error {
	for _, l := range t.liveLeases(time.Now()) {
		if l.member == a.id || !l.exclusive {
			continue
		}
		for _, p := range paths {
			for _, g := range l.paths {
				if ok, _ := doublestar.Match(g, p); ok || g == p {
					return fmt.Errorf("%s is leased: %s; nothing was written (ask them with thread.send, or wait)", p, l.line(t))
				}
			}
		}
	}
	return nil
}

// noteEdit records that a changed file p (as the agent reported it) and, when
// that overlaps another member's recent change or lease in the shared tree,
// says so in the thread. Changes in a member's own worktree are not compared:
// capsules settle those.
func (m *Manager) noteEdit(t *thread, a *agentState, p string) {
	if a.getWorktree() != nil {
		return
	}
	root := m.subRoot(a)
	t.mu.Lock()
	rel, ok := relTo(root, t.dir, p)
	if !ok {
		t.mu.Unlock()
		return
	}
	now := time.Now()
	key := pathKey(rel)
	var note string
	for _, l := range t.liveLeases(now) {
		if l.member == a.id {
			continue
		}
		for _, g := range l.paths {
			if ok, _ := doublestar.Match(g, rel); ok || g == rel {
				note = fmt.Sprintf("%s changed %s, which %s", a.getName(), rel, l.line(t))
			}
		}
	}
	if prev, seen := t.edits[key]; note == "" && seen && prev.member != a.id && now.Sub(prev.at) < overlapWindow {
		if o := t.member(prev.member); o != nil && o.status() != StatusExited {
			note = fmt.Sprintf("%s and %s both changed %s in the last few minutes; one may undo the other", o.getName(), a.getName(), rel)
		}
	}
	if t.edits == nil {
		t.edits = map[string]edit{}
	}
	t.edits[key] = edit{member: a.id, at: now}
	if note == "" || t.warned[key+">"+a.id].After(now.Add(-overlapWindow)) {
		t.mu.Unlock()
		return
	}
	if t.warned == nil {
		t.warned = map[string]time.Time{}
	}
	t.warned[key+">"+a.id] = now
	e := store.Entry{Type: store.EntryNotice, Via: viaOverlap, Event: eventText(note)}
	m.appendLocked(t, e)
	t.mu.Unlock()
	m.emit(msgFor(t.id, e))
}
