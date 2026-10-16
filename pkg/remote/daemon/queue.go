package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/devstore"
	"github.com/dev-dami/deck/pkg/remote/e2e"
	"github.com/dev-dami/deck/pkg/remote/queue"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

const (
	// queueSettle is how long the mailbox must be quiet before queued actions
	// run, so the whole mailbox is drained first (contract §6).
	defaultSettle = 2 * time.Second
	queueTick     = 500 * time.Millisecond
)

// QueuedVerifier checks the assertion on a queued action, where the nonce is
// the action's qid. PolicyGate implements it.
type QueuedVerifier interface {
	VerifyQueued(dev devstore.Device, actionDigest, qid string, as *wire.Assertion) *wire.Error
}

// Vetoer lets a person at the desk stop queued actions before they run (the
// TUI's 30-second banner). It returns the qids to cancel; it is called with
// the batch that is about to run and may block for the veto window.
type Vetoer func(ctx context.Context, batch []wire.QueuedAction) map[string]bool

// HandleMail receives one queued message from the relay: it opens it with the
// sending phone's pinned key, stores the action, and returns nil only once it
// is durably stored (or can never be). The relay deletes the mail on nil.
func (d *Daemon) HandleMail(_ context.Context, id, deviceHash string, data []byte) error {
	dev, err := d.cfg.Devices.ByTokenHash(deviceHash)
	if err != nil {
		slog.Warn("remote: mail from an unknown phone dropped", "id", id)
		return nil
	}
	payload, err := e2e.OpenK(
		e2e.Keypair{Private: d.cfg.Identity.StaticPrivate(), Public: d.cfg.Identity.StaticPublic()},
		dev.Static, d.cfg.Identity.CID(), data)
	if err != nil {
		slog.Warn("remote: mail that did not authenticate dropped", "id", id, "device", dev.ID)
		return nil
	}
	var a wire.QueuedAction
	if json.Unmarshal(payload, &a) != nil || a.Validate() != nil || a.DeviceID != dev.ID {
		slog.Warn("remote: malformed queued action dropped", "id", id, "device", dev.ID)
		return nil
	}
	now := d.cfg.Now()
	reject := func(reason string) error {
		return d.cfg.Queue.Reject(wire.Receipt{QID: a.QID, Status: wire.ReceiptDropped, Reason: reason, At: now}, dev.ID)
	}
	// A phone with a fast clock must not buy a longer life than the cap.
	if a.ExpiresAt.After(now.Add(wire.MaxQueueExpiry + wire.ClockSkewTolerance)) {
		return reject(wire.ReasonExpired)
	}
	_, err = d.cfg.Queue.Enqueue(a, now)
	if errors.Is(err, queue.ErrFull) {
		return reject(wire.ReasonBusy)
	}
	return err
}

// RunQueue executes queued actions as they settle, until ctx ends.
func (d *Daemon) RunQueue(ctx context.Context) {
	t := time.NewTicker(queueTick)
	defer t.Stop()
	prune := time.NewTicker(time.Hour)
	defer prune.Stop()
	for {
		select {
		case <-t.C:
			d.drainQueue(ctx)
		case <-prune.C:
			if err := d.cfg.Queue.Prune(d.cfg.Now()); err != nil {
				slog.Warn("remote: queue prune", "error", err)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (d *Daemon) drainQueue(ctx context.Context) {
	if d.cfg.Queue == nil {
		return
	}
	settle := d.cfg.QueueSettle
	if settle == 0 {
		settle = defaultSettle
	}
	last, err := d.cfg.Queue.LastReceived()
	if err != nil || (!last.IsZero() && d.cfg.Now().Sub(last) < settle) {
		return
	}
	pending, err := d.cfg.Queue.Pending()
	if err != nil || len(pending) == 0 {
		return
	}
	now := d.cfg.Now()

	var changed []wire.Receipt
	finish := func(a wire.QueuedAction, status, reason string) {
		r := wire.Receipt{QID: a.QID, Status: status, Reason: reason, At: d.cfg.Now()}
		if ok, err := d.cfg.Queue.Complete(r, a.DeviceID); err != nil {
			slog.Warn("remote: receipt not stored", "qid", a.QID, "error", err)
		} else if ok {
			changed = append(changed, r)
		}
	}

	// Cancels first: they apply to what was queued before them.
	for _, a := range pending {
		if a.Kind != wire.QKindCancel {
			continue
		}
		var c wire.QueueCancelPayload
		if json.Unmarshal(a.Payload, &c) == nil && c.QID != "" {
			if _, err := d.cfg.Queue.Cancel(a.DeviceID, c.QID, now); err != nil {
				slog.Warn("remote: cancel", "error", err)
			}
		}
		finish(a, wire.ReceiptApplied, "")
	}
	// Re-read: cancels may have removed some.
	live, err := d.cfg.Queue.Pending()
	if err != nil {
		return
	}

	var runnable []wire.QueuedAction
	for _, a := range live {
		if a.Kind == wire.QKindCancel {
			continue
		}
		if now.After(a.ExpiresAt.Add(wire.ClockSkewTolerance)) {
			finish(a, wire.ReceiptDropped, wire.ReasonExpired)
			continue
		}
		if a.PJ != "" && !d.ensureProvider(a.PJ) {
			// The project's Deck is not running yet (one is being started);
			// keep waiting until it is or the action expires.
			continue
		}
		runnable = append(runnable, a)
	}

	vetoed := map[string]bool{}
	if d.cfg.Veto != nil && len(runnable) > 0 {
		vetoed = d.cfg.Veto(ctx, runnable)
		// The window may have been used to cancel from the command line.
		if still, err := d.cfg.Queue.Pending(); err == nil {
			waiting := map[string]bool{}
			for _, a := range still {
				waiting[a.QID] = true
			}
			runnable = slices.DeleteFunc(runnable, func(a wire.QueuedAction) bool { return !waiting[a.QID] })
		}
	}
	for _, a := range runnable {
		if ctx.Err() != nil {
			break
		}
		if vetoed[a.QID] {
			finish(a, wire.ReceiptDropped, wire.ReasonVetoed)
			continue
		}
		status, reason := d.runAction(ctx, a)
		finish(a, status, reason)
	}
	if len(changed) > 0 {
		d.hub.publish(event{name: wire.EvReceiptsChanged, payload: changed})
		d.snapshotDirty()
	}
}

func decodePayload[T any](raw json.RawMessage) (T, bool) {
	var v T
	if len(raw) == 0 {
		return v, true
	}
	return v, json.Unmarshal(raw, &v) == nil
}

// runAction carries out one queued action and says what happened.
func (d *Daemon) runAction(ctx context.Context, a wire.QueuedAction) (status, reason string) {
	p := d.provider(a.PJ)
	if _, err := d.cfg.Projects.Get(a.PJ); err != nil || p == nil {
		return wire.ReceiptDropped, wire.ReasonProjectDisabled
	}
	gone := func(err error) (string, string) {
		if errors.Is(err, remote.ErrNotFound) {
			return wire.ReceiptDropped, wire.ReasonSessionGone
		}
		slog.Warn("remote: queued action failed", "qid", a.QID, "kind", a.Kind, "error", err)
		return wire.ReceiptFailed, "failed"
	}

	switch a.Kind {
	case wire.QKindChatSend:
		pl, ok := decodePayload[struct {
			Text string `json:"text"`
		}](a.Payload)
		if !ok || strings.TrimSpace(pl.Text) == "" {
			return wire.ReceiptFailed, "invalid"
		}
		if _, err := p.be.Mode(a.SessionID); err != nil {
			return gone(err)
		}
		if _, err := p.be.Send(ctx, a.SessionID, pl.Text, nil); err != nil {
			return gone(err)
		}
	case wire.QKindChatContinue:
		sessions, err := p.be.Sessions(ctx)
		if err != nil {
			return gone(err)
		}
		found := false
		for _, s := range sessions {
			if s.ID != a.SessionID {
				continue
			}
			found = true
			if s.Busy {
				return wire.ReceiptDropped, wire.ReasonBusy
			}
		}
		if !found {
			return wire.ReceiptDropped, wire.ReasonSessionGone
		}
		if err := p.be.Continue(a.SessionID); err != nil {
			return gone(err)
		}
	case wire.QKindModelsSet:
		pl, ok := decodePayload[wire.SessionNewPayload](a.Payload)
		if !ok || pl.Kind == "" || pl.Model == "" {
			return wire.ReceiptFailed, "invalid"
		}
		if err := p.be.SetModel(a.SessionID, pl.Kind, pl.Model, pl.Effort); err != nil {
			return gone(err)
		}
	case wire.QKindSessionNew:
		pl, ok := decodePayload[wire.SessionNewPayload](a.Payload)
		if !ok || strings.TrimSpace(pl.Text) == "" {
			return wire.ReceiptFailed, "invalid"
		}
		s, err := p.be.CreateSession(ctx, pl.Title)
		if err != nil {
			return gone(err)
		}
		if pl.Kind != "" && pl.Model != "" {
			if err := p.be.SetModel(s.ID, pl.Kind, pl.Model, pl.Effort); err != nil {
				slog.Warn("remote: queued session model not set", "error", err)
			}
		}
		if _, err := p.be.Send(ctx, s.ID, pl.Text, nil); err != nil {
			return gone(err)
		}
	case wire.QKindApproval:
		return d.runApproval(ctx, p, a)
	default:
		return wire.ReceiptFailed, "invalid"
	}
	return wire.ReceiptApplied, ""
}

// runApproval answers the exact approval the person saw: same id, same
// digest, and for a destructive allow a valid assertion over the qid.
func (d *Daemon) runApproval(ctx context.Context, p *provider, a wire.QueuedAction) (string, string) {
	pl, ok := decodePayload[struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason,omitempty"`
	}](a.Payload)
	if !ok || (wire.ApprovalsAnswerParams{Decision: pl.Decision}).Validate() != nil || a.Precond == nil {
		return wire.ReceiptFailed, "invalid"
	}
	pending, err := p.be.Approvals(a.SessionID)
	if err != nil {
		return wire.ReceiptDropped, wire.ReasonSessionGone
	}
	var target *wire.ApprovalV3
	for _, ap := range pending {
		if ap.ID == a.Precond.ApprovalID && ap.SessionID == a.SessionID {
			v := d.v3Approval(a.PJ, ap)
			target = &v
		}
	}
	if target == nil {
		return wire.ReceiptDropped, wire.ReasonApprovalGone
	}
	if target.Digest != a.Precond.Digest {
		return wire.ReceiptDropped, wire.ReasonDigestMismatch
	}
	if pl.Decision != wire.DecisionDeny && target.Tier == wire.TierDestructive {
		dev, err := d.cfg.Devices.Get(a.DeviceID)
		if err != nil {
			return wire.ReceiptDropped, wire.ReasonAssertion
		}
		v, ok := d.cfg.Gate.(QueuedVerifier)
		if !ok || v.VerifyQueued(dev, target.Digest, a.QID, a.Assertion) != nil {
			return wire.ReceiptDropped, wire.ReasonAssertion
		}
	}
	resolved, err := p.be.Answer(a.SessionID, a.Precond.ApprovalID, pl.Decision)
	if err != nil || !resolved {
		return wire.ReceiptDropped, wire.ReasonApprovalGone
	}
	if pl.Decision == wire.DecisionDeny && pl.Reason != "" {
		if _, err := p.be.Send(ctx, a.SessionID, "Denied from phone: "+pl.Reason, nil); err != nil {
			slog.Warn("remote: deny reason not delivered", "error", err)
		}
	}
	return wire.ReceiptApplied, ""
}

// screen is a provider with someone looking at it.
type screen interface {
	HasUI() bool
	Notify(text string) error
}

// reviewWindow is the default Vetoer: if a terminal UI is open for a project
// in the batch, tell it what is about to run and give the person time to stop
// it. With nobody at a screen the work runs at once, since the phone's owner
// is the authority.
func (d *Daemon) reviewWindow(ctx context.Context, batch []wire.QueuedAction) map[string]bool {
	counts := map[string]int{}
	for _, a := range batch {
		counts[a.PJ]++
	}
	notified := false
	for pj, n := range counts {
		p := d.provider(pj)
		if p == nil {
			continue
		}
		s, ok := p.be.(screen)
		if !ok || !s.HasUI() {
			continue
		}
		what := "1 action from your phone runs"
		if n > 1 {
			what = fmt.Sprintf("%d actions from your phone run", n)
		}
		msg := what + " here in " + d.vetoWindow().Round(time.Second).String() + ". Stop them with: deck mobile queue cancel --all"
		if err := s.Notify(msg); err == nil {
			notified = true
		}
	}
	if notified {
		select {
		case <-time.After(d.vetoWindow()):
		case <-ctx.Done():
		}
	}
	return nil
}

func (d *Daemon) vetoWindow() time.Duration {
	if d.cfg.VetoWindow > 0 {
		return d.cfg.VetoWindow
	}
	return wire.QueueVetoWindow
}

// PendingActions lists what is waiting in the queue, for the command line.
func (d *Daemon) PendingActions() ([]wire.QueuedAction, error) {
	if d.cfg.Queue == nil {
		return nil, nil
	}
	return d.cfg.Queue.Pending()
}

// CancelAction cancels a waiting action from the command line.
func (d *Daemon) CancelAction(qid string) (bool, error) {
	if d.cfg.Queue == nil {
		return false, nil
	}
	_, ok, err := d.cfg.Queue.CancelAny(qid, d.cfg.Now())
	if ok {
		d.hub.publish(event{name: wire.EvReceiptsChanged, payload: []wire.Receipt{{QID: qid, Status: wire.ReceiptDropped, Reason: wire.ReasonCancelled, At: d.cfg.Now()}}})
		d.snapshotDirty()
	}
	return ok, err
}
