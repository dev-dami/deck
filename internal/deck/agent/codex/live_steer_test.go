package codex

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// TestLiveSteerImagesContext drives the installed codex through one image
// turn and one steered turn. It spends a little of the user's Codex usage, so
// it only runs with DECK_LIVE_STEER=1. It never reads any credential file.
func TestLiveSteerImagesContext(t *testing.T) {
	if os.Getenv("DECK_LIVE_STEER") != "1" {
		t.Skip("set DECK_LIVE_STEER=1 to run against the installed codex")
	}
	// Not t.TempDir: the killed codex may hold the directory for a moment on Windows.
	dir, err := os.MkdirTemp("", "deck-live-steer")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		time.Sleep(500 * time.Millisecond)
		_ = os.RemoveAll(dir)
	}()
	img := filepath.Join(dir, "red.png")
	pm := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			pm.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	f, err := os.Create(img)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, pm); err != nil {
		t.Fatal(err)
	}
	f.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	run, err := New("codex").Start(ctx, agent.Spec{ID: "live", Dir: dir, Mode: agent.ModePlan})
	if err != nil {
		t.Fatal(err)
	}
	defer run.Close()
	go func() {
		for a := range run.Approvals() {
			a.Respond(agent.Decision{Allow: false})
		}
	}()

	var mu sync.Mutex
	var evs []agent.Event
	go func() {
		for e := range run.Events() {
			mu.Lock()
			evs = append(evs, e)
			mu.Unlock()
		}
	}()
	count := func(k agent.Kind) int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, e := range evs {
			if e.Kind == k {
				n++
			}
		}
		return n
	}
	wait := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Minute)
		for time.Now().Before(deadline) {
			if cond() {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s", what)
	}

	// Turn 1: image.
	if err := run.(agent.ImageSender).SendWithImages("What single color is this image? Answer with one word.", []agent.Image{{Path: img, MediaType: "image/png"}}); err != nil {
		t.Fatal(err)
	}
	wait("turn 1 done", func() bool { return count(agent.KindTurnDone) >= 1 })

	// Turn 2: a long answer, steered once it is streaming.
	if err := run.Send("Count from 1 to 400, one number per line, no other text."); err != nil {
		t.Fatal(err)
	}
	wait("streaming", func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, e := range evs[1:] {
			if e.Kind == agent.KindAssistantText && e.Delta {
				return true
			}
		}
		return false
	})
	if err := run.(agent.Steerer).Steer("Stop counting now. Reply with only the word STEERED.", nil); err != nil {
		t.Fatalf("steer: %v", err)
	}
	wait("turn 2 done", func() bool { return count(agent.KindTurnDone) >= 2 })

	mu.Lock()
	defer mu.Unlock()
	var text, errs strings.Builder
	var ctxSeen, winSeen bool
	for _, e := range evs {
		switch e.Kind {
		case agent.KindAssistantText:
			if !e.Delta {
				text.WriteString(e.Text + "\n")
			}
		case agent.KindError:
			errs.WriteString(e.Text + "\n")
		case agent.KindUsage:
			t.Logf("usage: %+v", *e.Usage)
			ctxSeen = ctxSeen || e.Usage.ContextTokens > 0
			winSeen = winSeen || e.Usage.ContextWindow > 0
		}
	}
	t.Logf("assistant text:\n%s", text.String())
	if errs.Len() > 0 {
		t.Errorf("errors: %s", errs.String())
	}
	if !strings.Contains(strings.ToLower(text.String()), "red") {
		t.Errorf("image not seen: no 'red' in answers")
	}
	if !strings.Contains(text.String(), "STEERED") {
		t.Errorf("steer not honoured")
	}
	if !ctxSeen || !winSeen {
		t.Errorf("context fields missing: tokens=%v window=%v", ctxSeen, winSeen)
	}
}
