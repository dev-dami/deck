package peer

import (
	"context"
	"io"
	"log/slog"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"
)

// quietLog drops log output in tests.
var quietLog = slog.New(slog.DiscardHandler)

// leakCheck fails the test if goroutines started during it are still running
// shortly after it ends. Call it first so its cleanup runs last. Tests in
// this package are not parallel, so the count is meaningful.
func leakCheck(t *testing.T) {
	t.Helper()
	before := runtime.NumGoroutine()
	t.Cleanup(func() {
		deadline := time.Now().Add(5 * time.Second)
		for {
			n := runtime.NumGoroutine()
			if n <= before {
				return
			}
			if time.Now().After(deadline) {
				buf := make([]byte, 1<<20)
				buf = buf[:runtime.Stack(buf, true)]
				t.Fatalf("goroutine leak: %d before, %d after\n%s", before, n, buf)
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
}

// fastKDF makes pairing's KDF cheap for the test.
func fastKDF(t *testing.T) {
	t.Helper()
	old := kdfParams
	kdfParams.memory = 1024
	kdfParams.threads = 1
	t.Cleanup(func() { kdfParams = old })
}

// testPeer is one Deck's peer state in a temp dir.
type testPeer struct {
	name  string
	dir   string
	id    *Identity
	store *Store
}

func newTestPeer(t *testing.T, name string) *testPeer {
	t.Helper()
	dir := t.TempDir()
	id, err := LoadOrCreateIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	return &testPeer{name: name, dir: dir, id: id, store: OpenStore(dir)}
}

// pin pins other in p's store at addr.
func (p *testPeer) pin(t *testing.T, other *testPeer, addr string) Pinned {
	t.Helper()
	pin := NewPinned(other.name, addr, "", other.id.PublicKey())
	if err := p.store.Pin(pin); err != nil {
		t.Fatal(err)
	}
	return pin
}

// listen starts a server for p on a loopback port.
func (p *testPeer) listen(t *testing.T, opts ServerOptions) *Server {
	t.Helper()
	opts.Identity = p.id
	opts.Store = p.store
	if opts.Name == "" {
		opts.Name = p.name
	}
	if opts.Version == "" {
		opts.Version = "test"
	}
	if opts.Logger == nil {
		opts.Logger = quietLog
	}
	s, err := Listen("127.0.0.1:0", opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// dial connects p to the pinned peer at the server's address.
func (p *testPeer) dial(t *testing.T, pin Pinned, h Handler) *Conn {
	t.Helper()
	c, err := Dial(context.Background(), DialOptions{
		Identity: p.id, Peer: pin, Handler: h, Name: p.name, Version: "test", Logger: quietLog,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// pairUp makes a listening peer a and a dialing peer b, pinned to each other.
func pairUp(t *testing.T, opts ServerOptions) (a, b *testPeer, srv *Server, pinA Pinned) {
	t.Helper()
	a, b = newTestPeer(t, "a"), newTestPeer(t, "b")
	srv = a.listen(t, opts)
	a.pin(t, b, "")
	pinA = b.pin(t, a, srv.Addr().String())
	return a, b, srv, pinA
}

// allowOps is a ServerOptions.Caller that lets the peer run ops (the
// default allow list is only peer.status).
func allowOps(ops ...string) func(Pinned) Caller {
	return func(p Pinned) Caller {
		return Caller{Name: p.Name, Fingerprint: p.Fingerprint, AllowInbound: ops}
	}
}

func ctxT(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// waitClosed reports whether c is closed by the other side within d
// (anything it sends first is drained).
func waitClosed(c net.Conn, d time.Duration) bool {
	_ = c.SetReadDeadline(time.Now().Add(d))
	_, err := io.Copy(io.Discard, c)
	if err == nil {
		return true // EOF
	}
	return !strings.Contains(err.Error(), "timeout")
}
