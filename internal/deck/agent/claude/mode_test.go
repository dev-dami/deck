package claude

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// launches polls the fake-claude args file until it holds at least n launches.
func launches(t *testing.T, f string, n int) [][]string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if b, err := os.ReadFile(f); err == nil && strings.TrimSpace(string(b)) != "" {
			if all := readArgs(t, f); len(all) >= n {
				return all
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %d launches", n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func launchCount(t *testing.T, f string) int {
	t.Helper()
	b, err := os.ReadFile(f)
	if err != nil || strings.TrimSpace(string(b)) == "" {
		return 0
	}
	return len(readArgs(t, f))
}

func startFake(t *testing.T, mut func(*agent.Spec)) (agent.Run, string) {
	t.Helper()
	a, spec, argsFile := newFake(t)
	if mut != nil {
		mut(&spec)
	}
	r, err := a.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r, argsFile
}

func noExitOrDone(t *testing.T, evs []agent.Event, wantDone int) {
	t.Helper()
	done := 0
	for _, e := range evs {
		switch e.Kind {
		case agent.KindExited:
			t.Fatalf("deliberate restart must not report exit: %v", kinds(evs))
		case agent.KindTurnDone:
			done++
		}
	}
	if done != wantDone {
		t.Fatalf("turn_done count %d want %d: %v", done, wantDone, kinds(evs))
	}
}

func TestModeFlags(t *testing.T) {
	cases := []struct {
		mode agent.Mode
		want string // "" = no --permission-mode
	}{
		{"", ""},
		{agent.ModeAsk, ""},
		{agent.ModePlan, "plan"},
		{agent.ModeYolo, "bypassPermissions"},
	}
	for _, c := range cases {
		t.Run("mode_"+string(c.mode), func(t *testing.T) {
			r, argsFile := startFake(t, func(s *agent.Spec) { s.Mode = c.mode; s.FirstPrompt = "go" })
			waitKind(t, r, agent.KindTurnDone)
			args := readArgs(t, argsFile)[0]
			v, ok := argAfter(args, "--permission-mode")
			if ok != (c.want != "") || v != c.want {
				t.Errorf("--permission-mode = %q (present=%v) want %q", v, ok, c.want)
			}
			// MCP config and messaging tools stay in every mode.
			if p, _ := argAfter(args, "--permission-prompt-tool"); p != "mcp__deck__approve" {
				t.Errorf("permission-prompt-tool %q", p)
			}
			if _, ok := argAfter(args, "--mcp-config"); !ok {
				t.Error("no --mcp-config")
			}
			if v, _ := argAfter(args, "--allowedTools"); v != "mcp__deck__send_message,mcp__deck__read_thread" {
				t.Errorf("allowedTools %q", v)
			}
			if contains(args, "--dangerously-skip-permissions") {
				t.Error("must use --permission-mode, not --dangerously-skip-permissions")
			}
		})
	}
}

func TestSetModeAndModelIdleRestartWithResume(t *testing.T) {
	r, argsFile := startFake(t, nil)
	ms, ok := r.(agent.ModeSetter)
	if !ok {
		t.Fatal("run is not a ModeSetter")
	}
	mdl, ok := r.(agent.ModelSetter)
	if !ok {
		t.Fatal("run is not a ModelSetter")
	}
	if err := r.Send("first"); err != nil {
		t.Fatal(err)
	}
	waitKind(t, r, agent.KindTurnDone)
	sid := r.SessionID()
	if sid == "" {
		t.Fatal("no session id")
	}

	if err := ms.SetMode(agent.ModePlan); err != nil {
		t.Fatal(err)
	}
	all := launches(t, argsFile, 2)
	if v, _ := argAfter(all[1], "--resume"); v != sid {
		t.Errorf("resume %q want %q", v, sid)
	}
	if v, _ := argAfter(all[1], "--permission-mode"); v != "plan" {
		t.Errorf("mode after SetMode: %q", v)
	}
	if v, _ := argAfter(all[1], "--model"); v != "sonnet" {
		t.Errorf("model kept: %q", v)
	}
	if _, ok := argAfter(all[1], "--mcp-config"); !ok {
		t.Error("mcp config lost on restart")
	}

	if err := mdl.SetModel("opus"); err != nil {
		t.Fatal(err)
	}
	all = launches(t, argsFile, 3)
	if v, _ := argAfter(all[2], "--model"); v != "opus" {
		t.Errorf("model after SetModel: %q", v)
	}
	if v, _ := argAfter(all[2], "--permission-mode"); v != "plan" {
		t.Errorf("mode kept across SetModel: %q", v)
	}
	if v, _ := argAfter(all[2], "--resume"); v != sid {
		t.Errorf("resume %q want %q", v, sid)
	}

	// SetModel("") drops --model (CLI default).
	if err := mdl.SetModel(""); err != nil {
		t.Fatal(err)
	}
	all = launches(t, argsFile, 4)
	if contains(all[3], "--model") {
		t.Errorf("--model should be gone: %v", all[3])
	}

	// agent still works after the restarts
	if err := r.Send("again"); err != nil {
		t.Fatal(err)
	}
	evs := waitKind(t, r, agent.KindTurnDone)
	noExitOrDone(t, evs, 1)
}

func TestSetModeSendDuringRestartIsQueued(t *testing.T) {
	r, argsFile := startFake(t, nil)
	if err := r.Send("first"); err != nil {
		t.Fatal(err)
	}
	waitKind(t, r, agent.KindTurnDone)

	if err := r.(agent.ModeSetter).SetMode(agent.ModeYolo); err != nil {
		t.Fatal(err)
	}
	// immediately, while the restart is in flight
	if err := r.Send("queued one"); err != nil {
		t.Fatal(err)
	}
	if err := r.Send("queued two"); err != nil {
		t.Fatal(err)
	}
	var evs []agent.Event
	for turns := 0; turns < 2; turns++ {
		evs = append(evs, waitKind(t, r, agent.KindTurnDone)...)
	}
	var users []string
	for _, e := range evs {
		if e.Kind == agent.KindUserText {
			users = append(users, e.Text)
		}
	}
	if strings.Join(users, "|") != "queued one|queued two" {
		t.Errorf("user texts %v", users)
	}
	noExitOrDone(t, evs, 2)
	all := readArgs(t, argsFile)
	if len(all) != 2 {
		t.Fatalf("launches %d want 2", len(all))
	}
	if v, _ := argAfter(all[1], "--permission-mode"); v != "bypassPermissions" {
		t.Errorf("restart mode %q", v)
	}
}

func TestSetModeBusyRestartsAfterTurnDone(t *testing.T) {
	r, argsFile := startFake(t, nil)
	if err := r.Send("first"); err != nil {
		t.Fatal(err)
	}
	waitKind(t, r, agent.KindTurnDone)
	sid := r.SessionID()

	if err := r.Send("SLOW turn"); err != nil {
		t.Fatal(err)
	}
	if err := r.(agent.ModeSetter).SetMode(agent.ModePlan); err != nil {
		t.Fatal(err)
	}
	if err := r.(agent.ModelSetter).SetModel("haiku"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if n := launchCount(t, argsFile); n != 1 {
		t.Fatalf("restart must wait for the turn to finish; launches=%d", n)
	}
	// the turn completes untouched, then the restart happens
	evs := waitKind(t, r, agent.KindTurnDone)
	noExitOrDone(t, evs, 1)
	// next Send straight after TurnDone: must go to the restarted process
	if err := r.Send("after"); err != nil {
		t.Fatal(err)
	}
	evs = waitKind(t, r, agent.KindTurnDone)
	noExitOrDone(t, evs, 1)
	all := readArgs(t, argsFile)
	if len(all) != 2 {
		t.Fatalf("launches %d want 2 (one restart for both changes)", len(all))
	}
	if v, _ := argAfter(all[1], "--resume"); v != sid {
		t.Errorf("resume %q want %q", v, sid)
	}
	if v, _ := argAfter(all[1], "--permission-mode"); v != "plan" {
		t.Errorf("mode %q", v)
	}
	if v, _ := argAfter(all[1], "--model"); v != "haiku" {
		t.Errorf("model %q", v)
	}
}

func TestSetModeModelUnchangedNoRestart(t *testing.T) {
	r, argsFile := startFake(t, func(s *agent.Spec) { s.Mode = agent.ModePlan })
	if err := r.Send("first"); err != nil {
		t.Fatal(err)
	}
	waitKind(t, r, agent.KindTurnDone)
	ms, mdl := r.(agent.ModeSetter), r.(agent.ModelSetter)
	for _, err := range []error{ms.SetMode(agent.ModePlan), mdl.SetModel("sonnet")} {
		if err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(300 * time.Millisecond)
	if n := launchCount(t, argsFile); n != 1 {
		t.Errorf("launches %d want 1", n)
	}
	if err := ms.SetMode("bogus"); err == nil {
		t.Error("unknown mode should be rejected")
	}
}

func TestSetModeBeforeFirstTurnNoResume(t *testing.T) {
	r, argsFile := startFake(t, nil)
	// Let the first process log its args: on Linux the restart's kill can
	// otherwise land before it does, leaving only one launch on record.
	launches(t, argsFile, 1)
	if err := r.(agent.ModeSetter).SetMode(agent.ModeYolo); err != nil {
		t.Fatal(err)
	}
	all := launches(t, argsFile, 2)
	if contains(all[1], "--resume") {
		t.Errorf("no session yet, must not --resume: %v", all[1])
	}
	if v, _ := argAfter(all[1], "--permission-mode"); v != "bypassPermissions" {
		t.Errorf("mode %q", v)
	}
	if err := r.Send("go"); err != nil {
		t.Fatal(err)
	}
	waitKind(t, r, agent.KindTurnDone)
}

func TestSetModeAfterClose(t *testing.T) {
	r, _ := startFake(t, nil)
	r.Close()
	if err := r.(agent.ModeSetter).SetMode(agent.ModePlan); err == nil {
		t.Error("SetMode after close should fail")
	}
	if err := r.(agent.ModelSetter).SetModel("opus"); err == nil {
		t.Error("SetModel after close should fail")
	}
}

// ---- model list ----

func readHelpSample(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/help_model.txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseModelAliases(t *testing.T) {
	help := readHelpSample(t)
	got := parseModelAliases(help)
	if strings.Join(got, ",") != "fable,opus,sonnet" {
		t.Errorf("aliases %v", got)
	}
	// CRLF help text (Windows) parses the same
	if g := parseModelAliases(strings.ReplaceAll(help, "\n", "\r\n")); strings.Join(g, ",") != "fable,opus,sonnet" {
		t.Errorf("crlf aliases %v", g)
	}
	if parseModelAliases("Usage: claude\n  --verbose  Override 'verbose' mode\n") != nil {
		t.Error("no --model entry should yield nil")
	}
}

func idsOf(ms []agent.ModelInfo) string {
	var ids []string
	for _, m := range ms {
		ids = append(ids, m.ID)
	}
	return strings.Join(ids, ",")
}

func TestModelsFromHelpAndCache(t *testing.T) {
	a := New("", "deck", nil).(*adapter)
	calls := 0
	a.initFn = noInit
	a.helpFn = func(context.Context) (string, error) { calls++; return readHelpSample(t), nil }
	ms, err := a.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.Name == "" || m.Description == "" {
			t.Errorf("incomplete %+v", m)
		}
	}
	if idsOf(ms) != ",fable,opus,sonnet" {
		t.Errorf("ids %q", idsOf(ms))
	}
	if !ms[0].Default || ms[0].ID != "" {
		t.Errorf("first must be the default: %+v", ms[0])
	}
	for _, m := range ms[1:] {
		if m.Default {
			t.Errorf("only the first is default: %+v", m)
		}
	}
	// mutating the result must not poison the cache
	ms[1].ID = "mutated"
	again, _ := a.Models(context.Background())
	if again[1].ID != "fable" || calls != 1 {
		t.Errorf("cache: id=%q calls=%d", again[1].ID, calls)
	}
}

func TestModelsFallback(t *testing.T) {
	const want = ",opus,sonnet,haiku,fable"
	// help text without model aliases
	a := New("", "deck", nil).(*adapter)
	a.initFn = noInit
	a.helpFn = func(context.Context) (string, error) { return "Usage: claude\n  --model <model>  Some model.\n", nil }
	ms, err := a.Models(context.Background())
	if err != nil || idsOf(ms) != want || !ms[0].Default {
		t.Errorf("unparsable help: %v %v", idsOf(ms), err)
	}
	// claude missing
	b := New("definitely-not-a-real-binary-xyz", "deck", nil).(*adapter)
	ms, err = b.Models(context.Background())
	if err != nil || idsOf(ms) != want {
		t.Errorf("missing binary: %v %v", idsOf(ms), err)
	}
}

func noInit(context.Context) ([]byte, error) { return nil, errors.New("no initialize") }

func TestModelsComeFromClaudeCodesOwnList(t *testing.T) {
	a := New("", "deck", nil).(*adapter)
	a.helpFn = func(context.Context) (string, error) { t.Fatal("help read"); return "", nil }
	a.initFn = func(context.Context) ([]byte, error) {
		return []byte(`{"type":"control_response","response":{"subtype":"success","request_id":"deck-models","response":{"models":[` +
			`{"value":"default","displayName":"Default (recommended)","description":"Opus 5.5 · Best for everyday, complex tasks","supportsEffort":true,"supportedEffortLevels":["low","medium","high","xhigh","max"]},` +
			`{"value":"sonnet","displayName":"Sonnet 5.5","description":"Efficient for routine tasks","supportsEffort":true,"supportedEffortLevels":["low","high"]},` +
			`{"value":"claude-haiku-4-5-20251001","displayName":"Haiku 4.5","description":"Fastest for quick answers"}]}}}` + "\n"), nil
	}
	ms, err := a.Models(context.Background())
	if err != nil || idsOf(ms) != ",sonnet,claude-haiku-4-5-20251001" {
		t.Fatalf("ids %q %v", idsOf(ms), err)
	}
	if !ms[0].Default || ms[0].Name != "Default (recommended)" || len(ms[0].Efforts) != 5 {
		t.Errorf("default: %+v", ms[0])
	}
	if e := ms[1].Efforts; len(e) != 2 || e[1].Level != "high" || e[1].Description == "" {
		t.Errorf("sonnet efforts: %+v", e)
	}
	if len(ms[2].Efforts) != 0 {
		t.Errorf("haiku 4.5 has no effort setting: %+v", ms[2])
	}
}

func TestEffortRestartsWithTheFlag(t *testing.T) {
	r := &run{a: &adapter{}, mode: agent.ModeAsk, effort: "high"}
	if v, _ := argAfter(r.args(""), "--effort"); v != "high" {
		t.Fatalf("--effort = %q", v)
	}
	r.effort = ""
	if contains(r.args(""), "--effort") {
		t.Fatal("no --effort for the model's default")
	}
}
