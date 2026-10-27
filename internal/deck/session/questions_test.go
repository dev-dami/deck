package session

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// A question is for the person in every mode: yolo neither answers it when
// it is raised nor when the thread switches to yolo, and plan lets it
// through.
func TestQuestionsWaitForThePersonInEveryMode(t *testing.T) {
	for _, mode := range []agent.Mode{agent.ModeYolo, agent.ModePlan, agent.ModeAsk} {
		t.Run(string(mode), func(t *testing.T) {
			m, ad, c := stubManager(t, testCfg())
			tid, _ := m.NewThread(t.TempDir())
			if err := m.SetMode(tid, mode); err != nil {
				t.Fatal(err)
			}
			id, _ := m.AddMember(tid, "claude", "")
			ch := ad.run(id).ask("q1", agent.ApproveQuestion, "AskUserQuestion", "Which one?")
			waitFor(t, "the question surfaces", func() bool { return len(c.approvals()) == 1 })
			select {
			case d := <-ch:
				t.Fatalf("answered without the person: %+v", d)
			case <-time.After(50 * time.Millisecond):
			}
			if len(c.autoApproved()) != 0 || len(c.autoDenied()) != 0 {
				t.Fatal("the mode answered a question")
			}
			if ai, _ := m.Agent(id); ai.Pending != 1 {
				t.Fatalf("pending = %d", ai.Pending)
			}
			want := agent.Decision{Allow: true, Answers: map[string]agent.Answer{"Which one?": {Selected: []string{"A"}}}}
			c.approvals()[0].Respond(want)
			if d := decision(t, ch); !d.Allow || d.Answers["Which one?"].Text() != "A" {
				t.Fatalf("answer lost: %+v", d)
			}
		})
	}
}

func TestSwitchingToYoloKeepsQuestions(t *testing.T) {
	m, ad, c := stubManager(t, testCfg())
	tid, _ := m.NewThread(t.TempDir())
	id, _ := m.AddMember(tid, "claude", "")
	run := ad.run(id)
	q := run.ask("q1", agent.ApproveQuestion, "AskUserQuestion", "Which one?")
	cmd := run.ask("p1", agent.ApproveCommand, "Bash", "ls")
	waitFor(t, "two pending", func() bool { return len(c.approvals()) == 2 })
	if err := m.SetMode(tid, agent.ModeYolo); err != nil {
		t.Fatal(err)
	}
	if d := decision(t, cmd); !d.Allow {
		t.Fatalf("command not auto-approved: %+v", d)
	}
	select {
	case d := <-q:
		t.Fatalf("question answered by the switch: %+v", d)
	case <-time.After(50 * time.Millisecond):
	}
	if ai, _ := m.Agent(id); ai.Pending != 1 {
		t.Fatalf("pending = %d, want the question", ai.Pending)
	}
}

// Stopping the agent withdraws its question: it is declined and every UI
// is told it is gone.
func TestInterruptDeclinesAQuestion(t *testing.T) {
	m, ad, c := stubManager(t, testCfg())
	tid, _ := m.NewThread(t.TempDir())
	id, _ := m.AddMember(tid, "claude", "")
	q := ad.run(id).ask("q1", agent.ApproveQuestion, "AskUserQuestion", "Which one?")
	waitFor(t, "the question", func() bool { return len(c.approvals()) == 1 })
	if err := m.Interrupt(id); err != nil {
		t.Fatal(err)
	}
	if d := decision(t, q); d.Allow {
		t.Fatalf("interrupt answered the question: %+v", d)
	}
	waitFor(t, "gone", func() bool {
		return c.count(func(m tea.Msg) bool { g, ok := m.(ApprovalGoneMsg); return ok && g.ID == "q1" }) == 1
	})
}
