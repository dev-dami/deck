package approve

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"strings"
	"time"
)

// The edit gate's hook (session/coord.go): Claude Code runs `deck
// mcp-approve ... --edit-gate` as a PreToolUse hook before each of its
// edits, in every permission mode (bypassPermissions skips the permission
// prompt tool, not hooks). The hook hands the edit's file to the broker,
// which waits for Deck's answer; a refusal comes back to Claude as the
// hook's deny reason. Anything that goes wrong lets the edit through: a
// gate that cannot ask must not stop the agent.

// EditGateFlag is the `deck mcp-approve` flag that runs the hook instead of
// the MCP server.
const EditGateFlag = "--edit-gate"

// EditGateTools are the Claude tools the hook runs before.
const EditGateTools = "Edit|MultiEdit|Write|NotebookEdit"

const kindGate = "gate"

// gateWait bounds the hook's wait for Deck; Deck answers sooner (its own
// timeout, config edit_gate).
var gateWait = 10 * time.Minute

type gateReply struct {
	Deny string `json:"deny,omitempty"`
}

// SetEditGate installs the edit gate: given an agent and the files its edit
// changes, "" lets it go on and anything else refuses it with that reason.
func (b *Broker) SetEditGate(f func(agentID string, paths []string) string) {
	b.mu.Lock()
	b.gate = f
	b.mu.Unlock()
}

func (b *Broker) handleGate(req wireRequest, gone <-chan struct{}) gateReply {
	b.mu.Lock()
	f := b.gate
	b.mu.Unlock()
	var a struct {
		Paths []string `json:"paths"`
	}
	if f == nil || json.Unmarshal(req.Args, &a) != nil || len(a.Paths) == 0 {
		return gateReply{}
	}
	done := make(chan string, 1)
	go func() { done <- f(req.AgentID, a.Paths) }()
	select {
	case deny := <-done:
		return gateReply{Deny: b.scrub(deny)}
	case <-b.done:
	case <-gone:
	}
	return gateReply{}
}

// hookInput is what Claude Code hands a PreToolUse hook on stdin.
type hookInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	} `json:"tool_input"`
}

type hookOutput struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

// EditGateHook is the hook: it reads Claude Code's PreToolUse input from in,
// asks the broker at addr, and writes a deny decision to out when Deck
// refuses the edit. It never fails the hook.
func EditGateHook(in io.Reader, out io.Writer, addr, token, agentID string) error {
	var h hookInput
	if json.NewDecoder(in).Decode(&h) != nil {
		return nil
	}
	p := strings.TrimSpace(h.ToolInput.FilePath)
	if p == "" {
		p = strings.TrimSpace(h.ToolInput.NotebookPath)
	}
	if p == "" {
		return nil
	}
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil
	}
	defer c.Close()
	args, _ := json.Marshal(map[string][]string{"paths": {p}})
	req, _ := json.Marshal(wireRequest{Token: token, Kind: kindGate, AgentID: agentID, ToolName: h.ToolName, Args: args})
	_ = c.SetDeadline(time.Now().Add(gateWait))
	if _, err := c.Write(append(req, '\n')); err != nil {
		return nil
	}
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil {
		return nil
	}
	var r gateReply
	if json.Unmarshal(line, &r) != nil || r.Deny == "" {
		return nil
	}
	var o hookOutput
	o.HookSpecificOutput.HookEventName = "PreToolUse"
	o.HookSpecificOutput.PermissionDecision = "deny"
	o.HookSpecificOutput.PermissionDecisionReason = r.Deny
	return json.NewEncoder(out).Encode(o)
}
