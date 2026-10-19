package e2e

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/dev-dami/deck/pkg/remote/wire"
)

const cid = "Zk3p0Jq8vYb1nT5cR2sWxA"

func keys(t *testing.T) Keypair {
	t.Helper()
	k, err := GenerateKeypair(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// connect runs an IK handshake and returns both ends.
func connect(t *testing.T) (phone, computer *Session) {
	t.Helper()
	ps, cs := keys(t), keys(t)
	msg1, pend, err := Initiator{Static: ps, PeerStatic: cs.Public, CID: cid}.Start([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	msg2, computer, err := Responder{Static: cs, CID: cid}.Accept(msg1, func(init, payload []byte) ([]byte, error) {
		if !bytes.Equal(init, ps.Public) || string(payload) != "hello" {
			t.Errorf("responder saw wrong initiator or payload")
		}
		return []byte("welcome"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	reply, phone, err := pend.Finish(msg2)
	if err != nil || string(reply) != "welcome" {
		t.Fatalf("finish: %q %v", reply, err)
	}
	return phone, computer
}

func send(t *testing.T, from, to *Session, frame []byte) []byte {
	t.Helper()
	msgs, err := from.Seal(frame)
	if err != nil {
		t.Fatal(err)
	}
	var got []byte
	for i, m := range msgs {
		out, err := to.Open(m)
		if err != nil {
			t.Fatalf("open chunk %d: %v", i, err)
		}
		if i < len(msgs)-1 && out != nil {
			t.Fatal("frame completed before its last chunk")
		}
		got = out
	}
	return got
}

func TestSessionBothDirections(t *testing.T) {
	phone, computer := connect(t)
	if got := send(t, phone, computer, []byte(`{"t":"hello","v":3}`)); string(got) != `{"t":"hello","v":3}` {
		t.Fatalf("computer got %q", got)
	}
	if got := send(t, computer, phone, []byte(`{"t":"welcome"}`)); string(got) != `{"t":"welcome"}` {
		t.Fatalf("phone got %q", got)
	}
	if !bytes.Equal(phone.Hash, computer.Hash) {
		t.Fatal("channel bindings differ")
	}
}

func TestEmptyFrameRoundTrips(t *testing.T) {
	phone, computer := connect(t)
	if got := send(t, phone, computer, nil); got == nil || len(got) != 0 {
		t.Fatalf("empty frame came back as %v", got)
	}
}

func TestLargeFrameIsChunked(t *testing.T) {
	phone, computer := connect(t)
	frame := make([]byte, 3<<20)
	if _, err := rand.Read(frame); err != nil {
		t.Fatal(err)
	}
	msgs, err := phone.Seal(frame)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if len(m) > 65535 {
			t.Fatalf("a noise message is %d bytes", len(m))
		}
	}
	var got []byte
	for _, m := range msgs {
		if got, err = computer.Open(m); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(got, frame) {
		t.Fatal("reassembled frame differs")
	}
}

func TestFrameOverLimitRefused(t *testing.T) {
	phone, _ := connect(t)
	if _, err := phone.Seal(make([]byte, wire.MaxFrameBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v", err)
	}
}

func TestTamperAndReplayAreRejected(t *testing.T) {
	phone, computer := connect(t)
	msgs, _ := phone.Seal([]byte("pay"))
	bad := bytes.Clone(msgs[0])
	bad[len(bad)-1] ^= 1
	if _, err := computer.Open(bad); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("tampered message: %v", err)
	}
	// A failed open does not advance the nonce, so the genuine one still opens.
	if _, err := computer.Open(msgs[0]); err != nil {
		t.Fatalf("genuine message after a tampered one: %v", err)
	}
	if _, err := computer.Open(msgs[0]); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("replay accepted: %v", err)
	}
}

func TestRekeyKeepsBothSidesInStep(t *testing.T) {
	phone, computer := connect(t)
	for i := 0; i < 2*wire.NoiseRekeyMessages+3; i++ {
		if got := send(t, phone, computer, []byte{byte(i)}); len(got) != 1 || got[0] != byte(i) {
			t.Fatalf("message %d lost", i)
		}
	}
	if got := send(t, computer, phone, []byte("back")); string(got) != "back" {
		t.Fatal("reverse direction broke after rekey")
	}
}

func TestUnknownDeviceCanBeRefused(t *testing.T) {
	ps, cs := keys(t), keys(t)
	msg1, _, _ := Initiator{Static: ps, PeerStatic: cs.Public, CID: cid}.Start(nil)
	_, _, err := Responder{Static: cs, CID: cid}.Accept(msg1, func(init, _ []byte) ([]byte, error) {
		return nil, ErrUnknownDevice
	})
	if !errors.Is(err, ErrUnknownDevice) {
		t.Fatalf("got %v", err)
	}
}

func TestHandshakeBoundToComputerAndKey(t *testing.T) {
	ps, cs, other := keys(t), keys(t), keys(t)
	ok := func([]byte, []byte) ([]byte, error) { return nil, nil }

	// Another cid in the prologue.
	msg1, _, _ := Initiator{Static: ps, PeerStatic: cs.Public, CID: "someone-elses-computer"}.Start(nil)
	if _, _, err := (Responder{Static: cs, CID: cid}).Accept(msg1, ok); !errors.Is(err, ErrHandshake) {
		t.Errorf("wrong cid accepted: %v", err)
	}
	// A phone that pinned a different computer key.
	msg1, _, _ = Initiator{Static: ps, PeerStatic: other.Public, CID: cid}.Start(nil)
	if _, _, err := (Responder{Static: cs, CID: cid}).Accept(msg1, ok); !errors.Is(err, ErrHandshake) {
		t.Errorf("wrong computer key accepted: %v", err)
	}
}

func TestPairingNeedsThePSK(t *testing.T) {
	ps, cs := keys(t), keys(t)
	psk := bytes.Repeat([]byte{7}, 32)
	pair := func(phonePSK []byte) error {
		msg1, pend, err := Initiator{Static: ps, PeerStatic: cs.Public, CID: cid, PSK: phonePSK}.Start([]byte("Pixel 9"))
		if err != nil {
			return err
		}
		msg2, _, err := Responder{Static: cs, CID: cid, PSK: psk}.Accept(msg1, func(_, _ []byte) ([]byte, error) {
			return []byte("token"), nil
		})
		if err != nil {
			return err
		}
		_, _, err = pend.Finish(msg2)
		return err
	}
	if err := pair(psk); err != nil {
		t.Fatalf("right psk: %v", err)
	}
	if err := pair(bytes.Repeat([]byte{8}, 32)); !errors.Is(err, ErrHandshake) {
		t.Fatalf("wrong psk: %v", err)
	}
}

func TestSealedK(t *testing.T) {
	ps, cs, stranger := keys(t), keys(t), keys(t)
	msg, err := SealK(ps, cs.Public, cid, []byte(`{"qid":"1"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := OpenK(cs, ps.Public, cid, msg)
	if err != nil || string(got) != `{"qid":"1"}` {
		t.Fatalf("open: %q %v", got, err)
	}
	if _, err := OpenK(cs, stranger.Public, cid, msg); !errors.Is(err, ErrHandshake) {
		t.Fatalf("message accepted from a device that did not send it: %v", err)
	}
	if _, err := OpenK(cs, ps.Public, "other-cid", msg); !errors.Is(err, ErrHandshake) {
		t.Fatalf("message accepted for another computer: %v", err)
	}
}

func FuzzOpen(f *testing.F) {
	f.Add([]byte{}, []byte("x"))
	f.Add(bytes.Repeat([]byte{1}, 17), []byte{0})
	f.Fuzz(func(t *testing.T, msg1, msg []byte) {
		cs := keys(t)
		_, session, err := Responder{Static: cs, CID: cid}.Accept(msg1, func(_, _ []byte) ([]byte, error) { return nil, nil })
		if err == nil {
			t.Fatal("garbage opened a handshake")
		}
		_ = session
		phone, computer := connect(t)
		_ = phone
		if out, err := computer.Open(msg); err == nil && out != nil {
			t.Fatal("garbage opened a transport message")
		}
	})
}
