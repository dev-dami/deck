package session

import (
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
)

func TestBrowserOriginApprovalWaitsAcrossModes(t *testing.T) {
	for _, mode := range []agent.Mode{agent.ModeAsk, agent.ModePlan, agent.ModeYolo} {
		t.Run(string(mode), func(t *testing.T) {
			m, ad, c := stubManager(t, testCfg())
			tid, err := m.NewThread(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := m.SetMode(tid, mode); err != nil {
				t.Fatal(err)
			}
			id, err := m.AddMember(tid, "claude", "")
			if err != nil {
				t.Fatal(err)
			}
			answer := ad.run(id).ask("origin1", agent.ApproveBrowserOrigin, "browser.open", "Visit https://example.com")
			waitFor(t, "origin approval", func() bool { return len(c.approvals()) == 1 })
			if err := m.SetMode(tid, agent.ModeYolo); err != nil {
				t.Fatal(err)
			}
			select {
			case d := <-answer:
				t.Fatalf("mode answered origin approval: %+v", d)
			default:
			}
			if ai, _ := m.Agent(id); ai.Pending != 1 {
				t.Fatal("origin approval was drained")
			}
			c.approvals()[0].Respond(agent.Decision{Allow: false})
			if decision(t, answer).Allow {
				t.Fatal("user denial was lost")
			}
		})
	}
}
