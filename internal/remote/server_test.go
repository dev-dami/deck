package remote

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/remote/auth"
	"github.com/dev-dami/deck/internal/remote/wire"
)

type fakeBackend struct {
	mu        sync.Mutex
	sent      []sentMsg
	mode      string
	modes     []string
	approvals []wire.Approval
	answered  map[string]string
	events    chan Event
	cancelled []string
	msgs      []wire.Message
	edits     []string
	editErr   error
	sessions  []wire.Session
}

type sentMsg struct {
	Session, Text string
	Images        []Image
}

func newFake() *fakeBackend {
	return &fakeBackend{mode: wire.ModeAsk, answered: map[string]string{}, events: make(chan Event, 64)}
}

func (f *fakeBackend) Info() Info {
	return Info{Version: "t", Project: "proj", Cwd: "/w", Kinds: []string{"claude"}}
}
func (f *fakeBackend) Sessions(context.Context) ([]wire.Session, error) {
	if f.sessions != nil {
		return f.sessions, nil
	}
	return []wire.Session{{ID: "s1", Title: "One"}}, nil
}
func (f *fakeBackend) EditMessage(_ context.Context, sid, mid, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edits = append(f.edits, sid+"/"+mid+"/"+text)
	return f.editErr
}
func (f *fakeBackend) CreateSession(_ context.Context, title string) (wire.Session, error) {
	return wire.Session{ID: "new", Title: title}, nil
}
func (f *fakeBackend) RenameSession(context.Context, string, string) error { return nil }
func (f *fakeBackend) DeleteSession(_ context.Context, id string) error {
	if id == "ghost" {
		return ErrNotFound
	}
	return nil
}
func (f *fakeBackend) Messages(context.Context, string) ([]wire.Message, error) { return f.msgs, nil }
func (f *fakeBackend) Send(_ context.Context, sid, text string, images []Image) error {
	f.mu.Lock()
	f.sent = append(f.sent, sentMsg{sid, text, images})
	f.mu.Unlock()
	return nil
}
func (f *fakeBackend) Cancel(sid string) {
	f.mu.Lock()
	f.cancelled = append(f.cancelled, sid)
	f.mu.Unlock()
}
func (f *fakeBackend) Interrupt(string, string) error   { return nil }
func (f *fakeBackend) Continue(string) error            { return nil }
func (f *fakeBackend) Queue(string) []wire.QueuedItem   { return nil }
func (f *fakeBackend) Steer(string) error               { return nil }
func (f *fakeBackend) CancelQueued(string) error        { return nil }
func (f *fakeBackend) Roster(string) []wire.AgentLine   { return nil }
func (f *fakeBackend) SetModel(_, _, _, _ string) error { return nil }
func (f *fakeBackend) Models(context.Context, string, string) ([]wire.ModelChoice, error) {
	return []wire.ModelChoice{{ID: "opus", Label: "Opus"}}, nil
}
func (f *fakeBackend) Mode() string { f.mu.Lock(); defer f.mu.Unlock(); return f.mode }
func (f *fakeBackend) SetMode(m string) error {
	f.mu.Lock()
	f.mode = m
	f.modes = append(f.modes, m)
	f.mu.Unlock()
	return nil
}
func (f *fakeBackend) Approvals() []wire.Approval { return f.approvals }
func (f *fakeBackend) Answer(id, decision string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id == "gone" {
		return false, nil
	}
	if id == "nope" {
		return false, ErrNotFound
	}
	f.answered[id] = decision
	return true, nil
}
func (f *fakeBackend) Changes(context.Context, string) ([]wire.ChangedFile, error) { return nil, nil }
func (f *fakeBackend) Events(context.Context) <-chan Event                         { return f.events }

type rig struct {
	t     *testing.T
	be    *fakeBackend
	store *auth.Store
	srv   *Server
	ts    *httptest.Server
}

func newRig(t *testing.T, opts Options) *rig {
	t.Helper()
	store, err := auth.New(t.TempDir())
	require.NoError(t, err)
	be := newFake()
	if opts.Static == nil {
		opts.Static = fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}, "app.js": {Data: []byte("js")}}
	}
	srv := New(be, store, opts)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.Run(ctx)
	return &rig{t: t, be: be, store: store, srv: srv, ts: ts}
}

func (r *rig) token() string {
	r.t.Helper()
	code, err := r.store.NewCode()
	require.NoError(r.t, err)
	tok, _, err := r.store.Pair(code, "Pixel", "127.0.0.1")
	require.NoError(r.t, err)
	return tok
}

func (r *rig) wsURL() string { return "ws" + strings.TrimPrefix(r.ts.URL, "http") + "/api/v1/ws" }

type client struct {
	t  *testing.T
	ws *websocket.Conn
	id int
}

func (r *rig) dial(token string, resume *wire.Resume) (*client, wire.Frame) {
	r.t.Helper()
	ws, _, err := websocket.DefaultDialer.Dial(r.wsURL(), nil)
	require.NoError(r.t, err)
	r.t.Cleanup(func() { ws.Close() })
	require.NoError(r.t, ws.WriteJSON(wire.Frame{T: wire.FrameHello, V: wire.ProtocolVersion, Token: token, Resume: resume}))
	c := &client{t: r.t, ws: ws}
	return c, c.read()
}

func (c *client) read() wire.Frame {
	c.t.Helper()
	_ = c.ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	var f wire.Frame
	require.NoError(c.t, c.ws.ReadJSON(&f))
	return f
}

// call sends a request and returns its response, skipping interleaved events.
func (c *client) call(method string, params any) wire.Frame {
	c.t.Helper()
	c.id++
	id := string(rune('a'+c.id)) + "x"
	p, err := json.Marshal(params)
	require.NoError(c.t, err)
	require.NoError(c.t, c.ws.WriteJSON(wire.Frame{T: wire.FrameReq, ID: id, M: method, P: p}))
	for {
		f := c.read()
		if f.T == wire.FrameRes && f.ID == id {
			return f
		}
	}
}

func (c *client) nextEvent(name string) wire.Frame {
	c.t.Helper()
	for {
		f := c.read()
		if f.T == wire.FrameEv && f.E == name {
			return f
		}
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
	require.False(t, *f.OK)
	require.NotNil(t, f.Err)
	return f.Err.Code
}

func closeCode(t *testing.T, ws *websocket.Conn) int {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _, err := ws.ReadMessage()
	var ce *websocket.CloseError
	require.ErrorAs(t, err, &ce)
	return ce.Code
}

func post(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestPairHappyPathThenWelcome(t *testing.T) {
	r := newRig(t, Options{})
	url, err := r.srv.PairingURL(r.ts.URL)
	require.NoError(t, err)
	code := url[strings.Index(url, "#pair=")+len("#pair="):]

	resp := post(t, r.ts.URL+"/api/v1/pair", wire.PairRequest{Code: code, DeviceName: "Pixel"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var pr wire.PairResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&pr))
	require.NotEmpty(t, pr.Token)

	_, welcome := r.dial(pr.Token, nil)
	require.Equal(t, wire.FrameWelcome, welcome.T)
	require.Equal(t, "proj", welcome.Server.Project)
	require.Equal(t, "Pixel", welcome.Device.Name)
	require.False(t, welcome.Server.AllowYolo)
	require.Equal(t, r.srv.Hub().Epoch(), welcome.Epoch)
}

func TestPairingURLKeepsCodeInFragment(t *testing.T) {
	r := newRig(t, Options{})
	u, err := r.srv.PairingURL("http://h:1/")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(u, "http://h:1/#pair="), u)
}

func TestPairCodeIsSingleUse(t *testing.T) {
	r := newRig(t, Options{})
	u, _ := r.srv.PairingURL(r.ts.URL)
	code := u[strings.Index(u, "#pair=")+6:]
	require.Equal(t, http.StatusOK, post(t, r.ts.URL+"/api/v1/pair", wire.PairRequest{Code: code}).StatusCode)
	require.Equal(t, http.StatusUnauthorized, post(t, r.ts.URL+"/api/v1/pair", wire.PairRequest{Code: code}).StatusCode)
}

func TestPairBadBody(t *testing.T) {
	r := newRig(t, Options{})
	resp, err := http.Post(r.ts.URL+"/api/v1/pair", "application/json", strings.NewReader("{nope"))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestPairLockoutAfterRepeatedFailures(t *testing.T) {
	r := newRig(t, Options{})
	for range 5 {
		require.Equal(t, http.StatusUnauthorized, post(t, r.ts.URL+"/api/v1/pair", wire.PairRequest{Code: "bad"}).StatusCode)
	}
	require.Equal(t, http.StatusTooManyRequests, post(t, r.ts.URL+"/api/v1/pair", wire.PairRequest{Code: "bad"}).StatusCode)
}

func TestBadTokenClosesWith4401(t *testing.T) {
	r := newRig(t, Options{})
	ws, _, err := websocket.DefaultDialer.Dial(r.wsURL(), nil)
	require.NoError(t, err)
	defer ws.Close()
	require.NoError(t, ws.WriteJSON(wire.Frame{T: wire.FrameHello, Token: "x.y"}))
	require.Equal(t, wire.CloseUnauthorized, closeCode(t, ws))
}

func TestFirstFrameMustBeHello(t *testing.T) {
	r := newRig(t, Options{})
	ws, _, err := websocket.DefaultDialer.Dial(r.wsURL(), nil)
	require.NoError(t, err)
	defer ws.Close()
	require.NoError(t, ws.WriteJSON(wire.Frame{T: wire.FrameReq, ID: "1", M: wire.MSessionsList}))
	require.Equal(t, wire.CloseUnauthorized, closeCode(t, ws))
}

func TestHelloDeadline(t *testing.T) {
	r := newRig(t, Options{})
	ws, _, err := websocket.DefaultDialer.Dial(r.wsURL(), nil)
	require.NoError(t, err)
	defer ws.Close()
	_ = ws.SetReadDeadline(time.Now().Add(helloDeadline + 3*time.Second))
	_, _, err = ws.ReadMessage()
	require.Error(t, err, "server must hang up on a silent client")
}

func TestUnknownHostIsRefused(t *testing.T) {
	r := newRig(t, Options{})
	req, _ := http.NewRequest("GET", r.ts.URL+"/api/v1/health", nil)
	req.Host = "evil.example"
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusMisdirectedRequest, resp.StatusCode)
}

func TestAddedHostIsAccepted(t *testing.T) {
	r := newRig(t, Options{Hosts: []string{"phone.trycloudflare.com"}})
	r.srv.AddHost("192.168.1.9:7878")
	for _, h := range []string{"phone.trycloudflare.com", "192.168.1.9:7878"} {
		req, _ := http.NewRequest("GET", r.ts.URL+"/api/v1/health", nil)
		req.Host = h
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, h)
	}
}

func TestCrossOriginSocketIsRefused(t *testing.T) {
	r := newRig(t, Options{})
	h := http.Header{"Origin": {"https://evil.example"}}
	_, resp, err := websocket.DefaultDialer.Dial(r.wsURL(), h)
	require.Error(t, err)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestSameOriginSocketIsAccepted(t *testing.T) {
	r := newRig(t, Options{})
	h := http.Header{"Origin": {r.ts.URL}}
	ws, _, err := websocket.DefaultDialer.Dial(r.wsURL(), h)
	require.NoError(t, err)
	ws.Close()
}

func TestSecurityHeaders(t *testing.T) {
	r := newRig(t, Options{})
	resp, err := http.Get(r.ts.URL + "/api/v1/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Contains(t, resp.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'")
	require.Equal(t, "no-referrer", resp.Header.Get("Referrer-Policy"))
	require.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	require.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
}

func TestHealthIsMinimal(t *testing.T) {
	r := newRig(t, Options{})
	resp, err := http.Get(r.ts.URL + "/api/v1/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	var h map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&h))
	require.Equal(t, map[string]any{"ok": true, "v": float64(1)}, h)
}

func TestStaticServesAppShellAndRoutes(t *testing.T) {
	r := newRig(t, Options{})
	get := func(p string) (int, string) {
		resp, err := http.Get(r.ts.URL + p)
		require.NoError(t, err)
		defer resp.Body.Close()
		var b bytes.Buffer
		_, _ = b.ReadFrom(resp.Body)
		return resp.StatusCode, b.String()
	}
	code, body := get("/")
	require.Equal(t, 200, code)
	require.Contains(t, body, "app")
	code, body = get("/app.js")
	require.Equal(t, 200, code)
	require.Equal(t, "js", body)
	code, body = get("/session/s1")
	require.Equal(t, 200, code, "client routes fall back to the shell")
	require.Contains(t, body, "app")
	code, _ = get("/missing.js")
	require.Equal(t, 404, code)
	code, _ = get("/api/v1/nothing")
	require.Equal(t, 404, code)
}

func TestStaticWithoutBuiltClient(t *testing.T) {
	r := newRig(t, Options{Static: fstest.MapFS{}})
	resp, err := http.Get(r.ts.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

func TestChatSendAndIdempotency(t *testing.T) {
	r := newRig(t, Options{})
	c, _ := r.dial(r.token(), nil)
	send := func(key string) wire.Frame {
		return c.call(wire.MChatSend, wire.ChatSendReq{SessionID: "s1", Text: "hi", IdempotencyKey: key})
	}
	okRes(t, send("k1"))
	okRes(t, send("k1"))
	okRes(t, send("k2"))
	require.Len(t, r.be.sent, 2, "a retried key posts once")
}

func TestChatSendValidation(t *testing.T) {
	r := newRig(t, Options{})
	c, _ := r.dial(r.token(), nil)
	require.Equal(t, wire.CodeInvalid, errCode(t, c.call(wire.MChatSend, wire.ChatSendReq{SessionID: "s1"})))
	require.Equal(t, wire.CodeInvalid, errCode(t, c.call(wire.MChatSend, wire.ChatSendReq{Text: "x"})))
	bad := wire.ChatSendReq{SessionID: "s1", Images: []wire.ImageUpload{{Name: "a.txt", Mime: "text/plain", Data: "AAAA"}}}
	require.Equal(t, wire.CodeInvalid, errCode(t, c.call(wire.MChatSend, bad)))
	bad.Images[0] = wire.ImageUpload{Name: "a.png", Mime: "image/png", Data: "!!!"}
	require.Equal(t, wire.CodeInvalid, errCode(t, c.call(wire.MChatSend, bad)))
}

func TestChatSendImage(t *testing.T) {
	r := newRig(t, Options{})
	c, _ := r.dial(r.token(), nil)
	req := wire.ChatSendReq{SessionID: "s1", Images: []wire.ImageUpload{
		{Name: "../../etc/pic.png", Mime: "image/png", Data: base64.StdEncoding.EncodeToString([]byte("PNG"))},
	}}
	okRes(t, c.call(wire.MChatSend, req))
	require.Len(t, r.be.sent, 1)
	require.Equal(t, []Image{{Name: "pic.png", Mime: "image/png", Data: []byte("PNG")}}, r.be.sent[0].Images)
}

func TestChatSendOversizedImage(t *testing.T) {
	r := newRig(t, Options{})
	c, _ := r.dial(r.token(), nil)
	big := base64.StdEncoding.EncodeToString(make([]byte, wire.MaxImageBytes+1))
	req := wire.ChatSendReq{SessionID: "s1", Images: []wire.ImageUpload{{Name: "a.png", Mime: "image/png", Data: big}}}
	require.Equal(t, wire.CodeTooLarge, errCode(t, c.call(wire.MChatSend, req)))
	require.Empty(t, r.be.sent)
}

func TestYoloGate(t *testing.T) {
	r := newRig(t, Options{})
	c, _ := r.dial(r.token(), nil)
	require.Equal(t, wire.CodeYoloDisabled, errCode(t, c.call(wire.MModeSet, wire.ModeBody{Mode: wire.ModeYolo})))
	require.Empty(t, r.be.modes)
	okRes(t, c.call(wire.MModeSet, wire.ModeBody{Mode: wire.ModePlan}))
	okRes(t, c.call(wire.MModeSet, wire.ModeBody{Mode: wire.ModeAsk}))
	require.Equal(t, []string{wire.ModePlan, wire.ModeAsk}, r.be.modes)
	require.Equal(t, wire.CodeInvalid, errCode(t, c.call(wire.MModeSet, wire.ModeBody{Mode: "turbo"})))
}

func TestYoloAllowedWithFlag(t *testing.T) {
	r := newRig(t, Options{AllowYolo: true})
	c, welcome := r.dial(r.token(), nil)
	require.True(t, welcome.Server.AllowYolo)
	okRes(t, c.call(wire.MModeSet, wire.ModeBody{Mode: wire.ModeYolo}))
	require.Equal(t, []string{wire.ModeYolo}, r.be.modes)
}

func TestApprovalsAnswer(t *testing.T) {
	r := newRig(t, Options{})
	c, _ := r.dial(r.token(), nil)

	var res wire.ApprovalsAnswerRes
	require.NoError(t, json.Unmarshal(okRes(t, c.call(wire.MApprovalsAnswer, wire.ApprovalsAnswerReq{ID: "p1", Decision: wire.DecisionAllowSession})), &res))
	require.True(t, res.Resolved)
	require.Equal(t, wire.DecisionAllowSession, r.be.answered["p1"])

	require.NoError(t, json.Unmarshal(okRes(t, c.call(wire.MApprovalsAnswer, wire.ApprovalsAnswerReq{ID: "gone", Decision: wire.DecisionAllow})), &res))
	require.False(t, res.Resolved, "another UI answered first")

	require.Equal(t, wire.CodeNotFound, errCode(t, c.call(wire.MApprovalsAnswer, wire.ApprovalsAnswerReq{ID: "nope", Decision: wire.DecisionDeny})))
	require.Equal(t, wire.CodeInvalid, errCode(t, c.call(wire.MApprovalsAnswer, wire.ApprovalsAnswerReq{ID: "p1", Decision: "maybe"})))
}

func TestListsAreArraysNotNull(t *testing.T) {
	r := newRig(t, Options{})
	c, _ := r.dial(r.token(), nil)
	for _, m := range []string{wire.MApprovalsList} {
		require.JSONEq(t, `[]`, string(okRes(t, c.call(m, struct{}{}))), m)
	}
	require.JSONEq(t, `[]`, string(okRes(t, c.call(wire.MQueueList, wire.SessionIDReq{SessionID: "s1"}))))
	require.JSONEq(t, `[]`, string(okRes(t, c.call(wire.MRosterGet, wire.SessionIDReq{SessionID: "s1"}))))
	require.JSONEq(t, `[]`, string(okRes(t, c.call(wire.MChangesGet, wire.ChangesGetReq{SessionID: "s1"}))))
}

func TestUnknownMethodAndBadParams(t *testing.T) {
	r := newRig(t, Options{})
	c, _ := r.dial(r.token(), nil)
	require.Equal(t, wire.CodeNotFound, errCode(t, c.call("shell.exec", struct{}{})))
	c.id++
	require.NoError(t, c.ws.WriteJSON(wire.Frame{T: wire.FrameReq, ID: "bad", M: wire.MSessionsRename, P: json.RawMessage(`"oops"`)}))
	for {
		f := c.read()
		if f.ID == "bad" {
			require.Equal(t, wire.CodeInvalid, errCode(t, f))
			break
		}
	}
}

func TestNotFoundMapsFromBackend(t *testing.T) {
	r := newRig(t, Options{})
	c, _ := r.dial(r.token(), nil)
	require.Equal(t, wire.CodeNotFound, errCode(t, c.call(wire.MSessionsDelete, wire.SessionRef{ID: "ghost"})))
}

func TestMessagesListPaging(t *testing.T) {
	r := newRig(t, Options{})
	for i := 1; i <= 5; i++ {
		r.be.msgs = append(r.be.msgs, wire.Message{ID: string(rune('a' + i)), CreatedAt: int64(i * 10)})
	}
	c, _ := r.dial(r.token(), nil)

	var page wire.MessagesListRes
	require.NoError(t, json.Unmarshal(okRes(t, c.call(wire.MMessagesList, wire.MessagesListReq{SessionID: "s1", Limit: 2})), &page))
	require.True(t, page.More)
	require.Len(t, page.Messages, 2)
	require.Equal(t, int64(40), page.Messages[0].CreatedAt, "the newest page, oldest first")

	require.NoError(t, json.Unmarshal(okRes(t, c.call(wire.MMessagesList, wire.MessagesListReq{SessionID: "s1", Limit: 2, Before: 40})), &page))
	require.Equal(t, int64(20), page.Messages[0].CreatedAt)
	require.True(t, page.More)

	require.NoError(t, json.Unmarshal(okRes(t, c.call(wire.MMessagesList, wire.MessagesListReq{SessionID: "s1", Limit: 2, Before: 20})), &page))
	require.Len(t, page.Messages, 1)
	require.False(t, page.More)

	require.Equal(t, wire.CodeInvalid, errCode(t, c.call(wire.MMessagesList, wire.MessagesListReq{})))
}

func TestEventsReachFocusedClientOnly(t *testing.T) {
	r := newRig(t, Options{})
	a, _ := r.dial(r.token(), nil)
	b, _ := r.dial(r.token(), nil)
	okRes(t, a.call(wire.MSessionsFocus, wire.SessionRef{ID: "s1"}))
	okRes(t, b.call(wire.MSessionsFocus, wire.SessionRef{ID: "s2"}))

	r.be.events <- Event{Name: wire.EvMessageUpdated, SessionID: "s1", Scoped: true, Payload: wire.Message{ID: "m1", SessionID: "s1"}}
	r.be.events <- Event{Name: wire.EvBusy, SessionID: "s1", Payload: map[string]any{"session_id": "s1", "busy": true}}

	require.Equal(t, "m1", func() string {
		var m wire.Message
		require.NoError(t, json.Unmarshal(a.nextEvent(wire.EvMessageUpdated).P, &m))
		return m.ID
	}())
	// b never gets the scoped row: its next event is the unscoped busy one.
	f := b.read()
	require.Equal(t, wire.EvBusy, f.E)
}

func TestApprovalEventsReachEveryone(t *testing.T) {
	r := newRig(t, Options{})
	a, _ := r.dial(r.token(), nil)
	r.be.events <- Event{Name: wire.EvApprovalRequested, SessionID: "s9", Payload: wire.Approval{ID: "p1", SessionID: "s9", Kind: wire.ApprovalCommand}}
	var ap wire.Approval
	require.NoError(t, json.Unmarshal(a.nextEvent(wire.EvApprovalRequested).P, &ap))
	require.Equal(t, "p1", ap.ID)
}

func TestResumeReplaysAfterReconnect(t *testing.T) {
	r := newRig(t, Options{})
	tok := r.token()
	a, w := r.dial(tok, nil)
	epoch := w.Epoch
	r.be.events <- Event{Name: wire.EvBusy, Payload: map[string]any{"busy": true}}
	first := a.nextEvent(wire.EvBusy)
	a.ws.Close()

	r.be.events <- Event{Name: wire.EvNotice, Payload: map[string]any{"text": "while away"}}
	require.Eventually(t, func() bool { return r.srv.Hub().Seq() == first.Seq+1 }, 2*time.Second, 10*time.Millisecond)

	b, w2 := r.dial(tok, &wire.Resume{Epoch: epoch, Seq: first.Seq})
	require.True(t, w2.Replay)
	f := b.read()
	require.Equal(t, wire.EvNotice, f.E)
}

func TestResumeWithWrongEpochResyncs(t *testing.T) {
	r := newRig(t, Options{})
	c, w := r.dial(r.token(), &wire.Resume{Epoch: "old", Seq: 7})
	require.Equal(t, wire.FrameWelcome, w.T)
	require.Equal(t, wire.FrameResync, c.read().T)
}

func TestRevokeClosesTheDevicesSocket(t *testing.T) {
	r := newRig(t, Options{})
	tokA := r.token()
	tokB := r.token()
	a, wa := r.dial(tokA, nil)
	b, _ := r.dial(tokB, nil)

	okRes(t, b.call(wire.MDevicesRevoke, wire.DevicesRevokeReq{ID: wa.Device.ID}))
	require.Equal(t, wire.CloseRevoked, closeCodeAfterEvent(t, a))

	var devs []wire.Device
	require.NoError(t, json.Unmarshal(okRes(t, b.call(wire.MDevicesList, struct{}{})), &devs))
	require.Len(t, devs, 1)
	require.True(t, devs[0].Current)

	_, _, err := websocket.DefaultDialer.Dial(r.wsURL(), nil)
	require.NoError(t, err)
	ws, _, _ := websocket.DefaultDialer.Dial(r.wsURL(), nil)
	require.NoError(t, ws.WriteJSON(wire.Frame{T: wire.FrameHello, Token: tokA}))
	require.Equal(t, wire.CloseUnauthorized, closeCode(t, ws), "the revoked token no longer authenticates")
}

func closeCodeAfterEvent(t *testing.T, c *client) int {
	t.Helper()
	_ = c.ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, _, err := c.ws.ReadMessage()
		var ce *websocket.CloseError
		if err != nil {
			require.ErrorAs(t, err, &ce)
			return ce.Code
		}
	}
}

func TestRevokeFromAnotherProcessIsEnforced(t *testing.T) {
	old := revokePoll
	revokePoll = 50 * time.Millisecond
	t.Cleanup(func() { revokePoll = old })
	r := newRig(t, Options{})
	tok := r.token()
	c, w := r.dial(tok, nil)
	require.NoError(t, r.store.Revoke(w.Device.ID)) // `deck serve revoke` in another process
	require.Equal(t, wire.CloseRevoked, closeCodeAfterEvent(t, c))
}

func TestMalformedFramesDoNotKillTheSocket(t *testing.T) {
	r := newRig(t, Options{})
	c, _ := r.dial(r.token(), nil)
	require.NoError(t, c.ws.WriteJSON(wire.Frame{T: "junk"}))
	require.NoError(t, c.ws.WriteJSON(wire.Frame{T: wire.FrameReq})) // no id
	okRes(t, c.call(wire.MSessionsList, struct{}{}))
}

func TestOversizedFrameClosesTheSocket(t *testing.T) {
	r := newRig(t, Options{})
	c, _ := r.dial(r.token(), nil)
	huge := make([]byte, wire.MaxFrameBytes+1024)
	for i := range huge {
		huge[i] = 'a'
	}
	_ = c.ws.WriteMessage(websocket.TextMessage, huge)
	_ = c.ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _, err := c.ws.ReadMessage()
	require.Error(t, err)
}

func TestClientAddrTrustsProxyHeaderOnlyFromLoopback(t *testing.T) {
	s := New(newFake(), mustStore(t), Options{TrustProxyIP: true})
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("CF-Connecting-IP", "203.0.113.9")
	require.Equal(t, "203.0.113.9", s.clientAddr(req))

	req.RemoteAddr = "198.51.100.4:5555"
	require.Equal(t, "198.51.100.4", s.clientAddr(req), "a remote peer cannot spoof its address")

	off := New(newFake(), mustStore(t), Options{})
	req.RemoteAddr = "127.0.0.1:5555"
	require.Equal(t, "127.0.0.1", off.clientAddr(req))
}

func mustStore(t *testing.T) *auth.Store {
	t.Helper()
	s, err := auth.New(t.TempDir())
	require.NoError(t, err)
	return s
}

func TestSessionScopedMethodsTakeSessionID(t *testing.T) {
	r := newRig(t, Options{})
	c, _ := r.dial(r.token(), nil)
	okRes(t, c.call(wire.MChatCancel, wire.SessionIDReq{SessionID: "s1"}))
	okRes(t, c.call(wire.MChatContinue, wire.SessionIDReq{SessionID: "s1"}))
	require.Equal(t, []string{"s1"}, r.be.cancelled)
}
