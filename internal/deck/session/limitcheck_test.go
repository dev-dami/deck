package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/store"
)

// limitsAdapter starts runs that report limits when they start and, given a
// first prompt, end its turn; specs records what it was asked to start.
type limitsAdapter struct {
	limits *agent.Limits // nil: reports nothing
	specs  []agent.Spec
}

func (a *limitsAdapter) Name() string { return "stub" }

func (a *limitsAdapter) Start(_ context.Context, sp agent.Spec) (agent.Run, error) {
	a.specs = append(a.specs, sp)
	r := &limitsRun{evs: make(chan agent.Event, 8), aps: make(chan agent.ApprovalRequest)}
	if a.limits != nil {
		r.evs <- agent.Event{Kind: agent.KindLimits, Limits: a.limits}
	}
	if sp.FirstPrompt != "" {
		r.evs <- agent.Event{Kind: agent.KindAssistantText, Text: "OK"}
		r.evs <- agent.Event{Kind: agent.KindTurnDone}
	}
	return r, nil
}

func (a *limitsAdapter) Models(context.Context) ([]agent.ModelInfo, error) {
	return []agent.ModelInfo{{ID: "big", Default: true}, {ID: "small-mini", Efforts: []agent.EffortInfo{{Level: "low"}, {Level: "high"}}}}, nil
}

type limitsRun struct {
	evs chan agent.Event
	aps chan agent.ApprovalRequest
}

func (r *limitsRun) Events() <-chan agent.Event              { return r.evs }
func (r *limitsRun) Approvals() <-chan agent.ApprovalRequest { return r.aps }
func (r *limitsRun) Send(string) error                       { return nil }
func (r *limitsRun) Interrupt() error                        { return nil }
func (r *limitsRun) SessionID() string                       { return "" }
func (r *limitsRun) Close() error                            { return nil }

func TestCheckLimitsAndStartWindow(t *testing.T) {
	reset := time.Now().Add(5 * time.Hour).Truncate(time.Second)
	codex := &limitsAdapter{limits: &agent.Limits{Plan: "plus", Windows: []agent.LimitWindow{{ID: "primary", Kind: agent.WindowSession, UsedPct: 3, ResetsAt: reset}}}}
	opencode := &limitsAdapter{limits: &agent.Limits{Unavailable: agent.LimitsUnsupported}}
	quiet := &limitsAdapter{}
	st, _ := store.New(t.TempDir())
	m := NewManager(map[string]agent.Adapter{"codex": codex, "opencode": opencode, "quiet": quiet}, st, testCfg())
	t.Cleanup(m.Close)
	collect(t, m)
	ctx := context.Background()

	l, err := m.CheckLimits(ctx, "codex")
	if err != nil || l.Plan != "plus" || len(l.Windows) != 1 || !l.Windows[0].ResetsAt.Equal(reset) {
		t.Fatalf("check %+v %v", l, err)
	}
	if sp := codex.specs[0]; sp.FirstPrompt != "" || sp.Mode != agent.ModePlan || !sp.Context.IsolateGlobalConfig || sp.Context.InstructionFiles != agent.InstrFilesNone {
		t.Errorf("a check starts a bare CLI that is told nothing: %+v", sp)
	}
	if got := m.PlanLimits()["codex"]; len(got.Windows) != 1 {
		t.Errorf("the check is what the manager knows now: %+v", got)
	}

	if _, err := m.CheckLimits(ctx, "opencode"); !errors.Is(err, ErrNoLimits) {
		t.Errorf("opencode: %v", err)
	}
	cctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err := m.CheckLimits(cctx, "quiet"); err == nil {
		t.Error("a CLI that never reports passed its check")
	}
	if _, err := m.CheckLimits(ctx, "nope"); err == nil {
		t.Error("an unknown kind passed its check")
	}

	sw, err := m.StartWindow(ctx, "codex")
	if err != nil || sw.Model != "small-mini" {
		t.Fatalf("start window %+v %v", sw, err)
	}
	if sp := codex.specs[1]; sp.FirstPrompt != windowPrompt || sp.Model != "small-mini" || sp.Effort != "low" {
		t.Errorf("the window starts on the cheapest model, lowest effort: %+v", sp)
	}
}
