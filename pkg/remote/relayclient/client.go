// Package relayclient is the daemon's dial-out to the Deck relay
// (docs/mobile-contract.md §5): it proves ownership of the computer id, keeps
// the relay told which phones may connect, turns each phone connection (a
// "lane") into a message Conn for the daemon, and delivers queued mail.
package relayclient

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dev-dami/deck/pkg/remote/identity"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// Backoff bounds, as in the peers supervisor.
const (
	minBackoff = time.Second
	maxBackoff = time.Minute
	// resumeCheck and resumeJump detect a machine wake: the wall clock moves
	// further than the monotonic one between two ticks.
	resumeCheck = 5 * time.Second
	resumeJump  = 15 * time.Second
	pingEvery   = 30 * time.Second
	writeWait   = 10 * time.Second
)

var b64 = base64.StdEncoding

// Handlers receive what arrives from the relay.
type Handlers struct {
	// Lane serves one phone connection; it returns when the lane is done. A
	// device lane is one that named a device token hash; a pair lane is the
	// pairing handshake.
	Lane func(ctx context.Context, lane *Lane, pair bool)
	// Mail receives one queued message for a device. Returning nil acks it,
	// so return only once it is durably stored.
	Mail func(ctx context.Context, id, deviceHash string, data []byte) error
}

// Config is what a Client needs.
type Config struct {
	Identity *identity.Identity
	// URL is the relay's base, https://app.example.dev (http for local tests).
	URL string
	// Hashes returns the token hashes of the paired devices, sent to the
	// relay on every (re)connect and by SendDevices.
	Hashes   func() []string
	Handlers Handlers
	// OnState is told when the connection to the relay goes up or down.
	OnState func(online bool)
	// HTTP is used to register; defaults to a client with a timeout.
	HTTP *http.Client
}

// Client keeps one connection to the relay alive.
type Client struct {
	cfg Config

	mu     sync.Mutex
	conn   *websocket.Conn
	lanes  map[string]*Lane
	wmu    sync.Mutex
	online bool
}

// New returns a client; call Run to start it.
func New(cfg Config) *Client {
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{cfg: cfg, lanes: map[string]*Lane{}}
}

// Online reports whether the host connection is established.
func (c *Client) Online() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.online
}

// Run connects and reconnects until ctx ends.
func (c *Client) Run(ctx context.Context) {
	backoff := minBackoff
	wake := make(chan struct{}, 1)
	go watchResume(ctx, wake)
	for ctx.Err() == nil {
		started := time.Now()
		err := c.session(ctx, wake)
		if ctx.Err() != nil {
			return
		}
		slog.Warn("remote: relay connection ended", "error", err)
		if time.Since(started) > 2*minBackoff {
			backoff = minBackoff
		}
		select {
		case <-time.After(jitter(backoff)):
		case <-wake:
			backoff = minBackoff
			continue
		case <-ctx.Done():
			return
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

func jitter(d time.Duration) time.Duration {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(d)/2+1))
	if err != nil {
		return d
	}
	return d/2 + time.Duration(n.Int64())
}

// watchResume signals wake when the wall clock jumps against the monotonic
// one, which is what a sleeping machine looks like when it comes back.
func watchResume(ctx context.Context, wake chan<- struct{}) {
	t := time.NewTicker(resumeCheck)
	defer t.Stop()
	last := time.Now()
	for {
		select {
		case now := <-t.C:
			if now.Round(0).Sub(last.Round(0))-now.Sub(last) > resumeJump {
				select {
				case wake <- struct{}{}:
				default:
				}
			}
			last = now
		case <-ctx.Done():
			return
		}
	}
}

func (c *Client) wsURL(path string) (string, error) {
	u, err := url.Parse(c.cfg.URL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("relay url must be http or https, got %q", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	return u.String(), nil
}

// register tells the relay about this computer. It is idempotent.
func (c *Client) register(ctx context.Context) error {
	body, _ := json.Marshal(wire.RegisterRequest{RelayPub: base64.RawURLEncoding.EncodeToString(c.cfg.Identity.RelayPublic())})
	u := strings.TrimRight(c.cfg.URL, "/") + "/r/v1/register"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.cfg.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("register: relay answered %s", resp.Status)
	}
	var out wire.RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("register: %w", err)
	}
	if out.CID != c.cfg.Identity.CID() {
		return errors.New("register: relay computed a different computer id")
	}
	return nil
}

func (c *Client) session(ctx context.Context, wake <-chan struct{}) error {
	path := "/r/v1/c/" + c.cfg.Identity.CID() + "/host"
	u, err := c.wsURL(path)
	if err != nil {
		return err
	}
	ws, resp, err := websocket.DefaultDialer.DialContext(ctx, u, nil)
	if err != nil {
		// An unknown cid is refused before the upgrade; register once and let
		// the caller retry.
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			if rerr := c.register(ctx); rerr != nil {
				return rerr
			}
		}
		return fmt.Errorf("dial relay: %w", err)
	}
	ws.SetReadLimit(wire.MaxRelayFrameBytes)
	defer ws.Close()

	var challenge wire.HostFrame
	if err := ws.ReadJSON(&challenge); err != nil || challenge.T != wire.HostChallenge {
		// Workers complete the upgrade before closing an unknown computer.
		// Register on that response as well as the pre-upgrade HTTP 404 above.
		if websocket.IsCloseError(err, wire.CloseUnknownCID) {
			if rerr := c.register(ctx); rerr != nil {
				return rerr
			}
		}
		return fmt.Errorf("relay challenge: %v", err)
	}
	sig := c.cfg.Identity.Sign([]byte(wire.RelayChallengeContext + c.cfg.Identity.CID() + "|" + challenge.Nonce))
	if err := ws.WriteJSON(wire.HostFrame{T: wire.HostAuth, Sig: base64.RawURLEncoding.EncodeToString(sig)}); err != nil {
		return err
	}
	var ready wire.HostFrame
	if err := ws.ReadJSON(&ready); err != nil || ready.T != wire.HostReady {
		return fmt.Errorf("relay refused the host: %v", err)
	}

	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c.mu.Lock()
	c.conn, c.online = ws, true
	c.mu.Unlock()
	if c.cfg.OnState != nil {
		c.cfg.OnState(true)
	}
	defer func() {
		c.mu.Lock()
		c.conn, c.online = nil, false
		for id, l := range c.lanes {
			l.shut(errors.New("relay connection ended"))
			delete(c.lanes, id)
		}
		c.mu.Unlock()
		if c.cfg.OnState != nil {
			c.cfg.OnState(false)
		}
	}()

	if err := c.SendDevices(); err != nil {
		return err
	}
	go func() {
		t := time.NewTicker(pingEvery)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				c.wmu.Lock()
				_ = ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeWait))
				c.wmu.Unlock()
			case <-wake:
				_ = ws.Close()
				return
			case <-sctx.Done():
				_ = ws.Close()
				return
			}
		}
	}()

	for {
		var f wire.HostFrame
		if err := ws.ReadJSON(&f); err != nil {
			if sctx.Err() != nil {
				return sctx.Err()
			}
			return err
		}
		c.dispatch(sctx, f)
	}
}

func (c *Client) dispatch(ctx context.Context, f wire.HostFrame) {
	switch f.T {
	case wire.HostOpen:
		if f.Lane == "" {
			return
		}
		l := newLane(c, f.Lane)
		c.mu.Lock()
		if old := c.lanes[f.Lane]; old != nil {
			old.shut(errors.New("lane replaced"))
		}
		c.lanes[f.Lane] = l
		c.mu.Unlock()
		if c.cfg.Handlers.Lane != nil {
			go func() {
				c.cfg.Handlers.Lane(ctx, l, f.Pair)
				_ = l.Close(0, "")
			}()
		}
	case wire.HostData:
		c.mu.Lock()
		l := c.lanes[f.Lane]
		c.mu.Unlock()
		if l == nil {
			return
		}
		data, err := b64.DecodeString(f.B64)
		if err != nil {
			return
		}
		l.deliver(data)
	case wire.HostClose:
		c.mu.Lock()
		l := c.lanes[f.Lane]
		delete(c.lanes, f.Lane)
		c.mu.Unlock()
		if l != nil {
			l.shut(errors.New("phone left"))
		}
	case wire.HostMail:
		data, err := b64.DecodeString(f.B64)
		if err != nil || c.cfg.Handlers.Mail == nil {
			return
		}
		go func() {
			if err := c.cfg.Handlers.Mail(ctx, f.ID, f.Device, data); err != nil {
				slog.Warn("remote: mail not stored", "id", f.ID, "error", err)
				return
			}
			_ = c.send(wire.HostFrame{T: wire.HostAck, ID: f.ID})
		}()
	}
}

var errOffline = errors.New("relayclient: not connected to the relay")

func (c *Client) send(f wire.HostFrame) error {
	c.mu.Lock()
	ws := c.conn
	c.mu.Unlock()
	if ws == nil {
		return errOffline
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = ws.SetWriteDeadline(time.Now().Add(writeWait))
	return ws.WriteJSON(f)
}

// SendDevices tells the relay which phones may connect. Call it after pairing
// or revoking.
func (c *Client) SendDevices() error {
	hashes := []string{}
	if c.cfg.Hashes != nil {
		hashes = c.cfg.Hashes()
	}
	return c.send(wire.HostFrame{T: wire.HostDevices, Hashes: hashes})
}

// PairOpen opens the pairing window on the relay.
func (c *Client) PairOpen(pairIDHash string, exp time.Time) error {
	return c.send(wire.HostFrame{T: wire.HostPairOpen, PairIDHash: pairIDHash, Exp: exp.Unix()})
}

// PairClose ends the pairing window.
func (c *Client) PairClose() error { return c.send(wire.HostFrame{T: wire.HostPairClose}) }

// Snapshot leaves an encrypted snapshot for a device.
func (c *Client) Snapshot(deviceHash string, data []byte) error {
	if len(data) > wire.MaxSnapshotBytes {
		return errors.New("relayclient: snapshot too large")
	}
	return c.send(wire.HostFrame{T: wire.HostSnap, DeviceHash: deviceHash, B64: b64.EncodeToString(data)})
}

// Push asks the relay to deliver an already encrypted web push to a device.
func (c *Client) Push(deviceHash string, data []byte, ttl time.Duration, urgent bool) error {
	if len(data) > wire.MaxPushPayloadBytes {
		return errors.New("relayclient: push payload too large")
	}
	u := wire.UrgencyNormal
	if urgent {
		u = wire.UrgencyHigh
	}
	return c.send(wire.HostFrame{T: wire.HostPush, DeviceHash: deviceHash, B64: b64.EncodeToString(data), TTL: int(ttl.Seconds()), Urgency: u})
}

// Lane is one phone's connection through the relay: it satisfies the
// daemon's Conn.
type Lane struct {
	c  *Client
	id string

	in     chan []byte
	closed chan struct{}
	once   sync.Once
	err    error
}

const laneQueue = 256

func newLane(c *Client, id string) *Lane {
	return &Lane{c: c, id: id, in: make(chan []byte, laneQueue), closed: make(chan struct{})}
}

func (l *Lane) deliver(data []byte) {
	select {
	case l.in <- data:
	default:
		// A phone that floods a lane is cut off rather than buffered.
		_ = l.Close(wire.CloseSlowConsumer, "too slow")
	}
}

func (l *Lane) shut(err error) {
	l.once.Do(func() {
		l.err = err
		close(l.closed)
	})
}

// Read returns the next Noise message from the phone.
func (l *Lane) Read(ctx context.Context) ([]byte, error) {
	select {
	case m := <-l.in:
		return m, nil
	case <-l.closed:
		select {
		case m := <-l.in:
			return m, nil
		default:
		}
		return nil, l.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Write sends a Noise message to the phone.
func (l *Lane) Write(ctx context.Context, msg []byte) error {
	select {
	case <-l.closed:
		return l.err
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return l.c.send(wire.HostFrame{T: wire.HostData, Lane: l.id, B64: b64.EncodeToString(msg)})
}

// Close ends the lane, telling the relay to drop the phone with code.
func (l *Lane) Close(code int, _ string) error {
	l.shut(errors.New("lane closed"))
	l.c.mu.Lock()
	if l.c.lanes[l.id] == l {
		delete(l.c.lanes, l.id)
	}
	l.c.mu.Unlock()
	if code == 0 {
		return nil
	}
	return l.c.send(wire.HostFrame{T: wire.HostClose, Lane: l.id, Code: code})
}
