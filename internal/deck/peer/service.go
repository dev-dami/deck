package peer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dev-dami/deck/internal/deck/config"
)

// LocalListenAddr is where Settings starts the listener the first time it is
// turned on: this machine only.
const LocalListenAddr = "127.0.0.1:" + config.DefaultPeerPort

// Service is the peer link as Deck's Settings drive it, so everything `deck
// peer` does can be done without a terminal or editing the config. It owns
// the Manager, made the first time peers are on, and puts every change it
// makes to the peers section in effect at once (Manager.Apply). Watchers
// learn of changes through Wait. Every method may read or write files or use
// the network: call them off the UI loop.
type Service struct {
	dir, cfgPath string
	log          *slog.Logger
	base         context.Context
	cancel       context.CancelFunc

	// applyMu serializes making and applying the Manager.
	applyMu sync.Mutex

	mu       sync.Mutex
	mgr      *Manager
	applied  *config.Peers // the section last put in effect
	applyErr error
	pairing  int // pairing exchanges in progress
	ver      uint64
	wake     chan struct{} // closed and replaced on every change
}

// ServiceOptions configures a Service.
type ServiceOptions struct {
	// Dir is the peers directory; empty means DefaultDir.
	Dir string
	// ConfigPath is Deck's config.json. Without it nothing can be changed.
	ConfigPath string
	Logger     *slog.Logger
}

// errNoConfig is returned by every change when the Service has no config file.
var errNoConfig = errors.New("peer: no config file to save to")

// NewService makes a Service. It reads nothing and runs nothing until Start.
func NewService(o ServiceOptions) (*Service, error) {
	if o.Dir == "" {
		d, err := DefaultDir()
		if err != nil {
			return nil, err
		}
		o.Dir = d
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{dir: o.Dir, cfgPath: o.ConfigPath, log: o.Logger, base: ctx, cancel: cancel, wake: make(chan struct{})}, nil
}

// Start puts p, the peers section Deck started with, in effect. With peers
// off it makes nothing: no key, no listener, no goroutines.
func (s *Service) Start(p *config.Peers) error { return s.apply(p) }

// Close ends any pairing and stops the link.
func (s *Service) Close() {
	s.cancel()
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	s.mu.Lock()
	mgr := s.mgr
	s.mu.Unlock()
	if mgr != nil {
		mgr.Stop()
	}
}

// Manager is the running Manager, or nil before peers were first on.
func (s *Service) Manager() *Manager {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mgr
}

// changed wakes every Wait.
func (s *Service) changed() {
	s.mu.Lock()
	s.ver++
	close(s.wake)
	s.wake = make(chan struct{})
	s.mu.Unlock()
}

// Wait blocks until something a Snapshot shows changed after the snapshot
// whose Version is since, or ctx ends, and returns the version now. It is
// how a status view follows the link without polling.
func (s *Service) Wait(ctx context.Context, since uint64) uint64 {
	for {
		s.mu.Lock()
		v, w := s.ver, s.wake
		s.mu.Unlock()
		if v != since {
			return v
		}
		select {
		case <-w:
		case <-ctx.Done():
			return v
		}
	}
}

func (s *Service) setPairing(d int) {
	s.mu.Lock()
	s.pairing += d
	s.mu.Unlock()
	s.changed()
}

// loadPeers reads the peers section of the config file; nil when it has
// none.
func (s *Service) loadPeers() (*config.Peers, error) {
	if s.cfgPath == "" {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.applied, nil
	}
	return config.LoadPeers(s.cfgPath)
}

// Reload reads the peers section again and puts it in effect: for a config
// edited outside Deck (Snapshot.Pending).
func (s *Service) Reload() error {
	p, err := s.loadPeers()
	if err != nil {
		return err
	}
	return s.apply(p)
}

// apply puts p in effect, making the Manager the first time peers are on.
func (s *Service) apply(p *config.Peers) error {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	defer s.changed()
	s.mu.Lock()
	mgr := s.mgr
	s.applied = p
	s.mu.Unlock()
	var err error
	switch {
	case mgr != nil:
		err = mgr.Apply(p)
	case p != nil && p.Enabled:
		mgr, err = NewManager(ManagerOptions{Dir: s.dir, Peers: p, ConfigPath: s.cfgPath, OnChange: s.changed, Logger: s.log})
		if err != nil {
			break
		}
		s.mu.Lock()
		s.mgr = mgr
		s.mu.Unlock()
		err = mgr.Start(s.base)
	}
	if err != nil {
		s.log.Warn("Peers: not all of the config is in effect", "error", err)
	}
	s.mu.Lock()
	s.applyErr = err
	s.mu.Unlock()
	return err
}

// update changes the peers section of the config file with fn and puts the
// result in effect.
func (s *Service) update(fn func(p *config.Peers) bool) error {
	if s.cfgPath == "" {
		return errNoConfig
	}
	if _, err := config.UpdatePeers(s.cfgPath, fn); err != nil {
		return err
	}
	return s.Reload()
}

// SetEnabled turns the peer link on or off. Turning it on makes this Deck's
// key, so its fingerprint can be shown and compared.
func (s *Service) SetEnabled(on bool) error {
	if on {
		if _, err := LoadOrCreateIdentity(s.dir); err != nil {
			return err
		}
	}
	return s.update(func(p *config.Peers) bool {
		if p.Enabled == on {
			return false
		}
		p.Enabled = on
		return true
	})
}

// SetName sets how this Deck introduces itself to peers; "" means the host
// name. Open links reconnect under the new name.
func (s *Service) SetName(name string) error {
	name = strings.TrimSpace(name)
	if name != "" && !config.ValidPeerName(name) {
		return fmt.Errorf("%q cannot be a name: use 1-32 of a-z, 0-9, '-', '_', '.'", truncate(name, 40))
	}
	return s.update(func(p *config.Peers) bool {
		if p.Name == name {
			return false
		}
		p.Name = name
		return true
	})
}

// SetListen turns the listener on or off and, when addr is set, moves it.
func (s *Service) SetListen(enabled bool, addr string) error {
	addr = strings.TrimSpace(addr)
	if addr != "" {
		if err := config.ValidPeerAddr(addr); err != nil {
			return fmt.Errorf("%q is not an address to listen on: %v (want host:port)", truncate(addr, 60), err)
		}
	}
	return s.update(func(p *config.Peers) bool {
		next := p.Listen
		next.Enabled = enabled
		if addr != "" {
			next.Addr = addr
		}
		if next == p.Listen {
			return false
		}
		p.Listen = next
		return true
	})
}

// SetRole sets a peer's role here, in its pin and its config row (made when
// a pinned peer has none).
func (s *Service) SetRole(name, role string) error {
	if role != config.PeerRoleObserver && role != config.PeerRoleContractor {
		return fmt.Errorf("role must be observer or contractor, not %q", truncate(role, 20))
	}
	store := OpenStore(s.dir)
	pin, pinned, err := store.Get(name)
	if err != nil {
		return err
	}
	if pinned && pin.Role != role {
		pin.Role = role
		if err := store.Pin(pin); err != nil {
			return err
		}
	}
	return s.update(func(p *config.Peers) bool {
		for i := range p.Remote {
			if r := &p.Remote[i]; r.Name == name {
				if r.Role == role {
					return false
				}
				r.Role = role
				return true
			}
		}
		if !pinned {
			return false
		}
		p.Remote = append(p.Remote, config.PeerRemote{Name: name, Addr: pin.Addr, Role: role})
		return true
	})
}

// SetDial sets whether this Deck keeps a link to a peer open. Dialing needs
// the peer's address.
func (s *Service) SetDial(name string, dial bool) error {
	pin, pinned, err := OpenStore(s.dir).Get(name)
	if err != nil {
		return err
	}
	return s.update(func(p *config.Peers) bool {
		for i := range p.Remote {
			if r := &p.Remote[i]; r.Name == name {
				if r.Dial == dial {
					return false
				}
				if r.Addr == "" {
					r.Addr = pin.Addr
				}
				r.Dial = dial
				return true
			}
		}
		if !pinned || !dial {
			return false
		}
		p.Remote = append(p.Remote, config.PeerRemote{Name: name, Addr: pin.Addr, Dial: true, Role: pin.Role})
		return true
	})
}

// Remove unpins a peer and drops it from the config, closing its links.
func (s *Service) Remove(name string) error {
	unpinned, unconfigured, cfgErr, err := removePeer(s.dir, s.cfgPath, name)
	if err != nil {
		return err
	}
	if cfgErr != nil {
		return fmt.Errorf("unpinned %q, but could not update %s: %w", name, s.cfgPath, cfgErr)
	}
	if !unpinned && !unconfigured {
		return fmt.Errorf("no peer %q", name)
	}
	return s.Reload()
}

// PingResult is one quick check of a peer, sanitized for display.
type PingResult struct {
	Latency time.Duration
	Tools   []string
	Status  string
}

// Ping connects to a pinned peer and reports its latency, tools and status.
func (s *Service) Ping(ctx context.Context, name string) (PingResult, error) {
	p, _ := s.loadPeers()
	r, err := pingPinned(ctx, s.dir, name, SelfName(p), 10*time.Second)
	if err != nil {
		return PingResult{}, err
	}
	out := PingResult{Latency: r.Latency, Status: Sanitize(truncate(r.Status, 400))}
	for _, t := range r.Tools {
		out.Tools = append(out.Tools, Sanitize(truncate(t, 64)))
	}
	return out, nil
}

// WithDefaultPort adds the default peer port to an address that names none.
func WithDefaultPort(addr string) string {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return net.JoinHostPort(addr, config.DefaultPeerPort)
	}
	return addr
}

// PairAddr is where `deck peer pair` and Settings listen for a pairing:
// peers.listen.addr, else every interface on the default port.
func PairAddr(p *config.Peers) string {
	if p != nil && p.Listen.Addr != "" {
		return p.Listen.Addr
	}
	return ":" + config.DefaultPeerPort
}

// PairWait is a pairing listener started with StartPairing. Done gets one
// outcome and is then closed; Cancel ends the pairing early.
type PairWait struct {
	Code        string
	Addr        string // the address it listens on
	Fingerprint string // this Deck's
	Name        string // this Deck's name, as the other Deck will see it
	Expires     time.Time
	Done        <-chan PairOutcome
	cancel      context.CancelFunc
}

// Cancel stops waiting; Done then gets context.Canceled.
func (w *PairWait) Cancel() {
	if w != nil && w.cancel != nil {
		w.cancel()
	}
}

// PairOutcome is how a pairing listener ended. Result.PeerName comes from
// the other side: Sanitize it before showing it.
type PairOutcome struct {
	Result PairResult
	Err    error
}

// StartPairing listens with a fresh one-time code (what `deck peer pair`
// does) on addr, or PairAddr when empty. When that address is taken (by this
// Deck's own listener, say) it listens on another port of the same host. The
// code is single use and expires after DefaultPairExpiry. Nothing is pinned:
// once the other side proves the code, Pin does that after the person
// compared the fingerprints.
func (s *Service) StartPairing(addr string) (*PairWait, error) {
	p, err := s.loadPeers()
	if err != nil {
		return nil, err
	}
	if addr == "" {
		addr = PairAddr(p)
	}
	id, err := LoadOrCreateIdentity(s.dir)
	if err != nil {
		return nil, err
	}
	code, err := NewPairCode()
	if err != nil {
		return nil, err
	}
	ln, err := listenPair(addr)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(s.base)
	out := make(chan PairOutcome, 1)
	w := &PairWait{
		Code: code, Addr: ln.Addr().String(), Fingerprint: id.Fingerprint(), Name: SelfName(p),
		Expires: time.Now().Add(DefaultPairExpiry), Done: out, cancel: cancel,
	}
	s.setPairing(1)
	go func() {
		defer s.setPairing(-1)
		defer close(out)
		defer cancel()
		res, err := ServePair(ctx, ln, PairOptions{Identity: id, Name: w.Name, Code: code, Expiry: DefaultPairExpiry, Logger: s.log})
		out <- PairOutcome{Result: res, Err: err}
	}()
	return w, nil
}

// listenPair listens on addr, or on a free port of its host when addr is
// taken.
func listenPair(addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err == nil {
		return ln, nil
	}
	host, _, serr := net.SplitHostPort(addr)
	if serr != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	if ln, err2 := net.Listen("tcp", net.JoinHostPort(host, "0")); err2 == nil {
		return ln, nil
	}
	return nil, fmt.Errorf("listen %s: %w", addr, err)
}

// PairDial proves code to the Deck waiting at addr (what `deck peer add`
// does) and returns both fingerprints for the person to compare. Nothing is
// pinned until Pin.
func (s *Service) PairDial(ctx context.Context, addr, code string) (PairResult, error) {
	addr = WithDefaultPort(strings.TrimSpace(addr))
	if err := config.ValidPeerAddr(addr); err != nil {
		return PairResult{}, fmt.Errorf("%q is not an address: %v (want host:port)", truncate(addr, 60), err)
	}
	p, err := s.loadPeers()
	if err != nil {
		return PairResult{}, err
	}
	id, err := LoadOrCreateIdentity(s.dir)
	if err != nil {
		return PairResult{}, err
	}
	s.setPairing(1)
	defer s.setPairing(-1)
	return DialPair(ctx, addr, PairOptions{Identity: id, Name: SelfName(p), Code: code})
}

// PinRequest pins a Deck that proved the pairing code, once the person
// confirmed both fingerprints match.
type PinRequest struct {
	Name string
	Addr string // where to dial it; "" for a Deck that only dials in
	Role string // "" means observer
	Dial bool   // keep a link to it open; needs Addr
	// Replace allows pinning over a different key already pinned under
	// Name. Without it that fails with ErrPinTaken.
	Replace bool
	Result  PairResult
}

// Pin pins r.Result's key under r.Name, records the peer in the config and
// puts it in effect.
func (s *Service) Pin(r PinRequest) error {
	if !config.ValidPeerName(r.Name) {
		return fmt.Errorf("%q cannot name a peer: use 1-32 of a-z, 0-9, '-', '_', '.'", truncate(r.Name, 40))
	}
	if !validRole(r.Role) {
		return fmt.Errorf("role must be observer or contractor, not %q", truncate(r.Role, 20))
	}
	if r.Result.PeerKey == nil {
		return errors.New("peer: nothing to pin: pairing did not finish")
	}
	p, err := s.loadPeers()
	if err != nil {
		return err
	}
	if r.Name == SelfName(p) {
		return fmt.Errorf("%q is this Deck's own name; pick another", r.Name)
	}
	store := OpenStore(s.dir)
	old, swap, err := pinState(store, r.Name, r.Result.PeerFingerprint)
	if err != nil {
		return err
	}
	if swap && !r.Replace {
		return fmt.Errorf("%w: %q is paired with another key (%s)", ErrPinTaken, r.Name, old.Fingerprint)
	}
	if r.Addr == "" {
		// Re-paired from this side: keep where it was dialed.
		if cur, ok := p.Find(r.Name); ok && cur.Addr != "" {
			r.Addr, r.Dial = cur.Addr, cur.Dial
		} else {
			r.Addr = old.Addr
		}
	}
	if r.Addr != "" {
		if err := config.ValidPeerAddr(r.Addr); err != nil {
			return fmt.Errorf("%q is not an address: %v (want host:port)", truncate(r.Addr, 60), err)
		}
	}
	if r.Dial && r.Addr == "" {
		return errors.New("dialing a peer needs its address")
	}
	if err := savePin(store, NewPinned(r.Name, r.Addr, r.Role, r.Result.PeerKey), swap); err != nil {
		return err
	}
	if s.cfgPath != "" {
		if _, err := SetRemote(s.cfgPath, config.PeerRemote{Name: r.Name, Addr: r.Addr, Dial: r.Dial, Role: r.Role}); err != nil {
			return fmt.Errorf("pinned %q, but could not add it to %s: %w", r.Name, s.cfgPath, err)
		}
	}
	return s.Reload()
}

// Snapshot is the peer link as a status view shows it. Every string in it is
// validated config, sanitized peer text or public (fingerprints); no key or
// code is ever in it.
type Snapshot struct {
	Version     uint64 // pass to Wait to hear of the next change
	Enabled     bool
	Name        string // this Deck's peer name
	HostName    bool   // Name comes from the host name (peers.name is unset)
	Fingerprint string // "" until this Deck's key is made
	Listen      config.PeerListen
	Listening   string // the bound address; "" when not listening
	ListenErr   string
	Running     bool // there is a listener or a peer to dial
	Pairing     bool // a pairing exchange is in progress
	// Pending is set when the peers section on disk asks for something else
	// than what runs (on/off, listener or name edited outside Deck). Reload,
	// or a restart, puts it in effect.
	Pending bool
	Err     string // the config or the pins could not be read, or the last change failed
	Peers   []PeerState
}

// PeerState is one paired or configured peer.
type PeerState struct {
	Name        string
	Addr        string // "" for a peer that only dials in
	Role        string
	Fingerprint string // "" when not pinned
	Dial        bool
	Pinned      bool // its key is pinned: it can connect
	Configured  bool // it has a peers.remote row
	Link        Link
}

// Snapshot reads the config, the pins and the link state.
func (s *Service) Snapshot() Snapshot {
	s.mu.Lock()
	snap := Snapshot{Version: s.ver, Pairing: s.pairing > 0}
	mgr, applied, applyErr := s.mgr, s.applied, s.applyErr
	s.mu.Unlock()

	var errs []string
	p, err := s.loadPeers()
	if err != nil {
		errs = append(errs, err.Error())
		p = applied
	}
	if p == nil {
		p = &config.Peers{}
	}
	snap.Enabled, snap.Name, snap.HostName, snap.Listen = p.Enabled, SelfName(p), p.Name == "", p.Listen
	snap.Pending = err == nil && !sameRuntime(p, applied)
	if id, err := LoadIdentity(s.dir); err == nil {
		snap.Fingerprint = id.Fingerprint()
	} else if !errors.Is(err, ErrNoIdentity) {
		errs = append(errs, err.Error())
	}
	var links map[string]Link
	if mgr != nil {
		snap.Running, snap.Listening, links = mgr.Running(), mgr.Addr(), mgr.Links()
		if e := mgr.ListenErr(); e != nil {
			snap.ListenErr = Sanitize(e.Error())
		}
	}
	if applyErr != nil && snap.ListenErr == "" {
		errs = append(errs, applyErr.Error())
	}

	byName := map[string]*PeerState{}
	pins, err := OpenStore(s.dir).List()
	if err != nil {
		errs = append(errs, err.Error())
	}
	for _, pin := range pins {
		byName[pin.Name] = &PeerState{Name: pin.Name, Addr: pin.Addr, Role: pin.Role, Fingerprint: pin.Fingerprint, Pinned: true}
	}
	for _, r := range p.Remote {
		ps := byName[r.Name]
		if ps == nil {
			ps = &PeerState{Name: r.Name}
			byName[r.Name] = ps
		}
		ps.Configured, ps.Dial, ps.Role = true, r.Dial, r.RoleOrDefault()
		if r.Addr != "" {
			ps.Addr = r.Addr
		}
	}
	for _, ps := range byName {
		ps.Addr = Sanitize(ps.Addr)
		if ps.Role == "" {
			ps.Role = config.PeerRoleObserver
		}
		ps.Link = links[ps.Name]
		ps.Link.Status.LastError = Sanitize(truncate(ps.Link.Status.LastError, 200))
		snap.Peers = append(snap.Peers, *ps)
	}
	sort.Slice(snap.Peers, func(i, j int) bool { return snap.Peers[i].Name < snap.Peers[j].Name })
	snap.Err = Sanitize(strings.Join(errs, "; "))
	return snap
}

// sameRuntime reports whether a and b ask for the same link: on/off, name
// and listener.
func sameRuntime(a, b *config.Peers) bool {
	if a == nil {
		a = &config.Peers{}
	}
	if b == nil {
		b = &config.Peers{}
	}
	return a.Enabled == b.Enabled && SelfName(a) == SelfName(b) && a.Listen == b.Listen
}
