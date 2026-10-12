package peer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/version"
)

// ManagerOptions configures a Manager.
type ManagerOptions struct {
	// Dir is the peers directory; empty means DefaultDir.
	Dir string
	// Peers is the config's peers section. Nil or disabled means Start does
	// nothing.
	Peers *config.Peers
	// Handler answers peers' calls; nil means a StatusHandler.
	Handler Handler
	Limits  Limits
	Logger  *slog.Logger
}

// Manager runs the peer link for one Deck: the listener when
// peers.listen.enabled, and a Supervisor per remote with dial set. With
// peers off it starts nothing at all.
type Manager struct {
	o     ManagerOptions
	id    *Identity
	store *Store
	name  string

	mu      sync.Mutex
	srv     *Server
	sups    map[string]*Supervisor
	started bool
}

// NewManager loads (or makes) the identity when peers are enabled.
func NewManager(o ManagerOptions) (*Manager, error) {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Dir == "" {
		d, err := DefaultDir()
		if err != nil {
			return nil, err
		}
		o.Dir = d
	}
	m := &Manager{o: o, store: OpenStore(o.Dir), sups: map[string]*Supervisor{}}
	m.name = SelfName(o.Peers)
	if o.Handler == nil {
		m.o.Handler = &StatusHandler{Name: m.name, Version: version.Version, Started: time.Now()}
	}
	return m, nil
}

// SelfName is this Deck's peer name: peers.name, else the host name made
// into a valid peer name, else "deck".
func SelfName(p *config.Peers) string {
	if p != nil && p.Name != "" {
		return p.Name
	}
	h, _ := os.Hostname()
	var b strings.Builder
	for _, r := range strings.ToLower(h) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && (r == '-' || r == '_' || r == '.'):
			b.WriteRune(r)
		}
		if b.Len() == 32 {
			break
		}
	}
	if s := b.String(); config.ValidPeerName(s) {
		return s
	}
	return "deck"
}

// Name is this Deck's peer name.
func (m *Manager) Name() string { return m.name }

// Start starts the listener and supervisors the config asks for. With peers
// disabled it returns nil and starts nothing. A dial peer that is not pinned
// is skipped with a warning.
func (m *Manager) Start(ctx context.Context) error {
	p := m.o.Peers
	if p == nil || !p.Enabled {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started {
		return errors.New("peer: manager already started")
	}
	if !p.Listen.Enabled && !anyDial(p) {
		return nil
	}
	id, err := LoadOrCreateIdentity(m.o.Dir)
	if err != nil {
		return err
	}
	m.id = id
	if p.Listen.Enabled {
		srv, err := Listen(p.Listen.ListenAddr(), ServerOptions{
			Identity: id, Store: m.store, Handler: m.o.Handler,
			Name: m.name, Version: version.Version,
			Caller: m.caller, Limits: m.o.Limits, Logger: m.o.Logger,
		})
		if err != nil {
			return err
		}
		m.srv = srv
	}
	for _, r := range p.Remote {
		if !r.Dial {
			continue
		}
		pin, ok, err := m.store.Get(r.Name)
		if err != nil || !ok {
			m.o.Logger.Warn("peer: not dialing an unpaired peer", "peer", r.Name, "error", err)
			continue
		}
		r := r
		sup := NewSupervisor(SupervisorOptions{
			Peer: r.Name,
			Dial: func(ctx context.Context) (*Conn, error) {
				return Dial(ctx, DialOptions{
					Identity: id, Peer: pin, Addr: r.Addr, Handler: m.o.Handler,
					Caller: m.caller(pin), Name: m.name, Version: version.Version,
					Limits: m.o.Limits, Keepalive: true, Logger: m.o.Logger,
				})
			},
		})
		sup.Start(ctx)
		m.sups[r.Name] = sup
	}
	m.started = true
	return nil
}

func anyDial(p *config.Peers) bool {
	for _, r := range p.Remote {
		if r.Dial {
			return true
		}
	}
	return false
}

// caller adds config (role, allow_inbound) to a pinned peer.
func (m *Manager) caller(p Pinned) Caller {
	c := Caller{Name: p.Name, Fingerprint: p.Fingerprint, Role: p.Role}
	if r, ok := m.o.Peers.Find(p.Name); ok {
		c.Role = r.RoleOrDefault()
		c.AllowInbound = r.AllowInbound
	}
	return c
}

// Stop closes the listener, every supervisor and every connection, and waits.
func (m *Manager) Stop() {
	m.mu.Lock()
	srv, sups := m.srv, m.sups
	m.srv, m.sups, m.started = nil, map[string]*Supervisor{}, false
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, s := range sups {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Close()
		}()
	}
	if srv != nil {
		srv.Close()
	}
	wg.Wait()
}

// Addr is the listener's address, or "" when not listening.
func (m *Manager) Addr() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.srv == nil || m.srv.Addr() == nil {
		return ""
	}
	return m.srv.Addr().String()
}

// Statuses returns every supervised peer's status, by name.
func (m *Manager) Statuses() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.sups))
	for _, s := range m.sups {
		out = append(out, s.Status())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Peer < out[j].Peer })
	return out
}

// Supervisor returns the supervisor for name, or nil.
func (m *Manager) Supervisor(name string) *Supervisor {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sups[name]
}

// String describes the manager for logs.
func (m *Manager) String() string {
	return fmt.Sprintf("peers(%s, listen=%q, dial=%d)", m.name, m.Addr(), len(m.Statuses()))
}
