package claude

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/agent"
)

// TestLiveImage sends a generated solid-red PNG to the real claude and checks
// that the image is understood and that context usage is reported.
// DECK_LIVE=image.
func TestLiveImage(t *testing.T) {
	if os.Getenv("DECK_LIVE") != "image" {
		t.Skip("set DECK_LIVE=image to send an image to real Claude")
	}
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	path := filepath.Join(dir, "square.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()

	a := New("", "", nil).(*adapter)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	r, err := a.Start(ctx, agent.Spec{ID: "claude1", Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	go func() {
		for req := range r.Approvals() {
			req.Respond(agent.Decision{Allow: false})
		}
	}()
	if err := r.(agent.ImageSender).SendWithImages("What colour is this square? Answer with one word.", []agent.Image{{Path: path}}); err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	var usages []*agent.Usage
	timeout := time.After(3 * time.Minute)
loop:
	for {
		select {
		case ev, ok := <-r.Events():
			if !ok {
				break loop
			}
			switch ev.Kind {
			case agent.KindAssistantText:
				if !ev.Delta {
					text.WriteString(ev.Text)
				}
			case agent.KindUsage:
				usages = append(usages, ev.Usage)
				t.Logf("usage: %+v", *ev.Usage)
			case agent.KindError:
				t.Fatalf("error event: %s", ev.Text)
			}
			if ev.Kind == agent.KindTurnDone || ev.Kind == agent.KindExited {
				break loop
			}
		case <-timeout:
			t.Fatal("timeout")
		}
	}
	t.Logf("reply: %q", text.String())
	if !strings.Contains(strings.ToLower(text.String()), "red") {
		t.Errorf("reply %q does not say red", text.String())
	}
	if len(usages) == 0 {
		t.Fatal("no usage events")
	}
	last := usages[len(usages)-1]
	if last.ContextTokens <= 0 || last.ContextWindow <= 0 || last.ContextTokens > last.ContextWindow {
		t.Errorf("final usage context fields: %+v", *last)
	}
}
