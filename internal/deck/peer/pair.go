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
)

// Pairing: `deck peer pair` listens and shows a one-time code, `deck peer add
// <name> <addr>` dials it with the code typed at its prompt (or taken from
// DECK_PEER_CODE, never the command line). The two run an SSH handshake in which
// each side accepts any ed25519 key, then prove to each other that they know
// the code over a "deck-pair" channel:
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

// PairChannelType is the channel the pairing exchange runs on. The pairing
// listener accepts it and nothing else; the normal server never accepts it.
const PairChannelType = "deck-pair"

// Pairing defaults.
const (
	DefaultPairExpiry   = 5 * time.Minute
	DefaultPairAttempts = 3
	pairConnTimeout     = 30 * time.Second
	pairMaxMessage      = 4 << 10
	pairVersion         = 1
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
	// Expiry and MaxAttempts bound the listener (5m, 3).
	Expiry      time.Duration
	MaxAttempts int
	Logger      *slog.Logger
}

// pairGate is the state ServePair's connections share: the per-IP and total
// counts of connections not yet done, and the code's attempts. Proofs are
// checked one at a time under vmu (the KDF is memory-hard, so they never run
// side by side), and only a wrong mac uses an attempt: a connection that
// stalls, speaks junk or never sends a hello costs nothing.
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

// ServePair serves pairing on ln until one peer proves the code. The code
// is single use: ServePair returns after the first success, after
// MaxAttempts wrong codes, or when the code expires, and closes ln.
// Connections are served side by side, each with pairHelloTimeout to reach
// its proof, so idle connections cannot hold the listener.
func ServePair(ctx context.Context, ln net.Listener, o PairOptions) (PairResult, error) {
	defer ln.Close()
	if o.Identity == nil {
		return PairResult{}, errors.New("peer: pairing needs an identity")
	}
	code := normalizeCode(o.Code)
	if !validCode(code) {
		return PairResult{}, errors.New("peer: invalid pairing code")
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
	ctx, cancel := context.WithTimeout(ctx, o.Expiry)
	defer cancel()
	g := &pairGate{code: code, max: o.MaxAttempts, conns: map[net.Conn]struct{}{}, perIP: map[string]int{}}
	results := make(chan pairDone)
	acceptErr := make(chan error, 1)
	quit := make(chan struct{})
	var wg sync.WaitGroup
	defer func() {
		close(quit)
		ln.Close()
		g.closeAll()
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
			if !g.admit(raw, ip) {
				raw.Close()
				continue
			}
			wg.Add(1) // this goroutine still counts, so wg is not at zero
			go func() {
				defer wg.Done()
				defer g.release(raw, ip)
				res, burned, err := pairServeConn(raw, g, o)
				select {
				case results <- pairDone{res: res, burned: burned, err: err, remote: raw.RemoteAddr().String()}:
				case <-quit:
				}
			}()
		}
	}()

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
		case d := <-results:
			if d.err == nil {
				return d.res, nil
			}
			o.Logger.Debug("peer: pairing attempt failed", "remote", d.remote, "error", d.err)
			if d.burned {
				return PairResult{}, ErrPairAttempts
			}
		}
	}
}

// pairServeConn runs the listener's side on one connection. burned reports
// that the code cannot be used any more after this connection failed: its
// wrong proof was the last attempt, or its right one could not be answered.
func pairServeConn(raw net.Conn, g *pairGate, o PairOptions) (res PairResult, burned bool, err error) {
	_ = raw.SetDeadline(time.Now().Add(pairHelloTimeout))
	cfg := &ssh.ServerConfig{
		Config:                  baseSSHConfig(),
		PublicKeyAuthAlgorithms: []string{ssh.KeyAlgoED25519},
		MaxAuthTries:            3,
		ServerVersion:           sshVersion,
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if key.Type() != ssh.KeyAlgoED25519 {
				return nil, errNotPinned
			}
			return &ssh.Permissions{Extensions: map[string]string{"key": string(key.Marshal())}}, nil
		},
	}
	cfg.AddHostKey(o.Identity.Signer)
	sc, chans, reqs, err := ssh.NewServerConn(raw, cfg)
	if err != nil {
		return res, false, err
	}
	defer sc.Close()
	go refuseGlobal(reqs)
	clientKey, err := ssh.ParsePublicKey([]byte(sc.Permissions.Extensions["key"]))
	if err != nil {
		return res, false, err
	}
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
	_ = raw.SetDeadline(time.Now().Add(pairConnTimeout))
	sid := sc.SessionID()
	g.vmu.Lock()
	if g.used {
		g.vmu.Unlock()
		return res, false, errPairClosed
	}
	k := pairKey(g.code, sid)
	want := pairMAC(k, "client", sid, o.Identity.PublicKey(), clientKey, hello.Name)
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
	reply := pairHello{V: pairVersion, OK: true, Name: o.Name,
		MAC: pairMAC(k, "server", sid, o.Identity.PublicKey(), clientKey, o.Name)}
	if err := writeJSONLine(ch, reply); err != nil {
		return res, true, err
	}
	// Wait for the dialer to close, so it has read the reply.
	_, _ = r.ReadByte()
	return PairResult{
		PeerName:        hello.Name,
		PeerKey:         clientKey,
		PeerFingerprint: Fingerprint(clientKey),
		OwnFingerprint:  o.Identity.Fingerprint(),
		RemoteAddr:      raw.RemoteAddr().String(),
	}, false, nil
}

// DialPair runs the dialing side against addr with the typed code.
func DialPair(ctx context.Context, addr string, o PairOptions) (PairResult, error) {
	if o.Identity == nil {
		return PairResult{}, errors.New("peer: pairing needs an identity")
	}
	code := normalizeCode(o.Code)
	if !validCode(code) {
		return PairResult{}, fmt.Errorf("peer: %q is not a pairing code (10 letters and digits, like ABCDE-23456)", o.Code)
	}
	d := net.Dialer{Timeout: 10 * time.Second}
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return PairResult{}, err
	}
	defer raw.Close()
	stop := context.AfterFunc(ctx, func() { raw.Close() })
	defer stop()
	_ = raw.SetDeadline(time.Now().Add(pairConnTimeout))
	// The callback runs again on every re-key, on x/crypto's own goroutine:
	// the first key is kept under mu and any other one ends the connection,
	// so the key the macs bind is the key that gets pinned.
	var pinned firstHostKey
	cfg := &ssh.ClientConfig{
		Config: baseSSHConfig(),
		User:   "deck-pair",
		Auth:   []ssh.AuthMethod{ssh.PublicKeys(o.Identity.Signer)},
		// Any key is accepted here: the code exchange below is what
		// authenticates it, and nothing is pinned until it passes.
		HostKeyCallback:   func(_ string, _ net.Addr, key ssh.PublicKey) error { return pinned.check(key) },
		HostKeyAlgorithms: []string{ssh.KeyAlgoED25519},
		ClientVersion:     sshVersion,
	}
	sc, chans, reqs, err := ssh.NewClientConn(raw, addr, cfg)
	if err != nil {
		return PairResult{}, fmt.Errorf("peer: pairing handshake: %w", err)
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
		return PairResult{}, fmt.Errorf("peer: pairing channel: %w", err)
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

// firstHostKey keeps the first host key a pairing connection presents and
// rejects any other one: x/crypto runs the host key callback again on every
// re-key, on its own goroutine.
type firstHostKey struct {
	mu    sync.Mutex
	first ssh.PublicKey
}

func (f *firstHostKey) check(key ssh.PublicKey) error {
	if key.Type() != ssh.KeyAlgoED25519 {
		return errors.New("peer: host key is not ed25519")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.first == nil {
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
