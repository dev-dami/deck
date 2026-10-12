package doctor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/peer"
)

// PeerChecker answers the peers report's questions. Tests inject a fake.
type PeerChecker interface {
	// Identity returns this Deck's fingerprint and key file, or
	// peer.ErrNoIdentity when no key has been made.
	Identity() (fingerprint, path string, err error)
	// Private returns an error when path is accessible by other users.
	Private(path string) error
	// Pinned returns the fingerprint pinned for name.
	Pinned(name string) (fingerprint string, ok bool, err error)
	// Probe connects to the pinned peer name and returns the ping round trip.
	Probe(ctx context.Context, name string, timeout time.Duration) (time.Duration, error)
}

// peerProbeTimeout keeps doctor quick when a peer is down.
const peerProbeTimeout = 3 * time.Second

// peersReport checks the identity, and for each configured remote that it is
// pinned and answers.
func peersReport(ctx context.Context, opts Options) string {
	pc := opts.PeerChecker
	if pc == nil {
		pc = realPeerChecker{}
	}
	p := opts.Peers
	var b strings.Builder
	state := "off"
	if p.Enabled {
		state = "on"
	}
	fmt.Fprintf(&b, "\npeers (%s), this Deck is %q\n", state, peer.SelfName(p))

	fp, path, err := pc.Identity()
	haveID := err == nil
	switch {
	case errors.Is(err, peer.ErrNoIdentity):
		fmt.Fprintf(&b, "✗ %-8s  not created — run: deck peer fingerprint\n", "identity")
	case err != nil:
		fmt.Fprintf(&b, "✗ %-8s  %v\n", "identity", err)
	default:
		if perr := pc.Private(path); perr != nil {
			fmt.Fprintf(&b, "! %-8s  %s  not private: %v\n", "identity", fp, perr)
		} else {
			fmt.Fprintf(&b, "✓ %-8s  %s  (private)\n", "identity", fp)
		}
	}

	if p.Listen.Enabled {
		fmt.Fprintf(&b, "  %-8s  %s\n", "listen", p.Listen.ListenAddr())
	} else {
		fmt.Fprintf(&b, "  %-8s  off\n", "listen")
	}

	for _, r := range p.Remote {
		pfp, ok, err := pc.Pinned(r.Name)
		switch {
		case err != nil:
			fmt.Fprintf(&b, "✗ %-8s  pinned peers unreadable: %v\n", r.Name, err)
			continue
		case !ok:
			addr := r.Addr
			if addr == "" {
				addr = "<host:port>"
			}
			fmt.Fprintf(&b, "✗ %-8s  not paired — run: deck peer add %s %s --code <code>\n", r.Name, r.Name, addr)
			continue
		}
		short := shortFingerprint(pfp)
		if r.Addr == "" {
			fmt.Fprintf(&b, "✓ %-8s  pinned %s, dials in\n", r.Name, short)
			continue
		}
		if !haveID {
			fmt.Fprintf(&b, "! %-8s  pinned %s, not checked (no identity)\n", r.Name, short)
			continue
		}
		lat, err := pc.Probe(ctx, r.Name, peerProbeTimeout)
		if err != nil {
			fmt.Fprintf(&b, "✗ %-8s  pinned %s, unreachable at %s: %s\n", r.Name, short, r.Addr, oneLine(err.Error(), 120))
			continue
		}
		fmt.Fprintf(&b, "✓ %-8s  pinned %s, reachable at %s (%s)\n", r.Name, short, r.Addr, lat.Round(time.Millisecond))
	}
	return b.String()
}

// shortFingerprint keeps "SHA256:" and the first 12 characters.
func shortFingerprint(fp string) string {
	if len(fp) > 19 {
		return fp[:19] + "…"
	}
	return fp
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		s = string(r[:n-1]) + "…"
	}
	return s
}

// realPeerChecker checks ~/.deck/peers.
type realPeerChecker struct{}

func (realPeerChecker) dir() (string, error) { return peer.DefaultDir() }

func (c realPeerChecker) Identity() (string, string, error) {
	dir, err := c.dir()
	if err != nil {
		return "", "", err
	}
	id, err := peer.LoadIdentity(dir)
	if err != nil {
		return "", "", err
	}
	return id.Fingerprint(), id.Path, nil
}

func (realPeerChecker) Private(path string) error { return peer.CheckPrivate(path) }

func (c realPeerChecker) Pinned(name string) (string, bool, error) {
	dir, err := c.dir()
	if err != nil {
		return "", false, err
	}
	p, ok, err := peer.OpenStore(dir).Get(name)
	return p.Fingerprint, ok, err
}

func (c realPeerChecker) Probe(ctx context.Context, name string, timeout time.Duration) (time.Duration, error) {
	dir, err := c.dir()
	if err != nil {
		return 0, err
	}
	id, err := peer.LoadIdentity(dir)
	if err != nil {
		return 0, err
	}
	pin, ok, err := peer.OpenStore(dir).Get(name)
	if err != nil || !ok {
		return 0, fmt.Errorf("not pinned")
	}
	r, err := peer.Probe(ctx, id, pin, "doctor", timeout)
	return r.Latency, err
}
