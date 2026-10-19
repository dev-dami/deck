package daemon

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/pkg/remote/devstore"
	"github.com/dev-dami/deck/pkg/remote/e2e"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

type fakeRelay struct {
	mu      sync.Mutex
	opened  []string
	closed  int
	devices int
}

func (f *fakeRelay) PairOpen(h string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened = append(f.opened, h)
	return nil
}

func (f *fakeRelay) PairClose() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
	return nil
}

func (f *fakeRelay) SendDevices() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.devices++
	return nil
}

func (f *fakeRelay) counts() (opened, closed, devices int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.opened), f.closed, f.devices
}

// pairRig is a daemon with no paired phones yet.
func pairRig(t *testing.T) (*rig, *fakeRelay, *[]devstore.Device) {
	t.Helper()
	r := newRig(t)
	if err := r.devs.Revoke(r.device.ID); err != nil {
		t.Fatal(err)
	}
	relay := &fakeRelay{}
	var paired []devstore.Device
	var mu sync.Mutex
	r.d.cfg.Relay = relay
	r.d.cfg.OnPaired = func(d devstore.Device) {
		mu.Lock()
		paired = append(paired, d)
		mu.Unlock()
	}
	return r, relay, &paired
}

// pairAs runs the phone side of a pairing with the given secret. confirm says
// whether the phone sends its confirming hello.
func pairAs(t *testing.T, r *rig, psk []byte, name string, confirm bool) (wire.PairWelcome, error) {
	t.Helper()
	phone, _ := e2e.GenerateKeypair(rand.Reader)
	pc, dc := pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.d.ServePair(ctx, dc) }()

	payload, _ := json.Marshal(wire.PairHello{DeviceName: name})
	msg1, pend, err := e2e.Initiator{Static: phone, PeerStatic: r.ident.StaticPublic(), CID: r.ident.CID(), PSK: psk}.Start(payload)
	if err != nil {
		t.Fatal(err)
	}
	_ = pc.Write(ctx, msg1)
	msg2, err := pc.Read(ctx)
	if err != nil {
		<-done
		return wire.PairWelcome{}, err
	}
	reply, sess, err := pend.Finish(msg2)
	if err != nil {
		pc.Close(0, "")
		<-done
		return wire.PairWelcome{}, err
	}
	var welcome wire.PairWelcome
	if err := json.Unmarshal(reply, &welcome); err != nil {
		t.Fatal(err)
	}
	if confirm {
		raw, _ := json.Marshal(wire.Frame3{T: wire.FrameHello, Hello3: &wire.Hello3{V: wire.ProtocolV3}})
		msgs, _ := sess.Seal(raw)
		for _, m := range msgs {
			_ = pc.Write(ctx, m)
		}
	} else {
		pc.Close(0, "")
	}
	err = <-done
	return welcome, err
}

func pskOf(t *testing.T, inv PairInvite) []byte {
	t.Helper()
	psk, err := b64url.DecodeString(inv.Blob.PSK)
	if err != nil {
		t.Fatal(err)
	}
	return psk
}

func TestPairingHappyPath(t *testing.T) {
	r, relay, paired := pairRig(t)
	inv, err := r.d.ArmPair("https://app.example.dev", []string{"192.168.1.20:7879"}, "ab:cd")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(inv.URL, "https://app.example.dev/#p=") {
		t.Fatalf("url %q", inv.URL)
	}
	var blob wire.QRBlob
	raw, err := b64url.DecodeString(strings.TrimPrefix(inv.URL, "https://app.example.dev/#p="))
	if err != nil || json.Unmarshal(raw, &blob) != nil || blob.CID != r.ident.CID() || blob.S != b64url.EncodeToString(r.ident.StaticPublic()) {
		t.Fatalf("blob %+v %v", blob, err)
	}
	if want := inv.ExpiresAt.Sub(r.d.cfg.Now()); want > wire.PairCodeTTL || want < wire.PairCodeTTL-time.Minute {
		t.Fatalf("ttl %v", want)
	}
	if o, _, _ := relay.counts(); o != 1 {
		t.Fatal("the relay's pairing window was not opened")
	}
	id, _ := PairIDOf(pskOf(t, inv))
	if relay.opened[0] != PairIDHash(id) {
		t.Fatal("relay told a different pair id hash")
	}

	w, err := pairAs(t, r, pskOf(t, inv), "Pixel 9", true)
	if err != nil {
		t.Fatalf("pairing: %v", err)
	}
	if w.DeviceID == "" || w.RelayToken == "" || w.Name != "test-pc" {
		t.Fatalf("welcome %+v", w)
	}
	dev, err := r.devs.Get(w.DeviceID)
	if err != nil || dev.Name != "Pixel 9" || dev.RelayTokenHash != devstore.HashToken(w.RelayToken) || dev.AllowYolo {
		t.Fatalf("stored %+v %v", dev, err)
	}
	if len(*paired) != 1 || (*paired)[0].ID != w.DeviceID {
		t.Fatal("the pairing was not announced")
	}
	if _, closed, devices := relay.counts(); closed != 1 || devices != 1 {
		t.Fatalf("relay closed=%d devices=%d", closed, devices)
	}
	// The secret is spent.
	if _, err := pairAs(t, r, pskOf(t, inv), "Second phone", true); err == nil {
		t.Fatal("a spent pairing secret worked twice")
	}
}

func TestWrongSecretLeavesNoDevice(t *testing.T) {
	r, _, paired := pairRig(t)
	if _, err := r.d.ArmPair("", nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := pairAs(t, r, make([]byte, 32), "Intruder", false); err == nil {
		t.Fatal("a phone without the secret paired")
	}
	if len(r.devs.List()) != 0 || len(*paired) != 0 {
		t.Fatal("a failed pairing left a device behind")
	}
}

func TestPendingDeviceCannotConnectBeforeConfirm(t *testing.T) {
	r, _, _ := pairRig(t)
	if _, err := r.d.ArmPair("", nil, ""); err != nil {
		t.Fatal(err)
	}
	// A phone that finishes the handshake but never confirms stays pending, so
	// a normal connection with the same key is refused meanwhile.
	phone, _ := e2e.GenerateKeypair(rand.Reader)
	psk := r.d.takeSlot()
	pc, dc := pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.d.ServePair(ctx, dc) }()
	payload, _ := json.Marshal(wire.PairHello{DeviceName: "Pixel"})
	msg1, pend, _ := e2e.Initiator{Static: phone, PeerStatic: r.ident.StaticPublic(), CID: r.ident.CID(), PSK: psk}.Start(payload)
	_ = pc.Write(ctx, msg1)
	msg2, err := pc.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := pend.Finish(msg2); err != nil {
		t.Fatal(err)
	}
	if _, err := r.devs.ByStatic(phone.Public); err == nil {
		t.Fatal("a device that has not confirmed was accepted")
	}
	cl, _ := r.connectAs(phone, nil)
	select {
	case <-cl.done:
	case <-time.After(3 * time.Second):
		t.Fatal("an unconfirmed device kept a normal connection")
	}
	pc.Close(0, "")
	<-done
}

func TestThreeFailuresBurnTheSlot(t *testing.T) {
	r, relay, _ := pairRig(t)
	inv, _ := r.d.ArmPair("", nil, "")
	good := pskOf(t, inv)
	for i := 0; i < wire.PairFailuresToBurn; i++ {
		if _, err := pairAs(t, r, make([]byte, 32), "Intruder", false); err == nil {
			t.Fatal("wrong secret paired")
		}
	}
	if _, closed, _ := relay.counts(); closed != 1 {
		t.Fatalf("the relay window was not closed on burn (closed=%d)", closed)
	}
	if _, err := pairAs(t, r, good, "Owner", true); err == nil {
		t.Fatal("the burned slot still accepted the right secret")
	}
	if len(r.devs.List()) != 0 {
		t.Fatal("a device was paired from a burned slot")
	}
}

func TestExpiredAndReplacedSlots(t *testing.T) {
	r, _, _ := pairRig(t)
	first, _ := r.d.ArmPair("", nil, "")
	second, _ := r.d.ArmPair("", nil, "")
	if _, err := pairAs(t, r, pskOf(t, first), "Old QR", true); err == nil {
		t.Fatal("a replaced pairing code still worked")
	}

	now := time.Now()
	r.d.cfg.Now = func() time.Time { return now.Add(wire.PairCodeTTL + time.Second) }
	if _, err := pairAs(t, r, pskOf(t, second), "Late", true); err == nil {
		t.Fatal("an expired pairing code worked")
	}
}

func TestNoSlotRefusesPairing(t *testing.T) {
	r, _, _ := pairRig(t)
	if _, err := pairAs(t, r, make([]byte, 32), "Nobody", true); err == nil {
		t.Fatal("paired with no slot armed")
	}
}

func TestBadDeviceNamesAreRefused(t *testing.T) {
	r, _, _ := pairRig(t)
	inv, _ := r.d.ArmPair("", nil, "")
	if _, err := pairAs(t, r, pskOf(t, inv), strings.Repeat("x", 65), true); err == nil {
		t.Fatal("a 65-character device name was accepted")
	}
	if _, err := pairAs(t, r, pskOf(t, inv), "", true); err == nil {
		t.Fatal("an empty device name was accepted")
	}
}

func TestPairIDIsStable(t *testing.T) {
	psk := make([]byte, 32)
	a, err := PairIDOf(psk)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := PairIDOf(psk)
	other, _ := PairIDOf(append(make([]byte, 31), 1))
	if a != b || a == other || len(a) != 43 {
		t.Fatalf("pair ids: %q %q %q", a, b, other)
	}
	if len(PairIDHash(a)) != 64 {
		t.Fatal("hash is not hex sha256")
	}
}

var updatePairVector = flag.Bool("update-pair", false, "rewrite ../wire/testdata/pair_id.json")

// TestPairIDVector pins the pair id derivation the phone must reproduce:
// base64url(HKDF-SHA256(ikm = psk, salt = none, info = "deck-pair-id", 32 bytes))
// and the relay's hash of it (lowercase hex SHA-256 of the pair id string).
func TestPairIDVector(t *testing.T) {
	type vcase struct {
		PSKHex     string `json:"psk_hex"`
		PairID     string `json:"pair_id"`
		PairIDHash string `json:"pair_id_hash"`
	}
	var cases []vcase
	for _, psk := range [][]byte{
		make([]byte, 32),
		bytes.Repeat([]byte{0xab}, 32),
		func() []byte {
			b := make([]byte, 32)
			for i := range b {
				b[i] = byte(i)
			}
			return b
		}(),
	} {
		id, err := PairIDOf(psk)
		if err != nil {
			t.Fatal(err)
		}
		cases = append(cases, vcase{hex.EncodeToString(psk), id, PairIDHash(id)})
	}
	raw, err := json.MarshalIndent(map[string]any{
		"note":  "pair_id = base64url(HKDF-SHA256(ikm=psk, salt=none, info=\"deck-pair-id\", L=32)); pair_id_hash = hex(SHA-256(pair_id as UTF-8)).",
		"cases": cases,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	path := filepath.Join("..", "wire", "testdata", "pair_id.json")
	if *updatePairVector {
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing vector (run with -update-pair): %v", err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), raw) {
		t.Fatal("pair_id.json differs from the live derivation; run with -update-pair if the change is intended")
	}
}
