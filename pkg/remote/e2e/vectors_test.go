package e2e

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite ../wire/testdata/noise_frames.json")

type framesVector struct {
	Note   string        `json:"note"`
	Frames []frameVector `json:"frames"`
	// Rekey: after this many messages in one direction the sender rekeys.
	RekeyEvery int `json:"rekey_every"`
}

type frameVector struct {
	From         string   `json:"from"`
	Length       int      `json:"plaintext_length"`
	PlaintextSHA string   `json:"plaintext_sha256_hex"`
	ChunkLengths []int    `json:"chunk_ciphertext_lengths"`
	ChunkSHAs    []string `json:"chunk_ciphertext_sha256_hex"`
}

func fixedSeed(b byte) *bytes.Reader {
	s := make([]byte, 32)
	for i := range s {
		s[i] = b + byte(i)
	}
	return bytes.NewReader(s)
}

// TestFramesVector pins the chunk framing (flag byte, 65535-byte messages)
// that the phone's Noise must reproduce. The session keys come from the
// IK vector's seeds, so it follows noise_ik.json.
func TestFramesVector(t *testing.T) {
	ps, _ := GenerateKeypair(fixedSeed(0x10))
	cs, _ := GenerateKeypair(fixedSeed(0x40))
	msg1, pend, err := Initiator{Static: ps, PeerStatic: cs.Public, CID: cid, Random: fixedSeed(0x70)}.Start([]byte(`{"t":"hello","v":3}`))
	if err != nil {
		t.Fatal(err)
	}
	msg2, computer, err := Responder{Static: cs, CID: cid, Random: fixedSeed(0xa0)}.Accept(msg1, func(_, _ []byte) ([]byte, error) { return []byte(`{"t":"welcome"}`), nil })
	if err != nil {
		t.Fatal(err)
	}
	_, phone, err := pend.Finish(msg2)
	if err != nil {
		t.Fatal(err)
	}

	big := make([]byte, 140000)
	for i := range big {
		big[i] = byte(i * 7)
	}
	var v framesVector
	v.Note = "Each frame is split into chunks of at most 65518 plaintext bytes, each prefixed with a flag byte (0 more, 1 last), then Noise-encrypted; keys follow noise_ik.json."
	v.RekeyEvery = 10000
	for _, c := range []struct {
		from  string
		s, r  *Session
		frame []byte
	}{
		{"initiator", phone, computer, []byte(`{"t":"req","id":"1","m":"inbox.get"}`)},
		{"initiator", phone, computer, nil},
		{"initiator", phone, computer, big},
		{"responder", computer, phone, []byte(`{"t":"res","id":"1","ok":true,"r":[]}`)},
	} {
		msgs, err := c.s.Seal(c.frame)
		if err != nil {
			t.Fatal(err)
		}
		fv := frameVector{From: c.from, Length: len(c.frame), PlaintextSHA: sum(c.frame)}
		var got []byte
		for _, m := range msgs {
			fv.ChunkLengths = append(fv.ChunkLengths, len(m))
			fv.ChunkSHAs = append(fv.ChunkSHAs, sum(m))
			if got, err = c.r.Open(m); err != nil {
				t.Fatal(err)
			}
		}
		if !bytes.Equal(got, c.frame) {
			t.Fatal("frame did not round trip")
		}
		v.Frames = append(v.Frames, fv)
	}

	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	path := filepath.Join("..", "wire", "testdata", "noise_frames.json")
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
		t.Fatal("noise_frames.json differs from the live framing; run with -update if the change is intended")
	}
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
