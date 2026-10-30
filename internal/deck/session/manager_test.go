package session

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/store"
)

// ---- one-member behaviour (what Deck did before shared threads) ----

func TestOneMemberThreadPassesPromptsThroughUnchanged(t *testing.T) {
	r := newRig(t, []fake.Script{
		turn(started("s1"), say("first answer"), finish()),
		turn(say("second answer"), finish()),
	}, nil)
	id, err := r.m.Start("claude", "do the thing", t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if id != "claude" {
		t.Fatalf("id = %q", id)
	}
	waitFor(t, "first turn", func() bool { return r.c.turnDones(id) == 1 })
	if err := r.m.Send(id, "and again"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "second turn", func() bool { return r.c.turnDones(id) == 2 })

	got := r.claude.prompts(id)
	if len(got) != 2 || got[0] != "do the thing" || got[1] != "and again" {
		t.Fatalf("prompts = %q, want the user's words untouched", got)
	}
	info, _ := r.m.Agent(id)
	if info.Title != "do the thing" || info.ThreadID == "" || info.Status != StatusIdle {
		t.Fatalf("info = %+v", info)
	}
	// the adapter's echo of the prompt is never forwarded; the user's message is
	users := r.c.events(func(e EventMsg) bool { return e.FromUser() })
	if len(users) != 2 || users[0].Text != "do the thing" || users[0].To[0] != "claude" || users[0].ThreadID != info.ThreadID {
		t.Fatalf("user messages = %+v", users)
	}
	echo := r.c.events(func(e EventMsg) bool { return e.Kind == agent.KindUserText && e.AgentID != "" })
	if len(echo) != 0 {
		t.Fatalf("adapter echoes forwarded: %+v", echo)
	}
}

func TestStartIDsAndConcurrentApprovals(t *testing.T) {
	st, _ := store.New(t.TempDir())
	m := NewManager(map[string]agent.Adapter{
		"claude": fake.New(fake.DemoScript("claude"), 0),
		"codex":  fake.New(fake.DemoCodexScript(), 0),
	}, st, testCfg())
	t.Cleanup(m.Close)
	c := collect(t, m)

	var ids []string
	for _, k := range []string{"claude", "claude", "codex"} {
		id, err := m.Start(k, "do the thing", t.TempDir(), "")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if strings.Join(ids, ",") != "claude,claude2,codex" {
		t.Fatalf("ids = %v", ids)
	}
	waitFor(t, "three approvals", func() bool { return len(c.approvals()) == 3 })
	for _, a := range m.Agents() {
		if a.Status != StatusApproval || a.Pending != 1 || a.ThreadID == "" || a.Title != "do the thing" {
			t.Errorf("%s: %+v", a.ID, a)
		}
	}
	threads := map[string]bool{}
	for _, ap := range c.approvals() {
		threads[ap.ThreadID] = true
		ap.Respond(agent.Decision{Allow: true})
		ap.Respond(agent.Decision{Allow: false}) // a second answer is harmless
	}
	if len(threads) != 3 {
		t.Fatalf("approvals came from threads %v", threads)
	}
	waitFor(t, "three turns", func() bool {
		return c.turnDones("claude")+c.turnDones("claude2")+c.turnDones("codex") == 3
	})
	total := 0.0
	for _, a := range m.Agents() {
		total += a.CostUSD
	}
	if math.Abs(total-0.28) > 1e-9 {
		t.Errorf("total cost = %v, want 0.28", total)
	}
	m.Flush()
	for _, a := range m.Agents() {
		entries, th, err := st.LoadEntries(a.ThreadID)
		if err != nil {
			t.Fatal(err)
		}
		if len(th.Members) != 1 || th.Members[0].SessionID == "" || math.Abs(th.CostUSD-a.CostUSD) > 1e-9 {
			t.Errorf("%s: stored %+v", a.ID, th)
		}
		var sawUser bool
		for _, e := range entries {
			if e.Delta {
				t.Errorf("%s: delta persisted", a.ID)
			}
			if e.Type == store.EntryUser && e.Speaker == "tester" && e.Text == "do the thing" {
				sawUser = true
			}
			if e.Type == store.EntryEvent && e.Kind == agent.KindUserText {
				t.Errorf("%s: adapter echo persisted", a.ID)
			}
		}
		if !sawUser {
			t.Errorf("%s: user entry missing", a.ID)
		}
	}
}

func TestInterruptDeniesPendingAndDropsQueued(t *testing.T) {
	st, _ := store.New(t.TempDir())
	m := NewManager(map[string]agent.Adapter{"codex": fake.New(fake.DemoCodexScript(), 0)}, st, testCfg())
	t.Cleanup(m.Close)
	c := collect(t, m)
	id, _ := m.Start("codex", "rebuild", t.TempDir(), "")
	waitFor(t, "approval", func() bool { return len(c.approvals()) == 1 })
	tid := c.approvals()[0].ThreadID
	if err := m.Send(id, "queued one"); err != nil {
		t.Fatal(err)
	}
	if err := m.Interrupt(id); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "turn done", func() bool { return c.turnDones(id) == 1 })
	gone := func() int {
		return c.count(func(m tea.Msg) bool {
			g, ok := m.(ApprovalGoneMsg)
			return ok && g.ThreadID == tid && g.ID == c.approvals()[0].ID
		})
	}
	// The denial ends the fake's turn on its own goroutine, so the turn's end
	// can reach the collector before the approval-gone message does.
	waitFor(t, "the approval gone", func() bool { return gone() > 0 })
	time.Sleep(50 * time.Millisecond)
	if n := gone(); n != 1 {
		t.Fatalf("approval-gone messages = %d", n)
	}
	if a, _ := m.Agent(id); a.Status != StatusIdle || a.Pending != 0 {
		t.Fatalf("after interrupt: %+v", a)
	}
	if c.turnDones(id) != 1 {
		t.Fatal("a message queued before the interrupt started a new turn")
	}
}

func TestRename(t *testing.T) {
	r := newRig(t, nil, nil)
	tid := r.thread(t)
	a := r.add(t, tid, "claude")
	b := r.add(t, tid, "claude")
	if a != "claude" || b != "claude2" {
		t.Fatalf("ids = %s %s", a, b)
	}
	if err := r.m.Rename(a, "auth"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "two words", "@x", "$x", "auth", "AUTH", a, "all", "user", "codex"} {
		if err := r.m.Rename(b, bad); err == nil {
			t.Errorf("Rename(%q) should fail", bad)
		}
	}
	if info, _ := r.m.Agent(a); info.Name != "auth" || info.ID != a {
		t.Fatalf("info = %+v", info)
	}
	if err := r.m.Rename("nope", "z"); err == nil {
		t.Fatal("unknown agent")
	}
	// the new name addresses the member
	if err := r.m.postAt(tid, "$auth hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "auth to reply", func() bool { return r.c.turnDones(a) == 1 })
	if len(r.claude.prompts(b)) != 0 {
		t.Fatal("message reached the wrong member")
	}
	r.m.Flush()
	_, th, _ := r.st.Load(tid)
	if th.Members[0].Name != "auth" {
		t.Fatalf("stored members = %+v", th.Members)
	}
}

func TestCloseUnblocksListenAndIsIdempotent(t *testing.T) {
	st, _ := store.New(t.TempDir())
	m := NewManager(map[string]agent.Adapter{"codex": fake.New(fake.DemoCodexScript(), 0)}, st, testCfg())
	_, _ = m.Start("codex", "x", t.TempDir(), "")
	res := make(chan tea.Msg, 1)
	go func() {
		for {
			if msg := m.Listen()(); msg == nil {
				res <- nil
				return
			}
		}
	}()
	time.Sleep(50 * time.Millisecond)
	m.Close()
	m.Close()
	select {
	case <-res:
	case <-time.After(3 * time.Second):
		t.Fatal("Listen did not return after Close")
	}
	if _, err := m.Start("codex", "x", "", ""); err == nil {
		t.Fatal("Start after Close should fail")
	}
}

func TestUnknownKind(t *testing.T) {
	r := newRig(t, nil, nil)
	if _, err := r.m.Start("nope", "", "", ""); err == nil {
		t.Fatal("expected error")
	}
	tid := r.thread(t)
	if _, err := r.m.AddMember(tid, "nope", ""); err == nil {
		t.Fatal("expected error")
	}
	if got := r.m.Kinds(); len(got) != 2 || got[0] != "claude" || got[1] != "codex" {
		t.Fatalf("kinds = %v", got)
	}
}

// ---- addressing and members ----

func TestPostAddsMemberForKindMentionAndBriefsItOnTheWholeThread(t *testing.T) {
	r := newRig(t, []fake.Script{
		turn(started("c1"),
			fake.Step{Event: agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{ID: "t1", Name: "Bash", Title: "go test ./...", Status: agent.ToolRunning}}},
			fake.Step{Event: agent.Event{Kind: agent.KindToolResult, Tool: &agent.Tool{ID: "t1", Name: "Bash", Title: "go test ./...", Output: "NOISY-TEST-OUTPUT", Status: agent.ToolOK}}},
			fake.Step{Event: agent.Event{Kind: agent.KindFileChange, Diff: &agent.FileDiff{Path: "store.go", Added: 3}}},
			fake.Step{Event: agent.Event{Kind: agent.KindFileChange, Diff: &agent.FileDiff{Path: "store_test.go", Added: 9}}},
			fake.Step{Event: agent.Event{Kind: agent.KindAssistantText, Text: "narration before tools"}},
			say("Store now drops deltas."), finish()),
	}, nil)
	tid := r.thread(t)

	if err := r.m.Post(tid, "fix the store"); err != nil { // empty thread: default kind
		t.Fatal(err)
	}
	waitFor(t, "claude", func() bool { return r.c.turnDones("claude") == 1 })

	if err := r.m.postAt(tid, "$codex please review"); err != nil { // kind with no member: added
		t.Fatal(err)
	}
	waitFor(t, "codex to start", func() bool { return len(r.codex.prompts("codex")) == 1 })

	ti, _ := r.m.Thread(tid)
	if len(ti.Members) != 2 || ti.Members[1].ID != "codex" || ti.Members[1].Kind != "codex" {
		t.Fatalf("members = %+v", ti.Members)
	}
	waitFor(t, "MemberAddedMsg", func() bool {
		return r.c.count(func(m tea.Msg) bool {
			a, ok := m.(MemberAddedMsg)
			return ok && a.AgentID == "codex" && a.Kind == "codex"
		}) == 1
	})

	p := r.codex.prompts("codex")[0]
	has(t, p,
		"[tester → claude] fix the store",
		"[claude changed 2 files: store.go, store_test.go]",
		"[claude ran: go test ./...]",
		"[claude] Store now drops deltas.",
		"[tester] please review",
	)
	lacks(t, p, "NOISY-TEST-OUTPUT", "narration before tools", "$codex")
	if strings.Index(p, "[claude] Store now") > strings.Index(p, "[tester] please review") {
		t.Errorf("briefing must come before the addressed message:\n%s", p)
	}

	// the new member's specs: briefing in Instructions (short, no history), MCP from MCPFor
	sp, _ := r.codex.spec("codex")
	if sp.MCP == nil || sp.MCP.Name != "deck" {
		t.Errorf("MCP = %+v", sp.MCP)
	}
	has(t, sp.Instructions, `"codex"`, "claude", "tester", "search", "execute", "thread.send", "thread.read", "plain text")
	lacks(t, sp.Instructions, "fix the store", "Store now drops")
	if len(sp.Instructions) > maxInstrChars {
		t.Errorf("Instructions are %d bytes", len(sp.Instructions))
	}
}

func TestAddressingRoutesAndFansOut(t *testing.T) {
	r := newRig(t, nil, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")

	// the first member is the lead
	if err := r.m.Post(tid, "one"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "claude turn", func() bool { return r.c.turnDones("claude") == 1 })
	if len(r.codex.prompts("codex")) != 0 {
		t.Fatal("codex should not have been addressed")
	}
	// Deck's own addressed posts reach a named member
	if err := r.m.postAt(tid, "$codex two"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "codex turn", func() bool { return r.c.turnDones("codex") == 1 })
	// the person's message goes to the lead, whoever spoke last; @ is text
	if err := r.m.Post(tid, "@codex three"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "claude second turn", func() bool { return r.c.turnDones("claude") == 2 })
	if got := len(r.codex.prompts("codex")); got != 1 {
		t.Fatalf("codex prompts = %d", got)
	}
	has(t, r.claude.prompts("claude")[1], "@codex three")
	// $all reaches both, concurrently
	if err := r.m.postAt(tid, "$all four"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "both answer", func() bool { return r.c.turnDones("claude") == 3 && r.c.turnDones("codex") == 2 })
	last := r.claude.prompts("claude")
	has(t, last[len(last)-1], "four")
	// mention-only just adds
	if err := r.m.postAt(tid, "$claude"); err != nil {
		t.Fatal(err)
	}
	users := r.c.events(func(e EventMsg) bool { return e.FromUser() })
	if len(users) != 4 || fmt.Sprint(users[3].To) != "[claude codex]" {
		t.Fatalf("user events = %+v", users)
	}
}

func TestAddMemberOfSameKindGetsNextID(t *testing.T) {
	r := newRig(t, nil, nil)
	tid := r.thread(t)
	if a, b := r.add(t, tid, "claude"), r.add(t, tid, "claude"); a != "claude" || b != "claude2" {
		t.Fatalf("ids %s %s", a, b)
	}
	tid2 := r.thread(t)
	c := r.add(t, tid2, "claude")
	if c != "claude3" {
		t.Fatalf("ids are unique across the deck, got %s", c)
	}
	// Names restart in each thread: the first claude of a new session is
	// "claude", and becomes "claude1" when a second claude joins its thread.
	names := map[string]string{}
	for _, a := range r.m.Agents() {
		names[a.ID] = a.Name
	}
	if names["claude"] != "claude1" || names["claude2"] != "claude2" || names[c] != "claude" {
		t.Fatalf("names %v", names)
	}
	// $claude in the second thread is its own claude, not the first thread's.
	th := r.m.thread(tid2)
	th.mu.Lock()
	refs := th.refs()
	th.mu.Unlock()
	ad := ParseAddressing("$claude hi", refs, []string{"claude", "codex"})
	if len(ad.Members) != 1 || ad.Members[0] != c {
		t.Fatalf("addressing in thread 2 = %+v", ad)
	}
}

// ---- queueing ----

func TestBusyMemberQueuesAndCoalesces(t *testing.T) {
	block := fake.Step{
		Event:    agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{ID: "t", Name: "Bash", Title: "make", Status: agent.ToolRunning}},
		Approval: &agent.ApprovalRequest{ID: "ap1", Kind: agent.ApproveCommand, ToolName: "Bash", Summary: "make"},
	}
	r := newRig(t, []fake.Script{
		turn(block, say("built"), finish()),
		turn(say("handled both"), finish()),
	}, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")

	if err := r.m.postAt(tid, "$claude build it"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "approval", func() bool { return len(r.c.approvals()) == 1 })
	if err := r.m.postAt(tid, "$claude also lint"); err != nil {
		t.Fatal(err)
	}
	if err := r.m.postAt(tid, "$claude and vet"); err != nil {
		t.Fatal(err)
	}
	if got := len(r.claude.prompts("claude")); got != 1 {
		t.Fatalf("prompts while busy = %d, want them queued", got)
	}
	r.c.approvals()[0].Respond(agent.Decision{Allow: true})
	waitFor(t, "both turns", func() bool { return r.c.turnDones("claude") == 2 })

	got := r.claude.prompts("claude")
	if len(got) != 2 {
		t.Fatalf("prompts = %q, want exactly one coalesced follow-up", got)
	}
	has(t, got[1], "[tester] also lint", "[tester] and vet")
	if strings.Index(got[1], "also lint") > strings.Index(got[1], "and vet") {
		t.Error("queued messages out of order")
	}
}

func TestMessageToBusyMemberViaToolSaysQueued(t *testing.T) {
	block := fake.Step{
		Event:    agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{ID: "t", Name: "Bash", Title: "make", Status: agent.ToolRunning}},
		Approval: &agent.ApprovalRequest{ID: "ap-busy", Kind: agent.ApproveCommand, ToolName: "Bash", Summary: "make"},
	}
	r := newRig(t, []fake.Script{turn(sendTo("codex", "ping"), say("sent"), finish())},
		[]fake.Script{turn(block, say("done"), finish()), turn(say("saw it"), finish())})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	worksFor(t, r.m, "codex", "claude") // plain messages between them start turns
	if err := r.m.postAt(tid, "$codex start"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "codex approval", func() bool { return len(r.c.approvals()) == 1 })
	if err := r.m.postAt(tid, "$claude go"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "claude", func() bool { return r.c.turnDones("claude") == 1 })
	res := r.c.toolResults("claude", "message")
	if len(res) != 1 || res[0] != "queued for codex (busy)" {
		t.Fatalf("thread.send result = %q", res)
	}
	r.c.approvals()[0].Respond(agent.Decision{Allow: true})
	waitFor(t, "codex second turn", func() bool { return r.c.turnDones("codex") == 2 })
	has(t, r.codex.prompts("codex")[1], "[claude → codex] ping")
}

// ---- relays: tool, mention, hops ----

// Agents reach each other with thread.send only: a "$claude" line in a reply
// is text, relayed to nobody.
func TestToolRelays(t *testing.T) {
	r := newRig(t, []fake.Script{
		turn(started("sess-claude"), sendTo("codex", "review the plan"), say("asked codex"), finish()),
		turn(say("thanks, applying it"), finish()),
	}, []fake.Script{
		turn(started("sess-codex"), sendTo("claude", "lock order matters, fix it first"), say("Looks fine.\n\n$claude a mention in a reply is text"), finish()),
	})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	worksFor(t, r.m, "codex", "claude") // plain messages between them start turns
	if err := r.m.postAt(tid, "$claude plan it"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "claude's second turn", func() bool { return r.c.turnDones("claude") == 2 })

	rel := r.c.relays()
	if len(rel) != 2 {
		t.Fatalf("relays = %+v", rel)
	}
	if rel[0].From != "claude" || rel[0].To[0] != "codex" || rel[0].Via != "tool" || rel[0].Text != "review the plan" || rel[0].Held {
		t.Errorf("tool relay = %+v", rel[0])
	}
	if rel[1].From != "codex" || rel[1].To[0] != "claude" || rel[1].Via != "tool" || rel[1].Text != "lock order matters, fix it first" {
		t.Errorf("reply relay = %+v", rel[1])
	}
	if res := r.c.toolResults("claude", "message"); len(res) != 1 || res[0] != "delivered to codex" {
		t.Errorf("thread.send result = %q", res)
	}
	// codex was briefed on what claude said and got the relay as a message to it
	has(t, r.codex.prompts("codex")[0], "[claude → codex] review the plan")
	// claude's second prompt: codex's reply as briefing, its thread.send as the message
	p := r.claude.prompts("claude")[1]
	has(t, p, "[codex → claude] lock order matters, fix it first")
	lacks(t, p, "[codex → claude] a mention in a reply is text")
	ti, _ := r.m.Thread(tid)
	if ti.Hops != 2 || ti.Paused || ti.MaxHops != 6 {
		t.Errorf("thread = %+v", ti)
	}
	// the thread log carries the speaker and who a relay went to
	r.m.Flush()
	entries, _, _ := r.st.LoadEntries(tid)
	var relays int
	for _, e := range entries {
		if e.Type == store.EntryRelay && e.Speaker != "" && len(e.To) == 1 && e.Via != "" {
			relays++
		}
	}
	if relays != 2 {
		t.Errorf("stored relay entries = %d", relays)
	}
}

func pingPong(from, to string, n int) []fake.Script {
	var out []fake.Script
	for i := 1; i <= n; i++ {
		out = append(out, turn(sendTo(to, fmt.Sprintf("round %d please", i)), say(fmt.Sprintf("round %d from %s", i, from)), finish()))
	}
	return out
}

func TestHopLimitPausesAndContinueReleases(t *testing.T) {
	r := newRig(t, pingPong("claude", "codex", 4), pingPong("codex", "claude", 3))
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	worksFor(t, r.m, "codex", "claude") // plain messages between them start turns
	if err := r.m.postAt(tid, "$claude start"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "pause", func() bool {
		ti, _ := r.m.Thread(tid)
		return ti.Paused
	})
	waitFor(t, "agents to settle", func() bool {
		return r.c.turnDones("claude") == 4 && r.c.turnDones("codex") == 3
	})
	time.Sleep(30 * time.Millisecond)

	var delivered, held int
	for _, rl := range r.c.relays() {
		if rl.Held {
			held++
		} else {
			delivered++
		}
	}
	if delivered != 6 || held != 1 {
		t.Fatalf("delivered=%d held=%d, want 6 and 1", delivered, held)
	}
	paused := r.c.count(func(m tea.Msg) bool { p, ok := m.(PausedMsg); return ok && p.Hops == 6 && p.ThreadID == tid })
	if paused != 1 {
		t.Fatalf("PausedMsg count = %d", paused)
	}
	ti, _ := r.m.Thread(tid)
	if !ti.Paused || ti.Hops != 6 || ti.Held != 1 {
		t.Fatalf("thread = %+v", ti)
	}
	for _, a := range ti.Members {
		if a.Status != StatusIdle {
			t.Errorf("%s is %s while paused", a.ID, a.Status)
		}
	}

	if err := r.m.Continue(tid); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "codex to receive the held relay", func() bool { return r.c.turnDones("codex") == 4 })
	has(t, r.codex.prompts("codex")[3], "[claude → codex] round 4 please")
	ti, _ = r.m.Thread(tid)
	if ti.Paused || ti.Hops != 0 || ti.Held != 0 {
		t.Fatalf("after Continue: %+v", ti)
	}
	if n := r.c.count(func(m tea.Msg) bool { _, ok := m.(ContinuedMsg); return ok }); n != 1 {
		t.Fatalf("ContinuedMsg = %d", n)
	}
	if err := r.m.Continue(tid); err != nil {
		t.Fatal("Continue on a thread that is not paused should do nothing:", err)
	}
}

func TestHeldRelayOverToolSaysSo(t *testing.T) {
	r := newRig(t, []fake.Script{turn(sendTo("codex", "one"), sendTo("codex", "two"), say("sent"), finish())}, nil)
	r.m.maxHops = 1
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	worksFor(t, r.m, "codex", "claude") // plain messages between them start turns
	if err := r.m.postAt(tid, "$claude go"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "claude", func() bool { return r.c.turnDones("claude") == 1 })
	res := r.c.toolResults("claude", "message")
	if len(res) != 2 || !strings.HasPrefix(res[0], "delivered to codex") ||
		!strings.HasPrefix(res[1], "held: Deck paused this thread after 1 agent-to-agent messages") {
		t.Fatalf("results = %q", res)
	}
}

func TestNewUserMessageEndsPauseAndDropsHeldRelays(t *testing.T) {
	r := newRig(t, []fake.Script{turn(sendTo("codex", "one"), sendTo("codex", "two"), say("sent"), finish())}, nil)
	r.m.maxHops = 1
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	worksFor(t, r.m, "codex", "claude") // plain messages between them start turns
	_ = r.m.postAt(tid, "$claude go")
	waitFor(t, "paused", func() bool { ti, _ := r.m.Thread(tid); return ti.Paused && r.c.turnDones("codex") == 1 })
	if err := r.m.postAt(tid, "$claude never mind"); err != nil {
		t.Fatal(err)
	}
	ti, _ := r.m.Thread(tid)
	if ti.Paused || ti.Hops != 0 || ti.Held != 0 {
		t.Fatalf("thread = %+v", ti)
	}
	waitFor(t, "claude", func() bool { return r.c.turnDones("claude") == 2 })
	time.Sleep(30 * time.Millisecond)
	if got := r.c.turnDones("codex"); got != 1 {
		t.Fatalf("the held relay was delivered anyway (codex turns = %d)", got)
	}
	// ... but it is still in the log, so codex sees it next time
	_ = r.m.postAt(tid, "$codex status?")
	waitFor(t, "codex", func() bool { return r.c.turnDones("codex") == 2 })
	has(t, r.codex.prompts("codex")[1], "[claude → codex] two", "[tester] status?")
}

func TestUnknownRecipientErrorsAndIsNotAHop(t *testing.T) {
	r := newRig(t, []fake.Script{turn(
		sendTo("nobody", "hi"), sendTo("", "hi"), sendTo("claude", "me"), sendTo("codex", "   "),
		say("tried"), finish())}, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	_ = r.m.postAt(tid, "$claude go")
	waitFor(t, "claude", func() bool { return r.c.turnDones("claude") == 1 })
	res := r.c.toolResults("claude", "message")
	if len(res) != 4 {
		t.Fatalf("results = %q", res)
	}
	has(t, res[0], `unknown recipient "nobody"`, "codex, all")
	has(t, res[1], "missing recipient", "codex, all")
	has(t, res[2], "yourself")
	has(t, res[3], "empty message")
	if ti, _ := r.m.Thread(tid); ti.Hops != 0 {
		t.Fatalf("failed calls counted as hops: %d", ti.Hops)
	}
	if len(r.codex.prompts("codex")) != 0 {
		t.Fatal("codex must not have been messaged")
	}
	for _, e := range r.c.events(func(e EventMsg) bool {
		return e.Tool != nil && e.Tool.Name == "message" && e.Kind == agent.KindToolResult
	}) {
		if e.Tool.Status != agent.ToolError {
			t.Errorf("tool result not an error: %+v", e.Tool)
		}
	}
}

func TestMessageToExitedAgentFailsFast(t *testing.T) {
	r := newRig(t, []fake.Script{turn(sendTo("codex", "hello?"), sendTo("all", "anyone?"), say("done"), finish())}, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	_ = r.codex.run("codex").Close()
	waitFor(t, "codex exit", func() bool { a, _ := r.m.Agent("codex"); return a.Status == StatusExited })

	start := time.Now()
	_ = r.m.postAt(tid, "$claude go")
	waitFor(t, "claude", func() bool { return r.c.turnDones("claude") == 1 })
	if time.Since(start) > 3*time.Second {
		t.Fatal("answer took too long")
	}
	res := r.c.toolResults("claude", "message")
	if len(res) != 2 {
		t.Fatalf("results = %q", res)
	}
	has(t, res[0], "codex is not running")
	has(t, res[1], "nobody else")
	// By name, an exited member is refused; by kind, a fresh one starts.
	if err := r.m.Rename("codex", "rev"); err != nil {
		t.Fatal(err)
	}
	if err := r.m.postAt(tid, "$rev are you there"); err == nil || !strings.Contains(err.Error(), "exited") {
		t.Fatalf("post to an exited member: %v", err)
	}
	if err := r.m.postAt(tid, "$codex"); err != nil {
		t.Fatalf("post to an exited kind: %v", err)
	}
	if a, ok := r.m.Agent("codex2"); !ok || a.Status == StatusExited {
		t.Fatalf("no fresh codex: %+v", a)
	}
}

func TestEchoLoopIsDroppedWithNotice(t *testing.T) {
	r := newRig(t, []fake.Script{turn(sendTo("codex", "Please  review"), sendTo("codex", "please review"), sendTo("codex", "something else"), say("ok"), finish())}, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	worksFor(t, r.m, "codex", "claude") // plain messages between them start turns
	_ = r.m.postAt(tid, "$claude go")
	waitFor(t, "claude", func() bool { return r.c.turnDones("claude") == 1 })
	waitFor(t, "codex to settle", func() bool { return r.c.turnDones("codex") >= 1 })
	res := r.c.toolResults("claude", "message")
	if len(res) != 3 || !strings.HasPrefix(res[1], "dropped") || strings.HasPrefix(res[0], "dropped") || strings.HasPrefix(res[2], "dropped") {
		t.Fatalf("results = %q", res)
	}
	if n := r.c.count(func(m tea.Msg) bool { n, ok := m.(NoticeMsg); return ok && strings.Contains(n.Text, "repeated") }); n != 1 {
		t.Fatalf("notices = %d", n)
	}
	if got := len(r.c.relays()); got != 2 {
		t.Fatalf("relays = %d, want the duplicate gone", got)
	}
}

// "@name" lines in a reply are text: agents reach each other through ops.
func TestAMentionInAReplyIsText(t *testing.T) {
	r := newRig(t, []fake.Script{turn(say("$codex review this\n\n@codex and this\n\n$all everyone"), finish())}, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	_ = r.m.postAt(tid, "$claude go")
	waitFor(t, "claude", func() bool { return r.c.turnDones("claude") == 1 })
	time.Sleep(30 * time.Millisecond)
	if n := len(r.c.relays()); n != 0 {
		t.Fatalf("relays = %+v", r.c.relays())
	}
}

// ---- briefing details ----

func TestUserNameHiddenUnlessAllowed(t *testing.T) {
	r := newRig(t, nil, nil)
	cfg := testCfg()
	cfg.TellAgentsName = false
	r.m.SetConfig(cfg)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	_ = r.m.postAt(tid, "$claude one")
	waitFor(t, "claude", func() bool { return r.c.turnDones("claude") == 1 })
	_ = r.m.postAt(tid, "$codex two")
	waitFor(t, "codex", func() bool { return r.c.turnDones("codex") == 1 })
	p := r.codex.prompts("codex")[0]
	has(t, p, "[user → claude] one", "[user] two")
	lacks(t, p, "tester")
	sp, _ := r.claude.spec("claude")
	lacks(t, sp.Instructions, "tester")
}

func TestOpenTurnsAreHeldBackThenDeliveredOnce(t *testing.T) {
	block := fake.Step{
		Event:    agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{ID: "t", Name: "Bash", Title: "make", Status: agent.ToolRunning}},
		Approval: &agent.ApprovalRequest{ID: "ap-open", Kind: agent.ApproveCommand, ToolName: "Bash", Summary: "make"},
	}
	r := newRig(t, []fake.Script{turn(say("half-baked thought"), block, say("FINAL claude answer"), finish())}, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	_ = r.m.postAt(tid, "$claude work")
	waitFor(t, "approval", func() bool { return len(r.c.approvals()) == 1 })
	_ = r.m.postAt(tid, "$codex what is claude doing?")
	waitFor(t, "codex", func() bool { return r.c.turnDones("codex") == 1 })
	first := r.codex.prompts("codex")[0]
	lacks(t, first, "half-baked thought", "FINAL")

	r.c.approvals()[0].Respond(agent.Decision{Allow: true})
	waitFor(t, "claude", func() bool { return r.c.turnDones("claude") == 1 })
	_ = r.m.postAt(tid, "$codex and now?")
	waitFor(t, "codex again", func() bool { return r.c.turnDones("codex") == 2 })
	second := r.codex.prompts("codex")[1]
	has(t, second, "[claude] FINAL claude answer", "[tester] and now?")
	lacks(t, second, "half-baked thought", "what is claude doing") // already delivered / not narration
}

func TestReadThreadTool(t *testing.T) {
	r := newRig(t, nil, []fake.Script{turn(fake.Step{ReadThread: true}, say("read it"), finish())})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	_ = r.m.postAt(tid, "$claude hello there")
	waitFor(t, "claude", func() bool { return r.c.turnDones("claude") == 1 })
	_ = r.m.postAt(tid, "$codex read the thread")
	waitFor(t, "codex", func() bool { return r.c.turnDones("codex") == 1 })
	out := r.c.toolResults("codex", "read")
	if len(out) != 1 {
		t.Fatalf("thread.read results = %q", out)
	}
	has(t, out[0], "claude, codex", "[tester → claude] hello there", "[tester → codex (you)] read the thread")
	if got := r.m.readThread("nobody"); got != "" {
		t.Fatalf("unknown agent: %q", got)
	}
}

// TestExecuteSkillsUsed: an op run through the hub sees the skills another
// member used in the thread.
func TestExecuteSkillsUsed(t *testing.T) {
	skill := fake.Step{Event: agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{
		ID: "s1", Name: "Skill", Title: "pdf", Input: json.RawMessage(`{"skill":"pdf"}`), Status: agent.ToolRunning,
	}}}
	r := newRig(t,
		[]fake.Script{turn(skill, say("used it"), finish())},
		[]fake.Script{turn(fake.Step{Exec: &fake.Exec{Op: "skills.used"}}, fake.Step{Exec: &fake.Exec{Op: "no.such.op"}}, say("ok"), finish())})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	_ = r.m.postAt(tid, "$claude make a pdf")
	waitFor(t, "claude", func() bool { return r.c.turnDones("claude") == 1 })
	_ = r.m.postAt(tid, "$codex which skills were used?")
	waitFor(t, "codex", func() bool { return r.c.turnDones("codex") == 1 })
	out := r.c.toolResults("codex", "deck")
	if len(out) != 2 {
		t.Fatalf("deck results = %q", out)
	}
	has(t, out[0], "pdf - used by claude (Skill tool)")
	has(t, out[1], "Unknown op", "search")
	if txt, isErr := r.m.execOp("nobody", "git.status", nil); !isErr || txt == "" {
		t.Fatalf("unknown agent: %q %v", txt, isErr)
	}
}

// ---- persistence: resume and migration ----

func TestResumeTwoMemberThread(t *testing.T) {
	claude := []fake.Script{
		turn(started("sess-claude"), sendTo("codex", "review the plan"), say("asked codex"), cost(0.09), finish()),
		turn(say("thanks"), cost(0.02), finish()),
	}
	codex := []fake.Script{
		turn(started("sess-codex"), sendTo("claude", "one change please"), say("Fine."), cost(0.04), finish()),
	}
	r := newRig(t, claude, codex)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	worksFor(t, r.m, "codex", "claude") // plain messages between them start turns
	_ = r.m.postAt(tid, "$claude plan it")
	waitFor(t, "exchange", func() bool { return r.c.turnDones("claude") == 2 })
	if err := r.m.Rename("codex", "reviewer"); err != nil {
		t.Fatal(err)
	}
	r.m.Close()

	r2 := newRigIn(t, r.dir, nil, nil)
	ti, hist, err := r2.m.Resume(tid)
	if err != nil {
		t.Fatal(err)
	}
	if len(ti.Members) != 2 || ti.Members[0].ID != "claude" || ti.Members[1].ID != "codex" || ti.Members[1].Name != "reviewer" {
		t.Fatalf("members = %+v", ti.Members)
	}
	if math.Abs(ti.CostUSD-0.15) > 1e-9 {
		t.Errorf("cost = %v", ti.CostUSD)
	}
	if sp, _ := r2.claude.spec("claude"); sp.ResumeID != "sess-claude" || sp.FirstPrompt != "" {
		t.Errorf("claude spec = %+v", sp)
	}
	if sp, _ := r2.codex.spec("codex"); sp.ResumeID != "sess-codex" {
		t.Errorf("codex spec = %+v", sp)
	}
	var users, relays, events int
	for _, h := range hist.Msgs() {
		switch v := h.(type) {
		case EventMsg:
			events++
			if v.FromUser() {
				users++
			}
			if v.ThreadID != tid {
				t.Errorf("history thread id = %q", v.ThreadID)
			}
		case RelayMsg:
			relays++
		}
	}
	if users != 1 || relays != 2 || events < 6 {
		t.Errorf("history: users=%d relays=%d events=%d", users, relays, events)
	}
	if again, h2, err := r2.m.Resume(tid); err != nil || again.ID != tid || h2.Msgs() != nil {
		t.Fatalf("resuming a running thread: %+v %v %v", again, h2, err)
	}

	// cursors survived: claude is briefed only on what came after its last
	// turn (codex's reply, which ended after its thread.send woke claude)
	if err := r2.m.postAt(tid, "$claude again"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "claude after resume", func() bool { return r2.c.turnDones("claude") == 1 })
	if got := r2.claude.prompts("claude"); len(got) != 1 || !strings.HasSuffix(got[0], "again") || strings.Contains(got[0], "plan it") {
		t.Fatalf("claude prompt after resume = %q", got)
	}
	// codex has not seen "again"; the name survived, so $reviewer reaches it
	if err := r2.m.postAt(tid, "$reviewer and you?"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "codex after resume", func() bool { return r2.c.turnDones("codex") == 1 })
	p := r2.codex.prompts("codex")[0]
	has(t, p, "[tester → claude] again", "[tester] and you?")
	lacks(t, p, "review the plan") // codex saw that before the restart
}

func TestResumeRemapsIDsThatAreTaken(t *testing.T) {
	r := newRig(t, nil, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	_ = r.m.postAt(tid, "$claude hi")
	waitFor(t, "claude", func() bool { return r.c.turnDones("claude") == 1 })
	r.m.Flush()

	// a second manager over the same store already runs an agent called "claude"
	r2 := newRigIn(t, r.dir, nil, nil)
	t2 := r2.thread(t)
	r2.add(t, t2, "claude")
	ti, hist, err := r2.m.Resume(tid)
	if err != nil {
		t.Fatal(err)
	}
	if ti.Members[0].ID == "claude" {
		t.Fatalf("id collision: %+v", ti.Members)
	}
	for _, h := range hist.Msgs() {
		if e, ok := h.(EventMsg); ok && !e.FromUser() && e.AgentID != ti.Members[0].ID {
			t.Fatalf("history event still carries %q", e.AgentID)
		}
		if e, ok := h.(EventMsg); ok && e.FromUser() && e.To[0] != ti.Members[0].ID {
			t.Fatalf("user message still addressed to %q", e.To[0])
		}
	}
}

func TestOldSingleAgentThreadMigrates(t *testing.T) {
	dir := t.TempDir()
	meta := `{"id":"old1","agent_kind":"claude","name":"claude","title":"fix it","dir":"` + strings.ReplaceAll(t.TempDir(), `\`, `\\`) +
		`","model":"m1","session_id":"sess-old","created":"2026-01-01T00:00:00Z","updated":"2026-01-01T00:00:00Z","cost_usd":0.12}`
	log := `{"AgentID":"claude","Time":"2026-01-01T00:00:00Z","Kind":"user_text","Text":"fix it"}` + "\n" +
		`{"AgentID":"claude","Time":"2026-01-01T00:00:01Z","Kind":"assistant_text","Text":"fixed"}` + "\n" +
		`{"AgentID":"claude","Time":"2026-01-01T00:00:02Z","Kind":"turn_done"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "old1.meta.json"), []byte(meta), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "old1.jsonl"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	r := newRigIn(t, dir, nil, nil)
	if saved := r.m.Saved(); len(saved) != 1 || len(saved[0].Members) != 1 {
		t.Fatalf("saved = %+v", saved)
	}
	ti, hist, err := r.m.Resume("old1")
	if err != nil {
		t.Fatal(err)
	}
	if len(ti.Members) != 1 || ti.Members[0].ID != "claude" || ti.Members[0].Model != "m1" || math.Abs(ti.CostUSD-0.12) > 1e-9 {
		t.Fatalf("thread = %+v", ti)
	}
	sp, _ := r.claude.spec("claude")
	if sp.ResumeID != "sess-old" {
		t.Fatalf("spec = %+v", sp)
	}
	if len(hist.Msgs()) != 3 {
		t.Fatalf("history = %+v", hist.Msgs())
	}
	if first, ok := hist.Msgs()[0].(EventMsg); !ok || !first.FromUser() || first.Text != "fix it" || first.To[0] != "claude" {
		t.Fatalf("first history message = %+v", hist.Msgs()[0])
	}
	if err := r.m.Post("old1", "next please"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "turn", func() bool { return r.c.turnDones("claude") == 1 })
	if got := r.claude.prompts("claude"); len(got) != 1 || got[0] != "next please" {
		t.Fatalf("prompts = %q (the old agent has seen the whole old log)", got)
	}
	r.m.Flush()
	_, th, _ := r.st.Load("old1")
	if th.Version != store.CurrentVersion || len(th.Members) != 1 || th.Members[0].SessionID != "sess-old" {
		t.Fatalf("migrated meta = %+v", th)
	}
}

// ---- the demo duo, end to end ----

func TestDemoDuoCollaborates(t *testing.T) {
	adapters, hub := fake.DemoDuo(0)
	st, _ := store.New(t.TempDir())
	m := NewManager(adapters, st, testCfg(), Options{Hub: hub, MCPFor: fake.MCPFor})
	t.Cleanup(m.Close)
	c := collect(t, m)
	tid, _ := m.NewThread(t.TempDir())
	// codex first: claude's first turn sends to it, and the fakes play
	// instantly, so codex must be in the thread before claude starts.
	if err := m.postAt(tid, "$codex"); err != nil { // add codex without a message
		t.Fatal(err)
	}
	if err := m.postAt(tid, "$claude plan the store change"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "claude's edit approval", func() bool { return len(c.approvals()) == 1 })
	ap := c.approvals()[0]
	if ap.AgentID != "claude" || ap.ThreadID != tid {
		t.Fatalf("approval = %+v", ap.ApprovalRequest)
	}
	if !m.Busy(tid) {
		t.Fatal("Busy is false while claude waits for an approval")
	}
	ap.Respond(agent.Decision{Allow: true})
	waitFor(t, "everyone idle", func() bool {
		return c.turnDones("claude") == 2 && c.turnDones("codex") == 1
	})
	if m.Busy(tid) || m.Busy("no-such-thread") {
		t.Fatal("Busy is true with everyone idle")
	}
	// claude's hand-off, then codex's reply as the task's result.
	rel := c.relays()
	if len(rel) != 2 || rel[0].Via != "tool" || rel[1].Via != "task" || rel[1].From != "codex" {
		t.Fatalf("relays = %+v", rel)
	}
	if tk, ok := m.Task(tid, "t1"); !ok || tk.Status != store.TaskDone || tk.Contract.Objective == "" {
		t.Fatalf("task = %+v", tk)
	}
	out := c.toolResults("codex", "read")
	if len(out) != 1 || !strings.Contains(out[0], "Plan for the store change") {
		t.Fatalf("thread.read = %q", out)
	}
	ti, _ := m.Thread(tid)
	if math.Abs(ti.CostUSD-0.19) > 1e-9 || ti.Paused || ti.Hops != 1 {
		t.Fatalf("thread = %+v", ti)
	}
	for _, a := range ti.Members {
		if a.Status != StatusIdle {
			t.Errorf("%s is %s", a.ID, a.Status)
		}
	}
}

// slowStart holds the first Start of its adapter until release closes.
type slowStart struct {
	agent.Adapter
	starts           atomic.Int32
	entered, release chan struct{}
}

func (s *slowStart) Start(ctx context.Context, sp agent.Spec) (agent.Run, error) {
	if s.starts.Add(1) == 1 {
		close(s.entered)
		<-s.release
	}
	return s.Adapter.Start(ctx, sp)
}

func TestAMessageToAMemberThatIsStartingStartsOneCLI(t *testing.T) {
	hub := fake.NewHub()
	codex := &slowStart{Adapter: fake.NewTurns(0, hub, turn(say("got it"), finish())), entered: make(chan struct{}), release: make(chan struct{})}
	m := NewManager(map[string]agent.Adapter{"claude": fake.NewTurns(0, hub), "codex": codex}, nil, testCfg(), Options{Hub: hub, MCPFor: fake.MCPFor})
	t.Cleanup(m.Close)
	c := collect(t, m)
	tid, _ := m.NewThread(t.TempDir())
	errs := make(chan error, 2)
	go func() { errs <- m.postAt(tid, "$codex") }()
	<-codex.entered // codex is in the thread; its CLI is starting
	go func() { errs <- m.postAt(tid, "$codex hello") }()
	time.Sleep(20 * time.Millisecond) // the message reaches codex while it starts
	close(codex.release)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "codex's turn", func() bool { return c.turnDones("codex") == 1 })
	if n := codex.starts.Load(); n != 1 {
		t.Fatalf("codex's CLI started %d times", n)
	}
}

// meeting holds each Start until want of them have begun, like a handshake
// that is slow: members started one after another never meet.
type meeting struct {
	want int32
	n    atomic.Int32
	all  chan struct{}
}

type meetStart struct {
	agent.Adapter
	m *meeting
}

func (s meetStart) Start(ctx context.Context, sp agent.Spec) (agent.Run, error) {
	if s.m.n.Add(1) == s.m.want {
		close(s.m.all)
	}
	select {
	case <-s.m.all:
	case <-time.After(2 * time.Second):
		return nil, fmt.Errorf("%s started alone", sp.ID)
	}
	return s.Adapter.Start(ctx, sp)
}

func TestResumeStartsMembersTogetherInRosterOrder(t *testing.T) {
	r := newRig(t, nil, nil)
	tid := r.thread(t)
	r.add(t, tid, "codex")
	r.add(t, tid, "claude")
	r.m.Close()

	st, err := store.New(r.dir)
	if err != nil {
		t.Fatal(err)
	}
	meet := &meeting{want: 2, all: make(chan struct{})}
	m := NewManager(map[string]agent.Adapter{
		"claude": meetStart{fake.NewTurns(0, nil), meet},
		"codex":  meetStart{fake.NewTurns(0, nil), meet},
	}, st, testCfg())
	t.Cleanup(m.Close)
	ti, _, err := m.Resume(tid)
	if err != nil {
		t.Fatal(err)
	}
	if len(ti.Members) != 2 || ti.Members[0].ID != "codex" || ti.Members[1].ID != "claude" {
		t.Fatalf("members = %+v", ti.Members)
	}
}
