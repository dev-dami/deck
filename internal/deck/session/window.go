package session

import (
	"errors"
	"fmt"
	"iter"
	"sort"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/deck/store"
)

// A thread keeps only the newest part of its log in memory: t.log holds the
// entries from index t.base on, and the older ones stay in the store's log,
// where they are read back, a part at a time, by the rare reader that asks
// for them (a briefing of a member that missed a lot, a fork, the history,
// an edit of an old message). Every log index the thread keeps (cursors,
// boundary, inert, skip, the queue, the logIndex) counts from the start of
// the whole log. The scans that run all the time (is a message waiting for
// a member, which relay handed a task over) read the logIndex, which keeps
// a small head of every message, instead of the entries.
//
// An entry leaves memory only once the store's log holds it at a place the
// thread knows (logDisk), so it can always be read back.

const (
	// defaultLogWindow is how much of a thread's log stays in memory, counted
	// as the entries' size in the store plus entryCost each (about what an
	// entry costs beyond its text). Past it the oldest entries are dropped
	// until about half of it is left. Only the logKeep newest entries, and
	// those not in the store yet, can make the log larger.
	defaultLogWindow = 8 << 20
	entryCost        = 400
	logKeep          = 64
	// Marks are placed markEvery entries or markBytes bytes of the store's log
	// apart (at the first write past that): a read of an old entry starts
	// about that far before it at most, and entries leave memory a mark's
	// span at a time.
	markEvery = 64
	markBytes = 64 << 10
	// loadEntries entries, or loadBytes bytes, are read back at a time.
	loadEntries = 256
	loadBytes   = 1 << 20
)

// mark says that log entry idx starts at byte off of the store's log.
type mark struct {
	idx int
	off int64
}

// logDisk is where the thread's log is in the store. t.mu guards it, but
// marks only grows, so a view may share it.
type logDisk struct {
	// n entries of the log are in the store's log, which ends at byte end
	// after them. Only those can leave memory.
	n   int
	end int64
	// baseOff is where entry t.base starts.
	baseOff int64
	marks   []mark
	// synced is false when the store's log may not line up with the
	// thread's, entry for entry (a failed write, a torn last line, a write
	// from elsewhere): n then stops growing, and only what was read or
	// written before can leave memory.
	synced bool
	fix    *logFix
}

// logFix is what Resume does to an entry as it was stored, for entries
// [0, upto): a legacy user_text line becomes a user entry, and members whose
// id was taken get their new one. Entries read back are fixed the same way.
type logFix struct {
	user  string
	remap map[string]string
	upto  int
}

func (f *logFix) apply(i int, e *store.Entry) {
	if f == nil || i >= f.upto {
		return
	}
	canonical(e, f.user)
	if len(f.remap) == 0 {
		return
	}
	e.AgentID = mapID(f.remap, e.AgentID)
	if e.Type != store.EntryUser {
		e.Speaker = mapID(f.remap, e.Speaker)
	}
	for j := range e.To {
		e.To[j] = mapID(f.remap, e.To[j]) // a fresh copy, read from the store
	}
}

// size is the length of the whole log. t.mu is held.
func (t *thread) size() int { return t.base + len(t.log) }

// markLocked marks entry n as starting at byte end of the store's log, when
// the last mark is far enough behind. t.mu is held.
func (d *logDisk) markLocked(n int, end int64) {
	if k := len(d.marks); k > 0 {
		if last := d.marks[k-1]; last.idx >= n || (n-last.idx < markEvery && end-last.off < markBytes) {
			return
		}
	}
	d.marks = append(d.marks, mark{n, end})
}

// wroteLocked takes note of the writer's append of es, logged from index
// at, to bytes [start, end) of the store's log. t.mu is held.
func (d *logDisk) wroteLocked(at int, es []store.Entry, start, end int64, err error) {
	if !d.synced {
		return
	}
	lined := err == nil && at == d.n && start == d.end && end >= start
	for _, e := range es {
		lined = lined && !e.Delta // the store drops deltas; the log never has them
	}
	if !lined {
		d.synced = false
		return
	}
	d.n, d.end = at+len(es), end
	d.markLocked(d.n, d.end)
}

// trimLocked drops the oldest entries from memory while the log there costs
// more than the manager's window: down to half of it, at a mark, keeping the
// logKeep newest entries and every one the store does not hold yet. With
// reuse it moves the entries kept down the array they are in, which no view
// may share; otherwise they go to a new one. t.mu is held.
func (t *thread) trimLocked(reuse bool) {
	w, d := t.m.window, &t.disk
	if w <= 0 || len(d.marks) == 0 {
		return
	}
	cost := func(idx int, off int64) int64 { return d.end - off + entryCost*int64(d.n-idx) }
	if cost(t.base, d.baseOff) <= w {
		return
	}
	limit := min(d.n, t.size()-logKeep)
	// The marks a new base can be at: after the current one, not past limit.
	lo := sort.Search(len(d.marks), func(j int) bool { return d.marks[j].idx > t.base })
	hi := sort.Search(len(d.marks), func(j int) bool { return d.marks[j].idx > limit })
	if lo >= hi {
		return
	}
	// The cost falls as the base moves on: take the first mark at half the
	// window, or the last one allowed.
	k := lo + sort.Search(hi-lo, func(j int) bool { mk := d.marks[lo+j]; return cost(mk.idx, mk.off) <= w/2 })
	k = min(k, hi-1)
	mk := d.marks[k]
	if reuse {
		kept := copy(t.log, t.log[mk.idx-t.base:])
		clear(t.log[kept:])
		t.log = t.log[:kept]
	} else {
		// A new array, so the dropped entries can go; views of the old one
		// keep it as it is.
		t.log = append([]store.Entry(nil), t.log[mk.idx-t.base:]...)
	}
	t.base, d.baseOff = mk.idx, mk.off
}

// logView is a thread's log as it stood when it was taken. Entries never
// change once logged and the store's log is only appended to, so a view can
// be read without t.mu.
type logView struct {
	st    *store.Store
	id    string
	base  int
	mem   []store.Entry
	marks []mark
	fix   *logFix
}

// view takes a view of the log. t.mu is held.
func (t *thread) view() logView {
	return logView{st: t.m.st, id: t.id, base: t.base, mem: t.log[:len(t.log):len(t.log)],
		marks: t.disk.marks[:len(t.disk.marks):len(t.disk.marks)], fix: t.disk.fix}
}

func (v logView) size() int { return v.base + len(v.mem) }

// each calls fn with each entry from index from up to (not including) to, in
// order, until fn returns false. The entries before the view's base are read
// from the store a part at a time.
func (v logView) each(from, to int, fn func(i int, e store.Entry) bool) error {
	from, to = max(from, 0), min(to, v.size())
	for from < to && from < v.base {
		part, err := v.load(from, min(to, v.base))
		if err != nil {
			return err
		}
		for k, e := range part {
			if !fn(from+k, e) {
				return nil
			}
		}
		from += len(part)
	}
	for i := max(from, v.base); i < to; i++ {
		if !fn(i, v.mem[i-v.base]) {
			return nil
		}
	}
	return nil
}

// entry is log entry i.
func (v logView) entry(i int) (store.Entry, error) {
	if i >= v.base && i < v.size() {
		return v.mem[i-v.base], nil
	}
	var out store.Entry
	found := false
	err := v.each(i, i+1, func(_ int, e store.Entry) bool { out, found = e, true; return false })
	if err == nil && !found {
		err = fmt.Errorf("session: no log entry %d", i)
	}
	return out, err
}

// load reads entries from index from on, up to to but at most loadEntries or
// loadBytes of them, from the store.
func (v logView) load(from, to int) ([]store.Entry, error) {
	if v.st == nil {
		return nil, errors.New("session: no store")
	}
	start := mark{}
	if k := sort.Search(len(v.marks), func(j int) bool { return v.marks[j].idx > from }); k > 0 {
		start = v.marks[k-1]
	}
	var out []store.Entry
	var size int64
	i, prev := start.idx, start.off
	_, _, err := v.st.ScanEntries(v.id, start.off, start.idx, 0, func(e store.Entry, next int64) bool {
		if i >= from {
			v.fix.apply(i, &e)
			out = append(out, e)
			size += next - prev
		}
		prev = next
		i++
		return i < to && len(out) < loadEntries && size < loadBytes
	})
	if err == nil && len(out) == 0 {
		err = fmt.Errorf("session: log entry %d is not in the store", from)
	}
	return out, err
}

// entryLocked is log entry i, read from the store when it is no longer in
// memory; ok is false when it cannot be read. t.mu is held.
func (t *thread) entryLocked(i int) (store.Entry, bool) {
	if i >= t.base && i < t.size() {
		return t.log[i-t.base], true
	}
	e, err := t.view().entry(i)
	return e, err == nil
}

// ---- history ----

// History is a thread's log as the messages the live stream carried, for a
// view that needs to be rebuilt. It is rendered as it is read, a part of the
// log at a time, so a long thread is never held as messages all at once. The
// zero History has no messages.
type History struct {
	t *thread
	v logView
}

// history is the thread's log as it is now. t.mu is held.
func (t *thread) history() History { return History{t: t, v: t.view()} }

// All yields the messages in order. A part of the log that cannot be read
// back from the store ends them early.
func (h History) All() iter.Seq[tea.Msg] {
	return func(yield func(tea.Msg) bool) {
		if h.t == nil {
			return
		}
		_ = h.v.each(0, h.v.size(), func(_ int, e store.Entry) bool {
			var msg tea.Msg
			if e.Type == store.EntryRevert || e.Type == store.EntryUnrevert {
				h.t.mu.Lock()
				msg = h.t.revertMsgLocked(e)
				h.t.mu.Unlock()
			} else if msg = msgFor(h.t.id, e); msg == nil {
				return true
			}
			return yield(msg)
		})
	}
}

// Msgs is every message at once.
func (h History) Msgs() []tea.Msg {
	var out []tea.Msg
	for msg := range h.All() {
		out = append(out, msg)
	}
	return out
}
