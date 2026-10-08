// Command deckdemo runs the Deck TUI against scripted fake agents, so the UI
// can be tried with no CLI installed and no tokens spent:
//
//	go run ./cmd/deckdemo
//
// It opens on the logo with an empty thread. Type anything to start the duo, or
// try "@claude plan the store refactor, @codex review it" to watch a scripted
// Claude and Codex plan, review and answer each other in one thread.
//
// The permission modes work here too: shift+tab (or a click on the chip at the
// right of the line above the composer) cycles plan, ask and yolo, and the model
// chips open a picker over the fakes' model lists.
package main

import (
	"context"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/agent/fake"
	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/store"
	"github.com/dev-dami/deck/internal/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "deckdemo:", err)
		os.Exit(1)
	}
}

func run() error {
	dir, err := os.MkdirTemp("", "deckdemo-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	st, err := store.New(dir)
	if err != nil {
		return err
	}

	cfg := config.Defaults()
	if os.Getenv(config.EnvReducedMotion) == "1" {
		cfg.Motion = "off"
	}
	adapters, hub := fake.DemoDuo(1)
	var mgr *session.Manager
	// However the demo is kicked off, Claude's script asks Codex for a review,
	// so make sure Codex is in the thread by then.
	adapters["claude"] = withCodex{adapters["claude"], func() *session.Manager { return mgr }}
	mgr = session.NewManager(adapters, st, cfg, session.Options{Hub: hub, MCPFor: fake.MCPFor})
	defer mgr.Close()

	// Start on an empty thread so the logo shows.
	wd, _ := os.Getwd()
	if _, err := mgr.NewThread(wd); err != nil {
		return err
	}

	// The demo never touches the real ~/.deck/config.json.
	app := tui.NewWithOptions(mgr, cfg, tui.Options{SaveConfig: func(config.Config) error { return nil }})
	_, err = tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseAllMotion()).Run()
	return err
}

// withCodex wraps the scripted Claude: when it starts in a thread that has no
// Codex, Codex joins, so the scripted review request has someone to go to.
type withCodex struct {
	agent.Adapter
	mgr func() *session.Manager
}

func (w withCodex) Start(ctx context.Context, spec agent.Spec) (agent.Run, error) {
	run, err := w.Adapter.Start(ctx, spec)
	if err == nil {
		go w.join(spec.ID) // not inline: the manager is still starting this agent
	}
	return run, err
}

func (w withCodex) join(claudeID string) {
	mgr := w.mgr()
	a, ok := mgr.Agent(claudeID)
	if !ok {
		return
	}
	if th, ok := mgr.Thread(a.ThreadID); ok {
		for _, m := range th.Members {
			if m.Kind == "codex" {
				return
			}
		}
	}
	_, _ = mgr.AddMember(a.ThreadID, "codex", "")
}

// Models passes the wrapped Claude's model list through, so the model picker
// works in the demo (Adapter alone would hide it).
func (w withCodex) Models(ctx context.Context) ([]agent.ModelInfo, error) {
	if ml, ok := w.Adapter.(agent.ModelLister); ok {
		return ml.Models(ctx)
	}
	return nil, nil
}
