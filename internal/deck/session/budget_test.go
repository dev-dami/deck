package session

import (
	"testing"

	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/hub"
)

func TestBudgetFor(t *testing.T) {
	fixed := ctxBudget{briefing: maxBriefingChars, entry: maxEntryChars, read: maxBriefingChars, output: hub.MaxOutput}
	cases := []struct {
		name         string
		window, used int
		cfg          config.Context
		want         ctxBudget
	}{
		// The defaults keep the fixed budgets for the windows they were set for.
		{"unknown window", 0, 0, config.Context{}, fixed},
		{"128k", 128_000, 0, config.Context{}, fixed},
		{"200k", 200_000, 10_000, config.Context{}, fixed},
		// A 1M window grows them, up to the ceilings.
		{"1M", 1_000_000, 0, config.Context{}, ctxBudget{briefing: 160_000, entry: 24_000, read: 160_000, output: 60_000}},
		{"400k", 400_000, 0, config.Context{}, ctxBudget{briefing: 64_000, entry: 9600, read: 64_000, output: 24_000}},
		// A small window shrinks them, down to the floors.
		{"32k", 32_000, 0, config.Context{}, ctxBudget{briefing: 16_000, entry: 2400, read: 16_000, output: 8 << 10}},
		// Over 80% full: half the briefing; thread.read keeps its share.
		{"full", 200_000, 170_000, config.Context{}, ctxBudget{briefing: 20_000, entry: 3000, read: 40_000, output: hub.MaxOutput}},
		// A configured share is taken as it is, within the clamps.
		{"briefing 2%", 200_000, 0, config.Context{BriefingPct: 2}, ctxBudget{briefing: 16_000, entry: 2400, read: 16_000, output: hub.MaxOutput}},
		{"output 1% of 1M", 1_000_000, 0, config.Context{ToolOutputPct: 1}, ctxBudget{briefing: 160_000, entry: 24_000, read: 160_000, output: 40_000}},
		{"output 10% of 1M", 1_000_000, 0, config.Context{ToolOutputPct: 10}, ctxBudget{briefing: 160_000, entry: 24_000, read: 160_000, output: hub.MaxOutputCap}},
	}
	for _, c := range cases {
		if got := budgetFor(c.window, c.used, c.cfg); got != c.want {
			t.Errorf("%s: budgetFor(%d, %d) = %+v, want %+v", c.name, c.window, c.used, got, c.want)
		}
	}
}

// TestBudgetFollowsTheWindow covers a member's budgets on a 1M window: its
// thread.read and its search and execute results may be longer than the
// fixed 20 KB, and its briefings keep more of a long thread.
func TestBudgetFollowsTheWindow(t *testing.T) {
	r := newRig(t, nil, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	a := r.m.get("claude")
	if b := r.m.budget(a); b.output != hub.MaxOutput || b.briefing != maxBriefingChars {
		t.Fatalf("before a window is known: %+v, want the fixed budgets", b)
	}
	a.mu.Lock()
	a.ctxWindow, a.ctxTokens = 1_000_000, 5000
	a.mu.Unlock()
	if b := r.m.budget(a); b.output != 60_000 || b.briefing != 160_000 {
		t.Fatalf("1M window: %+v", b)
	}
	cfg := testCfg()
	cfg.Context.ToolOutputPct = 1
	r.m.SetConfig(cfg)
	if b := r.m.budget(a); b.output != 40_000 {
		t.Fatalf("configured 1%%: %+v", b)
	}
}
