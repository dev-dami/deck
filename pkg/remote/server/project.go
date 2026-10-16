package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/auth"
	"github.com/dev-dami/deck/pkg/remote/hub"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// ProjectCaller runs the v2 project methods (sessions, messages, chat,
// queue, roster, models, mode, approvals, changes) against one Backend for
// the v3 daemon, so both protocols share one validation, capping and error
// mapping (R11, R12). It does not handle what is per connection or per
// computer: sessions.focus, devices.* and push.* belong to the caller, and
// mode.set and approvals.answer should pass through the caller's tier checks
// first.
type ProjectCaller struct {
	s *Server
}

// NewProjectCaller returns a caller over be.
func NewProjectCaller(be remote.Backend) *ProjectCaller {
	return &ProjectCaller{s: &Server{
		be:   be,
		hub:  hub.New(),
		idem: newIdem(time.Now),
		now:  time.Now,
	}}
}

// Call runs one method for the device. The result is ready to marshal; a
// non-nil error is what the phone should see.
func (p *ProjectCaller) Call(ctx context.Context, deviceID, method string, params json.RawMessage) (any, *wire.Error) {
	c := &conn{s: p.s, dev: auth.Device{ID: deviceID}}
	switch method {
	case wire.MSessionsFocus, wire.MDevicesList, wire.MDevicesRevoke,
		wire.MPushStatus, wire.MPushSubscribe, wire.MPushUnsubscribe, wire.MPushTest:
		return nil, invalid("unknown method")
	case wire.MModeSet:
		// Yolo is a per-device grant checked against the device store, which
		// this caller does not have; the daemon decides it and sets the mode
		// on the backend itself.
		if req, werr := decode[wire.ModeSetParams](params); werr == nil && req.Mode == wire.ModeYolo {
			return nil, &wire.Error{Code: wire.CodeYoloDisabled, Msg: "yolo needs a grant"}
		}
	}
	res, werr, _ := c.call(ctx, method, params)
	return res, werr
}
