package ipc

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/remotetest"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// link wires a fake provider to a Proxy over real loopback TCP the way the
// daemon and a provider meet.
func link(t *testing.T) (*remotetest.Fake, *Proxy, net.Conn) {
	t.Helper()
	ln, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	e := Endpoint{Addr: ln.Addr().String(), Secret: "s3cret"}

	fake := remotetest.New()
	fake.Info_ = remote.Info{Version: "9.9", Project: "deck", Kinds: []string{"claude"}}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	provider, err := Dial(ctx, e, "prj_a1")
	if err != nil {
		t.Fatal(err)
	}
	go ServeConn(ctx, provider, fake)

	pj, conn, err := Accept(ln, e.Secret)
	if err != nil || pj != "prj_a1" {
		t.Fatalf("accept: %q %v", pj, err)
	}
	t.Cleanup(func() { conn.Close() })
	p, err := NewProxy(conn)
	if err != nil {
		t.Fatal(err)
	}
	return fake, p, provider
}

func TestProxyIsABackend(t *testing.T) {
	fake, p, _ := link(t)
	ctx := context.Background()
	fake.AddSession("ses_1", "first")

	if info := p.Info(); info.Project != "deck" || info.Version != "9.9" {
		t.Fatalf("info %+v", info)
	}
	list, err := p.Sessions(ctx)
	want, _ := fake.Sessions(ctx)
	if err != nil || len(list) != len(want) || len(list) < 2 {
		t.Fatalf("sessions: %v %v", list, err)
	}
	found := false
	for _, s := range list {
		found = found || s.ID == "ses_1"
	}
	if !found {
		t.Fatalf("ses_1 missing from %v", list)
	}
	created, err := p.CreateSession(ctx, "new")
	if err != nil || created.Title != "new" {
		t.Fatalf("create: %v %v", created, err)
	}
	if err := p.RenameSession(ctx, created.ID, "renamed"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Send(ctx, "ses_1", "hello", []remote.Image{{Name: "a.png", Mime: "image/png", Data: []byte{1, 2, 3}}}); err != nil {
		t.Fatal(err)
	}
	sent := fake.Sent()
	if len(sent) != 1 || sent[0].Text != "hello" {
		t.Fatalf("provider saw %+v", sent)
	}
	if err := p.SetMode("ses_1", wire.ModeAsk); err != nil {
		t.Fatal(err)
	}
	if mode, err := p.Mode("ses_1"); err != nil || mode != wire.ModeAsk {
		t.Fatalf("mode %q %v", mode, err)
	}
}

func TestErrorsKeepTheirSentinels(t *testing.T) {
	_, p, _ := link(t)
	_, err := p.Queue("ses_missing")
	if !errors.Is(err, remote.ErrNotFound) {
		t.Fatalf("want ErrNotFound across the wire, got %v", err)
	}
	if err := p.SetMode("ses_missing", "nonsense"); err == nil {
		t.Fatal("bad call succeeded")
	}
}

func TestEventsFlowAndStopWithTheProvider(t *testing.T) {
	fake, p, provider := link(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := p.Events(ctx)

	fake.Inject(remote.Event{Name: wire.EvBusy, SessionID: "ses_1", Scoped: true, Payload: wire.BusyChanged{SessionID: "ses_1", Busy: true}})
	select {
	case ev := <-ch:
		if ev.Name != wire.EvBusy || ev.SessionID != "ses_1" || !ev.Scoped || ev.Payload == nil {
			t.Fatalf("event %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
	}

	provider.Close()
	select {
	case <-p.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("proxy did not notice the provider leave")
	}
	if _, ok := <-ch; ok {
		t.Fatal("events stayed open after the provider left")
	}
	if _, err := p.Sessions(context.Background()); !errors.Is(err, ErrOffline) {
		t.Fatalf("call after disconnect: %v", err)
	}
	if _, ok := <-p.Events(context.Background()); ok {
		t.Fatal("a new subscription opened on a dead proxy")
	}
}

func TestAcceptRefusesStrangers(t *testing.T) {
	ln, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	good := Endpoint{Addr: ln.Addr().String(), Secret: "right"}
	bad := Endpoint{Addr: good.Addr, Secret: "wrong"}

	for name, dial := range map[string]func() (net.Conn, error){
		"wrong secret": func() (net.Conn, error) { return Dial(context.Background(), bad, "prj") },
		"no project":   func() (net.Conn, error) { return Dial(context.Background(), good, "") },
		"garbage": func() (net.Conn, error) {
			c, err := net.Dial("tcp", good.Addr)
			if err == nil {
				c.Write([]byte("GET / HTTP/1.1\r\n\r\n"))
			}
			return c, err
		},
	} {
		t.Run(name, func(t *testing.T) {
			errc := make(chan error, 1)
			go func() { _, _, err := Accept(ln, good.Secret); errc <- err }()
			c, err := dial()
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if err := <-errc; !errors.Is(err, ErrBadHello) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestOversizedHelloIsRefused(t *testing.T) {
	ln, _ := Listen()
	defer ln.Close()
	errc := make(chan error, 1)
	go func() { _, _, err := Accept(ln, "s"); errc <- err }()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write(make([]byte, 4<<10))
	if err := <-errc; !errors.Is(err, ErrBadHello) {
		t.Fatalf("got %v", err)
	}
}

func TestEndpointFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	secret, err := NewSecret()
	if err != nil || len(secret) != 2*secretBytes {
		t.Fatalf("secret %q %v", secret, err)
	}
	want := Endpoint{Addr: "127.0.0.1:5555", Secret: secret}
	if err := WriteEndpoint(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadEndpoint(dir)
	if err != nil || got != want {
		t.Fatalf("got %+v %v", got, err)
	}
	if _, err := ReadEndpoint(t.TempDir()); err == nil {
		t.Fatal("missing endpoint file was not an error")
	}
}
