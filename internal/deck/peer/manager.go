package peer

import (
	"context"
	"errors"
	"fmt"
	"github.com/dev-dami/deck/internal/deck/sub"
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
	// nothing until an Apply turns peers on.
	Peers *config.Peers
	// ConfigPath, when set, is the config file Peers came from. The sweep
	// re-reads its peers section whenever the file changes, so role,
	// allow_inbound and the remotes to dial apply while Deck runs. Turning
	// peers or the listener on or off, moving the listener or renaming this
	// Deck by hand needs a restart, or an Apply (which Settings does).
	ConfigPath string
	// Handler answers peers' calls; nil means a StatusHandler.
	Handler Handler
	Limits  Limits
	// SweepInterval is how often the pins and the config are checked again
	// (10s): links of a peer removed or re-paired close within one interval
	// even when it makes no call, and dial remotes added since are started.
	SweepInterval time.Duration
	// OnChange, when set, is called whenever something a status view shows
	// changed: a link's state, an inbound link, the listener or the config in
	// effect. It is called from any goroutine, never with the Manager's lock
	// held, and must not block.
	OnChange func()
	Logger   *slog.Logger
	// SubStore, when set with a nil Handler, adds the SUB ops to the
	// default handler (SubHandler).
	SubStore func() *sub.Store
	// Team, when set with a nil Handler, adds the team chat (console.post)
	// to the default handler and records capsule requests.
	Team *Team
	// Local, with a nil Handler, is what peer.status tells of this Deck's
	// work (presence.go).
	Local Local
	// Granted, with SubStore, also serves a peer the objects a contract
	// shares with it alone (contract.go).
	Granted func(peer string, id sub.ID) bool
	// Extra, with a nil Handler, wraps the default handler with more ops.
	Extra func(Handler) Handler
	// Shared, with a nil Handler, serves this Deck's shared threads.
	Shared Shared
	// MaxRelays caps relayed channels on this Deck at once (16), both the
	// ones it relays and the ones relayed to it; MaxRelaysPerPeer caps those
	// of one pinned key (4). See relay.go.
	MaxRelays, MaxRelaysPerPeer int
}

// Manager runs the peer link for one Deck: the listener when
// peers.listen.enabled, and a Supervisor per remote with dial set. With
// peers off it runs nothing at all. Apply puts a new peers section in effect
// while it runs.
type Manager struct {
	o      ManagerOptions
	store  *Store
	name   atomic.Pointer[string]
	perKey *keyLimiter
	// pairs holds the pairing code armed on the listener. It outlives each
	// Server, so moving the listener keeps the code armed.
	pairs *pairSlot
	// peers is the config snapshot callers are built from, swapped by the
	// sweep and by Apply; nil is a valid (empty) config.
	peers atomic.Pointer[config.Peers]
	// relays counts and runs relayed channels (relay.go).
	relays *relayer

	// applyMu serializes Start, Apply and Stop.
	applyMu sync.Mutex

	mu        sync.Mutex
	id        *Identity
	srv       *Server
	rsrv      *Server // serves links relayed to this Deck; made on first use
	srvKey    string  // the address and name srv was started with
	listenErr error   // why the listener the config asks for is not up
	sups      map[string]*managed
	seen      map[string]time.Time // when each peer was last connected
	started   bool                 // Start was called, and Stop was not
	live      bool                 // identity loaded and sweep running
	base      context.Context      // Start's context
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
	via  string // the relay it dials through
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

// NewManager makes a Manager; nothing runs and no key is read until Start.
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
	m := &Manager{o: o, store: OpenStore(o.Dir), sups: map[string]*managed{}, seen: map[string]time.Time{}}
	m.perKey = newKeyLimiter(16)
	m.pairs = newPairSlot(nil)
	m.relays = newRelayer(o.MaxRelays, o.MaxRelaysPerPeer, o.Limits.withDefaults().IdleTimeout)
	m.peers.Store(o.Peers)
	m.setName(SelfName(o.Peers))
	if o.Handler == nil {
		sh := &StatusHandler{NameFunc: m.Name, Version: version.Version, Started: time.Now()}
		if o.Local.Presence != nil {
			sh.Presence = func(peer string, repos []string) Presence {
				p := m.peers.Load()
				return o.Local.Presence(peer, repos, p != nil && p.ShareLimits)
			}
		}
		m.o.Handler = sh
		if o.SubStore != nil {
			sh := &SubHandler{Base: m.o.Handler, Store: o.SubStore, Granted: o.Granted}
			if o.Team != nil {
				sh.OnRequest = func(from Caller, id sub.ID, served bool) { o.Team.AddRequest(from.Name, id, served) }
			}
			m.o.Handler = sh
		}
		if o.Team != nil {
			m.o.Handler = &TeamHandler{Base: m.o.Handler, Team: o.Team}
		}
		if o.Shared != nil {
			m.o.Handler = &ShareHandler{Base: m.o.Handler, Shared: o.Shared}
		}
		if o.Extra != nil {
			m.o.Handler = o.Extra(m.o.Handler)
		}
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
func (m *Manager) Name() string { return *m.name.Load() }

// setName sets this Deck's name and reports whether it changed.
func (m *Manager) setName(name string) bool {
	old := m.name.Swap(&name)
	return old == nil || *old != name
}

// Start starts the listener and supervisors the config asks for, and the
// sweep that keeps them in line with the pins. With peers disabled it
// returns nil and starts nothing (a later Apply may). A dial peer that is not
// pinned is skipped (the sweep starts it once it is). A listener that cannot
// start is reported, but the supervisors run anyway.
func (m *Manager) Start(ctx context.Context) error {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return errors.New("peer: manager already started")
	}
	m.started, m.base = true, ctx
	m.mu.Unlock()
	return m.apply(m.peers.Load())
}

// Apply puts a new peers section in effect at once: peers turned on or off,
// the listener started, stopped or moved, this Deck renamed (its links
// reconnect under the new name) and the supervisors brought in line with
// peers.remote and the pins. Before Start it only keeps p for Start. The
// error is the listener's; the rest of p is in effect either way.
func (m *Manager) Apply(p *config.Peers) error {
	if p == nil {
		p = &config.Peers{}
	}
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	m.peers.Store(p)
	m.mu.Lock()
	started := m.started
	m.mu.Unlock()
	if !started {
		m.setName(SelfName(p))
		return nil
	}
	return m.apply(p)
}

// apply brings what runs in line with p. applyMu is held.
func (m *Manager) apply(p *config.Peers) error {
	defer m.changed()
	renamed := m.setName(SelfName(p))
	if p == nil || !p.Enabled || (!p.Listen.Enabled && !anyDial(p)) {
		m.halt()
		return nil
	}
	if err := m.ensureLive(); err != nil {
		return err
	}
	if renamed {
		// Supervisors dial with the name they started with: stop them all
		// and let the sweep start them again under the new one.
		m.mu.Lock()
		sups, rsrv := m.sups, m.rsrv
		m.sups, m.rsrv = map[string]*managed{}, nil
		m.mu.Unlock()
		closeSups(sups)
		if rsrv != nil {
			rsrv.Close()
		}
	}
	err := m.applyListener(p)
	m.sweep()
	return err
}

// ensureLive loads the identity and starts the sweep, once. applyMu is held.
func (m *Manager) ensureLive() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.live {
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
	base := m.base
	if base == nil {
		base = context.Background()
	}
	m.ctx, m.stop = context.WithCancel(base)
	m.live = true
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

// applyListener starts, stops or moves the listener to match p. applyMu is
// held and the Manager is live.
func (m *Manager) applyListener(p *config.Peers) error {
	name := m.Name()
	want := ""
	if p.Listen.Enabled {
		want = p.Listen.ListenAddr() + " " + name
	}
	m.mu.Lock()
	if want == m.srvKey && (want == "" || m.srv != nil) {
		m.mu.Unlock()
		return nil
	}
	old, id := m.srv, m.id
	m.srv, m.srvKey, m.listenErr = nil, "", nil
	m.mu.Unlock()
	if old != nil {
		old.Close()
	}
	if want == "" {
		m.pairs.stop() // nothing serves an armed code any more
		return nil
	}
	srv, err := Listen(p.Listen.ListenAddr(), ServerOptions{
		Identity: id, Store: m.store, Handler: m.o.Handler,
		Name: name, Version: version.Version,
		Caller: m.caller, Limits: m.o.Limits, Logger: m.o.Logger,
		OnConn: m.inbound, OnDisconnect: m.inbound,
		perKey: m.perKey, pairs: m.pairs, relay: m.relayChannel,
	})
	if err != nil {
		m.pairs.stop()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.listenErr = err
		return err
	}
	m.srv, m.srvKey = srv, want
	if p.Listen.AllInterfaces() {
		m.o.Logger.Warn("peer: listening on every interface; set peers.listen.addr to one address (e.g. a WireGuard IP) to narrow it",
			"addr", p.Listen.ListenAddr())
	}
	return nil
}

// halt stops the listener, every supervisor and the sweep, and waits. The
// Manager stays started, so a later Apply brings them back. applyMu is held.
func (m *Manager) halt() {
	m.mu.Lock()
	srv, rsrv, sups, stop, done := m.srv, m.rsrv, m.sups, m.stop, m.sweepDone
	m.srv, m.rsrv, m.srvKey, m.listenErr = nil, nil, "", nil
	m.sups = map[string]*managed{}
	m.live, m.stop, m.sweepDone = false, nil, nil
	m.mu.Unlock()
	if stop != nil {
		stop()
	}
	if done != nil {
		<-done
	}
	m.relays.stopAll()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		closeSups(sups)
	}()
	for _, s := range []*Server{srv, rsrv} {
		if s != nil {
			s.Close()
		}
	}
	wg.Wait()
	m.relays.wg.Wait()
	m.pairs.stop()
}

// ArmPair arms a pairing code (a fresh one when o.Code is empty) on this
// Deck's listener, so the other Deck pairs on the same port as the link.
// Identity and Name are this Deck's. The code stays armed when Apply moves
// the listener, and ends when the listener stops. Without a listener it
// returns errNoListener.
func (m *Manager) ArmPair(ctx context.Context, o PairOptions) (*PairWait, error) {
	m.mu.Lock()
	srv, id := m.srv, m.id
	m.mu.Unlock()
	if srv == nil || srv.Addr() == nil {
		return nil, errNoListener
	}
	o.Identity, o.Name = id, m.Name()
	if o.Logger == nil {
		o.Logger = m.o.Logger
	}
	w, err := armPair(ctx, m.pairs, o)
	if err != nil {
		return nil, err
	}
	w.Addr = srv.Addr().String()
	w.Reachable = Reachable(w.Addr)
	return w, nil
}

// closeSups closes supervisors side by side and waits for them.
func closeSups(sups map[string]*managed) {
	var wg sync.WaitGroup
	for _, ms := range sups {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ms.sup.Close()
		}()
	}
	wg.Wait()
}

func anyDial(p *config.Peers) bool {
	for _, r := range p.Remote {
		if r.Dial {
			return true
		}
	}
	return false
}

// changed tells OnChange that something shown changed.
func (m *Manager) changed() {
	if m.o.OnChange != nil {
		m.o.OnChange()
	}
}

// inbound notes an inbound link opening or closing.
func (m *Manager) inbound(c *Conn) {
	m.markSeen(c.Peer().Name)
	m.changed()
}

func (m *Manager) markSeen(name string) {
	m.mu.Lock()
	m.seen[name] = time.Now()
	m.mu.Unlock()
}

// startSup starts a supervisor for remote r, pinned as pin. m.mu is held.
// With r.Via set it dials through that relay when r has no address, or when
// the direct dial fails.
func (m *Manager) startSup(r config.PeerRemote, pin Pinned) {
	name, addr, via, fp, id, self := r.Name, r.Addr, r.Via, pin.Fingerprint, m.id, m.Name()
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
			dial := func(relay *Conn) (*Conn, error) {
				return Dial(ctx, DialOptions{
					Identity: id, Peer: cur, Addr: addr, Via: relay, Handler: m.o.Handler,
					Caller: m.caller(cur), Recheck: m.recheck, Name: self, Version: version.Version,
					Limits: m.o.Limits, Keepalive: true, Logger: m.o.Logger, perKey: m.perKey,
					relay: m.relayChannel,
				})
			}
			var direct error
			if addr != "" {
				c, err := dial(nil)
				if err == nil || via == "" || ctx.Err() != nil {
					return c, err
				}
				direct = err
			}
			relay := m.directLink(via)
			if relay == nil {
				err = fmt.Errorf("peer: no link to the relay %q", via)
			} else {
				var c *Conn
				if c, err = dial(relay); err == nil {
					return c, nil
				}
			}
			if direct != nil {
				err = fmt.Errorf("%w; through %s: %w", direct, via, err)
			}
			return nil, err
		},
	})
	// Subscribed before Start, so the first change is not missed; the
	// forwarder ends with the supervisor.
	events, unsub := sup.Subscribe()
	sup.Start(m.ctx)
	go func() {
		defer unsub()
		for {
			select {
			case ev := <-events:
				if ev.From == StateConnected || ev.From == StateDegraded || ev.Status.State == StateConnected {
					m.markSeen(name)
				}
				m.changed()
			case <-sup.done:
				return
			}
		}
	}()
	m.sups[name] = &managed{sup: sup, fp: fp, addr: addr, via: via}
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
	if !m.live {
		m.mu.Unlock()
		return
	}
	var stop []*Supervisor
	for name, ms := range m.sups {
		p, pinned := byName[name]
		r, configured := cfg.Find(name)
		if !pinned || p.Fingerprint != ms.fp || !configured || !r.Dial || r.Addr != ms.addr || r.Via != ms.via {
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
	m.mu.Unlock()

	for _, s := range stop {
		s.Close()
	}
	for _, c := range m.inboundConns() {
		who := c.Peer()
		if p, ok := byName[who.Name]; !ok || p.Fingerprint != who.Fingerprint {
			m.o.Logger.Info("peer: closing the link of a peer no longer pinned", "peer", who.Name)
			c.drop()
		} else if r, ok := cfg.Find(c.Via()); c.Via() != "" && (!ok || !r.Relay) {
			m.o.Logger.Info("peer: closing a link relayed by a peer no longer allowed to relay", "peer", who.Name, "via", c.Via())
			c.drop()
		}
	}
	if cfg == nil || !cfg.Relay {
		m.relays.stopAll()
	}
	if len(stop) > 0 {
		m.changed()
	}
}

// Stop closes the listener, every supervisor and every connection, and waits.
func (m *Manager) Stop() {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	m.halt()
	m.mu.Lock()
	m.started = false
	m.mu.Unlock()
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

// ListenErr is why the listener the config asks for is not up, or nil.
func (m *Manager) ListenErr() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.listenErr
}

// Running reports whether the link runs: peers are on and there is a
// listener or a remote to dial.
func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.live
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

// inboundConns are the links peers opened to this Deck: to its listener,
// and relayed to it.
func (m *Manager) inboundConns() []*Conn {
	m.mu.Lock()
	srv, rsrv := m.srv, m.rsrv
	m.mu.Unlock()
	var out []*Conn
	for _, s := range []*Server{srv, rsrv} {
		if s != nil {
			out = append(out, s.Conns()...)
		}
	}
	return out
}

// Link is one peer's links as a status view shows them.
type Link struct {
	Status     Status // the supervisor's; zero when Supervised is false
	Supervised bool   // this Deck dials it
	Inbound    int    // links it opened to this Deck, directly or relayed
	// Via is the relay a link to it runs through; "" when all are direct.
	Via      string
	LastSeen time.Time
}

// Links returns what is known of every peer with a link or a record of
// one, by name.
func (m *Manager) Links() map[string]Link {
	m.mu.Lock()
	out := make(map[string]Link, len(m.sups)+len(m.seen))
	for name, at := range m.seen {
		out[name] = Link{LastSeen: at}
	}
	for name, ms := range m.sups {
		l := out[name]
		l.Status, l.Supervised = ms.sup.Status(), true
		l.Via = l.Status.Via
		out[name] = l
	}
	m.mu.Unlock()
	for _, c := range m.inboundConns() {
		name := c.Peer().Name
		l := out[name]
		l.Inbound++
		if c.Via() != "" {
			l.Via = c.Via()
		}
		out[name] = l
	}
	return out
}

// channel is an MCP channel to the peer name on a link that is already up:
// the client its supervisor keeps, else a new channel on a link the peer
// opened to this Deck. release ends what channel opened. It returns nil when
// no link is up.
func (m *Manager) channel(ctx context.Context, name string) (*Client, func()) {
	if sup := m.Supervisor(name); sup != nil {
		if cl := sup.Client(); cl != nil {
			return cl, func() {}
		}
	}
	for _, c := range m.inboundConns() {
		if c.Peer().Name != name {
			continue
		}
		cl, err := c.OpenMCP(ctx)
		if err != nil {
			continue
		}
		if _, err := cl.Initialize(ctx); err != nil {
			_ = cl.Close()
			continue
		}
		return cl, func() { _ = cl.Close() }
	}
	return nil, nil
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
	return fmt.Sprintf("peers(%s, listen=%q, dial=%d)", m.Name(), m.Addr(), len(m.Statuses()))
}
