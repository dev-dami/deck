// Package browser is the engine's side of the shared browser: the channel on
// which agents' browser.* ops reach the desktop app.
//
// The browser lives in the desktop app (one Chromium, every agent's tabs);
// the engine is another process. A window that hosts the browser attaches
// (Attach) and receives a [Request] for each op; it runs the op and answers
// with a [Result] (Complete). [Broker.Do] is the engine's side: it hands one
// request to the host window and waits, so an op is a plain call that returns
// text. Without a host window the call fails with [ErrNoBrowser].
//
// The channel is the local desktop's gRPC (pkg/rpc). Nothing else attaches:
// a paired or remote Deck's agents cannot reach this machine's browser.
package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Limits.
const (
	// MaxInFlight is the most requests waiting for the browser at once, and
	// MaxPerAgent the most one agent has: a loop of calls cannot queue up
	// work the browser would run for minutes.
	MaxInFlight = 32
	MaxPerAgent = 4
	// DefaultTimeout bounds a request that names none; MaxTimeout is the
	// longest it may name.
	DefaultTimeout = 30 * time.Second
	MaxTimeout     = 50 * time.Second
	// hostQueue is how many requests may wait for a host that is busy
	// reading its stream.
	hostQueue = 64
)

// ErrNoBrowser is the answer when no window hosts the browser.
var ErrNoBrowser = errors.New("no browser attached: open Deck's desktop app (it hosts the browser) and keep its window open")

// ErrBusy is the answer when too many requests wait for the browser.
var ErrBusy = errors.New("the browser has too many requests waiting; wait for your earlier calls to finish")

// Request is one op for the browser.
type Request struct {
	ID string
	// Agent is Deck's id for the calling agent ("claude2"), Name its name in
	// the thread ("claude") and Kind its CLI (claude, codex, ...).
	Agent, Name, Kind string
	// Op is the op without its "browser." prefix: open, snapshot, click, ...
	Op   string
	Args json.RawMessage
	// Free says the thread is in yolo mode: any http(s) page may load.
	// Otherwise a tab of the agent may only go where the person has said it
	// may: Allowed are the origins approved so far, and this machine's own
	// pages need no word.
	Free    bool
	Allowed []string
	// Timeout is how long the browser has to answer.
	Timeout time.Duration
}

// Result is the browser's answer to a [Request].
type Result struct {
	ID      string
	Text    string
	IsError bool
	// Image is a screenshot (Mime says its type), when the op took one.
	Image []byte
	Mime  string
}

// host is a window attached to the broker.
type host struct {
	id   string
	host bool // the window the person marked as the browser's
	seq  uint64
	ch   chan Request
}

type pending struct {
	host  *host
	agent string
	done  chan Result
}

// Broker carries ops from agents to the window that hosts the browser.
type Broker struct {
	mu      sync.Mutex
	hosts   []*host
	seq     uint64
	next    uint64
	waiting map[string]*pending
}

// NewBroker returns a broker with no window attached.
func NewBroker() *Broker { return &Broker{waiting: map[string]*pending{}} }

// Attach registers a window and returns the channel its requests arrive on
// and a detach func. isHost says the window is the browser's host; of
// several hosts the newest receives the requests, and a window that is not
// a host receives none. Detaching fails the requests the window had not
// answered.
func (b *Broker) Attach(id string, isHost bool) (<-chan Request, func()) {
	b.mu.Lock()
	b.seq++
	h := &host{id: id, host: isHost, seq: b.seq, ch: make(chan Request, hostQueue)}
	b.hosts = append(b.hosts, h)
	b.mu.Unlock()
	var once sync.Once
	return h.ch, func() {
		once.Do(func() {
			b.mu.Lock()
			for i, o := range b.hosts {
				if o == h {
					b.hosts = append(b.hosts[:i], b.hosts[i+1:]...)
					break
				}
			}
			var gone []*pending
			for rid, p := range b.waiting {
				if p.host == h {
					gone = append(gone, p)
					delete(b.waiting, rid)
				}
			}
			b.mu.Unlock()
			for _, p := range gone {
				p.done <- Result{IsError: true, Text: "the browser window closed before it answered"}
			}
		})
	}
}

// Hosted reports whether a window hosts the browser, and how many windows
// are attached in all.
func (b *Broker) Hosted() (hosted bool, attached int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.hostLocked() != nil, len(b.hosts)
}

// hostLocked is the window that receives requests: the newest host.
func (b *Broker) hostLocked() *host {
	var best *host
	for _, h := range b.hosts {
		if h.host && (best == nil || h.seq > best.seq) {
			best = h
		}
	}
	return best
}

// Do sends r to the host window and waits for its answer. The request gets
// an id here. It fails with ErrNoBrowser when no window hosts the browser,
// ErrBusy when too many requests wait, and with ctx's error or a timeout
// when the browser does not answer in time.
func (b *Broker) Do(ctx context.Context, r Request) (Result, error) {
	if r.Timeout <= 0 {
		r.Timeout = DefaultTimeout
	}
	r.Timeout = min(r.Timeout, MaxTimeout)
	b.mu.Lock()
	h := b.hostLocked()
	if h == nil {
		b.mu.Unlock()
		return Result{}, ErrNoBrowser
	}
	n := 0
	for _, p := range b.waiting {
		if p.agent == r.Agent {
			n++
		}
	}
	if len(b.waiting) >= MaxInFlight || n >= MaxPerAgent {
		b.mu.Unlock()
		return Result{}, ErrBusy
	}
	b.next++
	r.ID = fmt.Sprintf("b%d", b.next)
	p := &pending{host: h, agent: r.Agent, done: make(chan Result, 1)}
	b.waiting[r.ID] = p
	b.mu.Unlock()
	drop := func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		_, ok := b.waiting[r.ID]
		delete(b.waiting, r.ID)
		return ok
	}
	select {
	case h.ch <- r:
	default:
		drop()
		return Result{}, ErrBusy
	}
	timer := time.NewTimer(r.Timeout)
	defer timer.Stop()
	select {
	case res := <-p.done:
		res.ID = r.ID
		return res, nil
	case <-timer.C:
		if !drop() {
			return <-p.done, nil // answered just as time ran out
		}
		return Result{}, fmt.Errorf("the browser did not answer %s in %s", r.Op, r.Timeout.Round(time.Second))
	case <-ctx.Done():
		if !drop() {
			return <-p.done, nil
		}
		return Result{}, ctx.Err()
	}
}

// Complete delivers the answer of window hostID to the request it names. It
// reports false when the request is unknown, already answered or was sent to
// another window: only the window that got a request may answer it.
func (b *Broker) Complete(hostID string, r Result) bool {
	b.mu.Lock()
	p, ok := b.waiting[r.ID]
	if !ok || p.host.id != hostID {
		b.mu.Unlock()
		return false
	}
	delete(b.waiting, r.ID)
	b.mu.Unlock()
	p.done <- r
	return true
}

// Origin is the scheme://host[:port] of an http(s) URL, lower-cased.
func Origin(u *url.URL) string {
	return u.Scheme + "://" + strings.ToLower(u.Host)
}

// CheckURL parses raw as a page an agent may ask for: an http or https URL
// with a host and no credentials. file:, chrome:, devtools:, data:,
// javascript:, about: and every other scheme are refused, as are addresses
// that carry a user name or password. A bare "example.com/x" is taken as
// https.
func CheckURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("url is empty")
	}
	if len(raw) > 2048 {
		return nil, errors.New("url is too long")
	}
	if !strings.Contains(raw, ":") || bareHostPort(raw) {
		scheme := "https://"
		if h := strings.ToLower(raw); strings.HasPrefix(h, "localhost") || strings.HasPrefix(h, "127.") {
			scheme = "http://" // a dev server
		}
		raw = scheme + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("url is not valid")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("only http and https pages can be opened, not %q", u.Scheme+":")
	}
	if u.Hostname() == "" {
		return nil, errors.New("url has no host")
	}
	if u.User != nil {
		return nil, errors.New("url must not carry a user name or password")
	}
	return u, nil
}

// bareHostPort reports whether s looks like "host:port/path" (localhost:3000)
// rather than "scheme:rest".
func bareHostPort(s string) bool {
	head, _, _ := strings.Cut(s, "/")
	host, port, ok := strings.Cut(head, ":")
	if !ok || host == "" || port == "" || strings.Contains(s, "://") {
		return false
	}
	for _, c := range port {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// Loopback reports whether u is a page of this computer: localhost,
// 127.0.0.0/8 or ::1. Such a page is a dev server the agent could curl as
// well, so a tab may open it without asking.
func Loopback(u *url.URL) bool {
	h := strings.ToLower(u.Hostname())
	return h == "localhost" || h == "::1" || strings.HasPrefix(h, "127.")
}
