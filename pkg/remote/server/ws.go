package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dev-dami/deck/pkg/remote/auth"
	"github.com/dev-dami/deck/pkg/remote/hub"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

const writeDeadline = 10 * time.Second

// helloTimeout is how long a socket may stay silent before its hello
// (R4); a variable so tests can shorten it. Socket deadlines use the real
// clock, never Options.Now.
var helloTimeout = wire.HelloTimeout

// conn is one authenticated phone.
type conn struct {
	s      *Server
	ws     *websocket.Conn
	dev    auth.Device
	client *hub.Client

	wmu    sync.Mutex
	closed bool

	ctx    context.Context
	cancel context.CancelFunc
	sem    chan struct{}
}

func (c *conn) write(f wire.Frame) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.closed {
		return errors.New("connection closed")
	}
	_ = c.ws.SetWriteDeadline(time.Now().Add(writeDeadline))
	return c.ws.WriteJSON(f)
}

func (c *conn) closeWith(code int, text string) {
	c.wmu.Lock()
	if c.closed {
		c.wmu.Unlock()
		return
	}
	c.closed = true
	_ = c.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, text), time.Now().Add(time.Second))
	c.wmu.Unlock()
	_ = c.ws.Close()
	c.cancel()
}

func (c *conn) ping() error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.closed {
		return errors.New("connection closed")
	}
	return c.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeDeadline))
}

// closeNow closes a socket that never authenticated.
func closeNow(ws *websocket.Conn, code int, text string) {
	_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, text), time.Now().Add(time.Second))
	_ = ws.Close()
}

// handleWS upgrades, reads the hello under the pre-auth limits (R4),
// checks the token before the limiter (R1) and runs the connection.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	addr := clientAddr(r)
	if !s.sameOrigin(r) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	if !s.admit(addr) {
		http.Error(w, "too many connections", http.StatusTooManyRequests)
		return
	}
	admitted := true
	defer func() {
		if admitted {
			s.release(addr)
		}
	}()
	ws, err := s.up.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade already answered
	}

	ws.SetReadLimit(wire.MaxHelloBytes)
	_ = ws.SetReadDeadline(time.Now().Add(helloTimeout))
	var hello wire.Frame
	if _, data, err := ws.ReadMessage(); err != nil || json.Unmarshal(data, &hello) != nil {
		closeNow(ws, wire.CloseBadHello, "expected hello")
		return
	}
	if hello.T != wire.FrameHello || hello.Hello.Validate() != nil {
		closeNow(ws, wire.CloseBadHello, "bad hello")
		return
	}
	dev, err := s.store.Verify(hello.Token)
	if err != nil {
		// The limiter is consulted only now, after the lookup failed.
		id, _, _ := strings.Cut(hello.Token, ".")
		if wait, locked := s.limits.Locked(addr, id); locked {
			closeNow(ws, wire.CloseRateLimited, strconv.Itoa(retrySeconds(wait)))
			return
		}
		s.limits.Fail(addr, id)
		closeNow(ws, wire.CloseTokenInvalid, wire.CodeTokenInvalid)
		return
	}
	s.limits.OK(addr, dev.ID)
	s.release(addr)
	admitted = false
	s.store.Touch(dev.ID)
	ws.SetReadLimit(wire.MaxFrameBytes)

	ctx, cancel := context.WithCancel(context.Background())
	c := &conn{s: s, ws: ws, dev: dev, ctx: ctx, cancel: cancel, sem: make(chan struct{}, wire.MaxInFlight)}
	defer cancel()
	client, replay, resync := s.hub.Subscribe(hello.Resume)
	c.client = client
	defer s.hub.Unsubscribe(client)
	s.track(c)
	defer s.untrack(c)

	if err := c.write(s.welcome(dev, replay)); err != nil {
		return
	}
	if resync {
		if err := c.write(wire.Frame{T: wire.FrameResync}); err != nil {
			return
		}
	}
	go c.pump()
	c.readLoop()
	c.closeWith(websocket.CloseNormalClosure, "bye")
}

func (s *Server) welcome(dev auth.Device, replay bool) wire.Frame {
	info := s.be.Info()
	theme := info.Theme
	if theme.Palette == nil {
		theme.Palette = map[string]string{}
	}
	if theme.Kinds == nil {
		theme.Kinds = map[string]wire.KindTone{}
	}
	return wire.Frame{T: wire.FrameWelcome, Welcome: &wire.Welcome{
		Epoch: s.hub.Epoch(), Replay: replay,
		Server: wire.ServerInfo{
			Version: s.opts.Version, Project: info.Project, Kinds: nonNil(info.Kinds),
			AllowYolo: dev.AllowYolo, Features: append([]string(nil), features...),
			ServerID: s.store.ServerID(), Protocol: wire.Protocol, Headless: s.opts.Headless,
			Tunnel: wire.TunnelNone, Theme: theme,
		},
		Device: wire.DeviceRef{ID: dev.ID, Name: dev.Name, ExpiresAt: dev.ExpiresAt()},
	}}
}

// pump forwards the hub feed and keeps the socket alive.
func (c *conn) pump() {
	tick := time.NewTicker(wire.PingInterval)
	defer tick.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-c.client.Kicked():
			c.closeWith(wire.CloseSlowConsumer, "too slow")
			return
		case f := <-c.client.Frames():
			if c.write(f) != nil {
				c.cancel()
				return
			}
		case <-tick.C:
			if c.ping() != nil {
				c.cancel()
				return
			}
		}
	}
}

func (c *conn) readLoop() {
	_ = c.ws.SetReadDeadline(time.Now().Add(wire.IdleTimeout))
	c.ws.SetPongHandler(func(string) error {
		return c.ws.SetReadDeadline(time.Now().Add(wire.IdleTimeout))
	})
	for {
		var f wire.Frame
		if err := c.ws.ReadJSON(&f); err != nil {
			return
		}
		_ = c.ws.SetReadDeadline(time.Now().Add(wire.IdleTimeout))
		if f.T != wire.FrameReq {
			c.fail(f.ID, &wire.Error{Code: wire.CodeInvalid, Msg: "unknown frame type"})
			continue
		}
		if f.ID == "" {
			c.fail("", &wire.Error{Code: wire.CodeInvalid, Msg: "a request needs an id"})
			continue
		}
		select {
		case c.sem <- struct{}{}:
		default:
			c.fail(f.ID, &wire.Error{Code: wire.CodeBusy, Msg: "too many requests in flight"})
			continue
		}
		go func() {
			defer func() { <-c.sem }()
			c.respond(f)
		}()
	}
}

// fail answers a request with an error.
func (c *conn) fail(id string, werr *wire.Error) {
	no := false
	if c.write(wire.Frame{T: wire.FrameRes, ID: id, OK: &no, Err: werr}) != nil {
		c.cancel()
	}
}

func (c *conn) respond(req wire.Frame) {
	ctx, cancel := context.WithTimeout(c.ctx, wire.CallTimeout)
	defer cancel()
	res, werr, after := c.call(ctx, req.M, req.P)
	out := wire.Frame{T: wire.FrameRes, ID: req.ID}
	yes, no := true, false
	if werr != nil {
		out.OK, out.Err = &no, werr
	} else if b, err := json.Marshal(res); err != nil {
		slog.Warn("remote: could not encode a result", "method", req.M, "error", err)
		out.OK, out.Err = &no, &wire.Error{Code: wire.CodeInternal, Msg: "could not encode the result"}
	} else {
		out.OK, out.R = &yes, b
	}
	if c.write(out) != nil {
		c.cancel()
		return
	}
	if after != nil {
		after()
	}
}
