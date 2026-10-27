// Package pi is the Deck adapter for Pi, the pi coding agent
// (@earendil-works/pi-coding-agent). Pi itself speaks only its own RPC mode;
// its Agent Client Protocol server is the pi-acp adapter
// (https://github.com/svkozak/pi-acp), which runs `pi --mode rpc` and bridges
// it. Deck launches the user's installed `pi-acp` and drives it with Deck's
// ACP client (package acp). PI_ACP_PI_COMMAND, when the user sets it, still
// tells pi-acp which pi to run.
//
// Pi brings its own providers and logins: the models it offers are those it
// has a key or login for, listed as the session's "model" option
// ("provider/model"), and a model's thinking levels are its "thought_level"
// option ("off" is Deck's "none").
//
// Differences from OpenCode, which uses the same client:
//   - Pi runs its tools without asking: there is no approval for an edit or
//     a command, so Deck's ask and plan modes cannot hold it back, and Pi
//     never runs a teammate's contract. The approvals Deck does get are an
//     extension's confirm and select prompts.
//   - pi-acp does not pass ACP's MCP servers on to pi, so Pi has none of
//     Deck's tools (thread.send, thread.read and the rest).
//   - Pi has no plan mode; its ACP "mode" is the thinking level.
package pi

import (
	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/acp"
)

// Command is the ACP server Deck runs for Pi, and Install how to get it.
const (
	Command = "pi-acp"
	Install = "npm install -g @earendil-works/pi-coding-agent pi-acp"
)

// New returns the Pi adapter. binary is the pi-acp executable ("" = "pi-acp"
// on PATH); Spec.Binary overrides it per run.
func New(binary string) *acp.Adapter {
	return acp.New(binary, acp.Config{
		Kind: "pi", Command: Command, Install: Install,
		// The instructions travel in the first prompt of a session (once:
		// Spec.Briefed); pi-acp reports pi's context fill and window with
		// usage_update. Nothing is isolated, and no MCP server reaches pi.
		Caps: agent.ContextCaps{
			InstructionSlot: agent.InstrFirstPrompt, InstrPersistsOnResume: true,
			ReportsContext: agent.ContextCLI, ReportsWindow: agent.ContextCLI,
		},
		EffortAliases: map[string]string{"off": "none"},
	})
}
