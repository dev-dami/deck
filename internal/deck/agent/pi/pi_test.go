package pi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/acp/acptest"
)

// The test binary doubles as a scripted pi-acp (acptest, flavor "pi").
func TestMain(m *testing.M) {
	acptest.Main()
	os.Exit(m.Run())
}

type fake struct{ log, dir string }

func newFake(t *testing.T) fake {
	t.Helper()
	f := fake{log: filepath.Join(t.TempDir(), "acp.log"), dir: t.TempDir()}
	t.Setenv(acptest.EnvLog, f.log)
	t.Setenv(acptest.EnvDir, f.dir)
	t.Setenv(acptest.EnvFlavor, "pi")
	return f
}

func (f fake) has(t *testing.T, s string) bool {
	t.Helper()
	b, _ := os.ReadFile(f.log)
	return strings.Contains(string(b), s)
}

func (f fake) start(t *testing.T, spec agent.Spec) agent.Run {
	t.Helper()
	spec.ID, spec.Dir = "pi", f.dir
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	r, err := New(os.Args[0]).Start(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func turn(t *testing.T, r agent.Run, d agent.Decision) ([]agent.Event, []agent.ApprovalRequest) {
	t.Helper()
	var evs []agent.Event
	var reqs []agent.ApprovalRequest
	timeout := time.After(30 * time.Second)
	for {
		select {
		case e := <-r.Events():
			evs = append(evs, e)
			if e.Kind == agent.KindTurnDone || e.Kind == agent.KindExited {
				return evs, reqs
			}
		case req := <-r.Approvals():
			reqs = append(reqs, req)
			req.Respond(d)
		case <-timeout:
			t.Fatalf("no turn end; got %d events", len(evs))
		}
	}
}

// TestATurn covers one turn through pi-acp: the banner it prints after
// session/new is no reply, tools named by their title alone get Deck's
// names, and Pi's edit input gives the approval its diff.
func TestATurn(t *testing.T) {
	f := newFake(t)
	r := f.start(t, agent.Spec{})
	if ev := <-r.Events(); ev.Kind != agent.KindSessionStarted || ev.Text != "p/a" {
		t.Fatalf("first event = %+v", ev)
	}
	if err := r.Send("hi"); err != nil {
		t.Fatal(err)
	}
	evs, reqs := turn(t, r, agent.Decision{Allow: true, Always: true})
	if len(reqs) != 1 || reqs[0].Kind != agent.ApproveEdit || reqs[0].ToolName != "Edit" || reqs[0].Summary != "a.go" || reqs[0].Diff == nil {
		t.Fatalf("approvals = %+v", reqs)
	}
	if !f.has(t, `permission {"outcome":{"optionId":"always","outcome":"selected"}}`) {
		t.Error("allow always went back as allow_always")
	}
	var text []string
	tools := 0
	for _, e := range evs {
		if e.Kind == agent.KindAssistantText && !e.Delta {
			text = append(text, e.Text)
		}
		if e.Kind == agent.KindToolResult && e.Tool.Name == "Edit" && e.Tool.Status == agent.ToolOK {
			tools++
		}
	}
	if strings.Join(text, "|") != "Hello" || tools != 1 {
		t.Errorf("replies %q, edits %d", text, tools)
	}
}

// TestModels covers the models Pi has a key for, with each one's thinking
// levels: "off" is Deck's "none", and a model that only has "off" has none.
func TestModels(t *testing.T) {
	newFake(t)
	ms, err := New(os.Args[0]).Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].ID != "p/a" || !ms[0].Default || ms[0].Name != "Model A" || len(ms[0].Efforts) != 0 {
		t.Fatalf("models = %+v", ms)
	}
	var levels []string
	for _, e := range ms[1].Efforts {
		levels = append(levels, e.Level)
	}
	if strings.Join(levels, ",") != "none,low,high" || ms[1].DefaultEffort != "low" {
		t.Errorf("p/b efforts = %v, default %q", levels, ms[1].DefaultEffort)
	}
}

// TestSwitches covers a model and thinking level set at start and later:
// "none" goes out as Pi's "off", and no effort is the level the session
// started with.
func TestSwitches(t *testing.T) {
	f := newFake(t)
	r := f.start(t, agent.Spec{Model: "p/b", Effort: "high", Mode: agent.ModePlan})
	<-r.Events()
	for _, want := range []string{
		`"configId":"model","value":"p/b"`, `"configId":"thought_level","value":"high"`,
	} {
		if !f.has(t, want) {
			t.Errorf("missing %s", want)
		}
	}
	if f.has(t, `"configId":"mode"`) {
		t.Error("pi has no plan mode to set")
	}
	if err := r.(agent.EffortSetter).SetEffort("none"); err != nil || !f.has(t, `"configId":"thought_level","value":"off"`) {
		t.Errorf("none: %v", err)
	}
	if err := r.(agent.EffortSetter).SetEffort(""); err != nil || strings.Count(readLog(f), `"value":"low"`) != 1 {
		t.Errorf("default: %v\n%s", err, readLog(f))
	}
	if err := r.(agent.ModeSetter).SetMode(agent.ModeYolo); err != nil {
		t.Errorf("modes are Deck's alone: %v", err)
	}
	if err := r.(agent.ModelSetter).SetModel("p/a"); err != nil || !f.has(t, `"configId":"model","value":"p/a"`) {
		t.Errorf("set model: %v", err)
	}
}

func readLog(f fake) string { b, _ := os.ReadFile(f.log); return string(b) }

func TestNotContractSafe(t *testing.T) {
	a := New("")
	if a.Name() != "pi" || a.ContractSafe() {
		t.Error("pi runs its tools without asking: never a contract")
	}
	_, err := New("no-such-pi-acp").Start(context.Background(), agent.Spec{Dir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), Install) {
		t.Errorf("err = %v", err)
	}
}
