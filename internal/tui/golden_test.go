package tui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/muesli/termenv"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/agent/fake"
	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/store"
	"github.com/dev-dami/deck/internal/tui/motion"
)

func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.Ascii)
	lipgloss.SetHasDarkBackground(true)
	motion.SetReduced(true)
	os.Exit(m.Run())
}

func testConfig() config.Config {
	return config.Config{UserName: "Ada", DefaultAgent: "claude", Motion: "on", Bell: false,
		ClaudeBinary: "claude", CodexBinary: "codex"}
}

func repoDir() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, "repo")
}

// script helpers --------------------------------------------------------

func ev(k agent.Kind) agent.Event { return agent.Event{Kind: k} }

func delta(s string) fake.Step {
	return fake.Step{Event: agent.Event{Kind: agent.KindAssistantText, Text: s, Delta: true}}
}

func final(s string) fake.Step {
	return fake.Step{Event: agent.Event{Kind: agent.KindAssistantText, Text: s}}
}

func toolCall(id, name, title string) fake.Step {
	return fake.Step{Event: agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{ID: id, Name: name, Title: title, Status: agent.ToolRunning}}}
}

func toolResult(id, name, title, out string) fake.Step {
	return fake.Step{Event: agent.Event{Kind: agent.KindToolResult, Tool: &agent.Tool{ID: id, Name: name, Title: title, Output: out, Status: agent.ToolOK}}}
}

// demoDiff pulls the store diff out of the stock Claude demo script.
func demoDiff() *agent.FileDiff {
	for _, st := range fake.DemoScript("claude") {
		if st.Event.Kind == agent.KindFileChange {
			return st.Event.Diff
		}
	}
	panic("no diff in demo script")
}

const planText = "I'll check the store layer before changing anything. `Append` currently writes every event, so streaming deltas bloat the thread files.\n\n- read `store.go` to see the current layout\n- drop streaming deltas before they reach disk\n- run the package tests\n"

func midStreamScript() fake.Script {
	return fake.Script{
		{Event: agent.Event{Kind: agent.KindSessionStarted, SessionID: "s-mid"}},
		{Event: agent.Event{Kind: agent.KindReasoning, Text: "The user wants streaming deltas kept out of the thread files."}},
		final(planText),
		toolCall("r1", "Read", "internal/store/store.go"),
		toolResult("r1", "Read", "internal/store/store.go", "148 lines read"),
		toolCall("e1", "Edit", "internal/store/store.go"),
		{Event: agent.Event{Kind: agent.KindFileChange, Diff: demoDiff()}},
		toolResult("e1", "Edit", "internal/store/store.go", "Applied 2 hunks"),
		toolCall("b1", "Bash", "go test ./..."),
		toolResult("b1", "Bash", "go test ./...", "ok  \tgithub.com/dev-dami/deck/internal/store\t0.41s\nPASS"),
		delta("The store now drops streaming deltas, "),
		delta("and the package tests pass. Next I'm"),
	}
}

func doneScript() fake.Script {
	s := midStreamScript()
	s = s[:len(s)-2]
	s = append(s, final("The store now drops streaming deltas, and `go test ./...` passes."),
		fake.Step{Event: agent.Event{Kind: agent.KindUsage, Usage: &agent.Usage{CostUSD: 0.12}}},
		fake.Step{Event: ev(agent.KindTurnDone)})
	return s
}

// harness ---------------------------------------------------------------

type harness struct {
	t   *testing.T
	tm  *teatest.TestModel
	mgr *session.Manager
	mu  sync.Mutex
	buf bytes.Buffer
}

func newHarness(t *testing.T, w, h int, adapters map[string]agent.Adapter) *harness {
	t.Helper()
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	mgr := session.NewManager(adapters, st, cfg)
	t.Cleanup(mgr.Close)
	app := NewWithOptions(mgr, cfg, Options{Dir: repoDir(), SaveConfig: func(config.Config) error { return nil }})
	tm := teatest.NewTestModel(t, app, teatest.WithInitialTermSize(w, h))
	hr := &harness{t: t, tm: tm, mgr: mgr}
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() { // keep draining output so the program never blocks
		r := tm.Output()
		b := make([]byte, 32*1024)
		for {
			n, _ := r.Read(b)
			if n > 0 {
				hr.mu.Lock()
				hr.buf.Write(b[:n])
				hr.mu.Unlock()
				continue
			}
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Millisecond):
			}
		}
	}()
	return hr
}

// waitFor blocks until s has been drawn.
func (h *harness) waitFor(s string) {
	h.t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		got := strings.Contains(ansi.Strip(h.buf.String()), s)
		h.mu.Unlock()
		if got {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.t.Fatalf("timed out waiting for %q; last output:\n%s", s, tail(ansi.Strip(h.buf.String()), 3000))
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

func (h *harness) type_(s string) { h.tm.Type(s) }
func (h *harness) key(t tea.KeyType) {
	h.tm.Send(tea.KeyMsg{Type: t})
}

// settle lets queued messages drain before the final snapshot.
func (h *harness) settle() { time.Sleep(150 * time.Millisecond) }

// finish quits and returns the final View.
func (h *harness) finish(w, hgt int) string {
	h.t.Helper()
	h.settle()
	h.tm.Send(tea.QuitMsg{})
	fm := h.tm.FinalModel(h.t, teatest.WithFinalTimeout(5*time.Second))
	view := fm.View()
	checkShape(h.t, view, w, hgt)
	return view
}

// checkShape enforces the invariants that hold at every size.
func checkShape(t *testing.T, view string, w, h int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) != h {
		t.Errorf("view has %d lines, want %d", len(lines), h)
	}
	for i, l := range lines {
		if got := ansi.StringWidth(l); got > w {
			t.Errorf("line %d is %d cells wide, max %d: %q", i, got, w, l)
		}
	}
}

func claudeOnly(s fake.Script) map[string]agent.Adapter {
	return map[string]agent.Adapter{"claude": fake.New(s, 0), "codex": fake.New(fake.DemoCodexScript(), 0)}
}

var sizes = []struct{ w, h int }{{80, 24}, {140, 40}}

func forSizes(t *testing.T, f func(t *testing.T, w, h int)) {
	for _, sz := range sizes {
		sz := sz
		t.Run(fmt.Sprintf("%dx%d", sz.w, sz.h), func(t *testing.T) { f(t, sz.w, sz.h) })
	}
}

func requireGolden(t *testing.T, view string) {
	t.Helper()
	// trailing spaces carry no meaning; keep goldens diff-friendly
	lines := strings.Split(ansi.Strip(view), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	golden.RequireEqual(t, []byte(strings.Join(lines, "\n")))
}

// scenarios -------------------------------------------------------------

func TestGoldenEmpty(t *testing.T) {
	forSizes(t, func(t *testing.T, w, h int) {
		hr := newHarness(t, w, h, claudeOnly(midStreamScript()))
		hr.waitFor("/ for commands")
		requireGolden(t, hr.finish(w, h))
	})
}

func TestGoldenMidStream(t *testing.T) {
	forSizes(t, func(t *testing.T, w, h int) {
		hr := newHarness(t, w, h, claudeOnly(midStreamScript()))
		hr.type_("refactor the store")
		hr.key(tea.KeyEnter)
		hr.waitFor("Next I'm")
		requireGolden(t, hr.finish(w, h))
	})
}

func TestGoldenExpandedDiff(t *testing.T) {
	forSizes(t, func(t *testing.T, w, h int) {
		hr := newHarness(t, w, h, claudeOnly(doneScript()))
		hr.type_("refactor the store")
		hr.key(tea.KeyEnter)
		hr.waitFor("claude finished")
		hr.key(tea.KeyTab) // focus the transcript
		hr.tm.Type("[")    // cursor to the previous foldable row (Bash -> Edit)
		hr.key(tea.KeyEnter)
		hr.settle()
		requireGolden(t, hr.finish(w, h))
	})
}

func TestGoldenApprovalCard(t *testing.T) {
	forSizes(t, func(t *testing.T, w, h int) {
		hr := newHarness(t, w, h, claudeOnly(midStreamScript()))
		hr.type_("@codex rebuild dist")
		hr.key(tea.KeyEnter)
		hr.waitFor("y allow")
		requireGolden(t, hr.finish(w, h))
	})
}

func TestGoldenInbox(t *testing.T) {
	forSizes(t, func(t *testing.T, w, h int) {
		ad := map[string]agent.Adapter{
			"claude": fake.New(fake.DemoScript("claude"), 0),
			"codex":  fake.New(fake.DemoCodexScript(), 0),
		}
		hr := newHarness(t, w, h, ad)
		hr.type_("@codex rebuild dist")
		hr.key(tea.KeyEnter)
		hr.waitFor("wants to run")
		hr.type_("/new")
		hr.key(tea.KeyEnter)
		hr.type_("@claude tidy the store")
		hr.key(tea.KeyEnter)
		hr.waitFor("wants to edit")
		hr.settle()
		hr.type_("i")
		hr.settle()
		requireGolden(t, hr.finish(w, h))
	})
}

func TestGoldenAtMenu(t *testing.T) {
	forSizes(t, func(t *testing.T, w, h int) {
		hr := newHarness(t, w, h, claudeOnly(doneScript()))
		hr.type_("refactor the store")
		hr.key(tea.KeyEnter)
		hr.waitFor("claude finished")
		hr.type_("@")
		hr.settle()
		requireGolden(t, hr.finish(w, h))
	})
}

// shared threads -------------------------------------------------------------
//
// These drive the model by hand (see rig) so the order of events is exactly the
// one written down.

// drain feeds manager updates until none arrive for a moment.
func (r *rig) drain() {
	for {
		select {
		case msg := <-r.mgr.Updates():
			r.feed(msg)
		case <-time.After(60 * time.Millisecond):
			return
		}
	}
}

func toolRow(r *rig, run *stubRun, id, name, title, out string) {
	r.event(run, agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{ID: id, Name: name, Title: title, Status: agent.ToolRunning}})
	r.event(run, agent.Event{Kind: agent.KindToolResult, Tool: &agent.Tool{ID: id, Name: name, Title: title, Output: out, Status: agent.ToolOK}})
	r.drain()
}

// duoMidCollaboration: Claude has planned and asked Codex for a review, Codex
// has answered with an @claude line, and Claude has started on the test.
func duoMidCollaboration(t *testing.T, w, h int) *rig {
	r := newRig(t, w, h)
	r.say("@claude plan the store refactor, @codex review it")
	r.pump("two members", func() bool { return len(r.m.curThread().Members) == 2 })
	r.drain()
	cl, cx := r.claude.last(), r.codex.last()

	toolRow(r, cl, "c1", "Read", "internal/store/store.go", "148 lines read")
	say(cl, r, "Plan for the store change:\n\n- drop streaming deltas before they reach disk\n- keep one lock per thread\n- add a test that appends 1000 deltas")
	r.drain()
	r.relay("claude", "codex", "Please review this plan for the store change and tell me what you would change.")
	r.drain()

	toolRow(r, cx, "x0", "read_thread", "shared thread", "the thread")
	toolRow(r, cx, "x1", "Read", "internal/store/store.go", "148 lines read")
	say(cx, r, "The plan holds. One change: take the per-thread lock before the index lock, never the other way round, or Append and List can deadlock.\n\n@claude agree on the lock order and add the 1000-delta test.")
	r.drain()
	// what the manager emits for the "@claude ..." line of a final reply
	r.feed(session.RelayMsg{ThreadID: r.m.sel, From: "codex", To: []string{"claude"}, Via: "mention", Text: "agree on the lock order and add the 1000-delta test."})
	r.drain()

	r.event(cl, agent.Event{Kind: agent.KindAssistantText, Text: "Good catch on the lock order. Adding the delta test", Delta: true})
	r.event(cl, agent.Event{Kind: agent.KindUsage, Usage: &agent.Usage{CostUSD: 0.09}})
	r.event(cx, agent.Event{Kind: agent.KindUsage, Usage: &agent.Usage{CostUSD: 0.04}})
	r.drain()
	return r
}

func TestGoldenDuo(t *testing.T) {
	forSizes(t, func(t *testing.T, w, h int) {
		r := duoMidCollaboration(t, w, h)
		v := r.m.View()
		checkShape(t, v, w, h)
		requireGolden(t, v)
	})
}

func TestGoldenPaused(t *testing.T) {
	forSizes(t, func(t *testing.T, w, h int) {
		r := newRigHops(t, w, h, 1)
		r.say("@claude plan the store refactor, @codex review it")
		r.pump("two members", func() bool { return len(r.m.curThread().Members) == 2 })
		r.drain()
		say(r.claude.last(), r, "Plan: drop streaming deltas before they reach disk.")
		r.drain()
		r.done(r.claude.last(), "claude")
		r.relay("claude", "codex", "Please review the plan.")
		r.relay("codex", "claude", "Looks good - go ahead and apply it.")
		r.pump("paused", func() bool { return r.m.curThread().Paused && r.has("paused after") })
		r.drain()
		v := r.m.View()
		checkShape(t, v, w, h)
		requireGolden(t, v)
	})
}

func TestGoldenAtMenuMembers(t *testing.T) {
	forSizes(t, func(t *testing.T, w, h int) {
		r := newRig(t, w, h)
		r.start("tidy the store")
		if _, err := r.mgr.AddMember(r.m.sel, "claude", ""); err != nil {
			t.Fatal(err)
		}
		r.pump("second claude", func() bool { return len(r.m.curThread().Members) == 2 })
		r.drain()
		r.typeText("@")
		v := r.m.View()
		checkShape(t, v, w, h)
		requireGolden(t, v)
	})
}

func TestGoldenApprovalInSpeakersBlock(t *testing.T) {
	forSizes(t, func(t *testing.T, w, h int) {
		r := newRig(t, w, h)
		r.say("@claude plan the store refactor, @codex review it")
		r.pump("two members", func() bool { return len(r.m.curThread().Members) == 2 })
		r.drain()
		say(r.claude.last(), r, "I will keep the delta change small.")
		r.drain()
		r.approval(r.codex.last(), "x1", "go test ./internal/store/...", agent.ApproveCommand)
		r.pump("card", func() bool { return len(r.m.pending) == 1 })
		r.drain()
		v := r.m.View()
		checkShape(t, v, w, h)
		requireGolden(t, v)
	})
}
