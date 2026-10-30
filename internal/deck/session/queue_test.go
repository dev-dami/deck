package session

import (
	"math"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/store"
)

// blockStep holds a turn open until its approval is answered.
func blockStep(id string) fake.Step {
	return fake.Step{
		Event:    agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{ID: id, Name: "Bash", Title: "make", Status: agent.ToolRunning}},
		Approval: &agent.ApprovalRequest{ID: id, Kind: agent.ApproveCommand, ToolName: "Bash", Summary: "make"},
	}
}

func (c *collector) queueMsgs() []QueueChangedMsg {
	var out []QueueChangedMsg
	for _, m := range c.all() {
		if q, ok := m.(QueueChangedMsg); ok {
			out = append(out, q)
		}
	}
	return out
}

// lastQueue is the items of the most recent QueueChangedMsg (nil when none).
func (c *collector) lastQueue() []QueuedItem {
	qs := c.queueMsgs()
	if len(qs) == 0 {
		return nil
	}
	return qs[len(qs)-1].Items
}

func (c *collector) edited() []EditedMsg {
	var out []EditedMsg
	for _, m := range c.all() {
		if e, ok := m.(EditedMsg); ok {
			out = append(out, e)
		}
	}
	return out
}

func imgs(names ...string) []agent.Image {
	var out []agent.Image
	for _, n := range names {
		out = append(out, agent.Image{Path: "/tmp/" + n, MediaType: "image/png"})
	}
	return out
}

// busyClaude is a rig with one claude whose first turn waits for an approval,
// already started by "$claude build it".
func busyClaude(t *testing.T, claudeSteps ...fake.Script) (*rig, string) {
	t.Helper()
	scripts := append([]fake.Script{turn(blockStep("ap1"), say("built"), finish())}, claudeSteps...)
	r := newRig(t, scripts, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	if err := r.m.postAt(tid, "$claude build it"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "approval", func() bool { return len(r.c.approvals()) == 1 })
	return r, tid
}

// ---- images ----

func TestPostWithImagesGoesToImageSendersOnly(t *testing.T) {
	r := newRig(t, []fake.Script{turn(say("seen"), finish())}, []fake.Script{turn(say("heard"), finish())})
	r.claude.images = true // codex runs cannot take images
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")

	res, err := r.m.postAddressed(tid, "$all look at these", imgs("a.png", "b.png"))
	if err != nil {
		t.Fatal(err)
	}
	if res.EntryID == "" || len(res.Queued) != 0 {
		t.Fatalf("result = %+v", res)
	}
	waitFor(t, "both turns", func() bool { return r.c.turnDones("claude") == 1 && r.c.turnDones("codex") == 1 })

	calls := r.claude.imageCalls("claude")
	if len(calls) != 1 || len(calls[0].images) != 2 || calls[0].images[1].Path != "/tmp/b.png" {
		t.Fatalf("claude image calls = %+v", calls)
	}
	has(t, calls[0].prompt, "look at these")
	lacks(t, calls[0].prompt, "could not pass")

	p := r.codex.prompts("codex")
	if len(p) != 1 {
		t.Fatalf("codex prompts = %q", p)
	}
	has(t, p[0], "look at these", "[deck] The user attached 2 image(s) Deck could not pass to you directly; open them with your own file tools if you can: /tmp/a.png, /tmp/b.png")

	// the user message carries the id and the image paths, live and stored
	user := r.c.events(func(e EventMsg) bool { return e.FromUser() })
	if len(user) != 1 || user[0].EntryID != res.EntryID || len(user[0].Images) != 2 || user[0].Images[0] != "/tmp/a.png" {
		t.Fatalf("user event = %+v", user)
	}
	r.m.Flush()
	entries, _, _ := r.st.LoadEntries(tid)
	var found bool
	for _, e := range entries {
		if e.Type == store.EntryUser && e.ID == res.EntryID {
			found = len(e.Images) == 2
		}
	}
	if !found {
		t.Error("stored user entry lacks the image paths or id")
	}
}

func TestPostIsPostWithoutImages(t *testing.T) {
	r := newRig(t, []fake.Script{turn(say("ok"), finish())}, nil)
	r.claude.images = true
	tid := r.thread(t)
	r.add(t, tid, "claude")
	if err := r.m.postAt(tid, "$claude hi"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "turn", func() bool { return r.c.turnDones("claude") == 1 })
	if len(r.claude.imageCalls("claude")) != 0 {
		t.Error("a post without images used SendWithImages")
	}
}

// ---- queue ----

func TestQueueLifecycleAndPostResult(t *testing.T) {
	r, tid := busyClaude(t, turn(say("handled"), finish()))
	if q := r.m.Queue(tid); len(q) != 0 {
		t.Fatalf("queue = %+v", q)
	}
	res, err := r.m.postAddressed(tid, "$claude also lint", imgs("l.png"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Queued) != 1 {
		t.Fatalf("Queued = %+v", res.Queued)
	}
	it := res.Queued[0]
	if it.ThreadID != tid || it.AgentID != "claude" || it.Text != "also lint" || it.Edit || it.Steer ||
		it.EntryID != res.EntryID || it.ID == "" || it.At.IsZero() || len(it.Images) != 1 {
		t.Fatalf("item = %+v", it)
	}
	if q := r.m.Queue(tid); len(q) != 1 || q[0].ID != it.ID {
		t.Fatalf("Queue = %+v", q)
	}
	waitFor(t, "QueueChangedMsg with the item", func() bool { got := r.c.lastQueue(); return len(got) == 1 && got[0].ID == it.ID })

	res2, _ := r.m.postAddressed(tid, "$claude and vet", nil)
	q := r.m.Queue(tid)
	if len(q) != 2 || q[0].Text != "also lint" || q[1].Text != "and vet" || q[1].ID != res2.Queued[0].ID {
		t.Fatalf("Queue = %+v", q)
	}
	waitFor(t, "QueueChangedMsg with two items", func() bool { return len(r.c.lastQueue()) == 2 })

	// delivered when the turn ends: one coalesced prompt, queue empties
	r.c.approvals()[0].Respond(agent.Decision{Allow: true})
	waitFor(t, "second turn", func() bool { return r.c.turnDones("claude") == 2 })
	waitFor(t, "queue empty", func() bool { return len(r.m.Queue(tid)) == 0 })
	waitFor(t, "empty QueueChangedMsg", func() bool {
		qs := r.c.queueMsgs()
		return len(qs) > 0 && len(qs[len(qs)-1].Items) == 0
	})
	got := r.claude.prompts("claude")
	if len(got) != 2 {
		t.Fatalf("prompts = %q", got)
	}
	has(t, got[1], "also lint", "and vet")
}

func TestRelaysAreNotQueueItems(t *testing.T) {
	r := newRig(t, []fake.Script{turn(sendTo("codex", "ping"), say("sent"), finish())},
		[]fake.Script{turn(blockStep("apx"), say("done"), finish()), turn(say("saw it"), finish())})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	_ = r.m.postAt(tid, "$codex start")
	waitFor(t, "codex approval", func() bool { return len(r.c.approvals()) == 1 })
	_ = r.m.postAt(tid, "$claude go")
	waitFor(t, "claude", func() bool { return r.c.turnDones("claude") == 1 })
	if q := r.m.Queue(tid); len(q) != 0 {
		t.Fatalf("a relay appeared in the user queue: %+v", q)
	}
	r.c.approvals()[0].Respond(agent.Decision{Allow: true})
	waitFor(t, "codex", func() bool { return r.c.turnDones("codex") == 2 })
}

func TestSteerInjectsIntoRunningTurn(t *testing.T) {
	r := newRig(t, []fake.Script{
		turn(blockStep("ap1"), say("built"), finish()),
		turn(say("changed course"), finish()),
	}, nil)
	r.claude.steer = true
	tid := r.thread(t)
	r.add(t, tid, "claude")
	if err := r.m.postAt(tid, "$claude build it"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "approval", func() bool { return len(r.c.approvals()) == 1 })
	res, err := r.m.postAddressed(tid, "$claude actually write docs instead", imgs("d.png"))
	if err != nil || len(res.Queued) != 1 {
		t.Fatalf("post: %+v %v", res, err)
	}
	if err := r.m.Steer(res.Queued[0].ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "turn after steer", func() bool { return r.c.turnDones("claude") == 1 })

	calls := r.claude.steerCalls("claude")
	if len(calls) != 1 {
		t.Fatalf("steer calls = %+v", calls)
	}
	if want := "[deck] The user is redirecting you mid-task:\nactually write docs instead"; !strings.Contains(calls[0].prompt, want) {
		t.Fatalf("steer prompt = %q", calls[0].prompt)
	}
	if len(calls[0].images) != 1 || calls[0].images[0].Path != "/tmp/d.png" {
		t.Errorf("steer images = %+v", calls[0].images)
	}
	if p := r.claude.prompts("claude"); len(p) != 1 {
		t.Errorf("a Steerer must not get Send: prompts = %q", p)
	}
	if q := r.m.Queue(tid); len(q) != 0 {
		t.Errorf("queue after steer = %+v", q)
	}
	waitFor(t, "empty QueueChangedMsg", func() bool { return len(r.c.lastQueue()) == 0 })
	steers := r.c.events(func(e EventMsg) bool { return e.FromUser() && e.Steer })
	if len(steers) != 1 || steers[0].SteerOf != res.EntryID || steers[0].EntryID == "" || steers[0].Text != "actually write docs instead" ||
		len(steers[0].To) != 1 || steers[0].To[0] != "claude" {
		t.Fatalf("steer event = %+v", steers)
	}

	// a steer is stored as a flagged user entry, and replays as one
	r.m.Flush()
	entries, _, _ := r.st.LoadEntries(tid)
	var stored bool
	for _, e := range entries {
		if e.Type == store.EntryUser && e.Steer && e.Ref == res.EntryID {
			stored = true
		}
	}
	if !stored {
		t.Error("steer entry not stored")
	}
	var replay int
	for _, h := range r.m.History(tid) {
		if e, ok := h.(EventMsg); ok && e.Steer && e.SteerOf == res.EntryID {
			replay++
		}
	}
	if replay != 1 {
		t.Errorf("History steer events = %d", replay)
	}
	// the redirected agent does not get the message again later
	_ = r.m.postAt(tid, "$claude thanks")
	waitFor(t, "next turn", func() bool { return r.c.turnDones("claude") == 2 })
	p := r.claude.prompts("claude")
	lacks(t, p[len(p)-1], "write docs")
}

func TestSteerFallsBackToInterruptThenSend(t *testing.T) {
	r, tid := busyClaude(t, turn(say("changed course"), finish()))
	r.m.postAt(tid, "$claude first extra")
	res, _ := r.m.postAddressed(tid, "$claude switch to docs", nil)
	if len(r.m.Queue(tid)) != 2 {
		t.Fatalf("queue = %+v", r.m.Queue(tid))
	}
	if err := r.m.Steer(res.Queued[0].ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "redirected turn", func() bool { return r.c.turnDones("claude") == 2 })
	waitFor(t, "queue empty", func() bool { return len(r.m.Queue(tid)) == 0 })

	p := r.claude.prompts("claude")
	if len(p) != 2 {
		t.Fatalf("prompts = %q (the interrupt must not deliver the queue by itself)", p)
	}
	has(t, p[1], "[deck] The user is redirecting you mid-task:\nswitch to docs", "first extra")
	if n := strings.Count(p[1], "switch to docs"); n != 1 {
		t.Errorf("steered text appears %d times:\n%s", n, p[1])
	}
	// the first turn was interrupted, the stale approval is gone
	gone := r.c.count(func(m tea.Msg) bool { _, ok := m.(ApprovalGoneMsg); return ok })
	if gone != 1 {
		t.Errorf("approval-gone messages = %d", gone)
	}
	if len(r.c.events(func(e EventMsg) bool { return e.FromUser() && e.Steer })) != 1 {
		t.Error("no steer event")
	}
}

func TestSteerUnknownItemAndIdleMember(t *testing.T) {
	r := newRig(t, []fake.Script{turn(say("ok"), finish())}, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	if err := r.m.Steer("nope"); err == nil {
		t.Error("Steer of an unknown item should fail")
	}
	if err := r.m.CancelQueued("nope"); err == nil {
		t.Error("CancelQueued of an unknown item should fail")
	}
}

func TestCancelQueued(t *testing.T) {
	r, tid := busyClaude(t, turn(say("handled"), finish()))
	a, _ := r.m.postAddressed(tid, "$claude cancel me", nil)
	b, _ := r.m.postAddressed(tid, "$claude keep me", nil)
	if err := r.m.CancelQueued(a.Queued[0].ID); err != nil {
		t.Fatal(err)
	}
	q := r.m.Queue(tid)
	if len(q) != 1 || q[0].ID != b.Queued[0].ID {
		t.Fatalf("queue = %+v", q)
	}
	waitFor(t, "QueueChangedMsg after cancel", func() bool { return len(r.c.lastQueue()) == 1 })
	waitFor(t, "QueueCancelledMsg", func() bool {
		return r.c.count(func(m tea.Msg) bool {
			qc, ok := m.(QueueCancelledMsg)
			return ok && qc == QueueCancelledMsg{ThreadID: tid, AgentID: "claude", ItemID: a.Queued[0].ID}
		}) == 1
	})
	if err := r.m.CancelQueued(a.Queued[0].ID); err == nil {
		t.Error("cancelling twice should fail")
	}
	r.c.approvals()[0].Respond(agent.Decision{Allow: true})
	waitFor(t, "second turn", func() bool { return r.c.turnDones("claude") == 2 })
	p := r.claude.prompts("claude")
	if len(p) != 2 {
		t.Fatalf("prompts = %q", p)
	}
	has(t, p[1], "keep me")
	lacks(t, p[1], "cancel me")
}

func TestCancelledOnlyMessageStartsNoTurn(t *testing.T) {
	r, tid := busyClaude(t)
	a, _ := r.m.postAddressed(tid, "$claude cancel me", nil)
	if err := r.m.CancelQueued(a.Queued[0].ID); err != nil {
		t.Fatal(err)
	}
	r.c.approvals()[0].Respond(agent.Decision{Allow: true})
	waitFor(t, "first turn", func() bool { return r.c.turnDones("claude") == 1 })
	if p := r.claude.prompts("claude"); len(p) != 1 {
		t.Fatalf("a cancelled message started a turn: %q", p)
	}
	// and the next briefing never mentions it
	_ = r.m.postAt(tid, "$claude next")
	waitFor(t, "next turn", func() bool { return r.c.turnDones("claude") == 2 })
	p := r.claude.prompts("claude")
	lacks(t, p[len(p)-1], "cancel me")
}

func TestInterruptClearsTheQueue(t *testing.T) {
	r, tid := busyClaude(t)
	_, _ = r.m.postAddressed(tid, "$claude later", nil)
	if err := r.m.Interrupt("claude"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "turn done", func() bool { return r.c.turnDones("claude") == 1 })
	if q := r.m.Queue(tid); len(q) != 0 {
		t.Fatalf("queue after interrupt = %+v", q)
	}
	waitFor(t, "empty QueueChangedMsg", func() bool { return len(r.c.lastQueue()) == 0 })
}

// ---- edits ----

func TestEditIsDeliveredToTheOriginalAddressees(t *testing.T) {
	r := newRig(t, []fake.Script{turn(say("c1"), finish()), turn(say("c2"), finish())},
		[]fake.Script{turn(say("x1"), finish()), turn(say("x2"), finish())})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	res, err := r.m.postAddressed(tid, "$claude do X", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "first turn", func() bool { return r.c.turnDones("claude") == 1 })

	if err := r.m.Edit(tid, res.EntryID, "do Y"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "edit turn", func() bool { return r.c.turnDones("claude") == 2 })
	waitFor(t, "EditedMsg", func() bool { return len(r.c.edited()) == 1 })
	ed := r.c.edited()
	if len(ed) != 1 || ed[0] != (EditedMsg{ThreadID: tid, EntryID: res.EntryID, OldText: "do X", NewText: "do Y"}) {
		t.Fatalf("EditedMsg = %+v", ed)
	}
	p := r.claude.prompts("claude")
	if len(p) != 2 {
		t.Fatalf("claude prompts = %q", p)
	}
	want := "[deck] The user edited their earlier message.\nBefore: do X\nAfter: do Y\nTreat the edited version as what they meant."
	if !strings.Contains(p[1], want) {
		t.Fatalf("edit prompt = %q", p[1])
	}
	if len(r.codex.prompts("codex")) != 0 {
		t.Error("codex was not addressed but got the edit")
	}

	// edit again: Before is the previous edit
	if err := r.m.Edit(tid, res.EntryID, "do Z"); err != nil {
		t.Fatal(err)
	}
	// nothing changed: no-op
	if err := r.m.Edit(tid, res.EntryID, "do Z"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "second edit", func() bool { return r.c.turnDones("claude") == 3 })
	has(t, r.claude.prompts("claude")[2], "Before: do Y", "After: do Z")
	waitFor(t, "second EditedMsg", func() bool { return len(r.c.edited()) == 2 })
	if n := len(r.c.edited()); n != 2 {
		t.Errorf("EditedMsgs = %d", n)
	}

	// stored, and replayed after the message by History
	r.m.Flush()
	entries, _, _ := r.st.LoadEntries(tid)
	var edits []store.Entry
	for _, e := range entries {
		if e.Type == store.EntryEdit {
			edits = append(edits, e)
		}
	}
	if len(edits) != 2 || edits[0].Ref != res.EntryID || edits[0].Old != "do X" || edits[0].Text != "do Y" || len(edits[0].To) != 1 || edits[0].To[0] != "claude" {
		t.Fatalf("stored edits = %+v", edits)
	}
	var sawUser, sawEdit bool
	for _, h := range r.m.History(tid) {
		switch v := h.(type) {
		case EventMsg:
			if v.FromUser() && v.EntryID == res.EntryID {
				sawUser = true
			}
		case EditedMsg:
			if !sawUser {
				t.Error("EditedMsg replayed before its message")
			}
			sawEdit = true
		}
	}
	if !sawUser || !sawEdit {
		t.Errorf("history user=%v edit=%v", sawUser, sawEdit)
	}
}

func TestEditOfAQueuedMessageReplacesItInTheQueue(t *testing.T) {
	r, tid := busyClaude(t, turn(say("handled"), finish()))
	res, _ := r.m.postAddressed(tid, "$claude also lint", nil)
	if err := r.m.Edit(tid, res.EntryID, "also lint, then vet"); err != nil {
		t.Fatal(err)
	}
	q := r.m.Queue(tid)
	if len(q) != 1 || q[0].Text != "also lint, then vet" || q[0].Edit || q[0].ID != res.Queued[0].ID {
		t.Fatalf("queue = %+v", q)
	}
	waitFor(t, "QueueChangedMsg for the edit", func() bool {
		got := r.c.lastQueue()
		return len(got) == 1 && got[0].Text == "also lint, then vet"
	})
	waitFor(t, "EditedMsg", func() bool { return len(r.c.edited()) == 1 })
	if ed := r.c.edited(); ed[0].OldText != "also lint" || ed[0].NewText != "also lint, then vet" {
		t.Fatalf("EditedMsg = %+v", ed)
	}
	r.c.approvals()[0].Respond(agent.Decision{Allow: true})
	waitFor(t, "second turn", func() bool { return r.c.turnDones("claude") == 2 })
	p := r.claude.prompts("claude")
	if len(p) != 2 {
		t.Fatalf("prompts = %q", p)
	}
	has(t, p[1], "also lint, then vet")
	lacks(t, p[1], "edited their earlier message", "Before:")
	if strings.Count(p[1], "also lint") != 1 {
		t.Errorf("old text delivered as well:\n%s", p[1])
	}
}

func TestEditForABusyMemberIsQueuedAsAnEditItem(t *testing.T) {
	r, tid := busyClaude(t, turn(say("handled"), finish()))
	// the original ("build it") was delivered to claude, which is busy with it
	var orig string
	for _, h := range r.m.History(tid) {
		if e, ok := h.(EventMsg); ok && e.FromUser() {
			orig = e.EntryID
		}
	}
	if orig == "" {
		t.Fatal("no user message in history")
	}
	if err := r.m.Edit(tid, orig, "build it with -race"); err != nil {
		t.Fatal(err)
	}
	q := r.m.Queue(tid)
	if len(q) != 1 || !q[0].Edit || q[0].Text != "build it with -race" || q[0].EntryID != orig || q[0].AgentID != "claude" {
		t.Fatalf("queue = %+v", q)
	}
	waitFor(t, "QueueChangedMsg with the edit", func() bool { got := r.c.lastQueue(); return len(got) == 1 && got[0].Edit })
	r.c.approvals()[0].Respond(agent.Decision{Allow: true})
	waitFor(t, "edit turn", func() bool { return r.c.turnDones("claude") == 2 })
	p := r.claude.prompts("claude")
	if len(p) != 2 {
		t.Fatalf("prompts = %q", p)
	}
	has(t, p[1], "[deck] The user edited their earlier message.", "Before: build it", "After: build it with -race")
	waitFor(t, "queue empty", func() bool { return len(r.m.Queue(tid)) == 0 })
}

func TestSteeringAnEditItem(t *testing.T) {
	r, tid := busyClaude(t, turn(say("handled"), finish()))
	var orig string
	for _, h := range r.m.History(tid) {
		if e, ok := h.(EventMsg); ok && e.FromUser() {
			orig = e.EntryID
		}
	}
	_ = r.m.Edit(tid, orig, "build it with -race")
	q := r.m.Queue(tid)
	if len(q) != 1 {
		t.Fatalf("queue = %+v", q)
	}
	if err := r.m.Steer(q[0].ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "redirected turn", func() bool { return r.c.turnDones("claude") == 2 })
	p := r.claude.prompts("claude")
	has(t, p[1], "[deck] The user is redirecting you mid-task:", "Before: build it", "After: build it with -race")
	if n := strings.Count(p[1], "edited their earlier message"); n != 1 {
		t.Errorf("edit appears %d times:\n%s", n, p[1])
	}
}

func TestEditRejectsOtherEntries(t *testing.T) {
	r := newRig(t, []fake.Script{turn(sendTo("codex", "ping"), say("hello"), finish())}, []fake.Script{turn(say("pong"), finish())})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	_ = r.m.postAt(tid, "$claude go")
	waitFor(t, "exchange", func() bool { return r.c.turnDones("claude") == 1 && r.c.turnDones("codex") == 1 })

	th := r.m.thread(tid)
	th.mu.Lock()
	byType := map[string]string{}
	for _, e := range allLog(th) {
		key := e.Type
		if key == store.EntryEvent {
			key = "event:" + string(e.Kind)
		}
		byType[key] = e.ID
	}
	th.mu.Unlock()
	for _, key := range []string{"event:assistant_text", store.EntryRelay, store.EntryMember} {
		id := byType[key]
		if id == "" {
			t.Fatalf("no %s entry in %v", key, byType)
		}
		if err := r.m.Edit(tid, id, "changed"); err == nil || !strings.Contains(err.Error(), "only the user") {
			t.Errorf("Edit of a %s entry: %v", key, err)
		}
	}
	if err := r.m.Edit(tid, "no-such-entry", "x"); err == nil {
		t.Error("Edit of an unknown entry should fail")
	}
	if err := r.m.Edit("no-thread", "x", "y"); err == nil {
		t.Error("Edit in an unknown thread should fail")
	}
	if len(r.c.edited()) != 0 {
		t.Error("a rejected edit was announced")
	}
}

func TestEditsReplayOnResume(t *testing.T) {
	r := newRig(t, []fake.Script{turn(started("s1"), say("c1"), finish()), turn(say("c2"), finish())}, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	res, _ := r.m.postAddressed(tid, "$claude do X", nil)
	waitFor(t, "turn", func() bool { return r.c.turnDones("claude") == 1 })
	if err := r.m.Edit(tid, res.EntryID, "do Y"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "edit turn", func() bool { return r.c.turnDones("claude") == 2 })
	r.m.Flush()
	r.m.Close()

	r2 := newRigIn(t, r.dir, nil, nil)
	_, hist, err := r2.m.Resume(tid)
	if err != nil {
		t.Fatal(err)
	}
	var userAt, editAt = -1, -1
	for i, h := range hist.Msgs() {
		switch v := h.(type) {
		case EventMsg:
			if v.FromUser() && v.EntryID == res.EntryID {
				userAt = i
			}
		case EditedMsg:
			editAt = i
			if v.EntryID != res.EntryID || v.OldText != "do X" || v.NewText != "do Y" {
				t.Errorf("replayed edit = %+v", v)
			}
		}
	}
	if userAt < 0 || editAt < userAt {
		t.Fatalf("replay order: user=%d edit=%d", userAt, editAt)
	}
	// an edit after resume still works against the stored message id
	if err := r2.m.Edit(tid, res.EntryID, "do W"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "edit turn after resume", func() bool { return r2.c.turnDones("claude") == 1 })
	has(t, r2.claude.prompts("claude")[0], "Before: do Y", "After: do W")
}

// ---- context ----

func TestContextFieldsAreAggregatedFromUsageEvents(t *testing.T) {
	ctxOnly := func(tokens, window int) fake.Step {
		return fake.Step{Event: agent.Event{Kind: agent.KindUsage, Usage: &agent.Usage{ContextTokens: tokens, ContextWindow: window}}}
	}
	full := fake.Step{Event: agent.Event{Kind: agent.KindUsage, Usage: &agent.Usage{
		InputTokens: 10, OutputTokens: 5, CostUSD: 0.05, ContextTokens: 1500, ContextWindow: 200000,
	}}}
	r := newRig(t, []fake.Script{turn(ctxOnly(1000, 200000), say("a"), full, ctxOnly(2000, 0), cost(0), finish())}, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	if info, _ := r.m.Agent("claude"); info.ContextTokens != 0 || info.ContextWindow != 0 {
		t.Fatalf("before any usage: %+v", info)
	}
	_ = r.m.postAt(tid, "$claude go")
	waitFor(t, "turn", func() bool { return r.c.turnDones("claude") == 1 })
	info, _ := r.m.Agent("claude")
	if info.ContextTokens != 2000 || info.ContextWindow != 200000 {
		t.Errorf("context = %d/%d, want 2000/200000", info.ContextTokens, info.ContextWindow)
	}
	if math.Abs(info.CostUSD-0.05) > 1e-9 {
		t.Errorf("cost = %v: context-only and zero-cost usage events must not reset it", info.CostUSD)
	}
	ti, _ := r.m.Thread(tid)
	if ti.Members[0].ContextTokens != 2000 {
		t.Errorf("thread member = %+v", ti.Members[0])
	}

	// persisted with the member, so a resumed thread shows the meter at once
	r.m.Flush()
	_, meta, _ := r.st.LoadEntries(tid)
	if len(meta.Members) != 1 || meta.Members[0].ContextTokens != 2000 || meta.Members[0].ContextWindow != 200000 {
		t.Fatalf("stored member = %+v", meta.Members)
	}
	r.m.Close()
	r2 := newRigIn(t, r.dir, nil, nil)
	if _, _, err := r2.m.Resume(tid); err != nil {
		t.Fatal(err)
	}
	if info, _ := r2.m.Agent("claude"); info.ContextTokens != 2000 || info.ContextWindow != 200000 {
		t.Errorf("after resume: %+v", info)
	}
}

func TestMidTurnUsageKeepsTheTurnCost(t *testing.T) {
	ctxOnly := fake.Step{Event: agent.Event{Kind: agent.KindUsage, Usage: &agent.Usage{ContextTokens: 5, ContextWindow: 10}}}
	r := newRig(t, []fake.Script{turn(cost(0.3), ctxOnly, blockStep("apc"), finish())}, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	_ = r.m.postAt(tid, "$claude go")
	waitFor(t, "approval", func() bool { return len(r.c.approvals()) == 1 })
	if info, _ := r.m.Agent("claude"); math.Abs(info.CostUSD-0.3) > 1e-9 {
		t.Fatalf("mid-turn cost = %v", info.CostUSD)
	}
	r.c.approvals()[0].Respond(agent.Decision{Allow: true})
	waitFor(t, "turn", func() bool { return r.c.turnDones("claude") == 1 })
}

// waitIdle wakes on the event that ends the turn, and gives up after its
// timeout while the member stays busy.
func TestWaitIdleWakesWhenTheTurnEnds(t *testing.T) {
	a := &agentState{base: StatusWorking}
	if a.waitIdle(10 * time.Millisecond) {
		t.Fatal("a working member counted as idle")
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		a.apply(agent.Event{Kind: agent.KindTurnDone})
	}()
	start := time.Now()
	if !a.waitIdle(5 * time.Second) {
		t.Fatal("waitIdle missed the end of the turn")
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("waitIdle took %v to see the end of the turn", d)
	}
}
