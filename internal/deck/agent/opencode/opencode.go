// Package opencode is the Deck adapter for OpenCode. It launches the user's
// own installed `opencode acp` (the Agent Client Protocol server OpenCode
// ships) and drives it with Deck's ACP client (package acp).
//
// OpenCode brings its own providers: every model it is configured for,
// including external providers, is offered to Deck through the session's
// "model" option, and models that take a reasoning effort add an "effort"
// option. Deck never reads OpenCode's credentials and never calls a model API
// itself; it never writes OpenCode's config either. The one setting it passes
// is OPENCODE_CONFIG_CONTENT in the child's environment, so that edits and
// commands ask first and Deck's approvals (and its plan / ask / yolo modes)
// decide them.
package opencode

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/acp"
	"github.com/dev-dami/deck/internal/deck/agent/codex"
)

// Options tune the adapter.
type Options struct {
	// ClientVersion is reported in the initialize handshake.
	ClientVersion string
	// Tap, if set, sees every protocol line (both directions). For tests.
	Tap codex.Tap
}

// New returns the OpenCode adapter. binary is the opencode executable
// ("" = "opencode" on PATH); Spec.Binary overrides it per run.
func New(binary string) agent.Adapter { return NewWithOptions(binary, Options{}) }

// NewWithOptions is New with explicit options.
func NewWithOptions(binary string, opts Options) agent.Adapter {
	return acp.New(binary, acp.Config{
		Kind: "opencode", Command: "opencode", Args: []string{"acp"},
		Env: childEnv, Install: "npm install -g opencode-ai",
		ClientVersion: opts.ClientVersion, Tap: opts.Tap,
		// The instructions travel in the first prompt of a session (once:
		// Spec.Briefed), the fill and window come from ACP usage_update, and
		// Deck's MCP server is added to the session next to the user's own
		// (partial). No config or instruction-file isolation has been checked
		// against the CLI.
		Caps: agent.ContextCaps{
			InstructionSlot: agent.InstrFirstPrompt, InstrPersistsOnResume: true,
			IsolateMCP:     agent.SupportPartial,
			ReportsContext: agent.ContextCLI, ReportsWindow: agent.ContextCLI,
		},
		// In ask mode its edits, commands and fetches ask through Deck.
		ContractSafe: true,
		Video:        TakesVideo,
	})
}

// TakesVideo reports whether the OpenCode model ("provider/model", or a
// router's "provider/vendor/model") is sent the videos the person attaches.
// OpenCode reads an ACP resource_link to a local file into a file part with
// its MIME type and passes it on when models.dev lists video among the
// model's inputs (otherwise it swaps it for a note that the model cannot
// read it). The ACP model list does not carry those inputs, so this is
// Deck's own table, kept conservative: Google's Gemini chat models take
// video; Claude, GPT and the rest do not, and neither do Gemini's
// embedding, speech and image-generation models. The default model ("")
// is unknown, so it takes none.
func TakesVideo(model string) bool {
	name := strings.ToLower(model[strings.LastIndexByte(model, '/')+1:])
	if !strings.HasPrefix(name, "gemini-") {
		return false
	}
	for _, not := range []string{"embedding", "tts", "image", "live"} {
		if strings.Contains(name, not) {
			return false
		}
	}
	return true
}

// askFirst makes OpenCode ask before it edits a file, runs a command or
// fetches a page, so every such action reaches Deck as an approval.
var askFirst = map[string]any{"edit": "ask", "bash": "ask", "webfetch": "ask"}

// childEnv is the environment for `opencode acp`: the user's, the spec's, and
// OPENCODE_CONFIG_CONTENT with the ask-first permissions laid over whatever
// inline config the user already had there.
func childEnv(extra map[string]string) []string {
	var env []string
	inline := map[string]any{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, "OPENCODE_CONFIG_CONTENT") {
			_ = json.Unmarshal([]byte(v), &inline)
			continue
		}
		env = append(env, kv)
	}
	for k, v := range extra {
		if strings.EqualFold(k, "OPENCODE_CONFIG_CONTENT") {
			_ = json.Unmarshal([]byte(v), &inline)
			continue
		}
		env = append(env, k+"="+v)
	}
	perm, _ := inline["permission"].(map[string]any)
	if perm == nil {
		perm = map[string]any{}
	}
	for k, v := range askFirst {
		perm[k] = v
	}
	inline["permission"] = perm
	b, _ := json.Marshal(inline)
	return append(env, "OPENCODE_CONFIG_CONTENT="+string(b))
}
