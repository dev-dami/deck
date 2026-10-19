package relayclient

import (
	"context"
	"encoding/base64"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/pkg/remote/identity"
	"github.com/dev-dami/deck/pkg/remote/relayclient/relaytest"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

type fixture struct {
	relay *relaytest.Relay
	id    *identity.Identity
	c     *Client

	mu       sync.Mutex
	hashes   []string
	state    []bool
	lanes    chan laneEvent
	mail     chan mailEvent
	mailFail bool
}

type laneEvent struct {
	lane *Lane
	pair bool
}

type mailEvent struct {
	id, device string
	data       []byte
}

func start(t *testing.T, registered bool) *fixture {
	t.Helper()
	id, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{relay: relaytest.New(t), id: id, hashes: []string{"h_1"}, lanes: make(chan laneEvent, 8), mail: make(chan mailEvent, 8)}
	if registered {
		f.relay.Register(id)
	}
	f.c = New(Config{
		Identity: id,
		URL:      f.relay.URL,
		Hashes: func() []string {
			f.mu.Lock()
			defer f.mu.Unlock()
			return append([]string(nil), f.hashes...)
		},
		Handlers: Handlers{
			Lane: func(ctx context.Context, l *Lane, pair bool) {
				f.lanes <- laneEvent{l, pair}
				<-ctx.Done()
			},
			Mail: func(_ context.Context, mid, dev string, data []byte) error {
				f.mu.Lock()
				fail := f.mailFail
				f.mu.Unlock()
				if fail {
					return context.DeadlineExceeded
				}
				f.mail <- mailEvent{mid, dev, data}
				return nil
			},
		},
		OnState: func(on bool) {
			f.mu.Lock()
			f.state = append(f.state, on)
			f.mu.Unlock()
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.c.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return f
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestConnectAuthenticatesAndSendsDevices(t *testing.T) {
	f := start(t, true)
	waitFor(t, "online", f.c.Online)
	got, ok := f.relay.WaitFrame(wire.HostDevices, 5*time.Second)
	if !ok || len(got.Hashes) != 1 || got.Hashes[0] != "h_1" {
		t.Fatalf("devices frame %+v ok=%v", got, ok)
	}
	if f.relay.AuthFailures() != 0 {
		t.Fatal("the challenge failed")
	}
}

func TestUnknownComputerRegistersThenConnects(t *testing.T) {
	f := start(t, false)
	waitFor(t, "online after registering", f.c.Online)
	if f.relay.AuthFailures() != 0 {
		t.Fatal("auth failed after register")
	}
}

func TestSendDevicesAfterPairing(t *testing.T) {
	f := start(t, true)
	waitFor(t, "online", f.c.Online)
	f.relay.WaitFrame(wire.HostDevices, 5*time.Second)
	f.mu.Lock()
	f.hashes = []string{"h_1", "h_2"}
	f.mu.Unlock()
	if err := f.c.SendDevices(); err != nil {
		t.Fatal(err)
	}
	got, ok := f.relay.WaitFrame(wire.HostDevices, 5*time.Second)
	if !ok || len(got.Hashes) != 2 {
		t.Fatalf("got %+v", got)
	}
}

func TestLaneCarriesMessagesBothWays(t *testing.T) {
	f := start(t, true)
	waitFor(t, "online", f.c.Online)
	f.relay.OpenLane("l_01", "h_1", false)
	var ev laneEvent
	select {
	case ev = <-f.lanes:
	case <-time.After(5 * time.Second):
		t.Fatal("no lane opened")
	}
	if ev.pair {
		t.Fatal("device lane reported as pair")
	}

	f.relay.PhoneData("l_01", []byte{1, 2, 3})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	msg, err := ev.lane.Read(ctx)
	if err != nil || len(msg) != 3 || msg[2] != 3 {
		t.Fatalf("read %v %v", msg, err)
	}

	if err := ev.lane.Write(ctx, []byte{9, 8}); err != nil {
		t.Fatal(err)
	}
	got, ok := f.relay.WaitFrame(wire.HostData, 5*time.Second)
	raw, _ := base64.StdEncoding.DecodeString(got.B64)
	if !ok || got.Lane != "l_01" || len(raw) != 2 || raw[0] != 9 {
		t.Fatalf("host data %+v", got)
	}

	if err := ev.lane.Close(wire.CloseRevoked, "revoked"); err != nil {
		t.Fatal(err)
	}
	cl, ok := f.relay.WaitFrame(wire.HostClose, 5*time.Second)
	if !ok || cl.Code != wire.CloseRevoked {
		t.Fatalf("close frame %+v", cl)
	}
	if _, err := ev.lane.Read(ctx); err == nil {
		t.Fatal("read on a closed lane succeeded")
	}
}

func TestPairLaneIsFlagged(t *testing.T) {
	f := start(t, true)
	waitFor(t, "online", f.c.Online)
	f.relay.OpenLane("p_01", "", true)
	select {
	case ev := <-f.lanes:
		if !ev.pair {
			t.Fatal("pair lane not flagged")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no lane")
	}
}

func TestPhoneLeavingEndsTheLane(t *testing.T) {
	f := start(t, true)
	waitFor(t, "online", f.c.Online)
	f.relay.OpenLane("l_02", "h_1", false)
	ev := <-f.lanes
	f.relay.Send(wire.HostFrame{T: wire.HostClose, Lane: "l_02", Code: 1000})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := ev.lane.Read(ctx); err == nil {
		t.Fatal("lane stayed open after the phone left")
	}
}

func TestMailIsAckedOnlyAfterTheHandlerStoresIt(t *testing.T) {
	f := start(t, true)
	waitFor(t, "online", f.c.Online)

	f.mu.Lock()
	f.mailFail = true
	f.mu.Unlock()
	f.relay.Mail("m1", "h_1", []byte("sealed"))
	if _, ok := f.relay.WaitFrame(wire.HostAck, 500*time.Millisecond); ok {
		t.Fatal("a mail the handler failed to store was acked")
	}

	f.mu.Lock()
	f.mailFail = false
	f.mu.Unlock()
	f.relay.Mail("m2", "h_1", []byte("sealed"))
	select {
	case m := <-f.mail:
		if m.id != "m2" || m.device != "h_1" || string(m.data) != "sealed" {
			t.Fatalf("mail %+v", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("mail not delivered")
	}
	ack, ok := f.relay.WaitFrame(wire.HostAck, 5*time.Second)
	if !ok || ack.ID != "m2" {
		t.Fatalf("ack %+v ok=%v", ack, ok)
	}
}

func TestReconnectsAfterTheRelayDrops(t *testing.T) {
	f := start(t, true)
	waitFor(t, "online", f.c.Online)
	f.relay.OpenLane("l_03", "h_1", false)
	ev := <-f.lanes

	f.relay.DropHost()
	waitFor(t, "offline", func() bool { return !f.c.Online() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := ev.lane.Read(ctx); err == nil {
		t.Fatal("a lane survived the loss of the relay connection")
	}
	waitFor(t, "back online", f.c.Online)
	if f.relay.Hosts() < 2 {
		t.Fatalf("hosts accepted: %d", f.relay.Hosts())
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.state) < 3 || !f.state[0] || f.state[1] || !f.state[2] {
		t.Fatalf("state transitions %v", f.state)
	}
}

func TestOfflineSendsFail(t *testing.T) {
	id, _ := identity.LoadOrCreate(t.TempDir())
	c := New(Config{Identity: id, URL: "http://127.0.0.1:1"})
	if err := c.SendDevices(); err == nil {
		t.Fatal("send without a connection succeeded")
	}
	if err := c.Snapshot("h", make([]byte, wire.MaxSnapshotBytes+1)); err == nil {
		t.Fatal("oversized snapshot accepted")
	}
	if err := c.Push("h", make([]byte, wire.MaxPushPayloadBytes+1), time.Hour, true); err == nil {
		t.Fatal("oversized push accepted")
	}
}

func TestJitterStaysInRange(t *testing.T) {
	for range 200 {
		if j := jitter(8 * time.Second); j < 4*time.Second || j > 8*time.Second+time.Millisecond {
			t.Fatalf("jitter %v", j)
		}
	}
}
