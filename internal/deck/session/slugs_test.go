package session

import (
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
)

func TestSlugDropsTheCLIsAnnotations(t *testing.T) {
	for in, want := range map[string]string{
		"claude-opus-5-5[1m]": "claude-opus-5-5",
		" gpt-5.5-codex ":     "gpt-5.5-codex",
		"":                    "",
	} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

// The model a CLI says it runs names the member, and what its pick ran as
// names that pick from then on: in the model list, and for a member that has
// not started yet.
func TestTheModelIsNamedByTheSlugTheCLIReports(t *testing.T) {
	started := fake.Step{Event: agent.Event{Kind: agent.KindSessionStarted, Text: "claude-opus-5-5[1m]"}}
	r := newRig(t, []fake.Script{turn(started, say("on it"), finish())}, nil)
	tid := r.thread(t)
	if a, _ := r.m.Agent("claude"); a.RunningModel != "" {
		t.Fatalf("RunningModel %q before any run said", a.RunningModel)
	}
	if err := r.m.Post(tid, "hi"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the turn", func() bool { return r.c.turnDones("claude") == 1 })
	a, _ := r.m.Agent("claude")
	if a.RunningModel != "claude-opus-5-5" {
		t.Fatalf("RunningModel = %q", a.RunningModel)
	}
	if got := r.m.ModelSlug("claude", a.Model); got != "claude-opus-5-5" {
		t.Fatalf("ModelSlug(claude, %q) = %q", a.Model, got)
	}
	// A second member with the same pick is named before it runs.
	tid2 := r.thread(t)
	id, err := r.m.SetLead(tid2, "claude", a.Model, "")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := r.m.Agent(id); b.RunningModel != "claude-opus-5-5" {
		t.Fatalf("a new member of the same pick: RunningModel = %q", b.RunningModel)
	}
}
