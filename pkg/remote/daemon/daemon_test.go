package daemon

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/devstore"
	"github.com/dev-dami/deck/pkg/remote/e2e"
	"github.com/dev-dami/deck/pkg/remote/identity"
	"github.com/dev-dami/deck/pkg/remote/projects"
	"github.com/dev-dami/deck/pkg/remote/remotetest"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// pipeConn is one end of an in-memory message pipe.
type pipeConn struct {
	in, out chan []byte
	mu      sync.Mutex
	closed  chan struct{}
	code    int
	once    sync.Once
	peer    *pipeConn
}

func pipe() (a, b *pipeConn) {
	ab, ba := make(chan []byte, 512), make(chan []byte, 512)
	a = &pipeConn{in: ba, out: ab, closed: make(chan struct{})}
	b = &pipeConn{in: ab, out: ba, closed: make(chan struct{})}
	a.peer, b.peer = b, a
	return
}

func (p *pipeConn) Read(ctx context.Context) ([]byte, error) {
	select {
	case m := <-p.in:
		return m, nil
	case <-p.closed:
		return nil, errors.New("closed")
	case <-p.peer.closed:
		select {
		case m := <-p.in:
			return m, nil
		default:
		}
		return nil, errors.New("peer closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *pipeConn) Write(ctx context.Context, m []byte) error {
	select {
	case p.out <- m:
		return nil
	case <-p.closed:
		return errors.New("closed")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *pipeConn) Close(code int, _ string) error {
	p.once.Do(func() {
		p.mu.Lock()
		p.code = code
		p.mu.Unlock()
		close(p.closed)
	})
	return nil
}

func (p *pipeConn) closeCode() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.code
}

type rig struct {
	t       *testing.T
	d       *Daemon
	devs    *devstore.Store
	reg     *projects.Registry
	fake    *remotetest.Fake
	pj      string
	phone   e2e.Keypair
	device  devstore.Device
	ident   *identity.Identity
	nextReq int
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	ident, err := identity.LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	devs, err := devstore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := projects.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	proj, err := reg.Add(t.TempDir(), "deck")
	if err != nil {
		t.Fatal(err)
	}
	phone, err := e2e.GenerateKeypair(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dev, _, err := devs.Add("Pixel 9", phone.Public)
	if err != nil {
		t.Fatal(err)
	}
	d := New(Config{Identity: ident, Devices: devs, Projects: reg, Name: "test-pc", Version: "9.9"})
	fake := remotetest.New()
	d.Attach(proj.ID, fake)
	return &rig{t: t, d: d, devs: devs, reg: reg, fake: fake, pj: proj.ID, phone: phone, device: dev, ident: ident}
}

// phoneConn is a connected phone.
type phoneConn struct {
	t      *testing.T
	c      *pipeConn
	sess   *e2e.Session
	r      *rig
	done   chan error
	cancel context.CancelFunc
}

func (r *rig) connect(resume *wire.Resume3) (*phoneConn, wire.Frame3) {
	return r.connectAs(r.phone, resume)
}

func (r *rig) connectAs(phone e2e.Keypair, resume *wire.Resume3) (*phoneConn, wire.Frame3) {
	r.t.Helper()
	pc, dc := pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() { done <- r.d.Serve(ctx, dc); close(finished) }()
	// Stop and wait, so no connection goroutine is still writing the device
	// file when the temp directory is removed.
	r.t.Cleanup(func() { cancel(); <-finished })

	msg1, pend, err := e2e.Initiator{Static: phone, PeerStatic: r.ident.StaticPublic(), CID: r.ident.CID()}.Start(nil)
	if err != nil {
		r.t.Fatal(err)
	}
	cl := &phoneConn{t: r.t, c: pc, r: r, done: done, cancel: cancel}
	if err := pc.Write(ctx, msg1); err != nil {
		r.t.Fatal(err)
	}
	msg2, err := pc.Read(ctx2(r.t, 3*time.Second, pc))
	if err != nil {
		return cl, wire.Frame3{}
	}
	_, sess, err := pend.Finish(msg2)
	if err != nil {
		r.t.Fatalf("finish: %v", err)
	}
	cl.sess = sess
	cl.send(wire.Frame3{T: wire.FrameHello, Hello3: &wire.Hello3{V: wire.ProtocolV3, Resume: resume}})
	return cl, cl.recv()
}

func ctx2(t *testing.T, d time.Duration, _ *pipeConn) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

func (c *phoneConn) send(f wire.Frame3) {
	c.t.Helper()
	raw, _ := json.Marshal(f)
	msgs, err := c.sess.Seal(raw)
	if err != nil {
		c.t.Fatal(err)
	}
	for _, m := range msgs {
		if err := c.c.Write(context.Background(), m); err != nil {
			c.t.Fatal(err)
		}
	}
}

func (c *phoneConn) recv() wire.Frame3 {
	c.t.Helper()
	f, err := c.recvErr(3 * time.Second)
	if err != nil {
		c.t.Fatalf("recv: %v", err)
	}
	return f
}

func (c *phoneConn) recvErr(wait time.Duration) (wire.Frame3, error) {
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	for {
		m, err := c.c.Read(ctx)
		if err != nil {
			return wire.Frame3{}, err
		}
		raw, err := c.sess.Open(m)
		if err != nil {
			return wire.Frame3{}, err
		}
		if raw == nil {
			continue
		}
		var f wire.Frame3
		if err := json.Unmarshal(raw, &f); err != nil {
			return wire.Frame3{}, err
		}
		return f, nil
	}
}

// call sends a request and returns its response, skipping interleaved events.
func (c *phoneConn) call(pj, method string, params any) wire.Frame3 {
	c.t.Helper()
	c.r.nextReq++
	id := fmt.Sprint(c.r.nextReq)
	var p json.RawMessage
	if params != nil {
		p, _ = json.Marshal(params)
	}
	c.send(wire.Frame3{T: wire.FrameReq, ID: id, M: method, PJ: pj, P: p})
	for {
		f := c.recv()
		if f.T == wire.FrameRes && f.ID == id {
			return f
		}
	}
}

func okResult[T any](t *testing.T, f wire.Frame3) T {
	t.Helper()
	var v T
	if f.OK == nil || !*f.OK {
		t.Fatalf("request failed: %+v", f.Err)
	}
	if err := json.Unmarshal(f.R, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func wantErr(t *testing.T, f wire.Frame3, code string) {
	t.Helper()
	if f.OK == nil || *f.OK || f.Err == nil || f.Err.Code != code {
		t.Fatalf("want error %q, got ok=%v err=%+v", code, f.OK, f.Err)
	}
}

func TestWelcomeCarriesProjectsAndNoPaths(t *testing.T) {
	r := newRig(t)
	_, w := r.connect(nil)
	if w.T != wire.FrameWelcome || w.Welcome3 == nil {
		t.Fatalf("got %+v", w)
	}
	s := w.Server
	if s.CID != r.ident.CID() || s.Name != "test-pc" || s.Protocol != wire.ProtocolV3 {
		t.Fatalf("server %+v", s)
	}
	if len(s.Projects) != 1 || s.Projects[0].ID != r.pj || !s.Projects[0].Online || s.Projects[0].Name != "deck" {
		t.Fatalf("projects %+v", s.Projects)
	}
	raw, _ := json.Marshal(w)
	for _, p := range r.reg.List() {
		if strings.Contains(string(raw), strings.ReplaceAll(p.Path, `\`, `\\`)) {
			t.Fatal("a project path reached the phone")
		}
	}
	if w.Device.ID != r.device.ID {
		t.Fatalf("device %+v", w.Device)
	}
}

func TestUnknownPhoneIsRefused(t *testing.T) {
	r := newRig(t)
	stranger, _ := e2e.GenerateKeypair(rand.Reader)
	cl, _ := r.connectAs(stranger, nil)
	select {
	case <-cl.done:
	case <-time.After(3 * time.Second):
		t.Fatal("daemon kept a stranger's connection")
	}
	if cl.c.peer.closeCode() != wire.CloseTokenInvalid {
		t.Fatalf("close code %d", cl.c.peer.closeCode())
	}
}

func TestProjectRouting(t *testing.T) {
	r := newRig(t)
	cl, _ := r.connect(nil)

	sessions := okResult[[]wire.Session](t, cl.call(r.pj, wire.MSessionsList, nil))
	if len(sessions) != 1 || sessions[0].ID != "s1" {
		t.Fatalf("sessions %+v", sessions)
	}
	wantErr(t, cl.call("", wire.MSessionsList, nil), wire.CodeNotFound)
	wantErr(t, cl.call("prj_nope", wire.MSessionsList, nil), wire.CodeNotFound)

	// A project that is registered but whose provider left is unavailable.
	r.d.Detach(r.pj, r.fake)
	wantErr(t, cl.call(r.pj, wire.MSessionsList, nil), wire.CodeNotFound)
	// A disabled one is, too.
	r.d.Attach(r.pj, r.fake)
	if err := r.reg.SetEnabled(r.pj, false); err != nil {
		t.Fatal(err)
	}
	wantErr(t, cl.call(r.pj, wire.MSessionsList, nil), wire.CodeNotFound)
}

func TestInboxStatesOrderAndUnread(t *testing.T) {
	r := newRig(t)
	r.fake.AddSession("busy", "Busy one").Session.Busy = true
	r.fake.AddSession("wait", "Waiting").Approvals = []wire.Approval{{ID: "a1", SessionID: "wait", Kind: "command", Command: "ls", CreatedAt: time.Now()}}
	r.fake.Get("s1").Session.UpdatedAt = time.Now().Add(time.Hour) // after pairing

	cl, _ := r.connect(nil)
	items := okResult[[]wire.InboxItem](t, cl.call("", wire.MInboxGet, nil))
	states := map[string]string{}
	var order []string
	for _, it := range items {
		states[it.SessionID] = it.State
		order = append(order, it.State)
	}
	if states["wait"] != wire.InboxNeedsYou || states["busy"] != wire.InboxRunning || states["s1"] != wire.InboxDone {
		t.Fatalf("states %v", states)
	}
	if order[0] != wire.InboxNeedsYou || order[1] != wire.InboxRunning || order[2] != wire.InboxDone {
		t.Fatalf("order %v", order)
	}
	var s1 wire.InboxItem
	for _, it := range items {
		if it.SessionID == "s1" {
			s1 = it
		}
		if it.SessionID == "wait" && it.Approvals != 1 {
			t.Fatalf("approvals %d", it.Approvals)
		}
	}
	if !s1.Unread {
		t.Fatal("a session updated after pairing should be unread")
	}
	// The ack must land after the session's last activity, which the test set
	// an hour ahead.
	r.d.cfg.Now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	okResult[wire.Empty](t, cl.call("", wire.MInboxAck, wire.InboxAckParams{PJ: r.pj, SessionID: "s1"}))
	items = okResult[[]wire.InboxItem](t, cl.call("", wire.MInboxGet, nil))
	for _, it := range items {
		if it.SessionID == "s1" && it.Unread {
			t.Fatal("ack did not clear unread")
		}
	}
}

func TestApprovalsFailClosed(t *testing.T) {
	r := newRig(t)
	r.fake.Get("s1").Approvals = []wire.Approval{
		{ID: "a1", SessionID: "s1", Kind: "command", Command: "rm -rf build", CreatedAt: time.Now()},
		{ID: "a2", SessionID: "s1", Kind: "command", Command: "go test", CreatedAt: time.Now()},
	}
	r.fake.AddSession("s2", "other").Approvals = []wire.Approval{{ID: "b1", SessionID: "s2", Kind: "command", Command: "x", CreatedAt: time.Now()}}
	cl, _ := r.connect(nil)

	list := okResult[[]wire.ApprovalV3](t, cl.call(r.pj, wire.MApprovalsList, wire.ApprovalsListParams{SessionID: "s1"}))
	if len(list) != 2 || list[0].Tier != wire.TierDestructive || list[0].Digest != Digest(list[0].Approval) || len(list[0].Digest) != 64 {
		t.Fatalf("list %+v", list)
	}
	if Digest(list[0].Approval) == Digest(list[1].Approval) {
		t.Fatal("two different commands share a digest")
	}

	wantErr(t, cl.call(r.pj, wire.MApprovalsAnswer, wire.ApprovalsAnswer3Params{SessionID: "s1", ID: "a1", Decision: wire.DecisionAllow}), wire.CodeAssertionRequired)
	wantErr(t, cl.call(r.pj, wire.MApprovalsAnswer, wire.ApprovalsAnswer3Params{SessionID: "s1", ID: "a2", Decision: wire.DecisionAllowSession}), wire.CodeAssertionRequired)
	if len(r.fake.Answered()) != 0 {
		t.Fatalf("an allow reached the backend: %v", r.fake.Answered())
	}
	// An id from another session is not found (R10).
	wantErr(t, cl.call(r.pj, wire.MApprovalsAnswer, wire.ApprovalsAnswer3Params{SessionID: "s1", ID: "b1", Decision: wire.DecisionDeny}), wire.CodeNotFound)
	wantErr(t, cl.call(r.pj, wire.MApprovalsAnswer, wire.ApprovalsAnswer3Params{SessionID: "s1", ID: "a1", Decision: "maybe"}), wire.CodeInvalid)

	res := okResult[wire.ApprovalsAnswerResult](t, cl.call(r.pj, wire.MApprovalsAnswer,
		wire.ApprovalsAnswer3Params{SessionID: "s1", ID: "a1", Decision: wire.DecisionDeny, Reason: "use a dry run first"}))
	if !res.Resolved || r.fake.Answered()["a1"] != wire.DecisionDeny {
		t.Fatalf("deny not applied: %+v %v", res, r.fake.Answered())
	}
	sent := r.fake.Sent()
	if len(sent) != 1 || sent[0].Text != "Denied from phone: use a dry run first" {
		t.Fatalf("deny reason not delivered to the agent: %+v", sent)
	}
}

func TestYoloFailsClosed(t *testing.T) {
	r := newRig(t)
	cl, _ := r.connect(nil)
	wantErr(t, cl.call(r.pj, wire.MModeSet, wire.ModeSet3Params{SessionID: "s1", Mode: wire.ModeYolo}), wire.CodeYoloDisabled)
	if err := r.devs.SetYolo(r.device.ID, true); err != nil {
		t.Fatal(err)
	}
	wantErr(t, cl.call(r.pj, wire.MModeSet, wire.ModeSet3Params{SessionID: "s1", Mode: wire.ModeYolo}), wire.CodeAssertionRequired)
	if len(r.fake.Modes()) != 0 {
		t.Fatalf("yolo reached the backend: %v", r.fake.Modes())
	}
	res := okResult[wire.ModeResult](t, cl.call(r.pj, wire.MModeSet, wire.ModeSet3Params{SessionID: "s1", Mode: wire.ModePlan}))
	if res.Mode != wire.ModePlan {
		t.Fatalf("plan mode: %+v", res)
	}
}

func TestRevokedPhoneIsDropped(t *testing.T) {
	r := newRig(t)
	cl, _ := r.connect(nil)
	okResult[[]wire.ProjectRef](t, cl.call("", wire.MProjectsList, nil))
	if err := r.devs.Revoke(r.device.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cl.done:
	case <-time.After(2*revokeCheck + 2*time.Second):
		t.Fatal("a revoked phone stayed connected")
	}
	if got := cl.c.peer.closeCode(); got != wire.CloseRevoked {
		t.Fatalf("close code %d", got)
	}
}

func TestEventsCarryTheirProjectAndScopedOnesNeedFocus(t *testing.T) {
	r := newRig(t)
	cl, _ := r.connect(nil)

	r.fake.Inject(remote.Event{Name: wire.EvNotice, SessionID: "s1", Scoped: true, Payload: wire.Notice{SessionID: "s1", Text: "scoped"}})
	r.fake.Inject(remote.Event{Name: wire.EvModeChanged, Payload: wire.ModeChanged{SessionID: "s1", Mode: wire.ModeAsk}})
	f := cl.recv()
	if f.T != wire.FrameEv || f.E != wire.EvModeChanged || f.PJ != r.pj || f.Seq == 0 {
		t.Fatalf("first event %+v", f)
	}

	okResult[wire.Empty](t, cl.call(r.pj, wire.MSessionsFocus, wire.IDParams{ID: "s1"}))
	r.fake.Inject(remote.Event{Name: wire.EvNotice, SessionID: "s1", Scoped: true, Payload: wire.Notice{SessionID: "s1", Text: "scoped2"}})
	for {
		f = cl.recv()
		if f.E == wire.EvNotice {
			break
		}
	}
	if !strings.Contains(string(f.P), "scoped2") {
		t.Fatalf("got %s", f.P)
	}
	wantErr(t, cl.call(r.pj, wire.MSessionsFocus, wire.IDParams{ID: "nope"}), wire.CodeNotFound)
}

func TestInboxChangedIsPublished(t *testing.T) {
	r := newRig(t)
	cl, _ := r.connect(nil)
	r.fake.Inject(remote.Event{Name: wire.EvBusy, SessionID: "s1", Payload: wire.BusyChanged{SessionID: "s1", Busy: true}})
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("no inbox.changed after a busy event")
		default:
		}
		f, err := cl.recvErr(time.Second)
		if err != nil {
			continue
		}
		if f.E == wire.EvInboxChanged {
			var items []wire.InboxItem
			if json.Unmarshal(f.P, &items) != nil || len(items) == 0 {
				t.Fatalf("payload %s", f.P)
			}
			return
		}
	}
}

func TestResumeReplaysOrResyncs(t *testing.T) {
	r := newRig(t)
	cl, w := r.connect(nil)
	epoch := w.Epoch
	okResult[wire.Empty](t, cl.call("", wire.MInboxAck, wire.InboxAckParams{PJ: r.pj, SessionID: "s1"}))
	cl.cancel()

	r.fake.Inject(remote.Event{Name: wire.EvModeChanged, Payload: wire.ModeChanged{SessionID: "s1", Mode: wire.ModePlan}})
	time.Sleep(200 * time.Millisecond)

	_, w2 := r.connect(&wire.Resume3{Epoch: epoch, Seq: 0})
	if !w2.Replay {
		t.Fatalf("missed events were not replayed: %+v", w2.Welcome3)
	}
	cl3, w3 := r.connect(&wire.Resume3{Epoch: "another-run", Seq: 5})
	if w3.Replay {
		t.Fatal("replayed into a different epoch")
	}
	if f := cl3.recv(); f.T != wire.FrameResync {
		t.Fatalf("want resync, got %+v", f)
	}
}

func TestFirstHunk(t *testing.T) {
	diff := "--- a\n+++ b\n@@ -1 +1 @@\n-x\n+y\n@@ -9 +9 @@\n-p\n+q\n"
	got, cut := firstHunk(diff)
	if !cut || strings.Contains(got, "-p") || !strings.Contains(got, "+y") {
		t.Fatalf("got %q cut=%v", got, cut)
	}
	if got, cut := firstHunk("@@ -1 +1 @@\n-x\n+y\n"); cut || !strings.Contains(got, "+y") {
		t.Fatalf("single hunk: %q %v", got, cut)
	}
	big := "@@ -1 +1 @@\n" + strings.Repeat("+z\n", 5000)
	if got, cut := firstHunk(big); !cut || len(got) > wire.MaxChangeSummaryBytes {
		t.Fatalf("large hunk: %d bytes cut=%v", len(got), cut)
	}
}
