package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

func writeImage(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("not really an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func inputItems(t *testing.T, p map[string]any) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, it := range p["input"].([]any) {
		out = append(out, it.(map[string]any))
	}
	return out
}

func TestSendWithImagesInputShape(t *testing.T) {
	run, fe := startMode(t, "", "", "")
	is, ok := run.(agent.ImageSender)
	if !ok {
		t.Fatal("run is not an ImageSender")
	}
	a, b := writeImage(t, "a.png"), writeImage(t, "b.JPG")
	if err := is.SendWithImages("what is this", []agent.Image{{Path: a, MediaType: "image/png", Label: "login page error"}, {Path: b}}); err != nil {
		t.Fatal(err)
	}
	turns := fe.recorded(t, methodTurnStart)
	if len(turns) != 1 {
		t.Fatalf("turns = %v", turns)
	}
	items := inputItems(t, turns[0])
	if len(items) != 5 {
		t.Fatalf("input = %v", items)
	}
	// Each image follows its reference line.
	for i, ref := range []string{"[Image 1: login page error]", "[Image 2]"} {
		if items[2*i]["type"] != "text" || items[2*i]["text"] != ref {
			t.Errorf("reference %d = %v", i, items[2*i])
		}
	}
	for i, p := range []string{a, b} {
		img := items[2*i+1]
		if img["type"] != "localImage" || img["path"] != p {
			t.Errorf("item %d = %v", i, img)
		}
		if _, has := img["text"]; has {
			t.Errorf("image item carries text: %v", img)
		}
	}
	if items[4]["type"] != "text" || items[4]["text"] != "what is this" {
		t.Errorf("text item = %v", items[4])
	}
	if te, ok := items[4]["text_elements"].([]any); !ok || len(te) != 0 {
		t.Errorf("text_elements = %v", items[4]["text_elements"])
	}
}

func TestSendWithImagesPlanNoteAndEmptyPrompt(t *testing.T) {
	run, fe := startMode(t, agent.ModePlan, "", "")
	img := writeImage(t, "a.webp")
	if err := run.(agent.ImageSender).SendWithImages("", []agent.Image{{Path: img}}); err != nil {
		t.Fatal(err)
	}
	items := inputItems(t, fe.recorded(t, methodTurnStart)[0])
	if len(items) != 3 || items[0]["text"] != planNote || items[1]["text"] != "[Image 1]" || items[2]["type"] != "localImage" {
		t.Errorf("input = %v", items)
	}
}

func TestSendWithImagesValidation(t *testing.T) {
	run, fe := startMode(t, "", "", "")
	is := run.(agent.ImageSender)
	dir := t.TempDir()
	cases := map[string]agent.Image{
		"missing":  {Path: filepath.Join(dir, "gone.png")},
		"txt":      {Path: writeImage(t, "notes.txt")},
		"svg":      {Path: writeImage(t, "x.svg"), MediaType: "image/svg+xml"},
		"dir":      {Path: dir, MediaType: "image/png"},
		"nopath":   {},
		"badmedia": {Path: writeImage(t, "a.png"), MediaType: "application/pdf"},
	}
	for name, img := range cases {
		if err := is.SendWithImages("x", []agent.Image{img}); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if n := len(fe.recorded(t, methodTurnStart)); n != 0 {
		t.Errorf("invalid images reached the server: %d turn/starts", n)
	}
	// A valid media type wins over an unknown extension.
	if err := is.SendWithImages("x", []agent.Image{{Path: writeImage(t, "shot"), MediaType: "image/gif"}}); err != nil {
		t.Errorf("explicit media type rejected: %v", err)
	}
}

func TestContextFromTokenUsage(t *testing.T) {
	tr := newTranslator("c1", "")
	note := func(turnID string, lastIn, lastOut, lastReason, totIn, totOut int, window any) []agent.Event {
		params, _ := json.Marshal(map[string]any{
			"threadId": "thr", "turnId": turnID,
			"tokenUsage": map[string]any{
				"last": map[string]any{
					"inputTokens": lastIn, "outputTokens": lastOut, "reasoningOutputTokens": lastReason,
					"cachedInputTokens": 0, "totalTokens": lastIn + lastOut,
				},
				"total": map[string]any{
					"inputTokens": totIn, "outputTokens": totOut, "reasoningOutputTokens": 0,
					"cachedInputTokens": 0, "totalTokens": totIn + totOut,
				},
				"modelContextWindow": window,
			},
		})
		return tr.notification(noteTokenUsage, params, nil)
	}
	started, _ := json.Marshal(map[string]any{"threadId": "thr", "turn": map[string]any{"id": "t1", "status": "inProgress"}})
	tr.notification(noteTurnStarted, started, nil)

	// Two model calls in one turn: the context is the latest request, the
	// token counters stay per-turn cumulative.
	u := note("t1", 10000, 200, 50, 10000, 200, 272000)[0].Usage
	if u.ContextTokens != 10150 || u.ContextWindow != 272000 || u.InputTokens != 10000 || u.OutputTokens != 200 {
		t.Errorf("first usage = %+v", u)
	}
	u = note("t1", 10500, 100, 0, 20500, 300, 272000)[0].Usage
	if u.ContextTokens != 10600 || u.ContextWindow != 272000 || u.InputTokens != 20500 || u.OutputTokens != 300 {
		t.Errorf("second usage = %+v", u)
	}
	// Unknown window (null) leaves ContextWindow 0.
	u = note("t1", 10500, 100, 0, 20500, 300, nil)[0].Usage
	if u.ContextWindow != 0 || u.ContextTokens != 10600 {
		t.Errorf("null window usage = %+v", u)
	}
	// A new turn restarts the per-turn counters but not the context.
	done, _ := json.Marshal(map[string]any{"threadId": "thr", "turn": map[string]any{"id": "t1", "status": "completed"}})
	tr.notification(noteTurnCompleted, done, nil)
	tr.notification(noteTurnStarted, started, nil)
	u = note("t2", 11000, 80, 0, 31500, 380, 272000)[0].Usage
	if u.InputTokens != 11000 || u.OutputTokens != 80 || u.ContextTokens != 11080 {
		t.Errorf("next-turn usage = %+v", u)
	}
}

func TestSteerWhileBusyUsesTurnSteer(t *testing.T) {
	run, fe := startMode(t, "", "", "first")
	st, ok := run.(agent.Steerer)
	if !ok {
		t.Fatal("run is not a Steerer")
	}
	img := writeImage(t, "a.png")
	if err := st.Steer("actually do this", []agent.Image{{Path: img}}); err != nil {
		t.Fatal(err)
	}
	if n := len(fe.recorded(t, methodTurnStart)); n != 1 {
		t.Errorf("steer started a new turn: %d turn/starts", n)
	}
	if n := len(fe.recorded(t, "interrupt")); n != 0 {
		t.Errorf("steer interrupted: %d", n)
	}
	steers := fe.recorded(t, "steer")
	if len(steers) != 1 {
		t.Fatalf("steers = %v", steers)
	}
	s := steers[0]
	if s["threadId"] != "thr-mode" || s["expectedTurnId"] != "turn-mode" {
		t.Errorf("steer params = %v", s)
	}
	items := inputItems(t, s)
	if len(items) != 3 || items[0]["text"] != "[Image 1]" || items[1]["type"] != "localImage" || items[1]["path"] != img ||
		items[2]["type"] != "text" || items[2]["text"] != "actually do this" {
		t.Errorf("steer input = %v", items)
	}
}

func TestSteerWhenIdleStartsTurn(t *testing.T) {
	run, fe := startMode(t, "", "", "")
	if err := run.(agent.Steerer).Steer("hello", nil); err != nil {
		t.Fatal(err)
	}
	if n := len(fe.recorded(t, "steer")); n != 0 {
		t.Errorf("idle steer used turn/steer")
	}
	turns := fe.recorded(t, methodTurnStart)
	if len(turns) != 1 || inputTexts(t, turns[0])[0] != "hello" {
		t.Errorf("turns = %v", turns)
	}
}

func TestSteerAfterTurnCompletedStartsTurn(t *testing.T) {
	script := writeScript(t,
		send(`{"id":1,"method":"initialize","params":{}}`),
		recv(`{"id":1,"result":{"userAgent":"x"}}`),
		send(`{"id":2,"method":"thread/start","params":{}}`),
		recv(`{"id":2,"result":{"thread":{"id":"thr-mode"}}}`),
		send(`{"id":3,"method":"turn/start","params":{}}`),
		recv(`{"id":3,"result":{"turn":{"id":"turn-mode","status":"inProgress"}}}`),
		recv(`{"method":"turn/completed","params":{"threadId":"thr-mode","turn":{"id":"turn-mode","status":"completed"}}}`),
	)
	fe := newFakeEnv(t, script)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	run, err := New(os.Args[0]).Start(ctx, agent.Spec{ID: "m", Dir: t.TempDir(), Env: fe.env, FirstPrompt: "one"})
	if err != nil {
		t.Fatal(err)
	}
	defer run.Close()
	evs, mu := drain(run)
	waitFor(t, "turn done", func() bool { return hasKind(evs, mu, agent.KindTurnDone) })

	if err := run.(agent.Steerer).Steer("two", nil); err != nil {
		t.Fatal(err)
	}
	if n := len(fe.recorded(t, "steer")); n != 0 {
		t.Errorf("steer after completion used turn/steer")
	}
	if n := len(fe.recorded(t, methodTurnStart)); n != 2 {
		t.Errorf("turn/starts = %d, want 2", n)
	}
}

func TestSteerRefusedWhileTurnStillActiveReturnsError(t *testing.T) {
	fe := newFakeEnv(t, modeScript(t))
	fe.env["DECK_FAKE_STEER_ERR"] = "1"
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	run, err := New(os.Args[0]).Start(ctx, agent.Spec{ID: "m", Dir: t.TempDir(), Env: fe.env, FirstPrompt: "one"})
	if err != nil {
		t.Fatal(err)
	}
	defer run.Close()
	err = run.(agent.Steerer).Steer("two", nil)
	if err == nil || !strings.Contains(err.Error(), "no active turn") {
		t.Fatalf("err = %v", err)
	}
	if n := len(fe.recorded(t, methodTurnStart)); n != 1 {
		t.Errorf("refused steer started a turn: %d", n)
	}
}
