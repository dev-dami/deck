package peer

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/dev-dami/deck/internal/deck/sub"
	"github.com/dev-dami/deck/internal/version"
)

// The record of the capsules teammates asked this Deck for, so the person can
// share a private one when asked. It lives in Team and reaches status views
// through the Service's Snapshot, which every change bumps. The team chat
// (console.post, console.read, chat.jsonl) was removed in Deck v3; a
// chat.jsonl left in the peers directory is not read.

const (
	// maxChatText bounds one message posted to a shared thread, in bytes.
	maxChatText = 4000
	// maxRequests is how many capsule requests are kept.
	maxRequests = 50
	// requestRepeat folds a teammate asking for the same capsule again
	// within this window into the first request.
	requestRepeat = time.Minute
	// callTimeout bounds one call to a teammate's Deck (peer.status).
	callTimeout = 15 * time.Second
)

// CapsuleRequest is a teammate asking this Deck for a capsule.
type CapsuleRequest struct {
	Seq  uint64
	From string
	ID   sub.ID
	At   time.Time
	// Served is true when the capsule was public and the teammate got it;
	// false when it was private and needs the person to share it.
	Served bool
}

// Team holds the capsule requests.
type Team struct {
	onChange func()

	mu   sync.Mutex
	seq  uint64
	reqs []CapsuleRequest
}

// NewTeam calls onChange (which must not block) after every change.
func NewTeam(onChange func()) *Team {
	return &Team{onChange: onChange}
}

func (t *Team) changed() {
	if t.onChange != nil {
		t.onChange()
	}
}

// AddRequest records a teammate asking for capsule id; served says whether
// it was public and handed over.
func (t *Team) AddRequest(from string, id sub.ID, served bool) {
	now := time.Now()
	t.mu.Lock()
	for i := len(t.reqs) - 1; i >= 0; i-- {
		r := t.reqs[i]
		if r.From == from && r.ID == id && r.Served == served && now.Sub(r.At) < requestRepeat {
			t.mu.Unlock()
			return
		}
	}
	t.seq++
	t.reqs = append(t.reqs, CapsuleRequest{Seq: t.seq, From: from, ID: id, At: now, Served: served})
	if len(t.reqs) > maxRequests {
		t.reqs = t.reqs[len(t.reqs)-maxRequests:]
	}
	t.mu.Unlock()
	t.changed()
}

// Requests are the kept capsule requests, oldest first.
func (t *Team) Requests() []CapsuleRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]CapsuleRequest(nil), t.reqs...)
}

// cleanChat trims a message, drops control and format characters (bidi
// overrides, zero-width) a peer could use to garble the screen, and caps it.
func cleanChat(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if len(s) > maxChatText {
		s = strings.ToValidUTF8(s[:maxChatText], "") + "…"
	}
	return s
}

// cleanLine is s with control characters gone, on one line, capped at n
// bytes.
func cleanLine(s string, n int) string {
	return strings.Join(strings.Fields(cleanText(s, n)), " ")
}

// cleanText is s with control and format characters gone (newlines and tabs
// stay), capped at n bytes.
func cleanText(s string, n int) string {
	s = cleanChat(s)
	if len(s) > n {
		s = strings.ToValidUTF8(s[:n], "") + "…"
	}
	return s
}

// withPeer runs fn on an MCP channel to the pinned peer name: the one its
// supervisor keeps open, else a new channel on a link the peer opened to
// this Deck, else a link dialed for this call alone.
func (s *Service) withPeer(ctx context.Context, name string, fn func(cl *Client) error) error {
	if mgr := s.Manager(); mgr != nil {
		if cl, release := mgr.channel(ctx, name); cl != nil {
			defer release()
			return fn(cl)
		}
	}
	p, _ := s.loadPeers()
	return callPeer(ctx, s.dir, name, SelfName(p), fn)
}

// Team is the capsule requests.
func (s *Service) Team() *Team { return s.team }

// callPeer dials the pinned peer name, opens its MCP channel and runs fn.
func callPeer(ctx context.Context, dir, name, self string, fn func(cl *Client) error) error {
	pin, ok, err := OpenStore(dir).Get(name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no peer %q is paired", name)
	}
	if pin.Addr == "" {
		return fmt.Errorf("%s has no address this Deck can dial (it dials in)", name)
	}
	ident, err := LoadIdentity(dir)
	if err != nil {
		return err
	}
	conn, err := Dial(ctx, DialOptions{
		Identity: ident, Peer: pin, Name: self, Version: version.Version,
		DialTimeout: 10 * time.Second, Limits: Limits{HandshakeTimeout: 10 * time.Second},
	})
	if err != nil {
		return fmt.Errorf("%s: %s", name, Sanitize(err.Error()))
	}
	defer conn.Close()
	cl, err := conn.OpenMCP(ctx)
	if err != nil {
		return err
	}
	defer cl.Close()
	if _, err := cl.Initialize(ctx); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	return fn(cl)
}
