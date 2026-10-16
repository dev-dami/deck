package identity

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateIsStable(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a.CID() != b.CID() || string(a.StaticPublic()) != string(b.StaticPublic()) {
		t.Fatal("a reloaded identity differs from the one created")
	}
	if len(a.CID()) != cidLen {
		t.Fatalf("cid %q has length %d, want %d", a.CID(), len(a.CID()), cidLen)
	}
	other, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if other.CID() == a.CID() {
		t.Fatal("two computers share a cid")
	}
}

func TestSignatureVerifies(t *testing.T) {
	id, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("deck-relay-v1|" + id.CID() + "|nonce")
	if !ed25519.Verify(id.RelayPublic(), msg, id.Sign(msg)) {
		t.Fatal("signature does not verify")
	}
	if ed25519.Verify(id.RelayPublic(), append(msg, 'x'), id.Sign(msg)) {
		t.Fatal("signature verifies for another message")
	}
	if CIDOf(id.RelayPublic()) != id.CID() {
		t.Fatal("CIDOf disagrees with CID")
	}
}

func TestCorruptFileIsAnErrorNotARegeneration(t *testing.T) {
	for name, content := range map[string]string{
		"not json":   "{",
		"empty keys": `{"relay_key":"","static_key":""}`,
		"short seed": `{"relay_key":"AAAA","static_key":"AAAA"}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, fileName)
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadOrCreate(dir); err == nil {
				t.Fatal("a corrupt identity was accepted")
			}
			// A silent regeneration would orphan every paired phone.
			got, _ := os.ReadFile(path)
			if string(got) != content {
				t.Fatal("the corrupt file was overwritten")
			}
		})
	}
}
