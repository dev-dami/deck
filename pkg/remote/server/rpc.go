package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

const (
	defaultPage   = 50
	maxImageBytes = 8 << 20
	maxTitleRunes = 200
)

var ok = wire.Empty{}

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

func invalid(msg string) *wire.Error { return &wire.Error{Code: wire.CodeInvalid, Msg: msg} }

// fail maps a backend error to a wire error. Go error text never reaches
// the phone (R11): a *wire.Error passes through, the sentinels get their
// code, anything else is logged and becomes internal.
func fail(method string, err error) *wire.Error {
	var werr *wire.Error
	switch {
	case errors.As(err, &werr):
		msg := werr.Msg
		if utf8.RuneCountInString(msg) > wire.MaxErrorMsgRunes {
			msg = string([]rune(msg)[:wire.MaxErrorMsgRunes])
		}
		return &wire.Error{Code: werr.Code, Msg: msg}
	case errors.Is(err, remote.ErrNotFound):
		return &wire.Error{Code: wire.CodeNotFound, Msg: "not found"}
	case errors.Is(err, remote.ErrInvalid):
		return &wire.Error{Code: wire.CodeInvalid, Msg: "the request was refused"}
	case errors.Is(err, context.DeadlineExceeded):
		return &wire.Error{Code: wire.CodeInternal, Msg: "the request timed out"}
	}
	slog.Warn("remote: request failed", "method", method, "error", err)
	return &wire.Error{Code: wire.CodeInternal, Msg: "something went wrong on the desktop"}
}

// nonNil keeps an empty list as [] on the wire, not null.
func nonNil[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

// call runs one method. The table is explicit so nothing is reachable by
// accident; every id in the params is checked against its session by the
// backend (R10). after runs once the response is written.
func (c *conn) call(ctx context.Context, method string, p json.RawMessage) (res any, werr *wire.Error, after func()) {
	be := c.s.be
	switch method {
	case wire.MSessionsList:
		v, err := be.Sessions(ctx)
		if err != nil {
			return nil, fail(method, err), nil
		}
		return capSessions(nonNil(v)), nil, nil
	case wire.MSessionsCreate:
		req, werr := decode[wire.SessionsCreateParams](p)
		if werr != nil {
			return nil, werr, nil
		}
		if utf8.RuneCountInString(req.Title) > maxTitleRunes {
			return nil, invalid("a title is at most 200 characters"), nil
		}
		v, err := be.CreateSession(ctx, req.Title)
		if err != nil {
			return nil, fail(method, err), nil
		}
		return capSession(v), nil, nil
	case wire.MSessionsRename:
		req, werr := decode[wire.SessionsRenameParams](p)
		if werr != nil {
			return nil, werr, nil
		}
		if req.ID == "" || strings.TrimSpace(req.Title) == "" {
			return nil, invalid("id and title are required"), nil
		}
		if utf8.RuneCountInString(req.Title) > maxTitleRunes {
			return nil, invalid("a title is at most 200 characters"), nil
		}
		return done(method, be.RenameSession(ctx, req.ID, req.Title))
	case wire.MSessionsDelete:
		req, werr := decode[wire.IDParams](p)
		if werr != nil {
			return nil, werr, nil
		}
		if req.ID == "" {
			return nil, invalid("id is required"), nil
		}
		return done(method, be.DeleteSession(ctx, req.ID))
	case wire.MSessionsFocus:
		req, werr := decode[wire.IDParams](p)
		if werr != nil {
			return nil, werr, nil
		}
		if req.ID != "" {
			if _, err := be.Mode(req.ID); err != nil {
				return nil, fail(method, err), nil
			}
		}
		c.s.hub.Focus(c.client, req.ID)
		return ok, nil, nil
	case wire.MMessagesList:
		return c.messagesList(ctx, p)
	case wire.MChatSend:
		return c.chatSend(ctx, p)
	case wire.MMessageEdit:
		req, werr := decode[wire.MessageEditParams](p)
		if werr != nil {
			return nil, werr, nil
		}
		if req.SessionID == "" || req.MessageID == "" || strings.TrimSpace(req.Text) == "" {
			return nil, invalid("session_id, message_id and text are required"), nil
		}
		return done(method, be.EditMessage(ctx, req.SessionID, req.MessageID, req.Text))
	case wire.MChatCancel, wire.MChatContinue, wire.MQueueList, wire.MRosterGet, wire.MRosterKinds, wire.MModeGet, wire.MChangesGet:
		req, werr := decode[wire.SessionParams](p)
		if werr != nil {
			return nil, werr, nil
		}
		if req.SessionID == "" {
			return nil, invalid("session_id is required"), nil
		}
		return c.sessionCall(ctx, method, req.SessionID)
	case wire.MAgentInterrupt:
		req, werr := decode[wire.AgentInterruptParams](p)
		if werr != nil {
			return nil, werr, nil
		}
		if req.SessionID == "" || req.Agent == "" {
			return nil, invalid("session_id and agent are required"), nil
		}
		return done(method, be.Interrupt(req.SessionID, req.Agent))
	case wire.MQueueSteer, wire.MQueueCancel:
		req, werr := decode[wire.QueueItemParams](p)
		if werr != nil {
			return nil, werr, nil
		}
		if req.SessionID == "" || req.ItemID == "" {
			return nil, invalid("session_id and item_id are required"), nil
		}
		if method == wire.MQueueSteer {
			return done(method, be.Steer(req.SessionID, req.ItemID))
		}
		return done(method, be.CancelQueued(req.SessionID, req.ItemID))
	case wire.MModelsList:
		req, werr := decode[wire.ModelsListParams](p)
		if werr != nil {
			return nil, werr, nil
		}
		if req.SessionID == "" || req.Kind == "" {
			return nil, invalid("session_id and kind are required"), nil
		}
		v, err := be.Models(ctx, req.SessionID, req.Kind)
		if err != nil {
			return nil, fail(method, err), nil
		}
		return nonNil(v), nil, nil
	case wire.MModelsSet:
		req, werr := decode[wire.ModelsSetParams](p)
		if werr != nil {
			return nil, werr, nil
		}
		if req.SessionID == "" || req.Kind == "" {
			return nil, invalid("session_id and kind are required"), nil
		}
		return done(method, be.SetModel(req.SessionID, req.Kind, req.Model, req.Effort))
	case wire.MRosterAdd:
		req, werr := decode[wire.RosterAddParams](p)
		if werr != nil {
			return nil, werr, nil
		}
		if req.SessionID == "" || req.Kind == "" {
			return nil, invalid("session_id and kind are required"), nil
		}
		v, err := be.AddAgent(req.SessionID, req.Kind, req.Model)
		if err != nil {
			return nil, fail(method, err), nil
		}
		return v, nil, nil
	case wire.MModeSet:
		return c.modeSet(p)
	case wire.MApprovalsList:
		req, werr := decode[wire.ApprovalsListParams](p)
		if werr != nil {
			return nil, werr, nil
		}
		v, err := be.Approvals(req.SessionID)
		if err != nil {
			return nil, fail(method, err), nil
		}
		return capApprovals(nonNil(v)), nil, nil
	case wire.MApprovalsAnswer:
		req, werr := decode[wire.ApprovalsAnswerParams](p)
		if werr != nil {
			return nil, werr, nil
		}
		if req.SessionID == "" || req.ID == "" {
			return nil, invalid("session_id and id are required"), nil
		}
		if err := req.Validate(); err != nil {
			return nil, fail(method, err), nil
		}
		resolved, err := be.Answer(req.SessionID, req.ID, req.Decision)
		if err != nil {
			return nil, fail(method, err), nil
		}
		return wire.ApprovalsAnswerResult{Resolved: resolved}, nil, nil
	case wire.MDevicesList:
		return c.devicesList()
	case wire.MDevicesRevoke:
		return c.devicesRevoke(p)
	case wire.MPushStatus:
		return wire.PushStatus{}, nil, nil
	case wire.MPushSubscribe, wire.MPushUnsubscribe, wire.MPushTest:
		return nil, invalid("notifications are not available in this version"), nil
	}
	return nil, invalid("unknown method"), nil
}

// done answers a method without a result.
func done(method string, err error) (any, *wire.Error, func()) {
	if err != nil {
		return nil, fail(method, err), nil
	}
	return ok, nil, nil
}

// sessionCall runs the methods whose only param is a session id.
func (c *conn) sessionCall(ctx context.Context, method, sid string) (any, *wire.Error, func()) {
	be := c.s.be
	switch method {
	case wire.MChatCancel:
		return done(method, be.Cancel(sid))
	case wire.MChatContinue:
		return done(method, be.Continue(sid))
	case wire.MQueueList:
		v, err := be.Queue(sid)
		if err != nil {
			return nil, fail(method, err), nil
		}
		return capQueue(nonNil(v)), nil, nil
	case wire.MRosterGet:
		v, err := be.Roster(sid)
		if err != nil {
			return nil, fail(method, err), nil
		}
		return nonNil(v), nil, nil
	case wire.MRosterKinds:
		v, err := be.AgentKinds(sid)
		if err != nil {
			return nil, fail(method, err), nil
		}
		return nonNil(v), nil, nil
	case wire.MModeGet:
		mode, err := be.Mode(sid)
		if err != nil {
			return nil, fail(method, err), nil
		}
		return wire.ModeResult{Mode: mode}, nil, nil
	default: // changes.get
		v, err := be.Changes(ctx, sid)
		if err != nil {
			return nil, fail(method, err), nil
		}
		return capChanges(nonNil(v)), nil, nil
	}
}

func (c *conn) modeSet(p json.RawMessage) (any, *wire.Error, func()) {
	req, werr := decode[wire.ModeSetParams](p)
	if werr != nil {
		return nil, werr, nil
	}
	if req.SessionID == "" {
		return nil, invalid("session_id is required"), nil
	}
	if err := req.Validate(); err != nil {
		return nil, fail(wire.MModeSet, err), nil
	}
	if req.Mode == wire.ModeYolo && !c.allowYolo() {
		return nil, &wire.Error{Code: wire.CodeYoloDisabled, Msg: "This phone may not switch to yolo; allow it from the desktop."}, nil
	}
	if err := c.s.be.SetMode(req.SessionID, req.Mode); err != nil {
		return nil, fail(wire.MModeSet, err), nil
	}
	return wire.ModeResult{Mode: req.Mode}, nil, nil
}

// allowYolo is the device's grant as of now (R15): given or withdrawn from
// the desktop while the phone is connected, it applies at once.
func (c *conn) allowYolo() bool {
	if d, err := c.s.store.Get(c.dev.ID); err == nil {
		return d.AllowYolo
	}
	return c.dev.AllowYolo
}

func (c *conn) messagesList(ctx context.Context, p json.RawMessage) (any, *wire.Error, func()) {
	req, werr := decode[wire.MessagesListParams](p)
	if werr != nil {
		return nil, werr, nil
	}
	if req.SessionID == "" {
		return nil, invalid("session_id is required"), nil
	}
	if err := req.Validate(); err != nil {
		return nil, fail(wire.MMessagesList, err), nil
	}
	limit := req.Limit
	if limit == 0 {
		limit = defaultPage
	}
	msgs, more, next, err := c.s.be.Messages(ctx, req.SessionID, req.Before, limit)
	if err != nil {
		return nil, fail(wire.MMessagesList, err), nil
	}
	res := wire.MessagesListResult{Messages: capMessages(nonNil(msgs)), More: more}
	if more {
		res.Cursor = next
	}
	return res, nil, nil
}

func (c *conn) chatSend(ctx context.Context, p json.RawMessage) (any, *wire.Error, func()) {
	req, werr := decode[wire.ChatSendParams](p)
	if werr != nil {
		return nil, werr, nil
	}
	if req.SessionID == "" {
		return nil, invalid("session_id is required"), nil
	}
	if strings.TrimSpace(req.Text) == "" && len(req.Images) == 0 {
		return nil, invalid("nothing to send"), nil
	}
	images := make([]remote.Image, 0, len(req.Images))
	for _, im := range req.Images {
		if !strings.HasPrefix(im.Mime, "image/") {
			return nil, invalid("attachments must be images"), nil
		}
		if base64.StdEncoding.DecodedLen(len(im.Data)) > maxImageBytes+3 {
			return nil, &wire.Error{Code: wire.CodeTooLarge, Msg: "an image may be at most 8 MB"}, nil
		}
		data, err := base64.StdEncoding.DecodeString(im.Data)
		if err != nil {
			return nil, invalid("an image is not valid base64"), nil
		}
		if len(data) > maxImageBytes {
			return nil, &wire.Error{Code: wire.CodeTooLarge, Msg: "an image may be at most 8 MB"}, nil
		}
		name := path.Base(strings.ReplaceAll(im.Name, "\\", "/"))
		if name == "." || name == "/" || name == "" {
			name = "image"
		}
		images = append(images, remote.Image{Name: name, Mime: im.Mime, Data: data})
	}
	// A retry after a dropped connection must not post twice; the key is
	// remembered only once the send succeeded.
	key := ""
	if req.IdempotencyKey != "" {
		key = c.dev.ID + ":" + req.SessionID + ":" + req.IdempotencyKey
		if id, dup := c.s.idem.lookup(key); dup {
			return wire.ChatSendResult{MessageID: id}, nil, nil
		}
	}
	id, err := c.s.be.Send(ctx, req.SessionID, req.Text, images)
	if err != nil {
		return nil, fail(wire.MChatSend, err), nil
	}
	if key != "" {
		c.s.idem.record(key, id)
	}
	return wire.ChatSendResult{MessageID: id}, nil, nil
}

func (c *conn) devicesList() (any, *wire.Error, func()) {
	devs, err := c.s.store.List()
	if err != nil {
		return nil, fail(wire.MDevicesList, err), nil
	}
	out := make([]wire.Device, 0, len(devs))
	for _, d := range devs {
		out = append(out, d.Wire(d.ID == c.dev.ID))
	}
	return out, nil, nil
}

// devicesRevoke lets a phone revoke itself only (R10); the response goes
// out before the socket closes with 4403.
func (c *conn) devicesRevoke(p json.RawMessage) (any, *wire.Error, func()) {
	req, werr := decode[wire.IDParams](p)
	if werr != nil {
		return nil, werr, nil
	}
	if req.ID == "" {
		return nil, invalid("id is required"), nil
	}
	if req.ID != c.dev.ID {
		return nil, &wire.Error{Code: wire.CodeForbidden, Msg: "a phone can only unpair itself; use the desktop for others"}, nil
	}
	if err := c.s.store.Revoke(req.ID); err != nil {
		return nil, fail(wire.MDevicesRevoke, err), nil
	}
	return ok, nil, c.s.notifyChanged
}
