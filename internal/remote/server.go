// Package remote serves Deck to a phone: a paired, authenticated JSON-RPC
// WebSocket over the same Workspace the TUI uses, and the embedded web client.
package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dev-dami/deck/internal/remote/auth"
	"github.com/dev-dami/deck/internal/remote/push"
	"github.com/dev-dami/deck/internal/remote/web"
	"github.com/dev-dami/deck/internal/remote/wire"
)

const (
	helloDeadline = 5 * time.Second
	writeDeadline = 10 * time.Second
	pingEvery     = 30 * time.Second
	idleTimeout   = 75 * time.Second
	idemTTL       = 10 * time.Minute
	maxInflight   = 8
)

// revokePoll is how often connections are checked against devices.json, so a
// revoke made by `deck serve revoke` in another process takes effect.
var revokePoll = 3 * time.Second

// Options configure a Server.
type Options struct {
	// AllowYolo lets a phone switch Deck into yolo mode. Off by default:
	// yolo from a phone is a remote shell.
	AllowYolo bool
	// Headless is true when no TUI shares the workspace.
	Headless bool
	// Tunnel names the tunnel in use ("cloudflare", "tailscale"), for display.
	Tunnel  string
	Version string
	// TrustProxyIP makes the server believe CF-Connecting-IP when the peer is
	// loopback, which is only true behind a local tunnel agent.
	TrustProxyIP bool
	// Hosts are extra Host header values to accept (LAN addresses, a tunnel's
	// hostname). localhost, 127.0.0.1 and ::1 are always accepted.
	Hosts []string
	// Static overrides the embedded client; tests use it.
	Static fs.FS
	// Push sends notifications to phones that opted in; nil turns push off.
	Push *push.Service
}

// Server is the remote-control endpoint.
type Server struct {
	be    Backend
	store *auth.Store
	lim   *auth.Limiter
	hub   *Hub
	opts  Options
	up    websocket.Upgrader

	mu    sync.Mutex
	hosts map[string]bool
	conns map[string]map[*conn]struct{} // by device id
	idem  map[string]time.Time
	push  *push.Service

	busySince map[string]time.Time // when each busy session started, for "finished" pushes
}

// New returns a Server over be, pairing devices through store.
func New(be Backend, store *auth.Store, opts Options) *Server {
	s := &Server{
		be: be, store: store, lim: auth.NewLimiter(nil), hub: NewHub(), opts: opts,
		hosts: map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true},
		conns: map[string]map[*conn]struct{}{},
		idem:  map[string]time.Time{},
		push:  opts.Push, busySince: map[string]time.Time{},
	}
	for _, h := range opts.Hosts {
		s.AddHost(h)
	}
	s.up = websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096, CheckOrigin: s.checkOrigin}
	return s
}

// Hub exposes the event hub, for tests and the TUI dialog.
func (s *Server) Hub() *Hub { return s.hub }

// AddHost accepts another Host header value (a tunnel hostname found after start).
func (s *Server) AddHost(h string) {
	s.mu.Lock()
	s.hosts[hostOnly(h)] = true
	s.mu.Unlock()
}

// PairingURL mints a one-time code and returns the link a QR should carry. The
// code rides in the fragment so no request line, proxy or tunnel log sees it.
func (s *Server) PairingURL(origin string) (string, error) {
	code, err := s.store.NewCode()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(origin, "/") + "/#pair=" + code, nil
}

// PairingDisabled reports whether the failed-attempt lockout has tripped.
func (s *Server) PairingDisabled() bool { return s.lim.Disabled() }

// Run feeds backend events to the hub and enforces revocations made from other
// processes, until ctx ends.
func (s *Server) Run(ctx context.Context) {
	events := s.be.Events(ctx)
	tick := time.NewTicker(revokePoll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			s.closeAll(1001, "server stopping")
			return
		case ev, ok := <-events:
			if !ok {
				s.closeAll(1011, "backend closed")
				return
			}
			s.hub.Publish(ev)
			s.notify(ev)
		case <-tick.C:
			s.dropRevoked()
		}
	}
}

func (s *Server) dropRevoked() {
	devs, err := s.store.List()
	if err != nil {
		return // a transient lock or read problem must not log everyone out
	}
	live := make(map[string]bool, len(devs))
	for _, d := range devs {
		live[d.ID] = true
	}
	if s.push != nil {
		s.push.Prune(func(id string) bool { return live[id] })
	}
	s.mu.Lock()
	var gone []string
	for id := range s.conns {
		if !live[id] {
			gone = append(gone, id)
		}
	}
	s.mu.Unlock()
	for _, id := range gone {
		s.kickDevice(id)
	}
}

// kickDevice closes every connection of a revoked device.
func (s *Server) kickDevice(id string) {
	if s.push != nil {
		s.push.RemoveDevice(id)
	}
	s.mu.Lock()
	cs := make([]*conn, 0, len(s.conns[id]))
	for c := range s.conns[id] {
		cs = append(cs, c)
	}
	s.mu.Unlock()
	for _, c := range cs {
		_ = c.write(wire.Frame{T: wire.FrameEv, E: wire.EvDeviceRevoked, P: json.RawMessage(`{}`)})
		c.closeWith(wire.CloseRevoked, "revoked")
	}
}

func (s *Server) closeAll(code int, text string) {
	s.mu.Lock()
	var cs []*conn
	for _, set := range s.conns {
		for c := range set {
			cs = append(cs, c)
		}
	}
	s.mu.Unlock()
	for _, c := range cs {
		c.closeWith(code, text)
	}
}

func (s *Server) track(c *conn) {
	s.mu.Lock()
	if s.conns[c.dev.ID] == nil {
		s.conns[c.dev.ID] = map[*conn]struct{}{}
	}
	s.conns[c.dev.ID][c] = struct{}{}
	s.mu.Unlock()
}

func (s *Server) untrack(c *conn) {
	s.mu.Lock()
	delete(s.conns[c.dev.ID], c)
	if len(s.conns[c.dev.ID]) == 0 {
		delete(s.conns, c.dev.ID)
	}
	s.mu.Unlock()
}

// seen reports whether key was used in the last idemTTL, and records it.
func (s *Server) seen(key string) bool {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, exp := range s.idem {
		if now.After(exp) {
			delete(s.idem, k)
		}
	}
	if _, dup := s.idem[key]; dup {
		return true
	}
	s.idem[key] = now.Add(idemTTL)
	return false
}

func hostOnly(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if host, _, err := net.SplitHostPort(h); err == nil {
		return host
	}
	return strings.Trim(h, "[]")
}

func (s *Server) hostAllowed(h string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hosts[hostOnly(h)]
}

// checkOrigin rejects a browser page on another origin from opening the
// socket. A client with no Origin (the future native app) authenticates by
// token alone.
func (s *Server) checkOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	i := strings.Index(o, "://")
	if i < 0 {
		return false
	}
	return strings.EqualFold(o[i+3:], r.Host)
}

func (s *Server) clientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if s.opts.TrustProxyIP {
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			if fwd := r.Header.Get("CF-Connecting-IP"); fwd != "" {
				return fwd
			}
		}
	}
	return host
}

// Handler is the HTTP surface: health, pairing, the socket and the client.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("POST /api/v1/pair", s.handlePair)
	mux.HandleFunc("GET /api/v1/ws", s.handleWS)
	mux.HandleFunc("GET /{path...}", s.handleStatic)
	return s.secure(mux)
}

const csp = "default-src 'self'; connect-src 'self'; img-src 'self' data: blob:; style-src 'self'; script-src 'self'; " +
	"manifest-src 'self'; worker-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// secure rejects a Host that is not ours (DNS rebinding) and sets the headers
// every response carries.
func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hostAllowed(r.Host) {
			http.Error(w, "unknown host", http.StatusMisdirectedRequest)
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, wire.Health{OK: true, V: wire.ProtocolVersion})
}

func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	addr := s.clientAddr(r)
	if s.lim.Disabled() {
		writeJSON(w, http.StatusTooManyRequests, wire.Error{Code: wire.CodeForbidden, Msg: "pairing is disabled after repeated failures; restart deck serve"})
		return
	}
	if !s.lim.Allowed(addr) {
		writeJSON(w, http.StatusTooManyRequests, wire.Error{Code: wire.CodeForbidden, Msg: "too many attempts; wait a minute"})
		return
	}
	var req wire.PairRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil || req.Code == "" {
		writeJSON(w, http.StatusBadRequest, wire.Error{Code: wire.CodeInvalid, Msg: "expected {code, device_name}"})
		return
	}
	token, dev, err := s.store.Pair(req.Code, req.DeviceName, addr)
	switch {
	case errors.Is(err, auth.ErrInvalidCode):
		s.lim.Fail(addr)
		writeJSON(w, http.StatusUnauthorized, wire.Error{Code: wire.CodeUnauthorized, Msg: err.Error()})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, wire.Error{Code: wire.CodeInternal, Msg: "could not save the device"})
	default:
		writeJSON(w, http.StatusOK, wire.PairResponse{Token: token, DeviceID: dev.ID})
	}
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	fsys := s.opts.Static
	if fsys == nil {
		fsys = web.FS()
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	if strings.HasPrefix(name, "api/") {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(fsys, name)
	if err != nil && path.Ext(name) == "" {
		// A client-side route: the app shell handles it.
		name = "index.html"
		data, err = fs.ReadFile(fsys, name)
	}
	if err != nil {
		if name == "index.html" {
			http.Error(w, "the web client is not built into this binary", http.StatusServiceUnavailable)
			return
		}
		http.NotFound(w, r)
		return
	}
	if name == "index.html" || name == "sw.js" {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

// doneAfter is how long a session must have been busy before its end is worth
// a notification: a quick answer is already on screen.
var doneAfter = 15 * time.Second

const notifyBodyMax = 110

func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= notifyBodyMax {
		return s
	}
	cut, _ := truncate(s, notifyBodyMax-1)
	return cut + "…"
}

func approvalBody(a wire.Approval) string {
	switch a.Kind {
	case wire.ApprovalCommand:
		return clip("run: " + a.Command)
	case wire.ApprovalEdit:
		return clip("edit " + a.Path)
	}
	return clip(a.Description)
}

// notify turns an event into a push, for phones that asked for it. The
// notification text is encrypted for the phone: the push service cannot read it.
func (s *Server) notify(ev Event) {
	if s.push == nil {
		return
	}
	switch ev.Name {
	case wire.EvApprovalRequested:
		a, ok := ev.Payload.(wire.Approval)
		if !ok {
			return
		}
		go s.push.Approval(push.Message{
			Title: "Approval needed", Body: approvalBody(a),
			Tag: "approval-" + a.ID, SessionID: a.SessionID, Approval: a.ID,
		})
	case wire.EvBusy:
		p, ok := ev.Payload.(map[string]any)
		if !ok {
			return
		}
		sid, _ := p["session_id"].(string)
		busy, _ := p["busy"].(bool)
		if sid == "" {
			return
		}
		s.mu.Lock()
		started, was := s.busySince[sid]
		if busy {
			if !was {
				s.busySince[sid] = time.Now()
			}
			s.mu.Unlock()
			return
		}
		delete(s.busySince, sid)
		s.mu.Unlock()
		if !was || time.Since(started) < doneAfter {
			return
		}
		go s.pushDone(sid)
	}
}

func (s *Server) pushDone(sessionID string) {
	title := "an agent"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if list, err := s.be.Sessions(ctx); err == nil {
		for _, ss := range list {
			if ss.ID == sessionID && ss.Title != "" {
				title = ss.Title
			}
		}
	}
	s.push.Done(push.Message{Title: "Finished", Body: clip(title), Tag: "done-" + sessionID, SessionID: sessionID})
}
