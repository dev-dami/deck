package peer

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/hub"
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

// OpConsoleRead reads this Deck's team chat. It is not in
// DefaultAllowInbound: the chat holds what other teammates wrote to this
// Deck, so the person allows it per peer (allow_inbound).
const OpConsoleRead = "console.read"

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
	// chatTimeout bounds delivering chat to one teammate.
	chatTimeout = 15 * time.Second
	// maxReadMessages and maxReadBytes cap one console.read answer.
	maxReadMessages = 100
	maxReadBytes    = 32 << 10
)

var consoleOps = []OpInfo{
	{Name: OpConsolePost, Args: "{text*, id, agent}", Desc: "post a message to this Deck's team chat; id drops a repeat, agent names the agent that wrote it"},
	{Name: OpConsoleRead, Args: "{since}", Desc: "read this Deck's team chat: the newest messages after seq since, and the seq to pass next time"},
}

// ConsoleFeed is a team chat as console.read returns it.
type ConsoleFeed struct {
	// Seq is the newest seq of the feed: pass it as since next time.
	Seq      uint64        `json:"seq"`
	Messages []ChatMessage `json:"messages"`
}

// ChatMessage is one line of the team chat.
type ChatMessage struct {
	Seq  uint64 `json:"seq"`
	From string `json:"from"` // the Deck that wrote it
	// Agent is the agent of that Deck that wrote it; "" for its person.
	Agent string    `json:"agent,omitempty"`
	To    string    `json:"to,omitempty"`
	Text  string    `json:"text"`
	At    time.Time `json:"at"`
	Mine  bool      `json:"mine,omitempty"` // written on this Deck
	// ID is the sender's key for the message: a repeat of it is dropped.
	ID string `json:"id,omitempty"`
}

// Author is who wrote m: the Deck, and its agent after a slash
// ("vps/claude2").
func (m ChatMessage) Author() string {
	if m.Agent != "" {
		return m.From + "/" + m.Agent
	}
	return m.From
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

// AddChat records a chat message and keeps it on disk. A message whose ID
// is already kept from the same Deck is a repeat: it is dropped, and the
// kept one returned.
func (t *Team) AddChat(m ChatMessage) ChatMessage {
	m.Text = cleanChat(m.Text)
	if m.At.IsZero() {
		m.At = time.Now()
	}
	t.mu.Lock()
	for _, c := range t.chat {
		if m.ID != "" && c.ID == m.ID && c.From == m.From {
			t.mu.Unlock()
			return c
		}
	}
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

// ChatAfter returns the kept chat after seq since, oldest first, and the
// newest seq. A since past the newest seq is from before this Deck
// restarted, so all the kept chat comes back.
func (t *Team) ChatAfter(since uint64) ([]ChatMessage, uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if since > t.seq {
		since = 0
	}
	var out []ChatMessage
	for _, m := range t.chat {
		if m.Seq > since {
			out = append(out, m)
		}
	}
	return out, t.seq
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

// TeamHandler is Base plus console.post, recording into Team, and
// console.read, reading from it.
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
	if op == OpConsoleRead {
		var a struct {
			Since uint64 `json:"since"`
		}
		_ = json.Unmarshal(args, &a)
		msgs, seq := h.Team.ChatAfter(a.Since)
		b, err := json.Marshal(ConsoleFeed{Seq: seq, Messages: newestFit(msgs)})
		return string(b), err
	}
	if op != OpConsolePost {
		return h.Base.Execute(ctx, from, op, args)
	}
	var a struct {
		Text  string `json:"text"`
		ID    string `json:"id"`
		Agent string `json:"agent"`
	}
	if json.Unmarshal(args, &a) != nil || strings.TrimSpace(a.Text) == "" {
		return "", errors.New("console.post needs text")
	}
	if !config.ValidPeerName(a.Agent) {
		a.Agent = "" // a label only; one that is not a plain name is dropped
	}
	h.Team.AddChat(ChatMessage{From: from.Name, Agent: a.Agent, Text: a.Text, ID: truncate(a.ID, 64)})
	return "posted", nil
}

// newestFit keeps the newest of msgs that fit maxReadMessages and
// maxReadBytes, oldest first, with credentials redacted. It drops the fields
// only this Deck needs (To names its other teammates).
func newestFit(msgs []ChatMessage) []ChatMessage {
	var out []ChatMessage
	size := 0
	for i := len(msgs) - 1; i >= 0 && len(out) < maxReadMessages; i-- {
		m := msgs[i]
		m.Text = hub.Redact(m.Text)
		if size += len(m.Text) + len(m.From) + len(m.Agent) + 64; size > maxReadBytes {
			break
		}
		out = append(out, ChatMessage{Seq: m.Seq, From: m.From, Agent: m.Agent, Text: m.Text, At: m.At})
	}
	slices.Reverse(out)
	return out
}

// ReadConsole reads the team chat of teammate name after seq since. Pass
// the feed's Seq as since next time. That Deck must list console.read in
// allow_inbound for this one.
func (s *Service) ReadConsole(ctx context.Context, name string, since uint64) (ConsoleFeed, error) {
	var f ConsoleFeed
	err := s.shareCall(ctx, name, OpConsoleRead, map[string]any{"since": since}, &f)
	if len(f.Messages) > maxReadMessages {
		f.Messages = f.Messages[len(f.Messages)-maxReadMessages:]
	}
	for i, m := range f.Messages {
		f.Messages[i] = ChatMessage{Seq: m.Seq, From: cleanLine(m.From, 32), Agent: cleanLine(m.Agent, 32), Text: hub.Redact(cleanChat(m.Text)), At: m.At}
	}
	return f, err
}

// SendChat posts text from this Deck's person to the team chat of every
// paired teammate, and records it here. A teammate that cannot be reached
// gets it from the outbox once its link is back. It returns the teammates
// it reached; QueuedError names the ones it waits for, another error the
// ones that refused it.
func (s *Service) SendChat(ctx context.Context, text string) ([]string, error) {
	return s.PostChat(ctx, "", text)
}

// PostChat is SendChat for agent, an agent of this Deck ("" for its person).
func (s *Service) PostChat(ctx context.Context, agent, text string) ([]string, error) {
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
	id := newCallKey()
	args := map[string]any{"text": text, "id": id}
	if agent != "" {
		args["agent"] = agent
	}
	var reached, refused, queued []string
	for _, pin := range pins {
		var answered bool
		cctx, cancel := context.WithTimeout(ctx, chatTimeout)
		err := s.withPeer(cctx, pin.Name, func(cl *Client) error {
			r, err := cl.Execute(cctx, OpConsolePost, args)
			if err != nil {
				return err
			}
			answered = true
			if r.IsError {
				return errors.New(Sanitize(truncate(r.Text(), 200)))
			}
			return nil
		})
		cancel()
		switch {
		case err == nil:
			reached = append(reached, pin.Name)
		case answered:
			refused = append(refused, pin.Name+" ("+Sanitize(truncate(err.Error(), 120))+")")
		default:
			s.queue(pin.Name, OpConsolePost, id, args)
			queued = append(queued, pin.Name)
		}
	}
	if len(reached)+len(queued) > 0 {
		s.team.AddChat(ChatMessage{From: self, Agent: agent, To: strings.Join(append(reached, queued...), ", "), Text: text, Mine: true, ID: id})
	}
	switch {
	case len(refused) > 0:
		return reached, fmt.Errorf("refused by %s", strings.Join(refused, ", "))
	case len(queued) > 0:
		return reached, QueuedError{Peers: queued}
	}
	return reached, nil
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
