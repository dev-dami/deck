package peer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Shared threads (docs/multiplayer-design.md §8.5): a Deck's person shares
// a thread with a teammate and a role. The teammate finds it in peer.status
// (Presence.Threads), follows it with thread.mirror, a read that waits up to
// mirrorWait for something new, and, as the role allows, posts to it
// (thread.post) and answers its approvals (thread.answer). The serving Deck
// checks the role at every call; a thread not shared with the caller is as
// good as absent.

const (
	OpThreadMirror = "thread.mirror"
	OpThreadPost   = "thread.post"
	OpThreadAnswer = "thread.answer"
)

const (
	// mirrorWait is the longest one thread.mirror waits for news.
	mirrorWait = 20 * time.Second
	// shareCallTimeout bounds a call about a shared thread, the wait
	// included.
	shareCallTimeout = mirrorWait + 15*time.Second
	// maxMirrorLines caps the lines a mirror read may bring.
	maxMirrorLines = 2000
	// maxPostKeys is how many post keys are kept to drop repeats.
	maxPostKeys = 256
)

var shareOps = []OpInfo{
	{Name: OpThreadMirror, Args: "{thread*, after, wait}", Desc: "read a thread shared with you, from log entry after on; wait (seconds, at most 20) waits for something new"},
	{Name: OpThreadPost, Args: "{thread*, text*, id}", Desc: "post to a thread shared with you as participant or editor"},
	{Name: OpThreadAnswer, Args: "{thread*, approval*, allow}", Desc: "answer an approval of a thread shared with you as editor"},
}

// SharedThread is a thread a teammate shares with this Deck.
type SharedThread struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Role    string   `json:"role"` // observer, participant or editor
	Mode    string   `json:"mode"`
	Members []string `json:"members,omitempty"`
	Entries int      `json:"entries"`
}

// PendingApproval is an approval a shared thread waits for.
type PendingApproval struct {
	ID      string `json:"id"`
	Agent   string `json:"agent"`
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
}

// Mirror is part of a shared thread, as text.
type Mirror struct {
	Thread  SharedThread      `json:"thread"`
	Lines   []string          `json:"lines"`
	Next    int               `json:"next"`
	Pending []PendingApproval `json:"pending,omitempty"`
}

// clean makes a teammate's mirror safe to show: control characters gone,
// sizes capped.
func (mr Mirror) clean() Mirror {
	mr.Thread = mr.Thread.clean()
	if len(mr.Lines) > maxMirrorLines {
		mr.Lines = mr.Lines[len(mr.Lines)-maxMirrorLines:]
	}
	for i, l := range mr.Lines {
		mr.Lines[i] = cleanText(l, maxChatText*2)
	}
	if len(mr.Pending) > maxPresenceItems {
		mr.Pending = mr.Pending[:maxPresenceItems]
	}
	for i, p := range mr.Pending {
		mr.Pending[i] = PendingApproval{ID: cleanLine(p.ID, 64), Agent: cleanLine(p.Agent, 40), Kind: cleanLine(p.Kind, 20), Summary: cleanLine(p.Summary, 300)}
	}
	return mr
}

func (st SharedThread) clean() SharedThread {
	st.ID, st.Title, st.Role, st.Mode = cleanLine(st.ID, 64), cleanLine(st.Title, 120), cleanLine(st.Role, 20), cleanLine(st.Mode, 10)
	if len(st.Members) > maxPresenceItems {
		st.Members = st.Members[:maxPresenceItems]
	}
	for i, m := range st.Members {
		st.Members[i] = cleanLine(m, 40)
	}
	return st
}

// Shared serves this Deck's shared threads to teammates (the engine, from
// its threads). Each method checks peer's role on the thread.
type Shared interface {
	List(peer string) []SharedThread
	Read(ctx context.Context, peer, thread string, after int, wait time.Duration) (Mirror, error)
	Post(peer, thread, text string) error
	Answer(peer, thread, approval string, allow bool) error
}

// ShareHandler is Base plus the shared-thread ops.
type ShareHandler struct {
	Base   Handler
	Shared Shared

	mu   sync.Mutex
	keys []string // the latest post keys, "peer/key", to drop repeats
}

// Search implements Handler.
func (h *ShareHandler) Search(ctx context.Context, from Caller, query string) ([]OpInfo, error) {
	ops, err := h.Base.Search(ctx, from, query)
	if err != nil {
		return nil, err
	}
	return append(ops, matchOps(shareOps, query)...), nil
}

// Execute implements Handler.
func (h *ShareHandler) Execute(ctx context.Context, from Caller, op string, args json.RawMessage) (string, error) {
	if op != OpThreadMirror && op != OpThreadPost && op != OpThreadAnswer {
		return h.Base.Execute(ctx, from, op, args)
	}
	var a struct {
		Thread   string `json:"thread"`
		After    int    `json:"after"`
		Wait     int    `json:"wait"`
		Text     string `json:"text"`
		ID       string `json:"id"`
		Approval string `json:"approval"`
		Allow    bool   `json:"allow"`
	}
	if json.Unmarshal(args, &a) != nil || a.Thread == "" || len(a.Thread) > 64 {
		return "", fmt.Errorf("%w: no such shared thread", ErrUnknownOp)
	}
	switch op {
	case OpThreadMirror:
		wait := min(time.Duration(max(a.Wait, 0))*time.Second, mirrorWait)
		mr, err := h.Shared.Read(ctx, from.Name, a.Thread, a.After, wait)
		if err != nil {
			return "", err
		}
		b, err := json.Marshal(mr)
		return string(b), err
	case OpThreadPost:
		if a.ID != "" && h.repeat(from.Name+"/"+truncate(a.ID, 64)) {
			return "posted", nil
		}
		if err := h.Shared.Post(from.Name, a.Thread, cleanChat(a.Text)); err != nil {
			return "", err
		}
		return "posted", nil
	default:
		if err := h.Shared.Answer(from.Name, a.Thread, truncate(a.Approval, 64), a.Allow); err != nil {
			return "", err
		}
		return "answered", nil
	}
}

// repeat reports whether key was seen already, and remembers it.
func (h *ShareHandler) repeat(key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, k := range h.keys {
		if k == key {
			return true
		}
	}
	h.keys = append(h.keys, key)
	if len(h.keys) > maxPostKeys {
		h.keys = h.keys[1:]
	}
	return false
}

// shareCall runs op on the pinned peer name and decodes its answer into out
// (when not nil). A refusal comes back as the peer's words.
func (s *Service) shareCall(ctx context.Context, name, op string, args map[string]any, out any) error {
	ctx, cancel := context.WithTimeout(ctx, shareCallTimeout)
	defer cancel()
	return s.withPeer(ctx, name, func(cl *Client) error {
		r, err := cl.Execute(ctx, op, args)
		if err != nil {
			return err
		}
		if r.IsError {
			return errors.New(Sanitize(truncate(r.Text(), 200)))
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal([]byte(r.Text()), out)
	})
}

// ReadShared reads thread, which teammate name shares with this Deck, from
// log entry after on; wait is how long it may wait for something new.
func (s *Service) ReadShared(ctx context.Context, name, thread string, after int, wait time.Duration) (Mirror, error) {
	var mr Mirror
	err := s.shareCall(ctx, name, OpThreadMirror, map[string]any{"thread": thread, "after": after, "wait": int(wait / time.Second)}, &mr)
	return mr.clean(), err
}

// PostShared posts text from this Deck's person to thread, which teammate
// name shares with it as participant or editor.
func (s *Service) PostShared(ctx context.Context, name, thread, text string) error {
	if text = cleanChat(text); text == "" {
		return errors.New("nothing to send")
	}
	return s.shareCall(ctx, name, OpThreadPost, map[string]any{"thread": thread, "text": text, "id": newCallKey()}, nil)
}

// AnswerShared answers approval of thread, which teammate name shares with
// this Deck as editor.
func (s *Service) AnswerShared(ctx context.Context, name, thread, approval string, allow bool) error {
	return s.shareCall(ctx, name, OpThreadAnswer, map[string]any{"thread": thread, "approval": approval, "allow": allow}, nil)
}
