package session

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	pb "github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// An agent types a password: the page gets it, and nothing of the thread
// (its events, its log, its briefings) shows it.
func TestBrowserTypedTextStaysOutOfTheThread(t *testing.T) {
	args, _ := json.Marshal(map[string]any{"tab_id": "t1", "element_ref": "e3", "snapshot_version": "v1", "text": "hunter2-secret"})
	r := newRig(t, []fake.Script{turn(fake.Step{Exec: &fake.Exec{Op: "browser.type", Args: args}}, say("typed it"), finish())}, nil)
	tid := r.thread(t)
	id := r.add(t, tid, "claude")
	heard := make(chan string, 1)
	var once sync.Once
	r.m.SetBrowserAction(func(_ context.Context, _, _ string, c *pb.BrowserCommand) *pb.BrowserActionResponse {
		once.Do(func() { heard <- c.Text })
		return &pb.BrowserActionResponse{Result: &pb.OpResult{Text: "typed 14 characters into e3."}}
	})
	if err := r.m.Post(tid, "log in"); err != nil {
		t.Fatal(err)
	}
	select {
	case text := <-heard:
		if text != "hunter2-secret" {
			t.Fatalf("the page was sent %q", text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the browser heard nothing")
	}
	waitFor(t, "the turn", func() bool { return r.c.turnDones(id) == 1 })
	shows := func(tool *agent.Tool) bool {
		return tool != nil && (strings.Contains(string(tool.Input), "hunter2") || strings.Contains(tool.Title, "hunter2") || strings.Contains(tool.Output, "hunter2"))
	}
	seen := 0
	for _, m := range r.c.all() {
		if e, ok := m.(EventMsg); ok && e.Tool != nil {
			seen++
			if shows(e.Tool) {
				t.Fatalf("an event shows the typed text: %+v", e.Tool)
			}
		}
	}
	for _, m := range r.m.History(tid) {
		if e, ok := m.(EventMsg); ok && shows(e.Tool) {
			t.Fatalf("the log shows the typed text: %+v", e.Tool)
		}
	}
	if seen == 0 {
		t.Fatal("no tool event was seen at all")
	}
}
