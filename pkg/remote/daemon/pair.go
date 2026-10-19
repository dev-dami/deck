package daemon

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/hkdf"

	"github.com/dev-dami/deck/pkg/remote/devstore"
	"github.com/dev-dami/deck/pkg/remote/e2e"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

const (
	pairIDInfo     = "deck-pair-id"
	maxDeviceName  = 64
	pairConfirmFor = 10 * time.Second
)

var b64url = base64.RawURLEncoding

// RelayControl is the part of the relay client pairing needs. A daemon with
// no relay (LAN only) leaves it nil.
type RelayControl interface {
	PairOpen(pairIDHash string, exp time.Time) error
	PairClose() error
	SendDevices() error
}

// PairInvite is what `deck remote pair` shows: the URL for the QR code and
// when it stops working.
type PairInvite struct {
	URL       string
	Blob      wire.QRBlob
	ExpiresAt time.Time
}

type slot struct {
	psk   []byte
	exp   time.Time
	fails int
}

// PairIDOf derives the id a phone presents to the relay from the pairing
// secret: base64url of HKDF-SHA256(ikm=psk, salt=none, info="deck-pair-id",
// 32 bytes). The relay stores only PairIDHash of it.
func PairIDOf(psk []byte) (string, error) {
	out := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, psk, nil, []byte(pairIDInfo)), out); err != nil {
		return "", err
	}
	return b64url.EncodeToString(out), nil
}

// PairIDHash is the hex sha256 of a pair id, as sent to the relay in pair_open.
func PairIDHash(pairID string) string {
	sum := sha256.Sum256([]byte(pairID))
	return hex.EncodeToString(sum[:])
}

// ArmPair opens the one pairing slot: a fresh secret good for
// wire.PairCodeTTL, replacing any earlier one. relayURL and lan end up in the
// QR; either may be empty.
func (d *Daemon) ArmPair(relayURL string, lan []string, fp string) (PairInvite, error) {
	psk := make([]byte, 32)
	if _, err := rand.Read(psk); err != nil {
		return PairInvite{}, err
	}
	id, err := PairIDOf(psk)
	if err != nil {
		return PairInvite{}, err
	}
	exp := d.cfg.Now().Add(wire.PairCodeTTL)

	d.mu.Lock()
	d.slot = &slot{psk: psk, exp: exp}
	d.mu.Unlock()
	if d.cfg.Relay != nil {
		// A code the relay does not know is a QR that can only fail on the
		// phone, so say so here instead.
		if err := d.cfg.Relay.PairOpen(PairIDHash(id), exp); err != nil {
			d.mu.Lock()
			d.slot = nil
			d.mu.Unlock()
			return PairInvite{}, fmt.Errorf("the relay is not reachable yet: %w", err)
		}
	}

	blob := wire.QRBlob{
		V: 1, CID: d.cfg.Identity.CID(), Relay: relayURL,
		S: b64url.EncodeToString(d.cfg.Identity.StaticPublic()), PSK: b64url.EncodeToString(psk),
		Exp: exp.Unix(), Name: d.cfg.Name, LAN: lan, FP: fp,
	}
	raw, err := json.Marshal(blob)
	if err != nil {
		return PairInvite{}, err
	}
	return PairInvite{URL: relayURL + "/#p=" + b64url.EncodeToString(raw), Blob: blob, ExpiresAt: exp}, nil
}

// DisarmPair closes the slot.
func (d *Daemon) DisarmPair() {
	d.mu.Lock()
	had := d.slot != nil
	d.slot = nil
	d.mu.Unlock()
	if had && d.cfg.Relay != nil {
		_ = d.cfg.Relay.PairClose()
	}
}

// takeSlot returns the live slot's secret, or nil.
func (d *Daemon) takeSlot() []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.slot == nil || !d.cfg.Now().Before(d.slot.exp) {
		return nil
	}
	return d.slot.psk
}

// pairFailed counts a failed attempt against the slot that was live when it
// began and burns the slot at wire.PairFailuresToBurn.
func (d *Daemon) pairFailed(psk []byte) {
	d.mu.Lock()
	burn := false
	if d.slot != nil && string(d.slot.psk) == string(psk) {
		d.slot.fails++
		burn = d.slot.fails >= wire.PairFailuresToBurn
	}
	d.mu.Unlock()
	if burn {
		d.DisarmPair()
	}
}

// ServePair runs one pairing attempt on a pair lane: Noise IKpsk2 with the
// slot's secret. The device is pinned when the phone's handshake is proven
// good by its first message, and the slot is spent. Anything else counts as a
// failure.
func (d *Daemon) ServePair(ctx context.Context, c Conn) error {
	psk := d.takeSlot()
	if psk == nil {
		_ = c.Close(wire.CloseTokenInvalid, "no pairing in progress")
		return errors.New("daemon: no pairing slot")
	}
	hctx, cancel := context.WithTimeout(ctx, wire.HelloTimeout)
	defer cancel()
	msg1, err := c.Read(hctx)
	if err != nil {
		return err
	}
	if len(msg1) > wire.MaxHelloBytes {
		_ = c.Close(wire.CloseBadHello, "bad hello")
		d.pairFailed(psk)
		return errors.New("daemon: pairing message too large")
	}

	var dev devstore.Device
	var token string
	resp := e2e.Responder{
		Static: e2e.Keypair{Private: d.cfg.Identity.StaticPrivate(), Public: d.cfg.Identity.StaticPublic()},
		CID:    d.cfg.Identity.CID(),
		PSK:    psk,
	}
	msg2, sess, err := resp.Accept(msg1, func(initiator, payload []byte) ([]byte, error) {
		var hello wire.PairHello
		if err := json.Unmarshal(payload, &hello); err != nil || hello.DeviceName == "" || utf8.RuneCountInString(hello.DeviceName) > maxDeviceName {
			return nil, e2e.ErrHandshake
		}
		var err error
		if dev, token, err = d.cfg.Devices.AddPending(hello.DeviceName, initiator); err != nil {
			return nil, err
		}
		return json.Marshal(wire.PairWelcome{DeviceID: dev.ID, RelayToken: token, ExpiresAt: dev.ExpiresAt, Name: d.cfg.Name})
	})
	if err != nil {
		_ = c.Close(wire.CloseBadHello, "pairing refused")
		d.pairFailed(psk)
		return err
	}
	// The device is stored but pending, so no connection path accepts it until
	// the phone proves it could read message 2, which only a holder of the
	// secret can.
	revoke := func() {
		if rerr := d.cfg.Devices.Revoke(dev.ID); rerr != nil {
			slog.Warn("remote: unconfirmed device not removed", "error", rerr)
		}
		d.pairFailed(psk)
	}
	if err := c.Write(hctx, msg2); err != nil {
		revoke()
		return err
	}
	cctx, ccancel := context.WithTimeout(ctx, pairConfirmFor)
	defer ccancel()
	confirm, err := d.readFrame(cctx, c, sess, 0)
	if err != nil || confirm.T != wire.FrameHello {
		revoke()
		_ = c.Close(wire.CloseBadHello, "pairing not confirmed")
		return fmt.Errorf("daemon: pairing not confirmed: %v", err)
	}

	if err := d.cfg.Devices.Confirm(dev.ID); err != nil {
		d.pairFailed(psk)
		_ = c.Close(wire.CloseBadHello, "pairing not confirmed")
		return fmt.Errorf("daemon: confirm device: %w", err)
	}
	d.DisarmPair()
	if d.cfg.Relay != nil {
		if err := d.cfg.Relay.SendDevices(); err != nil {
			slog.Warn("remote: relay not told about the new phone", "error", err)
		}
	}
	d.hub.publish(event{name: wire.EvDevicePaired, payload: wire.Device{
		ID: dev.ID, Name: dev.Name, CreatedAt: dev.CreatedAt, ExpiresAt: dev.ExpiresAt,
	}})
	if d.cfg.OnPaired != nil {
		d.cfg.OnPaired(dev)
	}
	_ = c.Close(0, "")
	return nil
}
