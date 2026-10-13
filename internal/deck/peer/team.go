package peer

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/dev-dami/deck/internal/deck/sub"
	"github.com/dev-dami/deck/internal/version"
)

// The team console of docs/peers-design.md section 5, first cut: a chat
// room shared by the paired Decks (console.post), and a record of the
// capsules teammates asked this Deck for, so the person can share a private
// one when asked. Both live in Team and reach status views through the
// Service's Snapshot, which every change bumps.

// OpConsolePost posts a line to this Deck's team chat.
const OpConsolePost = "console.post"

const (
	// maxChatText bounds one chat message, in bytes.
	maxChatText = 4000
	// maxChat is how many chat messages are kept (and replayed from disk).
	maxChat = 200
	// maxRequests is how many capsule requests are kept.
	maxRequests = 50
	// requestRepeat folds a teammate asking for the same capsule again
	// within this window into the first request.
	requestRepeat = time.Minute
)

var consoleOps = []OpInfo{
	{Name: OpConsolePost, Args: "{text*}", Desc: "post a message to this Deck's team chat"},
}

// ChatMessage is one line of the team chat.
type ChatMessage struct {
	Seq  uint64    `json:"seq"`
	From string    `json:"from"` // the Deck that wrote it
	To   string    `json:"to,omitempty"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
	Mine bool      `json:"mine,omitempty"` // written on this Deck
}

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

// Team holds the team chat and the capsule requests.
type Team struct {
	path     string
	onChange func()

	mu   sync.Mutex
	seq  uint64
	chat []ChatMessage
	reqs []CapsuleRequest
}

// NewTeam keeps the chat in dir/chat.jsonl ("" for memory only) and calls
// onChange (which must not block) after every change.
func NewTeam(dir string, onChange func()) *Team {
	t := &Team{onChange: onChange}
	if dir != "" {
		t.path = filepath.Join(dir, "chat.jsonl")
		t.load()
	}
	return t
}

func (t *Team) load() {
	f, err := os.Open(t.path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 64<<10)
	for sc.Scan() {
		var m ChatMessage
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		t.seq++
		m.Seq = t.seq
		t.chat = append(t.chat, m)
	}
	if len(t.chat) > maxChat {
		t.chat = t.chat[len(t.chat)-maxChat:]
	}
}

func (t *Team) changed() {
	if t.onChange != nil {
		t.onChange()
	}
}

// AddChat records a chat message and keeps it on disk.
func (t *Team) AddChat(m ChatMessage) ChatMessage {
	m.Text = cleanChat(m.Text)
	if m.At.IsZero() {
		m.At = time.Now()
	}
	t.mu.Lock()
	t.seq++
	m.Seq = t.seq
	t.chat = append(t.chat, m)
	if len(t.chat) > maxChat {
		t.chat = t.chat[len(t.chat)-maxChat:]
	}
	path := t.path
	t.mu.Unlock()
	if path != "" {
		if b, err := json.Marshal(m); err == nil {
			_ = os.MkdirAll(filepath.Dir(path), 0o700)
			if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
				_, _ = f.Write(append(b, '\n'))
				_ = f.Close()
			}
		}
	}
	t.changed()
	return m
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

// Chat is the kept chat, oldest first.
func (t *Team) Chat() []ChatMessage {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]ChatMessage(nil), t.chat...)
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

// TeamHandler is Base plus console.post, recording into Team.
type TeamHandler struct {
	Base Handler
	Team *Team
}

// Search implements Handler.
func (h *TeamHandler) Search(ctx context.Context, from Caller, query string) ([]OpInfo, error) {
	ops, err := h.Base.Search(ctx, from, query)
	if err != nil {
		return nil, err
	}
	return append(ops, matchOps(consoleOps, query)...), nil
}

// Execute implements Handler.
func (h *TeamHandler) Execute(ctx context.Context, from Caller, op string, args json.RawMessage) (string, error) {
	if op != OpConsolePost {
		return h.Base.Execute(ctx, from, op, args)
	}
	var a struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(args, &a) != nil || strings.TrimSpace(a.Text) == "" {
		return "", errors.New("console.post needs text")
	}
	h.Team.AddChat(ChatMessage{From: from.Name, Text: a.Text})
	return "posted", nil
}

// SendChat posts text to the team chat of every paired teammate this Deck
// can dial, and records it here. It returns the teammates it reached and an
// error naming the ones it could not.
func (s *Service) SendChat(ctx context.Context, text string) ([]string, error) {
	text = cleanChat(text)
	if text == "" {
		return nil, errors.New("nothing to send")
	}
	pins, err := OpenStore(s.dir).List()
	if err != nil {
		return nil, err
	}
	if len(pins) == 0 {
		return nil, errors.New("no teammate is paired yet: pair one in Settings › Team")
	}
	p, _ := s.loadPeers()
	self := SelfName(p)
	var reached, failed []string
	for _, pin := range pins {
		err := callPeer(ctx, s.dir, pin.Name, self, func(cl *Client) error {
			r, err := cl.Execute(ctx, OpConsolePost, map[string]any{"text": text})
			if err != nil {
				return err
			}
			if r.IsError {
				return errors.New(Sanitize(truncate(r.Text(), 200)))
			}
			return nil
		})
		if err != nil {
			failed = append(failed, pin.Name+" ("+Sanitize(truncate(err.Error(), 120))+")")
			continue
		}
		reached = append(reached, pin.Name)
	}
	if len(reached) > 0 {
		s.team.AddChat(ChatMessage{From: self, To: strings.Join(reached, ", "), Text: text, Mine: true})
	}
	if len(failed) > 0 {
		return reached, fmt.Errorf("could not reach %s", strings.Join(failed, ", "))
	}
	return reached, nil
}

// Team is the team chat and the capsule requests.
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
