package session

import (
	"strconv"
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/store"
)

func subStep(id, desc string, status agent.SubStatus) fake.Step {
	return fake.Step{Event: agent.Event{Kind: agent.KindSubagent, Subagent: &agent.Subagent{ID: id, Description: desc, Type: "general-purpose", Status: status}}}
}

func subWork(sub string, ev agent.Event) fake.Step {
	ev.Sub = sub
	return fake.Step{Event: ev}
}

func subCall(sub, id, name, title string) fake.Step {
	return subWork(sub, agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{ID: id, Name: name, Title: title, Status: agent.ToolRunning}})
}

// TestSubagentsAreTheMembersChildren: a member's CLI-native subagents are
// kept with it, named from their task, on the roster under it, and stored;
// their work reaches the UI tagged with them, but is no reply of the
// member's and stays out of the thread's log.
func TestSubagentsAreTheMembersChildren(t *testing.T) {
	r := newRig(t, []fake.Script{
		turn(started("s1"),
			say("Starting reviewers."),
			subStep("ta", "Review PR #295", agent.SubRunning),
			subStep("tb", "Review PR #296", agent.SubRunning),
			subCall("ta", "a1", "Bash", "gh pr diff 295"),
			subCall("tb", "b1", "Bash", "gh pr diff 296"),
			subWork("tb", agent.Event{Kind: agent.KindToolResult, Tool: &agent.Tool{ID: "b1", Name: "Bash", Status: agent.ToolError, Output: "rate limit"}}),
			subWork("ta", agent.Event{Kind: agent.KindAssistantText, Text: "PR 295 looks good"}),
			subStep("ta", "Review PR #295", agent.SubDone),
			fake.Step{Event: agent.Event{Kind: agent.KindSubagent, Subagent: &agent.Subagent{ID: "tb", Status: agent.SubFailed, Error: "API error"}}},
			subStep("tc", "Review PR #296", agent.SubRunning),
			subCall("tc", "c1", "Grep", "TODO"),
			say("The #296 reviewer stopped; I restarted it."),
			finish()),
	}, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	if _, err := r.m.PostWith(tid, "spin up sub agents to review each of them", nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "turn", func() bool { return r.c.turnDones("claude") == 1 })

	info, _ := r.m.Agent("claude")
	var got []string
	for _, s := range info.Subs {
		got = append(got, s.Name+" "+s.Status+" "+itoa(s.Actions))
	}
	if strings.Join(got, ", ") != "review-pr-295 done 1, review-pr-296 failed 1, review-pr-296-2 running 1" {
		t.Fatalf("subs = %v", got)
	}
	if info.Subs[1].Error != "API error" || info.Subs[1].Ended.IsZero() || info.Subs[2].Started.IsZero() {
		t.Errorf("failed sub = %+v", info.Subs[1])
	}

	// The UI hears each piece of work as the subagent's, by its name.
	work := r.c.events(func(e EventMsg) bool { return e.Sub != "" })
	if len(work) != 5 {
		t.Fatalf("sub work = %d events", len(work))
	}
	for _, e := range work {
		if e.Subagent == nil || e.Subagent.Name == "" || e.AgentID != "claude" {
			t.Fatalf("sub event %+v", e)
		}
	}
	if w := work[len(work)-1]; w.Subagent.Name != "review-pr-296-2" || SubPath(w.AgentID, w.Subagent.Name) != "claude/review-pr-296-2" {
		t.Errorf("last work by %+v", w.Subagent)
	}

	// None of it is the member's: not its reply, not in the thread's log.
	for _, h := range r.m.History(tid) {
		if e, ok := h.(EventMsg); ok && (e.Sub != "" || e.Kind == agent.KindSubagent) {
			t.Errorf("logged: %+v", e.Event)
		}
	}
	a := r.m.get("claude")
	if text := r.m.readThread("claude"); strings.Contains(text, "PR 295 looks good") || !strings.Contains(text, "I restarted it.") {
		t.Errorf("subagent text in the thread:\n%s", text)
	}

	// thread.roster lists the running one under its member.
	ros := r.m.roster(a)
	has(t, ros, "↳ claude/review-pr-296-2 · claude's own subagent (general-purpose) · Review PR #296 · running", "2 more of claude's own subagents ended")

	// A resumed room still has them; the one running stopped with the CLI.
	r.m.Close()
	meta, err := r.st.Meta(tid)
	if err != nil || len(meta.Members) != 1 || len(meta.Members[0].Subs) != 3 {
		t.Fatalf("stored = %+v %v", meta.Members, err)
	}
	r2 := newRigIn(t, r.dir, nil, nil)
	ti, _, err := r2.m.Resume(tid)
	if err != nil {
		t.Fatal(err)
	}
	subs := ti.Members[0].Subs
	if len(subs) != 3 || subs[2].Name != "review-pr-296-2" || subs[2].Status != string(agent.SubStopped) || subs[0].Status != "done" {
		t.Fatalf("resumed subs = %+v", subs)
	}
	// A new subagent of the same task still gets a name of its own.
	b := r2.m.get("claude")
	b.mu.Lock()
	s, _ := b.subLocked("td", b.started)
	s.Description = "Review PR #296"
	s.Name = b.subNameLocked(s)
	b.mu.Unlock()
	if s.Name != "review-pr-296-3" {
		t.Errorf("next name %q", s.Name)
	}
}

// TestSubagentsStopWithTheCLI: when the member's CLI exits, its running
// subagents are stopped.
func TestSubagentsStopWithTheCLI(t *testing.T) {
	r := newRig(t, []fake.Script{
		turn(subStep("ta", "Map the code", agent.SubRunning), finish()),
	}, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	_ = r.m.Post(tid, "go")
	waitFor(t, "turn", func() bool { return r.c.turnDones("claude") == 1 })
	a := r.m.get("claude")
	r.m.onEvent(a, agent.Event{Kind: agent.KindExited})
	info, _ := r.m.Agent("claude")
	if len(info.Subs) != 1 || info.Subs[0].Status != string(agent.SubStopped) || info.Subs[0].Name != "map-the-code" {
		t.Fatalf("subs = %+v", info.Subs)
	}
	// The UI hears that it stopped, so its block ends.
	stops := func() []EventMsg {
		return r.c.events(func(e EventMsg) bool { return e.Kind == agent.KindSubagent && e.Subagent.Status == agent.SubStopped })
	}
	waitFor(t, "the stop", func() bool { return len(stops()) > 0 })
	if stopped := stops(); len(stopped) != 1 || stopped[0].Subagent.Name != "map-the-code" {
		t.Fatalf("stopped = %+v", stopped)
	}
}

func TestSubagentNames(t *testing.T) {
	for _, c := range []struct{ desc, typ, want string }{
		{"Review PR #296", "general-purpose", "review-pr-296"},
		{"", "Explore", "explore"},
		{"", "general-purpose", "subagent"},
		{"Investigate the flaky websocket reconnect test", "", "investigate-the-flaky"},
	} {
		var a agentState
		s := &store.SubAgent{Description: c.desc, Type: c.typ}
		if got := a.subNameLocked(s); got != c.want {
			t.Errorf("name(%q, %q) = %q, want %q", c.desc, c.typ, got, c.want)
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
