package approve

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"os"
	"time"
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

// MCPToolName is the tool exposed by the server; Claude sees it as
// mcp__deck__approve when the server is registered under the name "deck".
const MCPToolName = "approve"

// TokenEnv is the environment variable through which the broker token reaches
// the MCP helper process (never on the command line).
const TokenEnv = "DECK_APPROVE_TOKEN"

// TokenFromEnv returns the broker token from the environment, for the
// `deck mcp-approve` wiring.
func TokenFromEnv() string { return os.Getenv(TokenEnv) }

// MCPServe runs a minimal MCP server over newline-delimited JSON-RPC on
// stdin/stdout. It exposes one tool, "approve", that forwards permission
// prompts to the broker at addr.
func MCPServe(stdin io.Reader, stdout io.Writer, addr, token, agentID string) error {
	r := bufio.NewReaderSize(stdin, 4<<20)
	enc := json.NewEncoder(stdout)
	write := func(v any) error { return enc.Encode(v) }

	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			var req rpcRequest
			if jerr := json.Unmarshal(line, &req); jerr == nil && req.Method != "" {
				isNote := len(req.ID) == 0
				if werr := mcpDispatch(req, isNote, write, addr, token, agentID); werr != nil {
					return werr
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func mcpDispatch(req rpcRequest, isNote bool, write func(any) error, addr, token, agentID string) error {
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
		return reply(map[string]any{"tools": []any{map[string]any{
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
		}}})
	case "tools/call":
		var p struct {
			Name      string `json:"name"`
			Arguments struct {
				ToolName  string          `json:"tool_name"`
				Input     json.RawMessage `json:"input"`
				ToolUseID string          `json:"tool_use_id"`
			} `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return fail(-32602, "invalid params")
		}
		if p.Name != MCPToolName {
			return fail(-32602, "unknown tool "+p.Name)
		}
		decision := askBroker(addr, token, agentID, p.Arguments.ToolUseID, p.Arguments.ToolName, p.Arguments.Input)
		return reply(map[string]any{
			"content": []any{map[string]any{"type": "text", "text": decision}},
		})
	default:
		if isNote {
			return nil // notifications/initialized etc.
		}
		return fail(-32601, "method not found: "+req.Method)
	}
}

// askBroker forwards one request and returns the decision JSON string. On any
// failure it returns a deny decision so the agent keeps working.
func askBroker(addr, token, agentID, requestID, toolName string, input json.RawMessage) string {
	deny := func(msg string) string {
		b, _ := json.Marshal(wireResponse{Behavior: "deny", Message: msg})
		return string(b)
	}
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return deny("Deck approval broker unreachable")
	}
	defer conn.Close()
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
	line, err := bufio.NewReaderSize(conn, 4<<20).ReadBytes('\n')
	if err != nil {
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
