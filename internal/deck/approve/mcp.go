package approve

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// MCP tool names exposed by the server; Claude sees them as mcp__deck__search,
// mcp__deck__execute and mcp__deck__approve when the server is registered
// under the name "deck". Every agent gets search and execute; only Claude,
// whose --permission-prompt-tool must be an MCP tool, gets approve.
const (
	MCPToolName    = "approve"
	MCPToolSearch  = agent.DeckToolSearch
	MCPToolExecute = agent.DeckToolExecute
)

// ApproveToolFlag is the `deck mcp-approve` flag that adds the approve tool;
// only Claude's adapter passes it.
const ApproveToolFlag = "--approve-tool"

// execTimeout is how long an execute op (other than thread.send) may run.
var execTimeout = 60 * time.Second

// TokenEnv is the environment variable through which the broker token reaches
// the MCP helper process (never on the command line).
const TokenEnv = "DECK_APPROVE_TOKEN"

// TokenFromEnv returns the broker token from the environment, for the
// `deck mcp-approve` wiring.
func TokenFromEnv() string { return os.Getenv(TokenEnv) }

// MCPServe runs a minimal MCP server over newline-delimited JSON-RPC on
// stdin/stdout. It exposes Deck's two tools, "search" and "execute", plus
// "approve" (permission prompts) when approveTool is set; every call is
// forwarded to the broker at addr.
//
// Each tools/call runs on its own goroutine, so a long execute (up to a
// minute) never holds up an approval behind it; replies are written whole,
// one at a time, and carry their request's id. A call the client cancels
// (notifications/cancelled) stops waiting for Deck. MCPServe returns once
// stdin ends and every call has been answered.
func MCPServe(stdin io.Reader, stdout io.Writer, addr, token, agentID string, approveTool bool) error {
	// ReadBytes grows past the buffer for a long line (a Write's content).
	r := bufio.NewReaderSize(stdin, 64<<10)
	enc := json.NewEncoder(stdout)
	var (
		wmu  sync.Mutex
		werr error
		wg   sync.WaitGroup
	)
	write := func(v any) error {
		wmu.Lock()
		defer wmu.Unlock()
		if werr == nil {
			werr = enc.Encode(v)
		}
		return werr
	}
	failed := func() error {
		wmu.Lock()
		defer wmu.Unlock()
		return werr
	}
	var cmu sync.Mutex
	calls := map[string]context.CancelFunc{} // in-flight tools/call by id
	defer wg.Wait()

	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			var req rpcRequest
			if jerr := json.Unmarshal(line, &req); jerr == nil && req.Method != "" {
				isNote := len(req.ID) == 0
				switch {
				case req.Method == "tools/call" && !isNote:
					ctx, cancel := context.WithCancel(context.Background())
					key := string(bytes.TrimSpace(req.ID))
					cmu.Lock()
					calls[key] = cancel
					cmu.Unlock()
					wg.Add(1)
					go func() {
						defer wg.Done()
						defer func() {
							cmu.Lock()
							delete(calls, key)
							cmu.Unlock()
							cancel()
						}()
						_ = mcpDispatch(ctx, req, false, write, addr, token, agentID, approveTool)
					}()
				case req.Method == "notifications/cancelled":
					var p struct {
						RequestID json.RawMessage `json:"requestId"`
					}
					_ = json.Unmarshal(req.Params, &p)
					cmu.Lock()
					if cancel := calls[string(bytes.TrimSpace(p.RequestID))]; cancel != nil {
						cancel()
					}
					cmu.Unlock()
				default:
					if werr := mcpDispatch(context.Background(), req, isNote, write, addr, token, agentID, approveTool); werr != nil {
						return werr
					}
				}
			}
		}
		if werr := failed(); werr != nil {
			return werr
		}
		if err != nil {
			if err == io.EOF {
				wg.Wait()
				return failed()
			}
			return err
		}
	}
}

func mcpDispatch(ctx context.Context, req rpcRequest, isNote bool, write func(any) error, addr, token, agentID string, approveTool bool) error {
	reply := func(result any) error {
		if isNote {
			return nil
		}
		return write(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result})
	}
	fail := func(code int, msg string) error {
		if isNote {
			return nil
		}
		return write(rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: code, Message: msg}})
	}

	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if p.ProtocolVersion == "" {
			p.ProtocolVersion = "2024-11-05"
		}
		return reply(map[string]any{
			"protocolVersion": p.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "deck", "version": "0.1.0"},
		})
	case "ping":
		return reply(map[string]any{})
	case "tools/list":
		return reply(map[string]any{"tools": toolList(approveTool)})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return fail(-32602, "invalid params")
		}
		textResult := func(text string, isErr bool) error {
			res := map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}
			if isErr {
				res["isError"] = true
			}
			return reply(res)
		}
		switch {
		case p.Name == MCPToolName && approveTool:
			var a struct {
				ToolName  string          `json:"tool_name"`
				Input     json.RawMessage `json:"input"`
				ToolUseID string          `json:"tool_use_id"`
			}
			_ = json.Unmarshal(p.Arguments, &a)
			decision := askBroker(ctx, addr, token, agentID, a.ToolUseID, a.ToolName, a.Input)
			return textResult(decision, false)
		case p.Name == MCPToolSearch:
			args := p.Arguments
			if len(args) == 0 || string(args) == "null" {
				args = json.RawMessage(`{}`)
			}
			text, isErr := askHub(ctx, addr, wireRequest{
				Token: token, Kind: kindExec, AgentID: agentID, RequestID: newRequestID(),
				Op: opSearch, Args: args,
			}, execTimeout+15*time.Second)
			return textResult(text, isErr)
		case p.Name == MCPToolExecute:
			in := agent.ParseExecInput(p.Arguments)
			if in.Op == "" {
				return textResult("execute needs an \"op\". Call search (an empty query lists every op and its arguments).", true)
			}
			wait := execTimeout + 15*time.Second
			if in.Op == agent.OpThreadSend {
				wait = messageTimeout + 15*time.Second
			}
			text, isErr := askHub(ctx, addr, wireRequest{
				Token: token, Kind: kindExec, AgentID: agentID, RequestID: newRequestID(),
				Op: in.Op, Args: in.Args,
			}, wait)
			return textResult(text, isErr)
		}
		return fail(-32602, "unknown tool "+p.Name)
	default:
		if isNote {
			return nil // notifications/initialized etc.
		}
		return fail(-32601, "method not found: "+req.Method)
	}
}

// dial connects to the broker; the connection is closed when ctx ends (the
// call was cancelled), which tells the broker to stop waiting for it.
func dial(ctx context.Context, addr string) (net.Conn, func(), error) {
	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, nil, err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	return conn, func() { stop(); conn.Close() }, nil
}

// askBroker forwards one request and returns the decision JSON string. On any
// failure it returns a deny decision so the agent keeps working.
func askBroker(ctx context.Context, addr, token, agentID, requestID, toolName string, input json.RawMessage) string {
	deny := func(msg string) string {
		b, _ := json.Marshal(wireResponse{Behavior: "deny", Message: msg})
		return string(b)
	}
	conn, done, err := dial(ctx, addr)
	if err != nil {
		return deny("Deck approval broker unreachable")
	}
	defer done()
	cwd, _ := os.Getwd()
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	payload, _ := json.Marshal(wireRequest{
		Token: token, AgentID: agentID, RequestID: requestID,
		ToolName: toolName, Input: input, Cwd: cwd,
	})
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return deny("Deck approval broker write failed")
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		if ctx.Err() != nil {
			return deny("the approval request was cancelled")
		}
		return deny("Deck approval broker closed the connection")
	}
	var w wireResponse
	if json.Unmarshal(line, &w) != nil || w.Behavior == "" {
		return deny("Deck approval broker sent an invalid reply")
	}
	for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r') {
		line = line[:len(line)-1]
	}
	return string(line)
}

func toolList(approveTool bool) []any {
	tools := []any{
		map[string]any{
			"name": MCPToolSearch,
			"description": "Search Deck: the ops the execute tool can run, the skills any agent here has (Claude, Codex, OpenCode, Antigravity, Deck), " +
				"and this project's past Deck threads. An empty query lists every op with its arguments.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "Words to look for, e.g. \"git log\", \"pdf skill\", \"login bug\""},
					"limit": map[string]any{"type": "integer", "description": "Most skill and history hits to return (default 5)"},
				},
			},
		},
		map[string]any{
			"name": MCPToolExecute,
			"description": "Run one Deck op. thread.send {to, message} messages another agent in this shared thread (an agent name, or \"all\"); thread.read reads the thread. " +
				"Other ops read chat history (history.*), skills from every agent (skills.*), git and GitHub state (git.*, gh.*) and Deck's running commands (deck.jobs). " +
				"Call search to list them with their arguments. The Deck user sees every call.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"op":   map[string]any{"type": "string", "description": "The op, e.g. \"thread.send\" or \"git.status\""},
					"args": map[string]any{"type": "object", "description": "The op's arguments, e.g. {\"to\": \"codex\", \"message\": \"...\"}"},
				},
				"required": []string{"op"},
			},
		},
	}
	if approveTool {
		tools = append(tools, map[string]any{
			"name":        MCPToolName,
			"description": "Ask the Deck user to approve a tool call",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tool_name":   map[string]any{"type": "string"},
					"input":       map[string]any{"type": "object"},
					"tool_use_id": map[string]any{"type": "string"},
				},
				"required": []string{"tool_name", "input"},
			},
		})
	}
	return tools
}

func newRequestID() string {
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

// askHub forwards an exec request and returns the result text. On any
// failure it returns an error result so the agent keeps working.
func askHub(ctx context.Context, addr string, req wireRequest, wait time.Duration) (text string, isErr bool) {
	conn, done, err := dial(ctx, addr)
	if err != nil {
		return "Deck is unreachable", true
	}
	defer done()
	payload, _ := json.Marshal(req)
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return "Deck is unreachable", true
	}
	conn.SetReadDeadline(time.Now().Add(wait))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		if ctx.Err() != nil {
			return "cancelled", true
		}
		return "Deck did not answer", true
	}
	var w wireReply
	if json.Unmarshal(line, &w) != nil {
		return "Deck sent an invalid reply", true
	}
	return w.Text, w.IsError
}
