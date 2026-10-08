// Command deckdemo runs the Deck TUI against scripted fake agents, so the UI
// can be tried with no CLI installed and no tokens spent:
//
//	go run ./cmd/deckdemo
package main

import (
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
	adapters := map[string]agent.Adapter{
		"claude": fake.New(fake.DemoScript("claude"), 1),
		"codex":  fake.New(fake.DemoCodexScript(), 1),
	}
	mgr := session.NewManager(adapters, st, cfg)
	defer mgr.Close()

	// The demo never touches the real ~/.deck/config.json.
	app := tui.NewWithOptions(mgr, cfg, tui.Options{SaveConfig: func(config.Config) error { return nil }})
	_, err = tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}
