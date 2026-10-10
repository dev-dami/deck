package codex

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// ---------------------------------------------------------------------------
// Registering Deck's own MCP server with a codex thread
//
// Mechanism (checked against codex-cli 0.160.1: `codex app-server
// generate-json-schema`, `codex mcp get --json` with -c overrides, and a live
// run): the thread/start and thread/resume params carry a free-form `config`
// object. It takes the same dotted-key overrides as `codex -c key=value`, is
// scoped to that one thread and is never written anywhere. Deck therefore never
// touches ~/.codex/config.toml or CODEX_HOME. Keys used:
//
//	mcp_servers.<name>.command                      the Deck binary
//	mcp_servers.<name>.args                         its arguments
//	mcp_servers.<name>.env_vars                     NAMES of env vars to forward
//	mcp_servers.<name>.startup_timeout_sec          generous, the first start is cold
//	mcp_servers.<name>.default_tools_approval_mode  "approve": no prompt per call
//
// Secrets (MCPServer.Env, the hub token) are deliberately NOT sent as the
// `env` table: that would put the token on the JSON-RPC wire (visible to the
// protocol Tap and to anything logging it). Instead the values go into the
// environment of the `codex app-server` process (cmd.Env, never argv) and the
// config lists only their names under `env_vars`, which codex forwards from its
// own environment to the MCP child. The app-server command line stays a plain
// `codex app-server`. Codex's default shell_environment_policy strips names
// containing KEY/SECRET/TOKEN from commands the model runs, so a *_TOKEN
// variable does not reach the agent's shell tool.
// ---------------------------------------------------------------------------

const (
	deckServerStartupSec = 30
	// deckToolApproval is codex's per-server default tool approval mode that
	// never prompts (values: auto|prompt|writes|approve).
	deckToolApproval = "approve"
)

// mcpThreadConfig builds the thread `config` overrides for the server, or nil.
func mcpThreadConfig(m *agent.MCPServer) map[string]any {
	if m == nil || m.Name == "" || m.Command == "" {
		return nil
	}
	p := "mcp_servers." + m.Name + "."
	cfg := map[string]any{
		p + "command":                     m.Command,
		p + "args":                        append([]string{}, m.Args...),
		p + "startup_timeout_sec":         deckServerStartupSec,
		p + "default_tools_approval_mode": deckToolApproval,
	}
	if names := sortedEnvNames(m.Env); len(names) > 0 {
		cfg[p+"env_vars"] = names
	}
	return cfg
}

// mcpProcessEnv returns the KEY=VALUE entries to add to the app-server process
// environment so codex can forward them to the MCP server.
func mcpProcessEnv(m *agent.MCPServer) []string {
	if m == nil {
		return nil
	}
	var out []string
	for _, k := range sortedEnvNames(m.Env) {
		out = append(out, k+"="+m.Env[k])
	}
	return out
}

func sortedEnvNames(env map[string]string) []string {
	names := make([]string, 0, len(env))
	for k := range env {
		if k != "" && !strings.ContainsAny(k, "=\x00") {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	return names
}

// ---------------------------------------------------------------------------
// MCP elicitation fallback
// ---------------------------------------------------------------------------

// deckTools are the only tools of the "deck" server Deck auto-accepts.
var deckTools = map[string]bool{"send_message": true, "read_thread": true}

var deckToolWord = regexp.MustCompile(`\b(send_message|read_thread)\b`)

// elicitationParams is McpServerElicitationRequestParams (subset). Codex raises
// an MCP tool approval as an elicitation whose _meta carries
// codex_approval_kind = "mcp_tool_call" plus the tool's title/params.
type elicitationParams struct {
	ServerName string          `json:"serverName"`
	Message    string          `json:"message"`
	Meta       json.RawMessage `json:"_meta"`
}

// isDeckToolApproval reports whether the elicitation is codex asking to run
// one of Deck's messaging tools on the deck server. Anything else (other
// servers, other tools, tools it cannot identify) returns false and is declined.
func isDeckToolApproval(params json.RawMessage) bool {
	var p elicitationParams
	if json.Unmarshal(params, &p) != nil || p.ServerName != deckServerName {
		return false
	}
	var meta map[string]json.RawMessage
	_ = json.Unmarshal(p.Meta, &meta)
	if kind := metaString(meta, "codex_approval_kind"); kind != "" && kind != "mcp_tool_call" {
		return false
	}
	for _, k := range []string{"tool_name", "tool_title"} {
		if v := metaString(meta, k); v != "" {
			return deckTools[v]
		}
	}
	// No explicit tool name: accept only a message naming exactly one of our tools.
	return len(deckToolWord.FindAllString(p.Message, -1)) == 1
}

func metaString(meta map[string]json.RawMessage, key string) string {
	var s string
	if raw, ok := meta[key]; ok && json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

// ---------------------------------------------------------------------------
// Translation of the deck server's tool calls
// ---------------------------------------------------------------------------

const deckServerName = "deck"

// deckToolDisplay maps a deck-server mcpToolCall to its display name and title.
// ok is false for other servers/tools, which keep the generic rendering.
func deckToolDisplay(it threadItem) (name, title string, ok bool) {
	if it.Type != "mcpToolCall" || it.Server != deckServerName {
		return "", "", false
	}
	switch it.Tool {
	case "send_message":
		var a struct {
			To      string `json:"to"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(it.Arguments, &a)
		return "message", "to " + a.To + ": " + truncateRunes(oneLine(a.Message), 60), true
	case "read_thread":
		return "read", "shared thread", true
	}
	return "", "", false
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// deckResultText renders a deck tool result (the text blocks of the MCP
// result, or the error message) as plain text.
func deckResultText(it threadItem) string {
	if len(it.Error) > 0 && string(it.Error) != "null" {
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(it.Error, &e) == nil && e.Message != "" {
			return e.Message
		}
		return string(it.Error)
	}
	if len(it.Result) == 0 || string(it.Result) == "null" {
		return ""
	}
	var r struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(it.Result, &r) != nil {
		return string(it.Result)
	}
	var parts []string
	for _, c := range r.Content {
		if c.Type == "text" && c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}
