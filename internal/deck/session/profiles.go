package session

import (
	"fmt"
	"strings"

	"github.com/dev-dami/deck/internal/deck/profile"
	"github.com/dev-dami/deck/internal/deck/store"
)

// SetProfiles installs what reads the named agents (deck.json "profiles")
// for a thread's folder. "@charles" then adds a member from the profile
// charles to a thread with no member of that name. The engine installs it;
// without it no name is a profile.
func (m *Manager) SetProfiles(f func(dir string) []profile.Profile) {
	m.mu.Lock()
	m.profiles = f
	m.mu.Unlock()
}

// profilesFor is the profiles a message in t can name: none when text
// holds no sigil (to spare reading deck.json), and none that a teammate's
// name shadows.
func (m *Manager) profilesFor(t *thread, text string) []profile.Profile {
	if !strings.ContainsAny(text, "@$") {
		return nil
	}
	m.mu.Lock()
	read, team := m.profiles, m.team
	m.mu.Unlock()
	if read == nil {
		return nil
	}
	ps := read(t.dir)
	if team != nil {
		ps = profile.Without(ps, team.Names())
	}
	return ps
}

// storedProfile is the profile a resumed member came from: the one of that
// name in deck.json now, else what the thread stored of it.
func (m *Manager) storedProfile(t *thread, mem store.Member) profile.Profile {
	if mem.Profile == "" {
		return profile.Profile{}
	}
	m.mu.Lock()
	read := m.profiles
	m.mu.Unlock()
	if read != nil {
		if p, ok := profile.Find(read(t.dir), mem.Profile); ok && p.Kind == mem.Kind {
			return p
		}
	}
	return profile.Profile{Name: mem.Profile, Kind: mem.Kind, Role: mem.Role}
}

// freeProfileName names a new member from the profile called name: the
// name itself while no member of the thread has it, else name-2, name-3,
// ... t.mu is held.
func (t *thread) freeProfileName(name string) string {
	if !t.taken(name) {
		return name
	}
	for n := 2; ; n++ {
		if c := fmt.Sprintf("%s-%d", name, n); !t.taken(c) {
			return c
		}
	}
}

// who describes member o for a briefing: its name, then its kind when the
// name is not the kind's, and its role ("charles (claude, reviewer)").
func who(o *agentState) string {
	name := o.getName()
	var tags []string
	if !strings.EqualFold(name, o.kind) {
		tags = append(tags, o.kind)
	}
	if r := oneLine(o.prof.Role); r != "" {
		tags = append(tags, clip(r, 60))
	}
	if len(tags) == 0 {
		return name
	}
	return name + " (" + strings.Join(tags, ", ") + ")"
}
