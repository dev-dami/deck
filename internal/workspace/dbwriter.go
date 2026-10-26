package workspace

import (
	"sync"
	"time"
)

// dbQueue is how many writes the pump can hand off before it waits.
const dbQueue = 256

// dbWriter runs message-store writes the pump hands off, one at a time and in
// the order they were queued, on a goroutine of its own, so a slow insert holds
// up this queue rather than the pump that drains every agent's events. It opens
// no connection of its own: the store's single connection stays the only
// writer. The queue is bounded; when it is full the pump waits, as it would
// have for the write itself.
type dbWriter struct {
	jobs    chan dbJob
	mu      sync.Mutex
	pending map[string]int // queued writes by session
}

type dbJob struct {
	sid string // "" for a flush marker
	fn  func()
}

func newDBWriter() *dbWriter {
	d := &dbWriter{jobs: make(chan dbJob, dbQueue), pending: map[string]int{}}
	go d.run()
	return d
}

func (d *dbWriter) run() {
	for j := range d.jobs {
		j.fn()
		if j.sid == "" {
			continue
		}
		d.mu.Lock()
		if d.pending[j.sid]--; d.pending[j.sid] <= 0 {
			delete(d.pending, j.sid)
		}
		d.mu.Unlock()
	}
}

// do queues fn, a write to session sid. A nil writer (a workspace not made by
// NewDeckWorkspace) runs it now.
func (d *dbWriter) do(sid string, fn func()) {
	if d == nil {
		fn()
		return
	}
	d.mu.Lock()
	d.pending[sid]++
	d.mu.Unlock()
	d.jobs <- dbJob{sid: sid, fn: fn}
}

// wait returns once every write queued for sid so far has run, so a row the
// caller then writes for sid itself lands after them. It returns at once when
// none is queued.
func (d *dbWriter) wait(sid string) {
	if d == nil {
		return
	}
	d.mu.Lock()
	n := d.pending[sid]
	d.mu.Unlock()
	if n > 0 {
		d.flush(0)
	}
}

// flush returns once every write queued so far has run, or after timeout
// (none when 0). It reports whether they all ran.
func (d *dbWriter) flush(timeout time.Duration) bool {
	if d == nil {
		return true
	}
	var expired <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		expired = t.C
	}
	done := make(chan struct{})
	select {
	case d.jobs <- dbJob{fn: func() { close(done) }}:
	case <-expired:
		return false
	}
	select {
	case <-done:
		return true
	case <-expired:
		return false
	}
}
