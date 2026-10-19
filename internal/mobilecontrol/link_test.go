package mobilecontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/pkg/remote/devstore"
	"github.com/dev-dami/deck/pkg/remote/mobile"
)

func TestLinkPrefersSavedRelayAndFallsBackOnlyWhenUnset(t *testing.T) {
	for _, tc := range []struct {
		name, relay         string
		initialLAN, wantLAN bool
	}{{"fresh", "", false, true}, {"hosted", "https://relay.test", true, false}, {"lan", "http://192.168.1.10:8787", false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			got := linkOptions(Options{Action: "link", LAN: tc.initialLAN}, mobile.Config{RelayURL: tc.relay})
			if got.LAN != tc.wantLAN {
				t.Fatalf("unexpected LAN choice: %+v", got)
			}
		})
	}
	dir := t.TempDir()
	_, err := Run(context.Background(), Options{Dir: dir, Action: "link", LANIP: "invalid"})
	if err == nil || !strings.Contains(err.Error(), "--lan-ip") {
		t.Fatalf("fresh link must attempt LAN startup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "mobile.json")); !os.IsNotExist(err) {
		t.Fatal("failed startup wrote settings")
	}
}

func TestRepeatedLinkRotatesBusyPairingWithoutChangingHostedSettings(t *testing.T) {
	dir := t.TempDir()
	cfg := mobile.Config{Name: "PC", RelayURL: "https://relay.test"}
	if err := mobile.SaveConfig(dir, cfg); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	busy := true
	generation := 0
	fakeControl(t, dir, mobile.StatusResult{}, func(_ context.Context, method string, _ json.RawMessage) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		switch method {
		case "status":
			return mobile.StatusResult{Name: "PC", RelayURL: cfg.RelayURL, RelayOnline: true}, nil
		case "unpair":
			busy = false
			return nil, nil
		case "pair":
			if busy {
				return nil, errors.New("pairing is already armed")
			}
			busy = true
			generation++
			return mobile.PairResult{URL: cfg.RelayURL + "/#secret-" + strings.Repeat("x", generation), ExpiresAt: time.Now().Add(time.Minute)}, nil
		}
		return nil, errors.New("unexpected method")
	})
	first, err := Run(context.Background(), Options{Dir: dir, Action: "link", LAN: true})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Run(context.Background(), Options{Dir: dir, Action: "link"})
	if err != nil {
		t.Fatal(err)
	}
	if first.PairURL == second.PairURL || !second.Online {
		t.Fatal("link did not rotate the busy QR")
	}
	actual, _ := mobile.LoadConfig(dir)
	if !sameConfig(cfg, actual) {
		t.Fatal("link changed saved HTTPS relay")
	}
}

func TestStatusAndRevokeListOnlyPublicPhoneFieldsOffline(t *testing.T) {
	dir := t.TempDir()
	cfg := mobile.Config{Name: "PC", RelayURL: "https://relay.test"}
	if err := mobile.SaveConfig(dir, cfg); err != nil {
		t.Fatal(err)
	}
	store, err := devstore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, token, err := store.Add("Pixel", bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := store.Add("Other", bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "mobile.json"))
	r, err := Run(context.Background(), Options{Dir: dir, Action: "status", Project: t.TempDir(), LAN: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.DeviceList) != 2 || r.Devices != 2 || r.Online || r.PairURL != "" {
		t.Fatalf("unexpected offline status: %+v", r)
	}
	raw, _ := json.Marshal(r)
	for _, secret := range []string{token, a.RelayTokenHash, "static", "expires_at", "passkeys"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("private device field leaked: %s", secret)
		}
	}
	if _, err := Run(context.Background(), Options{Dir: dir, Action: "revoke", DeviceID: "unknown"}); err == nil {
		t.Fatal("unknown revoke succeeded")
	}
	if _, err := Run(context.Background(), Options{Dir: dir, Action: "revoke"}); err == nil {
		t.Fatal("empty revoke succeeded")
	}
	r, err = Run(context.Background(), Options{Dir: dir, Action: "revoke", DeviceID: a.ID})
	if err != nil {
		t.Fatal(err)
	}
	if r.Devices != 1 || len(r.DeviceList) != 1 || r.DeviceList[0].ID != b.ID {
		t.Fatalf("wrong phone revoked: %+v", r)
	}
	latest, err := devstore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := latest.Get(a.ID); !errors.Is(err, devstore.ErrNotFound) {
		t.Fatal("revoked phone remains")
	}
	if _, err := latest.Get(b.ID); err != nil {
		t.Fatal("other phone was revoked")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "mobile.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("status/revoke changed relay settings")
	}
	if _, err := os.Stat(filepath.Join(dir, "projects.json")); !os.IsNotExist(err) {
		t.Fatal("status/revoke shared a project")
	}
}
