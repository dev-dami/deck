package mobile

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/pkg/remote/daemon"
	"github.com/dev-dami/deck/pkg/remote/devstore"
	"github.com/dev-dami/deck/pkg/remote/e2e"
	"github.com/dev-dami/deck/pkg/remote/identity"
	"github.com/dev-dami/deck/pkg/remote/ipc"
	"github.com/dev-dami/deck/pkg/remote/projects"
	"github.com/dev-dami/deck/pkg/remote/relayclient/relaytest"
	"github.com/dev-dami/deck/pkg/remote/remotetest"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

func init() {
	reloadEvery = 100 * time.Millisecond
	providerRetry = 100 * time.Millisecond
}

type env struct {
	t      *testing.T
	dir    string
	relay  *relaytest.Relay
	ctx    context.Context
	cancel context.CancelFunc
	done   chan error
}

func start(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, dir: t.TempDir(), relay: relaytest.New(t), done: make(chan error, 1)}
	e.ctx, e.cancel = context.WithCancel(context.Background())
	go func() {
		e.done <- Run(e.ctx, Options{
			Dir: e.dir, Version: "9.9", QueueSettle: 20 * time.Millisecond,
			Config: Config{RelayURL: e.relay.URL, Name: "test-pc"},
		})
	}()
	t.Cleanup(func() {
		e.cancel()
		select {
		case <-e.done:
		case <-time.After(10 * time.Second):
			t.Error("the daemon did not stop")
		}
	})
	e.waitFor("the daemon's endpoint", func() bool { _, err := ipc.ReadEndpoint(e.dir); return err == nil })
	return e
}

func (e *env) waitFor(what string, cond func() bool) {
	e.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			e.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (e *env) control() *ipc.ControlClient {
	e.t.Helper()
	ep, err := ipc.ReadEndpoint(e.dir)
	if err != nil {
		e.t.Fatal(err)
	}
	c, err := ipc.DialControl(e.ctx, ep)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { c.Close() })
	return c
}

func (e *env) status() StatusResult {
	e.t.Helper()
	var st StatusResult
	if err := e.control().Call(e.ctx, "status", nil, &st); err != nil {
		e.t.Fatal(err)
	}
	return st
}

// share registers a project and offers a fake Deck for it, like the TUI does.
func (e *env) share() (string, *remotetest.Fake) {
	e.t.Helper()
	reg, err := projects.Open(e.dir)
	if err != nil {
		e.t.Fatal(err)
	}
	p, err := reg.Add(e.t.TempDir(), "deck")
	if err != nil {
		e.t.Fatal(err)
	}
	fake := remotetest.New()
	go Provide(e.ctx, e.dir, p.ID, fake)
	e.waitFor("the project to be online", func() bool {
		for _, pi := range e.status().Projects {
			if pi.ID == p.ID && pi.Online {
				return true
			}
		}
		return false
	})
	return p.ID, fake
}

// phone is the far end of a relay lane.
type phone struct {
	t    *testing.T
	e    *env
	kp   e2e.Keypair
	lane string
	sess *e2e.Session
}

func (p *phone) send(frame any) {
	p.t.Helper()
	raw, _ := json.Marshal(frame)
	msgs, err := p.sess.Seal(raw)
	if err != nil {
		p.t.Fatal(err)
	}
	for _, m := range msgs {
		p.e.relay.PhoneData(p.lane, m)
	}
}

func (p *phone) next() []byte {
	p.t.Helper()
	f, ok := p.e.relay.WaitFrame(wire.HostData, 10*time.Second)
	if !ok || f.Lane != p.lane {
		p.t.Fatalf("no data on lane %s (got %+v, ok=%v)", p.lane, f, ok)
	}
	raw, err := base64.StdEncoding.DecodeString(f.B64)
	if err != nil {
		p.t.Fatal(err)
	}
	return raw
}

func (p *phone) recv() wire.Frame3 {
	p.t.Helper()
	for {
		raw, err := p.sess.Open(p.next())
		if err != nil {
			p.t.Fatalf("open: %v", err)
		}
		if raw == nil {
			continue
		}
		var f wire.Frame3
		if err := json.Unmarshal(raw, &f); err != nil {
			p.t.Fatal(err)
		}
		return f
	}
}

func (p *phone) call(pj, method string, params any) wire.Frame3 {
	p.t.Helper()
	raw, _ := json.Marshal(params)
	p.send(wire.Frame3{T: wire.FrameReq, ID: method, M: method, PJ: pj, P: raw})
	for {
		if f := p.recv(); f.T == wire.FrameRes && f.ID == method {
			return f
		}
	}
}

// pair runs a full pairing through the relay and returns the new device.
func (e *env) pair() (e2e.Keypair, wire.PairWelcome) {
	e.t.Helper()
	e.waitFor("the relay link", func() bool { return e.status().RelayOnline })
	var res PairResult
	if err := e.control().Call(e.ctx, "pair", nil, &res); err != nil {
		e.t.Fatal(err)
	}
	if !strings.HasPrefix(res.URL, e.relay.URL+"/#p=") {
		e.t.Fatalf("pair url %q", res.URL)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(res.URL, e.relay.URL+"/#p="))
	if err != nil {
		e.t.Fatal(err)
	}
	var blob wire.QRBlob
	if err := json.Unmarshal(raw, &blob); err != nil {
		e.t.Fatal(err)
	}
	computer, _ := base64.RawURLEncoding.DecodeString(blob.S)
	psk, _ := base64.RawURLEncoding.DecodeString(blob.PSK)

	// The relay was told to open a pairing window for this secret.
	pf, ok := e.relay.WaitFrame(wire.HostPairOpen, 5*time.Second)
	id, _ := daemon.PairIDOf(psk)
	if !ok || pf.PairIDHash != daemon.PairIDHash(id) {
		e.t.Fatalf("pair_open %+v ok=%v", pf, ok)
	}

	kp, _ := e2e.GenerateKeypair(rand.Reader)
	p := &phone{t: e.t, e: e, kp: kp, lane: "pair-1"}
	if !e.relay.OpenLane(p.lane, "", true) {
		e.t.Fatal("no host connected")
	}
	hello, _ := json.Marshal(wire.PairHello{DeviceName: "Pixel 9"})
	msg1, pend, err := e2e.Initiator{Static: kp, PeerStatic: computer, CID: blob.CID, PSK: psk}.Start(hello)
	if err != nil {
		e.t.Fatal(err)
	}
	e.relay.PhoneData(p.lane, msg1)
	reply, sess, err := pend.Finish(p.next())
	if err != nil {
		e.t.Fatalf("pairing handshake: %v", err)
	}
	p.sess = sess
	var w wire.PairWelcome
	if err := json.Unmarshal(reply, &w); err != nil {
		e.t.Fatal(err)
	}
	p.send(wire.Frame3{T: wire.FrameHello, Hello3: &wire.Hello3{V: wire.ProtocolV3}})

	// The relay is told the new phone may connect.
	df, ok := e.relay.WaitFrame(wire.HostDevices, 10*time.Second)
	for ok && len(df.Hashes) == 0 {
		df, ok = e.relay.WaitFrame(wire.HostDevices, 10*time.Second)
	}
	if !ok || len(df.Hashes) != 1 || df.Hashes[0] != devstore.HashToken(w.RelayToken) {
		e.t.Fatalf("devices frame %+v ok=%v", df, ok)
	}
	return kp, w
}

// connect opens a normal session as a paired phone.
func (e *env) connect(kp e2e.Keypair, w wire.PairWelcome, lane string) (*phone, wire.Frame3) {
	e.t.Helper()
	ep := e.status()
	ident := e2eIdent(e.t, e.dir)
	p := &phone{t: e.t, e: e, kp: kp, lane: lane}
	if !e.relay.OpenLane(lane, devstore.HashToken(w.RelayToken), false) {
		e.t.Fatal("no host connected")
	}
	msg1, pend, err := e2e.Initiator{Static: kp, PeerStatic: ident.StaticPublic(), CID: ep.CID}.Start(nil)
	if err != nil {
		e.t.Fatal(err)
	}
	e.relay.PhoneData(lane, msg1)
	_, sess, err := pend.Finish(p.next())
	if err != nil {
		e.t.Fatalf("session handshake: %v", err)
	}
	p.sess = sess
	p.send(wire.Frame3{T: wire.FrameHello, Hello3: &wire.Hello3{V: wire.ProtocolV3}})
	return p, p.recv()
}

func TestStatusAndRefusalsBeforeAnyRelayIsConfigured(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, Options{Dir: dir, Version: "9.9"}) }()
	t.Cleanup(func() { cancel(); <-done })
	e := &env{t: t, dir: dir, ctx: ctx}
	e.waitFor("the daemon", func() bool { _, err := ipc.ReadEndpoint(dir); return err == nil })

	err := e.control().Call(ctx, "pair", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "relay") {
		t.Fatalf("pairing without a relay: %v", err)
	}
	if err := e.control().Call(ctx, "nonsense", nil, nil); err == nil {
		t.Fatal("an unknown command succeeded")
	}
}

func TestOnlyOneDaemonPerUser(t *testing.T) {
	e := start(t)
	err := Run(context.Background(), Options{Dir: e.dir})
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("got %v", err)
	}
}

func TestControlRefusesWithoutTheSecret(t *testing.T) {
	e := start(t)
	ep, _ := ipc.ReadEndpoint(e.dir)
	ep.Secret = "wrong"
	c, err := ipc.DialControl(e.ctx, ep)
	if err == nil {
		defer c.Close()
		if err := c.Call(e.ctx, "status", nil, nil); err == nil {
			t.Fatal("a control client without the secret was served")
		}
	}
}

func TestUnsharedProjectsAreRefusedAsProviders(t *testing.T) {
	e := start(t)
	fake := remotetest.New()
	go Provide(e.ctx, e.dir, "prj_not_shared", fake)
	time.Sleep(500 * time.Millisecond)
	for _, p := range e.status().Projects {
		if p.Online {
			t.Fatalf("an unshared project went online: %+v", p)
		}
	}
}

func TestPhoneFromPairingToQueuedWork(t *testing.T) {
	e := start(t)
	pj, fake := e.share()

	kp, w := e.pair()
	if w.DeviceID == "" || w.Name != "test-pc" {
		t.Fatalf("welcome %+v", w)
	}
	if got := e.status().Devices; got != 1 {
		t.Fatalf("daemon reports %d devices", got)
	}

	// A normal session over the relay sees the shared project and its inbox.
	p, welcome := e.connect(kp, w, "lane-1")
	if welcome.T != wire.FrameWelcome || len(welcome.Server.Projects) != 1 || welcome.Server.Projects[0].ID != pj || !welcome.Server.Projects[0].Online {
		t.Fatalf("welcome %+v", welcome)
	}
	res := p.call(pj, wire.MSessionsList, struct{}{})
	var sessions []wire.Session
	if res.OK == nil || !*res.OK || json.Unmarshal(res.R, &sessions) != nil || len(sessions) == 0 {
		t.Fatalf("sessions %+v", res)
	}

	// The phone goes away and leaves a prompt in the mailbox; the daemon runs
	// it with no phone connected and leaves a receipt.
	ep := e.status()
	ident := e2eIdent(t, e.dir)
	qa := wire.QueuedAction{
		QID: "0192f3a0-aaaa-7d2e-9a44-000000000001", DeviceID: w.DeviceID,
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
		PJ: pj, SessionID: "s1", Kind: wire.QKindChatSend, Payload: json.RawMessage(`{"text":"finish the refactor"}`),
	}
	raw, _ := json.Marshal(qa)
	sealed, err := e2e.SealK(kp, ident.StaticPublic(), ep.CID, raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !e.relay.Mail(qa.QID, devstore.HashToken(w.RelayToken), sealed) {
		t.Fatal("no host connected")
	}
	ack, ok := e.relay.WaitFrame(wire.HostAck, 10*time.Second)
	if !ok || ack.ID != qa.QID {
		t.Fatalf("mail was not acked: %+v ok=%v", ack, ok)
	}
	e.waitFor("the queued prompt to run", func() bool { return len(fake.Sent()) == 1 })
	if s := fake.Sent()[0]; s.Text != "finish the refactor" || s.SessionID != "s1" {
		t.Fatalf("sent %+v", s)
	}
	rs := p.call("", wire.MQueueStatus, struct{}{})
	var receipts []wire.Receipt
	if json.Unmarshal(rs.R, &receipts) != nil || len(receipts) != 1 || receipts[0].Status != wire.ReceiptApplied {
		t.Fatalf("receipts %+v", rs)
	}

	// The same mail redelivered (the relay never saw the ack) changes nothing.
	e.relay.Mail(qa.QID, devstore.HashToken(w.RelayToken), sealed)
	time.Sleep(300 * time.Millisecond)
	if n := len(fake.Sent()); n != 1 {
		t.Fatalf("redelivery ran the prompt again (%d)", n)
	}

	// A snapshot is left for the phone, readable only by it.
	e.relay.WaitFrame(wire.HostSnap, 15*time.Second)
}

func TestRevokingFromTheCLIDropsThePhoneAndTheRelayEntry(t *testing.T) {
	e := start(t)
	e.share()
	kp, w := e.pair()
	p, _ := e.connect(kp, w, "lane-r")
	_ = p

	store, err := devstore.Open(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Revoke(w.DeviceID); err != nil {
		t.Fatal(err)
	}
	// The daemon notices the edit, and tells the relay to stop accepting the token.
	e.waitFor("the relay to be told", func() bool {
		f, ok := e.relay.WaitFrame(wire.HostDevices, 200*time.Millisecond)
		return ok && len(f.Hashes) == 0
	})
	e.waitFor("the lane to be closed", func() bool {
		f, ok := e.relay.WaitFrame(wire.HostClose, 200*time.Millisecond)
		return ok && f.Lane == "lane-r" && f.Code == wire.CloseRevoked
	})
}

func e2eIdent(t *testing.T, dir string) *identity.Identity {
	t.Helper()
	id, err := identity.LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
