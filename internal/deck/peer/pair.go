package peer

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/ssh"

	"github.com/dev-dami/deck/internal/deck/config"
)

// Pairing: one side arms a one-time code and waits, the other dials it with
// the code typed at its prompt (or taken from DECK_PEER_CODE or an invite,
// never the command line). The two run an SSH handshake in which each side
// accepts any ed25519 key, then prove to each other that they know the code
// over a "deck-pair" channel:
//
//	K   = argon2id(code, salt = SHA256("deck-pair-v1" || session id))
//	mac = HMAC-SHA256(K, label || session id || host key || client key || name)
//
// The dialer sends its name and mac first; the listener checks it and only
// then answers with its own. The session id and both keys are bound in, so a
// man in the middle, who has two sessions with different ids and must
// substitute its own keys, needs the code to produce either mac. The code is
// short, so the KDF is memory-hard and salted per session: guessing it
// offline from a captured mac is far too slow to finish while the code is
// valid. On top of that each side prints both fingerprints for the people to
// compare, and nothing is pinned until they confirm.
//
// A running Deck serves pairing on its own listener (Server): while a code
// is armed it lets in the SSH user PairUser with any ed25519 key, and gives
// that connection the deck-pair channel and nothing else. Without a listener
// ServePair serves one code on a listener of its own.

// PairChannelType is the channel the pairing exchange runs on: the only one
// a pairing connection gets, and one a pinned link never gets.
const PairChannelType = "deck-pair"

// PairUser is the SSH user a pairing dialer logs in as. A Server lets it in
// only while a pairing code is armed, and then only to PairChannelType.
const PairUser = "deck-pair"

// Pairing defaults.
const (
	DefaultPairExpiry   = 5 * time.Minute
	DefaultPairAttempts = 3
	// PairDialTimeout bounds the dialing side from connect to the other
	// side's proof: a Deck that does not answer in that time is reported as
	// not answering rather than waited on.
	PairDialTimeout = 12 * time.Second
	pairConnTimeout = 30 * time.Second
	pairMaxMessage  = 4 << 10
	pairVersion     = 1
	// At most pairMaxConns pairing connections are open at once, and
	// pairMaxPerIP from one IP, so idle sockets cannot keep the real dialer
	// out.
	pairMaxConns = 8
	pairMaxPerIP = 2
)

// pairHelloTimeout is how long a pairing connection has from connect to its
// hello (a var for tests).
var pairHelloTimeout = 5 * time.Second

// codeAlphabet has no 0/O, 1/I/L or U, so a code read aloud survives.
const codeAlphabet = "23456789ABCDEFGHJKMNPQRSTVWXYZ"

// codeLen is the code's length without the dash: 30^10, about 49 bits.
const codeLen = 10

// Pairing errors.
var (
	ErrPairExpired  = errors.New("peer: pairing code expired")
	ErrPairAttempts = errors.New("peer: too many wrong pairing attempts; the code is burned")
	ErrPairWrong    = errors.New("peer: wrong pairing code")
	// ErrPairUnproven is returned to the dialer when the listener could not
	// prove it knows the code: either the code was mistyped and the listener
	// refused, or someone in the middle is answering.
	ErrPairUnproven = errors.New("peer: the other side did not prove it knows the code")
	// ErrPairNoAnswer: the dialer got no connection, or no answer in
	// PairDialTimeout (nothing listens there, a firewall drops it, or the
	// Deck there is not accepting connections).
	ErrPairNoAnswer = errors.New("peer: no answer")
	// ErrPairHandshake: something answered, but the SSH handshake with it
	// failed (not a Deck, or an incompatible one).
	ErrPairHandshake = errors.New("peer: pairing handshake failed")
	// ErrPairNotWaiting: a Deck answered but refused the pairing login: it
	// has no code armed (expired, used, or never started).
	ErrPairNotWaiting = errors.New("peer: the other Deck is not waiting for a code")
	// ErrPairFingerprint: the other side's key is not the one the invite
	// named. Nothing was sent that depends on the code.
	ErrPairFingerprint = errors.New("peer: the fingerprint does not match the invite")
)

// kdfParams is the argon2id cost; tests lower it.
var kdfParams = struct {
	time, memory uint32
	threads      uint8
}{time: 1, memory: 64 * 1024, threads: 4}

// NewPairCode returns a fresh random code, formatted XXXXX-XXXXX.
func NewPairCode() (string, error) {
	var b strings.Builder
	buf := make([]byte, 1)
	for b.Len() < codeLen {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		// Reject sampling keeps every symbol equally likely.
		if int(buf[0]) >= 256-256%len(codeAlphabet) {
			continue
		}
		b.WriteByte(codeAlphabet[int(buf[0])%len(codeAlphabet)])
	}
	s := b.String()
	return s[:5] + "-" + s[5:], nil
}

// normalizeCode upper-cases a typed code and drops dashes and spaces.
func normalizeCode(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if r == '-' || r == ' ' || r == '\t' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// validCode reports whether a normalized code could have come from NewPairCode.
func validCode(s string) bool {
	if len(s) != codeLen {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune(codeAlphabet, r) {
			return false
		}
	}
	return true
}

func pairKey(code string, sid []byte) []byte {
	salt := sha256.Sum256(append([]byte("deck-pair-v1"), sid...))
	return argon2.IDKey([]byte(code), salt[:], kdfParams.time, kdfParams.memory, kdfParams.threads, 32)
}

// pairMAC is the proof one side sends. label tells the two directions apart.
func pairMAC(key []byte, label string, sid []byte, hostKey, clientKey ssh.PublicKey, name string) []byte {
	m := hmac.New(sha256.New, key)
	for _, part := range [][]byte{[]byte("deck-pair-v1 " + label), sid, hostKey.Marshal(), clientKey.Marshal(), []byte(name)} {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(part)))
		m.Write(n[:])
		m.Write(part)
	}
	return m.Sum(nil)
}

type pairHello struct {
	V     int    `json:"v"`
	Name  string `json:"name,omitempty"`
	MAC   []byte `json:"mac,omitempty"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// PairResult is what a finished pairing learned about the other side.
type PairResult struct {
	// PeerName is the name the other side gave itself; the person may pick
	// another when pinning.
	PeerName        string
	PeerKey         ssh.PublicKey
	PeerFingerprint string
	OwnFingerprint  string
	RemoteAddr      string
}

// PairOptions configures both sides of pairing.
type PairOptions struct {
	Identity *Identity
	// Name is this Deck's name, sent to the other side.
	Name string
	// Code is the listener's code (from NewPairCode) or the code typed on the
	// dialing side.
	Code string
	// ExpectFingerprint, on the dialing side, is the fingerprint an invite
	// named: any other host key ends the pairing with ErrPairFingerprint
	// before the code is used.
	ExpectFingerprint string
	// Expiry and MaxAttempts bound the listener (5m, 3).
	Expiry      time.Duration
	MaxAttempts int
	Logger      *slog.Logger
}

// pairGate is the state a pairing session's connections share: the per-IP
// and total counts of connections not yet done, and the code's attempts.
// Proofs are checked one at a time under vmu (the KDF is memory-hard, so
// they never run side by side), and only a wrong mac uses an attempt: a
// connection that stalls, speaks junk or never sends a hello costs nothing.
type pairGate struct {
	code string
	max  int

	mu      sync.Mutex
	conns   map[net.Conn]struct{}
	perIP   map[string]int
	closing bool

	vmu   sync.Mutex
	fails int
	used  bool // succeeded or burned: no more proofs are checked
}

// errPairClosed is a connection's error after another one used the code.
var errPairClosed = errors.New("peer: pairing is over")

func (g *pairGate) admit(raw net.Conn, ip string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closing || len(g.conns) >= pairMaxConns || g.perIP[ip] >= pairMaxPerIP {
		return false
	}
	g.conns[raw] = struct{}{}
	g.perIP[ip]++
	return true
}

func (g *pairGate) release(raw net.Conn, ip string) {
	raw.Close()
	g.mu.Lock()
	delete(g.conns, raw)
	if g.perIP[ip]--; g.perIP[ip] <= 0 {
		delete(g.perIP, ip)
	}
	g.mu.Unlock()
}

func (g *pairGate) closeAll() {
	g.mu.Lock()
	g.closing = true
	for c := range g.conns {
		c.Close()
	}
	g.mu.Unlock()
}

// pairDone is one connection's outcome.
type pairDone struct {
	res    PairResult
	burned bool
	err    error
	remote string
}

// pairSession is one armed code: what every connection trying it shares
// (its gate), and where their outcomes go. ServePair runs one on a listener
// of its own; a Server serves the one armed in its pairSlot to PairUser.
type pairSession struct {
	g       *pairGate
	o       PairOptions
	expires time.Time
	results chan pairDone
	quit    chan struct{}
	endOnce sync.Once

	// cancel ends the session's wait and ended is closed once it has ended
	// (armPair sets both; ServePair waits on its own).
	cancel context.CancelFunc
	ended  chan struct{}
}

// newPairSession checks o and fills in its defaults.
func newPairSession(o PairOptions, now time.Time) (*pairSession, error) {
	if o.Identity == nil {
		return nil, errors.New("peer: pairing needs an identity")
	}
	code := normalizeCode(o.Code)
	if !validCode(code) {
		return nil, errors.New("peer: invalid pairing code")
	}
	if o.Expiry <= 0 {
		o.Expiry = DefaultPairExpiry
	}
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = DefaultPairAttempts
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &pairSession{
		g:       &pairGate{code: code, max: o.MaxAttempts, conns: map[net.Conn]struct{}{}, perIP: map[string]int{}},
		o:       o,
		expires: now.Add(o.Expiry),
		results: make(chan pairDone),
		quit:    make(chan struct{}),
		cancel:  func() {},
	}, nil
}

// live reports whether the session still takes proofs.
func (ps *pairSession) live(now time.Time) bool {
	select {
	case <-ps.quit:
		return false
	default:
	}
	return now.Before(ps.expires)
}

// end stops the session: no new connections, and open ones are closed.
func (ps *pairSession) end() {
	ps.endOnce.Do(func() {
		close(ps.quit)
		ps.g.closeAll()
	})
}

// report hands one connection's outcome to wait, unless the session is over.
func (ps *pairSession) report(d pairDone) {
	select {
	case ps.results <- d:
	case <-ps.quit:
	}
}

// wait collects outcomes until one connection proves the code, the code is
// burned, ctx ends, or acceptErr (the listener's, nil for none) fires.
func (ps *pairSession) wait(ctx context.Context, acceptErr <-chan error) (PairResult, error) {
	over := func() error {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return ErrPairExpired
		}
		return ctx.Err()
	}
	for {
		select {
		case <-ctx.Done():
			return PairResult{}, over()
		case err := <-acceptErr:
			if ctx.Err() != nil {
				return PairResult{}, over()
			}
			return PairResult{}, err
		case d := <-ps.results:
			if d.err == nil {
				return d.res, nil
			}
			ps.o.Logger.Debug("peer: pairing attempt failed", "remote", d.remote, "error", d.err)
			if d.burned {
				return PairResult{}, ErrPairAttempts
			}
		}
	}
}

// pairSlot holds the one pairing session a Server serves to PairUser. A
// Manager shares its slot with every Server it starts, so a listener moved
// while a code is armed keeps serving that code. Only this process arms it:
// nothing a peer sends can.
type pairSlot struct {
	mu  sync.Mutex
	ps  *pairSession
	now func() time.Time
}

func newPairSlot(now func() time.Time) *pairSlot {
	if now == nil {
		now = time.Now
	}
	return &pairSlot{now: now}
}

// get is the session to serve now, or nil: none armed, or it has ended or
// expired.
func (s *pairSlot) get() *pairSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ps == nil || !s.ps.live(s.now()) {
		return nil
	}
	return s.ps
}

// arm makes ps the session served; one armed before is cancelled.
func (s *pairSlot) arm(ps *pairSession) {
	s.mu.Lock()
	old := s.ps
	s.ps = ps
	s.mu.Unlock()
	if old != nil {
		old.cancel()
	}
}

// disarm clears ps, unless another session replaced it.
func (s *pairSlot) disarm(ps *pairSession) {
	s.mu.Lock()
	if s.ps == ps {
		s.ps = nil
	}
	s.mu.Unlock()
}

// stop cancels the armed session and waits until it has ended.
func (s *pairSlot) stop() {
	s.mu.Lock()
	ps := s.ps
	s.mu.Unlock()
	if ps == nil {
		return
	}
	ps.cancel()
	if ps.ended != nil {
		<-ps.ended
	}
}

// armPair arms a pairing session with o (a fresh code when o.Code is empty)
// in slot, and runs it until one Deck proves the code, the code is burned or
// expires, ctx ends or the PairWait is cancelled; then it is disarmed. Addr
// and Reachable are the caller's to fill in.
func armPair(ctx context.Context, slot *pairSlot, o PairOptions) (*PairWait, error) {
	if o.Code == "" {
		code, err := NewPairCode()
		if err != nil {
			return nil, err
		}
		o.Code = code
	}
	ps, err := newPairSession(o, slot.now())
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, ps.o.Expiry)
	ps.cancel, ps.ended = cancel, make(chan struct{})
	slot.arm(ps)
	out := make(chan PairOutcome, 1)
	go func() {
		defer close(ps.ended)
		defer close(out)
		defer cancel()
		res, err := ps.wait(ctx, nil)
		slot.disarm(ps)
		ps.end()
		out <- PairOutcome{Result: res, Err: err}
	}()
	return &PairWait{
		Code: o.Code, Fingerprint: o.Identity.Fingerprint(), Name: o.Name,
		Expires: ps.expires, Shared: true, Done: out, cancel: cancel, ended: ps.ended,
	}, nil
}

// ServePair serves pairing on ln, a listener of its own, until one peer
// proves the code. The code is single use: ServePair returns after the first
// success, after MaxAttempts wrong codes, or when the code expires, and
// closes ln. Connections are served side by side, each with
// pairHelloTimeout to reach its proof, so idle connections cannot hold the
// listener.
func ServePair(ctx context.Context, ln net.Listener, o PairOptions) (PairResult, error) {
	defer ln.Close()
	ps, err := newPairSession(o, time.Now())
	if err != nil {
		return PairResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, ps.o.Expiry)
	defer cancel()
	acceptErr := make(chan error, 1)
	var wg sync.WaitGroup
	defer func() {
		ps.end()
		ln.Close()
		wg.Wait()
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			raw, err := ln.Accept()
			if err != nil {
				acceptErr <- err
				return
			}
			ip := lockKey(raw.RemoteAddr())
			if !ps.g.admit(raw, ip) {
				raw.Close()
				continue
			}
			wg.Add(1) // this goroutine still counts, so wg is not at zero
			go func() {
				defer wg.Done()
				defer ps.g.release(raw, ip)
				res, burned, err := pairServeConn(raw, ps)
				ps.report(pairDone{res: res, burned: burned, err: err, remote: raw.RemoteAddr().String()})
			}()
		}
	}()
	return ps.wait(ctx, acceptErr)
}

// pairServerConfig accepts any plain ed25519 client key, handing it on in
// the permissions: the code exchange is what authenticates it.
func pairServerConfig(id *Identity) *ssh.ServerConfig {
	cfg := &ssh.ServerConfig{
		Config:                  baseSSHConfig(),
		PublicKeyAuthAlgorithms: []string{ssh.KeyAlgoED25519},
		MaxAuthTries:            3,
		ServerVersion:           sshVersion,
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if key.Type() != ssh.KeyAlgoED25519 {
				return nil, errNotPinned
			}
			return &ssh.Permissions{Extensions: map[string]string{extPairKey: string(key.Marshal())}}, nil
		},
	}
	cfg.AddHostKey(id.Signer)
	return cfg
}

// pairServeConn runs the listener's side on one connection of ServePair's
// own listener: the handshake, then the exchange.
func pairServeConn(raw net.Conn, ps *pairSession) (res PairResult, burned bool, err error) {
	timer := time.AfterFunc(pairHelloTimeout, func() { raw.Close() })
	defer timer.Stop()
	sc, chans, reqs, err := ssh.NewServerConn(raw, pairServerConfig(ps.o.Identity))
	if err != nil {
		return res, false, err
	}
	clientKey, err := ssh.ParsePublicKey([]byte(sc.Permissions.Extensions[extPairKey]))
	if err != nil {
		closeUnserved(sc, chans, reqs)
		return res, false, err
	}
	return ps.serve(raw, timer, sc, chans, reqs, clientKey)
}

// serve runs the listener's side of the exchange on an authenticated
// pairing connection: it accepts the one deck-pair channel (and refuses
// every other channel and global request but keepalives), checks the
// dialer's proof and answers with its own. timer closes raw; it is moved on
// from the hello timeout to pairConnTimeout once the hello is in. burned
// reports that the code cannot be used any more after this connection
// failed: its wrong proof was the last attempt, or its right one could not
// be answered.
func (ps *pairSession) serve(raw net.Conn, timer *time.Timer, sc *ssh.ServerConn, chans <-chan ssh.NewChannel, reqs <-chan *ssh.Request, clientKey ssh.PublicKey) (res PairResult, burned bool, err error) {
	defer sc.Close()
	go pairGlobal(reqs)
	var ch ssh.Channel
	for nc := range chans {
		if nc.ChannelType() != PairChannelType || ch != nil {
			_ = nc.Reject(ssh.UnknownChannelType, "only deck-pair")
			continue
		}
		var creqs <-chan *ssh.Request
		ch, creqs, err = nc.Accept()
		if err != nil {
			return res, false, err
		}
		go refuseRequests(creqs)
		go func() { // anything after the first channel is refused
			for nc := range chans {
				_ = nc.Reject(ssh.UnknownChannelType, "only deck-pair")
			}
		}()
		break
	}
	if ch == nil {
		return res, false, errors.New("peer: no pairing channel")
	}
	defer ch.Close()
	r := bufio.NewReaderSize(ch, 1024)
	var hello pairHello
	line, err := readFrame(r, pairMaxMessage)
	if err != nil {
		return res, false, err
	}
	if err := json.Unmarshal(line, &hello); err != nil || hello.V != pairVersion || len(hello.MAC) == 0 {
		// Not a guess at the code, so it uses no attempt.
		return res, false, errors.New("peer: bad pairing hello")
	}
	// The hello is in: the rest (KDF, reply, the dialer reading it) gets the
	// longer timeout.
	timer.Reset(pairConnTimeout)
	sid := sc.SessionID()
	g := ps.g
	g.vmu.Lock()
	if g.used {
		g.vmu.Unlock()
		return res, false, errPairClosed
	}
	k := pairKey(g.code, sid)
	want := pairMAC(k, "client", sid, ps.o.Identity.PublicKey(), clientKey, hello.Name)
	ok := hmac.Equal(want, hello.MAC)
	if ok {
		g.used = true // single use, even if this connection fails from here
	} else {
		g.fails++
		burned = g.fails >= g.max
		g.used = burned
	}
	g.vmu.Unlock()
	if !ok {
		_ = writeJSONLine(ch, pairHello{V: pairVersion, Error: "wrong code"})
		return res, burned, ErrPairWrong
	}
	reply := pairHello{V: pairVersion, OK: true, Name: ps.o.Name,
		MAC: pairMAC(k, "server", sid, ps.o.Identity.PublicKey(), clientKey, ps.o.Name)}
	if err := writeJSONLine(ch, reply); err != nil {
		return res, true, err
	}
	// Wait for the dialer to close, so it has read the reply.
	_, _ = r.ReadByte()
	return PairResult{
		PeerName:        hello.Name,
		PeerKey:         clientKey,
		PeerFingerprint: Fingerprint(clientKey),
		OwnFingerprint:  ps.o.Identity.Fingerprint(),
		RemoteAddr:      raw.RemoteAddr().String(),
	}, false, nil
}

// DialPair runs the dialing side against addr with the typed code, within
// PairDialTimeout. Its errors say what went wrong (ErrPairNoAnswer,
// ErrPairHandshake, ErrPairNotWaiting, ErrPairFingerprint, ErrPairWrong,
// ErrPairUnproven) and never contain the code; PairErrText words them for a
// person.
func DialPair(ctx context.Context, addr string, o PairOptions) (PairResult, error) {
	if o.Identity == nil {
		return PairResult{}, errors.New("peer: pairing needs an identity")
	}
	code := normalizeCode(o.Code)
	if !validCode(code) {
		return PairResult{}, errors.New("peer: that is not a pairing code (10 letters and digits, like ABCDE-23456)")
	}
	ctx, cancel := context.WithTimeout(ctx, PairDialTimeout)
	defer cancel()
	res, err := dialPair(ctx, addr, code, o)
	if err == nil {
		return res, nil
	}
	if errors.Is(err, ErrPairWrong) || errors.Is(err, ErrPairFingerprint) {
		return PairResult{}, err
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return PairResult{}, fmt.Errorf("%w from %s in %s", ErrPairNoAnswer, addr, PairDialTimeout)
	}
	if ctx.Err() != nil {
		return PairResult{}, ctx.Err()
	}
	return PairResult{}, err
}

func dialPair(ctx context.Context, addr, code string, o PairOptions) (PairResult, error) {
	d := net.Dialer{Timeout: PairDialTimeout}
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return PairResult{}, fmt.Errorf("%w from %s: %v", ErrPairNoAnswer, addr, err)
	}
	defer raw.Close()
	stop := context.AfterFunc(ctx, func() { raw.Close() })
	defer stop()
	_ = raw.SetDeadline(time.Now().Add(pairConnTimeout))
	// The callback runs again on every re-key, on x/crypto's own goroutine:
	// the first key is kept under mu and any other one ends the connection,
	// so the key the macs bind is the key that gets pinned. A key other than
	// the one an invite named ends it at once, before the code is used.
	pinned := firstHostKey{expect: o.ExpectFingerprint}
	cfg := &ssh.ClientConfig{
		Config: baseSSHConfig(),
		User:   PairUser,
		Auth:   []ssh.AuthMethod{ssh.PublicKeys(o.Identity.Signer)},
		// Any key is accepted here: the code exchange below is what
		// authenticates it, and nothing is pinned until it passes.
		HostKeyCallback:   func(_ string, _ net.Addr, key ssh.PublicKey) error { return pinned.check(key) },
		HostKeyAlgorithms: []string{ssh.KeyAlgoED25519},
		ClientVersion:     sshVersion,
	}
	sc, chans, reqs, err := ssh.NewClientConn(raw, addr, cfg)
	if err != nil {
		switch {
		case pinned.mismatched():
			return PairResult{}, ErrPairFingerprint
		case strings.Contains(err.Error(), "unable to authenticate"):
			return PairResult{}, fmt.Errorf("%w (%s)", ErrPairNotWaiting, addr)
		}
		return PairResult{}, fmt.Errorf("%w with %s: %v", ErrPairHandshake, addr, err)
	}
	defer sc.Close()
	go refuseGlobal(reqs)
	go func() {
		for nc := range chans {
			_ = nc.Reject(ssh.Prohibited, "no channels during pairing")
		}
	}()
	hostKey := pinned.key()
	ch, creqs, err := sc.OpenChannel(PairChannelType, nil)
	if err != nil {
		return PairResult{}, fmt.Errorf("%w: pairing channel refused: %v", ErrPairHandshake, err)
	}
	defer ch.Close()
	go refuseRequests(creqs)
	sid := sc.SessionID()
	k := pairKey(code, sid)
	own := o.Identity.PublicKey()
	hello := pairHello{V: pairVersion, Name: o.Name, MAC: pairMAC(k, "client", sid, hostKey, own, o.Name)}
	if err := writeJSONLine(ch, hello); err != nil {
		return PairResult{}, err
	}
	line, err := readFrame(bufio.NewReaderSize(ch, 1024), pairMaxMessage)
	if err != nil {
		return PairResult{}, fmt.Errorf("%w (%v)", ErrPairUnproven, err)
	}
	var reply pairHello
	if err := json.Unmarshal(line, &reply); err != nil || reply.V != pairVersion {
		return PairResult{}, ErrPairUnproven
	}
	if !reply.OK {
		if reply.Error == "wrong code" {
			return PairResult{}, ErrPairWrong
		}
		return PairResult{}, ErrPairUnproven
	}
	if !hmac.Equal(reply.MAC, pairMAC(k, "server", sid, hostKey, own, reply.Name)) {
		return PairResult{}, ErrPairUnproven
	}
	return PairResult{
		PeerName:        reply.Name,
		PeerKey:         hostKey,
		PeerFingerprint: Fingerprint(hostKey),
		OwnFingerprint:  o.Identity.Fingerprint(),
		RemoteAddr:      addr,
	}, nil
}

// PairErrText says why pairing with addr failed, in words a person can act
// on. It never contains the code.
func PairErrText(err error, addr string) string {
	port := portOfAddr(addr)
	switch {
	case errors.Is(err, ErrPairFingerprint):
		return "The fingerprint does not match the invite: someone may be intercepting this connection. Nothing was paired."
	case errors.Is(err, ErrPairWrong):
		return "Wrong code. Check it on the other Deck's screen and try again (3 tries per code)."
	case errors.Is(err, ErrPairAttempts):
		return "Too many wrong codes: this code no longer works. Start again for a new one."
	case errors.Is(err, ErrPairExpired):
		return "The code expired before the other Deck used it. Start again for a new one."
	case errors.Is(err, ErrPairUnproven):
		return "Wrong or expired code: the other Deck did not accept it. Ask for a fresh one."
	case errors.Is(err, ErrPairNotWaiting):
		return "Wrong or expired code: " + addr + " is not waiting for a code (it expired, was used, or pairing stopped there). Start waiting there again."
	case errors.Is(err, ErrPairHandshake):
		return "Host key / handshake failed with " + addr + ": is that a Deck on port " + port + "?"
	case errors.Is(err, ErrPairNoAnswer), errors.Is(err, context.DeadlineExceeded):
		return "No answer from " + addr + ". Is Deck running there, is Accept connections on, and is port " + port + " open in its firewall?"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "No such host. Check the address."
	}
	return strings.TrimPrefix(Sanitize(err.Error()), "peer: ")
}

// portOfAddr is the port of host:port, else the default.
func portOfAddr(addr string) string {
	if _, port, err := net.SplitHostPort(addr); err == nil && port != "" {
		return port
	}
	return config.DefaultPeerPort
}

func writeJSONLine(w interface{ Write([]byte) (int, error) }, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// refuseGlobal refuses every global request.
func refuseGlobal(reqs <-chan *ssh.Request) {
	for r := range reqs {
		_ = r.Reply(false, nil)
	}
}

// pairGlobal answers keepalives on a pairing connection and refuses every
// other global request.
func pairGlobal(reqs <-chan *ssh.Request) {
	for r := range reqs {
		_ = r.Reply(r.Type == keepaliveRequest, nil)
	}
}

// firstHostKey keeps the first host key a pairing connection presents and
// rejects any other one: x/crypto runs the host key callback again on every
// re-key, on its own goroutine. With expect set, a first key with another
// fingerprint is rejected too.
type firstHostKey struct {
	expect string

	mu       sync.Mutex
	first    ssh.PublicKey
	mismatch bool
}

func (f *firstHostKey) check(key ssh.PublicKey) error {
	if key.Type() != ssh.KeyAlgoED25519 {
		return errors.New("peer: host key is not ed25519")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.first == nil {
		if f.expect != "" && Fingerprint(key) != f.expect {
			f.mismatch = true
			return ErrPairFingerprint
		}
		f.first = key
		return nil
	}
	if !sameKey(f.first, key) {
		return fmt.Errorf("%w: the other side changed its host key during pairing", ErrHostKeyMismatch)
	}
	return nil
}

// key is the first host key seen, or nil.
func (f *firstHostKey) key() ssh.PublicKey {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.first
}

// mismatched reports whether the host key was not the expected one.
func (f *firstHostKey) mismatched() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mismatch
}
