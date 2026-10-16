package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/dev-dami/deck/pkg/remote/devstore"
	"github.com/dev-dami/deck/pkg/remote/push"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

const (
	approvalPushTTL = time.Hour
	donePushTTL     = 6 * time.Hour
	pushBodyRunes   = 140
)

// Pusher sends an already encrypted web push to a device through the relay;
// the relay client implements it.
type Pusher interface {
	Push(deviceHash string, data []byte, ttl time.Duration, urgent bool) error
}

// registerPush stores a phone's push keys and choices. Keys that cannot
// encrypt a message are refused here, not discovered at the first approval.
func (c *connCtx) registerPush(p wire.PushRegisterParams) (any, *wire.Error) {
	if p.P256dh == "" && p.Auth == "" {
		if err := c.d.cfg.Devices.SetPush(c.dev.ID, nil); err != nil {
			return nil, &wire.Error{Code: wire.CodeForbidden, Msg: "This phone is no longer paired."}
		}
		return wire.Empty{}, nil
	}
	if _, err := push.Encrypt([]byte("ok"), push.Keys{P256dh: p.P256dh, Auth: p.Auth}, nil); err != nil {
		return nil, invalid("those notification keys are not valid")
	}
	prefs := &devstore.PushPrefs{P256dh: p.P256dh, Auth: p.Auth, Approvals: p.Approvals, Done: p.Done}
	if err := c.d.cfg.Devices.SetPush(c.dev.ID, prefs); err != nil {
		return nil, &wire.Error{Code: wire.CodeForbidden, Msg: "This phone is no longer paired."}
	}
	return wire.Empty{}, nil
}

// connected reports whether a phone has a live session, in which case it sees
// events itself and a push would only be noise.
func (d *Daemon) connected(deviceID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.live[deviceID] > 0
}

func (d *Daemon) track(deviceID string, delta int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.live[deviceID] += delta
	if d.live[deviceID] <= 0 {
		delete(d.live, deviceID)
	}
}

// notify tells every phone that wants it and is not already connected.
func (d *Daemon) notify(kind string, msg wire.PushMessage, ttl time.Duration, urgent bool) {
	if d.cfg.Pusher == nil {
		return
	}
	msg.CID = d.cfg.Identity.CID()
	raw, err := json.Marshal(msg)
	if err != nil {
		return
	}
	for _, dev := range d.cfg.Devices.List() {
		p := dev.Push
		if p == nil || d.connected(dev.ID) {
			continue
		}
		if (kind == wire.PushApproval && !p.Approvals) || (kind == wire.PushDone && !p.Done) {
			continue
		}
		body, err := push.Encrypt(raw, push.Keys{P256dh: p.P256dh, Auth: p.Auth}, nil)
		if err != nil {
			slog.Warn("remote: push not encrypted", "device", dev.ID, "error", err)
			continue
		}
		if err := d.cfg.Pusher.Push(dev.RelayTokenHash, body, ttl, urgent); err != nil {
			slog.Warn("remote: push not sent", "device", dev.ID, "error", err)
		}
	}
}

func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit-1]) + "…"
}

func (d *Daemon) projectName(pj string) string {
	if p, err := d.cfg.Projects.Get(pj); err == nil {
		return p.Name
	}
	return "Deck"
}

// onProjectEvent turns provider events into pushes.
func (d *Daemon) onProjectEvent(ctx context.Context, pj string, name string, payload any) {
	if d.cfg.Pusher == nil {
		return
	}
	switch name {
	case wire.EvApprovalRequested:
		var a wire.Approval
		if !decodeAny(payload, &a) || a.ID == "" {
			return
		}
		v := d.v3Approval(pj, a)
		body := "Needs your answer"
		switch a.Kind {
		case wire.ApprovalCommand:
			body = "Run: " + oneLine(a.Command, pushBodyRunes)
		case wire.ApprovalEdit:
			body = "Edit " + oneLine(a.Path, pushBodyRunes)
		default:
			if a.Description != "" {
				body = oneLine(a.Description, pushBodyRunes)
			}
		}
		d.notify(wire.PushApproval, wire.PushMessage{
			Kind: wire.PushApproval, PJ: pj, SessionID: a.SessionID, Title: d.projectName(pj), Body: body,
			ApprovalID: a.ID, Digest: v.Digest, Tier: v.Tier, At: d.cfg.Now().UTC(),
		}, approvalPushTTL, true)
	case wire.EvBusy:
		var b wire.BusyChanged
		if !decodeAny(payload, &b) || b.Busy {
			return
		}
		p := d.provider(pj)
		if p == nil {
			return
		}
		// A session that stopped because it is waiting on an approval has its
		// own push; only a session that is truly finished is "done".
		if pending, err := p.be.Approvals(b.SessionID); err != nil || len(pending) > 0 {
			return
		}
		title := "A session"
		if sessions, err := p.be.Sessions(ctx); err == nil {
			for _, s := range sessions {
				if s.ID == b.SessionID && s.Title != "" {
					title = oneLine(s.Title, 80)
				}
			}
		}
		d.notify(wire.PushDone, wire.PushMessage{
			Kind: wire.PushDone, PJ: pj, SessionID: b.SessionID, Title: d.projectName(pj),
			Body: title + " finished", At: d.cfg.Now().UTC(),
		}, donePushTTL, false)
	}
}

// decodeAny reads a payload that is either a typed value (in-process) or raw
// JSON (from a provider over IPC).
func decodeAny(payload any, out any) bool {
	var raw []byte
	switch v := payload.(type) {
	case json.RawMessage:
		raw = v
	case []byte:
		raw = v
	default:
		var err error
		if raw, err = json.Marshal(payload); err != nil {
			return false
		}
	}
	return json.Unmarshal(raw, out) == nil
}
