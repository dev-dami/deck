package session

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/dev-dami/deck/internal/deck/config"
)

// The longest instructions an agent gets (a team paired, Highlights served,
// Deck's attribution on) fit the budget uncut at every orchestration level,
// and say how to use the browser. The project folder is a short one: a longer
// path costs its extra bytes, and the budget cuts the end.
func TestInstructionsFitTheBudgetAtEveryLevel(t *testing.T) {
	for _, level := range config.LevelNames {
		t.Run(level, func(t *testing.T) {
			cfg := testCfg()
			cfg.Orchestration, cfg.DeckAttribution = level, true
			dir, err := os.MkdirTemp("", "d")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(dir) })
			r := newRigWith(t, t.TempDir(), false, cfg, nil, nil)
			r.m.SetTeam(&postTeam{})
			r.m.SetHighlights(func(context.Context, string, string, string, string, json.RawMessage) (string, error) { return "", nil })
			tid, err := r.m.NewThread(dir)
			if err != nil {
				t.Fatal(err)
			}
			r.add(t, tid, "claude")
			sp, _ := r.claude.spec("claude")
			t.Logf("%s: %d of %d bytes", level, len(sp.Instructions), maxInstrChars)
			if len(sp.Instructions) > maxInstrChars {
				t.Fatalf("Instructions are %d bytes", len(sp.Instructions))
			}
			lacks(t, sp.Instructions, "[cut]")
			has(t, sp.Instructions, "browser.open", "Teammates' Decks", "highlight.add", "Write plan files in", "PRs end with")
		})
	}
}
