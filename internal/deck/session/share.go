package session

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/hub"
	"github.com/dev-dami/deck/internal/deck/store"
)

// Shared threads (docs/multiplayer-design.md §8.5). The person shares a
// thread with a teammate's Deck and a role:
//
//   - observer: reads the thread (a mirror that follows it live);
//   - participant: also posts to it;
//   - editor: also answers its approvals.
//
// A teammate's message enters the thread like the user's, through the same
// queue, with its name in front ("[vps] ..."). It cannot address agents,
// start a race or add an agent: "$" in it is not a mention. It does not end
// a pause or reset the brakes, and a thread in yolo mode goes to ask mode
// first: work a teammate starts runs in ask mode at most. A share approval
// (a code contract) never travels.

// The share roles.
const (
	ShareObserver    = "observer"
	ShareParticipant = "participant"
	ShareEditor      = "editor"
)

const (
	// mirrorEntries caps the log entries one mirror read covers, and
	// mirrorChars the text it returns.
	mirrorEntries = 2000
	mirrorChars   = 48 << 10
	// maxSharedPosts is how many times a minute one teammate may post to
	// one thread.
	maxSharedPosts = 6
	// maxSharedText caps one post from a teammate.
	maxSharedText = 4000
)

func validShareRole(r string) bool {
	return r == ShareObserver || r == ShareParticipant || r == ShareEditor
}

// shareRank orders the roles: what each one may do includes the one below.
func shareRank(r string) int {
	return map[string]int{ShareObserver: 1, ShareParticipant: 2, ShareEditor: 3}[r]
}

// SetShare shares the thread with teammate peer in role, or stops sharing
// it with peer when role is "".
func (m *Manager) SetShare(threadID, peer, role string) error {
	t := m.thread(threadID)
	if t == nil {
		return fmt.Errorf("session: no thread %q", threadID)
	}
	if !config.ValidPeerName(peer) {
		return fmt.Errorf("%q is not a teammate's name", peer)
	}
	if role != "" && !validShareRole(role) {
		return fmt.Errorf("unknown role %q: observer, participant or editor", role)
	}
	t.mu.Lock()
	if role == "" {
		delete(t.shares, peer)
	} else {
		if t.shares == nil {
			t.shares = map[string]string{}
		}
		t.shares[peer] = role
	}
	t.mu.Unlock()
	m.persistMeta(t)
	text := "stopped sharing this thread with " + peer
	if role != "" {
		text = "shared this thread with " + peer + " as " + role
	}
	m.emit(NoticeMsg{ThreadID: threadID, Text: text})
	return nil
}

// Shares are the teammates the thread is shared with, and their roles.
func (m *Manager) Shares(threadID string) map[string]string {
	t := m.thread(threadID)
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return maps.Clone(t.shares)
}

// SharedThread is a thread as a teammate it is shared with sees it.
type SharedThread struct {
	ID      string
	Title   string
	Role    string // the teammate's role
	Mode    string // the thread's permission mode
	Members []string
	// Entries is the length of the thread's log: a mirror read from here
	// has nothing new.
	Entries int
}

// SharedWith lists the threads shared with teammate peer.
func (m *Manager) SharedWith(peer string) []SharedThread {
	m.mu.Lock()
	ts := append([]*thread(nil), m.torder...)
	m.mu.Unlock()
	var out []SharedThread
	for _, t := range ts {
		if t.gone.Load() {
			continue
		}
		t.mu.Lock()
		if role := t.shares[peer]; role != "" {
			out = append(out, t.sharedLocked(role))
		}
		t.mu.Unlock()
	}
	return out
}

// sharedLocked is t as a teammate in role sees it listed. t.mu is held.
func (t *thread) sharedLocked(role string) SharedThread {
	st := SharedThread{ID: t.id, Title: t.title, Role: role, Mode: string(t.mode), Entries: t.size()}
	for _, a := range t.members {
		st.Members = append(st.Members, a.getName())
	}
	return st
}

// sharedThread is the thread threadID when it is shared with peer in at
// least role want, else an error that tells nothing of other threads.
func (m *Manager) sharedThread(peer, threadID, want string) (*thread, string, error) {
	t := m.thread(threadID)
	if t != nil && !t.gone.Load() {
		t.mu.Lock()
		role := t.shares[peer]
		t.mu.Unlock()
		switch {
		case role == "":
		case shareRank(role) < shareRank(want):
			return nil, "", fmt.Errorf("you are %s on this thread; that needs %s", role, want)
		default:
			return t, role, nil
		}
	}
	return nil, "", errors.New("no such shared thread")
}

// PendingApproval is an approval an agent of a shared thread waits for.
type PendingApproval struct {
	ID      string
	Agent   string
	Kind    string
	Summary string
}

// Mirror is part of a shared thread, as text.
type Mirror struct {
	Thread SharedThread
	// Lines are the thread's messages and what its agents did, from the
	// entry the read started at, redacted.
	Lines []string
	// Next is where the next read starts.
	Next int
	// Pending are the approvals the thread waits for: for an editor only.
	Pending []PendingApproval
}

// ReadShared is what thread threadID, shared with peer, holds from log
// entry after on. With wait set and nothing new, it first waits up to wait
// for the thread to grow.
func (m *Manager) ReadShared(ctx context.Context, peer, threadID string, after int, wait time.Duration) (Mirror, error) {
	t, role, err := m.sharedThread(peer, threadID, ShareObserver)
	if err != nil {
		return Mirror{}, err
	}
	if wait > 0 {
		t.mu.Lock()
		grew := t.grew
		if t.size() <= after {
			if grew == nil {
				grew = make(chan struct{})
				t.grew = grew
			}
		} else {
			grew = nil
		}
		t.mu.Unlock()
		if grew != nil {
			wctx, cancel := context.WithTimeout(ctx, wait)
			select {
			case <-grew:
			case <-wctx.Done():
			}
			cancel()
		}
	}
	t.mu.Lock()
	after = min(max(after, 0), t.size())
	end := min(t.size(), after+mirrorEntries)
	lines, _ := m.walk(t, viewer{includeOpen: true}, after, end, func(int, store.Entry) bool { return true })
	mr := Mirror{Thread: t.sharedLocked(role), Next: end}
	var members []*agentState
	if role == ShareEditor {
		members = append(members, t.members...)
	}
	t.mu.Unlock()
	n := 0
	for _, l := range lines {
		text := hub.Redact(l.text)
		if n += len(text); n > mirrorChars && len(mr.Lines) > 0 {
			mr.Lines = append(mr.Lines, "[... more of the thread was cut here ...]")
			break
		}
		mr.Lines = append(mr.Lines, text)
	}
	for _, a := range members {
		a.mu.Lock()
		for _, p := range a.pending {
			if p.kind != agent.ApproveShare {
				mr.Pending = append(mr.Pending, PendingApproval{ID: p.id, Agent: a.name, Kind: string(p.kind), Summary: hub.Redact(p.summary)})
			}
		}
		a.mu.Unlock()
	}
	return mr, nil
}

// PostShared posts text from teammate peer, a participant or editor, to the
// thread. It goes to whom a plain message of the user would go, through the
// same queue.
func (m *Manager) PostShared(peer, threadID, text string) error {
	t, _, err := m.sharedThread(peer, threadID, ShareParticipant)
	if err != nil {
		return err
	}
	text = strings.TrimSpace(hub.Redact(text))
	if text == "" {
		return errors.New("nothing to post")
	}
	if len(text) > maxSharedText {
		text = strings.ToValidUTF8(text[:maxSharedText], "") + "…"
	}
	// "$" from a teammate is never a mention: it cannot pick agents, add
	// one or start a race.
	text = "[" + peer + "] " + strings.ReplaceAll(text, "$", "＄")
	key, now := "share:"+peer, time.Now()
	t.mu.Lock()
	recent := t.sends[key][:0]
	for _, at := range t.sends[key] {
		if now.Sub(at) < time.Minute {
			recent = append(recent, at)
		}
	}
	if len(recent) >= maxSharedPosts {
		t.mu.Unlock()
		return fmt.Errorf("slow down: %d posts in the last minute", len(recent))
	}
	if t.sends == nil {
		t.sends = map[string][]time.Time{}
	}
	t.sends[key] = append(recent, now)
	yolo := t.mode == agent.ModeYolo
	target := t.defaultTarget()
	t.mu.Unlock()
	if target == nil {
		return errors.New("no agent in this thread is running")
	}
	if yolo {
		if err := m.SetMode(threadID, agent.ModeAsk); err != nil {
			return err
		}
		m.emit(NoticeMsg{ThreadID: threadID, Text: peer + " posted: the thread is in ask mode now, so work a teammate starts asks before each edit and command"})
	}
	m.wake(t)
	_, err = m.postFrom(t, peer, text, []*agentState{target}, nil, false)
	return err
}

// AnswerShared is teammate peer, an editor of the thread, answering one of
// its approvals. A share (a code contract) is never answered from afar.
func (m *Manager) AnswerShared(peer, threadID, approvalID string, allow bool) error {
	t, _, err := m.sharedThread(peer, threadID, ShareEditor)
	if err != nil {
		return err
	}
	t.mu.Lock()
	members := append([]*agentState(nil), t.members...)
	t.mu.Unlock()
	for _, a := range members {
		a.mu.Lock()
		var found *pendingApproval
		for i := range a.pending {
			if a.pending[i].id == approvalID {
				p := a.pending[i]
				found = &p
			}
		}
		a.mu.Unlock()
		if found == nil {
			continue
		}
		if found.kind == agent.ApproveShare {
			return errors.New("only this Deck's person answers that")
		}
		if !a.claimPending(approvalID) {
			return errors.New("it was answered already")
		}
		d := agent.Decision{Allow: allow}
		verb := "allowed"
		if !allow {
			d.Reason, verb = peer+" declined it", "declined"
		}
		found.respond(d)
		m.emit(ApprovalGoneMsg{ThreadID: t.id, AgentID: a.id, ID: approvalID})
		m.emit(NoticeMsg{ThreadID: t.id, Text: peer + " " + verb + ": " + found.summary})
		return nil
	}
	return errors.New("no such approval waits")
}
