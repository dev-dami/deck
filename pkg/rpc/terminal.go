package rpc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

const (
	// maxTerminals bounds the shells one Deck keeps open.
	maxTerminals = 16
	// scrollback is how much of a terminal's output it keeps for a
	// subscriber that comes (back) later.
	scrollback = 256 << 10
	// readChunk is the most one TerminalChunk carries.
	readChunk = 32 << 10
	// subscriberBuffer is how many chunks a slow subscriber may fall behind
	// before it is ended; it then subscribes again and gets the scrollback.
	subscriberBuffer = 256
)

// pty is a started shell on a pseudo-terminal (terminal_unix.go,
// terminal_windows.go).
type pty interface {
	io.ReadWriter
	Resize(cols, rows uint16) error
	// Wait waits for the shell to end and returns its exit code.
	Wait() int
	// Close kills the shell and closes the terminal.
	Close() error
}

// terminal is one shell and everything it printed that a subscriber may
// still need.
type terminal struct {
	p pty

	mu     sync.Mutex
	buf    []byte
	subs   map[chan []byte]struct{}
	exited bool
	code   int
	done   chan struct{}
}

func (t *terminal) pump() {
	b := make([]byte, readChunk)
	for {
		n, err := t.p.Read(b)
		if n > 0 {
			chunk := append([]byte(nil), b[:n]...)
			t.mu.Lock()
			t.buf = append(t.buf, chunk...)
			if over := len(t.buf) - scrollback; over > 0 {
				t.buf = append([]byte(nil), t.buf[over:]...)
			}
			for c := range t.subs {
				select {
				case c <- chunk:
				default:
					// Too far behind: end it; it resubscribes from the scrollback.
					delete(t.subs, c)
					close(c)
				}
			}
			t.mu.Unlock()
		}
		if err != nil {
			break
		}
	}
	code := t.p.Wait()
	t.mu.Lock()
	t.exited, t.code = true, code
	// done first: a subscriber whose channel closes then knows the shell
	// ended rather than that it fell behind.
	close(t.done)
	for c := range t.subs {
		close(c)
	}
	t.subs = map[chan []byte]struct{}{}
	t.mu.Unlock()
}

// subscribe returns the scrollback and a channel of what comes after it;
// the channel closes when the shell ends or the subscriber falls behind.
func (t *terminal) subscribe() ([]byte, chan []byte, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	back := append([]byte(nil), t.buf...)
	if t.exited {
		return back, nil, true
	}
	c := make(chan []byte, subscriberBuffer)
	t.subs[c] = struct{}{}
	return back, c, false
}

func (t *terminal) unsubscribe(c chan []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.subs[c]; ok {
		delete(t.subs, c)
		close(c)
	}
}

// terminals are the shells of one Deck, in its project folder.
type terminals struct {
	dir   string
	start func(dir string, cols, rows uint16) (pty, error)

	mu   sync.Mutex
	open map[string]*terminal
}

func newTerminals(dir string) *terminals {
	return &terminals{dir: dir, start: startPTY, open: map[string]*terminal{}}
}

func (ts *terminals) get(id string) (*terminal, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	t, ok := ts.open[id]
	if !ok {
		return nil, status.Error(codes.NotFound, "no such terminal")
	}
	return t, nil
}

func (ts *terminals) closeAll() {
	ts.mu.Lock()
	all := ts.open
	ts.open = map[string]*terminal{}
	ts.mu.Unlock()
	for _, t := range all {
		_ = t.p.Close()
	}
}

// size clamps a requested size to what a terminal can be.
func size(cols, rows int32) (uint16, uint16) {
	clamp := func(v int32, lo, hi int32) uint16 {
		return uint16(min(max(v, lo), hi))
	}
	return clamp(cols, 2, 1000), clamp(rows, 1, 500)
}

func (s *Server) needTerminals() error {
	if s.terms == nil {
		return status.Error(codes.FailedPrecondition, "this Deck has no project folder")
	}
	return nil
}

func (s *Server) TerminalOpen(_ context.Context, r *deckv1.TerminalOpenRequest) (*deckv1.TerminalRef, error) {
	if err := s.needTerminals(); err != nil {
		return nil, err
	}
	ts := s.terms
	ts.mu.Lock()
	n := len(ts.open)
	ts.mu.Unlock()
	if n >= maxTerminals {
		return nil, status.Error(codes.ResourceExhausted, "too many terminals are open")
	}
	cols, rows := size(r.GetCols(), r.GetRows())
	p, err := ts.start(ts.dir, cols, rows)
	if err != nil {
		if errors.Is(err, errNoPTY) {
			return nil, status.Error(codes.Unimplemented, err.Error())
		}
		return nil, status.Error(codes.Internal, "could not start a shell: "+err.Error())
	}
	var id [8]byte
	_, _ = rand.Read(id[:])
	t := &terminal{p: p, subs: map[chan []byte]struct{}{}, done: make(chan struct{})}
	key := "term_" + hex.EncodeToString(id[:])
	ts.mu.Lock()
	ts.open[key] = t
	ts.mu.Unlock()
	go t.pump()
	return &deckv1.TerminalRef{TerminalId: key}, nil
}

func (s *Server) TerminalWrite(_ context.Context, r *deckv1.TerminalInput) (*deckv1.Empty, error) {
	if err := s.needTerminals(); err != nil {
		return nil, err
	}
	t, err := s.terms.get(r.GetTerminalId())
	if err != nil {
		return nil, err
	}
	if _, err := t.p.Write(r.GetData()); err != nil {
		return nil, status.Error(codes.FailedPrecondition, "the terminal has ended")
	}
	return &deckv1.Empty{}, nil
}

func (s *Server) TerminalResize(_ context.Context, r *deckv1.TerminalSize) (*deckv1.Empty, error) {
	if err := s.needTerminals(); err != nil {
		return nil, err
	}
	t, err := s.terms.get(r.GetTerminalId())
	if err != nil {
		return nil, err
	}
	cols, rows := size(r.GetCols(), r.GetRows())
	_ = t.p.Resize(cols, rows)
	return &deckv1.Empty{}, nil
}

func (s *Server) TerminalClose(_ context.Context, r *deckv1.TerminalRef) (*deckv1.Empty, error) {
	if err := s.needTerminals(); err != nil {
		return nil, err
	}
	ts := s.terms
	ts.mu.Lock()
	t, ok := ts.open[r.GetTerminalId()]
	delete(ts.open, r.GetTerminalId())
	ts.mu.Unlock()
	if ok {
		_ = t.p.Close()
	}
	return &deckv1.Empty{}, nil
}

// TerminalOutput sends the scrollback, then what the shell prints, then a
// last chunk with exited set when the shell ends.
func (s *Server) TerminalOutput(r *deckv1.TerminalRef, stream deckv1.Deck_TerminalOutputServer) error {
	if err := s.needTerminals(); err != nil {
		return err
	}
	t, err := s.terms.get(r.GetTerminalId())
	if err != nil {
		return err
	}
	back, c, exited := t.subscribe()
	if len(back) > 0 {
		if err := stream.Send(&deckv1.TerminalChunk{Data: back}); err != nil {
			if c != nil {
				t.unsubscribe(c)
			}
			return err
		}
	}
	if !exited {
		if err := follow(t, c, stream); err != nil {
			return err
		}
	}
	t.mu.Lock()
	code := t.code
	t.mu.Unlock()
	return stream.Send(&deckv1.TerminalChunk{Exited: true, ExitCode: int32(code)})
}

// follow sends what the shell prints until it ends (nil) or the stream
// does (its error).
func follow(t *terminal, c chan []byte, stream deckv1.Deck_TerminalOutputServer) error {
	defer t.unsubscribe(c)
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case chunk, ok := <-c:
			if !ok {
				select {
				case <-t.done:
					return nil
				default:
					// Fell behind: the client subscribes again.
					return status.Error(codes.ResourceExhausted, "the terminal stream fell behind")
				}
			}
			if err := stream.Send(&deckv1.TerminalChunk{Data: chunk}); err != nil {
				return err
			}
		}
	}
}
