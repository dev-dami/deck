// Package e2e is the end-to-end channel between a phone and a computer:
// Noise IK for sessions, IKpsk2 for pairing and K for one-shot sealed
// messages (docs/mobile-contract.md §3). The relay and the network only
// carry its output.
package e2e

import (
	"errors"
	"fmt"
	"io"

	"github.com/flynn/noise"

	"github.com/dev-dami/deck/pkg/remote/wire"
)

var suite = noise.NewCipherSuite(noise.DH25519, noise.CipherAESGCM, noise.HashSHA256)

// Prologue binds a channel to one computer.
func Prologue(cid string) []byte { return []byte("deck-v3|" + cid) }

// Keypair is an X25519 key pair.
type Keypair struct {
	Private []byte
	Public  []byte
}

func (k Keypair) dh() noise.DHKey { return noise.DHKey{Private: k.Private, Public: k.Public} }

// Errors a caller can tell apart.
var (
	// ErrUnknownDevice is returned when the initiator's static key is not
	// one the responder accepts.
	ErrUnknownDevice = errors.New("e2e: unknown device")
	// ErrHandshake is a handshake that did not verify; the cause is not
	// distinguished on purpose.
	ErrHandshake = errors.New("e2e: handshake failed")
	// ErrDecrypt is a transport message that did not authenticate.
	ErrDecrypt = errors.New("e2e: message did not authenticate")
	// ErrTooLarge is a reassembled frame over wire.MaxFrameBytes.
	ErrTooLarge = errors.New("e2e: frame too large")
)

const (
	// chunkData is the plaintext carried per Noise message: the Noise limit
	// of 65535 bytes less the 16-byte tag and the 1-byte flag.
	chunkData = noise.MaxMsgLen - 16 - 1
	flagMore  = 0
	flagLast  = 1
)

// Session is an established channel. It is not safe for concurrent use in
// one direction: one goroutine seals and one opens.
type Session struct {
	send, recv *noise.CipherState
	sent, got  int
	partial    []byte
	// PeerStatic is the other side's static key.
	PeerStatic []byte
	// Hash is the handshake hash, for channel binding and the test vectors.
	Hash []byte
}

func newSession(send, recv *noise.CipherState, peer, hash []byte) *Session {
	return &Session{send: send, recv: recv, PeerStatic: peer, Hash: hash}
}

// Seal splits one frame into Noise transport messages of at most 65535 bytes.
// Each carries a flag byte: 0 for more to come, 1 for the last chunk.
func (s *Session) Seal(frame []byte) ([][]byte, error) {
	if len(frame) > wire.MaxFrameBytes {
		return nil, ErrTooLarge
	}
	var out [][]byte
	for first := true; first || len(frame) > 0; first = false {
		n := min(len(frame), chunkData)
		flag := byte(flagMore)
		if n == len(frame) {
			flag = flagLast
		}
		pt := append([]byte{flag}, frame[:n]...)
		ct, err := s.send.Encrypt(nil, nil, pt)
		if err != nil {
			return nil, fmt.Errorf("e2e: seal: %w", err)
		}
		out = append(out, ct)
		frame = frame[n:]
		if s.sent++; s.sent%wire.NoiseRekeyMessages == 0 {
			s.send.Rekey()
		}
	}
	return out, nil
}

// Open decrypts one transport message. It returns the whole frame once the
// last chunk arrives and nil before that. A message that fails to
// authenticate does not advance the nonce; the caller should still close the
// session, since a transport that corrupts bytes cannot be trusted.
func (s *Session) Open(msg []byte) ([]byte, error) {
	pt, err := s.recv.Decrypt(nil, nil, msg)
	if err != nil {
		return nil, ErrDecrypt
	}
	if s.got++; s.got%wire.NoiseRekeyMessages == 0 {
		s.recv.Rekey()
	}
	if len(pt) == 0 || pt[0] > flagLast {
		return nil, ErrDecrypt
	}
	if len(s.partial)+len(pt)-1 > wire.MaxFrameBytes {
		s.partial = nil
		return nil, ErrTooLarge
	}
	s.partial = append(s.partial, pt[1:]...)
	if pt[0] == flagMore {
		return nil, nil
	}
	frame := s.partial
	s.partial = nil
	if frame == nil {
		frame = []byte{}
	}
	return frame, nil
}

// Initiator is the phone side (and the Go test client).
type Initiator struct {
	Static     Keypair
	PeerStatic []byte
	CID        string
	// PSK makes the handshake IKpsk2; set for pairing only.
	PSK []byte
	// Random replaces crypto/rand; for test vectors only.
	Random io.Reader
}

// Pending is an initiator waiting for the second handshake message.
type Pending struct {
	hs *noise.HandshakeState
}

// Start writes the first handshake message carrying payload.
func (i Initiator) Start(payload []byte) ([]byte, *Pending, error) {
	hs, err := noise.NewHandshakeState(config(true, i.Static, i.PeerStatic, i.CID, i.PSK, i.Random))
	if err != nil {
		return nil, nil, fmt.Errorf("e2e: %w", err)
	}
	msg, _, _, err := hs.WriteMessage(nil, payload)
	if err != nil {
		return nil, nil, fmt.Errorf("e2e: %w", err)
	}
	return msg, &Pending{hs: hs}, nil
}

// Finish reads the second message and returns the responder's payload and the
// session. A wrong PSK or a responder that is not the pinned computer fails
// here.
func (p *Pending) Finish(msg2 []byte) ([]byte, *Session, error) {
	reply, send, recv, err := p.hs.ReadMessage(nil, msg2)
	if err != nil || send == nil {
		return nil, nil, ErrHandshake
	}
	return reply, newSession(send, recv, p.hs.PeerStatic(), p.hs.ChannelBinding()), nil
}

// Responder is the computer side.
type Responder struct {
	Static Keypair
	CID    string
	// PSK makes the handshake IKpsk2; set for pairing only.
	PSK []byte
	// Random replaces crypto/rand; for test vectors only.
	Random io.Reader
}

// Accept reads the first handshake message and calls handle with the
// initiator's static key and payload. handle decides whether to accept (a
// pinned device, or any phone holding the pairing PSK) and returns the reply
// payload; an error from it ends the handshake with nothing written.
func (r Responder) Accept(msg1 []byte, handle func(initiator, payload []byte) ([]byte, error)) ([]byte, *Session, error) {
	hs, err := noise.NewHandshakeState(config(false, r.Static, nil, r.CID, r.PSK, r.Random))
	if err != nil {
		return nil, nil, fmt.Errorf("e2e: %w", err)
	}
	payload, _, _, err := hs.ReadMessage(nil, msg1)
	if err != nil {
		return nil, nil, ErrHandshake
	}
	reply, err := handle(hs.PeerStatic(), payload)
	if err != nil {
		return nil, nil, err
	}
	msg2, send, recv, err := hs.WriteMessage(nil, reply)
	if err != nil || send == nil {
		return nil, nil, ErrHandshake
	}
	// The responder's first cipher state decrypts what the initiator sends.
	return msg2, newSession(recv, send, hs.PeerStatic(), hs.ChannelBinding()), nil
}

// SealK writes a one-message Noise K payload from the holder of static to
// peer, both statics known. It is how the phone queues work for a computer
// that is asleep.
func SealK(static Keypair, peerStatic []byte, cid string, payload []byte, random io.Reader) ([]byte, error) {
	hs, err := noise.NewHandshakeState(configK(true, static, peerStatic, cid, random))
	if err != nil {
		return nil, fmt.Errorf("e2e: %w", err)
	}
	msg, _, _, err := hs.WriteMessage(nil, payload)
	if err != nil {
		return nil, fmt.Errorf("e2e: %w", err)
	}
	return msg, nil
}

// OpenK reads a Noise K message at the computer. senderStatic is the pinned
// key of the device it must come from.
func OpenK(static Keypair, senderStatic []byte, cid string, msg []byte) ([]byte, error) {
	hs, err := noise.NewHandshakeState(configK(false, static, senderStatic, cid, nil))
	if err != nil {
		return nil, fmt.Errorf("e2e: %w", err)
	}
	payload, _, _, err := hs.ReadMessage(nil, msg)
	if err != nil {
		return nil, ErrHandshake
	}
	return payload, nil
}

func config(initiator bool, static Keypair, peer []byte, cid string, psk []byte, random io.Reader) noise.Config {
	cfg := noise.Config{
		CipherSuite:   suite,
		Pattern:       noise.HandshakeIK,
		Initiator:     initiator,
		Prologue:      Prologue(cid),
		StaticKeypair: static.dh(),
		PeerStatic:    peer,
		Random:        random,
	}
	if len(psk) > 0 {
		cfg.PresharedKey = psk
		cfg.PresharedKeyPlacement = 2
	}
	return cfg
}

func configK(initiator bool, static Keypair, peer []byte, cid string, random io.Reader) noise.Config {
	return noise.Config{
		CipherSuite:   suite,
		Pattern:       noise.HandshakeK,
		Initiator:     initiator,
		Prologue:      Prologue(cid),
		StaticKeypair: static.dh(),
		PeerStatic:    peer,
		Random:        random,
	}
}

// GenerateKeypair makes an X25519 key pair.
func GenerateKeypair(random io.Reader) (Keypair, error) {
	kp, err := noise.DH25519.GenerateKeypair(random)
	if err != nil {
		return Keypair{}, err
	}
	return Keypair{Private: kp.Private, Public: kp.Public}, nil
}
