package session

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/deck/agent/fake"
)

// The person's messages go to the lead, and "@codex" in them is text: it adds
// no agent and reaches none.
func TestThePersonTalksToTheLeadAndAtIsText(t *testing.T) {
	r := newRig(t, []fake.Script{turn(say("on it"), finish())}, nil)
	tid := r.thread(t)
	if err := r.m.Post(tid, "@codex can you fix the limiter? mail user@example.com"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the lead's turn", func() bool { return r.c.turnDones("claude") == 1 })
	info, _ := r.m.Thread(tid)
	if len(info.Members) != 1 || info.Lead != "claude" {
		t.Fatalf("members %+v, lead %q: want claude alone, as lead", info.Members, info.Lead)
	}
	has(t, r.claude.prompts("claude")[0], "@codex can you fix the limiter?")
}

// Switching the lead to another kind keeps the thread: the new agent's first
// prompt is briefed on what was said, and the old one hears no more.
func TestSwitchingTheLeadKeepsTheHistory(t *testing.T) {
	r := newRig(t, []fake.Script{turn(say("the limiter keys by nothing"), finish())}, []fake.Script{turn(say("fixed"), finish())})
	tid := r.thread(t)
	if err := r.m.Post(tid, "why does pairing lock out?"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "claude", func() bool { return r.c.turnDones("claude") == 1 })
	id, err := r.m.SetLead(tid, "codex", "", "")
	if err != nil || id != "codex" {
		t.Fatalf("SetLead = %q, %v", id, err)
	}
	if err := r.m.Post(tid, "fix it"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "codex", func() bool { return r.c.turnDones("codex") == 1 })
	p := r.codex.prompts("codex")[0]
	has(t, p, "why does pairing lock out?", "[claude] the limiter keys by nothing", "fix it")
	if n := len(r.claude.prompts("claude")); n != 1 {
		t.Fatalf("claude got %d prompts after the switch", n)
	}
	if r.c.count(func(m tea.Msg) bool { n, ok := m.(NoticeMsg); return ok && strings.Contains(n.Text, "codex took over from claude") }) != 1 {
		t.Fatal("no notice of the switch")
	}
	// Back to claude: the idle member is reused, not a second claude.
	if id, _ := r.m.SetLead(tid, "claude", "", ""); id != "claude" {
		t.Fatalf("SetLead back = %q", id)
	}
	if info, _ := r.m.Thread(tid); len(info.Members) != 2 || info.Lead != "claude" {
		t.Fatalf("members %+v lead %q", info.Members, info.Lead)
	}
}

func TestSetLeadRefusesAnUnknownKind(t *testing.T) {
	r := newRig(t, nil, nil)
	tid := r.thread(t)
	if _, err := r.m.SetLead(tid, "gemini", "", ""); err == nil || !strings.Contains(err.Error(), "no agent kind") {
		t.Fatalf("err = %v", err)
	}
}
