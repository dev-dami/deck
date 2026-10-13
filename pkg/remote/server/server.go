// Package server serves Deck to a phone: the pairing and health routes, the
// WebSocket of docs/remote-contract.md §3 and the embedded client. It
// depends on remote.Backend alone and is tested against a fake.
package server

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/auth"
	"github.com/dev-dami/deck/pkg/remote/hub"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// Features the server announces in welcome.
var features = []string{"images", "edit", "changes", "queue", "models", "devices"}

// Options configure a Server.
type Options struct {
	Version string
	// Headless is true when no TUI shares the workspace.
	Headless bool
	// TLS says the page is served over https, which the Origin check
	// (R17) and the advert need to know.
	TLS bool
	// Hosts are extra Host header values to accept (LAN addresses).
	// localhost, 127.0.0.1 and ::1 are always accepted.
	Hosts []string
	// Static overrides the embedded client; tests use it.
	Static fs.FS
	// Now replaces time.Now; tests pin it.
	Now func() time.Time
	// Paired is told of every new device, for the console.
	Paired func(auth.Device)
}

// Server is the remote-control endpoint.
type Server struct {
	be     remote.Backend
	store  *auth.Store
	limits *auth.Limits
	hub    *hub.Hub
	opts   Options
	now    func() time.Time
	up     websocket.Upgrader
	idem   *idem

	// addrOf is the client address used for limits; tests replace it.
	addrOf func(*http.Request) string
	// beforeTrack runs between Verify and track; tests use it to revoke.
	beforeTrack func()

	mu     sync.Mutex
	hosts  map[string]bool
	conns  map[string]map[*conn]struct{} // by device id
	known  map[string]auth.Device        // devices as of the last refresh
	seeded bool
	// unauth counts connections that have not sent a valid hello (R4).
	unauth      map[string]int
	unauthTotal int
	changed     chan struct{}
	// refreshMu serialises refreshDevices: read, diff and apply as one.
	refreshMu sync.Mutex
}

// New returns a Server over be, pairing devices through store.
func New(be remote.Backend, store *auth.Store, opts Options) *Server {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	s := &Server{
		be: be, store: store, limits: auth.NewLimits(opts.Now), hub: hub.New(), opts: opts, now: opts.Now,
		idem:    newIdem(opts.Now),
		hosts:   map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true},
		conns:   map[string]map[*conn]struct{}{},
		known:   map[string]auth.Device{},
		unauth:  map[string]int{},
		changed: make(chan struct{}, 1),
		addrOf:  func(r *http.Request) string { return r.RemoteAddr },
	}
	for _, h := range opts.Hosts {
		s.AddHost(h)
	}
	s.up = websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096, CheckOrigin: s.sameOrigin}
	return s
}

// Hub exposes the event hub, for the TUI and tests.
func (s *Server) Hub() *hub.Hub { return s.hub }

// Store exposes the device store.
func (s *Server) Store() *auth.Store { return s.store }

// AddHost accepts another Host header value.
func (s *Server) AddHost(h string) {
	s.mu.Lock()
	s.hosts[hostOnly(h)] = true
	s.mu.Unlock()
}

// Run feeds backend events to the hub, prunes the limits, flushes
// last-seen times and applies device changes made by any process, until
// ctx ends.
func (s *Server) Run(ctx context.Context) {
	events := s.be.Events(ctx)
	go s.store.Run(ctx)
	go s.limits.Run(ctx)
	// The watch's baseline comes first, so no change slips between it and
	// the snapshot.
	s.store.Watch(ctx, s.notifyChanged)
	s.refreshDevices()
	for {
		select {
		case <-ctx.Done():
			s.closeAll(websocket.CloseGoingAway, "server stopping")
			return
		case ev, ok := <-events:
			if !ok {
				s.closeAll(websocket.CloseInternalServerErr, "backend closed")
				return
			}
			s.hub.Publish(capEvent(ev))
		case <-s.changed:
			s.refreshDevices()
		}
	}
}

func (s *Server) notifyChanged() {
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

// refreshDevices diffs devices.json against the last snapshot: a new
// device is announced (R3), a missing one is revoked and dropped (R16).
// One refresh at a time, read to apply, so a stale read never revokes a
// live device.
func (s *Server) refreshDevices() {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	devs, err := s.store.List()
	if err != nil {
		slog.Warn("remote: could not read the devices", "error", err)
		return // a transient read problem must not log everyone out
	}
	live := make(map[string]auth.Device, len(devs))
	for _, d := range devs {
		live[d.ID] = d
	}
	s.mu.Lock()
	seeded := s.seeded
	var added []auth.Device
	var gone []string
	for id, d := range live {
		if _, ok := s.known[id]; !ok {
			added = append(added, d)
		}
	}
	for id := range s.known {
		if _, ok := live[id]; !ok {
			gone = append(gone, id)
		}
	}
	// A device paired and revoked between two refreshes was never known,
	// but may be connected: what matters is that it is not in the file.
	for id := range s.conns {
		if _, ok := live[id]; !ok {
			if _, ok := s.known[id]; !ok {
				gone = append(gone, id)
			}
		}
	}
	s.known, s.seeded = live, true
	s.mu.Unlock()
	if !seeded {
		return
	}
	for _, d := range added {
		s.hub.Emit(wire.EvDevicePaired, d.Wire(false))
		if s.opts.Paired != nil {
			s.opts.Paired(d)
		}
	}
	for _, id := range gone {
		s.hub.Emit(wire.EvDeviceRevoked, wire.DeviceRevoked{ID: id})
		s.kickDevice(id)
	}
}

// kickDevice closes every connection of a revoked device.
func (s *Server) kickDevice(id string) {
	s.mu.Lock()
	cs := make([]*conn, 0, len(s.conns[id]))
	for c := range s.conns[id] {
		cs = append(cs, c)
	}
	s.mu.Unlock()
	for _, c := range cs {
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

// track registers c, unless its device was revoked since Verify. It
// registers first and looks the device up after, so a revoke either
// lands before the lookup (refused here) or after the registration
// (kicked by refreshDevices); there is no window between the two.
func (s *Server) track(c *conn) bool {
	s.mu.Lock()
	if s.conns[c.dev.ID] == nil {
		s.conns[c.dev.ID] = map[*conn]struct{}{}
	}
	s.conns[c.dev.ID][c] = struct{}{}
	s.mu.Unlock()
	if _, err := s.store.Get(c.dev.ID); errors.Is(err, auth.ErrNotFound) {
		s.untrack(c)
		return false
	}
	return true
}

func (s *Server) untrack(c *conn) {
	s.mu.Lock()
	delete(s.conns[c.dev.ID], c)
	if len(s.conns[c.dev.ID]) == 0 {
		delete(s.conns, c.dev.ID)
	}
	s.mu.Unlock()
}

// Connections counts the live connections of a device ("" for all).
func (s *Server) Connections(deviceID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if deviceID != "" {
		return len(s.conns[deviceID])
	}
	n := 0
	for _, set := range s.conns {
		n += len(set)
	}
	return n
}

// admit reserves an unauthenticated slot for addr (R4), or refuses.
func (s *Server) admit(addr string) bool {
	key := peer.LockKey(addr)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unauth[key] >= wire.MaxUnauthPerSource || s.unauthTotal >= wire.MaxUnauthTotal {
		return false
	}
	s.unauth[key]++
	s.unauthTotal++
	return true
}

// release frees addr's unauthenticated slot.
func (s *Server) release(addr string) {
	key := peer.LockKey(addr)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unauth[key] <= 1 {
		delete(s.unauth, key)
	} else {
		s.unauth[key]--
	}
	if s.unauthTotal > 0 {
		s.unauthTotal--
	}
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

// pageOrigin is the origin the page was served from: the only Origin the
// socket and the pair route accept (R7, R17).
func (s *Server) pageOrigin(r *http.Request) string {
	scheme := "http"
	if s.opts.TLS || r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + strings.ToLower(r.Host)
}

// sameOrigin reports whether r carries exactly the page's Origin. An
// absent Origin is refused: the PWA is the only client.
func (s *Server) sameOrigin(r *http.Request) bool {
	o := strings.ToLower(strings.TrimSpace(r.Header.Get("Origin")))
	return o != "" && o == s.pageOrigin(r)
}
