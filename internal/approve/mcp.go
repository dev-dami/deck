package approve

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"os"
	"strings"
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

// MCP tool names exposed by the server; Claude sees them as mcp__deck__approve,
// mcp__deck__send_message and mcp__deck__read_thread when the server is
// registered under the name "deck".
const (
	MCPToolName        = "approve"
	MCPToolSendMessage = "send_message"
	MCPToolReadThread  = "read_thread"
)

// TokenEnv is the environment variable through which the broker token reaches
// the MCP helper process (never on the command line).
const TokenEnv = "DECK_APPROVE_TOKEN"

// TokenFromEnv returns the broker token from the environment, for the
// `deck mcp-approve` wiring.
func TokenFromEnv() string { return os.Getenv(TokenEnv) }

// MCPServe runs a minimal MCP server over newline-delimited JSON-RPC on
// stdin/stdout. It exposes "approve" (permission prompts) plus the shared-thread
// tools "send_message" and "read_thread", all forwarded to the broker at addr.
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
		return reply(map[string]any{"tools": toolList()})
	case "tools/call":
		var p struct {
			Name      string `json:"name"`
			Arguments struct {
				ToolName  string          `json:"tool_name"`
				Input     json.RawMessage `json:"input"`
				ToolUseID string          `json:"tool_use_id"`
				To        string          `json:"to"`
				Message   string          `json:"message"`
			} `json:"arguments"`
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
		switch p.Name {
		case MCPToolName:
			decision := askBroker(addr, token, agentID, p.Arguments.ToolUseID, p.Arguments.ToolName, p.Arguments.Input)
			return textResult(decision, false)
		case MCPToolSendMessage:
			if strings.TrimSpace(p.Arguments.To) == "" || strings.TrimSpace(p.Arguments.Message) == "" {
				return textResult("send_message needs both \"to\" and \"message\"", true)
			}
			text, isErr := askHub(addr, wireRequest{
				Token: token, Kind: kindMessage, AgentID: agentID, RequestID: newRequestID(),
				To: strings.TrimSpace(p.Arguments.To), Text: p.Arguments.Message,
			}, messageTimeout+15*time.Second)
			return textResult(text, isErr)
		case MCPToolReadThread:
			text, isErr := askHub(addr, wireRequest{
				Token: token, Kind: kindRead, AgentID: agentID, RequestID: newRequestID(),
			}, 15*time.Second)
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

func toolList() []any {
	return []any{
		map[string]any{
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
		},
		map[string]any{
			"name": MCPToolSendMessage,
			"description": "Send a message to another agent in this shared Deck thread (for example \"codex\" or \"claude2\"), or to \"all\" of them. " +
				"Use it to ask a question, hand over work or share a finding. The other agent receives it as a normal message in the thread. " +
				"The Deck user sees every message you send; nothing is hidden.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"to":      map[string]any{"type": "string", "description": "Agent name such as \"codex\" or \"claude2\", or \"all\""},
					"message": map[string]any{"type": "string", "description": "The message text"},
				},
				"required": []string{"to", "message"},
			},
		},
		map[string]any{
			"name":        MCPToolReadThread,
			"description": "Read the shared Deck thread transcript: what the user and every agent in this thread have said and done so far.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}
}

func newRequestID() string {
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

// askHub forwards a message/read request and returns the result text. On any
// failure it returns an error result so the agent keeps working.
func askHub(addr string, req wireRequest, wait time.Duration) (text string, isErr bool) {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return "Deck is unreachable", true
	}
	defer conn.Close()
	payload, _ := json.Marshal(req)
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return "Deck is unreachable", true
	}
	conn.SetReadDeadline(time.Now().Add(wait))
	line, err := bufio.NewReaderSize(conn, 4<<20).ReadBytes('\n')
	if err != nil {
		return "Deck did not answer", true
	}
	var w wireReply
	if json.Unmarshal(line, &w) != nil {
		return "Deck sent an invalid reply", true
	}
	return w.Text, w.IsError
}
