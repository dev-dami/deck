package codex

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/agent"
)

// modeScript is a minimal session: handshake plus a turn/start response that
// can be answered any number of times.
func modeScript(t *testing.T) string {
	return writeScript(t,
		send(`{"id":1,"method":"initialize","params":{}}`),
		recv(`{"id":1,"result":{"userAgent":"x"}}`),
		send(`{"id":2,"method":"thread/start","params":{}}`),
		recv(`{"id":2,"result":{"thread":{"id":"thr-mode"}}}`),
		send(`{"id":3,"method":"turn/start","params":{}}`),
		recv(`{"id":3,"result":{"turn":{"id":"turn-mode","status":"inProgress"}}}`),
	)
}

// recorded returns the JSON params the fake recorded for method, in order.
func (f fakeEnv) recorded(t *testing.T, method string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(f.read(t), "\n") {
		if rest, ok := strings.CutPrefix(line, method+"="); ok {
			var m map[string]any
			if err := json.Unmarshal([]byte(rest), &m); err != nil {
				t.Fatalf("bad %s params %q: %v", method, rest, err)
			}
			out = append(out, m)
		}
	}
	return out
}

func inputTexts(t *testing.T, p map[string]any) []string {
	t.Helper()
	var texts []string
	for _, it := range p["input"].([]any) {
		texts = append(texts, it.(map[string]any)["text"].(string))
	}
	return texts
}

func startMode(t *testing.T, mode agent.Mode, resume string, prompt string) (agent.Run, fakeEnv) {
	t.Helper()
	fe := newFakeEnv(t, modeScript(t))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	run, err := New(os.Args[0]).Start(ctx, agent.Spec{
		ID: "m", Dir: t.TempDir(), Env: fe.env, Mode: mode, ResumeID: resume, FirstPrompt: prompt,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { run.Close() })
	return run, fe
}

func TestModeParamsOnThreadStartAndResume(t *testing.T) {
	cases := []struct {
		mode              agent.Mode
		approval, sandbox string
	}{
		{"", "on-request", "workspace-write"},
		{agent.ModeAsk, "on-request", "workspace-write"},
		{agent.ModePlan, "on-request", "read-only"},
		{agent.ModeYolo, "never", "danger-full-access"},
	}
	for _, c := range cases {
		for _, resume := range []string{"", "old-thread"} {
			method := methodThreadStart
			if resume != "" {
				method = methodThreadResume
			}
			_, fe := startMode(t, c.mode, resume, "")
			got := fe.recorded(t, method)
			if len(got) != 1 {
				t.Fatalf("mode %q %s: %d requests", c.mode, method, len(got))
			}
			if got[0]["approvalPolicy"] != c.approval || got[0]["sandbox"] != c.sandbox {
				t.Errorf("mode %q %s: params = %v, want %s/%s", c.mode, method, got[0], c.approval, c.sandbox)
			}
		}
	}
}

func TestModeParamsOnFirstTurn(t *testing.T) {
	// ask / yolo: no per-turn overrides and no note (thread settings already hold).
	for _, m := range []agent.Mode{agent.ModeAsk, agent.ModeYolo} {
		_, fe := startMode(t, m, "", "hello")
		turns := fe.recorded(t, methodTurnStart)
		if len(turns) != 1 {
			t.Fatalf("turns = %v", turns)
		}
		for _, k := range []string{"approvalPolicy", "sandboxPolicy", "model"} {
			if _, ok := turns[0][k]; ok {
				t.Errorf("mode %s: unexpected %s on first turn: %v", m, k, turns[0])
			}
		}
		if got := inputTexts(t, turns[0]); len(got) != 1 || got[0] != "hello" {
			t.Errorf("mode %s: input = %q", m, got)
		}
	}
	// plan: the plan note rides in front of the prompt as its own input item.
	_, fe := startMode(t, agent.ModePlan, "", "hello")
	turns := fe.recorded(t, methodTurnStart)
	got := inputTexts(t, turns[0])
	if len(got) != 2 || got[0] != planNote || got[1] != "hello" {
		t.Errorf("plan input = %q", got)
	}
	if !strings.Contains(planNote, "Plan only: investigate and propose; do not modify files or run commands that change state.") {
		t.Error("plan note text changed")
	}
}

func TestSetModeAndSetModelApplyOnNextTurnOnly(t *testing.T) {
	run, fe := startMode(t, "", "", "")
	ms, ok := run.(agent.ModeSetter)
	if !ok {
		t.Fatal("run is not a ModeSetter")
	}
	mdl, ok := run.(agent.ModelSetter)
	if !ok {
		t.Fatal("run is not a ModelSetter")
	}

	if err := run.Send("one"); err != nil {
		t.Fatal(err)
	}
	if err := ms.SetMode(agent.ModePlan); err != nil {
		t.Fatal(err)
	}
	if err := mdl.SetModel("gpt-next"); err != nil {
		t.Fatal(err)
	}
	if err := run.(agent.EffortSetter).SetEffort("high"); err != nil {
		t.Fatal(err)
	}
	// Nothing is sent by SetMode / SetModel themselves.
	if n := len(fe.recorded(t, methodTurnStart)); n != 1 {
		t.Fatalf("SetMode/SetModel sent a request: %d turn/starts", n)
	}
	if err := run.Send("two"); err != nil {
		t.Fatal(err)
	}
	if err := ms.SetMode(agent.ModeYolo); err != nil {
		t.Fatal(err)
	}
	if err := run.Send("three"); err != nil {
		t.Fatal(err)
	}
	if err := run.Send("four"); err != nil {
		t.Fatal(err)
	}

	turns := fe.recorded(t, methodTurnStart)
	if len(turns) != 4 {
		t.Fatalf("turns = %d", len(turns))
	}
	// 1: untouched.
	for _, k := range []string{"approvalPolicy", "sandboxPolicy", "model", "effort"} {
		if _, ok := turns[0][k]; ok {
			t.Errorf("turn 1 carries %s", k)
		}
	}
	// 2: plan + model override, plan note.
	if turns[1]["approvalPolicy"] != "on-request" || turns[1]["model"] != "gpt-next" || turns[1]["effort"] != "high" ||
		turns[1]["sandboxPolicy"].(map[string]any)["type"] != "readOnly" {
		t.Errorf("turn 2 = %v", turns[1])
	}
	if got := inputTexts(t, turns[1]); len(got) != 2 || got[0] != planNote {
		t.Errorf("turn 2 input = %q", got)
	}
	// 3: yolo override, model not repeated, no plan note.
	if turns[2]["approvalPolicy"] != "never" || turns[2]["sandboxPolicy"].(map[string]any)["type"] != "dangerFullAccess" {
		t.Errorf("turn 3 = %v", turns[2])
	}
	if _, ok := turns[2]["model"]; ok {
		t.Errorf("turn 3 repeats model: %v", turns[2])
	}
	if got := inputTexts(t, turns[2]); len(got) != 1 {
		t.Errorf("turn 3 input = %q", got)
	}
	// 4: overrides were consumed.
	for _, k := range []string{"approvalPolicy", "sandboxPolicy", "model", "effort"} {
		if _, ok := turns[3][k]; ok {
			t.Errorf("turn 4 carries %s", k)
		}
	}

	if err := ms.SetMode("bogus"); err == nil {
		t.Error("SetMode(bogus) = nil error")
	}
	if err := mdl.SetModel(""); err == nil {
		t.Error("SetModel(\"\") = nil error")
	}
	// ask maps to workspaceWrite.
	if err := ms.SetMode(agent.ModeAsk); err != nil {
		t.Fatal(err)
	}
	if err := run.Send("five"); err != nil {
		t.Fatal(err)
	}
	turns = fe.recorded(t, methodTurnStart)
	last := turns[len(turns)-1]
	if last["approvalPolicy"] != "on-request" || last["sandboxPolicy"].(map[string]any)["type"] != "workspaceWrite" {
		t.Errorf("ask turn = %v", last)
	}
}

func TestPlanNoteHiddenFromUserEcho(t *testing.T) {
	tr := newTranslator("c1", "")
	content, _ := json.Marshal([]map[string]any{
		{"type": "text", "text": planNote}, {"type": "text", "text": "fix the bug"},
	})
	item := threadItem{Type: "userMessage", ID: "u1", Content: content}
	evs := tr.itemCompleted(item, nil)
	if len(evs) != 1 || evs[0].Kind != agent.KindUserText || evs[0].Text != "fix the bug" {
		t.Fatalf("events = %+v", evs)
	}
}
