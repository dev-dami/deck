package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/store"
)

// ---- stub adapter: a run the test drives by hand ----

type stubAdapter struct {
	mu   sync.Mutex
	runs map[string]*stubRun
}

type stubRun struct {
	ev   chan agent.Event
	ap   chan agent.ApprovalRequest
	once sync.Once
}

func (s *stubAdapter) Name() string { return "stub" }

func (s *stubAdapter) Start(ctx context.Context, sp agent.Spec) (agent.Run, error) {
	r := &stubRun{ev: make(chan agent.Event, 16), ap: make(chan agent.ApprovalRequest, 16)}
	s.mu.Lock()
	if s.runs == nil {
		s.runs = map[string]*stubRun{}
	}
	s.runs[sp.ID] = r
	s.mu.Unlock()
	return r, nil
}

func (s *stubAdapter) run(id string) *stubRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs[id]
}

func (r *stubRun) Events() <-chan agent.Event              { return r.ev }
func (r *stubRun) Approvals() <-chan agent.ApprovalRequest { return r.ap }
func (r *stubRun) Send(string) error                       { return nil }
func (r *stubRun) Interrupt() error                        { return nil }
func (r *stubRun) SessionID() string                       { return "" }
func (r *stubRun) Close() error {
	r.once.Do(func() { close(r.ev); close(r.ap) })
	return nil
}

// ask raises an approval request and returns where the answer will arrive.
func (r *stubRun) ask(id string, kind agent.ApprovalKind, tool, summary string) <-chan agent.Decision {
	got := make(chan agent.Decision, 1)
	r.ap <- agent.ApprovalRequest{ID: id, Kind: kind, ToolName: tool, Summary: summary,
		Respond: func(d agent.Decision) { got <- d }}
	return got
}

func decision(t *testing.T, ch <-chan agent.Decision) agent.Decision {
	t.Helper()
	select {
	case d := <-ch:
		return d
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the agent to be answered")
	}
	return agent.Decision{}
}

func stubManager(t *testing.T, cfg config.Config) (*Manager, *stubAdapter, *collector) {
	t.Helper()
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ad := &stubAdapter{}
	m := NewManager(map[string]agent.Adapter{"claude": ad}, st, cfg)
	t.Cleanup(m.Close)
	return m, ad, collect(t, m)
}

func (c *collector) autoApproved() []AutoApprovedMsg {
	var out []AutoApprovedMsg
	for _, m := range c.all() {
		if a, ok := m.(AutoApprovedMsg); ok {
			out = append(out, a)
		}
	}
	return out
}

func (c *collector) autoDenied() []AutoDeniedMsg {
	var out []AutoDeniedMsg
	for _, m := range c.all() {
		if a, ok := m.(AutoDeniedMsg); ok {
			out = append(out, a)
		}
	}
	return out
}

func (c *collector) modeChanges() []ModeChangedMsg {
	var out []ModeChangedMsg
	for _, m := range c.all() {
		if a, ok := m.(ModeChangedMsg); ok {
			out = append(out, a)
		}
	}
	return out
}

// inspect reaches the fake run under a spy.
func inspect(t *testing.T, s *spyAdapter, id string) fake.Inspector {
	t.Helper()
	sr, ok := s.run(id).(*spyRun)
	if !ok {
		t.Fatalf("no spy run for %s", id)
	}
	in, ok := sr.Run.(fake.Inspector)
	if !ok {
		t.Fatalf("%s is not a fake run", id)
	}
	return in
}

func setterRig(t *testing.T, claude, codex []fake.Script) *rig {
	t.Helper()
	return newRigWith(t, t.TempDir(), true, testCfg(), claude, codex)
}

// ---- NextMode ----

func TestNextModeCycle(t *testing.T) {
	want := map[agent.Mode]agent.Mode{
		agent.ModePlan: agent.ModeAsk,
		agent.ModeAsk:  agent.ModeYolo,
		agent.ModeYolo: agent.ModePlan,
		"":             agent.ModeAsk,
		"nonsense":     agent.ModeAsk,
	}
	for in, out := range want {
		if got := NextMode(in); got != out {
			t.Errorf("NextMode(%q) = %q, want %q", in, got, out)
		}
	}
}

// ---- SetMode ----

func TestSetModePropagatesPersistsAndEmits(t *testing.T) {
	r := setterRig(t, nil, nil)
	tid := r.thread(t)
	if got := r.m.Mode(tid); got != agent.ModeAsk {
		t.Fatalf("default mode = %q, want ask", got)
	}
	claude := r.add(t, tid, "claude")
	codex := r.add(t, tid, "codex")
	if sp, _ := r.claude.spec(claude); sp.Mode != agent.ModeAsk {
		t.Errorf("first spec mode = %q, want ask", sp.Mode)
	}

	if err := r.m.SetMode(tid, agent.ModePlan); err != nil {
		t.Fatal(err)
	}
	if got := r.m.Mode(tid); got != agent.ModePlan {
		t.Errorf("Mode = %q", got)
	}
	if ti, _ := r.m.Thread(tid); ti.Mode != agent.ModePlan {
		t.Errorf("ThreadInfo.Mode = %q", ti.Mode)
	}
	for _, p := range []struct {
		s  *spyAdapter
		id string
	}{{r.claude, claude}, {r.codex, codex}} {
		in := inspect(t, p.s, p.id)
		if in.Mode() != agent.ModePlan || len(in.ModeCalls()) != 1 {
			t.Errorf("%s: mode %q calls %v, want one call with plan", p.id, in.Mode(), in.ModeCalls())
		}
	}
	waitFor(t, "ModeChangedMsg", func() bool { return len(r.c.modeChanges()) == 1 })
	if mc := r.c.modeChanges()[0]; mc.ThreadID != tid || mc.Mode != agent.ModePlan || mc.Prev != agent.ModeAsk {
		t.Errorf("ModeChangedMsg = %+v", mc)
	}

	// Setting the same mode again changes and emits nothing.
	if err := r.m.SetMode(tid, agent.ModePlan); err != nil {
		t.Fatal(err)
	}
	if len(inspect(t, r.claude, claude).ModeCalls()) != 1 {
		t.Error("same-mode SetMode reached the run")
	}

	// A member started later gets the thread's mode in its Spec.
	late := r.add(t, tid, "claude")
	if sp, _ := r.claude.spec(late); sp.Mode != agent.ModePlan {
		t.Errorf("late member spec mode = %q, want plan", sp.Mode)
	}

	// Persisted with the thread.
	r.m.Flush()
	_, th, err := r.st.LoadEntries(tid)
	if err != nil || th.Mode != "plan" {
		t.Fatalf("stored mode = %q, %v", th.Mode, err)
	}

	if err := r.m.SetMode(tid, "bogus"); err == nil {
		t.Error("unknown mode accepted")
	}
	if err := r.m.SetMode("nope", agent.ModeAsk); err == nil {
		t.Error("unknown thread accepted")
	}
	if len(r.c.modeChanges()) != 1 {
		t.Errorf("failed calls emitted: %+v", r.c.modeChanges())
	}
}

func TestSetModeWithoutModeSetterStillEnforcesAndAppliesToNewMembers(t *testing.T) {
	r := newRig(t, nil, nil) // runs do not implement agent.ModeSetter
	tid := r.thread(t)
	r.add(t, tid, "claude")
	if err := r.m.SetMode(tid, agent.ModeYolo); err != nil {
		t.Fatalf("SetMode on a run without ModeSetter: %v", err)
	}
	if got := r.m.Mode(tid); got != agent.ModeYolo {
		t.Errorf("Mode = %q", got)
	}
	id := r.add(t, tid, "codex")
	if sp, _ := r.codex.spec(id); sp.Mode != agent.ModeYolo {
		t.Errorf("new member spec mode = %q, want yolo", sp.Mode)
	}
	if len(r.c.autoApproved()) != 0 {
		t.Error("nothing was asked, nothing should be auto-approved")
	}
}

// ---- Deck-side enforcement ----

func TestYoloAutoAllowsNewApprovals(t *testing.T) {
	m, ad, c := stubManager(t, testCfg())
	tid, _ := m.NewThread(t.TempDir())
	if err := m.SetMode(tid, agent.ModeYolo); err != nil {
		t.Fatal(err)
	}
	id, err := m.AddMember(tid, "claude", "")
	if err != nil {
		t.Fatal(err)
	}
	run := ad.run(id)
	answers := []<-chan agent.Decision{
		run.ask("a1", agent.ApproveCommand, "Bash", "rm -rf dist"),
		run.ask("a2", agent.ApproveEdit, "Edit", "internal/x.go"),
		run.ask("a3", agent.ApproveOther, "WebFetch", ""),
	}
	for i, ch := range answers {
		if d := decision(t, ch); !d.Allow {
			t.Errorf("request %d denied in yolo: %+v", i+1, d)
		}
	}
	waitFor(t, "three AutoApprovedMsg", func() bool { return len(c.autoApproved()) == 3 })
	got := c.autoApproved()
	if got[0] != (AutoApprovedMsg{ThreadID: tid, AgentID: id, Summary: "rm -rf dist"}) {
		t.Errorf("first AutoApprovedMsg = %+v", got[0])
	}
	if got[2].Summary != "WebFetch" {
		t.Errorf("empty summary should fall back to the tool name, got %q", got[2].Summary)
	}
	if n := len(c.approvals()); n != 0 {
		t.Errorf("%d ApprovalMsg surfaced in yolo", n)
	}
	if ai, _ := m.Agent(id); ai.Pending != 0 || ai.Status == StatusApproval {
		t.Errorf("agent shows pending approvals: %+v", ai)
	}
}

func TestSwitchingToYoloAnswersPendingApprovals(t *testing.T) {
	m, ad, c := stubManager(t, testCfg())
	tid, _ := m.NewThread(t.TempDir())
	id, _ := m.AddMember(tid, "claude", "")
	run := ad.run(id)
	first := run.ask("p1", agent.ApproveCommand, "Bash", "go test ./...")
	second := run.ask("p2", agent.ApproveEdit, "Edit", "a.go")
	waitFor(t, "two pending approvals", func() bool { return len(c.approvals()) == 2 })
	if ai, _ := m.Agent(id); ai.Pending != 2 || ai.Status != StatusApproval {
		t.Fatalf("before: %+v", ai)
	}

	if err := m.SetMode(tid, agent.ModeYolo); err != nil {
		t.Fatal(err)
	}
	for _, ch := range []<-chan agent.Decision{first, second} {
		if d := decision(t, ch); !d.Allow {
			t.Errorf("pending approval denied on switch to yolo: %+v", d)
		}
	}
	waitFor(t, "gone and auto-approved for both", func() bool {
		return len(c.autoApproved()) == 2 && c.count(func(m tea.Msg) bool { _, ok := m.(ApprovalGoneMsg); return ok }) == 2
	})
	// Each ApprovalGoneMsg is followed by its AutoApprovedMsg, after the mode change.
	var order []string
	for _, msg := range c.all() {
		switch v := msg.(type) {
		case ModeChangedMsg:
			order = append(order, "mode")
		case ApprovalGoneMsg:
			order = append(order, "gone:"+v.ID)
		case AutoApprovedMsg:
			order = append(order, "auto:"+v.Summary)
		}
	}
	if got := strings.Join(order, ","); got != "mode,gone:p1,auto:go test ./...,gone:p2,auto:a.go" {
		t.Errorf("message order = %s", got)
	}
	if ai, _ := m.Agent(id); ai.Pending != 0 {
		t.Errorf("still pending: %+v", ai)
	}
}

func TestPlanAutoDeniesEditsAndCommandsButNotOther(t *testing.T) {
	m, ad, c := stubManager(t, testCfg())
	tid, _ := m.NewThread(t.TempDir())
	if err := m.SetMode(tid, agent.ModePlan); err != nil {
		t.Fatal(err)
	}
	id, _ := m.AddMember(tid, "claude", "")
	run := ad.run(id)

	for _, k := range []struct {
		kind    agent.ApprovalKind
		tool    string
		summary string
	}{{agent.ApproveEdit, "Edit", "internal/x.go"}, {agent.ApproveCommand, "Bash", "rm -rf dist"}} {
		d := decision(t, run.ask("d-"+k.tool, k.kind, k.tool, k.summary))
		if d.Allow || d.Reason != "Deck is in plan mode: propose the change instead of making it." {
			t.Errorf("%s: decision %+v", k.tool, d)
		}
	}
	waitFor(t, "two AutoDeniedMsg", func() bool { return len(c.autoDenied()) == 2 })
	if got := c.autoDenied()[1]; got != (AutoDeniedMsg{ThreadID: tid, AgentID: id, Summary: "rm -rf dist"}) {
		t.Errorf("AutoDeniedMsg = %+v", got)
	}
	if len(c.approvals()) != 0 || len(c.autoApproved()) != 0 {
		t.Error("denied requests must not surface as approvals or auto-approvals")
	}

	// Kind other still asks the user, and the user's answer goes through.
	other := run.ask("o1", agent.ApproveOther, "WebFetch", "https://example.com")
	waitFor(t, "a pending approval", func() bool { return len(c.approvals()) == 1 })
	if ai, _ := m.Agent(id); ai.Pending != 1 {
		t.Errorf("pending = %d, want 1", ai.Pending)
	}
	c.approvals()[0].Respond(agent.Decision{Allow: true})
	if d := decision(t, other); !d.Allow {
		t.Errorf("user's allow lost: %+v", d)
	}
}

func TestAskModeLeavesApprovalsToTheUser(t *testing.T) {
	m, ad, c := stubManager(t, testCfg())
	tid, _ := m.NewThread(t.TempDir())
	id, _ := m.AddMember(tid, "claude", "")
	ch := ad.run(id).ask("k1", agent.ApproveCommand, "Bash", "ls")
	waitFor(t, "approval", func() bool { return len(c.approvals()) == 1 })
	select {
	case d := <-ch:
		t.Fatalf("answered without the user: %+v", d)
	case <-time.After(50 * time.Millisecond):
	}
	if len(c.autoApproved())+len(c.autoDenied()) != 0 {
		t.Error("ask mode auto-answered")
	}
	c.approvals()[0].Respond(agent.Decision{Allow: false, Reason: "no"})
	if d := decision(t, ch); d.Allow || d.Reason != "no" {
		t.Errorf("decision = %+v", d)
	}
}

// ---- the mode line in the next prompt ----

func TestModeLineOpensTheNextPromptOnce(t *testing.T) {
	r := setterRig(t, []fake.Script{turn(say("one"), finish()), turn(say("two"), finish()), turn(say("three"), finish()), turn(say("four"), finish())}, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")

	if err := r.m.Post(tid, "first"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "turn 1", func() bool { return r.c.turnDones("claude") == 1 })
	if err := r.m.SetMode(tid, agent.ModePlan); err != nil {
		t.Fatal(err)
	}
	if err := r.m.Post(tid, "second"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "turn 2", func() bool { return r.c.turnDones("claude") == 2 })
	if err := r.m.Post(tid, "third"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "turn 3", func() bool { return r.c.turnDones("claude") == 3 })

	p := r.claude.prompts("claude")
	if len(p) != 3 {
		t.Fatalf("prompts = %q", p)
	}
	if p[0] != "first" {
		t.Errorf("first prompt = %q, want the user's words untouched", p[0])
	}
	wantLine := "[deck] Permission mode is now plan. Investigate and propose; don't change files."
	if !strings.HasPrefix(p[1], wantLine+"\n\n") || !strings.HasSuffix(p[1], "second") {
		t.Errorf("second prompt = %q", p[1])
	}
	if p[2] != "third" {
		t.Errorf("mode line repeated: %q", p[2])
	}

	// Switching away and back before the next prompt tells the agent nothing.
	_ = r.m.SetMode(tid, agent.ModeYolo)
	_ = r.m.SetMode(tid, agent.ModePlan)
	if err := r.m.Post(tid, "fourth"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "turn 4", func() bool { return r.c.turnDones("claude") == 4 })
	if p = r.claude.prompts("claude"); p[3] != "fourth" {
		t.Errorf("net-zero mode change announced: %q", p[3])
	}

	// A plain mode name for the others.
	_ = r.m.SetMode(tid, agent.ModeYolo)
	if got := modeLine(agent.ModeYolo); got != "[deck] Permission mode is now yolo." {
		t.Errorf("modeLine(yolo) = %q", got)
	}
}

func TestModeLineForMemberWhoseRunHasNotStarted(t *testing.T) {
	r := newRig(t, []fake.Script{turn(say("ok"), finish())}, nil)
	tid := r.thread(t)
	if err := r.m.SetMode(tid, agent.ModeYolo); err != nil {
		t.Fatal(err)
	}
	// The member is created in yolo, so it starts in yolo and needs no line.
	if err := r.m.Post(tid, "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "turn", func() bool { return r.c.turnDones("claude") == 1 })
	if p := r.claude.prompts("claude"); len(p) != 1 || p[0] != "hello" {
		t.Errorf("prompts = %q", p)
	}
	if sp, _ := r.claude.spec("claude"); sp.Mode != agent.ModeYolo {
		t.Errorf("spec mode = %q", sp.Mode)
	}
}

// ---- config: default_mode ----

func TestDefaultModeFromConfigAndYoloGuard(t *testing.T) {
	cases := []struct {
		cfg     string
		want    agent.Mode
		notice  bool
		comment string
	}{
		{"", agent.ModeAsk, false, "unset"},
		{"ask", agent.ModeAsk, false, "ask"},
		{"plan", agent.ModePlan, false, "plan"},
		{"yolo", agent.ModeAsk, true, "yolo is never a silent start"},
	}
	for _, c := range cases {
		t.Run(c.comment, func(t *testing.T) {
			cfg := testCfg()
			cfg.DefaultMode = c.cfg
			m, _, col := stubManager(t, cfg)
			tid, _ := m.NewThread(t.TempDir())
			if got := m.Mode(tid); got != c.want {
				t.Errorf("new thread mode = %q, want %q", got, c.want)
			}
			if got := m.Mode("no-such-thread"); got != c.want {
				t.Errorf("unknown thread mode = %q, want %q", got, c.want)
			}
			if ti, _ := m.Thread(tid); ti.Mode != c.want {
				t.Errorf("ThreadInfo.Mode = %q", ti.Mode)
			}
			yoloNotices := func() int {
				return col.count(func(msg tea.Msg) bool {
					n, ok := msg.(NoticeMsg)
					return ok && n.Text == "default_mode yolo ignored at startup; press shift+tab to switch"
				})
			}
			if c.notice {
				waitFor(t, "the yolo notice", func() bool { return yoloNotices() == 1 })
			} else {
				time.Sleep(30 * time.Millisecond)
				if n := yoloNotices(); n != 0 {
					t.Errorf("unexpected yolo notice (%d)", n)
				}
			}
		})
	}
}

func TestYoloNoticeIsDeliveredEvenWithoutAListener(t *testing.T) {
	cfg := testCfg()
	cfg.DefaultMode = "yolo"
	st, _ := store.New(t.TempDir())
	m := NewManager(map[string]agent.Adapter{"claude": &stubAdapter{}}, st, cfg)
	t.Cleanup(m.Close)
	select {
	case msg := <-m.Updates():
		n, ok := msg.(NoticeMsg)
		if !ok || !strings.Contains(n.Text, "default_mode yolo ignored at startup") {
			t.Errorf("first message = %#v", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("no notice")
	}
}

// ---- resume ----

func TestResumeRestoresModeButNeverYolo(t *testing.T) {
	for _, tc := range []struct {
		set        agent.Mode
		want       agent.Mode
		wantNotice bool
	}{{agent.ModePlan, agent.ModePlan, false}, {agent.ModeYolo, agent.ModeAsk, true}} {
		t.Run(string(tc.set), func(t *testing.T) {
			r := newRig(t, []fake.Script{turn(started("s1"), say("hi"), finish())}, nil)
			tid := r.thread(t)
			r.add(t, tid, "claude")
			if err := r.m.SetMode(tid, tc.set); err != nil {
				t.Fatal(err)
			}
			_ = r.m.Post(tid, "hello")
			waitFor(t, "turn", func() bool { return r.c.turnDones("claude") == 1 })
			r.m.Close()

			r2 := newRigIn(t, r.dir, nil, nil)
			ti, _, err := r2.m.Resume(tid)
			if err != nil {
				t.Fatal(err)
			}
			if ti.Mode != tc.want || r2.m.Mode(tid) != tc.want {
				t.Errorf("resumed mode = %q, want %q", ti.Mode, tc.want)
			}
			if sp, _ := r2.claude.spec("claude"); sp.Mode != tc.want {
				t.Errorf("resumed spec mode = %q", sp.Mode)
			}
			notices := func() int {
				return r2.c.count(func(msg tea.Msg) bool {
					n, ok := msg.(NoticeMsg)
					return ok && n.ThreadID == tid && strings.Contains(n.Text, "yolo")
				})
			}
			if tc.wantNotice {
				waitFor(t, "the yolo resume notice", func() bool { return notices() == 1 })
			} else if n := notices(); n != 0 {
				t.Errorf("unexpected yolo notice (%d)", n)
			}
		})
	}
}

func TestOldThreadWithoutModeResumesInAsk(t *testing.T) {
	dir := t.TempDir()
	meta := `{"id":"pre-modes","version":2,"title":"t","dir":"` + strings.ReplaceAll(t.TempDir(), `\`, `\\`) +
		`","members":[{"id":"claude","kind":"claude","name":"claude"}],"created":"2026-01-01T00:00:00Z","updated":"2026-01-01T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "pre-modes.meta.json"), []byte(meta), 0o600); err != nil {
		t.Fatal(err)
	}
	r := newRigIn(t, dir, nil, nil)
	ti, _, err := r.m.Resume("pre-modes")
	if err != nil {
		t.Fatal(err)
	}
	if ti.Mode != agent.ModeAsk {
		t.Errorf("mode = %q, want ask", ti.Mode)
	}
	if sp, _ := r.claude.spec("claude"); sp.Mode != agent.ModeAsk {
		t.Errorf("spec mode = %q", sp.Mode)
	}
}

// ---- models ----

func TestSetModelWithModelSetter(t *testing.T) {
	r := setterRig(t, nil, nil)
	tid := r.thread(t)
	id, err := r.m.AddMember(tid, "claude", "sonnet")
	if err != nil {
		t.Fatal(err)
	}
	if sp, _ := r.claude.spec(id); sp.Model != "sonnet" {
		t.Fatalf("spec model = %q", sp.Model)
	}
	if err := r.m.SetModel(id, "opus"); err != nil {
		t.Fatal(err)
	}
	if got := inspect(t, r.claude, id); got.Model() != "opus" || len(got.ModelCalls()) != 1 {
		t.Errorf("run model %q calls %v", got.Model(), got.ModelCalls())
	}
	if ai, _ := r.m.Agent(id); ai.Model != "opus" {
		t.Errorf("AgentInfo.Model = %q", ai.Model)
	}
	waitFor(t, "ModelChangedMsg", func() bool {
		return r.c.count(func(m tea.Msg) bool {
			mc, ok := m.(ModelChangedMsg)
			return ok && mc == ModelChangedMsg{ThreadID: tid, AgentID: id, Model: "opus"}
		}) == 1
	})
	r.m.Flush()
	_, th, _ := r.st.LoadEntries(tid)
	if len(th.Members) != 1 || th.Members[0].Model != "opus" {
		t.Errorf("stored members = %+v", th.Members)
	}
	if err := r.m.SetModel("ghost", "opus"); err == nil {
		t.Error("unknown agent accepted")
	}
}

func TestSetEffortIsKeptAndResumed(t *testing.T) {
	cfg := testCfg()
	cfg.Efforts.Claude = "low"
	r := newRigWith(t, t.TempDir(), true, cfg, nil, nil)
	tid := r.thread(t)
	id, err := r.m.AddMember(tid, "claude", "")
	if err != nil {
		t.Fatal(err)
	}
	if sp, _ := r.claude.spec(id); sp.Effort != "low" {
		t.Fatalf("a new member starts at the configured effort: %q", sp.Effort)
	}
	if err := r.m.SetEffort(id, "High"); err != nil {
		t.Fatal(err)
	}
	if got := inspect(t, r.claude, id).Effort(); got != "high" {
		t.Errorf("run effort %q", got)
	}
	if ai, _ := r.m.Agent(id); ai.Effort != "high" {
		t.Errorf("AgentInfo.Effort = %q", ai.Effort)
	}
	waitFor(t, "ModelChangedMsg", func() bool {
		return r.c.count(func(m tea.Msg) bool {
			mc, ok := m.(ModelChangedMsg)
			return ok && mc.AgentID == id && mc.Effort == "high"
		}) == 1
	})
	r.m.Flush()
	_, th, _ := r.st.LoadEntries(tid)
	if len(th.Members) != 1 || th.Members[0].Effort != "high" {
		t.Fatalf("stored members = %+v", th.Members)
	}
	r.m.Close()

	r2 := newRigIn(t, r.dir, nil, nil)
	if _, _, err := r2.m.Resume(tid); err != nil {
		t.Fatal(err)
	}
	if ai, _ := r2.m.Agent(id); ai.Effort != "high" {
		t.Errorf("resumed effort = %q", ai.Effort)
	}
}

func TestSetModelWithoutModelSetter(t *testing.T) {
	r := newRig(t, nil, nil) // plain spy runs: no ModelSetter
	tid := r.thread(t)
	id, _ := r.m.AddMember(tid, "claude", "sonnet")
	err := r.m.SetModel(id, "opus")
	if err == nil || err.Error() != "this agent can't switch model mid-session; start a new one" {
		t.Fatalf("err = %v", err)
	}
	if ai, _ := r.m.Agent(id); ai.Model != "sonnet" {
		t.Errorf("model changed to %q despite the error", ai.Model)
	}
	if n := r.c.count(func(m tea.Msg) bool { _, ok := m.(ModelChangedMsg); return ok }); n != 0 {
		t.Errorf("%d ModelChangedMsg emitted on failure", n)
	}
}

type listerAdapter struct {
	agent.Adapter
	calls atomic.Int32
	fail  atomic.Bool
}

func (l *listerAdapter) Models(ctx context.Context) ([]agent.ModelInfo, error) {
	l.calls.Add(1)
	if l.fail.Load() {
		return nil, errors.New("cli not logged in")
	}
	return []agent.ModelInfo{{ID: "default", Name: "Default", Default: true}, {ID: "opus", Name: "Opus"}}, nil
}

func TestModelsCachedPerKind(t *testing.T) {
	lister := &listerAdapter{Adapter: &stubAdapter{}}
	m := NewManager(map[string]agent.Adapter{
		"claude": lister,
		"codex":  &stubAdapter{}, // cannot list
	}, nil, testCfg())
	t.Cleanup(m.Close)
	ctx := context.Background()

	lister.fail.Store(true)
	if _, err := m.Models(ctx, "claude"); err == nil {
		t.Fatal("lister error swallowed")
	}
	lister.fail.Store(false)
	first, err := m.Models(ctx, "claude")
	if err != nil || len(first) != 2 || first[0].ID != "default" {
		t.Fatalf("models = %+v, %v", first, err)
	}
	first[0].ID = "mutated" // callers cannot corrupt the cache
	second, _ := m.Models(ctx, "claude")
	if second[0].ID != "default" {
		t.Error("cache shares memory with callers")
	}
	if got := lister.calls.Load(); got != 2 { // one failed call (not cached) + one good call
		t.Errorf("adapter asked %d times, want 2", got)
	}

	if ms, err := m.Models(ctx, "codex"); ms != nil || err != nil {
		t.Errorf("non-lister: %v, %v; want nil, nil", ms, err)
	}
	if _, err := m.Models(ctx, "gemini"); err == nil {
		t.Error("unknown kind accepted")
	}
}

func TestSetDefaultModelAppliesToLaterMembers(t *testing.T) {
	cfg := testCfg()
	cfg.Models = config.Models{Claude: "from-config", Codex: "codex-config"}
	r := newRigWith(t, t.TempDir(), false, cfg, nil, nil)
	tid := r.thread(t)

	a := r.add(t, tid, "claude")
	if sp, _ := r.claude.spec(a); sp.Model != "from-config" {
		t.Errorf("config default: spec model = %q", sp.Model)
	}
	r.m.SetDefaultModel("claude", "opus")
	b := r.add(t, tid, "claude")
	if sp, _ := r.claude.spec(b); sp.Model != "opus" {
		t.Errorf("SetDefaultModel: spec model = %q", sp.Model)
	}
	if sp, _ := r.claude.spec(a); sp.Model != "from-config" {
		t.Errorf("running member's spec rewritten: %q", sp.Model)
	}
	c, _ := r.m.AddMember(tid, "claude", "haiku")
	if sp, _ := r.claude.spec(c); sp.Model != "haiku" {
		t.Errorf("explicit model lost: %q", sp.Model)
	}
	d := r.add(t, tid, "codex")
	if sp, _ := r.codex.spec(d); sp.Model != "codex-config" {
		t.Errorf("other kind affected: %q", sp.Model)
	}
	r.m.SetDefaultModel("claude", "") // back to the CLI's own default
	e := r.add(t, tid, "claude")
	if sp, _ := r.claude.spec(e); sp.Model != "" {
		t.Errorf("empty default: spec model = %q", sp.Model)
	}
}
