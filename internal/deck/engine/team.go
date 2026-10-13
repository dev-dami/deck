package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/hub"
	"github.com/dev-dami/deck/internal/deck/peer"
)

// peerTeam is the peer link as the peers.* ops show it to agents: who this
// Deck's teammates are and how the link to each is doing.
type peerTeam struct{ s *peer.Service }

var _ hub.Team = peerTeam{}

func (t peerTeam) List() string {
	snap := t.s.Snapshot()
	var b strings.Builder
	switch {
	case !snap.Enabled:
		fmt.Fprintf(&b, "This Deck is %q; its peer link is off (turn it on in Settings › Team or with `deck peer`).\n", snap.Name)
	case snap.Listening != "":
		fmt.Fprintf(&b, "This Deck is %q, listening for teammates on %s.\n", snap.Name, snap.Listening)
	default:
		fmt.Fprintf(&b, "This Deck is %q.\n", snap.Name)
	}
	if snap.Err != "" {
		fmt.Fprintf(&b, "Problem: %s\n", snap.Err)
	}
	if len(snap.Peers) == 0 {
		b.WriteString("No teammates are paired yet: pair one in Settings › Team or with `deck peer pair`.")
		return b.String()
	}
	fmt.Fprintf(&b, "Teammates (%d):\n", len(snap.Peers))
	for _, p := range snap.Peers {
		role := p.Role
		if role == "" {
			role = "observer"
		}
		where := "dials in to this Deck"
		if p.Addr != "" && p.Dial {
			where = "this Deck dials " + p.Addr
		} else if p.Addr != "" {
			where = "at " + p.Addr + " (not dialed)"
		}
		fmt.Fprintf(&b, "  %s: %s · role %s · %s", p.Name, linkText(p, snap.Enabled), role, where)
		if !p.Pinned {
			b.WriteString(" · not paired yet (no pinned key)")
		}
		b.WriteByte('\n')
	}
	b.WriteString("Ask one how it is with peers.status {peer}.")
	return b.String()
}

func (t peerTeam) Status(ctx context.Context, name string) (string, error) {
	r, err := t.s.Ping(ctx, name)
	if err != nil {
		return "", fmt.Errorf("could not reach %s: %w", name, err)
	}
	status := strings.TrimSpace(r.Status)
	if status == "" {
		status = "(no status)"
	}
	return fmt.Sprintf("%s answered in %s.\n%s", name, r.Latency.Round(time.Millisecond), status), nil
}

// linkText is how the link to one teammate is doing, in a few words.
func linkText(p peer.PeerState, enabled bool) string {
	if !enabled {
		return "offline (link off)"
	}
	l := p.Link
	if l.Supervised {
		s := l.Status
		switch s.State {
		case peer.StateConnected:
			if s.Latency > 0 {
				return "connected, " + s.Latency.Round(time.Millisecond).String()
			}
			return "connected"
		case peer.StateBackoff:
			out := "reconnecting"
			if !s.NextRetry.IsZero() {
				out += " in " + time.Until(s.NextRetry).Round(time.Second).String()
			}
			if s.LastError != "" {
				out += " (" + s.LastError + ")"
			}
			return out
		default:
			return string(s.State)
		}
	}
	if l.Inbound > 0 {
		return "connected (it dialed in)"
	}
	if !l.LastSeen.IsZero() {
		return "offline, last seen " + time.Since(l.LastSeen).Round(time.Minute).String() + " ago"
	}
	return "offline"
}
