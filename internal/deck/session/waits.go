package session

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/store"
)

// Waiting for a plan limit to reset.
//
// When a member's CLI says the account's plan ran out (agent.LimitHit on a
// KindError), the turn that ended is not a failure: the member waits
// (StatusWaiting) until the limit resets. Messages for it queue as they do
// for a busy member, and at the reset Deck resumes it with a nudge to carry
// on, then the queue. The person can resume it at once or cancel the wait,
// which drops what is queued for it. A wait whose reset is not known yet
// waits for the next limits report of its kind (Deck asks for one).
//
// The user can also schedule a message for later (the next reset): it waits
// in the thread, and is posted at its time as if the user wrote it then.
//
// Both are stored with the thread and survive a restart of Deck:
// RestoreWaits resumes the threads that hold any.

// resumeSlack is how long after the told reset Deck resumes a waiting
// member: the CLIs round reset times, and an early nudge would only hit the
// limit again.
var resumeSlack = 20 * time.Second

// resumeNudge is what a member is told when its wait ends.
const resumeNudge = "[deck] Your plan's usage limit has reset. Carry on with the task you were working on when you hit it; the messages below came in while you waited."

// waitState is a member's wait (store.Wait plus its timer).
type waitState struct {
	store.Wait
	timer *time.Timer
}

// WaitChangedMsg says a member started or stopped waiting for a limit, or a
// thread's scheduled messages changed.
type WaitChangedMsg struct {
	ThreadID string
	AgentID  string // "" for a scheduled message
}

// WaitInfo is a member waiting for its plan limit to reset.
type WaitInfo struct {
	ThreadID, AgentID, Name, Kind string
	store.Wait
}

// ScheduledInfo is a message waiting for its time.
type ScheduledInfo struct {
	ThreadID string
	store.Scheduled
}

// ---- noticing ----

// noteHit records that a's turn ran into the plan limit; the turn's end
// starts the wait (apply). The reset time is resolved now, from what the CLI
// said or from the kind's last limits report.
func (m *Manager) noteHit(a *agentState, hit agent.LimitHit) {
	hit.ResetsAt = m.resetFor(a.kind, hit, time.Now())
	a.mu.Lock()
	if a.hit == nil || hit.ResetsAt.After(a.hit.ResetsAt) {
		a.hit = &hit
	}
	a.mu.Unlock()
}

// resetFor is when kind's plan lets a hit member go on: the hit's own time,
// else the time of the window it names, else the latest reset of a window
// that is full. Zero when nothing says.
func (m *Manager) resetFor(kind string, hit agent.LimitHit, now time.Time) time.Time {
	if !hit.ResetsAt.IsZero() {
		return hit.ResetsAt
	}
	m.mu.Lock()
	l := m.limits[kind]
	m.mu.Unlock()
	var at time.Time
	for _, w := range l.Windows {
		if !w.ResetsAt.After(now) {
			continue
		}
		if hit.Window != "" && w.ID == hit.Window {
			return w.ResetsAt
		}
		if hit.Window == "" && w.UsedPct >= 99 && w.ResetsAt.After(at) {
			at = w.ResetsAt
		}
	}
	return at
}

// startedWait follows apply putting a in a wait: it arms the resume, tells
// the thread and stores the wait.
func (m *Manager) startedWait(t *thread, a *agentState) {
	a.mu.Lock()
	w := a.wait
	a.mu.Unlock()
	if w == nil {
		return
	}
	m.armWait(t, a)
	m.emit(NoticeMsg{ThreadID: t.id, Via: "limit", Text: waitNotice(a.getName(), w.Until, time.Now())})
	m.persistMeta(t)
	m.emit(WaitChangedMsg{ThreadID: t.id, AgentID: a.id})
	if w.Until.IsZero() {
		go m.refreshLimits(a.kind)
	}
}

func waitNotice(name string, until, now time.Time) string {
	if until.IsZero() {
		return fmt.Sprintf("%s ran out of its plan's usage; Deck holds its work until the limit resets (asking %s when that is)", name, name)
	}
	return fmt.Sprintf("%s ran out of its plan's usage; Deck holds its work until the limit resets at %s (in %s) and then resumes it", name, clock(until, now), humanDur(until.Sub(now)))
}

// clock is a time of day, with the date when it is not today.
func clock(t, now time.Time) string {
	t = t.Local()
	if y, m, d := t.Date(); y == now.Local().Year() && m == now.Local().Month() && d == now.Local().Day() {
		return t.Format("15:04")
	}
	return t.Format("Mon 2 Jan 15:04")
}

func humanDur(d time.Duration) string {
	d = d.Round(time.Minute)
	if d < time.Minute {
		return "less than a minute"
	}
	h, mins := int(d.Hours()), int(d.Minutes())%60
	switch {
	case h >= 48:
		return fmt.Sprintf("%dd %dh", h/24, h%24)
	case h > 0 && mins > 0:
		return fmt.Sprintf("%dh %dm", h, mins)
	case h > 0:
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dm", mins)
}

// armWait (re)starts the timer that ends a's wait at its reset. A wait with
// no known reset has none.
func (m *Manager) armWait(t *thread, a *agentState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w := a.wait
	if w == nil {
		return
	}
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	if w.Until.IsZero() {
		return
	}
	d := max(0, time.Until(w.Until)) + resumeSlack
	w.timer = time.AfterFunc(d, func() {
		m.onThread(t, func() { _ = m.endWait(t, a, w, true) })
	})
}

// rearmWaits gives the waits of kind with no known reset the one its plan
// now reports; called with each limits report.
func (m *Manager) rearmWaits(kind string) {
	now := time.Now()
	for _, wi := range m.Waits() {
		if wi.Kind != kind || !wi.Until.IsZero() {
			continue
		}
		t, a := m.thread(wi.ThreadID), m.get(wi.AgentID)
		if t == nil || a == nil {
			continue
		}
		until := m.resetFor(kind, agent.LimitHit{Window: wi.Window}, now)
		if until.IsZero() {
			continue
		}
		a.mu.Lock()
		if a.wait != nil {
			a.wait.Until = until
		}
		a.mu.Unlock()
		m.armWait(t, a)
		m.emit(NoticeMsg{ThreadID: t.id, Via: "limit", Text: fmt.Sprintf("%s's plan resets at %s (in %s); Deck resumes it then", a.getName(), clock(until, now), humanDur(until.Sub(now)))})
		m.persistMeta(t)
		m.emit(WaitChangedMsg{ThreadID: t.id, AgentID: a.id})
	}
}

// refreshLimits asks kind's CLI for its limits, for a wait that does not
// know its reset.
func (m *Manager) refreshLimits(kind string) {
	_, err := m.CheckLimits(m.ctx, kind)
	if err == nil {
		return
	}
	for _, wi := range m.Waits() {
		if wi.Kind == kind && wi.Until.IsZero() {
			m.emit(NoticeMsg{ThreadID: wi.ThreadID, Via: "limit", Text: fmt.Sprintf("could not ask %s when its limit resets (%v); resume %s by hand when it has", kind, err, wi.Name)})
		}
	}
}

// ---- ending ----

// endWait ends a's wait w (if it is still the current one): auto when its
// reset came, else the person resumed it. The member then gets the nudge and
// what was queued for it.
func (m *Manager) endWait(t *thread, a *agentState, w *waitState, auto bool) error {
	if !m.clearWait(a, w) {
		return errNotWaiting
	}
	name := a.getName()
	text := name + "'s plan limit reset; Deck resumed it"
	if !auto {
		text = "you resumed " + name + " before its plan limit reset"
	}
	m.emit(NoticeMsg{ThreadID: t.id, Via: "limit", Text: text})
	m.persistMeta(t)
	m.emit(WaitChangedMsg{ThreadID: t.id, AgentID: a.id})
	a.mu.Lock()
	a.nudge = resumeNudge
	a.mu.Unlock()
	d, err := m.deliverEx(t, a, false)
	if d != dStarted {
		a.mu.Lock()
		a.nudge = ""
		a.mu.Unlock()
	}
	return err
}

var errNotWaiting = errors.New("session: that agent is not waiting for a limit")

// clearWait drops a's wait w (any wait when w is nil) and makes it idle; it
// reports false when there was no such wait.
func (m *Manager) clearWait(a *agentState, w *waitState) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.wait == nil || w != nil && a.wait != w {
		return false
	}
	if a.wait.timer != nil {
		a.wait.timer.Stop()
	}
	a.wait = nil
	if a.base == StatusWaiting {
		a.base = StatusIdle
	}
	a.stirLocked()
	return true
}

// ResumeWait ends a member's wait now: the person thinks the limit is back
// (or wants to try). It gets the nudge and its queue at once.
func (m *Manager) ResumeWait(agentID string) error {
	a := m.get(agentID)
	if a == nil {
		return fmt.Errorf("session: no agent %q", agentID)
	}
	return m.endWait(a.thread, a, nil, false)
}

// CancelWait ends a member's wait without resuming it: what was queued for
// it is cancelled, and it stays idle until someone writes to it.
func (m *Manager) CancelWait(agentID string) error {
	a := m.get(agentID)
	if a == nil {
		return fmt.Errorf("session: no agent %q", agentID)
	}
	t := a.thread
	if !m.clearWait(a, nil) {
		return errNotWaiting
	}
	t.mu.Lock()
	var ids []string
	for _, q := range t.queue {
		if q.AgentID == a.id && !q.Steer {
			ids = append(ids, q.ID)
		}
	}
	t.mu.Unlock()
	for _, id := range ids {
		_ = m.CancelQueued(id)
	}
	m.emit(NoticeMsg{ThreadID: t.id, Via: "limit", Text: "you cancelled " + a.getName() + "'s wait for its plan limit"})
	m.persistMeta(t)
	m.emit(WaitChangedMsg{ThreadID: t.id, AgentID: a.id})
	return nil
}

// Waits lists the members waiting for a limit, in every live thread.
func (m *Manager) Waits() []WaitInfo {
	m.mu.Lock()
	as := append([]*agentState(nil), m.order...)
	m.mu.Unlock()
	var out []WaitInfo
	for _, a := range as {
		a.mu.Lock()
		w := a.wait
		var info WaitInfo
		if w != nil {
			info = WaitInfo{ThreadID: a.thread.id, AgentID: a.id, Name: a.name, Kind: a.kind, Wait: w.Wait}
		}
		a.mu.Unlock()
		if w != nil && !a.thread.gone.Load() {
			out = append(out, info)
		}
	}
	return out
}

// restoreWait puts a resumed member back in the wait it had when Deck
// stopped; one whose reset has passed resumes at once.
func (m *Manager) restoreWait(t *thread, a *agentState, w store.Wait) {
	a.mu.Lock()
	if a.base == StatusExited {
		a.mu.Unlock()
		return
	}
	a.wait = &waitState{Wait: w}
	a.base = StatusWaiting
	a.mu.Unlock()
	m.armWait(t, a)
	m.emit(WaitChangedMsg{ThreadID: t.id, AgentID: a.id})
	if w.Until.IsZero() {
		go m.refreshLimits(a.kind)
	}
}

// ---- scheduled messages ----

// ScheduleSend keeps text to be posted in the thread at at, as if the user
// wrote it then (addressed by its mentions, as Post reads them). It returns
// the scheduled message's id.
func (m *Manager) ScheduleSend(threadID, text string, at time.Time) (string, error) {
	t := m.thread(threadID)
	if t == nil {
		return "", fmt.Errorf("session: no thread %q", threadID)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("session: empty message")
	}
	if at.IsZero() {
		return "", errors.New("session: when to send it is not known")
	}
	s := store.Scheduled{ID: "at-" + store.NewID(), Text: text, At: at, Created: time.Now()}
	t.mu.Lock()
	t.scheduled = append(t.scheduled, s)
	t.mu.Unlock()
	m.armScheduled(t, s)
	m.persistMeta(t)
	m.emit(WaitChangedMsg{ThreadID: t.id})
	return s.ID, nil
}

func (m *Manager) armScheduled(t *thread, s store.Scheduled) {
	d := max(0, time.Until(s.At))
	timer := time.AfterFunc(d, func() {
		m.onThread(t, func() { _ = m.sendScheduled(t, s.ID) })
	})
	t.mu.Lock()
	if t.atTimers == nil {
		t.atTimers = map[string]*time.Timer{}
	}
	t.atTimers[s.ID] = timer
	t.mu.Unlock()
}

// takeScheduled removes the scheduled message id from t.
func (m *Manager) takeScheduled(t *thread, id string) (store.Scheduled, bool) {
	t.mu.Lock()
	i := slices.IndexFunc(t.scheduled, func(s store.Scheduled) bool { return s.ID == id })
	if i < 0 {
		t.mu.Unlock()
		return store.Scheduled{}, false
	}
	s := t.scheduled[i]
	t.scheduled = slices.Delete(t.scheduled, i, i+1)
	if timer := t.atTimers[id]; timer != nil {
		timer.Stop()
		delete(t.atTimers, id)
	}
	t.mu.Unlock()
	m.persistMeta(t)
	m.emit(WaitChangedMsg{ThreadID: t.id})
	return s, true
}

func (m *Manager) sendScheduled(t *thread, id string) error {
	s, ok := m.takeScheduled(t, id)
	if !ok || t.gone.Load() {
		return nil
	}
	_, err := m.PostWith(t.id, s.Text, nil)
	if err != nil {
		m.emit(NoticeMsg{ThreadID: t.id, Text: "the message scheduled for " + clock(s.At, time.Now()) + " could not be sent: " + err.Error()})
	}
	return err
}

// CancelScheduled drops a scheduled message, or with now sends it at once.
func (m *Manager) CancelScheduled(threadID, id string, now bool) error {
	t := m.thread(threadID)
	if t == nil {
		return fmt.Errorf("session: no thread %q", threadID)
	}
	if now {
		t.mu.Lock()
		found := slices.ContainsFunc(t.scheduled, func(s store.Scheduled) bool { return s.ID == id })
		t.mu.Unlock()
		if !found {
			return fmt.Errorf("session: no scheduled message %q", id)
		}
		return m.sendScheduled(t, id)
	}
	if _, ok := m.takeScheduled(t, id); !ok {
		return fmt.Errorf("session: no scheduled message %q", id)
	}
	return nil
}

// Scheduled lists the scheduled messages of every live thread, soonest first.
func (m *Manager) Scheduled() []ScheduledInfo {
	m.mu.Lock()
	ts := append([]*thread(nil), m.torder...)
	m.mu.Unlock()
	var out []ScheduledInfo
	for _, t := range ts {
		if t.gone.Load() {
			continue
		}
		t.mu.Lock()
		for _, s := range t.scheduled {
			out = append(out, ScheduledInfo{ThreadID: t.id, Scheduled: s})
		}
		t.mu.Unlock()
	}
	slices.SortStableFunc(out, func(a, b ScheduledInfo) int { return a.At.Compare(b.At) })
	return out
}

// stopTimers stops t's wait and schedule timers (the thread is leaving the
// live set; a resume arms them again from the store).
func (t *thread) stopTimers() {
	t.mu.Lock()
	for id, timer := range t.atTimers {
		timer.Stop()
		delete(t.atTimers, id)
	}
	members := append([]*agentState(nil), t.members...)
	t.mu.Unlock()
	for _, a := range members {
		a.mu.Lock()
		if a.wait != nil && a.wait.timer != nil {
			a.wait.timer.Stop()
			a.wait.timer = nil
		}
		a.mu.Unlock()
	}
}

// ---- after a restart ----

// RestoreWaits resumes the stored threads that hold a wait or a scheduled
// message, so their timers run again (and a reset that passed while Deck
// was stopped resumes its member now). Archived threads are left alone.
func (m *Manager) RestoreWaits() {
	if m.st == nil {
		return
	}
	for _, th := range m.Saved() {
		if th.Archived || m.thread(th.ID) != nil {
			continue
		}
		holds := len(th.Scheduled) > 0
		for _, mem := range th.Members {
			holds = holds || mem.Wait != nil && !mem.Ephemeral
		}
		if !holds {
			continue
		}
		if _, _, err := m.Resume(th.ID); err != nil && !m.isClosed() {
			m.emit(NoticeMsg{ThreadID: th.ID, Text: "could not resume this thread's wait for a plan limit: " + err.Error()})
		}
	}
}
