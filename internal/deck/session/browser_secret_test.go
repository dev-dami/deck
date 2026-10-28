package session

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/browser"
)

// An agent types a password: the page gets it, and nothing of the thread
// (its events, its log, its briefings) shows it.
func TestBrowserTypedTextStaysOutOfTheThread(t *testing.T) {
	args, _ := json.Marshal(map[string]any{"ref": "e3", "text": "hunter2-secret"})
	r := newRig(t, []fake.Script{turn(fake.Step{Exec: &fake.Exec{Op: "browser.type", Args: args}}, say("typed it"), finish())}, nil)
	tid := r.thread(t)
	id := r.add(t, tid, "claude")
	ch, detach := r.m.Browser().Attach("w1", true)
	defer detach()
	heard := make(chan browser.Request, 1)
	go func() {
		for req := range ch {
			heard <- req
			r.m.Browser().Complete("w1", browser.Result{ID: req.ID, Text: "typed 14 characters into e3."})
		}
	}()
	if err := r.m.Post(tid, "log in"); err != nil {
		t.Fatal(err)
	}
	select {
	case req := <-heard:
		if !strings.Contains(string(req.Args), "hunter2-secret") {
			t.Fatalf("the page was not sent the text: %s", req.Args)
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
