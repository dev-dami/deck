package peer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/approve"
)

// recHandler records who called it and answers like StatusHandler.
type recHandler struct {
	StatusHandler
	mu      sync.Mutex
	callers []Caller
	block   chan struct{} // when set, Execute "block" waits on it
	panicOp string
}

func (h *recHandler) Execute(ctx context.Context, from Caller, op string, args json.RawMessage) (string, error) {
	h.mu.Lock()
	h.callers = append(h.callers, from)
	h.mu.Unlock()
	switch {
	case op == "block" && h.block != nil:
		select {
		case <-h.block:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		return "unblocked", nil
	case op == h.panicOp && op != "":
		panic("boom")
	case op == "echo":
		return string(args), nil
	}
	return h.StatusHandler.Execute(ctx, from, op, args)
}

func (h *recHandler) calls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.callers)
}

func (h *recHandler) last() Caller {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.callers) == 0 {
		return Caller{}
	}
	return h.callers[len(h.callers)-1]
}

func openInit(t *testing.T, c *Conn) *Client {
	t.Helper()
	cl, err := c.OpenMCP(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cl.Close() })
	if _, err := cl.Initialize(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	return cl
}

func TestTwoPeersBothDirections(t *testing.T) {
	leakCheck(t)
	ha := &recHandler{StatusHandler: StatusHandler{Name: "a", Version: "1.0", Started: time.Now().Add(-time.Hour)}}
	hb := &recHandler{StatusHandler: StatusHandler{Name: "b", Version: "2.0", Started: time.Now()}}
	inbound := make(chan *Conn, 1)
	a, b, _, pinA := pairUp(t, ServerOptions{Handler: ha, OnConn: func(c *Conn) { inbound <- c }})

	conn := b.dial(t, pinA, hb)
	if conn.Peer().Name != "a" || conn.Peer().Fingerprint != a.id.Fingerprint() {
		t.Fatalf("dialer sees peer %+v", conn.Peer())
	}
	cl := openInit(t, conn)
	ctx := ctxT(t)

	tools, err := cl.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].Name != "search" || tools[1].Name != "execute" {
		t.Fatalf("tools/list = %+v, want exactly search and execute", tools)
	}

	res, err := cl.Execute(ctx, OpPeerStatus, nil)
	if err != nil || res.IsError {
		t.Fatalf("peer.status: %+v %v", res, err)
	}
	var st struct {
		Name          string `json:"name"`
		Version       string `json:"version"`
		UptimeSeconds int64  `json:"uptime_seconds"`
	}
	if err := json.Unmarshal([]byte(res.Text()), &st); err != nil {
		t.Fatalf("status %q: %v", res.Text(), err)
	}
	if st.Name != "a" || st.Version != "1.0" || st.UptimeSeconds < 3599 {
		t.Fatalf("status = %+v", st)
	}
	if got := ha.last(); got.Name != "b" || got.Fingerprint != b.id.Fingerprint() {
		t.Fatalf("handler saw caller %+v, want b", got)
	}

	res, err = cl.Execute(ctx, "git.status", nil)
	if err != nil || !res.IsError || !strings.Contains(res.Text(), "unknown op") {
		t.Fatalf("unknown op: %+v %v", res, err)
	}
	res, err = cl.CallTool(ctx, ToolExecute, map[string]any{})
	if err != nil || !res.IsError || !strings.Contains(res.Text(), "needs an \"op\"") {
		t.Fatalf("execute without op: %+v %v", res, err)
	}
	// Flattened args reach the handler like Deck's own execute.
	res, err = cl.CallTool(ctx, ToolExecute, map[string]any{"op": "echo", "x": 1})
	if err != nil || res.Text() != `{"x":1}` {
		t.Fatalf("flattened args: %+v %v", res, err)
	}
	res, err = cl.CallTool(ctx, ToolSearch, map[string]any{"query": ""})
	if err != nil || res.IsError || !strings.Contains(res.Text(), OpPeerStatus) {
		t.Fatalf("search: %+v %v", res, err)
	}
	if lat, err := cl.Ping(ctx); err != nil || lat < 0 {
		t.Fatalf("ping: %v %v", lat, err)
	}

	// Reverse: the listener calls the dialer over the same link.
	var in *Conn
	select {
	case in = <-inbound:
	case <-time.After(5 * time.Second):
		t.Fatal("no inbound conn")
	}
	if in.Peer().Name != "b" || in.Peer().Fingerprint != b.id.Fingerprint() {
		t.Fatalf("listener sees peer %+v", in.Peer())
	}
	rcl := openInit(t, in)
	res, err = rcl.Execute(ctx, OpPeerStatus, nil)
	if err != nil || res.IsError || !strings.Contains(res.Text(), `"name":"b"`) {
		t.Fatalf("reverse peer.status: %+v %v", res, err)
	}
	if got := hb.last(); got.Name != "a" {
		t.Fatalf("dialer's handler saw caller %+v, want a", got)
	}
	rtools, err := rcl.ListTools(ctx)
	if err != nil || len(rtools) != 2 {
		t.Fatalf("reverse tools/list: %+v %v", rtools, err)
	}
}

// TestToolsMatchLocalDeck checks the peer tools carry the same input schemas
// as Deck's local MCP server.
func TestToolsMatchLocalDeck(t *testing.T) {
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n")
	var out bytes.Buffer
	if err := approve.MCPServe(in, &out, "127.0.0.1:1", "", "x", false); err != nil {
		t.Fatal(err)
	}
	var local []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var resp struct {
			ID     int `json:"id"`
			Result struct {
				Tools []map[string]any `json:"tools"`
			} `json:"result"`
		}
		_ = json.Unmarshal([]byte(line), &resp)
		if resp.ID == 2 {
			local = resp.Result.Tools
		}
	}
	if len(local) != 2 {
		t.Fatalf("local tools: %v", local)
	}
	for i, tool := range Tools() {
		if local[i]["name"] != tool.Name {
			t.Fatalf("tool %d: local %v, peer %s", i, local[i]["name"], tool.Name)
		}
		want, _ := json.Marshal(local[i]["inputSchema"])
		got, _ := json.Marshal(tool.InputSchema)
		if !bytes.Equal(want, got) {
			t.Fatalf("%s schema differs:\nlocal %s\npeer  %s", tool.Name, want, got)
		}
	}
}

func TestAllowInboundLimitsOps(t *testing.T) {
	leakCheck(t)
	_, b, _, pinA := pairUp(t, ServerOptions{Caller: func(p Pinned) Caller {
		return Caller{Name: p.Name, Fingerprint: p.Fingerprint, AllowInbound: []string{"console.read"}}
	}})
	cl := openInit(t, b.dial(t, pinA, nil))
	res, err := cl.Execute(ctxT(t), OpPeerStatus, nil)
	if err != nil || !res.IsError || !strings.Contains(res.Text(), "not allowed") {
		t.Fatalf("disallowed op ran: %+v %v", res, err)
	}
}

func TestHandlerPanicAndBusyAndCancel(t *testing.T) {
	leakCheck(t)
	h := &recHandler{StatusHandler: StatusHandler{Name: "a"}, block: make(chan struct{}), panicOp: "crash"}
	_, b, _, pinA := pairUp(t, ServerOptions{Handler: h, Limits: Limits{MaxInFlight: 1, CallTimeout: 5 * time.Second}})
	cl := openInit(t, b.dial(t, pinA, nil))
	ctx := ctxT(t)

	res, err := cl.Execute(ctx, "crash", nil)
	if err != nil || !res.IsError || res.Text() != "internal error" {
		t.Fatalf("panic: %+v %v", res, err)
	}

	done := make(chan ToolResult, 1)
	go func() {
		r, err := cl.Execute(ctx, "block", nil)
		if err != nil {
			r = textResult(err.Error(), true)
		}
		done <- r
	}()
	// Wait until the blocking call holds the only slot.
	deadline := time.Now().Add(5 * time.Second)
	for h.calls() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("blocking call never reached the handler")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if r, err := cl.Execute(ctx, OpPeerStatus, nil); err == nil || !strings.Contains(err.Error(), "too many calls") {
		t.Fatalf("second call not refused as busy: %+v %v", r, err)
	}
	close(h.block)
	if r := <-done; r.Text() != "unblocked" {
		t.Fatalf("blocked call: %+v", r)
	}
	// The slot is free again.
	if res, err := cl.Execute(ctx, OpPeerStatus, nil); err != nil || res.IsError {
		t.Fatalf("after busy: %+v %v", res, err)
	}
}

func TestCallTimeout(t *testing.T) {
	leakCheck(t)
	h := &recHandler{StatusHandler: StatusHandler{Name: "a"}, block: make(chan struct{})}
	defer close(h.block)
	_, b, _, pinA := pairUp(t, ServerOptions{Handler: h, Limits: Limits{CallTimeout: 100 * time.Millisecond}})
	cl := openInit(t, b.dial(t, pinA, nil))
	res, err := cl.Execute(ctxT(t), "block", nil)
	if err != nil || !res.IsError || !strings.Contains(res.Text(), "cancelled") {
		t.Fatalf("timed out call: %+v %v", res, err)
	}
}

func TestRemovedPeerRefusedAtNextConnect(t *testing.T) {
	leakCheck(t)
	a, b, _, pinA := pairUp(t, ServerOptions{})
	c := b.dial(t, pinA, nil)
	c.Close()
	if _, err := a.store.Remove("b"); err != nil {
		t.Fatal(err)
	}
	_, err := Dial(ctxT(t), DialOptions{Identity: b.id, Peer: pinA, Logger: quietLog})
	if err == nil || !strings.Contains(err.Error(), "unable to authenticate") {
		t.Fatalf("removed peer connected: %v", err)
	}
}

func TestManagerOffStartsNothing(t *testing.T) {
	leakCheck(t)
	m, err := NewManager(ManagerOptions{Dir: t.TempDir(), Logger: quietLog})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Addr() != "" || len(m.Statuses()) != 0 {
		t.Fatal("disabled manager started something")
	}
	m.Stop()
	// No key is made either.
	if _, err := LoadIdentity(m.o.Dir); !errors.Is(err, ErrNoIdentity) {
		t.Fatalf("disabled manager made a key: %v", err)
	}
}
