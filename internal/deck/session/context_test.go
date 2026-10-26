package session

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/hub"
)

// TestSetModelClearsContextWindow covers a switch from a 1M-window model:
// its window must not stay on the meter (apply keeps a window until a
// non-zero one arrives), so it reads unknown until the new model reports.
func TestSetModelClearsContextWindow(t *testing.T) {
	usage := fake.Step{Event: agent.Event{Kind: agent.KindUsage, Usage: &agent.Usage{ContextTokens: 5000, ContextWindow: 1_000_000}}}
	r := newRigWith(t, t.TempDir(), true, testCfg(), []fake.Script{turn(usage, say("a"), finish())}, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	_ = r.m.Post(tid, "$claude go")
	waitFor(t, "turn", func() bool { return r.c.turnDones("claude") == 1 })
	if info, _ := r.m.Agent("claude"); info.ContextWindow != 1_000_000 {
		t.Fatalf("window before switch = %d", info.ContextWindow)
	}
	if err := r.m.SetModel("claude", "small"); err != nil {
		t.Fatal(err)
	}
	info, _ := r.m.Agent("claude")
	if info.ContextWindow != 0 || info.ContextTokens != 5000 {
		t.Fatalf("after switch: %d/%d, want 5000/0 (window unknown, conversation kept)", info.ContextTokens, info.ContextWindow)
	}
}

// TestBriefedPersistsAcrossResume covers the member's briefed mark: set once
// a turn of its CLI session is done, kept in the member meta, and handed to
// the adapter on resume (Spec.Briefed) so OpenCode and Antigravity do not
// send Deck's instructions into the same conversation again.
func TestBriefedPersistsAcrossResume(t *testing.T) {
	started := fake.Step{Event: agent.Event{Kind: agent.KindSessionStarted, SessionID: "s1"}}
	r := newRig(t, []fake.Script{turn(started, say("a"), finish())}, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.claude.mu.Lock()
	first := r.claude.specs[len(r.claude.specs)-1]
	r.claude.mu.Unlock()
	if first.Briefed {
		t.Fatal("a new session is not briefed")
	}
	_ = r.m.Post(tid, "$claude go")
	waitFor(t, "turn", func() bool { return r.c.turnDones("claude") == 1 })
	r.m.Flush()
	if _, meta, _ := r.st.LoadEntries(tid); len(meta.Members) != 1 || !meta.Members[0].Briefed || meta.Members[0].SessionID != "s1" {
		t.Fatalf("stored member = %+v", meta.Members)
	}
	r.m.Close()

	r2 := newRigIn(t, r.dir, []fake.Script{turn(say("b"), finish())}, nil)
	if _, _, err := r2.m.Resume(tid); err != nil {
		t.Fatal(err)
	}
	_ = r2.m.Post(tid, "$claude again")
	waitFor(t, "resumed run", func() bool {
		r2.claude.mu.Lock()
		defer r2.claude.mu.Unlock()
		return len(r2.claude.specs) > 0
	})
	r2.claude.mu.Lock()
	spec := r2.claude.specs[len(r2.claude.specs)-1]
	r2.claude.mu.Unlock()
	if spec.ResumeID != "s1" || !spec.Briefed {
		t.Fatalf("resumed with %q briefed=%v, want s1 and briefed", spec.ResumeID, spec.Briefed)
	}
}

// TestThreadReadFitsTheHubCap covers thread.read on a long thread: it fits
// under the hub's output cap, so the broker does not cut the end (the newest
// activity) off, and it keeps that end.
func TestThreadReadFitsTheHubCap(t *testing.T) {
	const turns = 12
	var scripts []fake.Script
	for i := 0; i < turns-1; i++ {
		scripts = append(scripts, turn(say(fmt.Sprintf("part %d %s", i, strings.Repeat("é", 2500))), finish()))
	}
	scripts = append(scripts, turn(say("THE-END"), finish()))
	r := newRig(t, scripts, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	for i := 1; i <= turns; i++ {
		_ = r.m.Post(tid, "$claude go")
		waitFor(t, "turn", func() bool { return r.c.turnDones("claude") == i })
	}
	got := r.m.readThread("claude")
	if len(got) > hub.MaxOutput-readSlack/2 {
		t.Fatalf("thread.read = %d bytes, over the %d hub cap", len(got), hub.MaxOutput)
	}
	if !strings.Contains(got, "THE-END") || !strings.Contains(got, "earlier activity omitted") {
		t.Fatalf("lost the newest activity or the note:\n%.300s ... %s", got, got[max(0, len(got)-200):])
	}
	if !utf8.ValidString(got) {
		t.Fatal("cut inside a rune")
	}
}

// TestContextSourceKeptAndStored covers where a member's context figure came
// from: it follows the usage event that set the fill, shows in AgentInfo and
// is stored with the member, so a resumed thread still marks an estimate.
func TestContextSourceKeptAndStored(t *testing.T) {
	est := fake.Step{Event: agent.Event{Kind: agent.KindUsage, Usage: &agent.Usage{ContextTokens: 9000, ContextWindow: 1_000_000, ContextSource: agent.ContextEstimated}}}
	r := newRig(t, []fake.Script{turn(est, say("a"), finish())}, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	_ = r.m.Post(tid, "$claude go")
	waitFor(t, "turn", func() bool { return r.c.turnDones("claude") == 1 })
	if info, _ := r.m.Agent("claude"); info.ContextSource != agent.ContextEstimated {
		t.Fatalf("source = %q", info.ContextSource)
	}
	r.m.Flush()
	if _, meta, _ := r.st.LoadEntries(tid); len(meta.Members) != 1 || meta.Members[0].ContextSource != agent.ContextEstimated {
		t.Fatalf("stored member = %+v", meta.Members)
	}
}

// TestContextPolicyReachesTheSpec covers the context section: each member's
// Spec.Context is the section's knobs with its kind's overrides.
func TestContextPolicyReachesTheSpec(t *testing.T) {
	cfg := testCfg()
	off := false
	cfg.Context = config.Context{IsolateGlobalConfig: true, MCP: agent.MCPInherit, CompactAtPct: 70,
		PerAgent: map[string]config.ContextOverride{"codex": {IsolateGlobalConfig: &off, InstructionFiles: agent.InstrFilesNone}}}
	r := newRigWith(t, t.TempDir(), true, cfg, nil, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	r.claude.mu.Lock()
	cs := r.claude.specs[len(r.claude.specs)-1].Context
	r.claude.mu.Unlock()
	r.codex.mu.Lock()
	xs := r.codex.specs[len(r.codex.specs)-1].Context
	r.codex.mu.Unlock()
	if want := (agent.ContextPolicy{IsolateGlobalConfig: true, MCP: agent.MCPInherit, CompactAtPct: 70}); cs != want {
		t.Errorf("claude policy = %+v, want %+v", cs, want)
	}
	if want := (agent.ContextPolicy{InstructionFiles: agent.InstrFilesNone, MCP: agent.MCPInherit, CompactAtPct: 70}); xs != want {
		t.Errorf("codex policy = %+v, want %+v", xs, want)
	}
}

// TestContextPolicyNotice covers the one notice per kind that says which
// knobs its CLI cannot honour, and that the defaults say nothing.
func TestContextPolicyNotice(t *testing.T) {
	cfg := testCfg()
	cfg.Context = config.Context{IsolateGlobalConfig: true, CompactAtPct: 70}
	r := newRigWith(t, t.TempDir(), true, cfg, nil, nil)
	r.claude.caps = agent.ContextCaps{IsolateConfig: agent.SupportPartial, Compaction: agent.CompactCaps{Threshold: true}}
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	tid2 := r.thread(t)
	r.add(t, tid2, "codex")
	tally := func() map[string]int {
		notes := map[string]int{}
		for _, m := range r.c.all() {
			nm, ok := m.(NoticeMsg)
			if !ok {
				continue
			}
			n := nm.Text
			for _, k := range []string{"claude:", "codex:"} {
				if !strings.HasPrefix(n, k) {
					continue
				}
				notes[k]++
				if k == "codex:" && (!strings.Contains(n, "isolate_global_config is not supported") || !strings.Contains(n, "compact_at_pct is not supported")) {
					t.Errorf("codex notice = %q", n)
				}
				if k == "claude:" && (!strings.Contains(n, "hooks and permissions") || strings.Contains(n, "not supported")) {
					t.Errorf("claude notice = %q", n)
				}
			}
		}
		return notes
	}
	waitFor(t, "notices", func() bool { n := tally(); return n["claude:"] > 0 && n["codex:"] > 0 })
	r.m.Flush()
	notes := tally()
	if notes["claude:"] != 1 || notes["codex:"] != 1 {
		t.Fatalf("notices = %v, want one per kind", notes)
	}

	if got := policyNotes(agent.ContextPolicy{}, agent.ContextCaps{}); len(got) != 0 {
		t.Fatalf("the defaults need no notice: %q", got)
	}
}

// TestCompact covers the Summarize action: it asks the members whose CLI
// compacts on request, and says so when none can.
func TestCompact(t *testing.T) {
	r := newRig(t, nil, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	if err := r.m.Compact(tid); !errors.Is(err, errNoCompactor) {
		t.Fatalf("no compactor: %v", err)
	}
	r2 := newRigWith(t, t.TempDir(), true, testCfg(), nil, nil)
	r2.claude.compact = true
	tid2 := r2.thread(t)
	r2.add(t, tid2, "claude")
	if err := r2.m.Compact(tid2); err != nil {
		t.Fatal(err)
	}
	run := r2.claude.runs["claude"]
	run.mu.Lock()
	defer run.mu.Unlock()
	if run.compacts != 1 {
		t.Fatalf("compacts = %d", run.compacts)
	}
}
