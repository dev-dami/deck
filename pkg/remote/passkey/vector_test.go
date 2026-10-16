package passkey

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite ../wire/testdata/assert_challenge.json")

type challengeCase struct {
	CID          string `json:"cid"`
	DeviceID     string `json:"device_id"`
	ActionDigest string `json:"action_digest"`
	Nonce        string `json:"nonce"`
	ChallengeHex string `json:"challenge_hex"`
	ChallengeB64 string `json:"challenge_b64url"`
}

type challengeVector struct {
	Note  string          `json:"note"`
	Cases []challengeCase `json:"cases"`
}

// TestChallengeVector pins the assertion challenge the phone must pass to
// navigator.credentials.get: SHA-256 over "deck-assert-v1" and then, for each
// of cid, device id, action digest, nonce, a zero byte followed by the UTF-8
// string.
func TestChallengeVector(t *testing.T) {
	digest := "9f2c1a7e5b3d48c0a6e1f7d2b9c4a835e0d1f6b7c2a94e8d3b5f0a1c7e6d2b48"
	inputs := [][4]string{
		{"Zk3p0Jq8vYb1nT5cR2sWxA", "dev_7a1f", digest, "n_5c1d8e2f6a10b7"},
		{"Zk3p0Jq8vYb1nT5cR2sWxA", "dev_7a1f", digest, "0192f3a0-7b1c-7d2e-9a44-5c1d8e2f6a11"},
		{"c", "d", "x", ""},
		{"Zk3p0Jq8vYb1nT5cR2sWxA", "dev_é", digest, "n"},
	}
	v := challengeVector{Note: "challenge = SHA-256(\"deck-assert-v1\" || 0x00 cid || 0x00 device_id || 0x00 action_digest || 0x00 nonce); a queued action uses its qid as the nonce."}
	for _, in := range inputs {
		ch := Challenge(in[0], in[1], in[2], in[3])
		v.Cases = append(v.Cases, challengeCase{in[0], in[1], in[2], in[3], hex.EncodeToString(ch), b64.EncodeToString(ch)})
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	path := filepath.Join("..", "wire", "testdata", "assert_challenge.json")
	if *update {
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing vector (run with -update): %v", err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), raw) {
		t.Fatal("assert_challenge.json differs from the live derivation; run with -update if the change is intended")
	}
}
