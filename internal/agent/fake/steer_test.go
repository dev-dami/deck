package fake

import (
	"errors"
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/agent"
)

func TestSendWithImagesRecordsThemAndPlaysATurn(t *testing.T) {
	one := Script{{Event: agent.Event{Kind: agent.KindAssistantText, Text: "seen"}}, {Event: agent.Event{Kind: agent.KindTurnDone}}}
	r := startRun(t, NewTurns(0, nil, one), "a")
	is, ok := r.(agent.ImageSender)
	if !ok {
		t.Fatal("fake runs must implement agent.ImageSender")
	}
	imgs := []agent.Image{{Path: "/tmp/x.png", MediaType: "image/png"}}
	if err := is.SendWithImages("what is this", imgs); err != nil {
		t.Fatal(err)
	}
	got := untilTurnDone(t, r)
	if k := kinds(got); !equalKinds(k, []agent.Kind{agent.KindUserText, agent.KindAssistantText, agent.KindTurnDone}) || got[0].Text != "what is this" {
		t.Fatalf("events = %v", k)
	}
	calls := r.(Inspector).ImageCalls()
	if len(calls) != 1 || calls[0].Prompt != "what is this" || len(calls[0].Images) != 1 || calls[0].Images[0] != imgs[0] {
		t.Fatalf("ImageCalls = %+v", calls)
	}
	imgs[0].Path = "changed"
	if r.(Inspector).ImageCalls()[0].Images[0].Path != "/tmp/x.png" {
		t.Error("the recorded images alias the caller's slice")
	}
	_ = r.Close()
	if err := is.SendWithImages("late", nil); !errors.Is(err, ErrClosed) {
		t.Errorf("SendWithImages after Close = %v", err)
	}
}

func TestSteerReplacesTheRunningTurnWithoutATurnDone(t *testing.T) {
	blocked := Script{
		{Event: agent.Event{Kind: agent.KindAssistantText, Text: "working"}},
		{Approval: &agent.ApprovalRequest{ID: "ap", Kind: agent.ApproveCommand, ToolName: "Bash", Summary: "make"}},
		{Event: agent.Event{Kind: agent.KindAssistantText, Text: "never"}},
		{Event: agent.Event{Kind: agent.KindTurnDone}},
	}
	redirected := Script{{Event: agent.Event{Kind: agent.KindAssistantText, Text: "redirected"}}, {Event: agent.Event{Kind: agent.KindTurnDone}}}
	r := startRun(t, NewTurns(0, nil, blocked, redirected), "a")
	st, ok := r.(agent.Steerer)
	if !ok {
		t.Fatal("fake runs must implement agent.Steerer")
	}
	if err := r.Send("go"); err != nil {
		t.Fatal(err)
	}
	nextApproval(t, r)
	if err := st.Steer("do this instead", nil); err != nil {
		t.Fatal(err)
	}
	got := untilTurnDone(t, r)
	var texts []string
	for _, ev := range got {
		if ev.Kind == agent.KindAssistantText || ev.Kind == agent.KindUserText {
			texts = append(texts, string(ev.Kind)+":"+ev.Text)
		}
	}
	want := []string{"user_text:go", "assistant_text:working", "user_text:do this instead", "assistant_text:redirected"}
	if strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Fatalf("events = %q, want %q", texts, want)
	}
	n := 0
	for _, ev := range got {
		if ev.Kind == agent.KindTurnDone {
			n++
		}
	}
	if n != 1 {
		t.Errorf("turn_done count = %d: the steered turn must not end before the redirect", n)
	}
	calls := r.(Inspector).SteerCalls()
	if len(calls) != 1 || calls[0].Prompt != "do this instead" {
		t.Fatalf("SteerCalls = %+v", calls)
	}
	// steering an idle run just starts a turn
	if err := st.Steer("again", nil); err != nil {
		t.Fatal(err)
	}
	untilTurnDone(t, r)
}

func TestDemoUsageEventsCarryAGrowingContext(t *testing.T) {
	for _, tc := range []struct {
		name   string
		window int
		cost   float64
	}{{"claude", 200000, 0.12}, {"codex", 272000, 0.04}} {
		var used []int
		var final *agent.Usage
		for _, st := range DemoScript(tc.name) {
			u := st.Event.Usage
			if st.Event.Kind != agent.KindUsage || u == nil {
				continue
			}
			if u.ContextWindow != tc.window || u.ContextTokens <= 0 || u.ContextTokens >= tc.window {
				t.Errorf("%s: usage context = %d/%d", tc.name, u.ContextTokens, u.ContextWindow)
			}
			used = append(used, u.ContextTokens)
			if u.CostUSD > 0 {
				final = u
			} else if u.InputTokens != 0 || u.OutputTokens != 0 {
				t.Errorf("%s: a mid-turn usage event must carry only the context fields: %+v", tc.name, u)
			}
		}
		if len(used) < 3 {
			t.Errorf("%s: %d usage events, want the meter to move several times", tc.name, len(used))
		}
		for i := 1; i < len(used); i++ {
			if used[i] <= used[i-1] {
				t.Errorf("%s: context does not grow: %v", tc.name, used)
			}
		}
		if final == nil || final.CostUSD != tc.cost {
			t.Errorf("%s: final usage = %+v", tc.name, final)
		}
	}
}

func TestDefaultTurnsReportAGrowingContext(t *testing.T) {
	for _, tc := range []struct {
		id     string
		window int
	}{{"claude", 200000}, {"claude2", 200000}, {"codex", 272000}} {
		r := startRun(t, NewTurns(0, nil), tc.id)
		last := 0
		for i := 0; i < 3; i++ {
			if err := r.Send("go"); err != nil {
				t.Fatal(err)
			}
			var u *agent.Usage
			for _, ev := range untilTurnDone(t, r) {
				if ev.Kind == agent.KindUsage {
					u = ev.Usage
				}
			}
			if u == nil || u.ContextWindow != tc.window || u.ContextTokens <= last {
				t.Fatalf("%s turn %d: usage = %+v (previous %d)", tc.id, i, u, last)
			}
			last = u.ContextTokens
		}
	}
}
