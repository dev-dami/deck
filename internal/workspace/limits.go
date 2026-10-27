package workspace

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	deckagent "github.com/dev-dami/deck/internal/deck/agent"
	decks "github.com/dev-dami/deck/internal/deck/session"
)

// LimitsDesk is what the desktop does about the agents' plan limits beyond
// reading them (UsageReader): ask the CLIs for them now, start a rolling
// window early, and handle the agents that wait for a limit to reset and
// the messages scheduled for later (internal/deck/session waits.go,
// limitcheck.go).
type LimitsDesk interface {
	// CheckLimits asks kind's CLI (every installed one for "") for its
	// plan's windows now, records them, and returns how each check went.
	CheckLimits(ctx context.Context, kind string) []LimitCheck
	// LimitChecks are the checks of this run of Deck, by kind.
	LimitChecks() []LimitCheck
	// StartWindow starts kind's rolling window with a minimal prompt on its
	// cheapest model and returns that model.
	StartWindow(ctx context.Context, kind string) (string, error)
	Waits() []AgentWait
	// ResumeWait resumes a waiting agent now; CancelWait ends its wait and
	// drops what is queued for it.
	ResumeWait(sessionID, agent string) error
	CancelWait(sessionID, agent string) error
	// ScheduleSend posts text in the session at at.
	ScheduleSend(ctx context.Context, sessionID, text string, at time.Time) (string, error)
	Scheduled() []ScheduledSend
	// CancelScheduled drops a scheduled message, or with now sends it now.
	CancelScheduled(sessionID, id string, now bool) error
}

// LimitCheck is how asking a kind's CLI for its limits went: At when, Err
// why it failed ("" when it did not), Unsupported when the CLI has no way to
// tell.
type LimitCheck struct {
	Kind        string
	At          time.Time
	Err         string
	Unsupported bool
}

// AgentWait is an agent waiting for its plan limit to reset. Agent is its
// name in the session, as the roster says it.
type AgentWait struct {
	SessionID, Agent, Kind string
	Since, Until           time.Time
	Window                 string
}

// ScheduledSend is a message waiting for its time.
type ScheduledSend struct {
	SessionID, ID, Text string
	At, Created         time.Time
}

var _ LimitsDesk = (*DeckWorkspace)(nil)

// limitChecks are the checks of this run, by kind; shared by every
// workspace of the process (there is one).
var limitChecks = struct {
	sync.Mutex
	m map[string]LimitCheck
}{m: map[string]LimitCheck{}}

// CheckLimits implements [LimitsDesk]. Kinds are checked together.
func (w *DeckWorkspace) CheckLimits(ctx context.Context, kind string) []LimitCheck {
	kinds := []string{kind}
	if kind == "" {
		kinds = w.mgr().Kinds()
	}
	out := make([]LimitCheck, len(kinds))
	var wg sync.WaitGroup
	for i, k := range kinds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := w.mgr().CheckLimits(ctx, k)
			c := LimitCheck{Kind: k, At: time.Now()}
			switch {
			case errors.Is(err, decks.ErrNoLimits):
				c.Unsupported, c.Err = true, err.Error()
			case err != nil:
				c.Err = err.Error()
			}
			if len(l.Windows) > 0 || l.Plan != "" {
				w.onLimits(deckagent.Event{AgentID: k, Time: c.At, Kind: deckagent.KindLimits, Limits: &l})
			}
			limitChecks.Lock()
			limitChecks.m[k] = c
			limitChecks.Unlock()
			out[i] = c
		}()
	}
	wg.Wait()
	return out
}

// LimitChecks implements [LimitsDesk].
func (w *DeckWorkspace) LimitChecks() []LimitCheck {
	limitChecks.Lock()
	defer limitChecks.Unlock()
	out := make([]LimitCheck, 0, len(limitChecks.m))
	for _, c := range limitChecks.m {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b LimitCheck) int {
		if a.Kind < b.Kind {
			return -1
		}
		if a.Kind > b.Kind {
			return 1
		}
		return 0
	})
	return out
}

// StartWindow implements [LimitsDesk]. The limits the CLI reports after are
// recorded as a check.
func (w *DeckWorkspace) StartWindow(ctx context.Context, kind string) (string, error) {
	sw, err := w.mgr().StartWindow(ctx, kind)
	if len(sw.Limits.Windows) > 0 {
		now := time.Now()
		w.onLimits(deckagent.Event{AgentID: kind, Time: now, Kind: deckagent.KindLimits, Limits: &sw.Limits})
		limitChecks.Lock()
		limitChecks.m[kind] = LimitCheck{Kind: kind, At: now}
		limitChecks.Unlock()
	}
	return sw.Model, err
}

// Waits implements [LimitsDesk].
func (w *DeckWorkspace) Waits() []AgentWait {
	var out []AgentWait
	for _, wi := range w.mgr().Waits() {
		sid := w.sessionOf(wi.ThreadID)
		if sid == "" {
			continue
		}
		out = append(out, AgentWait{SessionID: sid, Agent: wi.Name, Kind: wi.Kind, Since: wi.Since, Until: wi.Until, Window: wi.Window})
	}
	return out
}

// member finds a session's agent by its name (or id).
func (w *DeckWorkspace) member(sessionID, agent string) (string, error) {
	th, ok := w.threadOf(sessionID)
	if !ok {
		return "", ErrNotFound
	}
	for _, a := range th.Members {
		if a.Name == agent || a.ID == agent {
			return a.ID, nil
		}
	}
	return "", ErrNotFound
}

// ResumeWait implements [LimitsDesk].
func (w *DeckWorkspace) ResumeWait(sessionID, agent string) error {
	id, err := w.member(sessionID, agent)
	if err != nil {
		return err
	}
	return w.mgr().ResumeWait(id)
}

// CancelWait implements [LimitsDesk].
func (w *DeckWorkspace) CancelWait(sessionID, agent string) error {
	id, err := w.member(sessionID, agent)
	if err != nil {
		return err
	}
	return w.mgr().CancelWait(id)
}

// ScheduleSend implements [LimitsDesk]. A session nobody wrote in yet gets
// its thread now.
func (w *DeckWorkspace) ScheduleSend(ctx context.Context, sessionID, text string, at time.Time) (string, error) {
	tid, err := w.roomThread(ctx, sessionID, true)
	if err != nil {
		return "", err
	}
	return w.mgr().ScheduleSend(tid, text, at)
}

// Scheduled implements [LimitsDesk].
func (w *DeckWorkspace) Scheduled() []ScheduledSend {
	var out []ScheduledSend
	for _, s := range w.mgr().Scheduled() {
		sid := w.sessionOf(s.ThreadID)
		if sid == "" {
			continue
		}
		out = append(out, ScheduledSend{SessionID: sid, ID: s.ID, Text: s.Text, At: s.At, Created: s.Created})
	}
	return out
}

// CancelScheduled implements [LimitsDesk].
func (w *DeckWorkspace) CancelScheduled(sessionID, id string, now bool) error {
	w.mu.Lock()
	tid, ok := w.threads[sessionID]
	w.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	return w.mgr().CancelScheduled(tid, id, now)
}
