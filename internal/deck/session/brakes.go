package session

import (
	"fmt"
	"maps"
	"sort"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/store"
)

// Brakes on what agents set going without the user (docs/multiplayer-design.md
// §7):
//
//   - Work. Every subagent a member starts and every task the scheduler hands
//     out counts against MaxWork; at the limit the thread pauses, as it does
//     after MaxHops relays, until the user writes or continues it.
//   - Budgets. A task run by subagents may spend Config.Budget (its own
//     Budget wins field by field): tokens and reported cost, by its owner and
//     the owner's subagents, and minutes since it started. Over budget, the
//     task is blocked and its owner and their subagents interrupted.
//   - Turns. Every turn that starts without the user (a relay, a hand-off,
//     a task's brief or report, a review, a retry) counts against the
//     level's MaxTurns; at the limit the thread pauses, and while it is
//     paused no such turn starts. The user's next message or Continue
//     resets the count and wakes whoever waits. This is the one ledger the
//     hop and work brakes cannot be: a report-back or a brief is neither a
//     relay nor new work, but each starts a turn.
//   - Stalls. Those turns also pause the thread early, at the level's
//     MaxStalls, when they stop making progress (stall.go).
//   - Loop guards. One agent may thread.send at most maxSendsPerMinute times
//     a minute, and a member with maxWaiting agent messages it has not read
//     takes no more.
//   - Routing. A task that names no kind goes to the default agent, unless one
//     of that CLI's plan windows is RouteAbovePct full: then to the installed
//     kind with the most room.

const (
	maxSendsPerMinute = 12
	maxWaiting        = 8
)

// errPaused is what a spawn is told while the thread is paused.
var errPaused = fmt.Errorf("held: Deck paused this thread; the user will continue it")

// takeWork counts one unit of work against the thread's MaxWork. It reports
// false, and starts nothing, while the thread is paused; reaching the limit
// pauses it.
func (m *Manager) takeWork(t *thread) bool {
	t.mu.Lock()
	if t.paused {
		t.mu.Unlock()
		return false
	}
	if t.work >= m.levelLocked(t).MaxWork {
		t.paused = true
		pe := store.Entry{Type: store.EntryPause, Event: eventText(fmt.Sprintf("%d subagents and handed-out tasks", t.work))}
		m.appendLocked(t, pe)
		t.mu.Unlock()
		m.emitAll(msgFor(t.id, pe))
		return false
	}
	t.work++
	t.mu.Unlock()
	return true
}

// userTriggered reports whether a message from the user (or a teammate)
// waits for a: its turn is theirs, not one the agents started. t.mu is held.
func (t *thread) userTriggered(a *agentState) bool {
	for _, h := range t.index.since(a.cursor) {
		if (h.typ == store.EntryUser || h.typ == store.EntryEdit) && !t.inert[h.i] && t.skip[skipKey{h.i, a.id}] == 0 && contains(h.to, a.id) {
			return true
		}
	}
	return false
}

// chargeTurnLocked counts one turn started without the user. It reports
// false while the thread is paused, and when the turn would pass the
// level's MaxTurns, which pauses it; pe is the pause to announce then.
// t.mu is held.
func (m *Manager) chargeTurnLocked(t *thread) (pe *store.Entry, ok bool) {
	if t.paused {
		return nil, false
	}
	if limit := m.levelLocked(t).MaxTurns; limit > 0 && t.turns >= limit {
		t.paused = true
		e := store.Entry{Type: store.EntryPause, Event: eventText(fmt.Sprintf("%d turns the agents started without you", t.turns))}
		m.appendLocked(t, e)
		return &e, false
	}
	t.turns++
	return nil, true
}

// wakeAll starts a turn for every live member with messages waiting: after
// a pause ends, the turns it held back go ahead.
func (m *Manager) wakeAll(t *thread) error {
	t.mu.Lock()
	live := t.live()
	t.mu.Unlock()
	var first error
	for _, a := range live {
		if _, err := m.deliver(t, a); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// ---- budgets ----

// budgetOf is the budget task tk runs under: its own, over the config's for a
// task run by a Deck subagent. t.mu is held.
func (m *Manager) budgetOf(tk store.Task) store.Budget {
	var b store.Budget
	if tk.Run != nil {
		d := m.config().Budget
		b = store.Budget{Tokens: d.Tokens, CostUSD: d.CostUSD, Minutes: d.Minutes}
	}
	if o := tk.Budget; o != nil {
		if o.Tokens > 0 {
			b.Tokens = o.Tokens
		}
		if o.CostUSD > 0 {
			b.CostUSD = o.CostUSD
		}
		if o.Minutes > 0 {
			b.Minutes = o.Minutes
		}
	}
	return b
}

// chargedTasks are the indexes of the tasks a's spending counts toward: the
// running, in review or blocked tasks a owns, the task a reviews, and those
// of every member above it. t.mu is held.
func (t *thread) chargedTasks(a *agentState) []int {
	var out []int
	seen := map[string]bool{}
	for i := 0; a != nil && i < 64 && !seen[a.id]; i++ {
		seen[a.id] = true
		for j, tk := range t.tasks {
			if tk.Owner == a.id && tk.Status.Active() || tk.Reviewer == a.id {
				out = append(out, j)
			}
		}
		if !a.isEphemeral() {
			break
		}
		a = t.member(a.parentID())
	}
	return out
}

// charge adds what a's finished turn spent to the tasks it counts toward,
// then checks their budgets.
func (m *Manager) charge(t *thread, a *agentState, s store.Spend) {
	if s == (store.Spend{}) || t.gone.Load() {
		return
	}
	t.mu.Lock()
	for _, i := range t.chargedTasks(a) {
		t.tasks[i].Spent.Tokens += s.Tokens
		t.tasks[i].Spent.CostUSD += s.CostUSD
	}
	t.mu.Unlock()
	m.persistMeta(t)
	m.checkBudgets(t, a, store.Spend{})
}

// checkBudgets blocks the running or reviewed tasks a counts toward that are
// over budget, counting extra (a's turn so far) as spent, or every such task
// of the thread when a is nil.
func (m *Manager) checkBudgets(t *thread, a *agentState, extra store.Spend) {
	if t.gone.Load() || m.isClosed() {
		return
	}
	type over struct{ id, owner, note string }
	var overs []over
	now := time.Now()
	t.mu.Lock()
	var idx []int
	if a != nil {
		idx = t.chargedTasks(a)
	} else {
		for i := range t.tasks {
			idx = append(idx, i)
		}
	}
	for _, i := range idx {
		tk := t.tasks[i]
		if tk.Status != store.TaskRunning && tk.Status != store.TaskReview || tk.Remote != "" { // a teammate's Deck keeps its own budget
			continue
		}
		if note := overBudget(m.budgetOf(tk), tk.Spent, extra, tk.Started, now); note != "" {
			overs = append(overs, over{tk.ID, tk.Owner, note})
		}
	}
	t.mu.Unlock()
	for _, o := range overs {
		if _, err := m.moveTask(t, o.id, store.TaskBlocked, "", o.note+"; the user can retry it", nil); err != nil {
			continue
		}
		m.haltTree(t, o.owner)
	}
}

// overBudget says how spent (plus extra) and the time since started go past
// b, or "" when they do not.
func overBudget(b store.Budget, spent, extra store.Spend, started, now time.Time) string {
	switch tokens, cost := spent.Tokens+extra.Tokens, spent.CostUSD+extra.CostUSD; {
	case b.Tokens > 0 && tokens > b.Tokens:
		return fmt.Sprintf("over its budget: %s of %s tokens", kTokens(tokens), kTokens(b.Tokens))
	case b.CostUSD > 0 && cost > b.CostUSD:
		return fmt.Sprintf("over its budget: $%.2f of $%.2f", cost, b.CostUSD)
	case b.Minutes > 0 && !started.IsZero() && now.Sub(started) > time.Duration(b.Minutes)*time.Minute:
		return fmt.Sprintf("over its budget: ran %d of %d minutes", int(now.Sub(started).Minutes()), b.Minutes)
	}
	return ""
}

func kTokens(n int) string {
	if n >= 10_000 {
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprint(n)
}

// haltTree interrupts member id and every live subagent below it: their
// turns stop, nothing is cancelled, and the user decides what next.
func (m *Manager) haltTree(t *thread, id string) {
	t.mu.Lock()
	var ids []string
	queue := []string{id}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		ids = append(ids, cur)
		for _, k := range t.children(cur) {
			queue = append(queue, k.id)
		}
	}
	t.mu.Unlock()
	for _, id := range ids {
		_ = m.Interrupt(id)
	}
}

// ---- loop guards ----

// guardSend checks a thread.send from from to targets against the loop
// guards and returns the targets that take it. t.mu is held.
func (t *thread) guardSend(from *agentState, targets []*agentState, now time.Time) ([]*agentState, error) {
	recent := t.sends[from.id][:0]
	for _, at := range t.sends[from.id] {
		if now.Sub(at) < time.Minute {
			recent = append(recent, at)
		}
	}
	if t.sends == nil {
		t.sends = map[string][]time.Time{}
	}
	t.sends[from.id] = recent
	if len(recent) >= maxSendsPerMinute {
		return nil, fmt.Errorf("slow down: you sent %d messages in the last minute; wait for answers before sending more", len(recent))
	}
	var ok, full []*agentState
	for _, a := range targets {
		if t.unread(a) >= maxWaiting {
			full = append(full, a)
			continue
		}
		ok = append(ok, a)
	}
	if len(ok) == 0 {
		var names []string
		for _, a := range full {
			names = append(names, a.getName())
		}
		return nil, fmt.Errorf("%s already has %d or more messages from agents it has not read; wait until it catches up", strings.Join(names, ", "), maxWaiting)
	}
	t.sends[from.id] = append(t.sends[from.id], now)
	return ok, nil
}

// unread counts the agent messages addressed to a that it has not been given.
// t.mu is held.
func (t *thread) unread(a *agentState) int {
	n := 0
	for _, h := range t.index.since(a.cursor) {
		if h.typ == store.EntryRelay && !t.inert[h.i] && contains(h.to, a.id) {
			n++
		}
	}
	return n
}

// ---- routing by plan limits ----

func (m *Manager) routeAbovePct() float64 {
	if n := m.config().RouteAbovePct; n > 0 {
		return float64(n)
	}
	return config.DefaultRouteAbovePct
}

// setLimits records the plan limits a CLI of kind reported.
func (m *Manager) setLimits(kind string, l agent.Limits) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.limits == nil {
		m.limits = map[string]agent.Limits{}
	}
	m.limits[kind] = l
}

// PlanLimits are the plan limits each kind's CLI last reported.
func (m *Manager) PlanLimits() map[string]agent.Limits {
	m.mu.Lock()
	defer m.mu.Unlock()
	return maps.Clone(m.limits)
}

// fullest is how full the fullest current window of kind's plan is, and that
// window's label; ok is false when kind reported none.
func (m *Manager) fullest(kind string, now time.Time) (pct float64, label string, ok bool) {
	m.mu.Lock()
	l, known := m.limits[kind]
	m.mu.Unlock()
	if !known {
		return 0, "", false
	}
	for _, w := range l.Windows {
		if !w.ResetsAt.IsZero() && w.ResetsAt.Before(now) {
			continue // it has reset since it was reported
		}
		if !ok || w.UsedPct > pct {
			pct, label, ok = w.UsedPct, w.Label, true
		}
	}
	return pct, label, ok
}

// routeKind is the kind a task that names none runs: def, unless def's plan
// is RouteAbovePct full, then the installed kind with the most room (kinds
// that report no limits after those that report room). note says why when
// it is not def.
func (m *Manager) routeKind(def string) (kind, note string) {
	limit := m.routeAbovePct()
	now := time.Now()
	pct, label, ok := m.fullest(def, now)
	if limit >= 100 || !ok || pct < limit {
		return def, ""
	}
	type cand struct {
		kind  string
		pct   float64
		known bool
	}
	var cands []cand
	for _, k := range m.Kinds() {
		if k == def {
			continue
		}
		p, _, known := m.fullest(k, now)
		if known && p >= limit {
			continue
		}
		cands = append(cands, cand{k, p, known})
	}
	if len(cands) == 0 {
		return def, ""
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].known != cands[j].known {
			return cands[i].known
		}
		return cands[i].pct < cands[j].pct
	})
	return cands[0].kind, fmt.Sprintf("%s's %s window is %.0f%% used, so %s took this task", def, label, pct, cands[0].kind)
}

// notice logs a note from Deck in the thread, which the members' briefings
// and the user both see.
func (m *Manager) notice(t *thread, text string) {
	e := store.Entry{Type: store.EntryNotice, Event: eventText(text)}
	t.mu.Lock()
	m.appendLocked(t, e)
	t.mu.Unlock()
	m.emit(msgFor(t.id, e))
}
