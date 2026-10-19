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
	// perKey, when set, also caps calls in flight per pinned key over every
	// channel and link of that key.
	perKey *keyLimiter
}

// mcpServer answers MCP requests on one channel for one caller.
type mcpServer struct {
	h      Handler
	caller Caller
	info   map[string]any
	lim    serverLimits
	log    *slog.Logger
	// recheck, when set, runs before every call: it returns the caller as
	// the pins and config say now, or an error when the peer was removed or
	// re-paired with another key. revoked is then called (it drops the link).
	recheck func(Caller) (Caller, error)
	revoked func()

	mu          sync.Mutex
	initialized bool
	calls       map[string]context.CancelFunc
	// closed is set once the channel is gone: no call starts after it, so
	// wg.Add never races serveMCP's wg.Wait.
	closed bool

	sem chan struct{}
	wg  sync.WaitGroup
}

// ErrRevoked means a peer is no longer pinned here, or is pinned with
// another key than the link was made with.
var ErrRevoked = errors.New("peer: no longer pinned (removed or re-paired)")

// Fixed answers for failed calls. The handler's own error text is logged here
// and never sent: it may hold paths, hosts or other local detail.
const (
	msgUnknownOp = "unknown op; call search to list the ops"
	msgFailed    = "op failed"
	msgInternal  = "internal error"
	msgTimeout   = "cancelled: the call timed out"
	msgCancelled = "cancelled"
	msgRevoked   = "this Deck no longer accepts calls from you"
)

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
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
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
		// Handle runs on the read loop, one request at a time, so the
		// check and the insert below cannot race.
		key := req.ID.String()
		s.mu.Lock()
		_, dup := s.calls[key]
		s.mu.Unlock()
		if dup {
			fail(rpcErr(codeInvalidRequest, "duplicate request id"))
			return
		}
		release, ok := s.acquire()
		if !ok {
			fail(rpcErr(codeBusy, "too many calls in flight"))
			return
		}
		cctx, cancel := context.WithTimeout(ctx, s.lim.callTimeout)
		s.mu.Lock()
		if s.closed {
			// The channel is gone: there is no one to answer.
			s.mu.Unlock()
			cancel()
			release()
			return
		}
		s.calls[key] = cancel
		s.wg.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.wg.Done()
			// call frees the slot before it returns when the handler is
			// done, so the caller's next call never finds it still taken;
			// a handler still running after the timeout keeps it.
			res := s.call(cctx, p.Name, p.Arguments, release)
			s.mu.Lock()
			delete(s.calls, key)
			s.mu.Unlock()
			cancel()
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

// acquire takes a call slot on this channel and, when set, one of the
// caller's key; release frees both, once.
func (s *mcpServer) acquire() (release func(), ok bool) {
	select {
	case s.sem <- struct{}{}:
	default:
		return nil, false
	}
	fp := s.caller.Fingerprint
	if s.lim.perKey != nil && !s.lim.perKey.acquire(fp) {
		<-s.sem
		return nil, false
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			if s.lim.perKey != nil {
				s.lim.perKey.release(fp)
			}
			<-s.sem
		})
	}, true
}

// call checks one tool call and runs it. release frees the call's slot; it is
// called by the time call returns, unless a handler that ignored ctx is
// still running, which keeps its slot until it does return.
func (s *mcpServer) call(ctx context.Context, tool string, args json.RawMessage, release func()) ToolResult {
	handed := false
	defer func() {
		if !handed {
			release()
		}
	}()
	caller := s.caller
	if s.recheck != nil {
		c, err := s.recheck(caller)
		if err != nil {
			s.log.Warn("peer: refusing a call from a peer no longer pinned; closing its link", "peer", caller.Name, "error", err)
			if s.revoked != nil {
				s.revoked()
			}
			return textResult(msgRevoked, true)
		}
		caller = c
	}
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage(`{}`)
	}
	switch tool {
	case ToolSearch:
		var in struct {
			Query string `json:"query"`
		}
		if json.Unmarshal(args, &in) != nil {
			return textResult("search takes {\"query\": \"words\"}", true)
		}
		handed = true
		return s.run(ctx, caller, tool, release, func(ctx context.Context) (string, error) {
			ops, err := s.h.Search(ctx, caller, in.Query)
			if err != nil {
				return "", err
			}
			return searchText(caller, ops), nil
		})
	default: // ToolExecute
		in := agent.ParseExecInput(args)
		if in.Op == "" {
			return textResult("execute needs an \"op\". Call search to list the ops.", true)
		}
		if !caller.Allowed(in.Op) {
			return textResult(fmt.Sprintf("op %q is not allowed for peer %q", truncate(in.Op, 64), caller.Name), true)
		}
		handed = true
		return s.run(ctx, caller, in.Op, release, func(ctx context.Context) (string, error) {
			return s.h.Execute(ctx, caller, in.Op, in.Args)
		})
	}
}

// searchText formats the ops the caller may run, one per line.
func searchText(caller Caller, ops []OpInfo) string {
	var b strings.Builder
	for _, op := range ops {
		if !caller.Allowed(op.Name) {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%s %s: %s", op.Name, op.Args, op.Desc)
	}
	if b.Len() == 0 {
		return "no ops match"
	}
	return b.String()
}

// run calls fn on its own goroutine and answers when it returns or when ctx
// ends, whichever comes first, so a handler that ignores ctx can hold neither
// the channel, nor Conn.Close, nor Server.Close. Such a handler keeps its slot
// (release) until it returns, so stuck handlers cannot pile up past the
// limits. A panic becomes "internal error": a peer must never be able to
// crash this Deck through a handler bug.
func (s *mcpServer) run(ctx context.Context, caller Caller, what string, release func(), fn func(context.Context) (string, error)) ToolResult {
	type result struct {
		text     string
		err      error
		panicked bool
	}
	done := make(chan result, 1)
	go func() {
		var r result
		defer func() {
			if p := recover(); p != nil {
				s.log.Error("peer: handler panic", "peer", caller.Name, "op", truncate(what, 64), "panic", p)
				r = result{panicked: true}
			}
			release()
			done <- r
		}()
		r.text, r.err = fn(ctx)
	}()
	var r result
	select {
	case r = <-done:
	case <-ctx.Done():
		select {
		case r = <-done: // finished at the same moment: use its answer
		default:
			s.log.Warn("peer: handler still running after its call ended", "peer", caller.Name, "op", truncate(what, 64))
			return cancelledResult(ctx)
		}
	}
	switch {
	case r.panicked:
		return textResult(msgInternal, true)
	case r.err == nil:
		return textResult(r.text, false)
	case errors.Is(r.err, ErrUnknownOp):
		return textResult(msgUnknownOp, true)
	case ctx.Err() != nil:
		return cancelledResult(ctx)
	}
	s.log.Info("peer: op failed", "peer", caller.Name, "op", truncate(what, 64), "error", r.err)
	return textResult(msgFailed, true)
}

func cancelledResult(ctx context.Context) ToolResult {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return textResult(msgTimeout, true)
	}
	return textResult(msgCancelled, true)
}

// Client is the MCP client end of one mcp channel this Deck opened. It has its
// own small request/response layer instead of a jsonrpc2.Conn: that Conn keeps
// calls it has already failed in its pending map, and a reply that arrives
// after Close then sends on a closed channel and panics the process, which a
// peer could time. Here a call leaves the map before it is answered and no
// channel is ever closed, so a late or repeated reply is ignored.
type Client struct {
	st   *lineStream
	log  *slog.Logger
	mu   sync.Mutex
	seq  uint64
	pend map[uint64]chan clientReply
	err  error // why the channel ended; nil while it is open
	done chan struct{}
}

type clientReply struct {
	result json.RawMessage
	err    error
}

// clientMsg is any message a server may send a client.
type clientMsg struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *jsonrpc2.Error `json:"error,omitempty"`
}

func newClient(ctx context.Context, rwc io.ReadWriteCloser, maxMsg int, log *slog.Logger) *Client {
	c := &Client{st: newLineStream(rwc, maxMsg), log: log, pend: map[uint64]chan clientReply{}, done: make(chan struct{})}
	go c.read()
	go func() {
		select {
		case <-ctx.Done():
			c.finish(jsonrpc2.ErrClosed)
		case <-c.done:
		}
	}()
	return c
}

// read dispatches replies to their calls until the stream ends. Requests a
// server sends to a client get "method not found": peers get no sampling,
// roots or elicitation.
func (c *Client) read() {
	var err error
	for {
		var m clientMsg
		if err = c.st.ReadObject(&m); err != nil {
			break
		}
		hasID := len(m.ID) > 0 && string(m.ID) != "null"
		switch {
		case m.Method != "":
			if hasID {
				if err = c.st.WriteObject(map[string]any{
					"jsonrpc": "2.0", "id": m.ID,
					"error": map[string]any{"code": codeMethodNotFound, "message": "method not found"},
				}); err != nil {
					break
				}
			}
		case hasID:
			var id uint64
			if json.Unmarshal(m.ID, &id) != nil {
				continue
			}
			c.mu.Lock()
			ch := c.pend[id]
			delete(c.pend, id)
			c.mu.Unlock()
			if ch == nil {
				continue // unknown, late or repeated reply
			}
			if m.Error != nil {
				ch <- clientReply{err: m.Error}
			} else {
				ch <- clientReply{result: m.Result}
			}
		}
		if err != nil {
			break
		}
	}
	c.finish(jsonrpc2.ErrClosed)
}

// finish ends the client once: every waiting call fails with err.
func (c *Client) finish(err error) {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return
	}
	c.err = err
	for id, ch := range c.pend {
		delete(c.pend, id)
		ch <- clientReply{err: err} // buffered; one send per entry
	}
	close(c.done)
	c.mu.Unlock()
	_ = c.st.Close()
}

func (c *Client) forget(id uint64) {
	c.mu.Lock()
	delete(c.pend, id)
	c.mu.Unlock()
}

// Call sends a request and decodes its result into result (nil to skip).
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	ch := make(chan clientReply, 1)
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return err
	}
	c.seq++
	id := c.seq
	c.pend[id] = ch
	c.mu.Unlock()
	msg := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	if err := c.st.WriteObject(msg); err != nil {
		c.forget(id)
		return err
	}
	select {
	case <-ctx.Done():
		c.forget(id)
		return ctx.Err()
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		if result == nil || len(r.result) == 0 {
			return nil
		}
		return json.Unmarshal(r.result, result)
	}
}

// Notify sends a notification.
func (c *Client) Notify(ctx context.Context, method string, params any) error {
	c.mu.Lock()
	err := c.err
	c.mu.Unlock()
	if err != nil {
		return err
	}
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	return c.st.WriteObject(msg)
}

// Done is closed when the channel ends.
func (c *Client) Done() <-chan struct{} { return c.done }

// Close closes the channel and fails every call still waiting.
func (c *Client) Close() error {
	c.finish(jsonrpc2.ErrClosed)
	return nil
}

// Initialize runs MCP's initialize handshake and returns the server's info.
func (c *Client) Initialize(ctx context.Context) (map[string]any, error) {
	var res struct {
		ServerInfo map[string]any `json:"serverInfo"`
	}
	err := c.Call(ctx, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "deck-peer", "version": "1"},
	}, &res)
	if err != nil {
		return nil, err
	}
	return res.ServerInfo, c.Notify(ctx, "notifications/initialized", map[string]any{})
}

// ListTools returns the server's tools.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	var res struct {
		Tools []Tool `json:"tools"`
	}
	err := c.Call(ctx, "tools/list", map[string]any{}, &res)
	return res.Tools, err
}

// CallTool calls a tool with args (any JSON-marshalable value).
func (c *Client) CallTool(ctx context.Context, name string, args any) (ToolResult, error) {
	var res ToolResult
	err := c.Call(ctx, "tools/call", map[string]any{"name": name, "arguments": args}, &res)
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
	err := c.Call(ctx, "ping", nil, &res)
	return time.Since(start), err
}
