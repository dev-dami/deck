package session

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/store"
)

// The scheduler runs a thread's task board when orchestration is on
// (SetOrchestrate). Whenever the board changes, or a member's turn ends, it
// does three things in order:
//
//  1. Failure propagation: tasks waiting on one that failed for good (out of
//     attempts, or cancelled) are blocked or cancelled, or let through,
//     according to the failed task's OnFail.
//  2. Retries: a failed task with attempts left is put back to ready after a
//     backoff, with the failure added to its brief. A task whose subagent was
//     lost gets a fresh one.
//  3. Dispatch: ready tasks, oldest first, are handed to their owner or to a
//     new subagent, while fewer than MaxParallel tasks are running.
//
// Dispatches are Deck's, not an agent's thread.send, so they are not hops.

// retryBackoff is the wait before a failed task's attempt number attempt+1:
// 2s, 4s, 8s, ... up to a minute. A variable so tests can shorten it.
var retryBackoff = func(attempt int) time.Duration {
	d := 2 * time.Second << max(0, attempt-1)
	return min(d, time.Minute)
}

// schedState keeps one scheduler pass at a time per thread: a kick during a
// pass asks for another one when it ends.
type schedState struct {
	mu      sync.Mutex
	running bool
	again   bool
	// retry wakes the scheduler when the next backoff ends; one per thread,
	// reset by each pass, so kicks during a backoff do not pile timers up.
	retry *time.Timer
}

// kick has the scheduler look at the thread's board again, soon and off the
// caller's goroutine. It does nothing when orchestration is off.
func (m *Manager) kick(t *thread) {
	if m.isClosed() || t.gone.Load() {
		return
	}
	t.mu.Lock()
	on := t.orchestrate
	t.mu.Unlock()
	if !on {
		return
	}
	t.sched.mu.Lock()
	if t.sched.running {
		t.sched.again = true
		t.sched.mu.Unlock()
		return
	}
	t.sched.running = true
	t.sched.mu.Unlock()
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		for {
			m.schedule(t)
			t.sched.mu.Lock()
			if !t.sched.again {
				t.sched.running = false
				t.sched.mu.Unlock()
				return
			}
			t.sched.again = false
			t.sched.mu.Unlock()
		}
	}()
}

func (m *Manager) maxParallel() int {
	if n := m.config().MaxParallel; n > 0 {
		return n
	}
	return config.DefaultMaxParallel
}

// schedule is one pass over the board.
func (m *Manager) schedule(t *thread) {
	if m.isClosed() || t.gone.Load() {
		return
	}
	m.checkBudgets(t, nil, store.Spend{}) // minutes pass without events
	m.propagateFailures(t)
	m.retryFailed(t)
	m.dispatchReady(t)
}

// exhausted reports whether a failed task is out of attempts.
func exhausted(tk store.Task) bool { return tk.Attempts >= tk.Tries() }

// lostForGood reports whether tk will not be done without someone stepping in:
// cancelled, or failed with no attempt left.
func lostForGood(tk store.Task) bool {
	return tk.Status == store.TaskCancelled || tk.Status == store.TaskFailed && exhausted(tk)
}

func (m *Manager) propagateFailures(t *thread) {
	type act struct {
		id, by, note string
		cancel       bool
	}
	var acts []act
	t.mu.Lock()
	for _, tk := range t.tasks {
		if tk.Status != store.TaskPending && tk.Status != store.TaskReady {
			continue
		}
		for _, d := range tk.Deps {
			j := store.FindTask(t.tasks, d)
			if j < 0 || !lostForGood(t.tasks[j]) {
				continue
			}
			dep := t.tasks[j]
			if dep.Status == store.TaskFailed && dep.FailPolicy() == store.OnFailContinue {
				continue // let through: refreshReady counts it as satisfied
			}
			note := fmt.Sprintf("%s %s", dep.ID, dep.Status)
			if dep.Error != "" && dep.Status == store.TaskFailed {
				note += ": " + clipText(oneLine(dep.Error), 160)
			}
			acts = append(acts, act{id: tk.ID, by: dep.ID, note: note, cancel: dep.FailPolicy() == store.OnFailCancel})
			break
		}
	}
	t.mu.Unlock()
	for _, a := range acts {
		if a.cancel {
			_, _ = m.moveTask(t, a.id, store.TaskCancelled, "", "dependency "+a.note, nil)
			continue
		}
		by := a.by
		_, _ = m.moveTask(t, a.id, store.TaskBlocked, "", "waits on "+a.note, func(tk *store.Task) { tk.BlockedBy = by })
	}
}

func (m *Manager) retryFailed(t *thread) {
	now := time.Now()
	var due []string
	var wait time.Duration
	changed := false
	t.mu.Lock()
	for i := range t.tasks {
		tk := &t.tasks[i]
		if tk.Status != store.TaskFailed || exhausted(*tk) {
			continue
		}
		switch {
		case tk.RetryAt.IsZero():
			tk.RetryAt = now.Add(retryBackoff(tk.Attempts))
			tk.Updated = now
			changed = true
			fallthrough
		case now.Before(tk.RetryAt):
			if d := tk.RetryAt.Sub(now); wait == 0 || d < wait {
				wait = d
			}
		default:
			due = append(due, tk.ID)
		}
	}
	t.mu.Unlock()
	if changed {
		m.persistMeta(t)
	}
	if wait > 0 {
		t.sched.mu.Lock()
		if t.sched.retry != nil {
			t.sched.retry.Stop()
		}
		t.sched.retry = time.AfterFunc(wait+10*time.Millisecond, func() { m.kick(t) })
		t.sched.mu.Unlock()
	}
	for _, id := range due {
		var old *store.RunRef
		_, _ = m.moveTask(t, id, store.TaskReady, "", "retrying", func(tk *store.Task) {
			old = tk.Run
			note := tk.Error
			if note == "" {
				note = "it failed"
			}
			base := tk.Brief
			if base == "" {
				base = tk.Title
			}
			tk.Brief = clipText(base+fmt.Sprintf("\n\n[deck] Attempt %d of %d failed: %s. Try again, and avoid that failure.",
				tk.Attempts, tk.Tries(), clipText(oneLine(note), 300)), maxTaskText)
			tk.RetryAt = time.Time{}
			if tk.Run != nil { // a new subagent takes the retry
				tk.Owner, tk.Run = "", nil
			}
		})
		if old != nil {
			m.retireMember(t, old.Member)
		}
	}
}

// errChildLimit is Spawn's refusal when a parent has too many live subagents.
var errChildLimit = errors.New("too many subagents")

func (m *Manager) dispatchReady(t *thread) {
	limit := m.maxParallel()
	for {
		t.mu.Lock()
		running := 0
		var next *store.Task
		for i := range t.tasks {
			switch t.tasks[i].Status {
			case store.TaskRunning:
				running++
			case store.TaskReady:
				if next == nil {
					tk := cloneTask(t.tasks[i])
					next = &tk
				}
			}
		}
		var owner *agentState
		if next != nil && next.Owner != "" {
			if o := t.member(next.Owner); o != nil && o.status() != StatusExited {
				owner = o
			}
		}
		full := t.liveSubagents() >= m.maxSubagents()
		t.mu.Unlock()
		if next == nil || running >= limit || owner == nil && full {
			return // a subagent stopping kicks the scheduler again
		}
		if !m.takeWork(t) {
			return // paused; Continue or the user's next message kicks it again
		}
		if err := m.dispatch(t, *next, owner); err != nil {
			// Spawn fails the task itself once it has claimed it. A task that
			// did not get that far counts the attempt here, so it cannot loop
			// forever: the next passes retry it with a backoff.
			if cur, ok := m.Task(t.id, next.ID); ok && cur.Status == store.TaskReady {
				_, _ = m.moveTask(t, next.ID, store.TaskRunning, "", "", nil)
				_, _ = m.moveTask(t, next.ID, store.TaskFailed, "", "could not start it: "+err.Error(), nil)
			}
			continue
		}
		if cur, ok := m.Task(t.id, next.ID); !ok || cur.Status == store.TaskReady {
			return // the dispatch did not take hold: do not hand it out again and again
		}
	}
}

// dispatch hands one ready task to its live owner, or to a new subagent.
func (m *Manager) dispatch(t *thread, tk store.Task, owner *agentState) error {
	brief := tk.Brief
	if brief == "" {
		brief = tk.Title
	}
	if owner != nil {
		t.mu.Lock()
		if i := store.FindTask(t.tasks, tk.ID); i >= 0 && t.tasks[i].Contract == nil {
			t.tasks[i].Contract = &store.Contract{}
		}
		t.mu.Unlock()
		m.startHandedTask(t, tk.ID, owner, "")
		m.deliverBrief(t, deckSpeaker, owner, brief, tk.ID)
		return nil
	}
	_, err := m.Spawn(t.id, "", SpawnSpec{TaskID: tk.ID, Brief: brief, Isolate: tk.Isolate})
	return err
}

// failOwned fails the running tasks whose owner has just exited.
func (m *Manager) failOwned(t *thread, a *agentState) {
	if m.isClosed() || t.gone.Load() {
		return
	}
	var ids []string
	t.mu.Lock()
	for _, tk := range t.tasks {
		if tk.Owner == a.id && tk.Status == store.TaskRunning {
			ids = append(ids, tk.ID)
		}
	}
	name := a.getName()
	t.mu.Unlock()
	for _, id := range ids {
		_, _ = m.moveTask(t, id, store.TaskFailed, "", name+" exited before finishing it", nil)
	}
}
