package fake

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

const wait5 = 5 * time.Second

func nextEvent(t *testing.T, r agent.Run) agent.Event {
	t.Helper()
	select {
	case ev, ok := <-r.Events():
		if !ok {
			t.Fatal("events channel closed unexpectedly")
		}
		return ev
	case <-time.After(wait5):
		t.Fatal("timed out waiting for an event")
	}
	return agent.Event{}
}

func nextApproval(t *testing.T, r agent.Run) agent.ApprovalRequest {
	t.Helper()
	select {
	case req := <-r.Approvals():
		return req
	case <-time.After(wait5):
		t.Fatal("timed out waiting for an approval")
	}
	return agent.ApprovalRequest{}
}

// untilTurnDone reads events until turn_done, returning everything seen.
func untilTurnDone(t *testing.T, r agent.Run) []agent.Event {
	t.Helper()
	var got []agent.Event
	for {
		ev := nextEvent(t, r)
		got = append(got, ev)
		if ev.Kind == agent.KindTurnDone {
			return got
		}
	}
}

func kinds(evs []agent.Event) []agent.Kind {
	out := make([]agent.Kind, len(evs))
	for i, ev := range evs {
		out[i] = ev.Kind
	}
	return out
}

func startRun(t *testing.T, a agent.Adapter, id string) agent.Run {
	t.Helper()
	r, err := a.Start(context.Background(), agent.Spec{ID: id, Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func TestName(t *testing.T) {
	if got := New(nil, 0).Name(); got != "fake" {
		t.Fatalf("Name() = %q, want fake", got)
	}
}

func TestPlaybackOrderAndStamping(t *testing.T) {
	script := Script{
		{Event: agent.Event{Kind: agent.KindSessionStarted, SessionID: "sess-1"}},
		{Event: agent.Event{Kind: agent.KindAssistantText, Text: "a", Delta: true}},
		{Event: agent.Event{Kind: agent.KindAssistantText, Text: "b", Delta: true}},
		{Event: agent.Event{Kind: agent.KindAssistantText, Text: "ab"}},
		{Event: agent.Event{Kind: agent.KindTurnDone}},
	}
	r := startRun(t, New(script, 0), "claude2")

	got := untilTurnDone(t, r)
	want := []agent.Kind{
		agent.KindSessionStarted, agent.KindAssistantText, agent.KindAssistantText,
		agent.KindAssistantText, agent.KindTurnDone,
	}
	if k := kinds(got); !equalKinds(k, want) {
		t.Fatalf("kinds = %v, want %v", k, want)
	}
	for _, ev := range got {
		if ev.AgentID != "claude2" {
			t.Errorf("AgentID = %q, want claude2", ev.AgentID)
		}
		if ev.Time.IsZero() {
			t.Errorf("event %s has zero Time", ev.Kind)
		}
	}
	if got[3].Text != "ab" || got[3].Delta {
		t.Errorf("final text event = %+v", got[3])
	}
	if r.SessionID() != "sess-1" {
		t.Errorf("SessionID() = %q, want sess-1", r.SessionID())
	}
}

func TestSpeedScalesDelays(t *testing.T) {
	script := Script{
		{Delay: 10 * time.Second, Event: agent.Event{Kind: agent.KindAssistantText, Text: "late"}},
		{Event: agent.Event{Kind: agent.KindTurnDone}},
	}
	start := time.Now()
	r := startRun(t, New(script, 0), "x")
	untilTurnDone(t, r)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("speed 0 took %v, want instant", elapsed)
	}

	slow := Script{
		{Delay: 4 * time.Second, Event: agent.Event{Kind: agent.KindTurnDone}},
	}
	// speed 4 turns 4s into ~1s. Bounds are wide so a loaded CI machine can't
	// flake it: sleeps never end early, and 2.5s is still far below unscaled 4s.
	start = time.Now()
	r2 := startRun(t, New(slow, 4), "y")
	untilTurnDone(t, r2)
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond || elapsed > 2500*time.Millisecond {
		t.Fatalf("speed 4 elapsed %v, want about 1s", elapsed)
	}
}

func TestApprovalBlocksUntilRespond(t *testing.T) {
	script := Script{
		{
			Event: agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{ID: "t1", Name: "Edit", Status: agent.ToolRunning}},
			Approval: &agent.ApprovalRequest{
				Kind: agent.ApproveEdit, ToolName: "Edit", Summary: "f.go",
			},
		},
		{Event: agent.Event{Kind: agent.KindFileChange, Diff: &agent.FileDiff{Path: "f.go", Added: 1}}},
		{Event: agent.Event{Kind: agent.KindTurnDone}},
	}
	r := startRun(t, New(script, 0), "claude1")

	if ev := nextEvent(t, r); ev.Kind != agent.KindToolCall {
		t.Fatalf("first event = %s, want tool_call", ev.Kind)
	}
	req := nextApproval(t, r)
	if req.AgentID != "claude1" || req.Kind != agent.ApproveEdit || req.Summary != "f.go" {
		t.Fatalf("approval = %+v", req)
	}
	if req.ID == "" || req.Respond == nil {
		t.Fatalf("approval missing ID or Respond: %+v", req)
	}

	// While the approval is unanswered, playback must not advance.
	select {
	case ev := <-r.Events():
		t.Fatalf("event %s emitted while approval pending", ev.Kind)
	case <-time.After(100 * time.Millisecond):
	}

	req.Respond(agent.Decision{Allow: true})
	rest := untilTurnDone(t, r)
	if k := kinds(rest); !equalKinds(k, []agent.Kind{agent.KindFileChange, agent.KindTurnDone}) {
		t.Fatalf("after allow kinds = %v", k)
	}
}

func TestApprovalDenyEmitsErrorResult(t *testing.T) {
	script := Script{
		{
			Event: agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{ID: "t9", Name: "Bash", Status: agent.ToolRunning}},
			Approval: &agent.ApprovalRequest{
				Kind: agent.ApproveCommand, ToolName: "Bash", Summary: "rm -rf dist",
			},
		},
		{Event: agent.Event{Kind: agent.KindFileChange, Diff: &agent.FileDiff{Path: "never.go"}}},
		{Event: agent.Event{Kind: agent.KindTurnDone}},
	}
	r := startRun(t, New(script, 0), "codex1")

	nextEvent(t, r) // tool_call
	nextApproval(t, r).Respond(agent.Decision{Allow: false, Reason: "not now"})

	rest := untilTurnDone(t, r)
	if k := kinds(rest); !equalKinds(k, []agent.Kind{agent.KindToolResult, agent.KindTurnDone}) {
		t.Fatalf("after deny kinds = %v, want tool_result then turn_done", k)
	}
	res := rest[0]
	if res.Tool == nil || res.Tool.Status != agent.ToolError || res.Tool.Output != "denied" {
		t.Fatalf("denied result = %+v (tool %+v)", res, res.Tool)
	}
	if res.Tool.ID != "t9" {
		t.Errorf("denied tool ID = %q, want t9 (matching the tool_call)", res.Tool.ID)
	}
}

func TestRespondIsIdempotent(t *testing.T) {
	script := Script{
		{Approval: &agent.ApprovalRequest{Kind: agent.ApproveOther, ToolName: "X"}},
		{Event: agent.Event{Kind: agent.KindTurnDone}},
	}
	r := startRun(t, New(script, 0), "x")
	req := nextApproval(t, r)
	req.Respond(agent.Decision{Allow: true})
	req.Respond(agent.Decision{Allow: false}) // must not block or flip the outcome
	if k := kinds(untilTurnDone(t, r)); !equalKinds(k, []agent.Kind{agent.KindTurnDone}) {
		t.Fatalf("kinds = %v", k)
	}
}

func TestInterruptStopsPlayback(t *testing.T) {
	script := Script{
		{Event: agent.Event{Kind: agent.KindAssistantText, Text: "start"}},
		{Delay: 10 * time.Second, Event: agent.Event{Kind: agent.KindAssistantText, Text: "never"}},
		{Event: agent.Event{Kind: agent.KindTurnDone}},
	}
	r := startRun(t, New(script, 1), "x")

	if ev := nextEvent(t, r); ev.Text != "start" {
		t.Fatalf("first event text = %q", ev.Text)
	}
	begin := time.Now()
	if err := r.Interrupt(); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	if d := time.Since(begin); d > time.Second {
		t.Fatalf("Interrupt took %v", d)
	}
	ev := nextEvent(t, r)
	if ev.Kind != agent.KindTurnDone {
		t.Fatalf("after interrupt got %s (%q), want turn_done", ev.Kind, ev.Text)
	}
}

func TestInterruptDuringApproval(t *testing.T) {
	script := Script{
		{Approval: &agent.ApprovalRequest{Kind: agent.ApproveCommand, ToolName: "Bash", Summary: "ls"}},
		{Event: agent.Event{Kind: agent.KindTurnDone}},
	}
	r := startRun(t, New(script, 0), "x")
	nextApproval(t, r)
	if err := r.Interrupt(); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	if ev := nextEvent(t, r); ev.Kind != agent.KindTurnDone {
		t.Fatalf("got %s, want turn_done", ev.Kind)
	}
}

func TestInterruptWhenIdleIsNoop(t *testing.T) {
	script := Script{{Event: agent.Event{Kind: agent.KindTurnDone}}}
	r := startRun(t, New(script, 0), "x")
	untilTurnDone(t, r)
	if err := r.Interrupt(); err != nil {
		t.Fatalf("Interrupt on idle run: %v", err)
	}
}

func TestSendReplaysReplyWithoutSessionStart(t *testing.T) {
	script := Script{
		{Event: agent.Event{Kind: agent.KindSessionStarted, SessionID: "s"}},
		{Event: agent.Event{Kind: agent.KindAssistantText, Text: "hi there"}},
		{Event: agent.Event{Kind: agent.KindTurnDone}},
	}
	r := startRun(t, New(script, 0), "x")
	untilTurnDone(t, r)

	if err := r.Send("hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := untilTurnDone(t, r)
	want := []agent.Kind{agent.KindUserText, agent.KindAssistantText, agent.KindTurnDone}
	if k := kinds(got); !equalKinds(k, want) {
		t.Fatalf("reply kinds = %v, want %v", k, want)
	}
	if got[0].Text != "hello" {
		t.Errorf("user text = %q, want hello", got[0].Text)
	}
}

func TestSendWhileBusyFails(t *testing.T) {
	script := Script{
		{Delay: 10 * time.Second, Event: agent.Event{Kind: agent.KindAssistantText, Text: "slow"}},
		{Event: agent.Event{Kind: agent.KindTurnDone}},
	}
	r := startRun(t, New(script, 1), "x")
	if err := r.Send("again"); err != ErrBusy {
		t.Fatalf("Send while busy = %v, want ErrBusy", err)
	}
	if err := r.Interrupt(); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	untilTurnDone(t, r)
}

func TestCloseEmitsExitedAndClosesChannels(t *testing.T) {
	script := Script{
		{Delay: 10 * time.Second, Event: agent.Event{Kind: agent.KindAssistantText, Text: "slow"}},
	}
	r, err := New(script, 1).Start(context.Background(), agent.Spec{ID: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	var sawExited bool
	for ev := range r.Events() {
		if ev.Kind == agent.KindExited {
			sawExited = true
		}
	}
	if !sawExited {
		t.Fatal("no exited event before the events channel closed")
	}
	if _, ok := <-r.Approvals(); ok {
		t.Fatal("approvals channel still open after Close")
	}
	if err := r.Send("late"); err != ErrClosed {
		t.Fatalf("Send after Close = %v, want ErrClosed", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestContextCancelClosesRun(t *testing.T) {
	script := Script{{Delay: 10 * time.Second, Event: agent.Event{Kind: agent.KindAssistantText}}}
	ctx, cancel := context.WithCancel(context.Background())
	r, err := New(script, 1).Start(ctx, agent.Spec{ID: "x"})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	deadline := time.After(wait5)
	for {
		select {
		case _, ok := <-r.Events():
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("run not closed after context cancel")
		}
	}
}

func TestClaudeDemoStructure(t *testing.T) {
	s := DemoScript("claude")
	if len(s) == 0 {
		t.Fatal("empty demo script")
	}
	if s[0].Event.Kind != agent.KindSessionStarted {
		t.Errorf("first step = %s, want session_started", s[0].Event.Kind)
	}
	if last := s[len(s)-1].Event.Kind; last != agent.KindTurnDone {
		t.Errorf("last step = %s, want turn_done", last)
	}

	var deltas int
	var sawRead, sawBash, sawUsage bool
	var approval *agent.ApprovalRequest
	var fileChange *agent.FileDiff
	for _, st := range s {
		ev := st.Event
		if ev.Kind == agent.KindAssistantText && ev.Delta {
			deltas++
		}
		if ev.Tool != nil && ev.Tool.Name == "Read" {
			sawRead = true
		}
		if ev.Tool != nil && ev.Tool.Name == "Bash" && ev.Tool.Title == "go test ./..." {
			sawBash = true
		}
		if ev.Kind == agent.KindUsage && ev.Usage != nil && ev.Usage.CostUSD == 0.12 {
			sawUsage = true
		}
		if ev.Kind == agent.KindFileChange {
			fileChange = ev.Diff
		}
		if st.Approval != nil {
			approval = st.Approval
		}
	}
	if deltas < 5 || deltas > 7 {
		t.Errorf("delta count = %d, want about 6", deltas)
	}
	if !sawRead || !sawBash || !sawUsage {
		t.Errorf("read=%v bash=%v usage=%v", sawRead, sawBash, sawUsage)
	}
	if approval == nil || approval.Kind != agent.ApproveEdit || approval.Diff == nil {
		t.Fatalf("edit approval missing: %+v", approval)
	}
	if fileChange == nil {
		t.Fatal("no file_change event")
	}
	d := approval.Diff
	if d.Path != "internal/store/store.go" || len(d.Hunks) != 2 {
		t.Fatalf("diff path/hunks = %q / %d", d.Path, len(d.Hunks))
	}
	if d.Added != 24 || d.Removed != 9 {
		t.Errorf("diff counts +%d -%d, want +24 -9", d.Added, d.Removed)
	}
	checkDiffCounts(t, d)
	if fileChange.Added != 24 || fileChange.Removed != 9 {
		t.Errorf("file_change diff +%d -%d, want +24 -9", fileChange.Added, fileChange.Removed)
	}
}

func TestCodexDemoStructure(t *testing.T) {
	s := DemoCodexScript()
	var approval *agent.ApprovalRequest
	for _, st := range s {
		if st.Approval != nil {
			approval = st.Approval
		}
	}
	if approval == nil || approval.Kind != agent.ApproveCommand {
		t.Fatalf("command approval missing: %+v", approval)
	}
	if approval.Summary != "rm -rf dist && npm run build" {
		t.Errorf("summary = %q", approval.Summary)
	}
	if s[len(s)-1].Event.Kind != agent.KindTurnDone {
		t.Errorf("last step = %s", s[len(s)-1].Event.Kind)
	}
	if DemoScript("codex")[0].Event.SessionID != "fake-demo-codex" {
		t.Error("DemoScript(codex) did not return the codex demo")
	}
}

// TestDemoPlaysThroughWithApprovals runs both demos at full speed, answering
// every approval with allow, and checks that each ends with turn_done.
func TestDemoPlaysThroughWithApprovals(t *testing.T) {
	for _, name := range []string{"claude", "codex"} {
		t.Run(name, func(t *testing.T) {
			r := startRun(t, New(DemoScript(name), 0), name)
			done := make(chan struct{})
			go func() {
				for req := range r.Approvals() {
					req.Respond(agent.Decision{Allow: true})
				}
				close(done)
			}()
			got := untilTurnDone(t, r)
			if got[0].Kind != agent.KindSessionStarted {
				t.Errorf("first event = %s", got[0].Kind)
			}
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
			<-done
		})
	}
}

// checkDiffCounts verifies each hunk's header line counts and the +/- totals
// match the hunk body.
func checkDiffCounts(t *testing.T, d *agent.FileDiff) {
	t.Helper()
	var added, removed int
	for i, h := range d.Hunks {
		var oldN, newN int
		for _, l := range h.Lines {
			switch l.Op {
			case agent.OpAdd:
				added++
				newN++
			case agent.OpDel:
				removed++
				oldN++
			default:
				oldN++
				newN++
			}
		}
		if oldN != h.OldLines || newN != h.NewLines {
			t.Errorf("hunk %d counts old %d/%d new %d/%d", i, oldN, h.OldLines, newN, h.NewLines)
		}
	}
	if added != d.Added || removed != d.Removed {
		t.Errorf("diff totals +%d -%d, hunks give +%d -%d", d.Added, d.Removed, added, removed)
	}
}

func equalKinds(a, b []agent.Kind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestNewTurnsPlaysOneScriptPerTurnAndStaysIdleWithoutPrompt(t *testing.T) {
	t1 := Script{{Event: agent.Event{Kind: agent.KindAssistantText, Text: "one"}}, {Event: agent.Event{Kind: agent.KindTurnDone}}}
	t2 := Script{{Event: agent.Event{Kind: agent.KindAssistantText, Text: "two"}}, {Event: agent.Event{Kind: agent.KindTurnDone}}}
	r := startRun(t, NewTurns(0, nil, t1, t2), "a")
	select {
	case ev := <-r.Events():
		t.Fatalf("a run started without a prompt must stay idle, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
	for _, want := range []string{"one", "two", "Noted."} {
		if err := r.Send("go"); err != nil {
			t.Fatal(err)
		}
		var text string
		for _, ev := range untilTurnDone(t, r) {
			if ev.Kind == agent.KindAssistantText {
				text = ev.Text
			}
		}
		if text != want {
			t.Fatalf("turn text = %q, want %q", text, want)
		}
	}
}

func TestNewTurnsWithFirstPromptPlaysFirstTurn(t *testing.T) {
	t1 := Script{{Event: agent.Event{Kind: agent.KindAssistantText, Text: "one"}}, {Event: agent.Event{Kind: agent.KindTurnDone}}}
	r, err := NewTurns(0, nil, t1).Start(context.Background(), agent.Spec{ID: "a", FirstPrompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	got := untilTurnDone(t, r)
	if k := kinds(got); !equalKinds(k, []agent.Kind{agent.KindUserText, agent.KindAssistantText, agent.KindTurnDone}) {
		t.Fatalf("kinds = %v", k)
	}
}

func TestMessageStepGoesThroughTheHub(t *testing.T) {
	hub := NewHub()
	hub.SetThreadReader(func(id string) string { return "thread for " + id })
	script := Script{
		{Message: &agent.Message{To: "codex", Text: "review this"}},
		{ReadThread: true},
		{Event: agent.Event{Kind: agent.KindTurnDone}},
	}
	r, err := NewTurns(0, hub, script).Start(context.Background(), agent.Spec{ID: "claude", FirstPrompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })

	select {
	case msg := <-hub.Messages():
		if msg.From != "claude" || msg.To != "codex" || msg.Text != "review this" {
			t.Fatalf("message = %+v", msg)
		}
		msg.Respond("delivered to codex", nil)
	case <-time.After(wait5):
		t.Fatal("no message reached the hub")
	}
	got := untilTurnDone(t, r)
	var results []string
	for _, ev := range got {
		if ev.Kind == agent.KindToolResult {
			results = append(results, ev.Tool.Name+"="+ev.Tool.Output+"/"+string(ev.Tool.Status))
		}
	}
	want := []string{"send_message=delivered to codex/ok", "read_thread=thread for claude/ok"}
	if len(results) != 2 || results[0] != want[0] || results[1] != want[1] {
		t.Fatalf("tool results = %q, want %q", results, want)
	}
}

func TestMessageStepErrorIsAToolError(t *testing.T) {
	hub := NewHub()
	script := Script{{Message: &agent.Message{To: "nobody", Text: "x"}}, {Event: agent.Event{Kind: agent.KindTurnDone}}}
	r, err := NewTurns(0, hub, script).Start(context.Background(), agent.Spec{ID: "claude", FirstPrompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	msg := <-hub.Messages()
	msg.Respond("", errors.New("unknown recipient"))
	for _, ev := range untilTurnDone(t, r) {
		if ev.Kind == agent.KindToolResult && (ev.Tool.Status != agent.ToolError || ev.Tool.Output != "unknown recipient") {
			t.Fatalf("tool result = %+v", ev.Tool)
		}
	}
}

func TestMessageStepWithoutHubErrors(t *testing.T) {
	script := Script{{Message: &agent.Message{To: "codex", Text: "x"}}, {Event: agent.Event{Kind: agent.KindTurnDone}}}
	r, err := NewTurns(0, nil, script).Start(context.Background(), agent.Spec{ID: "claude", FirstPrompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	var sawError bool
	for _, ev := range untilTurnDone(t, r) {
		if ev.Kind == agent.KindToolResult && ev.Tool.Status == agent.ToolError {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("expected an error tool_result")
	}
}

func TestInterruptCancelsAPendingHubCall(t *testing.T) {
	hub := NewHub()
	script := Script{{Message: &agent.Message{To: "codex", Text: "x"}}, {Event: agent.Event{Kind: agent.KindTurnDone}}}
	r, err := NewTurns(0, hub, script).Start(context.Background(), agent.Spec{ID: "claude", FirstPrompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	<-hub.Messages() // never answered
	done := make(chan struct{})
	go func() { _ = r.Interrupt(); close(done) }()
	select {
	case <-done:
	case <-time.After(wait5):
		t.Fatal("Interrupt hung on an unanswered hub call")
	}
}

func TestDemoDuoScriptsShape(t *testing.T) {
	c, x := DemoDuoScripts()
	if len(c) != 2 || len(x) != 1 {
		t.Fatalf("turns: claude %d codex %d", len(c), len(x))
	}
	var sends, mentions int
	for _, st := range c[0] {
		if st.Message != nil && st.Message.To == "codex" {
			sends++
		}
	}
	for _, st := range x[0] {
		if st.Event.Kind == agent.KindAssistantText && strings.Contains(st.Event.Text, "\n$claude ") {
			mentions++
		}
	}
	if sends != 1 || mentions != 1 {
		t.Fatalf("sends=%d mentions=%d", sends, mentions)
	}
	adapters, hub := DemoDuo(0)
	if adapters["claude"] == nil || adapters["codex"] == nil || hub == nil {
		t.Fatal("DemoDuo incomplete")
	}
}

func TestRunRecordsModeAndModel(t *testing.T) {
	a := NewTurns(0, nil)
	r, err := a.Start(context.Background(), agent.Spec{ID: "c", Mode: agent.ModePlan, Model: "opus"})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	ms, ok := r.(agent.ModeSetter)
	if !ok {
		t.Fatal("fake run is not an agent.ModeSetter")
	}
	mo, ok := r.(agent.ModelSetter)
	if !ok {
		t.Fatal("fake run is not an agent.ModelSetter")
	}
	in := r.(Inspector)
	if in.Mode() != agent.ModePlan || in.Model() != "opus" {
		t.Fatalf("initial mode/model = %q/%q, want spec values", in.Mode(), in.Model())
	}
	if err := ms.SetMode(agent.ModeYolo); err != nil {
		t.Fatal(err)
	}
	if err := mo.SetModel("sonnet"); err != nil {
		t.Fatal(err)
	}
	if in.Mode() != agent.ModeYolo || in.Model() != "sonnet" {
		t.Errorf("after set mode/model = %q/%q", in.Mode(), in.Model())
	}
	if got := in.ModeCalls(); len(got) != 1 || got[0] != agent.ModeYolo {
		t.Errorf("ModeCalls = %v", got)
	}
	if got := in.ModelCalls(); len(got) != 1 || got[0] != "sonnet" {
		t.Errorf("ModelCalls = %v", got)
	}

	_ = r.Close()
	if err := ms.SetMode(agent.ModeAsk); !errors.Is(err, ErrClosed) {
		t.Errorf("SetMode after Close = %v, want ErrClosed", err)
	}
}

func TestAdaptersListModels(t *testing.T) {
	adapters, _ := DemoDuo(0)
	for kind, wantFirst := range map[string]string{"claude": "default", "codex": "gpt-5-codex"} {
		ml, ok := adapters[kind].(agent.ModelLister)
		if !ok {
			t.Fatalf("%s adapter is not an agent.ModelLister", kind)
		}
		ms, err := ml.Models(context.Background())
		if err != nil || len(ms) < 3 || ms[0].ID != wantFirst || !ms[0].Default {
			t.Errorf("%s models = %+v, %v", kind, ms, err)
		}
	}
	ml := New(nil, 0).(agent.ModelLister)
	if ms, _ := ml.Models(context.Background()); len(ms) != 4 {
		t.Errorf("plain adapter models = %+v, want the claude list", ms)
	}
	custom := WithModels(New(nil, 0), CodexModels()).(agent.ModelLister)
	if ms, _ := custom.Models(context.Background()); ms[0].ID != "gpt-5-codex" {
		t.Errorf("WithModels not applied: %+v", ms)
	}
}
