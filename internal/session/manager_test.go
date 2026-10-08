package session

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/agent/fake"
	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/store"
)

func testCfg() config.Config {
	return config.Config{UserName: "Tester", DefaultAgent: "claude", Motion: "off"}
}

func newTestManager(t *testing.T) (*Manager, *store.Store) {
	t.Helper()
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(map[string]agent.Adapter{
		"claude": fake.New(fake.DemoScript("claude"), 0),
		"codex":  fake.New(fake.DemoCodexScript(), 0),
	}, st, testCfg())
	t.Cleanup(m.Close)
	return m, st
}

// pump reads Updates until stop returns true, recording everything.
type log struct {
	mu        sync.Mutex
	events    []agent.Event
	approvals []ApprovalMsg
	gone      []ApprovalGoneMsg
}

func (l *log) next(t *testing.T, m *Manager, d time.Duration) (tea.Msg, bool) {
	t.Helper()
	select {
	case msg := <-m.Updates():
		l.mu.Lock()
		defer l.mu.Unlock()
		switch v := msg.(type) {
		case EventMsg:
			l.events = append(l.events, v.Event)
		case ApprovalMsg:
			l.approvals = append(l.approvals, v)
		case ApprovalGoneMsg:
			l.gone = append(l.gone, v)
		}
		return msg, true
	case <-time.After(d):
		return nil, false
	}
}

func (l *log) until(t *testing.T, m *Manager, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		l.next(t, m, 100*time.Millisecond)
	}
}

func (l *log) count(f func(agent.Event) bool) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, e := range l.events {
		if f(e) {
			n++
		}
	}
	return n
}

func turnDones(id string) func(agent.Event) bool {
	return func(e agent.Event) bool { return e.AgentID == id && e.Kind == agent.KindTurnDone }
}

func TestStartIDsAndThreeConcurrent(t *testing.T) {
	m, st := newTestManager(t)
	var ids []string
	for _, k := range []string{"claude", "claude", "codex"} {
		id, err := m.Start(k, "do the thing", t.TempDir(), "")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if got, want := ids, []string{"claude", "claude2", "codex"}; got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("ids = %v, want %v", got, want)
	}

	var l log
	l.until(t, m, "three approvals", func() bool { return len(l.approvals) == 3 })

	for _, a := range m.Agents() {
		if a.Status != StatusApproval || a.Pending != 1 {
			t.Errorf("%s: status=%s pending=%d, want approval/1", a.ID, a.Status, a.Pending)
		}
		if a.ThreadID == "" || a.Title != "do the thing" {
			t.Errorf("%s: thread=%q title=%q", a.ID, a.ThreadID, a.Title)
		}
	}
	seen := map[string]bool{}
	for _, ap := range l.approvals {
		seen[ap.AgentID] = true
		ap.Respond(agent.Decision{Allow: true})
		ap.Respond(agent.Decision{Allow: false}) // second call must be harmless
	}
	if len(seen) != 3 {
		t.Fatalf("approvals came from %v", seen)
	}
	l.until(t, m, "three turn_done", func() bool {
		return l.count(func(e agent.Event) bool { return e.Kind == agent.KindTurnDone }) == 3
	})

	total := 0.0
	for _, a := range m.Agents() {
		if a.Status != StatusIdle || a.Pending != 0 {
			t.Errorf("%s: status=%s pending=%d, want idle/0", a.ID, a.Status, a.Pending)
		}
		total += a.CostUSD
	}
	if math.Abs(total-0.28) > 1e-9 {
		t.Errorf("total cost = %v, want 0.28", total)
	}

	m.Flush()
	for _, a := range m.Agents() {
		evs, th, err := st.Load(a.ThreadID)
		if err != nil {
			t.Fatal(err)
		}
		if len(evs) == 0 {
			t.Errorf("%s: no events persisted", a.ID)
		}
		for _, e := range evs {
			if e.Delta {
				t.Errorf("%s: delta persisted", a.ID)
			}
		}
		if th.SessionID == "" {
			t.Errorf("%s: session id not stored", a.ID)
		}
		if math.Abs(th.CostUSD-a.CostUSD) > 1e-9 {
			t.Errorf("%s: stored cost %v != %v", a.ID, th.CostUSD, a.CostUSD)
		}
	}
}

func TestInterruptDeniesPendingAndGoesIdle(t *testing.T) {
	m, _ := newTestManager(t)
	id, err := m.Start("codex", "rebuild", t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	var l log
	l.until(t, m, "approval", func() bool { return len(l.approvals) == 1 })
	if a, _ := m.Agent(id); a.Status != StatusApproval {
		t.Fatalf("status = %s", a.Status)
	}
	if err := m.Interrupt(id); err != nil {
		t.Fatal(err)
	}
	l.until(t, m, "turn_done", func() bool { return l.count(turnDones(id)) == 1 })
	if len(l.gone) != 1 || l.gone[0].ID != l.approvals[0].ID {
		t.Fatalf("gone = %+v", l.gone)
	}
	if a, _ := m.Agent(id); a.Status != StatusIdle || a.Pending != 0 {
		t.Fatalf("after interrupt: %+v", a)
	}
}

func TestDenyEndsTurn(t *testing.T) {
	m, _ := newTestManager(t)
	id, _ := m.Start("codex", "rebuild", t.TempDir(), "")
	var l log
	l.until(t, m, "approval", func() bool { return len(l.approvals) == 1 })
	l.approvals[0].Respond(agent.Decision{Allow: false, Reason: "no"})
	l.until(t, m, "turn_done", func() bool { return l.count(turnDones(id)) == 1 })
	denied := l.count(func(e agent.Event) bool {
		return e.Kind == agent.KindToolResult && e.Tool != nil && e.Tool.Status == agent.ToolError
	})
	if denied != 1 {
		t.Fatalf("denied tool results = %d", denied)
	}
}

func TestSendFollowUpAndBusy(t *testing.T) {
	m, _ := newTestManager(t)
	id, _ := m.Start("codex", "", t.TempDir(), "")
	var l log
	l.until(t, m, "approval", func() bool { return len(l.approvals) == 1 })
	if err := m.Send(id, "again"); err == nil {
		t.Fatal("Send while a turn is running should fail")
	}
	if a, _ := m.Agent(id); a.Status != StatusApproval {
		t.Fatalf("failed Send must not disturb status, got %s", a.Status)
	}
	l.approvals[0].Respond(agent.Decision{Allow: true})
	l.until(t, m, "first turn", func() bool { return l.count(turnDones(id)) == 1 })

	if err := m.Send(id, "second request"); err != nil {
		t.Fatal(err)
	}
	l.until(t, m, "second approval", func() bool { return len(l.approvals) == 2 })
	l.approvals[1].Respond(agent.Decision{Allow: true})
	l.until(t, m, "second turn", func() bool { return l.count(turnDones(id)) == 2 })
	echo := l.count(func(e agent.Event) bool { return e.Kind == agent.KindUserText && e.Text == "second request" })
	if echo != 1 {
		t.Fatalf("user_text echoes = %d, want exactly 1 (the manager must not add its own)", echo)
	}
	a, _ := m.Agent(id)
	if math.Abs(a.CostUSD-0.08) > 1e-9 {
		t.Fatalf("cost over two turns = %v, want 0.08", a.CostUSD)
	}
	if a.Title != "" && a.Title == "second request" {
		t.Fatalf("title must stay the first prompt")
	}
}

func TestRename(t *testing.T) {
	m, st := newTestManager(t)
	a, _ := m.Start("claude", "x", t.TempDir(), "")
	b, _ := m.Start("claude", "y", t.TempDir(), "")
	if err := m.Rename(a, "auth"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "two words", "@x", "auth", a} {
		if err := m.Rename(b, bad); err == nil {
			t.Errorf("Rename(%q) should fail", bad)
		}
	}
	info, _ := m.Agent(a)
	if info.Name != "auth" || info.ID != a {
		t.Fatalf("info = %+v", info)
	}
	m.Flush()
	_, th, _ := st.Load(info.ThreadID)
	if th.Name != "auth" {
		t.Fatalf("stored name = %q", th.Name)
	}
	if err := m.Rename("nope", "z"); err == nil {
		t.Fatal("unknown agent")
	}
}

type spy struct {
	agent.Adapter
	mu    sync.Mutex
	specs []agent.Spec
}

func (s *spy) Start(ctx context.Context, sp agent.Spec) (agent.Run, error) {
	s.mu.Lock()
	s.specs = append(s.specs, sp)
	s.mu.Unlock()
	return s.Adapter.Start(ctx, sp)
}

func TestResumeLoadsHistoryAndPassesSessionID(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.New(dir)
	sp := &spy{Adapter: fake.New(fake.DemoCodexScript(), 0)}
	m := NewManager(map[string]agent.Adapter{"codex": sp}, st, testCfg())
	id, _ := m.Start("codex", "rebuild dist", t.TempDir(), "gpt-x")
	var l log
	l.until(t, m, "approval", func() bool { return len(l.approvals) == 1 })
	l.approvals[0].Respond(agent.Decision{Allow: true})
	l.until(t, m, "done", func() bool { return l.count(turnDones(id)) == 1 })
	info, _ := m.Agent(id)
	m.Close()

	st2, _ := store.New(dir)
	m2 := NewManager(map[string]agent.Adapter{"codex": sp}, st2, testCfg())
	defer m2.Close()
	rid, hist, err := m2.Resume(info.ThreadID)
	if err != nil {
		t.Fatal(err)
	}
	if rid != "codex" || len(hist) == 0 {
		t.Fatalf("id=%s history=%d", rid, len(hist))
	}
	for _, e := range hist {
		if e.AgentID != rid {
			t.Fatalf("history event has AgentID %q", e.AgentID)
		}
	}
	sp.mu.Lock()
	got := sp.specs[len(sp.specs)-1]
	sp.mu.Unlock()
	if got.ResumeID != "fake-demo-codex" || got.Model != "gpt-x" || got.FirstPrompt != "" {
		t.Fatalf("resume spec = %+v", got)
	}
	again, h2, err := m2.Resume(info.ThreadID)
	if err != nil || again != rid || h2 != nil {
		t.Fatalf("second resume should return the running agent, got %s %v %v", again, h2, err)
	}
	if a, _ := m2.Agent(rid); math.Abs(a.CostUSD-0.04) > 1e-9 {
		t.Fatalf("resumed cost = %v", a.CostUSD)
	}
}

func TestCloseUnblocksListenAndIsIdempotent(t *testing.T) {
	m, _ := newTestManager(t)
	_, _ = m.Start("codex", "x", t.TempDir(), "")
	res := make(chan tea.Msg, 1)
	go func() {
		// drain until Close, then Listen must return nil promptly
		for {
			msg := m.Listen()()
			if msg == nil {
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
	if _, err := m.Start("claude", "x", "", ""); err == nil {
		t.Fatal("Start after Close should fail")
	}
}

func TestUnknownKind(t *testing.T) {
	m, _ := newTestManager(t)
	if _, err := m.Start("nope", "", "", ""); err == nil {
		t.Fatal("expected error")
	}
	if got := m.Kinds(); len(got) != 2 || got[0] != "claude" || got[1] != "codex" {
		t.Fatalf("kinds = %v", got)
	}
}
