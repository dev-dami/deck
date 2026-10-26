// Package main is the entry point for the Deck CLI.
package main

import (
	"fmt"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"

	"github.com/dev-dami/deck/internal/cmd"
	"github.com/dev-dami/deck/internal/deck/engine"
	_ "github.com/dev-dami/deck/internal/dns"
)

func main() {
	if os.Getenv("DECK_PROFILE") != "" {
		go func() {
			slog.Info("Serving pprof at localhost:6060")
			if httpErr := http.ListenAndServe("localhost:6060", nil); httpErr != nil {
				slog.Error("Failed to pprof listen", "error", httpErr)
			}
		}()
	}

	// Claude Code starts this binary as its approval MCP server.
	if len(os.Args) > 1 && os.Args[1] == "mcp-approve" {
		if err := engine.MCPApprove(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "deck:", err)
			os.Exit(1)
		}
		return
	}
	stopProfiles := startProfiles(os.Getenv("DECK_PPROF"))
	err := cmd.Execute()
	stopProfiles()
	if err != nil {
		os.Exit(1)
	}
}
