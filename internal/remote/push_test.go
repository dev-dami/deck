package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/remote/push"
	"github.com/dev-dami/deck/internal/remote/wire"
)

type pushed struct {
	Sub     push.Subscription
	Message push.Message
}

type recordingSender struct {
	mu  sync.Mutex
	got []pushed
}

func (r *recordingSender) Send(_ context.Context, sub push.Subscription, payload []byte, _ bool) (int, error) {
	var m push.Message
	_ = json.Unmarshal(payload, &m)
	r.mu.Lock()
	r.got = append(r.got, pushed{sub, m})
	r.mu.Unlock()
	return http.StatusCreated, nil
}

func (r *recordingSender) all() []pushed {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]pushed(nil), r.got...)
}

const fcm = "https://fcm.googleapis.com/fcm/send/phone1"

func pushRig(t *testing.T) (*rig, *recordingSender) {
	t.Helper()
	rs := &recordingSender{}
	svc, err := push.New(t.TempDir(), rs)
	require.NoError(t, err)
	return newRig(t, Options{Push: svc}), rs
}

func subscribeReq(endpoint string, approvals, done bool) wire.PushSubscribeReq {
	var r wire.PushSubscribeReq
	r.Subscription.Endpoint = endpoint
	r.Subscription.Keys.P256dh, r.Subscription.Keys.Auth = "p", "a"
	r.Approvals, r.Done = approvals, done
	return r
}

func TestMessageEditRPC(t *testing.T) {
	r := newRig(t, Options{})
	c, _ := r.dial(r.token(), nil)
	okRes(t, c.call(wire.MMessageEdit, wire.MessageEditReq{SessionID: "s1", MessageID: "m1", Text: "better"}))
	require.Equal(t, []string{"s1/m1/better"}, r.be.edits)

	for name, req := range map[string]wire.MessageEditReq{
		"no text":    {SessionID: "s1", MessageID: "m1", Text: "  "},
		"no message": {SessionID: "s1", Text: "x"},
		"no session": {MessageID: "m1", Text: "x"},
	} {
		require.Equal(t, wire.CodeInvalid, errCode(t, c.call(wire.MMessageEdit, req)), name)
	}
	require.Len(t, r.be.edits, 1, "refused edits never reach the backend")
}

func TestMessageEditMapsBackendRefusals(t *testing.T) {
	r := newRig(t, Options{})
	c, _ := r.dial(r.token(), nil)
	req := wire.MessageEditReq{SessionID: "s1", MessageID: "m1", Text: "x"}

	r.be.editErr = fmt.Errorf("%w: this message can't be edited", ErrInvalid)
	require.Equal(t, wire.CodeInvalid, errCode(t, c.call(wire.MMessageEdit, req)))
	r.be.editErr = ErrNotFound
	require.Equal(t, wire.CodeNotFound, errCode(t, c.call(wire.MMessageEdit, req)))
	r.be.editErr = errors.New("boom")
	require.Equal(t, wire.CodeInternal, errCode(t, c.call(wire.MMessageEdit, req)))
}

func TestWelcomeAdvertisesPushAndEdit(t *testing.T) {
	r, _ := pushRig(t)
	_, w := r.dial(r.token(), nil)
	require.NotEmpty(t, w.Server.PushKey)
	require.Contains(t, w.Server.Features, "push")
	require.Contains(t, w.Server.Features, "edit")

	plain := newRig(t, Options{})
	_, w = plain.dial(plain.token(), nil)
	require.Empty(t, w.Server.PushKey)
	require.NotContains(t, w.Server.Features, "push")
}

func TestPushRPCsWithoutPush(t *testing.T) {
	r := newRig(t, Options{})
	c, _ := r.dial(r.token(), nil)
	var st wire.PushStatus
	require.NoError(t, json.Unmarshal(okRes(t, c.call(wire.MPushStatus, struct{}{})), &st))
	require.False(t, st.Available)
	require.Equal(t, wire.CodeInvalid, errCode(t, c.call(wire.MPushSubscribe, subscribeReq(fcm, true, true))))
}

func TestPushSubscribeStatusUnsubscribe(t *testing.T) {
	r, _ := pushRig(t)
	c, _ := r.dial(r.token(), nil)
	var st wire.PushStatus
	require.NoError(t, json.Unmarshal(okRes(t, c.call(wire.MPushStatus, struct{}{})), &st))
	require.Equal(t, wire.PushStatus{Available: true}, st)

	okRes(t, c.call(wire.MPushSubscribe, subscribeReq(fcm, true, false)))
	require.NoError(t, json.Unmarshal(okRes(t, c.call(wire.MPushStatus, struct{}{})), &st))
	require.Equal(t, wire.PushStatus{Available: true, Enabled: true, Approvals: true}, st)

	okRes(t, c.call(wire.MPushUnsubscribe, struct{}{}))
	require.NoError(t, json.Unmarshal(okRes(t, c.call(wire.MPushStatus, struct{}{})), &st))
	require.False(t, st.Enabled)
}

func TestPushSubscribeRefusesForeignEndpoints(t *testing.T) {
	r, _ := pushRig(t)
	c, _ := r.dial(r.token(), nil)
	for _, bad := range []string{"https://evil.example/x", "http://fcm.googleapis.com/x", "https://169.254.169.254/x", ""} {
		require.Equal(t, wire.CodeInvalid, errCode(t, c.call(wire.MPushSubscribe, subscribeReq(bad, true, true))), bad)
	}
}

func TestPushTest(t *testing.T) {
	r, rs := pushRig(t)
	c, _ := r.dial(r.token(), nil)
	require.Equal(t, wire.CodeInternal, errCode(t, c.call(wire.MPushTest, struct{}{})), "not subscribed yet")
	okRes(t, c.call(wire.MPushSubscribe, subscribeReq(fcm, false, false)))
	okRes(t, c.call(wire.MPushTest, struct{}{}))
	got := rs.all()
	require.Len(t, got, 1)
	require.Equal(t, "test", got[0].Message.Tag)
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	require.Eventually(t, cond, 2*time.Second, 10*time.Millisecond)
}

func TestApprovalRequestSendsAPush(t *testing.T) {
	r, rs := pushRig(t)
	c, _ := r.dial(r.token(), nil)
	okRes(t, c.call(wire.MPushSubscribe, subscribeReq(fcm, true, false)))

	r.be.events <- Event{Name: wire.EvApprovalRequested, SessionID: "s1",
		Payload: wire.Approval{ID: "p1", SessionID: "s1", Kind: wire.ApprovalCommand, Command: "rm -rf   build\n && echo done"}}
	eventually(t, func() bool { return len(rs.all()) == 1 })
	m := rs.all()[0].Message
	require.Equal(t, "Approval needed", m.Title)
	require.Equal(t, "run: rm -rf build && echo done", m.Body, "whitespace is collapsed to one line")
	require.Equal(t, "p1", m.Approval)
	require.Equal(t, "s1", m.SessionID)
}

func TestApprovalPushRespectsPreferences(t *testing.T) {
	r, rs := pushRig(t)
	c, _ := r.dial(r.token(), nil)
	okRes(t, c.call(wire.MPushSubscribe, subscribeReq(fcm, false, true)))
	r.be.events <- Event{Name: wire.EvApprovalRequested, Payload: wire.Approval{ID: "p1", Kind: wire.ApprovalEdit, Path: "a.go"}}
	// A later event proves the first one was handled and produced nothing.
	r.be.events <- Event{Name: wire.EvNotice, Payload: map[string]string{}}
	c.nextEvent(wire.EvNotice)
	time.Sleep(50 * time.Millisecond)
	require.Empty(t, rs.all())
}

func TestApprovalBodies(t *testing.T) {
	long := wire.Approval{Kind: wire.ApprovalCommand, Command: strings.Repeat("x", 300)}
	got := approvalBody(long)
	require.LessOrEqual(t, len(got), notifyBodyMax+len("…"))
	require.True(t, strings.HasSuffix(got, "…"))
	require.Equal(t, "edit a.go", approvalBody(wire.Approval{Kind: wire.ApprovalEdit, Path: "a.go"}))
	require.Equal(t, "something", approvalBody(wire.Approval{Kind: wire.ApprovalOther, Description: "something"}))
}

func busyEvent(sid string, busy bool) Event {
	return Event{Name: wire.EvBusy, SessionID: sid, Payload: map[string]any{"session_id": sid, "busy": busy}}
}

func TestLongRunFinishingSendsAPush(t *testing.T) {
	old := doneAfter
	doneAfter = 30 * time.Millisecond
	t.Cleanup(func() { doneAfter = old })

	r, rs := pushRig(t)
	r.be.sessions = []wire.Session{{ID: "s1", Title: "Fix the login bug"}}
	c, _ := r.dial(r.token(), nil)
	okRes(t, c.call(wire.MPushSubscribe, subscribeReq(fcm, false, true)))

	r.be.events <- busyEvent("s1", true)
	time.Sleep(60 * time.Millisecond)
	r.be.events <- busyEvent("s1", false)
	eventually(t, func() bool { return len(rs.all()) == 1 })
	m := rs.all()[0].Message
	require.Equal(t, "Finished", m.Title)
	require.Equal(t, "Fix the login bug", m.Body)
	require.Equal(t, "s1", m.SessionID)
}

func TestQuickRunFinishingSendsNothing(t *testing.T) {
	r, rs := pushRig(t) // doneAfter is 15s
	c, _ := r.dial(r.token(), nil)
	okRes(t, c.call(wire.MPushSubscribe, subscribeReq(fcm, true, true)))
	r.be.events <- busyEvent("s1", true)
	r.be.events <- busyEvent("s1", false)
	r.be.events <- Event{Name: wire.EvNotice, Payload: map[string]string{}}
	c.nextEvent(wire.EvNotice)
	time.Sleep(50 * time.Millisecond)
	require.Empty(t, rs.all())
}

func TestRevokingADeviceDropsItsSubscription(t *testing.T) {
	r, rs := pushRig(t)
	c, w := r.dial(r.token(), nil)
	okRes(t, c.call(wire.MPushSubscribe, subscribeReq(fcm, true, true)))
	r.srv.kickDevice(w.Device.ID)
	_, ok := r.srv.push.Get(w.Device.ID)
	require.False(t, ok)
	r.be.events <- Event{Name: wire.EvApprovalRequested, Payload: wire.Approval{ID: "p1"}}
	time.Sleep(50 * time.Millisecond)
	require.Empty(t, rs.all(), "a revoked phone gets nothing")
}
