package session

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/deck/agent/fake"
)

func opStep(name, args string) fake.Step {
	return fake.Step{Exec: &fake.Exec{Op: name, Args: json.RawMessage(args)}}
}

// subThreadOf is the sub-thread the rig's collector saw opened.
func subThreadOf(t *testing.T, r *rig) SubThreadMsg {
	t.Helper()
	var out SubThreadMsg
	waitFor(t, "a sub-thread", func() bool {
		for _, m := range r.c.all() {
			if s, ok := m.(SubThreadMsg); ok {
				out = s
				return true
			}
		}
		return false
	})
	return out
}

// The lead opens a sub-thread of another kind; its agent's answer comes back
// to the lead, and what the person says in the sub-thread stays there.
func TestASubThreadReportsToItsLeadOnlyWhatTheLeadAsked(t *testing.T) {
	r := newRig(t, []fake.Script{
		turn(opStep("thread.open", `{"title":"Second opinion","brief":"Is the limiter fix right?","kind":"codex"}`), say("asked codex in a sub-thread"), finish()),
		turn(say("codex agrees"), finish()),
		turn(say("unexpected"), finish()),
	}, []fake.Script{
		turn(say("Yes: keyed by address, with a test."), finish()),
		turn(say("Docs too: the README says it."), finish()),
	})
	tid := r.thread(t)
	if err := r.m.Post(tid, "fix the limiter, and get a second opinion"); err != nil {
		t.Fatal(err)
	}
	sub := subThreadOf(t, r)
	if sub.ParentID != tid || sub.Title != "Second opinion" {
		t.Fatalf("sub-thread = %+v", sub)
	}
	waitFor(t, "the lead hears back", func() bool { return r.c.turnDones("claude") == 2 })
	info, _ := r.m.Thread(sub.ThreadID)
	if info.Parent != tid || info.ParentAgent != "claude" || info.Lead == "" || len(info.Members) != 1 || info.Members[0].Kind != "codex" {
		t.Fatalf("sub-thread info = %+v", info)
	}
	brief := r.codex.prompts(info.Lead)[0]
	has(t, brief, "claude, the agent of the thread", "Is the limiter fix right?")
	report := r.claude.prompts("claude")[1]
	has(t, report, `[sub-thread "Second opinion"] Yes: keyed by address, with a test.`)

	// The person talks to the sub-thread's agent: the lead hears nothing.
	if err := r.m.Post(sub.ThreadID, "and the docs?"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "codex answers the person", func() bool { return r.c.turnDones(info.Lead) == 2 })
	time.Sleep(60 * time.Millisecond)
	if n := len(r.claude.prompts("claude")); n != 2 {
		t.Fatalf("the lead was woken by the person's own talk in the sub-thread: %d prompts", n)
	}

	// The clients see the brief as claude's, the person's words as theirs.
	var from []string
	for _, m := range r.c.all() {
		if e, ok := m.(EventMsg); ok && e.ThreadID == sub.ThreadID && e.FromUser() {
			from = append(from, e.From)
		}
	}
	if len(from) != 2 || from[0] != "claude" || from[1] != "" {
		t.Fatalf("user messages in the sub-thread are from %q, want [claude \"\"]", from)
	}
}

// A follow-up from the lead is an ask too; one from anyone else is refused.
func TestThreadPostIsTheOpenersOnly(t *testing.T) {
	r := newRig(t, []fake.Script{
		turn(opStep("thread.open", `{"brief":"draft the docs","kind":"codex"}`), say("opened"), finish()),
		turn(say("got the draft"), finish()),
		turn(say("got the second draft"), finish()),
	}, []fake.Script{
		turn(say("draft one"), finish()),
		turn(say("draft two"), finish()),
	})
	tid := r.thread(t)
	if err := r.m.Post(tid, "write docs with codex"); err != nil {
		t.Fatal(err)
	}
	sub := subThreadOf(t, r)
	waitFor(t, "the first report", func() bool { return r.c.turnDones("claude") == 2 })
	parent := r.m.thread(tid)
	parent.mu.Lock()
	lead := parent.member("claude")
	parent.mu.Unlock()
	if err := r.m.postToSubThread(parent, lead, sub.ThreadID, "shorter, please"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the second report", func() bool { return r.c.turnDones("claude") == 3 })
	has(t, r.claude.prompts("claude")[2], "draft two")
	info, _ := r.m.Thread(sub.ThreadID)
	has(t, r.codex.prompts(info.Lead)[1], "From claude, the agent that opened this sub-thread", "shorter, please")

	other, _ := r.m.NewThread(t.TempDir())
	if err := r.m.postToSubThread(r.m.thread(other), lead, sub.ThreadID, "hi"); err == nil || !strings.Contains(err.Error(), "not a sub-thread you opened") {
		t.Fatalf("err = %v", err)
	}
}

// With orchestration off, a lead opens sub-threads only in a turn where the
// person spoke to it, and no more than its level allows.
func TestSubThreadsFollowTheLevel(t *testing.T) {
	r := newRig(t, []fake.Script{
		turn(opStep("thread.open", `{"brief":"a","kind":"codex"}`), opStep("thread.open", `{"brief":"b","kind":"codex"}`), opStep("thread.open", `{"brief":"c","kind":"codex"}`), say("opened"), finish()),
	}, []fake.Script{turn(say("ok"), finish()), turn(say("ok"), finish())})
	tid := r.thread(t)
	if err := r.m.Post(tid, "split it three ways"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the lead's turn", func() bool { return r.c.turnDones("claude") >= 1 })
	res := r.c.toolResults("claude", "deck")
	if len(res) != 3 || !strings.HasPrefix(res[0], "opened sub-thread") || !strings.HasPrefix(res[1], "opened sub-thread") || !strings.Contains(res[2], "the most this thread's level allows") {
		t.Fatalf("results = %q", res)
	}
	if n := r.c.count(func(m tea.Msg) bool { _, ok := m.(SubThreadMsg); return ok }); n != 2 {
		t.Fatalf("%d sub-threads opened, want 2", n)
	}
}
