package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/pkg/remote/auth"
	"github.com/dev-dami/deck/pkg/remote/remotetest"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// clock is a settable time source shared by the store, limits and server.
type clock struct{ t atomic.Pointer[time.Time] }

func newClock() *clock {
	c := &clock{}
	c.set(time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC))
	return c
}

func (c *clock) set(t time.Time)         { c.t.Store(&t) }
func (c *clock) now() time.Time          { return *c.t.Load() }
func (c *clock) advance(d time.Duration) { c.set(c.now().Add(d)) }

type rig struct {
	t     *testing.T
	be    *remotetest.Fake
	store *auth.Store
	srv   *Server
	ts    *httptest.Server
	clk   *clock
}

func newRig(t *testing.T) *rig {
	t.Helper()
	clk := newClock()
	store, err := auth.Open(filepath.Join(t.TempDir(), "remote"), auth.WithClock(clk.now))
	require.NoError(t, err)
	be := remotetest.New()
	opts := Options{
		Version: "t", Now: clk.now,
		Static: fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}, "app.js": {Data: []byte("js")}},
	}
	srv := New(be, store, opts)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.Run(ctx)
	return &rig{t: t, be: be, store: store, srv: srv, ts: ts, clk: clk}
}

// token pairs a device and returns its token and record.
func (r *rig) token(name string) (string, auth.Device) {
	r.t.Helper()
	code, err := r.store.Arm()
	require.NoError(r.t, err)
	tok, dev, err := r.store.Pair(code.Code, name)
	require.NoError(r.t, err)
	return tok, dev
}

func (r *rig) wsURL() string { return "ws" + strings.TrimPrefix(r.ts.URL, "http") + "/api/v1/ws" }

// connect opens a socket with the page's Origin and sends nothing.
func (r *rig) connect(origin string) (*websocket.Conn, *http.Response, error) {
	r.t.Helper()
	h := http.Header{}
	if origin != "" {
		h.Set("Origin", origin)
	}
	ws, resp, err := websocket.DefaultDialer.Dial(r.wsURL(), h)
	if ws != nil {
		r.t.Cleanup(func() { ws.Close() })
	}
	return ws, resp, err
}

type client struct {
	t  *testing.T
	ws *websocket.Conn
	id int
}

// dial connects, sends a hello and returns the first frame back.
func (r *rig) dial(token string, resume *wire.Resume) (*client, wire.Frame) {
	r.t.Helper()
	ws, _, err := r.connect(r.ts.URL)
	require.NoError(r.t, err)
	require.NoError(r.t, ws.WriteJSON(wire.Frame{T: wire.FrameHello, Hello: &wire.Hello{V: wire.Protocol, Token: token, Resume: resume}}))
	c := &client{t: r.t, ws: ws}
	return c, c.read()
}

// welcome dials and requires a welcome frame.
func (r *rig) welcome(token string, resume *wire.Resume) (*client, wire.Frame) {
	r.t.Helper()
	c, f := r.dial(token, resume)
	require.Equal(r.t, wire.FrameWelcome, f.T, "frame %+v", f)
	return c, f
}

func (c *client) read() wire.Frame {
	c.t.Helper()
	_ = c.ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	var f wire.Frame
	require.NoError(c.t, c.ws.ReadJSON(&f))
	return f
}

// call sends a request and returns its response, skipping events.
func (c *client) call(method string, params any) wire.Frame {
	c.t.Helper()
	c.id++
	id := "r" + string(rune('a'+c.id%26)) + string(rune('a'+c.id/26))
	var p json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		require.NoError(c.t, err)
		p = b
	}
	require.NoError(c.t, c.ws.WriteJSON(wire.Frame{T: wire.FrameReq, ID: id, M: method, P: p}))
	for {
		f := c.read()
		if f.T == wire.FrameRes && f.ID == id {
			return f
		}
	}
}

// event reads frames until one event named name arrives.
func (c *client) event(name string) wire.Frame {
	c.t.Helper()
	for {
		f := c.read()
		if f.T == wire.FrameEv && f.Ev != nil && f.Ev.E == name {
			return f
		}
	}
}

// closeCode reads until the server closes and returns the close code and
// reason.
func (c *client) closeCode() (int, string) {
	c.t.Helper()
	return closeCode(c.t, c.ws)
}

func closeCode(t *testing.T, ws *websocket.Conn) (int, string) {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, _, err := ws.ReadMessage()
		if err == nil {
			continue
		}
		var ce *websocket.CloseError
		require.ErrorAs(t, err, &ce, "expected a close, got %v", err)
		return ce.Code, ce.Text
	}
}

func okRes(t *testing.T, f wire.Frame) json.RawMessage {
	t.Helper()
	require.NotNil(t, f.OK, "frame %+v", f)
	require.True(t, *f.OK, "error: %+v", f.Err)
	return f.R
}

func errCode(t *testing.T, f wire.Frame) string {
	t.Helper()
	require.NotNil(t, f.OK)
	require.False(t, *f.OK, "expected an error, got %s", f.R)
	require.NotNil(t, f.Err)
	return f.Err.Code
}

func unmarshal[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var v T
	require.NoError(t, json.Unmarshal(raw, &v))
	return v
}

// post sends a pair-style request with the given headers.
func (r *rig) post(path string, body []byte, headers map[string]string) *http.Response {
	r.t.Helper()
	req, err := http.NewRequest(http.MethodPost, r.ts.URL+path, bytes.NewReader(body))
	require.NoError(r.t, err)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(r.t, err)
	r.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// pair posts a correct pairing request for code.
func (r *rig) pair(code, name string) *http.Response {
	r.t.Helper()
	b, _ := json.Marshal(wire.PairRequest{Code: code, DeviceName: name})
	return r.post("/api/v1/pair", b, map[string]string{"Origin": r.ts.URL, "Content-Type": "application/json", "Sec-Fetch-Site": "same-origin"})
}

func pairError(t *testing.T, resp *http.Response) string {
	t.Helper()
	var pe wire.PairError
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&pe))
	return pe.Error
}
