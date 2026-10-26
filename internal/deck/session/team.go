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

// teamNews is the team chat a has not been told of yet, for the start of
// its next turn, and the last message number it covers; "" when there is
// none. Teammates' messages reach agents only this way: they never start a
// turn of their own.
func (m *Manager) teamNews(a *agentState) (string, uint64) {
	m.mu.Lock()
	team := m.team
	m.mu.Unlock()
	if team == nil {
		return "", 0
	}
	a.mu.Lock()
	since := a.teamSeq
	a.mu.Unlock()
	return team.News(a.getName(), since, a.started)
}

// teamPost sends text from member a to the team chat, redacted, within
// maxTeamPosts a minute: to every teammate, or only to to ("vps" or
// "vps/codex").
func (m *Manager) teamPost(a *agentState, to, text string) (string, error) {
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
	return team.Post(ctx, a.getName(), strings.TrimSpace(to), text)
}
