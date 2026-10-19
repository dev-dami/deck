package wire

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/flynn/noise"
)

// noiseVector is one recorded handshake: every key is fixed so the phone's
// TypeScript Noise can replay it byte for byte. The ciphertexts are from
// github.com/flynn/noise, the library the computer uses.
type noiseVector struct {
	Protocol         string           `json:"protocol"`
	PrologueHex      string           `json:"prologue_hex"`
	PSKHex           string           `json:"psk_hex,omitempty"`
	InitiatorStatic  string           `json:"initiator_static_priv_hex"`
	ResponderStatic  string           `json:"responder_static_priv_hex"`
	InitiatorEph     string           `json:"initiator_ephemeral_priv_hex"`
	ResponderEph     string           `json:"responder_ephemeral_priv_hex,omitempty"`
	InitiatorStaticP string           `json:"initiator_static_pub_hex"`
	ResponderStaticP string           `json:"responder_static_pub_hex"`
	Messages         []noiseMessage   `json:"messages"`
	HandshakeHashHex string           `json:"handshake_hash_hex,omitempty"`
	Transport        []noiseTransport `json:"transport,omitempty"`
}

type noiseMessage struct {
	From          string `json:"from"`
	PayloadHex    string `json:"payload_hex"`
	CiphertextHex string `json:"ciphertext_hex"`
}

type noiseTransport struct {
	From          string `json:"from"`
	PlaintextHex  string `json:"plaintext_hex"`
	CiphertextHex string `json:"ciphertext_hex"`
}

// Fixed 32-byte seeds; the values only need to be distinct and non-trivial.
func seed(b byte) []byte {
	out := make([]byte, 32)
	for i := range out {
		out[i] = b + byte(i)
	}
	return out
}

func keypair(t *testing.T, priv []byte) noise.DHKey {
	t.Helper()
	kp, err := noise.DH25519.GenerateKeypair(bytes.NewReader(priv))
	if err != nil {
		t.Fatal(err)
	}
	return kp
}

func hx(b []byte) string { return hex.EncodeToString(b) }

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type noisePlan struct {
	file     string
	protocol string
	pattern  noise.HandshakePattern
	psk      bool
	// payloads, in message order.
	payloads [][]byte
}

var noisePlans = []noisePlan{
	{"noise_ikpsk2", "Noise_IKpsk2_25519_AESGCM_SHA256", noise.HandshakeIK, true,
		[][]byte{[]byte(`{"device_name":"Pixel 9"}`), []byte(`{"device_id":"dev_7a1f"}`)}},
	{"noise_ik", "Noise_IK_25519_AESGCM_SHA256", noise.HandshakeIK, false,
		[][]byte{[]byte(`{"t":"hello","v":3}`), []byte(`{"t":"welcome"}`)}},
	{"noise_k", "Noise_K_25519_AESGCM_SHA256", noise.HandshakeK, false,
		[][]byte{[]byte(`{"qid":"0192f3a0","kind":"chat.send"}`)}},
}

func (p noisePlan) config(t *testing.T, initiator bool, v noiseVector) noise.Config {
	cfg := noise.Config{
		CipherSuite: noise.NewCipherSuite(noise.DH25519, noise.CipherAESGCM, noise.HashSHA256),
		Pattern:     p.pattern,
		Initiator:   initiator,
		Prologue:    mustHex(t, v.PrologueHex),
	}
	is, rs := keypair(t, mustHex(t, v.InitiatorStatic)), keypair(t, mustHex(t, v.ResponderStatic))
	if initiator {
		cfg.StaticKeypair = is
		cfg.PeerStatic = rs.Public
		cfg.Random = bytes.NewReader(mustHex(t, v.InitiatorEph))
	} else {
		cfg.StaticKeypair = rs
		if p.pattern.Name == "K" {
			cfg.PeerStatic = is.Public
		}
		if v.ResponderEph != "" {
			cfg.Random = bytes.NewReader(mustHex(t, v.ResponderEph))
		}
	}
	if p.psk {
		cfg.PresharedKey = mustHex(t, v.PSKHex)
		cfg.PresharedKeyPlacement = 2
	}
	return cfg
}

// run performs the handshake with the vector's keys and returns the messages
// as written; with check set it also asserts they equal the vector's.
func (p noisePlan) run(t *testing.T, v noiseVector, check bool) noiseVector {
	t.Helper()
	ini, err := noise.NewHandshakeState(p.config(t, true, v))
	if err != nil {
		t.Fatal(err)
	}
	rsp, err := noise.NewHandshakeState(p.config(t, false, v))
	if err != nil {
		t.Fatal(err)
	}
	out := v
	out.Messages = nil
	out.Transport = nil
	var csI, csR [2]*noise.CipherState
	for i, payload := range p.payloads {
		w, r, from := ini, rsp, "initiator"
		if i%2 == 1 {
			w, r, from = rsp, ini, "responder"
		}
		msg, c0, c1, err := w.WriteMessage(nil, payload)
		if err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		got, d0, d1, err := r.ReadMessage(nil, msg)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("message %d payload mismatch", i)
		}
		if c0 != nil {
			if from == "initiator" {
				csI = [2]*noise.CipherState{c0, c1}
				csR = [2]*noise.CipherState{d0, d1}
			} else {
				csR = [2]*noise.CipherState{c0, c1}
				csI = [2]*noise.CipherState{d0, d1}
			}
		}
		out.Messages = append(out.Messages, noiseMessage{from, hx(payload), hx(msg)})
		if check {
			if want := v.Messages[i]; want.From != from || want.PayloadHex != hx(payload) || want.CiphertextHex != hx(msg) {
				t.Fatalf("message %d differs from the recorded vector", i)
			}
		}
	}
	if csI[0] == nil {
		return out
	}
	out.HandshakeHashHex = hx(ini.ChannelBinding())
	if !bytes.Equal(ini.ChannelBinding(), rsp.ChannelBinding()) {
		t.Fatal("channel bindings differ")
	}
	// Transport: two messages each way, nonces 0 and 1.
	for _, s := range []struct {
		from     string
		enc, dec *noise.CipherState
		pt       string
	}{
		{"initiator", csI[0], csR[0], "first from the phone"},
		{"initiator", csI[0], csR[0], "second from the phone"},
		{"responder", csR[1], csI[1], "first from the computer"},
	} {
		// One-way patterns (K) carry traffic from the initiator only.
		if s.enc == nil || s.dec == nil {
			continue
		}
		ct, err := s.enc.Encrypt(nil, nil, []byte(s.pt))
		if err != nil {
			t.Fatal(err)
		}
		if pt, err := s.dec.Decrypt(nil, nil, ct); err != nil || string(pt) != s.pt {
			t.Fatalf("transport round trip failed: %v", err)
		}
		out.Transport = append(out.Transport, noiseTransport{s.from, hx([]byte(s.pt)), hx(ct)})
	}
	if check {
		if len(out.Transport) != len(v.Transport) {
			t.Fatal("transport length differs")
		}
		for i := range out.Transport {
			if out.Transport[i] != v.Transport[i] {
				t.Errorf("transport %d differs from the recorded vector", i)
			}
		}
		if out.HandshakeHashHex != v.HandshakeHashHex {
			t.Error("handshake hash differs from the recorded vector")
		}
	}
	return out
}

func TestNoiseVectors(t *testing.T) {
	for _, p := range noisePlans {
		t.Run(p.file, func(t *testing.T) {
			path := filepath.Join("testdata", p.file+".json")
			base := noiseVector{
				Protocol:        p.protocol,
				PrologueHex:     hx([]byte("deck-v3|Zk3p0Jq8vYb1nT5cR2sWxA")),
				InitiatorStatic: hx(seed(0x10)),
				ResponderStatic: hx(seed(0x40)),
				InitiatorEph:    hx(seed(0x70)),
			}
			base.InitiatorStaticP = hx(keypair(t, seed(0x10)).Public)
			base.ResponderStaticP = hx(keypair(t, seed(0x40)).Public)
			if len(p.payloads) > 1 {
				base.ResponderEph = hx(seed(0xa0))
			}
			if p.psk {
				base.PSKHex = hx(seed(0xd0))
			}
			if *updateV3 {
				raw, err := json.Marshal(p.run(t, base, false))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, pretty(t, raw), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("missing vector (run with -update-v3): %v", err)
			}
			var v noiseVector
			if err := json.Unmarshal(raw, &v); err != nil {
				t.Fatal(err)
			}
			if v.Protocol != p.protocol {
				t.Fatalf("protocol %q, want %q", v.Protocol, p.protocol)
			}
			p.run(t, v, true)
		})
	}
}

// TestNoiseRejectsWrongPSK is the property pairing relies on: a phone with the
// wrong code cannot finish the handshake.
func TestNoiseRejectsWrongPSK(t *testing.T) {
	p := noisePlans[0]
	v := noiseVector{
		PrologueHex: hx([]byte("deck-v3|c")), InitiatorStatic: hx(seed(0x10)), ResponderStatic: hx(seed(0x40)),
		InitiatorEph: hx(seed(0x70)), ResponderEph: hx(seed(0xa0)), PSKHex: hx(seed(0xd0)),
	}
	ini, err := noise.NewHandshakeState(p.config(t, true, v))
	if err != nil {
		t.Fatal(err)
	}
	bad := v
	bad.PSKHex = hx(seed(0xd1))
	rsp, err := noise.NewHandshakeState(p.config(t, false, bad))
	if err != nil {
		t.Fatal(err)
	}
	m1, _, _, err := ini.WriteMessage(nil, []byte("hi"))
	if err != nil {
		t.Fatal(err)
	}
	// psk2 is mixed at the end of message 2, so message 1 still opens and the
	// initiator is the one that rejects message 2.
	if _, _, _, err := rsp.ReadMessage(nil, m1); err != nil {
		t.Fatal(err)
	}
	m2, _, _, err := rsp.WriteMessage(nil, []byte("ok"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ini.ReadMessage(nil, m2); err == nil {
		t.Fatal("handshake finished with the wrong psk")
	}
}
