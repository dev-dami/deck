package peer

import (
	"context"
	"encoding/json"
	"maps"
	"reflect"
	"time"
)

// Presence (docs/multiplayer-design.md §8.2): peer.status also says what a
// Deck's agents are doing, which files they hold in repositories the asking
// Deck has too, and, when its person lets it (peers.share_limits), how full
// its CLI plans are. Each Deck asks the teammates it has a link to every
// presenceEvery and shows the answers in its Snapshot.

const (
	presenceEvery = 20 * time.Second
	// The most of each a teammate's answer may hold; the rest is dropped.
	maxPresenceItems = 32
	// maxRepos caps the repositories one peer.status may ask about.
	maxRepos = 32
)

// Presence is what peer.status says about a Deck's work.
type Presence struct {
	Agents []AgentPresence `json:"agents,omitempty"`
	// Leases are file leases in the repositories the asking Deck named.
	Leases []LeasePresence `json:"leases,omitempty"`
	// Limits are its CLIs' plan windows, when it shares them.
	Limits []LimitPresence `json:"limits,omitempty"`
}

// AgentPresence is one running agent.
type AgentPresence struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	Task       string `json:"task,omitempty"` // the title of the task it runs
	ContextPct int    `json:"context_pct,omitempty"`
}

// LeasePresence is one file lease (session/leases.go).
type LeasePresence struct {
	Repo      string    `json:"repo"` // the repository's root commit
	Agent     string    `json:"agent"`
	Paths     []string  `json:"paths"`
	Exclusive bool      `json:"exclusive,omitempty"`
	Until     time.Time `json:"until"`
}

// LimitPresence is one plan window of one CLI.
type LimitPresence struct {
	Kind    string  `json:"kind"`
	Window  string  `json:"window"`
	UsedPct float64 `json:"used_pct"`
}

// Local is this Deck's own work, as the engine reads it from its threads.
type Local struct {
	// Presence is what peer.status tells a teammate: leases only in repos
	// (root commits), plan windows only with limits set.
	Presence func(repos []string, limits bool) Presence
	// Repos are the root commits of the repositories this Deck works in.
	Repos func() []string
}

// clean keeps what a teammate sent within bounds, for display.
func (p Presence) clean() Presence {
	str := func(s string, n int) string { return Sanitize(truncate(s, n)) }
	var out Presence
	for _, a := range p.Agents[:min(len(p.Agents), maxPresenceItems)] {
		out.Agents = append(out.Agents, AgentPresence{Name: str(a.Name, 32), Kind: str(a.Kind, 32), Status: str(a.Status, 32), Task: str(a.Task, 120), ContextPct: max(0, min(a.ContextPct, 100))})
	}
	for _, l := range p.Leases[:min(len(p.Leases), maxPresenceItems)] {
		c := LeasePresence{Repo: str(l.Repo, 64), Agent: str(l.Agent, 32), Exclusive: l.Exclusive, Until: l.Until}
		for _, path := range l.Paths[:min(len(l.Paths), 16)] {
			c.Paths = append(c.Paths, str(path, 200))
		}
		out.Leases = append(out.Leases, c)
	}
	for _, l := range p.Limits[:min(len(p.Limits), 16)] {
		out.Limits = append(out.Limits, LimitPresence{Kind: str(l.Kind, 32), Window: str(l.Window, 32), UsedPct: l.UsedPct})
	}
	return out
}

// presenceLoop asks every teammate with a link up for its presence, every
// presenceEvery, until the Service closes.
func (s *Service) presenceLoop() {
	for {
		s.pollPresence(s.base)
		select {
		case <-s.base.Done():
			return
		case <-time.After(presenceEvery):
		}
	}
}

// pollPresence asks the teammates with a link up for peer.status, over
// that link only (it never dials), and keeps their presence.
func (s *Service) pollPresence(ctx context.Context) {
	mgr := s.Manager()
	if mgr == nil {
		return
	}
	var repos []string
	if s.local.Repos != nil {
		repos = s.local.Repos()
	}
	got := map[string]Presence{}
	for name := range mgr.Links() {
		cctx, cancel := context.WithTimeout(ctx, chatTimeout)
		cl, release := mgr.channel(cctx, name)
		if cl == nil {
			cancel()
			continue
		}
		r, err := cl.Execute(cctx, OpPeerStatus, map[string]any{"repos": repos})
		release()
		cancel()
		if err != nil || r.IsError {
			continue
		}
		var st struct {
			Presence *Presence `json:"presence"`
		}
		if json.Unmarshal([]byte(r.Text()), &st) == nil && st.Presence != nil {
			got[name] = st.Presence.clean()
		}
	}
	s.mu.Lock()
	same := maps.EqualFunc(s.presence, got, func(a, b Presence) bool { return reflect.DeepEqual(a, b) })
	s.presence = got
	s.mu.Unlock()
	if !same {
		s.changed()
	}
}

// PeerStatus asks the teammate name for peer.status, with the repositories
// this Deck works in, over a link that is up or one dialed for it.
func (s *Service) PeerStatus(ctx context.Context, name string) (string, error) {
	var repos []string
	if s.local.Repos != nil {
		repos = s.local.Repos()
	}
	var text string
	err := s.withPeer(ctx, name, func(cl *Client) error {
		r, err := cl.Execute(ctx, OpPeerStatus, map[string]any{"repos": repos})
		if err != nil {
			return err
		}
		text = Sanitize(truncate(r.Text(), 8000))
		return nil
	})
	return text, err
}
