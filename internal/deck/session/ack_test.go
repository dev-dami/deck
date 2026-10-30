package session

import (
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent/fake"
)

func TestIsAck(t *testing.T) {
	for _, s := range []string{
		"Thanks!", "thank you so much 🙏", "You're welcome!", "@claude thanks, got it.", "Sounds good, will do",
		"np", "Noted, thanks for the heads-up", "ok", "Great, thanks again!", "Cheers 👍",
	} {
		if !isAck(s) {
			t.Errorf("isAck(%q) = false, want true", s)
		}
	}
	for _, s := range []string{
		"", "👍", "done", "Done, tests pass", "yes", "no", "LGTM", "fixed it", "thanks, can you also run the tests?",
		"Thanks! Now add a test for the per-address limit", "ok go ahead and merge",
		"thanks thanks thanks thanks thanks thanks thanks thanks thanks thanks thanks thanks thanks",
	} {
		if isAck(s) {
			t.Errorf("isAck(%q) = true, want false", s)
		}
	}
}

// An agent that answers a hand-off and thanks the sender does not wake it,
// and neither does a "you're welcome" back: the thanks is logged, for the
// recipient's next briefing, and spends no hop.
func TestAnAcknowledgementStartsNoTurn(t *testing.T) {
	r := newRig(t, []fake.Script{
		turn(say("@codex check the limiter"), finish()),
		turn(say("unexpected"), finish()),
	}, []fake.Script{
		turn(say("It keys by address now.\n\n@claude thanks for the heads-up!"), finish()),
		turn(say("unexpected"), finish()),
	})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	if err := r.m.Post(tid, "@claude look at the limiter with codex"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "codex's answer", func() bool { return r.c.turnDones("codex") == 1 })
	time.Sleep(80 * time.Millisecond) // time for claude to wake, were it going to
	if p := r.claude.prompts("claude"); len(p) != 1 {
		t.Fatalf("claude woke for a thanks: %q", p)
	}
	if info, _ := r.m.Thread(tid); info.Hops != 1 {
		t.Fatalf("hops = %d, want 1 (the thanks spends none)", info.Hops)
	}
	thanks := 0
	for _, rl := range r.c.relays() {
		if strings.Contains(rl.Text, "heads-up") {
			thanks++
		}
	}
	if thanks != 1 {
		t.Fatalf("the thanks was not logged: %+v", r.c.relays())
	}
}
