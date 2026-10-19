package relayclient

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/dev-dami/deck/pkg/remote/identity"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

var updateAuthVector = flag.Bool("update-auth", false, "rewrite ../wire/testdata/hostauth_ed25519.json")

// TestHostAuthVector pins the encodings the relay must accept for a host:
// the raw 32-byte public key as relay_pub, the cid derived from those raw
// bytes, and the signature over "deck-relay-v1|" + cid + "|" + nonce, all in
// unpadded base64url. Ed25519 signatures are deterministic, so the vector is
// stable and the Worker's tests can verify against it.
func TestHostAuthVector(t *testing.T) {
	b64 := base64.RawURLEncoding
	type vcase struct {
		SeedHex string `json:"seed_hex"`
		Pub     string `json:"relay_pub"`
		CID     string `json:"cid"`
		Nonce   string `json:"nonce"`
		Message string `json:"message"`
		Sig     string `json:"sig"`
	}
	var cases []vcase
	for i, nonce := range []string{"Fq8Yb2nT5cR0sWxAZk3p0J", "n0nce-with_url-safe~chars"} {
		seed := bytes.Repeat([]byte{byte(i + 1)}, ed25519.SeedSize)
		priv := ed25519.NewKeyFromSeed(seed)
		pub := priv.Public().(ed25519.PublicKey)
		cid := identity.CIDOf(pub)
		msg := wire.RelayChallengeContext + cid + "|" + nonce
		cases = append(cases, vcase{
			SeedHex: bytesToHex(seed), Pub: b64.EncodeToString(pub), CID: cid, Nonce: nonce,
			Message: msg, Sig: b64.EncodeToString(ed25519.Sign(priv, []byte(msg))),
		})
	}
	raw, err := json.MarshalIndent(map[string]any{
		"note":  "relay_pub = unpadded base64url of the raw 32-byte Ed25519 public key; cid = first 22 chars of unpadded base64url(SHA-256(raw key)); sig = unpadded base64url of the Ed25519 signature over message (UTF-8).",
		"cases": cases,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	path := filepath.Join("..", "wire", "testdata", "hostauth_ed25519.json")
	if *updateAuthVector {
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing vector (run with -update-auth): %v", err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), raw) {
		t.Fatal("hostauth_ed25519.json differs from the live derivation; run with -update-auth if the change is intended")
	}
}

func bytesToHex(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 2*len(b))
	for _, x := range b {
		out = append(out, digits[x>>4], digits[x&0xf])
	}
	return string(out)
}
