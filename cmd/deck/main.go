// Command deck is one calm terminal deck for all your coding agents.
//
// Deck launches your own installed claude and codex CLIs under your own
// login, over their documented structured protocols, and draws its own
// interface from their events. It never reads their credentials and never
// calls a model API itself.
//
//	deck            open the deck
//	deck doctor     check the CLIs Deck can drive
//	deck ls         list saved threads
//	deck debug input  show the key and mouse events your terminal sends
//	deck version    print the version
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/agent/claude"
	"github.com/dev-dami/deck/internal/agent/codex"
	"github.com/dev-dami/deck/internal/approve"
	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/doctor"
	"github.com/dev-dami/deck/internal/inputdebug"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/store"
	"github.com/dev-dami/deck/internal/tui"
)

// Set at release time: -ldflags "-X main.version=... -X main.commit=... -X main.date=..."
var (
	version = "dev"
	commit  = ""
	date    = ""
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "deck:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}
	switch cmd {
	case "":
		return runDeck()
	case "doctor":
		return doctor.Run(context.Background(), os.Stdout)
	case "ls":
		return listThreads(os.Stdout)
	case "version", "--version", "-v":
		fmt.Println("deck", version)
		if commit != "" {
			fmt.Println("commit", commit)
		}
		if date != "" {
			fmt.Println("built", date)
		}
		return nil
	case "debug":
		if len(args) == 2 && args[1] == "input" {
			return inputdebug.Run()
		}
		usage(os.Stderr)
		return fmt.Errorf("usage: deck debug input")
	case "mcp-approve":
		return mcpApprove(args[1:])
	case "help", "--help", "-h":
		usage(os.Stdout)
		return nil
	default:
		usage(os.Stderr)
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `deck — one deck for all your agents

  deck            open the deck
  deck doctor     check the CLIs Deck can drive
  deck ls         list saved threads
  deck debug input  show the key and mouse events your terminal sends (q quits)
  deck version    print the version
`)
}

func runDeck() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	st, err := store.New("")
	if err != nil {
		return fmt.Errorf("threads: %w", err)
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	broker, err := approve.NewBroker()
	if err != nil {
		return fmt.Errorf("approval broker: %w", err)
	}
	defer broker.Close()

	adapters := map[string]agent.Adapter{}
	if found(cfg.ClaudeBinary, "claude") {
		adapters["claude"] = claude.New(cfg.ClaudeBinary, self, broker)
	}
	if found(cfg.CodexBinary, "codex") {
		adapters["codex"] = codex.New(cfg.CodexBinary)
	}
	if len(adapters) == 0 {
		return fmt.Errorf("neither claude nor codex was found on PATH — run `deck doctor`")
	}
	if adapters[cfg.DefaultAgent] == nil {
		for k := range adapters {
			cfg.DefaultAgent = k
			break
		}
	}

	mgr := session.NewManager(adapters, st, cfg, session.Options{
		Hub:    broker,
		MCPFor: func(id string) *agent.MCPServer { return broker.ServerFor(self, id) },
	})
	defer mgr.Close()
	return tui.Run(mgr, cfg)
}

// found reports whether the configured binary (or the default name) is runnable.
func found(configured, def string) bool {
	name := configured
	if name == "" {
		name = def
	}
	_, err := exec.LookPath(name)
	return err == nil
}

func listThreads(w io.Writer) error {
	st, err := store.New("")
	if err != nil {
		return err
	}
	threads, err := st.List()
	if err != nil {
		return err
	}
	if len(threads) == 0 {
		fmt.Fprintln(w, "no threads yet")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, t := range threads {
		kinds := t.AgentKind
		if len(t.Members) > 1 { // a shared thread lists every kind in it
			ks := make([]string, len(t.Members))
			for i, mem := range t.Members {
				ks[i] = mem.Kind
			}
			kinds = strings.Join(ks, "+")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t$%.2f\t%s\n", t.Name, kinds, t.Title, t.CostUSD, ago(t.Updated))
	}
	return tw.Flush()
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// mcpApprove is the stdio MCP server Claude Code launches for
// --permission-prompt-tool. The broker token arrives via the environment,
// never on the command line.
func mcpApprove(args []string) error {
	fs := flag.NewFlagSet("mcp-approve", flag.ContinueOnError)
	addr := fs.String("addr", "", "broker address")
	id := fs.String("agent", "", "agent id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *addr == "" || *id == "" {
		return fmt.Errorf("mcp-approve: --addr and --agent are required")
	}
	return approve.MCPServe(os.Stdin, os.Stdout, *addr, approve.TokenFromEnv(), *id)
}
