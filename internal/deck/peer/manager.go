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
	"sync/atomic"
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
	// ConfigPath, when set, is the config file Peers came from. The sweep
	// re-reads its peers section whenever the file changes, so role,
	// allow_inbound and the remotes to dial apply while Deck runs. Turning
	// peers or the listener on or off, or moving the listener, still needs
	// a restart.
	ConfigPath string
	// Handler answers peers' calls; nil means a StatusHandler.
	Handler Handler
	Limits  Limits
	// SweepInterval is how often the pins and the config are checked again
	// (10s): links of a peer removed or re-paired close within one interval
	// even when it makes no call, and dial remotes added since are started.
	SweepInterval time.Duration
	Logger        *slog.Logger
}

// Manager runs the peer link for one Deck: the listener when
// peers.listen.enabled, and a Supervisor per remote with dial set. With
// peers off it starts nothing at all.
type Manager struct {
	o      ManagerOptions
	id     *Identity
	store  *Store
	name   string
	perKey *keyLimiter
	// peers is the config snapshot callers are built from, swapped by the
	// sweep; nil is a valid (empty) config.
	peers atomic.Pointer[config.Peers]

	mu        sync.Mutex
	srv       *Server
	sups      map[string]*managed
	started   bool
	ctx       context.Context
	stop      context.CancelFunc
	sweepDone chan struct{}
	cfgStamp  fileStamp
}

// managed is one running supervisor and what it was started for.
type managed struct {
	sup  *Supervisor
	fp   string // the pin's fingerprint
	addr string // the address it dials
}

// fileStamp tells whether a file changed since it was last read.
type fileStamp struct {
	mod  time.Time
	size int64
	ok   bool
}

func stampOf(path string) fileStamp {
	fi, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{mod: fi.ModTime(), size: fi.Size(), ok: true}
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
	if o.SweepInterval <= 0 {
		o.SweepInterval = 10 * time.Second
	}
	m := &Manager{o: o, store: OpenStore(o.Dir), sups: map[string]*managed{}}
	m.perKey = newKeyLimiter(16)
	m.peers.Store(o.Peers)
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

// Start starts the listener and supervisors the config asks for, and the
// sweep that keeps them in line with the pins. With peers disabled it
// returns nil and starts nothing. A dial peer that is not pinned is skipped
// (the sweep starts it once it is).
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
	if m.o.ConfigPath != "" {
		m.cfgStamp = stampOf(m.o.ConfigPath)
	}
	if p.Listen.Enabled {
		srv, err := Listen(p.Listen.ListenAddr(), ServerOptions{
			Identity: id, Store: m.store, Handler: m.o.Handler,
			Name: m.name, Version: version.Version,
			Caller: m.caller, Limits: m.o.Limits, Logger: m.o.Logger,
			perKey: m.perKey,
		})
		if err != nil {
			return err
		}
		m.srv = srv
		if p.Listen.AllInterfaces() {
			m.o.Logger.Warn("peer: listening on every interface; set peers.listen.addr to one address (e.g. a WireGuard IP) to narrow it",
				"addr", p.Listen.ListenAddr())
		}
	}
	m.ctx, m.stop = context.WithCancel(ctx)
	for _, r := range p.Remote {
		if !r.Dial {
			continue
		}
		pin, ok, err := m.store.Get(r.Name)
		if err != nil || !ok {
			m.o.Logger.Warn("peer: not dialing an unpaired peer", "peer", r.Name, "error", err)
			continue
		}
		m.startSup(r, pin)
	}
	m.started = true
	m.sweepDone = make(chan struct{})
	go func(ctx context.Context, done chan struct{}) {
		defer close(done)
		t := time.NewTicker(m.o.SweepInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				m.sweep()
			}
		}
	}(m.ctx, m.sweepDone)
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

// startSup starts a supervisor for remote r, pinned as pin. m.mu is held.
func (m *Manager) startSup(r config.PeerRemote, pin Pinned) {
	name, addr, fp := r.Name, r.Addr, pin.Fingerprint
	sup := NewSupervisor(SupervisorOptions{
		Peer: name,
		Dial: func(ctx context.Context) (*Conn, error) {
			// The pin is read again for every dial: a peer removed or
			// re-paired since this supervisor started is never dialed with
			// the key it had, and its role and allow_inbound are current.
			cur, ok, err := m.store.Get(name)
			if err != nil {
				return nil, err
			}
			if !ok || cur.Fingerprint != fp {
				return nil, fmt.Errorf("peer: %q: %w", name, ErrRevoked)
			}
			return Dial(ctx, DialOptions{
				Identity: m.id, Peer: cur, Addr: addr, Handler: m.o.Handler,
				Caller: m.caller(cur), Recheck: m.recheck, Name: m.name, Version: version.Version,
				Limits: m.o.Limits, Keepalive: true, Logger: m.o.Logger, perKey: m.perKey,
			})
		},
	})
	sup.Start(m.ctx)
	m.sups[name] = &managed{sup: sup, fp: fp, addr: addr}
}

// caller adds config (role, allow_inbound) to a pinned peer, from the
// current snapshot.
func (m *Manager) caller(p Pinned) Caller {
	c := Caller{Name: p.Name, Fingerprint: p.Fingerprint, Role: p.Role}
	if r, ok := m.peers.Load().Find(p.Name); ok {
		c.Role = r.RoleOrDefault()
		c.AllowInbound = r.AllowInbound
	}
	return c
}

// recheck is the per-call check for links this Manager dialed: the peer
// must still be pinned with the key the link was made with.
func (m *Manager) recheck(c Caller) (Caller, error) {
	p, ok, err := m.store.Get(c.Name)
	if err != nil {
		return Caller{}, err
	}
	if !ok || p.Fingerprint != c.Fingerprint {
		return Caller{}, ErrRevoked
	}
	return m.caller(p), nil
}

// reloadConfig swaps in the config file's peers section when the file
// changed since the last read. A file that no longer parses keeps the last
// good snapshot.
func (m *Manager) reloadConfig() {
	if m.o.ConfigPath == "" {
		return
	}
	st := stampOf(m.o.ConfigPath)
	m.mu.Lock()
	same := st == m.cfgStamp
	m.cfgStamp = st
	m.mu.Unlock()
	if same {
		return
	}
	p, err := config.LoadPeers(m.o.ConfigPath)
	if err != nil {
		m.o.Logger.Warn("peer: config not reloaded; keeping the last one", "error", err)
		return
	}
	if p == nil {
		p = &config.Peers{}
	}
	m.peers.Store(p)
}

// sweep re-reads the config (when it changed) and the pins, and brings the
// links in line: every link of a peer that is no longer pinned, or is pinned
// with another key, is closed, and the supervisors follow peers.remote
// (stopped when a remote is removed, no longer dialed, moved or re-paired;
// started for a newly pinned dial remote). A pin list that cannot be read
// counts as empty: fail closed.
func (m *Manager) sweep() {
	m.reloadConfig()
	pins, err := m.store.List()
	if err != nil {
		m.o.Logger.Error("peer: pinned store unreadable; closing every link", "error", err)
		pins = nil
	}
	byName := make(map[string]Pinned, len(pins))
	for _, p := range pins {
		byName[p.Name] = p
	}
	cfg := m.peers.Load()

	m.mu.Lock()
	if !m.started {
		m.mu.Unlock()
		return
	}
	var stop []*Supervisor
	for name, ms := range m.sups {
		p, pinned := byName[name]
		r, configured := cfg.Find(name)
		if !pinned || p.Fingerprint != ms.fp || !configured || !r.Dial || r.Addr != ms.addr {
			m.o.Logger.Info("peer: stopping the link to a peer whose pin or config changed", "peer", name)
			stop = append(stop, ms.sup)
			delete(m.sups, name)
		}
	}
	if cfg != nil {
		for _, r := range cfg.Remote {
			if p, pinned := byName[r.Name]; r.Dial && pinned && m.sups[r.Name] == nil {
				m.startSup(r, p)
			}
		}
	}
	srv := m.srv
	m.mu.Unlock()

	for _, s := range stop {
		s.Close()
	}
	if srv != nil {
		for _, c := range srv.Conns() {
			who := c.Peer()
			if p, ok := byName[who.Name]; !ok || p.Fingerprint != who.Fingerprint {
				m.o.Logger.Info("peer: closing the link of a peer no longer pinned", "peer", who.Name)
				c.drop()
			}
		}
	}
}

// Stop closes the listener, every supervisor and every connection, and waits.
func (m *Manager) Stop() {
	m.mu.Lock()
	srv, sups, stop, done := m.srv, m.sups, m.stop, m.sweepDone
	m.srv, m.sups, m.started = nil, map[string]*managed{}, false
	m.stop, m.sweepDone = nil, nil
	m.mu.Unlock()
	if stop != nil {
		stop()
	}
	if done != nil {
		<-done
	}
	var wg sync.WaitGroup
	for _, ms := range sups {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ms.sup.Close()
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
	for _, ms := range m.sups {
		out = append(out, ms.sup.Status())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Peer < out[j].Peer })
	return out
}

// Supervisor returns the supervisor for name, or nil.
func (m *Manager) Supervisor(name string) *Supervisor {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ms := m.sups[name]; ms != nil {
		return ms.sup
	}
	return nil
}

// String describes the manager for logs.
func (m *Manager) String() string {
	return fmt.Sprintf("peers(%s, listen=%q, dial=%d)", m.name, m.Addr(), len(m.Statuses()))
}
