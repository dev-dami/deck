package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/hub"
)

// maxTeamPosts is how many times a minute one agent may post to the team
// chat (team.post), which people on other machines read.
const maxTeamPosts = 4

// teamPost sends text from member a to the team chat, redacted, within
// maxTeamPosts a minute.
func (m *Manager) teamPost(a *agentState, text string) (string, error) {
	m.mu.Lock()
	team := m.team
	m.mu.Unlock()
	if team == nil {
		return "", errors.New("this Deck has no peer link, so no team chat")
	}
	if text = strings.TrimSpace(hub.Redact(text)); text == "" {
		return "", errors.New("team.post needs text")
	}
	t, key, now := a.thread, "team:"+a.id, time.Now()
	t.mu.Lock()
	recent := t.sends[key][:0]
	for _, at := range t.sends[key] {
		if now.Sub(at) < time.Minute {
			recent = append(recent, at)
		}
	}
	if len(recent) >= maxTeamPosts {
		t.mu.Unlock()
		return "", fmt.Errorf("slow down: you posted %d times in the last minute; the team chat is for people too", len(recent))
	}
	if t.sends == nil {
		t.sends = map[string][]time.Time{}
	}
	t.sends[key] = append(recent, now)
	t.mu.Unlock()
	ctx, cancel := context.WithTimeout(m.ctx, hub.Timeout)
	defer cancel()
	return team.Post(ctx, a.getName(), text)
}
