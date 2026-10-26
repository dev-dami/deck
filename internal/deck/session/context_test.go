package session

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
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
