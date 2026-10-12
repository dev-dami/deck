package peer

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestIdentityCreatedOnceAndPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "peers")
	if _, err := LoadIdentity(dir); !errors.Is(err, ErrNoIdentity) {
		t.Fatalf("LoadIdentity on empty dir = %v, want ErrNoIdentity", err)
	}
	id, err := LoadOrCreateIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(id.Fingerprint(), "SHA256:") || id.PublicKey().Type() != ssh.KeyAlgoED25519 {
		t.Fatalf("bad identity %s %s", id.Fingerprint(), id.PublicKey().Type())
	}
	again, err := LoadOrCreateIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.Fingerprint() != id.Fingerprint() {
		t.Fatal("identity changed on reload")
	}
	path := filepath.Join(dir, IdentityFile)
	// The owner can read it.
	if data, err := os.ReadFile(path); err != nil || len(data) == 0 {
		t.Fatalf("owner cannot read identity: %v", err)
	}
	if err := CheckPrivate(path); err != nil {
		t.Fatalf("identity not private: %v", err)
	}
	if err := CheckPrivate(dir); err != nil {
		t.Fatalf("peers dir not private: %v", err)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(path)
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("identity mode %o, want 600", fi.Mode().Perm())
		}
		di, _ := os.Stat(dir)
		if di.Mode().Perm() != 0o700 {
			t.Fatalf("dir mode %o, want 700", di.Mode().Perm())
		}
	}
	// No temp files are left behind.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temp file left: %s", e.Name())
		}
	}
}

func TestCheckPrivateFlagsSharedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// On Windows a file in the temp dir inherits its ACL, which also grants
	// SYSTEM and Administrators.
	if err := CheckPrivate(path); err == nil {
		t.Fatal("CheckPrivate accepted a file others can read")
	}
	if err := restrictPath(path, false); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivate(path); err != nil {
		t.Fatalf("after restrictPath: %v", err)
	}
}

func TestIdentityConcurrentCreateAgrees(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	fps := make([]string, 8)
	errs := make([]error, 8)
	for i := range fps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := LoadOrCreateIdentity(dir)
			errs[i] = err
			if err == nil {
				fps[i] = id.Fingerprint()
			}
		}()
	}
	wg.Wait()
	for i := range fps {
		if errs[i] != nil {
			t.Fatalf("worker %d: %v", i, errs[i])
		}
		if fps[i] != fps[0] {
			t.Fatalf("workers disagree: %s vs %s", fps[i], fps[0])
		}
	}
}

func TestIdentityRejectsBadFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, IdentityFile)
	for _, content := range []string{"", "garbage", "-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA\n-----END OPENSSH PRIVATE KEY-----\n"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadIdentity(dir); err == nil || errors.Is(err, ErrNoIdentity) {
			t.Fatalf("LoadIdentity(%q) = %v, want a parse error", content, err)
		}
		if _, err := LoadOrCreateIdentity(dir); err == nil {
			t.Fatalf("LoadOrCreateIdentity overwrote or accepted a bad key %q", content)
		}
	}
	// A non-ed25519 key is refused.
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	block, _ := ssh.MarshalPrivateKey(k, "")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIdentity(dir); err == nil || !strings.Contains(err.Error(), "want ed25519") {
		t.Fatalf("ecdsa identity: %v", err)
	}
}

func TestStorePinListRemove(t *testing.T) {
	dir := t.TempDir()
	s := OpenStore(dir)
	if peers, err := s.List(); err != nil || len(peers) != 0 {
		t.Fatalf("empty store: %v %v", peers, err)
	}
	k1, k2 := newKey(t), newKey(t)
	if err := s.Pin(NewPinned("vps", "vps.example.com:7722", "", k1)); err != nil {
		t.Fatal(err)
	}
	if err := s.Pin(NewPinned("pc", "", "contractor", k2)); err != nil {
		t.Fatal(err)
	}
	peers, err := s.List()
	if err != nil || len(peers) != 2 || peers[0].Name != "pc" || peers[1].Name != "vps" {
		t.Fatalf("List = %+v %v", peers, err)
	}
	if peers[1].Role != "observer" || peers[0].Role != "contractor" {
		t.Fatalf("roles: %+v", peers)
	}
	p, ok, err := s.ByKey(k1)
	if err != nil || !ok || p.Name != "vps" || p.Fingerprint != Fingerprint(k1) {
		t.Fatalf("ByKey = %+v %v %v", p, ok, err)
	}
	if _, ok, _ := s.ByKey(newKey(t)); ok {
		t.Fatal("ByKey found an unpinned key")
	}
	// The same key under another name is refused.
	if err := s.Pin(NewPinned("other", "", "", k1)); err == nil {
		t.Fatal("pinned one key under two names")
	}
	// Re-pinning a name replaces its key.
	k3 := newKey(t)
	if err := s.Pin(NewPinned("vps", "vps.example.com:7722", "", k3)); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.ByKey(k1); ok {
		t.Fatal("old key still pinned after replace")
	}
	for _, bad := range []Pinned{
		NewPinned("Bad Name", "", "", newKey(t)),
		NewPinned("ok", "no-port", "", newKey(t)),
		{Name: "nokey"},
	} {
		if err := s.Pin(bad); err == nil {
			t.Fatalf("Pin(%+v) accepted", bad)
		}
	}
	if ok, err := s.Remove("vps"); err != nil || !ok {
		t.Fatalf("Remove = %v %v", ok, err)
	}
	if ok, _ := s.Remove("vps"); ok {
		t.Fatal("removed twice")
	}
	if _, ok, _ := s.Get("vps"); ok {
		t.Fatal("still pinned after remove")
	}
	if err := CheckPrivate(s.Path()); err != nil {
		t.Fatalf("store not private: %v", err)
	}
}

func TestStoreFailsClosedOnDamage(t *testing.T) {
	k := newKey(t)
	good := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
	for name, content := range map[string]string{
		"not json":        "{",
		"bad key":         `{"peers":[{"name":"a","public_key":"ssh-ed25519 AAAA","fingerprint":"x"}]}`,
		"wrong fp":        `{"peers":[{"name":"a","public_key":"` + good + `","fingerprint":"SHA256:nope"}]}`,
		"bad name":        `{"peers":[{"name":"../x","public_key":"` + good + `","fingerprint":"` + Fingerprint(k) + `"}]}`,
		"duplicate names": `{"peers":[{"name":"a","public_key":"` + good + `","fingerprint":"` + Fingerprint(k) + `"},{"name":"a","public_key":"` + good + `","fingerprint":"` + Fingerprint(k) + `"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, StoreFile), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			s := OpenStore(dir)
			if _, err := s.List(); err == nil {
				t.Fatal("damaged store loaded")
			}
			if _, ok, err := s.ByKey(k); ok || err == nil {
				t.Fatal("damaged store let a key in")
			}
		})
	}
	// A non-ed25519 pinned key is refused too.
	ek, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pub, _ := ssh.NewPublicKey(&ek.PublicKey)
	dir := t.TempDir()
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	content := `{"peers":[{"name":"a","public_key":"` + line + `","fingerprint":"` + Fingerprint(pub) + `"}]}`
	_ = os.WriteFile(filepath.Join(dir, StoreFile), []byte(content), 0o600)
	if _, err := OpenStore(dir).List(); err == nil {
		t.Fatal("ecdsa pin loaded")
	}
}

func newKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return k
}
