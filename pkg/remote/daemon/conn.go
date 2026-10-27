package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/devstore"
	"github.com/dev-dami/deck/pkg/remote/e2e"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// Conn is a message-oriented transport to one phone: a relay lane, a direct
// WebSocket, or a pipe in tests. Messages are whole Noise messages.
type Conn interface {
	Read(ctx context.Context) ([]byte, error)
	Write(ctx context.Context, msg []byte) error
	Close(code int, reason string) error
}

const (
	revokeCheck = 2 * time.Second
	writeWait   = 10 * time.Second
	outQueue    = 256
)

// Serve runs one phone's connection until it ends: handshake (Noise IK,
// device pinned in the store), hello, welcome, then requests and events.
func (d *Daemon) Serve(ctx context.Context, c Conn) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sess, dev, err := d.handshake(ctx, c)
	if err != nil {
		return err
	}
	hello, err := d.readFrame(ctx, c, sess, wire.HelloTimeout)
	if err != nil || hello.Hello3 == nil || hello.T != wire.FrameHello || hello.Hello3.Validate() != nil {
		_ = c.Close(wire.CloseBadHello, "bad hello")
		return errors.New("daemon: bad hello")
	}

	cl, replay, resync := d.hub.subscribe(hello.Resume)
	defer d.hub.unsubscribe(cl)
	d.track(dev.ID, 1)
	defer d.track(dev.ID, -1)

	out := make(chan wire.Frame3, outQueue)
	w := &connWriter{c: c, sess: sess}
	welcome := wire.Frame3{T: wire.FrameWelcome, Welcome3: &wire.Welcome3{
		Epoch: d.hub.epoch, Replay: replay,
		Server: wire.ServerInfo3{
			Version: d.cfg.Version, CID: d.cfg.Identity.CID(), Name: d.cfg.Name,
			Projects: d.projectRefs(), Kinds: d.kinds(), AllowYolo: dev.AllowYolo,
			Features: []string{"images", "inbox", "offline_queue", "passkey", "push_e2e"},
			Protocol: wire.ProtocolV3, Theme: d.theme(),
		},
		Device: wire.DeviceRef{ID: dev.ID, Name: dev.Name, ExpiresAt: dev.ExpiresAt},
	}}
	if err := w.write(ctx, welcome); err != nil {
		return err
	}
	if resync {
		if err := w.write(ctx, wire.Frame3{T: wire.FrameResync}); err != nil {
			return err
		}
	}
	_ = d.cfg.Devices.Touch(dev.ID)

	// Writer: responses and hub events share one ordered stream.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		for {
			var f wire.Frame3
			select {
			case f = <-out:
			case f = <-cl.ch:
			case <-cl.kicked:
				_ = c.Close(wire.CloseSlowConsumer, "too slow")
				return
			case <-ctx.Done():
				return
			}
			if err := w.write(ctx, f); err != nil {
				return
			}
		}
	}()

	// Revocation: drop a revoked or expired phone within seconds (R16).
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(revokeCheck)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if _, err := d.cfg.Devices.Get(dev.ID); err != nil {
					_ = c.Close(wire.CloseRevoked, "revoked")
					cancel()
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	sem := make(chan struct{}, wire.MaxInFlight)
	cc := &connCtx{d: d, dev: dev, hub: d.hub, cl: cl}
	for ctx.Err() == nil {
		f, err := d.readFrame(ctx, c, sess, 0)
		if err != nil {
			break
		}
		if f.T != wire.FrameReq || f.ID == "" {
			continue
		}
		select {
		case sem <- struct{}{}:
		default:
			out <- errRes(f.ID, &wire.Error{Code: wire.CodeBusy, Msg: "too many requests at once"})
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			cctx, ccancel := context.WithTimeout(ctx, wire.CallTimeout)
			defer ccancel()
			res, werr := cc.route(cctx, f)
			select {
			case out <- response(f.ID, res, werr):
			case <-ctx.Done():
			}
		}()
	}
	cancel()
	wg.Wait()
	return nil
}

func (d *Daemon) handshake(ctx context.Context, c Conn) (*e2e.Session, devstore.Device, error) {
	hctx, cancel := context.WithTimeout(ctx, wire.HelloTimeout)
	defer cancel()
	msg1, err := c.Read(hctx)
	if err != nil {
		return nil, devstore.Device{}, err
	}
	if len(msg1) > wire.MaxHelloBytes {
		_ = c.Close(wire.CloseBadHello, "bad hello")
		return nil, devstore.Device{}, errors.New("daemon: handshake too large")
	}
	var dev devstore.Device
	resp := e2e.Responder{
		Static: e2e.Keypair{Private: d.cfg.Identity.StaticPrivate(), Public: d.cfg.Identity.StaticPublic()},
		CID:    d.cfg.Identity.CID(),
	}
	msg2, sess, err := resp.Accept(msg1, func(initiator, _ []byte) ([]byte, error) {
		var err error
		if dev, err = d.cfg.Devices.ByStatic(initiator); err != nil {
			return nil, e2e.ErrUnknownDevice
		}
		return nil, nil
	})
	if err != nil {
		code := wire.CloseBadHello
		if errors.Is(err, e2e.ErrUnknownDevice) {
			code = wire.CloseTokenInvalid
		}
		_ = c.Close(code, "handshake refused")
		return nil, devstore.Device{}, err
	}
	if err := c.Write(hctx, msg2); err != nil {
		return nil, devstore.Device{}, err
	}
	return sess, dev, nil
}

// readFrame reads Noise messages until a whole frame arrives. A positive
// timeout bounds the wait.
func (d *Daemon) readFrame(ctx context.Context, c Conn, sess *e2e.Session, timeout time.Duration) (wire.Frame3, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	for {
		msg, err := c.Read(ctx)
		if err != nil {
			return wire.Frame3{}, err
		}
		raw, err := sess.Open(msg)
		if err != nil {
			_ = c.Close(wire.CloseBadHello, "bad message")
			return wire.Frame3{}, err
		}
		if raw == nil {
			continue
		}
		var f wire.Frame3
		if err := json.Unmarshal(raw, &f); err != nil {
			return wire.Frame3{}, err
		}
		return f, nil
	}
}

type connWriter struct {
	c    Conn
	sess *e2e.Session
}

// write is only called from one goroutine, which keeps Noise nonces in order.
func (w *connWriter) write(ctx context.Context, f wire.Frame3) error {
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	msgs, err := w.sess.Seal(raw)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, writeWait)
	defer cancel()
	for _, m := range msgs {
		if err := w.c.Write(wctx, m); err != nil {
			return err
		}
	}
	return nil
}

func response(id string, res any, werr *wire.Error) wire.Frame3 {
	if werr != nil {
		return errRes(id, werr)
	}
	raw, err := json.Marshal(res)
	if err != nil {
		slog.Warn("remote: encode result", "error", err)
		return errRes(id, &wire.Error{Code: wire.CodeInternal, Msg: "something went wrong on the desktop"})
	}
	t := true
	return wire.Frame3{T: wire.FrameRes, ID: id, OK: &t, R: raw}
}

func errRes(id string, werr *wire.Error) wire.Frame3 {
	f := false
	return wire.Frame3{T: wire.FrameRes, ID: id, OK: &f, Err: werr}
}

// connCtx is one phone's view for routing.
type connCtx struct {
	d   *Daemon
	dev devstore.Device
	hub *hub
	cl  *client
}

func invalid(msg string) *wire.Error { return &wire.Error{Code: wire.CodeInvalid, Msg: msg} }

func notFound(msg string) *wire.Error { return &wire.Error{Code: wire.CodeNotFound, Msg: msg} }

func decode[T any](p json.RawMessage) (T, *wire.Error) {
	var v T
	if len(p) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(p, &v); err != nil {
		return v, invalid("the params are not valid JSON for this method")
	}
	return v, nil
}

// route runs one request: computer-level methods here, project methods on the
// provider named by pj.
func (c *connCtx) route(ctx context.Context, f wire.Frame3) (any, *wire.Error) {
	d := c.d
	switch f.M {
	case wire.MProjectsList:
		return d.projectRefs(), nil
	case wire.MInboxGet:
		return d.Inbox(ctx, c.dev), nil
	case wire.MInboxAck:
		req, werr := decode[wire.InboxAckParams](f.P)
		if werr != nil {
			return nil, werr
		}
		if req.PJ == "" || req.SessionID == "" {
			return nil, invalid("pj and session_id are required")
		}
		d.ack(c.dev, req.PJ, req.SessionID)
		return wire.Empty{}, nil
	case wire.MQueueStatus:
		if d.cfg.Queue == nil {
			return []wire.Receipt{}, nil
		}
		rs, err := d.cfg.Queue.Receipts(c.dev.ID, wire.MaxSnapshotReceipts)
		if err != nil {
			return nil, mapErr(f.M, err)
		}
		return rs, nil
	case wire.MPasskeyBegin, wire.MPasskeyFinish, wire.MPasskeyList, wire.MPasskeyRemove, wire.MAssertChallenge:
		return c.passkey(f)
	case wire.MPushRegister:
		req, werr := decode[wire.PushRegisterParams](f.P)
		if werr != nil {
			return nil, werr
		}
		return c.registerPush(req)
	}

	if _, err := d.cfg.Projects.Get(f.PJ); f.PJ == "" || err != nil {
		return nil, notFound("that project is not available")
	}
	p := d.provider(f.PJ)
	if p == nil {
		if !d.ensureProvider(f.PJ) && d.cfg.Spawn != nil {
			return nil, &wire.Error{Code: wire.CodeBusy, Msg: "That project is starting. Try again in a moment."}
		}
		return nil, notFound("that project is not available")
	}

	switch f.M {
	case wire.MSessionsFocus:
		req, werr := decode[wire.IDParams](f.P)
		if werr != nil {
			return nil, werr
		}
		if req.ID != "" {
			if _, err := p.be.Mode(req.ID); err != nil {
				return nil, mapErr(f.M, err)
			}
		}
		c.hub.setFocus(c.cl, wire.FocusRef{PJ: f.PJ, SessionID: req.ID})
		return wire.Empty{}, nil
	case wire.MModeSet:
		return c.modeSet(ctx, p, f)
	case wire.MApprovalsList:
		res, werr := p.call.Call(ctx, c.dev.ID, f.M, f.P)
		if werr != nil {
			return nil, werr
		}
		list, _ := res.([]wire.Approval)
		out := make([]wire.ApprovalV3, 0, len(list))
		for _, a := range list {
			out = append(out, d.v3Approval(f.PJ, a))
		}
		return out, nil
	case wire.MApprovalsAnswer:
		return c.answer(ctx, p, f)
	case wire.MChangesGet:
		return c.changes(ctx, p, f)
	}
	res, werr := p.call.Call(ctx, c.dev.ID, f.M, f.P)
	return res, werr
}

func (c *connCtx) modeSet(ctx context.Context, p *provider, f wire.Frame3) (any, *wire.Error) {
	req, werr := decode[wire.ModeSet3Params](f.P)
	if werr != nil {
		return nil, werr
	}
	if req.Mode != wire.ModeYolo {
		return p.call.Call(ctx, c.dev.ID, f.M, f.P)
	}
	if req.SessionID == "" {
		return nil, invalid("session_id is required")
	}
	// The grant may have been revoked since the connection opened.
	dev, err := c.d.cfg.Devices.Get(c.dev.ID)
	if err != nil {
		return nil, &wire.Error{Code: wire.CodeForbidden, Msg: "this phone is no longer paired"}
	}
	if werr := c.d.cfg.Gate.AllowYolo(ctx, dev, f.PJ, req); werr != nil {
		return nil, werr
	}
	if err := p.be.SetMode(req.SessionID, wire.ModeYolo); err != nil {
		return nil, mapErr(f.M, err)
	}
	return wire.ModeResult{Mode: wire.ModeYolo}, nil
}

func (c *connCtx) answer(ctx context.Context, p *provider, f wire.Frame3) (any, *wire.Error) {
	req, werr := decode[wire.ApprovalsAnswer3Params](f.P)
	if werr != nil {
		return nil, werr
	}
	if req.SessionID == "" || req.ID == "" {
		return nil, invalid("session_id and id are required")
	}
	if err := (wire.ApprovalsAnswerParams{Decision: req.Decision}).Validate(); err != nil {
		return nil, invalid("unknown decision")
	}
	// Find the approval in its own session, so an id from another session is
	// not_found (R10) and the tier is decided from what is really pending.
	pending, err := p.be.Approvals(req.SessionID)
	if err != nil {
		return nil, mapErr(f.M, err)
	}
	var target *wire.ApprovalV3
	for _, a := range pending {
		if a.ID == req.ID && a.SessionID == req.SessionID {
			v := c.d.v3Approval(f.PJ, a)
			target = &v
		}
	}
	if target == nil {
		return nil, notFound("that approval is no longer pending")
	}
	dev, err := c.d.cfg.Devices.Get(c.dev.ID)
	if err != nil {
		return nil, &wire.Error{Code: wire.CodeForbidden, Msg: "this phone is no longer paired"}
	}
	if werr := c.d.cfg.Gate.AllowAnswer(ctx, dev, *target, req); werr != nil {
		return nil, werr
	}
	resolved, err := p.be.Answer(req.SessionID, req.ID, req.Decision, nil)
	if err != nil {
		return nil, mapErr(f.M, err)
	}
	if req.Decision == wire.DecisionDeny && req.Reason != "" && resolved {
		// The workspace's deny takes no reason, so tell the agent separately.
		if _, err := p.be.Send(ctx, req.SessionID, "Denied from phone: "+req.Reason, nil); err != nil {
			slog.Warn("remote: deny reason not delivered", "error", err)
		}
	}
	return wire.ApprovalsAnswerResult{Resolved: resolved}, nil
}

func (c *connCtx) changes(ctx context.Context, p *provider, f wire.Frame3) (any, *wire.Error) {
	req, werr := decode[wire.ChangesGet3Params](f.P)
	if werr != nil {
		return nil, werr
	}
	res, werr := p.call.Call(ctx, c.dev.ID, f.M, f.P)
	if werr != nil || !req.Summary {
		return res, werr
	}
	files, _ := res.([]wire.ChangedFile)
	for i := range files {
		files[i].Diff, files[i].DiffTruncated = firstHunk(files[i].Diff)
	}
	return files, nil
}

// firstHunk keeps the header and the first hunk of a unified diff, at most
// wire.MaxChangeSummaryBytes.
func firstHunk(diff string) (string, bool) {
	cut := false
	if i := indexSecondHunk(diff); i >= 0 {
		diff, cut = diff[:i], true
	}
	short, trunc := wire.Truncate(diff, wire.MaxChangeSummaryBytes)
	return short, cut || trunc
}

func indexSecondHunk(diff string) int {
	first := -1
	for i := 0; i+3 <= len(diff); i++ {
		if (i == 0 || diff[i-1] == '\n') && diff[i:i+2] == "@@" {
			if first < 0 {
				first = i
				continue
			}
			return i
		}
	}
	return -1
}

func mapErr(method string, err error) *wire.Error {
	switch {
	case errors.Is(err, remote.ErrNotFound):
		return notFound("not found")
	case errors.Is(err, remote.ErrInvalid):
		return invalid("the request was refused")
	}
	slog.Warn("remote: request failed", "method", method, "error", err)
	return &wire.Error{Code: wire.CodeInternal, Msg: "something went wrong on the desktop"}
}

// passkey serves passkey.* and assert.challenge through the gate, when it has
// the service.
func (c *connCtx) passkey(f wire.Frame3) (any, *wire.Error) {
	svc, ok := c.d.cfg.Gate.(PasskeyService)
	if !ok {
		return nil, invalid("this is not available in this version")
	}
	switch f.M {
	case wire.MPasskeyBegin:
		res, werr := svc.Begin(c.dev)
		if werr != nil {
			return nil, werr
		}
		return res, nil
	case wire.MPasskeyFinish:
		req, werr := decode[wire.PasskeyFinishParams](f.P)
		if werr != nil {
			return nil, werr
		}
		if werr := svc.Finish(c.dev, req); werr != nil {
			return nil, werr
		}
		return wire.Empty{}, nil
	case wire.MPasskeyList:
		return svc.List(c.dev), nil
	case wire.MPasskeyRemove:
		req, werr := decode[wire.PasskeyRemoveParams](f.P)
		if werr != nil {
			return nil, werr
		}
		if werr := svc.Remove(c.dev, req.ID); werr != nil {
			return nil, werr
		}
		return wire.Empty{}, nil
	default: // assert.challenge
		req, werr := decode[wire.AssertChallengeParams](f.P)
		if werr != nil {
			return nil, werr
		}
		n, werr := svc.Challenge(c.dev, req.ActionDigest)
		if werr != nil {
			return nil, werr
		}
		return wire.AssertChallengeResult{Nonce: n}, nil
	}
}
