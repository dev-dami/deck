package session

import (
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
)

// limitOut is a turn that runs into the plan limit, resetting at until.
func limitOut(until time.Time) fake.Script {
	return turn(
		say("starting on it"),
		fake.Step{Event: agent.Event{Kind: agent.KindError, Text: "5-hour limit reached", LimitHit: &agent.LimitHit{Window: "five_hour", ResetsAt: until}}},
		finish(),
	)
}

func quickResume(t *testing.T) {
	old := resumeSlack
	resumeSlack = 0
	t.Cleanup(func() { resumeSlack = old })
}

func status(m *Manager, id string) Status {
	a, _ := m.Agent(id)
	return a.Status
}

func lastPrompt(s *spyAdapter, id string) string {
	ps := s.prompts(id)
	if len(ps) == 0 {
		return ""
	}
	return ps[len(ps)-1]
}

func TestLimitHitWaitsQueuesAndResumesAtReset(t *testing.T) {
	quickResume(t)
	until := time.Now().Add(300 * time.Millisecond)
	r := newRig(t, []fake.Script{limitOut(until), turn(say("back at it"), finish())}, nil)
	tid := r.thread(t)
	id := r.add(t, tid, "claude")
	if err := r.m.Post(tid, "refactor the store"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the wait", func() bool { return status(r.m, id) == StatusWaiting })
	ws := r.m.Waits()
	if len(ws) != 1 || ws[0].AgentID != id || !ws[0].Until.Equal(until) || ws[0].Window != "five_hour" {
		t.Fatalf("waits %+v", ws)
	}

	// A message meanwhile queues instead of starting a turn.
	if err := r.m.Post(tid, "also rename it"); err != nil {
		t.Fatal(err)
	}
	if q := r.m.Queue(tid); len(q) != 1 || q[0].Text != "also rename it" {
		t.Fatalf("queue %+v", q)
	}
	if n := len(r.claude.prompts(id)); n != 1 {
		t.Fatalf("prompts while waiting = %d", n)
	}

	// At the reset it resumes with the nudge and what was queued.
	waitFor(t, "the resume", func() bool { return len(r.claude.prompts(id)) == 2 })
	p := lastPrompt(r.claude, id)
	has(t, p, resumeNudge, "also rename it")
	waitFor(t, "idle", func() bool { return status(r.m, id) == StatusIdle })
	if len(r.m.Waits()) != 0 || len(r.m.Queue(tid)) != 0 {
		t.Errorf("after resume: waits %v queue %v", r.m.Waits(), r.m.Queue(tid))
	}
}

func TestResumeWaitNowAndCancelWait(t *testing.T) {
	later := time.Now().Add(time.Hour)
	r := newRig(t, []fake.Script{limitOut(later), turn(say("resumed"), finish()), limitOut(later)}, nil)
	tid := r.thread(t)
	id := r.add(t, tid, "claude")
	_ = r.m.Post(tid, "one")
	waitFor(t, "the wait", func() bool { return status(r.m, id) == StatusWaiting })

	if err := r.m.ResumeWait(id); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the nudge", func() bool { return len(r.claude.prompts(id)) == 2 })
	has(t, lastPrompt(r.claude, id), resumeNudge)
	if err := r.m.ResumeWait(id); err == nil {
		t.Error("resuming an agent that does not wait succeeded")
	}

	// It runs out again; this time the person cancels the wait, and what was
	// queued for it with it.
	waitFor(t, "idle", func() bool { return status(r.m, id) == StatusIdle })
	_ = r.m.Post(tid, "two")
	waitFor(t, "the second wait", func() bool { return status(r.m, id) == StatusWaiting })
	_ = r.m.Post(tid, "three")
	if len(r.m.Queue(tid)) != 1 {
		t.Fatalf("queue %v", r.m.Queue(tid))
	}
	if err := r.m.CancelWait(id); err != nil {
		t.Fatal(err)
	}
	if s := status(r.m, id); s != StatusIdle {
		t.Errorf("status after cancel %s", s)
	}
	if q := r.m.Queue(tid); len(q) != 0 {
		t.Errorf("queue after cancel %v", q)
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(r.claude.prompts(id)); n != 3 {
		t.Errorf("prompts after cancel = %d, want 3 (nothing delivered)", n)
	}
}

func TestWaitWithoutResetTakesTheFullWindowsReset(t *testing.T) {
	reset := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	limits := fake.Step{Event: agent.Event{Kind: agent.KindLimits, Limits: &agent.Limits{Windows: []agent.LimitWindow{
		{ID: "five_hour", Kind: agent.WindowSession, UsedPct: 100, ResetsAt: reset},
		{ID: "seven_day", Kind: agent.WindowWeekly, UsedPct: 40, ResetsAt: reset.Add(72 * time.Hour)},
	}}}}
	r := newRig(t, []fake.Script{turn(limits,
		fake.Step{Event: agent.Event{Kind: agent.KindError, Text: "You've hit your usage limit."}}, // no time, no structure
		finish())}, nil)
	tid := r.thread(t)
	id := r.add(t, tid, "claude")
	_ = r.m.Post(tid, "go")
	waitFor(t, "the wait", func() bool { return status(r.m, id) == StatusWaiting })
	if ws := r.m.Waits(); len(ws) != 1 || !ws[0].Until.Equal(reset) {
		t.Fatalf("waits %+v", ws)
	}
}

func TestWaitSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	later := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	r := newRigIn(t, dir, []fake.Script{limitOut(later)}, nil)
	tid := r.thread(t)
	id := r.add(t, tid, "claude")
	_ = r.m.Post(tid, "go")
	waitFor(t, "the wait", func() bool { return status(r.m, id) == StatusWaiting })
	at := time.Now().Add(5 * time.Hour).Truncate(time.Second)
	sid, err := r.m.ScheduleSend(tid, "carry on with the docs", at)
	if err != nil {
		t.Fatal(err)
	}
	r.m.Close()

	r2 := newRigIn(t, dir, nil, nil)
	r2.m.RestoreWaits()
	ws := r2.m.Waits()
	if len(ws) != 1 || ws[0].ThreadID != tid || !ws[0].Until.Equal(later) || ws[0].Window != "five_hour" {
		t.Fatalf("waits after restart %+v", ws)
	}
	if s := status(r2.m, ws[0].AgentID); s != StatusWaiting {
		t.Errorf("status after restart %s", s)
	}
	sc := r2.m.Scheduled()
	if len(sc) != 1 || sc[0].ID != sid || !sc[0].At.Equal(at) || sc[0].Text != "carry on with the docs" {
		t.Fatalf("scheduled after restart %+v", sc)
	}
	if err := r2.m.CancelScheduled(tid, sid, false); err != nil {
		t.Fatal(err)
	}
	if len(r2.m.Scheduled()) != 0 {
		t.Error("scheduled message not cancelled")
	}
}

func TestWaitWhoseResetPassedWhileStoppedResumesOnRestore(t *testing.T) {
	quickResume(t)
	dir := t.TempDir()
	soon := time.Now().Add(400 * time.Millisecond)
	r := newRigIn(t, dir, []fake.Script{limitOut(soon.Add(time.Hour))}, nil)
	tid := r.thread(t)
	id := r.add(t, tid, "claude")
	_ = r.m.Post(tid, "go")
	waitFor(t, "the wait", func() bool { return status(r.m, id) == StatusWaiting })
	// Bring the reset forward, as a later report would, then stop Deck.
	a := r.m.get(id)
	a.mu.Lock()
	a.wait.Until = soon
	a.mu.Unlock()
	r.m.persistMeta(r.m.thread(tid))
	r.m.Close()
	time.Sleep(time.Until(soon) + 50*time.Millisecond)

	r2 := newRigIn(t, dir, []fake.Script{turn(say("resumed"), finish())}, nil)
	r2.m.RestoreWaits()
	waitFor(t, "the resume", func() bool {
		for _, ai := range r2.m.Agents() {
			if strings.Contains(lastPrompt(r2.claude, ai.ID), resumeNudge) {
				return true
			}
		}
		return false
	})
	if len(r2.m.Waits()) != 0 {
		t.Errorf("waits %v", r2.m.Waits())
	}
}

func TestScheduledMessageIsPostedAtItsTime(t *testing.T) {
	r := newRig(t, []fake.Script{turn(say("hi"), finish()), turn(say("on it"), finish())}, nil)
	tid := r.thread(t)
	id := r.add(t, tid, "claude")
	_ = r.m.Post(tid, "hello")
	waitFor(t, "first turn", func() bool { return r.c.turnDones(id) == 1 })
	if _, err := r.m.ScheduleSend(tid, "  ", time.Now()); err == nil {
		t.Error("an empty message was scheduled")
	}
	if _, err := r.m.ScheduleSend(tid, "now the tests", time.Now().Add(100*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if len(r.m.Scheduled()) != 1 {
		t.Fatal("not listed")
	}
	waitFor(t, "the scheduled post", func() bool { return strings.Contains(lastPrompt(r.claude, id), "now the tests") })
	if len(r.m.Scheduled()) != 0 {
		t.Error("still listed after it was sent")
	}
}

func TestErrorTextOfAnyAdapterIsALimitHit(t *testing.T) {
	later := time.Now().Add(time.Hour)
	text := "You've hit your usage limit. Try again in 1 hour."
	r := newRig(t, nil, []fake.Script{turn(fake.Step{Event: agent.Event{Kind: agent.KindError, Text: text}}, finish())})
	tid := r.thread(t)
	id := r.add(t, tid, "codex")
	_ = r.m.Post(tid, "go")
	waitFor(t, "the wait", func() bool { return status(r.m, id) == StatusWaiting })
	ws := r.m.Waits()
	if len(ws) != 1 || ws[0].Until.Before(later.Add(-2*time.Minute)) || ws[0].Until.After(later.Add(time.Minute)) {
		t.Fatalf("waits %+v", ws)
	}
}

func TestHumanDur(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * time.Second:                "1m",
		10 * time.Second:                "less than a minute",
		2*time.Hour + 13*time.Minute:    "2h 13m",
		3 * time.Hour:                   "3h",
		50*time.Hour + 20*time.Minute:   "2d 2h",
		45*time.Minute + 20*time.Second: "45m",
	} {
		if got := humanDur(d); got != want {
			t.Errorf("humanDur(%v) = %q, want %q", d, got, want)
		}
	}
}
