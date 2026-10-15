// Package backend runs one `deck serve --rpc` per open project and keeps a
// gRPC connection and an event stream to it. The CLI is the engine; this
// is only its lifecycle.
package backend

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"google.golang.org/grpc"

	"github.com/dev-dami/deck/pkg/rpc"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// handshakeTimeout bounds how long the engine may take to start.
const handshakeTimeout = 60 * time.Second

// Project is one running engine.
type Project struct {
	Dir    string
	Info   *deckv1.InfoResponse
	Client deckv1.DeckClient

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	conn   *grpc.ClientConn
	cancel context.CancelFunc
	done   chan struct{}
}

// EventFunc receives every event of a project, Ready included.
type EventFunc func(p *Project, ev *deckv1.Event)

// FindDeck returns the deck binary: the one bundled next to the app (or in
// its bin folder) first, then the one on PATH.
func FindDeck() (string, error) {
	name := "deck"
	if runtime.GOOS == "windows" {
		name = "deck.exe"
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, p := range []string{filepath.Join(dir, "bin", name), filepath.Join(dir, name)} {
			if p == exe {
				continue
			}
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p, nil
			}
		}
	}
	p, err := exec.LookPath("deck")
	if err != nil {
		return "", errors.New("deck is not installed: put deck on PATH or next to the app")
	}
	return p, nil
}

// Open starts an engine for dir, dials it and streams its events to on
// until Close. The stream reconnects and resumes on its own.
func Open(ctx context.Context, dir string, on EventFunc) (*Project, error) {
	bin, err := FindDeck()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(bin, "serve", "--rpc", "-c", dir)
	hideWindow(cmd)
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start deck: %w", err)
	}
	hs, err := readHandshake(stdout)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}
	go func() { _, _ = io.Copy(io.Discard, stdout) }()

	conn, client, err := rpc.Dial(hs.Sock, hs.Token)
	if err != nil {
		_ = stdin.Close()
		_ = cmd.Wait()
		return nil, err
	}
	ictx, icancel := context.WithTimeout(ctx, 10*time.Second)
	info, err := client.Info(ictx, &deckv1.Empty{})
	icancel()
	if err != nil {
		_ = conn.Close()
		_ = stdin.Close()
		_ = cmd.Wait()
		return nil, fmt.Errorf("deck did not answer: %w", err)
	}

	sctx, cancel := context.WithCancel(ctx)
	p := &Project{Dir: dir, Info: info, Client: client, cmd: cmd, stdin: stdin, conn: conn, cancel: cancel, done: make(chan struct{})}
	go p.stream(sctx, on)
	return p, nil
}

func readHandshake(r io.Reader) (rpc.Handshake, error) {
	type res struct {
		hs  rpc.Handshake
		err error
	}
	ch := make(chan res, 1)
	go func() {
		line, err := bufio.NewReader(r).ReadBytes('\n')
		if err != nil {
			ch <- res{err: fmt.Errorf("deck exited before it was ready: %w", err)}
			return
		}
		var hs rpc.Handshake
		if err := json.Unmarshal(line, &hs); err != nil || hs.Sock == "" {
			ch <- res{err: fmt.Errorf("deck printed %q, not a handshake (is it older than the app?)", string(line))}
			return
		}
		if hs.V != rpc.Protocol {
			ch <- res{err: fmt.Errorf("deck speaks protocol %d, the app %d: update one of them", hs.V, rpc.Protocol)}
			return
		}
		ch <- res{hs: hs}
	}()
	select {
	case r := <-ch:
		return r.hs, r.err
	case <-time.After(handshakeTimeout):
		return rpc.Handshake{}, errors.New("deck took too long to start")
	}
}

// stream follows Events, resuming from the last seq after a drop.
func (p *Project) stream(ctx context.Context, on EventFunc) {
	defer close(p.done)
	var epoch string
	var seq uint64
	for ctx.Err() == nil {
		st, err := p.Client.Events(ctx, &deckv1.Subscribe{Epoch: epoch, Seq: seq})
		for err == nil {
			var ev *deckv1.Event
			ev, err = st.Recv()
			if err != nil {
				break
			}
			if r := ev.GetReady(); r != nil {
				epoch = r.GetEpoch()
				if r.GetResync() || seq == 0 {
					seq = r.GetSeq()
				}
			} else {
				seq = ev.GetSeq()
			}
			on(p, ev)
		}
		select {
		case <-ctx.Done():
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// Close stops the engine: closing its stdin ends it; it is killed if it
// lingers.
func (p *Project) Close() {
	p.cancel()
	<-p.done
	_ = p.conn.Close()
	_ = p.stdin.Close()
	exited := make(chan struct{})
	go func() { _ = p.cmd.Wait(); close(exited) }()
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		_ = p.cmd.Process.Kill()
		<-exited
	}
}

// Pool holds the open projects by directory.
type Pool struct {
	mu   sync.Mutex
	open map[string]*Project
}

// NewPool returns an empty pool.
func NewPool() *Pool { return &Pool{open: map[string]*Project{}} }

// Open returns the running project for dir, starting it if needed.
func (pl *Pool) Open(ctx context.Context, dir string, on EventFunc) (*Project, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	pl.mu.Lock()
	if p := pl.open[abs]; p != nil {
		pl.mu.Unlock()
		return p, nil
	}
	pl.mu.Unlock()
	p, err := Open(ctx, abs, on)
	if err != nil {
		return nil, err
	}
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if prev := pl.open[abs]; prev != nil { // opened twice at once: keep one
		go p.Close()
		return prev, nil
	}
	pl.open[abs] = p
	return p, nil
}

// CloseAll stops every engine.
func (pl *Pool) CloseAll() {
	pl.mu.Lock()
	ps := make([]*Project, 0, len(pl.open))
	for _, p := range pl.open {
		ps = append(ps, p)
	}
	pl.open = map[string]*Project{}
	pl.mu.Unlock()
	var wg sync.WaitGroup
	for _, p := range ps {
		wg.Go(p.Close)
	}
	wg.Wait()
}
