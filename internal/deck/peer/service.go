package peer

import (
	"context"
	"errors"
	"fmt"
	"github.com/dev-dami/deck/internal/deck/sub"
	"log/slog"
	"net"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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
	subStore     func() *sub.Store
	base         context.Context
	cancel       context.CancelFunc

	// applyMu serializes making and applying the Manager.
	applyMu sync.Mutex

	mu       sync.Mutex
	mgr      *Manager
	applied  *config.Peers // the section last put in effect
	applyErr error
	pairing  int       // pairing exchanges in progress
	wait     *PairWait // the code this Service is waiting with, if any
	ver      uint64
	wake     chan struct{} // closed and replaced on every change

	// team is the team chat and the capsule requests.
	team *Team
	// out holds the calls waiting for a teammate (outbox.go); flushing is
	// set while a loop delivers them.
	out      *outbox
	flushing atomic.Bool
	// local is this Deck's own work; presence what each teammate said of
	// its own, by name (presence.go).
	local    Local
	presence map[string]Presence
	// contracts are the code contracts both ways (contract.go); hooks run
	// them in the engine's threads.
	contracts *contractBook
	hooks     ContractHooks
	answerMu  sync.Mutex
	shared    Shared
	// draining is set by Drain: teammates can start no new work here.
	draining atomic.Bool
	// audit logs every event of the ops contracts (audit.go).
	audit *auditLog
	// ctlWG counts the control socket's goroutines (control.go).
	ctlWG   sync.WaitGroup
	ctlOnce sync.Once
}

// ServiceOptions configures a Service.
type ServiceOptions struct {
	// Dir is the peers directory; empty means DefaultDir.
	Dir string
	// ConfigPath is Deck's config.json. Without it nothing can be changed.
	ConfigPath string
	Logger     *slog.Logger
	// SubStore, when set, is the Source Update Bridge store published
	// capsules are served from (sub.closure, sub.objects).
	SubStore func() *sub.Store
	// Local is this Deck's own work, for peer.status; zero tells nothing.
	Local Local
	// Contracts runs the contracts this Deck's person accepts, and hears of
	// the ones it proposed; zero runs none.
	Contracts ContractHooks
	// Shared serves this Deck's shared threads to teammates; nil serves
	// none.
	Shared Shared
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
	s := &Service{dir: o.Dir, cfgPath: o.ConfigPath, log: o.Logger, subStore: o.SubStore, local: o.Local, hooks: o.Contracts, shared: o.Shared, base: ctx, cancel: cancel, wake: make(chan struct{})}
	s.team = NewTeam(o.Dir, s.changed)
	s.out = newOutbox(o.Dir)
	s.contracts = newContractBook(o.Dir)
	s.audit = newAuditLog(o.Dir)
	return s, nil
}

// Start puts p, the peers section Deck started with, in effect. With peers
// off it makes nothing: no key, no listener, no goroutines.
func (s *Service) Start(p *config.Peers) error {
	s.ctlOnce.Do(s.startControl)
	err := s.apply(p)
	if p != nil && p.Enabled {
		s.flushSoon() // what waited when Deck last stopped
	}
	return err
}

// Close ends any pairing and stops the link.
func (s *Service) Close() {
	s.cancel()
	s.ctlWG.Wait()
	s.stopWait()
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	s.mu.Lock()
	mgr := s.mgr
	s.mu.Unlock()
	if mgr != nil {
		mgr.Stop()
	}
}

// errDraining refuses new work while the Service drains.
var errDraining = errors.New("peer: this Deck is shutting down and takes no new work")

// Drain makes teammates unable to start new work here (contract.propose,
// thread.post) before the Deck stops. The link stays up, so work under way
// can still report and the outbox can still deliver.
func (s *Service) Drain() { s.draining.Store(true) }

// Waiting is how many calls wait in the outbox for a teammate.
func (s *Service) Waiting() int { return s.out.len() }

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
		mgr, err = NewManager(ManagerOptions{Dir: s.dir, Peers: p, ConfigPath: s.cfgPath, OnChange: s.changed, Logger: s.log, SubStore: s.subStore, Team: s.team, Local: s.local,
			Granted: s.granted, Shared: s.shared, Extra: func(h Handler) Handler { return &ContractHandler{Base: h, S: s} }})
		if err != nil {
			break
		}
		s.mu.Lock()
		s.mgr = mgr
		s.mu.Unlock()
		err = mgr.Start(s.base)
		go s.presenceLoop()
		go s.contractLoop()
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

// SetRelay sets whether this Deck relays links and pairings between two
// peers pinned here that cannot reach each other (peers.relay). It sees
// only ciphertext. Turning it off ends the links it relays.
func (s *Service) SetRelay(on bool) error {
	return s.update(func(p *config.Peers) bool {
		if p.Relay == on {
			return false
		}
		p.Relay = on
		return true
	})
}

// SetVia sets the peer that relays this Deck's link to name
// (peers.remote[].via); "" dials it directly. A relay keeps the link open
// (dial on); with no relay and no address there is nothing to dial, so dial
// goes off.
func (s *Service) SetVia(name, via string) error {
	store := OpenStore(s.dir)
	pin, pinned, err := store.Get(name)
	if err != nil {
		return err
	}
	if !pinned {
		return fmt.Errorf("no teammate %q is paired here", truncate(name, 40))
	}
	var vpin Pinned
	if via != "" {
		if via == name {
			return fmt.Errorf("%q cannot relay its own link", name)
		}
		var ok bool
		if vpin, ok, err = store.Get(via); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("no teammate %q is paired here to relay through", truncate(via, 40))
		}
	}
	return s.update(func(p *config.Peers) bool { return setVia(p, pin, vpin, via) })
}

// setVia sets the relay of pin's row in p to via, whose pin is vpin ("" for
// direct), making the rows it needs.
func setVia(p *config.Peers, pin, vpin Pinned, via string) bool {
	if via != "" {
		ensureRemote(p, vpin)
	}
	r := ensureRemote(p, pin)
	if r.Via == via {
		return false
	}
	r.Via = via
	switch {
	case via != "":
		r.Dial = true
	case r.Addr == "":
		r.Dial = false
	}
	return true
}

// SetMayRelay sets whether name may bring links and pairings from other
// peers pinned here (peers.remote[].relay): on for the Deck that relays to
// this one.
func (s *Service) SetMayRelay(name string, on bool) error {
	pin, pinned, err := OpenStore(s.dir).Get(name)
	if err != nil {
		return err
	}
	if !pinned {
		return fmt.Errorf("no teammate %q is paired here", truncate(name, 40))
	}
	return s.update(func(p *config.Peers) bool { return setMayRelay(p, pin, on) })
}

// setMayRelay sets whether the pinned peer pin may relay links to this Deck.
func setMayRelay(p *config.Peers, pin Pinned, on bool) bool {
	r := ensureRemote(p, pin)
	if r.Relay == on {
		return false
	}
	r.Relay = on
	return true
}

// ensureRemote is p's row for the pinned peer pin, made from the pin when
// it has none.
func ensureRemote(p *config.Peers, pin Pinned) *config.PeerRemote {
	for i := range p.Remote {
		if p.Remote[i].Name == pin.Name {
			return &p.Remote[i]
		}
	}
	p.Remote = append(p.Remote, config.PeerRemote{Name: pin.Name, Addr: pin.Addr, Role: pin.Role})
	return &p.Remote[len(p.Remote)-1]
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

// PairAddr is where a pairing listener of its own listens (when this Deck's
// listener is not running): peers.listen.addr, else every interface on the
// default port.
func PairAddr(p *config.Peers) string {
	if p != nil && p.Listen.Addr != "" {
		return p.Listen.Addr
	}
	return ":" + config.DefaultPeerPort
}

// PairWait is a pairing code armed and waiting for the other Deck. Done gets
// one outcome and is then closed; Cancel ends the pairing early. Code is the
// one-time secret: show it to the person, never log it.
type PairWait struct {
	Code        string
	Addr        string // the address it is served on
	Fingerprint string // this Deck's
	Name        string // this Deck's name, as the other Deck will see it
	// Hosts are the addresses another machine may reach it by, best first
	// (InviteHosts); the invite carries them.
	Hosts []string
	// Reachable is false when Addr is loopback: no other machine can
	// connect.
	Reachable bool
	// Shared is set when the code is served by this Deck's own listener
	// rather than a listener of its own.
	Shared  bool
	Expires time.Time
	Done    <-chan PairOutcome
	cancel  context.CancelFunc
	ended   <-chan struct{} // closed once it stopped listening and Done has its outcome
}

// Cancel stops waiting; Done then gets context.Canceled.
func (w *PairWait) Cancel() {
	if w != nil && w.cancel != nil {
		w.cancel()
	}
}

// over reports whether the wait has ended.
func (w *PairWait) over() bool {
	if w.ended == nil {
		return false
	}
	select {
	case <-w.ended:
		return true
	default:
		return false
	}
}

// PairOutcome is how a pairing listener ended. Result.PeerName comes from
// the other side: Sanitize it before showing it.
type PairOutcome struct {
	Result PairResult
	Err    error
}

// errNoListener: the Manager has no listener to arm a code on.
var errNoListener = errors.New("peer: not listening")

// StartPairListen arms a fresh one-time code (what Settings and `deck peer
// pair` do). While this Deck's own listener runs the code is served there,
// on the same port as the link; otherwise a listener of its own is opened on
// PairAddr, and a port already taken is an error, never another port. While
// the link runs, the code is also served to pairings a peer relays here, so
// a Deck behind NAT pairs through a Deck both are paired with. The code is
// single use and expires after DefaultPairExpiry. A pairing this
// Service started before is stopped first. Nothing is pinned: once the
// other side proves the code, Pin does that after the person compared the
// fingerprints.
func (s *Service) StartPairListen(ctx context.Context) (*PairWait, error) {
	s.stopWait()
	p, err := s.loadPeers()
	if err != nil {
		return nil, err
	}
	if mgr := s.Manager(); mgr != nil {
		ctx, cancel := s.bound(ctx)
		w, err := mgr.ArmPair(ctx, PairOptions{Logger: s.log})
		if err == nil {
			w.Hosts = InviteHosts(w.Addr)
			s.track(w, cancel)
			return w, nil
		}
		if !errors.Is(err, errNoListener) {
			cancel()
			return nil, err
		}
		if !mgr.Running() {
			// The link was turned off: no relay can bring a pairing.
			cancel()
			return s.StartPairing(PairAddr(p))
		}
		ln, err := ListenPair(PairAddr(p))
		if err != nil {
			cancel()
			return nil, err
		}
		if w, err = mgr.ArmPairOn(ctx, ln, PairOptions{Logger: s.log}); err != nil {
			cancel()
			return nil, err
		}
		w.Hosts = InviteHosts(w.Addr)
		s.track(w, cancel)
		return w, nil
	}
	return s.StartPairing(PairAddr(p))
}

// StartPairing waits for a pairing on a listener of its own on addr: for a
// Deck whose own listener is not running. An address already in use is an
// error.
func (s *Service) StartPairing(addr string) (*PairWait, error) {
	p, err := s.loadPeers()
	if err != nil {
		return nil, err
	}
	id, err := LoadOrCreateIdentity(s.dir)
	if err != nil {
		return nil, err
	}
	code, err := NewPairCode()
	if err != nil {
		return nil, err
	}
	ln, err := ListenPair(addr)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.bound(context.Background())
	out := make(chan PairOutcome, 1)
	ended := make(chan struct{})
	w := &PairWait{
		Code: code, Addr: ln.Addr().String(), Fingerprint: id.Fingerprint(), Name: SelfName(p),
		Expires: time.Now().Add(DefaultPairExpiry), Done: out, cancel: cancel, ended: ended,
	}
	w.Reachable, w.Hosts = Reachable(w.Addr), InviteHosts(w.Addr)
	s.mu.Lock()
	s.wait = w
	s.mu.Unlock()
	s.setPairing(1)
	go func() {
		defer s.setPairing(-1)
		defer close(ended)
		defer close(out)
		defer cancel()
		res, err := ServePair(ctx, ln, PairOptions{Identity: id, Name: w.Name, Code: code, Expiry: DefaultPairExpiry, Logger: s.log})
		out <- PairOutcome{Result: res, Err: err}
	}()
	return w, nil
}

// ListenPair opens a pairing listener of its own on addr. A port that is
// taken (by a Deck already listening there, say) is reported as such; it
// never falls back to another port the other Deck would not know.
func ListenPair(addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err == nil {
		return ln, nil
	}
	return nil, fmt.Errorf("cannot wait for a code on %s: %v. Port %s is probably in use by another program (or another Deck): free it, or pick another port in Settings › Team › Listen on (deck peer pair --addr)",
		addr, err, portOfAddr(addr))
}

// bound is ctx, also ended when the Service closes.
func (s *Service) bound(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.base, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

// track remembers w, armed on the Manager's listener, as this Service's
// pairing, and shows it in the Snapshot until it ends.
func (s *Service) track(w *PairWait, cancel context.CancelFunc) {
	s.mu.Lock()
	s.wait = w
	s.mu.Unlock()
	s.setPairing(1)
	ended := w.ended
	go func() {
		<-ended
		cancel()
		s.setPairing(-1)
	}()
}

// stopWait ends the pairing this Service is waiting on, if any, and waits
// until it has let go of its port.
func (s *Service) stopWait() {
	s.mu.Lock()
	w := s.wait
	s.wait = nil
	s.mu.Unlock()
	if w != nil {
		w.Cancel()
		if w.ended != nil {
			<-w.ended
		}
	}
}

// PairListenAll moves pairing to every interface, for a wait whose address
// only this machine can reach: it saves Listen on as all interfaces on the
// same port and puts it in effect. A code armed on this Deck's listener
// stays armed through the move and the wait comes back with its new
// address; a listener of its own is reopened with a fresh code. The person
// asked for this: it is never done on its own.
func (s *Service) PairListenAll(w *PairWait) (*PairWait, error) {
	if w == nil {
		return nil, errors.New("peer: no pairing to move")
	}
	all := net.JoinHostPort("0.0.0.0", portOfAddr(w.Addr))
	p, err := s.loadPeers()
	if err != nil {
		return nil, err
	}
	if !w.Shared {
		w.Cancel()
		if w.ended != nil {
			<-w.ended
		}
		enabled := p != nil && p.Listen.Enabled
		if err := s.SetListen(enabled, all); err != nil {
			return nil, err
		}
		return s.StartPairListen(context.Background())
	}
	if err := s.SetListen(true, all); err != nil {
		return nil, err
	}
	mgr := s.Manager()
	if mgr == nil || mgr.Addr() == "" || w.over() {
		return s.StartPairListen(context.Background())
	}
	moved := *w
	moved.Addr = mgr.Addr()
	moved.Reachable, moved.Hosts = Reachable(moved.Addr), InviteHosts(moved.Addr)
	s.mu.Lock()
	if s.wait == w {
		s.wait = &moved
	}
	s.mu.Unlock()
	return &moved, nil
}

// PairDial proves code to the Deck waiting at addr (what `deck peer add`
// does) and returns both fingerprints for the person to compare. expectFP,
// from an invite, is the fingerprint the other Deck must have; "" skips that
// check. Nothing is pinned until Pin.
func (s *Service) PairDial(ctx context.Context, addr, code, expectFP string) (PairResult, error) {
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
	o := PairOptions{Identity: id, Name: SelfName(p), Code: code, ExpectFingerprint: expectFP}
	// With the invite's fingerprint, a Deck that does not answer may still
	// be reached through a teammate both are paired with.
	var relays []*Conn
	if mgr := s.Manager(); mgr != nil && expectFP != "" {
		for _, c := range mgr.relayCandidates() {
			if c.Peer().Fingerprint != expectFP {
				relays = append(relays, c)
			}
		}
	}
	if len(relays) > 0 {
		o.connectTimeout = pairQuickConnect
	}
	res, err := DialPair(ctx, addr, o)
	if err == nil || len(relays) == 0 || !errors.Is(err, ErrPairNoAnswer) {
		return res, err
	}
	miss := &RelayMissError{Direct: err}
	for _, c := range relays {
		r, rerr := DialPairVia(ctx, c, o)
		if rerr == nil {
			return r, nil
		}
		if !errors.Is(rerr, ErrPairRelay) {
			return PairResult{}, rerr // it reached the Deck: wrong code, say
		}
		miss.Relays = append(miss.Relays, rerr)
	}
	return PairResult{}, miss
}

// pairQuickConnect bounds the direct TCP connect of a pairing that can still
// go through a relay after it.
const pairQuickConnect = 4 * time.Second

// RelayMissError is a pairing that got no direct answer, and that no relay
// could bring to the other Deck.
type RelayMissError struct {
	Direct error   // the direct attempt (ErrPairNoAnswer)
	Relays []error // one ErrPairRelay per relay tried
}

func (e *RelayMissError) Error() string {
	msgs := []string{e.Direct.Error()}
	for _, r := range e.Relays {
		msgs = append(msgs, r.Error())
	}
	return strings.Join(msgs, "; ")
}

func (e *RelayMissError) Unwrap() []error { return append([]error{e.Direct}, e.Relays...) }

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
	// Paired through a relay: the dialing side reaches it through that relay
	// (the invite's address did not answer), and the waiting side lets the
	// relay bring its links.
	via, relayRow := "", ""
	if r.Result.Via != "" {
		if r.Result.Dialed {
			// With no address, the relay is the only way: keep the link open.
			via, r.Addr, r.Dial = r.Result.Via, "", true
		} else {
			relayRow = r.Result.Via
		}
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
	if r.Dial && r.Addr == "" && via == "" {
		return errors.New("dialing a peer needs its address")
	}
	if err := savePin(store, NewPinned(r.Name, r.Addr, r.Role, r.Result.PeerKey), swap); err != nil {
		return err
	}
	if s.cfgPath != "" {
		if _, err := config.UpdatePeers(s.cfgPath, func(p *config.Peers) bool {
			if via != "" {
				if vp, ok, _ := store.Get(via); ok {
					ensureRemote(p, vp)
				}
			}
			changed := setRemote(p, config.PeerRemote{Name: r.Name, Addr: r.Addr, Dial: r.Dial, Role: r.Role, Via: via})
			if relayRow != "" {
				if rp, ok, _ := store.Get(relayRow); ok {
					if rr := ensureRemote(p, rp); !rr.Relay {
						rr.Relay, changed = true, true
					}
				}
			}
			return changed
		}); err != nil {
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
	// Relay is set when this Deck relays for its peers (peers.relay);
	// Relaying is how many links it relays now.
	Relay    bool
	Relaying int
	Err      string // the config or the pins could not be read, or the last change failed
	Peers    []PeerState
	// Chat is the team chat, oldest first; Requests the capsules teammates
	// asked this Deck for.
	Chat     []ChatMessage
	Requests []CapsuleRequest
	// Contracts are the code contracts both ways, oldest first.
	Contracts []Contract
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
	// Via is the relay this Deck dials it through (peers.remote[].via);
	// Relay is set when it may relay links to this Deck.
	Via   string
	Relay bool
	Link  Link
	// Presence is what it said of its work the last time it was asked; nil
	// while no link to it is up.
	Presence *Presence
}

// Snapshot reads the config, the pins and the link state.
func (s *Service) Snapshot() Snapshot {
	s.mu.Lock()
	snap := Snapshot{Version: s.ver, Pairing: s.pairing > 0}
	mgr, applied, applyErr := s.mgr, s.applied, s.applyErr
	presence := s.presence
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
	snap.Enabled, snap.Name, snap.HostName, snap.Listen, snap.Relay = p.Enabled, SelfName(p), p.Name == "", p.Listen, p.Relay
	snap.Pending = err == nil && !sameRuntime(p, applied)
	if id, err := LoadIdentity(s.dir); err == nil {
		snap.Fingerprint = id.Fingerprint()
	} else if !errors.Is(err, ErrNoIdentity) {
		errs = append(errs, err.Error())
	}
	var links map[string]Link
	if mgr != nil {
		snap.Running, snap.Listening, links = mgr.Running(), mgr.Addr(), mgr.Links()
		snap.Relaying = mgr.Relaying()
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
		ps.Configured, ps.Dial, ps.Role, ps.Via, ps.Relay = true, r.Dial, r.RoleOrDefault(), r.Via, r.Relay
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
		if pr, ok := presence[ps.Name]; ok {
			ps.Presence = &pr
		}
		ps.Link.Status.LastError = Sanitize(truncate(ps.Link.Status.LastError, 200))
		snap.Peers = append(snap.Peers, *ps)
	}
	sort.Slice(snap.Peers, func(i, j int) bool { return snap.Peers[i].Name < snap.Peers[j].Name })
	snap.Err = Sanitize(strings.Join(errs, "; "))
	if s.team != nil {
		snap.Chat, snap.Requests = s.team.Chat(), s.team.Requests()
	}
	snap.Contracts = s.contracts.all()
	return snap
}

// sameRuntime reports whether a and b ask for the same link: on/off, name,
// listener, relaying and remotes (`deck peer relay` edits those outside
// Deck).
func sameRuntime(a, b *config.Peers) bool {
	if a == nil {
		a = &config.Peers{}
	}
	if b == nil {
		b = &config.Peers{}
	}
	return a.Enabled == b.Enabled && SelfName(a) == SelfName(b) && a.Listen == b.Listen &&
		a.Relay == b.Relay && (len(a.Remote) == 0 && len(b.Remote) == 0 || reflect.DeepEqual(a.Remote, b.Remote))
}
