package peer

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"
)

// The control socket lets `deck peer` hand its work to the Deck that runs
// on this machine (deck serve or a terminal Deck): pairing on its listener
// and through its relays, and applying a config edited from the shell. It is
// a Unix socket in the peers directory, which only this user can open, so a
// caller is this user already. One Deck serves it at a time; a second one
// leaves it to the first.
//
// One request per connection, as JSON lines. pair answers with the code it
// armed ("wait"), then with what the other Deck proved ("result"); add
// answers with the result. After a result the caller sends pin (once the
// person compared the fingerprints) or closes the connection, which pins
// nothing. reload answers "ok". Every failure answers "error", worded for a
// person.

// controlName is the socket's file name in the peers directory.
const controlName = "control.sock"

// controlConfirmTimeout bounds how long a result waits for pin.
const controlConfirmTimeout = 5 * time.Minute

// controlMsg is one line either way.
type controlMsg struct {
	Op      string `json:"op,omitempty"` // pair, add, pin, reload
	Name    string `json:"name,omitempty"`
	Role    string `json:"role,omitempty"`
	Addr    string `json:"addr,omitempty"`
	Code    string `json:"code,omitempty"`
	Expect  string `json:"expect,omitempty"`
	Dial    bool   `json:"dial,omitempty"`
	Replace bool   `json:"replace,omitempty"`

	Wait   *controlWait   `json:"wait,omitempty"`
	Result *controlResult `json:"result,omitempty"`
	OK     string         `json:"ok,omitempty"`
	Error  string         `json:"error,omitempty"`
	Taken  bool           `json:"taken,omitempty"` // the name is pinned with another key
}

// controlWait is a code armed by the running Deck.
type controlWait struct {
	Code        string    `json:"code"`
	Addr        string    `json:"addr"`
	Fingerprint string    `json:"fingerprint"`
	Name        string    `json:"name"`
	Hosts       []string  `json:"hosts,omitempty"`
	Reachable   bool      `json:"reachable"`
	Expires     time.Time `json:"expires"`
}

// controlResult is what the other Deck proved, sanitized for display.
type controlResult struct {
	PeerName        string `json:"peer_name"`
	PeerFingerprint string `json:"peer_fingerprint"`
	OwnFingerprint  string `json:"own_fingerprint"`
	RemoteAddr      string `json:"remote_addr"`
	Via             string `json:"via,omitempty"`
}

func resultOf(r PairResult) *controlResult {
	return &controlResult{
		PeerName: Sanitize(truncate(r.PeerName, 40)), PeerFingerprint: r.PeerFingerprint,
		OwnFingerprint: r.OwnFingerprint, RemoteAddr: Sanitize(r.RemoteAddr), Via: r.Via,
	}
}

// startControl serves the control socket in the peers directory until the
// Service closes, unless another running Deck serves it already.
func (s *Service) startControl() {
	if err := ensurePrivateDir(s.dir); err != nil {
		s.log.Debug("peer: no control socket", "error", err)
		return
	}
	path := filepath.Join(s.dir, controlName)
	ln, err := net.Listen("unix", path)
	if err != nil {
		if c, derr := net.DialTimeout("unix", path, time.Second); derr == nil {
			c.Close() // another Deck answers there: it keeps it
			return
		}
		// Left behind by a Deck that did not stop cleanly.
		_ = os.Remove(path)
		if ln, err = net.Listen("unix", path); err != nil {
			s.log.Debug("peer: no control socket", "error", err)
			return
		}
	}
	_ = restrictPath(path, false)
	s.ctlWG.Add(1)
	go func() {
		defer s.ctlWG.Done()
		<-s.base.Done()
		ln.Close()
	}()
	s.ctlWG.Add(1)
	go func() {
		defer s.ctlWG.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.ctlWG.Add(1)
			go func() {
				defer s.ctlWG.Done()
				defer c.Close()
				s.handleControl(c)
			}()
		}
	}()
}

// handleControl serves one request.
func (s *Service) handleControl(c net.Conn) {
	ctx, cancel := context.WithCancel(s.base)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	in := make(chan controlMsg)
	go readControl(ctx, c, in, cancel)
	var req controlMsg
	select {
	case m, ok := <-in:
		if !ok {
			return
		}
		req = m
	case <-ctx.Done():
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	reply := func(m controlMsg) bool { return writeJSONLine(c, m) == nil }
	switch req.Op {
	case "reload":
		if err := s.Reload(); err != nil {
			reply(controlMsg{Error: err.Error()})
			return
		}
		reply(controlMsg{OK: "applied"})
	case "pair":
		w, err := s.StartPairListen(ctx)
		if err != nil {
			reply(controlMsg{Error: err.Error()})
			return
		}
		defer w.Cancel()
		if !reply(controlMsg{Wait: &controlWait{Code: w.Code, Addr: w.Addr, Fingerprint: w.Fingerprint, Name: w.Name,
			Hosts: w.Hosts, Reachable: w.Reachable, Expires: w.Expires}}) {
			return
		}
		var out PairOutcome
		select {
		case o, ok := <-w.Done:
			if !ok {
				return
			}
			out = o
		case <-ctx.Done():
			return
		}
		if out.Err != nil {
			reply(controlMsg{Error: PairErrText(out.Err, "")})
			return
		}
		s.confirmControl(ctx, in, reply, out.Result, req)
	case "add":
		res, err := s.PairDial(ctx, req.Addr, req.Code, req.Expect)
		if err != nil {
			reply(controlMsg{Error: PairErrText(err, WithDefaultPort(req.Addr))})
			return
		}
		s.confirmControl(ctx, in, reply, res, req)
	default:
		reply(controlMsg{Error: fmt.Sprintf("unknown request %q", truncate(req.Op, 20))})
	}
}

// confirmControl sends res and pins it when the caller confirms with pin.
// req is the first request: add gives the address and dial.
func (s *Service) confirmControl(ctx context.Context, in <-chan controlMsg, reply func(controlMsg) bool, res PairResult, req controlMsg) {
	if !reply(controlMsg{Result: resultOf(res)}) {
		return
	}
	timer := time.NewTimer(controlConfirmTimeout)
	defer timer.Stop()
	var m controlMsg
	select {
	case got, ok := <-in:
		if !ok || got.Op != "pin" {
			return // closed or said no: nothing is pinned
		}
		m = got
	case <-timer.C:
		return
	case <-ctx.Done():
		return
	}
	r := PinRequest{Name: m.Name, Role: m.Role, Replace: m.Replace, Result: res}
	if req.Op == "add" {
		r.Addr, r.Dial = WithDefaultPort(req.Addr), req.Dial
	}
	if err := s.Pin(r); err != nil {
		reply(controlMsg{Error: err.Error(), Taken: errors.Is(err, ErrPinTaken)})
		return
	}
	reply(controlMsg{OK: "pinned"})
}

// readControl sends each line c reads to in, and cancels the request when
// c ends: the caller went away.
func readControl(ctx context.Context, c net.Conn, in chan<- controlMsg, cancel context.CancelFunc) {
	defer cancel()
	defer close(in)
	r := bufio.NewReaderSize(c, 4096)
	for {
		line, err := readFrame(r, pairMaxMessage)
		if err != nil {
			return
		}
		var m controlMsg
		if json.Unmarshal(line, &m) != nil {
			return
		}
		select {
		case in <- m:
		case <-ctx.Done():
			return
		}
	}
}

// controlClient is a connection to the running Deck's control socket.
type controlClient struct {
	c net.Conn
	r *bufio.Reader
}

// dialControl connects to the control socket of the Deck running with
// peers directory dir; nil when none runs.
func dialControl(dir string) *controlClient {
	c, err := net.DialTimeout("unix", filepath.Join(dir, controlName), time.Second)
	if err != nil {
		return nil
	}
	return &controlClient{c: c, r: bufio.NewReaderSize(c, 4096)}
}

func (cc *controlClient) Close() error { return cc.c.Close() }

func (cc *controlClient) send(m controlMsg) error { return writeJSONLine(cc.c, m) }

// recv reads the next answer; an "error" answer is returned as an error,
// and a Deck that stopped as errControlGone.
func (cc *controlClient) recv() (controlMsg, error) {
	line, err := readFrame(cc.r, 64<<10)
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
			return controlMsg{}, errControlGone
		}
		return controlMsg{}, err
	}
	var m controlMsg
	if err := json.Unmarshal(line, &m); err != nil {
		return controlMsg{}, err
	}
	if m.Error != "" {
		err := errors.New(Sanitize(m.Error))
		if m.Taken {
			err = fmt.Errorf("%w: %s", ErrPinTaken, Sanitize(m.Error))
		}
		return m, err
	}
	return m, nil
}

// errControlGone: the running Deck closed the connection (it stopped).
var errControlGone = errors.New("the running Deck stopped answering")

// ReloadRunning asks the Deck running with peers directory dir to apply the
// config again; ran is false when no Deck runs there.
func ReloadRunning(dir string) (ran bool, err error) {
	cc := dialControl(dir)
	if cc == nil {
		return false, nil
	}
	defer cc.Close()
	_ = cc.c.SetDeadline(time.Now().Add(15 * time.Second))
	if err := cc.send(controlMsg{Op: "reload"}); err != nil {
		return true, err
	}
	_, err = cc.recv()
	return true, err
}
