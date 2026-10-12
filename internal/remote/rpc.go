package remote

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dev-dami/deck/internal/remote/auth"
	"github.com/dev-dami/deck/internal/remote/push"
	"github.com/dev-dami/deck/internal/remote/wire"
)

const (
	defaultPage = 100
	callTimeout = 2 * time.Minute
)

// conn is one authenticated phone.
type conn struct {
	s   *Server
	ws  *websocket.Conn
	dev auth.Device
	sub *subscriber

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

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	ws, err := s.up.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade already answered
	}
	ws.SetReadLimit(wire.MaxFrameBytes)
	addr := s.clientAddr(r)

	reject := func(text string) {
		_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(wire.CloseUnauthorized, text), time.Now().Add(time.Second))
		_ = ws.Close()
	}
	_ = ws.SetReadDeadline(time.Now().Add(helloDeadline))
	var hello wire.Frame
	if err := ws.ReadJSON(&hello); err != nil || hello.T != wire.FrameHello {
		reject("expected hello")
		return
	}
	if !s.lim.Allowed(addr) {
		reject("too many attempts")
		return
	}
	dev, err := s.store.Verify(hello.Token, addr)
	if err != nil {
		s.lim.Fail(addr)
		reject("unauthorized")
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	c := &conn{s: s, ws: ws, dev: dev, ctx: ctx, cancel: cancel, sem: make(chan struct{}, maxInflight)}
	defer cancel()
	sub, replay, resync := s.hub.Subscribe(hello.Resume)
	c.sub = sub
	defer s.hub.Unsubscribe(sub)
	s.track(c)
	defer s.untrack(c)

	info := s.be.Info()
	welcome := wire.Frame{T: wire.FrameWelcome, Welcome: wire.Welcome{
		Epoch: s.hub.Epoch(), Replay: len(replay) > 0,
		Server: &wire.ServerInfo{
			Version: info.Version, Project: info.Project, Cwd: info.Cwd, Kinds: info.Kinds,
			AllowYolo: s.opts.AllowYolo, ProtocolV: wire.ProtocolVersion, Headless: s.opts.Headless,
			TunnelKind: s.opts.Tunnel, Features: s.features(), PushKey: s.pushKey(),
		},
		Device: &wire.DeviceRef{ID: dev.ID, Name: dev.Name},
	}}
	if c.write(welcome) != nil {
		return
	}
	if resync {
		if c.write(wire.Frame{T: wire.FrameResync}) != nil {
			return
		}
	}
	for _, f := range replay {
		if c.write(f) != nil {
			return
		}
	}

	go c.pump()
	c.readLoop()
	c.closeWith(websocket.CloseNormalClosure, "bye")
}

// pump forwards the hub feed and keeps the socket alive.
func (c *conn) pump() {
	tick := time.NewTicker(pingEvery)
	defer tick.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-c.sub.kicked:
			c.closeWith(wire.CloseSlowConsumer, "too slow")
			return
		case f := <-c.sub.ch:
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
	_ = c.ws.SetReadDeadline(time.Now().Add(idleTimeout))
	c.ws.SetPongHandler(func(string) error {
		return c.ws.SetReadDeadline(time.Now().Add(idleTimeout))
	})
	for {
		var f wire.Frame
		if err := c.ws.ReadJSON(&f); err != nil {
			return
		}
		_ = c.ws.SetReadDeadline(time.Now().Add(idleTimeout))
		if f.T != wire.FrameReq || f.ID == "" {
			continue
		}
		select {
		case c.sem <- struct{}{}:
		case <-c.ctx.Done():
			return
		}
		go func() {
			defer func() { <-c.sem }()
			c.respond(f)
		}()
	}
}

func (c *conn) respond(req wire.Frame) {
	ctx, cancel := context.WithTimeout(c.ctx, callTimeout)
	defer cancel()
	res, werr := c.call(ctx, req.M, req.P)
	out := wire.Frame{T: wire.FrameRes, ID: req.ID}
	t, f := true, false
	if werr != nil {
		out.OK, out.Err = &f, werr
	} else {
		b, err := json.Marshal(res)
		if err != nil {
			out.OK, out.Err = &f, &wire.Error{Code: wire.CodeInternal, Msg: "could not encode the result"}
		} else {
			out.OK, out.R = &t, b
		}
	}
	if c.write(out) != nil {
		c.cancel()
	}
}

func decode[T any](p json.RawMessage) (T, *wire.Error) {
	var v T
	if len(p) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(p, &v); err != nil {
		return v, &wire.Error{Code: wire.CodeInvalid, Msg: "bad params: " + err.Error()}
	}
	return v, nil
}

func fail(err error) *wire.Error {
	if errors.Is(err, ErrNotFound) {
		return &wire.Error{Code: wire.CodeNotFound, Msg: err.Error()}
	}
	if errors.Is(err, ErrInvalid) {
		return &wire.Error{Code: wire.CodeInvalid, Msg: err.Error()}
	}
	return &wire.Error{Code: wire.CodeInternal, Msg: err.Error()}
}

func invalid(msg string) *wire.Error { return &wire.Error{Code: wire.CodeInvalid, Msg: msg} }

var ok = struct{}{}

// call runs one method. The table is explicit so nothing is reachable by
// accident.
func (c *conn) call(ctx context.Context, method string, p json.RawMessage) (any, *wire.Error) {
	be := c.s.be
	switch method {
	case wire.MSessionsList:
		v, err := be.Sessions(ctx)
		if err != nil {
			return nil, fail(err)
		}
		return nonNil(v), nil
	case wire.MSessionsCreate:
		req, werr := decode[wire.SessionsCreateReq](p)
		if werr != nil {
			return nil, werr
		}
		v, err := be.CreateSession(ctx, req.Title)
		if err != nil {
			return nil, fail(err)
		}
		return v, nil
	case wire.MSessionsRename:
		req, werr := decode[wire.SessionsRenameReq](p)
		if werr != nil {
			return nil, werr
		}
		if req.ID == "" || strings.TrimSpace(req.Title) == "" {
			return nil, invalid("id and title are required")
		}
		if err := be.RenameSession(ctx, req.ID, req.Title); err != nil {
			return nil, fail(err)
		}
		return ok, nil
	case wire.MSessionsDelete:
		req, werr := decode[wire.SessionRef](p)
		if werr != nil {
			return nil, werr
		}
		if err := be.DeleteSession(ctx, req.ID); err != nil {
			return nil, fail(err)
		}
		return ok, nil
	case wire.MSessionsFocus:
		req, werr := decode[wire.SessionsFocusReq](p)
		if werr != nil {
			return nil, werr
		}
		c.s.hub.Focus(c.sub, req.ID)
		return ok, nil
	case wire.MMessagesList:
		return c.messagesList(ctx, p)
	case wire.MChatSend:
		return c.chatSend(ctx, p)
	case wire.MMessageEdit:
		req, werr := decode[wire.MessageEditReq](p)
		if werr != nil {
			return nil, werr
		}
		if req.SessionID == "" || req.MessageID == "" || strings.TrimSpace(req.Text) == "" {
			return nil, invalid("session_id, message_id and text are required")
		}
		if err := be.EditMessage(ctx, req.SessionID, req.MessageID, req.Text); err != nil {
			return nil, fail(err)
		}
		return ok, nil
	case wire.MPushStatus, wire.MPushSubscribe, wire.MPushUnsubscribe, wire.MPushTest:
		return c.pushCall(ctx, method, p)
	case wire.MChatCancel:
		req, werr := decode[wire.SessionIDReq](p)
		if werr != nil {
			return nil, werr
		}
		be.Cancel(req.SessionID)
		return ok, nil
	case wire.MAgentInterrupt:
		req, werr := decode[wire.AgentInterruptReq](p)
		if werr != nil {
			return nil, werr
		}
		if err := be.Interrupt(req.SessionID, req.Agent); err != nil {
			return nil, fail(err)
		}
		return ok, nil
	case wire.MChatContinue:
		req, werr := decode[wire.SessionIDReq](p)
		if werr != nil {
			return nil, werr
		}
		if err := be.Continue(req.SessionID); err != nil {
			return nil, fail(err)
		}
		return ok, nil
	case wire.MQueueList:
		req, werr := decode[wire.SessionIDReq](p)
		if werr != nil {
			return nil, werr
		}
		return nonNil(be.Queue(req.SessionID)), nil
	case wire.MQueueSteer:
		req, werr := decode[wire.QueueItemRef](p)
		if werr != nil {
			return nil, werr
		}
		if err := be.Steer(req.ItemID); err != nil {
			return nil, fail(err)
		}
		return ok, nil
	case wire.MQueueCancel:
		req, werr := decode[wire.QueueItemRef](p)
		if werr != nil {
			return nil, werr
		}
		if err := be.CancelQueued(req.ItemID); err != nil {
			return nil, fail(err)
		}
		return ok, nil
	case wire.MRosterGet:
		req, werr := decode[wire.SessionIDReq](p)
		if werr != nil {
			return nil, werr
		}
		return nonNil(be.Roster(req.SessionID)), nil
	case wire.MModelsList:
		req, werr := decode[wire.ModelsListReq](p)
		if werr != nil {
			return nil, werr
		}
		v, err := be.Models(ctx, req.SessionID, req.Kind)
		if err != nil {
			return nil, fail(err)
		}
		return nonNil(v), nil
	case wire.MModelsSet:
		req, werr := decode[wire.ModelsSetReq](p)
		if werr != nil {
			return nil, werr
		}
		if req.Kind == "" {
			return nil, invalid("kind is required")
		}
		if err := be.SetModel(req.SessionID, req.Kind, req.Model, req.Effort); err != nil {
			return nil, fail(err)
		}
		return ok, nil
	case wire.MModeGet:
		return wire.ModeBody{Mode: be.Mode()}, nil
	case wire.MModeSet:
		return c.modeSet(p)
	case wire.MApprovalsList:
		return nonNil(be.Approvals()), nil
	case wire.MApprovalsAnswer:
		req, werr := decode[wire.ApprovalsAnswerReq](p)
		if werr != nil {
			return nil, werr
		}
		switch req.Decision {
		case wire.DecisionAllow, wire.DecisionAllowSession, wire.DecisionDeny:
		default:
			return nil, invalid("decision must be allow, allow_session or deny")
		}
		resolved, err := be.Answer(req.ID, req.Decision)
		if err != nil {
			return nil, fail(err)
		}
		return wire.ApprovalsAnswerRes{Resolved: resolved}, nil
	case wire.MChangesGet:
		req, werr := decode[wire.ChangesGetReq](p)
		if werr != nil {
			return nil, werr
		}
		v, err := be.Changes(ctx, req.SessionID)
		if err != nil {
			return nil, fail(err)
		}
		return nonNil(v), nil
	case wire.MDevicesList:
		return c.devicesList()
	case wire.MDevicesRevoke:
		req, werr := decode[wire.DevicesRevokeReq](p)
		if werr != nil {
			return nil, werr
		}
		if err := c.s.store.Revoke(req.ID); err != nil {
			if errors.Is(err, auth.ErrNotFound) {
				return nil, &wire.Error{Code: wire.CodeNotFound, Msg: err.Error()}
			}
			return nil, fail(err)
		}
		// Closing the caller's own socket here would drop this response.
		go c.s.kickDevice(req.ID)
		return ok, nil
	}
	return nil, &wire.Error{Code: wire.CodeNotFound, Msg: "unknown method " + method}
}

// nonNil keeps an empty list as [] on the wire, not null.
func nonNil[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

func (c *conn) modeSet(p json.RawMessage) (any, *wire.Error) {
	req, werr := decode[wire.ModeBody](p)
	if werr != nil {
		return nil, werr
	}
	switch req.Mode {
	case wire.ModePlan, wire.ModeAsk:
	case wire.ModeYolo:
		if !c.s.opts.AllowYolo {
			return nil, &wire.Error{Code: wire.CodeYoloDisabled, Msg: "yolo is off for remote control; start deck with --allow-yolo"}
		}
	default:
		return nil, invalid("mode must be plan, ask or yolo")
	}
	if err := c.s.be.SetMode(req.Mode); err != nil {
		return nil, fail(err)
	}
	return wire.ModeBody{Mode: req.Mode}, nil
}

func (c *conn) messagesList(ctx context.Context, p json.RawMessage) (any, *wire.Error) {
	req, werr := decode[wire.MessagesListReq](p)
	if werr != nil {
		return nil, werr
	}
	if req.SessionID == "" {
		return nil, invalid("session_id is required")
	}
	limit := req.Limit
	if limit <= 0 || limit > defaultPage {
		limit = defaultPage
	}
	all, err := c.s.be.Messages(ctx, req.SessionID)
	if err != nil {
		return nil, fail(err)
	}
	if req.Before > 0 {
		cut := len(all)
		for i, m := range all {
			if m.CreatedAt >= req.Before {
				cut = i
				break
			}
		}
		all = all[:cut]
	}
	more := len(all) > limit
	if more {
		all = all[len(all)-limit:]
	}
	return wire.MessagesListRes{Messages: nonNil(all), More: more}, nil
}

func (c *conn) chatSend(ctx context.Context, p json.RawMessage) (any, *wire.Error) {
	req, werr := decode[wire.ChatSendReq](p)
	if werr != nil {
		return nil, werr
	}
	if req.SessionID == "" {
		return nil, invalid("session_id is required")
	}
	if strings.TrimSpace(req.Text) == "" && len(req.Images) == 0 {
		return nil, invalid("nothing to send")
	}
	images := make([]Image, 0, len(req.Images))
	for _, im := range req.Images {
		if !strings.HasPrefix(im.Mime, "image/") {
			return nil, invalid("attachments must be images")
		}
		if base64.StdEncoding.DecodedLen(len(im.Data)) > wire.MaxImageBytes+3 {
			return nil, &wire.Error{Code: wire.CodeTooLarge, Msg: "image is over 8 MB"}
		}
		data, err := base64.StdEncoding.DecodeString(im.Data)
		if err != nil {
			return nil, invalid("image is not valid base64")
		}
		if len(data) > wire.MaxImageBytes {
			return nil, &wire.Error{Code: wire.CodeTooLarge, Msg: "image is over 8 MB"}
		}
		name := filepath.Base(im.Name)
		if name == "." || name == string(filepath.Separator) {
			name = "image"
		}
		images = append(images, Image{Name: name, Mime: im.Mime, Data: data})
	}
	// A retry after a dropped connection must not post twice.
	if req.IdempotencyKey != "" && c.s.seen(c.dev.ID+":"+req.IdempotencyKey) {
		return ok, nil
	}
	if err := c.s.be.Send(ctx, req.SessionID, req.Text, images); err != nil {
		return nil, fail(err)
	}
	return ok, nil
}

func (c *conn) devicesList() (any, *wire.Error) {
	devs, err := c.s.store.List()
	if err != nil {
		return nil, fail(err)
	}
	out := make([]wire.Device, 0, len(devs))
	for _, d := range devs {
		out = append(out, wire.Device{
			ID: d.ID, Name: d.Name, CreatedAt: d.CreatedAt.Unix(), LastSeen: d.LastSeen.Unix(), Current: d.ID == c.dev.ID,
		})
	}
	return out, nil
}

func (s *Server) features() []string {
	f := []string{"queue", "models", "changes", "devices", "edit"}
	if s.push != nil {
		f = append(f, "push")
	}
	return f
}

func (s *Server) pushKey() string {
	if s.push == nil {
		return ""
	}
	return s.push.PublicKey()
}

func (c *conn) pushCall(ctx context.Context, method string, p json.RawMessage) (any, *wire.Error) {
	ps := c.s.push
	if ps == nil {
		if method == wire.MPushStatus {
			return wire.PushStatus{}, nil
		}
		return nil, invalid("notifications are not available on this server")
	}
	switch method {
	case wire.MPushStatus:
		sub, on := ps.Get(c.dev.ID)
		return wire.PushStatus{Available: true, Enabled: on, Approvals: sub.Approvals, Done: sub.Done}, nil
	case wire.MPushSubscribe:
		req, werr := decode[wire.PushSubscribeReq](p)
		if werr != nil {
			return nil, werr
		}
		err := ps.Subscribe(c.dev.ID, push.Subscription{
			Endpoint: req.Subscription.Endpoint, P256dh: req.Subscription.Keys.P256dh, Auth: req.Subscription.Keys.Auth,
			Approvals: req.Approvals, Done: req.Done,
		})
		if err != nil {
			return nil, invalid(err.Error())
		}
		return ok, nil
	case wire.MPushUnsubscribe:
		if err := ps.Unsubscribe(c.dev.ID); err != nil {
			return nil, fail(err)
		}
		return ok, nil
	default: // push.test
		if err := ps.Test(ctx, c.dev.ID, push.Message{Title: "Deck", Body: "Notifications are on.", Tag: "test"}); err != nil {
			return nil, &wire.Error{Code: wire.CodeInternal, Msg: err.Error()}
		}
		return ok, nil
	}
}
