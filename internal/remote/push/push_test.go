package push

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type sent struct {
	Sub     Subscription
	Payload Message
	Urgent  bool
}

type fakeSender struct {
	mu     sync.Mutex
	got    []sent
	status int
	err    error
}

func (f *fakeSender) Send(_ context.Context, sub Subscription, payload []byte, urgent bool) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var m Message
	_ = json.Unmarshal(payload, &m)
	f.got = append(f.got, sent{sub, m, urgent})
	if f.err != nil {
		return 0, f.err
	}
	if f.status == 0 {
		return http.StatusCreated, nil
	}
	return f.status, nil
}

func (f *fakeSender) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.got) }

const goodEndpoint = "https://fcm.googleapis.com/fcm/send/abc"

func sub(endpoint string) Subscription {
	return Subscription{Endpoint: endpoint, P256dh: "k", Auth: "a", Approvals: true, Done: true}
}

func newSvc(t *testing.T) (*Service, *fakeSender, string) {
	t.Helper()
	dir := t.TempDir()
	f := &fakeSender{}
	s, err := New(dir, f)
	require.NoError(t, err)
	return s, f, dir
}

func TestValidEndpoint(t *testing.T) {
	for _, ok := range []string{
		"https://fcm.googleapis.com/fcm/send/x",
		"https://updates.push.services.mozilla.com/wpush/v2/x",
		"https://web.push.apple.com/x",
		"https://wns2-par02p.notify.windows.com/?token=x",
	} {
		require.NoError(t, ValidEndpoint(ok), ok)
	}
	for _, bad := range []string{
		"http://fcm.googleapis.com/x",
		"https://evil.example/x",
		"https://fcm.googleapis.com.evil.example/x",
		"https://notfcm.googleapis.com.attacker.io/x",
		"https://127.0.0.1/x",
		"https://169.254.169.254/latest/meta-data",
		"https://user:pw@fcm.googleapis.com/x",
		"ftp://fcm.googleapis.com/x",
		"",
		"::not a url",
	} {
		require.Error(t, ValidEndpoint(bad), bad)
	}
}

func TestKeysAreCreatedOnceAndPersist(t *testing.T) {
	s, _, dir := newSvc(t)
	pub := s.PublicKey()
	require.NotEmpty(t, pub)
	again, err := New(dir, &fakeSender{})
	require.NoError(t, err)
	require.Equal(t, pub, again.PublicKey(), "the key survives a restart, or every phone would have to resubscribe")
}

func TestFilesAreOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	s, _, dir := newSvc(t)
	require.NoError(t, s.Subscribe("d1", sub(goodEndpoint)))
	for _, f := range []string{"vapid.json", "push.json"} {
		fi, err := os.Stat(filepath.Join(dir, f))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), f)
	}
}

func TestSubscribeValidates(t *testing.T) {
	s, _, _ := newSvc(t)
	require.Error(t, s.Subscribe("d", sub("https://evil.example/x")))
	bad := sub(goodEndpoint)
	bad.Auth = ""
	require.ErrorContains(t, s.Subscribe("d", bad), "keys")
	_, ok := s.Get("d")
	require.False(t, ok)
}

func TestSubscriptionsPersist(t *testing.T) {
	s, _, dir := newSvc(t)
	require.NoError(t, s.Subscribe("d1", sub(goodEndpoint)))
	again, err := New(dir, &fakeSender{})
	require.NoError(t, err)
	got, ok := again.Get("d1")
	require.True(t, ok)
	require.Equal(t, goodEndpoint, got.Endpoint)
}

func TestApprovalGoesToPhonesThatWantIt(t *testing.T) {
	s, f, _ := newSvc(t)
	a := sub(goodEndpoint)
	b := sub("https://fcm.googleapis.com/fcm/send/b")
	b.Approvals = false
	require.NoError(t, s.Subscribe("a", a))
	require.NoError(t, s.Subscribe("b", b))

	s.Approval(Message{Title: "Approval needed", Tag: "p1", Approval: "p1"})
	require.Equal(t, 1, f.count())
	require.Equal(t, goodEndpoint, f.got[0].Sub.Endpoint)
	require.True(t, f.got[0].Urgent, "approvals wake the phone")
}

func TestDoneGoesToPhonesThatWantIt(t *testing.T) {
	s, f, _ := newSvc(t)
	a := sub(goodEndpoint)
	a.Done = false
	require.NoError(t, s.Subscribe("a", a))
	s.Done(Message{Title: "Finished"})
	require.Zero(t, f.count())

	require.NoError(t, s.SetPrefs("a", true, true))
	s.Done(Message{Title: "Finished"})
	require.Equal(t, 1, f.count())
	require.False(t, f.got[0].Urgent)
}

func TestGoneSubscriptionIsDropped(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusGone} {
		s, f, dir := newSvc(t)
		f.status = status
		require.NoError(t, s.Subscribe("a", sub(goodEndpoint)))
		s.Approval(Message{Title: "x"})
		_, ok := s.Get("a")
		require.False(t, ok, "%d means the phone is gone", status)
		again, _ := New(dir, &fakeSender{})
		_, ok = again.Get("a")
		require.False(t, ok, "and the removal is saved")
	}
}

func TestServerErrorKeepsTheSubscription(t *testing.T) {
	s, f, _ := newSvc(t)
	f.status = http.StatusServiceUnavailable
	require.NoError(t, s.Subscribe("a", sub(goodEndpoint)))
	s.Approval(Message{Title: "x"})
	_, ok := s.Get("a")
	require.True(t, ok, "a push service outage must not unsubscribe anyone")
}

func TestTest(t *testing.T) {
	s, f, _ := newSvc(t)
	require.ErrorContains(t, s.Test(t.Context(), "nobody", Message{}), "not subscribed")

	quiet := sub(goodEndpoint)
	quiet.Approvals, quiet.Done = false, false
	require.NoError(t, s.Subscribe("a", quiet))
	require.NoError(t, s.Test(t.Context(), "a", Message{Title: "hi"}))
	require.Equal(t, 1, f.count(), "a test ignores the preferences")

	f.status = http.StatusGone
	require.ErrorContains(t, s.Test(t.Context(), "a", Message{}), "enable notifications again")
	_, ok := s.Get("a")
	require.False(t, ok)

	f.status = http.StatusBadGateway
	require.NoError(t, s.Subscribe("a", quiet))
	require.ErrorContains(t, s.Test(t.Context(), "a", Message{}), "502")
}

func TestUnsubscribeUnknownIsFine(t *testing.T) {
	s, _, _ := newSvc(t)
	require.NoError(t, s.Unsubscribe("nobody"))
	s.RemoveDevice("nobody")
}

func TestSetPrefsNeedsASubscription(t *testing.T) {
	s, _, _ := newSvc(t)
	require.Error(t, s.SetPrefs("nobody", true, true))
}

func TestSubscriptionCap(t *testing.T) {
	s, _, _ := newSvc(t)
	for i := range maxSubs {
		require.NoError(t, s.Subscribe(string(rune('A'+i%26))+string(rune('a'+i/26)), sub(goodEndpoint)))
	}
	require.ErrorContains(t, s.Subscribe("overflow", sub(goodEndpoint)), "too many")
	require.NoError(t, s.Subscribe("Aa", sub(goodEndpoint)), "an existing device can still resubscribe")
}

func TestCorruptSubscriptionFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "push.json"), []byte("{nope"), 0o600))
	_, err := New(dir, &fakeSender{})
	require.Error(t, err)
}
