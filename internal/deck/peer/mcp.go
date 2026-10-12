package peer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/sourcegraph/jsonrpc2"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// MCP over an mcp channel. The methods are MCP's: initialize,
// notifications/initialized, ping, tools/list and tools/call. tools/list
// returns exactly Deck's two tools, search and execute, with the input
// schemas every local agent sees; any other tool or method is a JSON-RPC
// error.

// Tool names, the same as Deck's own MCP server.
const (
	ToolSearch  = agent.DeckToolSearch
	ToolExecute = agent.DeckToolExecute
)

// ProtocolVersion is the MCP version the peer server speaks when the client
// asks for none.
const ProtocolVersion = "2024-11-05"

// JSON-RPC error codes.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeNotInitialized = -32002
	codeBusy           = -32000
)

// maxResultText caps a tool result's text so a reply always fits in a frame.
const maxResultText = 256 << 10

// Tool is an entry of tools/list.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// Tools returns the two tools a peer sees. The input schemas match Deck's
// local MCP server (internal/deck/approve), so an agent calls a peer the same
// way it calls Deck.
func Tools() []Tool {
	return []Tool{
		{
			Name:        ToolSearch,
			Description: "Search this Deck: the ops the execute tool can run for a peer. An empty query lists every op with its arguments.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "Words to look for, e.g. \"git log\", \"pdf skill\", \"login bug\""},
					"limit": map[string]any{"type": "integer", "description": "Most skill and history hits to return (default 5)"},
				},
			},
		},
		{
			Name:        ToolExecute,
			Description: "Run one Deck op on this Deck, e.g. peer.status. Call search to list the ops a peer may run.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"op":   map[string]any{"type": "string", "description": "The op, e.g. \"thread.send\" or \"git.status\""},
					"args": map[string]any{"type": "object", "description": "The op's arguments, e.g. {\"to\": \"codex\", \"message\": \"...\"}"},
				},
				"required": []string{"op"},
			},
		},
	}
}

// ToolResult is a tools/call result.
type ToolResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

// Content is one content block of a tool result.
type Content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Text joins the result's text blocks.
func (r ToolResult) Text() string {
	var b strings.Builder
	for _, c := range r.Content {
		b.WriteString(c.Text)
	}
	return b.String()
}

func textResult(text string, isErr bool) ToolResult {
	if len(text) > maxResultText {
		cut := maxResultText
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut] + "\n[truncated]"
	}
	return ToolResult{Content: []Content{{Type: "text", Text: text}}, IsError: isErr}
}

func rpcErr(code int64, msg string) *jsonrpc2.Error {
	return &jsonrpc2.Error{Code: code, Message: msg}
}

// slogLogger routes jsonrpc2's own log lines to slog at debug level.
type slogLogger struct{ l *slog.Logger }

func (s slogLogger) Printf(format string, v ...any) {
	s.l.Debug(strings.TrimSpace(fmt.Sprintf(format, v...)))
}

// serverLimits bounds one mcp channel's server.
type serverLimits struct {
	maxInFlight int           // concurrent tools/call
	callTimeout time.Duration // per tools/call
}

// mcpServer answers MCP requests on one channel for one caller.
type mcpServer struct {
	h      Handler
	caller Caller
	info   map[string]any
	lim    serverLimits
	log    *slog.Logger

	mu          sync.Mutex
	initialized bool
	calls       map[string]context.CancelFunc

	sem chan struct{}
	wg  sync.WaitGroup
}

func newMCPServer(h Handler, caller Caller, name, version string, lim serverLimits, log *slog.Logger) *mcpServer {
	if lim.maxInFlight <= 0 {
		lim.maxInFlight = 8
	}
	if lim.callTimeout <= 0 {
		lim.callTimeout = 60 * time.Second
	}
	return &mcpServer{
		h: h, caller: caller, lim: lim, log: log,
		info:  map[string]any{"name": "deck-peer:" + name, "version": version},
		calls: map[string]context.CancelFunc{},
		sem:   make(chan struct{}, lim.maxInFlight),
	}
}

// serveMCP runs the MCP server on rwc until the channel ends or ctx is done,
// and waits for every call it started.
func serveMCP(ctx context.Context, rwc io.ReadWriteCloser, s *mcpServer, maxMsg int) {
	rpc := jsonrpc2.NewConn(ctx, newLineStream(rwc, maxMsg), s, jsonrpc2.SetLogger(slogLogger{s.log}))
	<-rpc.DisconnectNotify()
	s.wg.Wait()
}

// Handle implements jsonrpc2.Handler. It runs on the channel's read loop, so
// everything but tools/call answers inline; tools/call runs on its own
// goroutine, at most lim.maxInFlight at a time.
func (s *mcpServer) Handle(ctx context.Context, c *jsonrpc2.Conn, req *jsonrpc2.Request) {
	reply := func(v any) {
		if !req.Notif {
			_ = c.Reply(ctx, req.ID, v)
		}
	}
	fail := func(e *jsonrpc2.Error) {
		if !req.Notif {
			_ = c.ReplyWithError(ctx, req.ID, e)
		}
	}
	params := json.RawMessage(nil)
	if req.Params != nil {
		params = *req.Params
	}

	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(params, &p)
		if p.ProtocolVersion == "" || len(p.ProtocolVersion) > 32 {
			p.ProtocolVersion = ProtocolVersion
		}
		s.mu.Lock()
		s.initialized = true
		s.mu.Unlock()
		reply(map[string]any{
			"protocolVersion": p.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      s.info,
		})
	case "ping":
		reply(map[string]any{})
	case "notifications/cancelled":
		var p struct {
			RequestID jsonrpc2.ID `json:"requestId"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		s.mu.Lock()
		if cancel := s.calls[p.RequestID.String()]; cancel != nil {
			cancel()
		}
		s.mu.Unlock()
	case "tools/list":
		if !s.ready() {
			fail(rpcErr(codeNotInitialized, "not initialized"))
			return
		}
		reply(map[string]any{"tools": Tools()})
	case "tools/call":
		if req.Notif {
			return
		}
		if !s.ready() {
			fail(rpcErr(codeNotInitialized, "not initialized"))
			return
		}
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			fail(rpcErr(codeInvalidParams, "invalid params"))
			return
		}
		if p.Name != ToolSearch && p.Name != ToolExecute {
			fail(rpcErr(codeInvalidParams, "unknown tool"))
			return
		}
		select {
		case s.sem <- struct{}{}:
		default:
			fail(rpcErr(codeBusy, "too many calls in flight"))
			return
		}
		cctx, cancel := context.WithTimeout(ctx, s.lim.callTimeout)
		key := req.ID.String()
		s.mu.Lock()
		s.calls[key] = cancel
		s.mu.Unlock()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			res := s.call(cctx, p.Name, p.Arguments)
			// Free the slot before replying, so the caller's next call
			// never finds it still taken.
			s.mu.Lock()
			delete(s.calls, key)
			s.mu.Unlock()
			cancel()
			<-s.sem
			reply(res)
		}()
	default:
		if req.Notif {
			return // notifications/initialized and any other notification
		}
		fail(rpcErr(codeMethodNotFound, "method not found"))
	}
}

func (s *mcpServer) ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.initialized
}

// call runs one tool. A handler panic is turned into an error result: a
// peer must never be able to crash this Deck through a handler bug.
func (s *mcpServer) call(ctx context.Context, tool string, args json.RawMessage) (res ToolResult) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("peer: handler panic", "peer", s.caller.Name, "tool", tool, "panic", r)
			res = textResult("internal error", true)
		}
	}()
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage(`{}`)
	}
	var (
		text string
		err  error
	)
	switch tool {
	case ToolSearch:
		text, err = s.h.Search(ctx, s.caller, args)
	case ToolExecute:
		in := agent.ParseExecInput(args)
		if in.Op == "" {
			return textResult("execute needs an \"op\". Call search to list the ops.", true)
		}
		if !s.caller.allowed(in.Op) {
			return textResult(fmt.Sprintf("op %q is not allowed for peer %q", in.Op, s.caller.Name), true)
		}
		text, err = s.h.Execute(ctx, s.caller, in.Op, in.Args)
	}
	if err != nil {
		if ctx.Err() != nil && !errors.Is(err, ErrUnknownOp) {
			return textResult("cancelled: "+ctx.Err().Error(), true)
		}
		return textResult(err.Error(), true)
	}
	return textResult(text, false)
}

// Client is the MCP client end of one mcp channel this Deck opened.
type Client struct {
	rpc *jsonrpc2.Conn
}

// refuseHandler answers every request a server sends to a client with
// "method not found": peers get no sampling, roots or elicitation.
type refuseHandler struct{}

func (refuseHandler) Handle(ctx context.Context, c *jsonrpc2.Conn, req *jsonrpc2.Request) {
	if !req.Notif {
		_ = c.ReplyWithError(ctx, req.ID, rpcErr(codeMethodNotFound, "method not found"))
	}
}

func newClient(ctx context.Context, rwc io.ReadWriteCloser, maxMsg int, log *slog.Logger) *Client {
	return &Client{rpc: jsonrpc2.NewConn(ctx, newLineStream(rwc, maxMsg), refuseHandler{}, jsonrpc2.SetLogger(slogLogger{log}))}
}

// Initialize runs MCP's initialize handshake and returns the server's info.
func (c *Client) Initialize(ctx context.Context) (map[string]any, error) {
	var res struct {
		ServerInfo map[string]any `json:"serverInfo"`
	}
	err := c.rpc.Call(ctx, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "deck-peer", "version": "1"},
	}, &res)
	if err != nil {
		return nil, err
	}
	return res.ServerInfo, c.rpc.Notify(ctx, "notifications/initialized", map[string]any{})
}

// ListTools returns the server's tools.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	var res struct {
		Tools []Tool `json:"tools"`
	}
	err := c.rpc.Call(ctx, "tools/list", map[string]any{}, &res)
	return res.Tools, err
}

// CallTool calls a tool with args (any JSON-marshalable value).
func (c *Client) CallTool(ctx context.Context, name string, args any) (ToolResult, error) {
	var res ToolResult
	err := c.rpc.Call(ctx, "tools/call", map[string]any{"name": name, "arguments": args}, &res)
	return res, err
}

// Execute calls the execute tool with op and args.
func (c *Client) Execute(ctx context.Context, op string, args any) (ToolResult, error) {
	if args == nil {
		args = map[string]any{}
	}
	return c.CallTool(ctx, ToolExecute, map[string]any{"op": op, "args": args})
}

// Ping sends an MCP ping and returns the round trip.
func (c *Client) Ping(ctx context.Context) (time.Duration, error) {
	start := time.Now()
	var res json.RawMessage
	err := c.rpc.Call(ctx, "ping", nil, &res)
	return time.Since(start), err
}

// Call sends any request; for tests and future ops.
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	return c.rpc.Call(ctx, method, params, result)
}

// Done is closed when the channel ends.
func (c *Client) Done() <-chan struct{} { return c.rpc.DisconnectNotify() }

// Close closes the channel.
func (c *Client) Close() error {
	err := c.rpc.Close()
	if errors.Is(err, jsonrpc2.ErrClosed) {
		return nil
	}
	return err
}
