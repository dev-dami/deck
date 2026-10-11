package codex

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// Codex reports the ChatGPT plan's windows as a RateLimitSnapshot: a primary
// (the 5-hour window) and a secondary (the weekly one) window, each with the
// percentage used, its length and when it resets. Deck reads a snapshot once
// the thread is up (account/rateLimits/read, and account/read for the plan
// when the snapshot does not name it) and then follows the sparse
// account/rateLimits/updated notifications, merging each field into what it
// knows. Only the "codex" bucket is shown. Nothing else about the account is
// read: account/read's answer is decoded down to the plan type, never the
// email.

// limitProbeTimeout bounds the start-up reads; a server that does not answer
// leaves Deck with the notifications only.
const limitProbeTimeout = 15 * time.Second

// Default window lengths when the server does not say.
const (
	primaryMins   = 300
	secondaryMins = 10080
)

type windowState struct {
	used   *float64
	mins   *int
	resets *int64
}

func (w *windowState) merge(rw *rateLimitWindow) {
	if rw == nil {
		return
	}
	u := rw.UsedPercent
	w.used = &u
	if rw.WindowDurationMins != nil {
		m := *rw.WindowDurationMins
		w.mins = &m
	}
	if rw.ResetsAt != nil {
		r := *rw.ResetsAt
		w.resets = &r
	}
}

// limitState is what is known of the plan's windows. The event loop and the
// start-up reads both update it.
type limitState struct {
	mu        sync.Mutex
	plan      string
	primary   windowState
	secondary windowState
	failed    bool // the start-up read failed
}

// merge folds a (possibly sparse) snapshot in. It reports false for a
// snapshot of another bucket than codex's own.
func (s *limitState) merge(snap rateLimitSnapshot) bool {
	if snap.LimitID != nil && *snap.LimitID != "" && *snap.LimitID != "codex" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if snap.PlanType != nil && *snap.PlanType != "" {
		s.plan = *snap.PlanType
	}
	s.primary.merge(snap.Primary)
	s.secondary.merge(snap.Secondary)
	if s.primary.used != nil || s.secondary.used != nil {
		s.failed = false
	}
	return true
}

func (s *limitState) setPlan(p string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p != "" {
		s.plan = p
	}
}

func (s *limitState) hasPlan() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.plan != ""
}

func (s *limitState) setFailed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.primary.used == nil && s.secondary.used == nil {
		s.failed = true
	}
}

// limits is the current picture.
func (s *limitState) limits() *agent.Limits {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := &agent.Limits{Plan: s.plan}
	for _, w := range []struct {
		id   string
		st   windowState
		mins int
	}{{"primary", s.primary, primaryMins}, {"secondary", s.secondary, secondaryMins}} {
		if w.st.used == nil {
			continue
		}
		mins := w.mins
		if w.st.mins != nil && *w.st.mins > 0 {
			mins = *w.st.mins
		}
		kind, label := windowKind(mins)
		lw := agent.LimitWindow{ID: w.id, Kind: kind, Label: label, UsedPct: min(100, max(0, *w.st.used)), DurationMins: mins}
		if w.st.resets != nil && *w.st.resets > 0 {
			lw.ResetsAt = time.Unix(*w.st.resets, 0)
		}
		l.Windows = append(l.Windows, lw)
	}
	if len(l.Windows) == 0 && s.failed {
		l.Unavailable = agent.LimitsProbeFailed
	}
	return l
}

// windowKind names a window by its length.
func windowKind(mins int) (kind, label string) {
	const day = 24 * 60
	switch {
	case mins == 7*day:
		return agent.WindowWeekly, "Weekly"
	case mins >= 28*day && mins <= 31*day:
		return agent.WindowMonthly, "Monthly"
	case mins > 0 && mins < day:
		if mins%60 == 0 {
			return agent.WindowSession, fmt.Sprintf("%dh", mins/60)
		}
		return agent.WindowSession, fmt.Sprintf("%dm", mins)
	case mins > 0 && mins%day == 0:
		return agent.WindowOther, fmt.Sprintf("%dd", mins/day)
	case mins > 0 && mins%60 == 0:
		return agent.WindowOther, fmt.Sprintf("%dh", mins/60)
	}
	return agent.WindowOther, "Window"
}

// limitsEvent is the KindLimits event for what is known now. It carries no
// raw line: start-up answers may say more about the account than Deck shows.
func (t *translator) limitsEvent() agent.Event {
	e := t.ev(agent.KindLimits, nil)
	e.Limits = t.limits.limits()
	return e
}

// probeLimits reads the plan's windows once the thread is up, and the plan's
// name when the snapshot does not carry it. The caller has done
// r.pushers.Add(1), so the event loop does not close the events channel
// under it.
func (r *run) probeLimits() {
	defer r.pushers.Done()
	ctx, cancel := context.WithTimeout(context.Background(), limitProbeTimeout)
	defer cancel()

	var rl rateLimitsResponse
	if err := r.client.Call(ctx, methodRateLimits, rateLimitsParams{ExcludeResetCreditDetails: true}, &rl); err != nil {
		r.tr.limits.setFailed()
	} else {
		snap := rl.RateLimits
		if s, ok := rl.RateLimitsByLimitID["codex"]; ok {
			snap = s
		}
		r.tr.limits.merge(snap)
	}
	if !r.tr.limits.hasPlan() {
		var acc accountResponse
		if err := r.client.Call(ctx, methodAccountRead, accountParams{}, &acc); err == nil && acc.Account != nil {
			r.tr.limits.setPlan(acc.Account.PlanType)
		}
	}
	r.emit(r.tr.limitsEvent())
}
