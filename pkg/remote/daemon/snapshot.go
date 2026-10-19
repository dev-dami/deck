package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/dev-dami/deck/pkg/remote/devstore"
	"github.com/dev-dami/deck/pkg/remote/e2e"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

const (
	defaultSnapshotDebounce = 10 * time.Second
	// maxSnapshotPlain keeps a snapshot inside one Noise message (65535 bytes
	// less the 16-byte tag and the handshake's ephemeral key and static
	// encryption), so the phone opens it with the one-shot K reader.
	maxSnapshotPlain = 60000
)

// SnapshotSink stores an encrypted snapshot for a device; the relay client
// implements it.
type SnapshotSink interface {
	Snapshot(deviceHash string, data []byte) error
}

// BuildSnapshot is what a phone shows greyed while the computer is away: the
// inbox as that device sees it, the approvals waiting, and the latest
// receipts. It is trimmed to fit one sealed message, dropping the least
// useful first: approval bodies, then older receipts, then the inbox tail.
func (d *Daemon) BuildSnapshot(ctx context.Context, dev devstore.Device) wire.Snapshot {
	snap := wire.Snapshot{
		At: d.cfg.Now().UTC(), Inbox: d.Inbox(ctx, dev),
		Approvals: []wire.PendingApproval{}, Receipts: []wire.Receipt{},
	}
	for _, ref := range d.projectRefs() {
		p := d.provider(ref.ID)
		if p == nil {
			continue
		}
		list, err := p.be.Approvals("")
		if err != nil {
			continue
		}
		for _, a := range list {
			v := d.v3Approval(ref.ID, a)
			snap.Approvals = append(snap.Approvals, wire.PendingApproval{PJ: ref.ID, Approval: v})
		}
	}
	if d.cfg.Queue != nil {
		if rs, err := d.cfg.Queue.Receipts(dev.ID, wire.MaxSnapshotReceipts); err == nil {
			snap.Receipts = rs
		}
	}
	return fit(snap)
}

func size(s wire.Snapshot) int {
	raw, _ := json.Marshal(s)
	return len(raw)
}

func fit(s wire.Snapshot) wire.Snapshot {
	if size(s) <= maxSnapshotPlain {
		return s
	}
	for i := range s.Approvals {
		a := &s.Approvals[i].Approval.Approval
		a.Diff, a.Raw, a.DiffTruncated, a.RawTruncated = "", "", true, true
		a.Command, _ = wire.Truncate(a.Command, 512)
	}
	for size(s) > maxSnapshotPlain && len(s.Receipts) > 0 {
		s.Receipts = s.Receipts[:len(s.Receipts)-1]
	}
	for size(s) > maxSnapshotPlain && len(s.Inbox) > 0 {
		s.Inbox = s.Inbox[:len(s.Inbox)-1]
	}
	for size(s) > maxSnapshotPlain && len(s.Approvals) > 0 {
		s.Approvals = s.Approvals[:len(s.Approvals)-1]
	}
	return s
}

// SealSnapshot encrypts a snapshot to one device with Noise K, the computer as
// initiator, so only that phone can open it and the relay sees ciphertext.
func (d *Daemon) SealSnapshot(dev devstore.Device, s wire.Snapshot) ([]byte, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	return e2e.SealK(
		e2e.Keypair{Private: d.cfg.Identity.StaticPrivate(), Public: d.cfg.Identity.StaticPublic()},
		dev.Static, d.cfg.Identity.CID(), raw, nil)
}

// PushSnapshots writes a fresh snapshot for every paired phone.
func (d *Daemon) PushSnapshots(ctx context.Context) {
	if d.cfg.Snapshots == nil {
		return
	}
	for _, dev := range d.cfg.Devices.List() {
		sealed, err := d.SealSnapshot(dev, d.BuildSnapshot(ctx, dev))
		if err != nil {
			slog.Warn("remote: snapshot not sealed", "device", dev.ID, "error", err)
			continue
		}
		if err := d.cfg.Snapshots.Snapshot(dev.RelayTokenHash, sealed); err != nil {
			slog.Warn("remote: snapshot not stored", "device", dev.ID, "error", err)
		}
	}
}

// snapshotDirty asks RunSnapshots for a refresh.
func (d *Daemon) snapshotDirty() {
	select {
	case d.dirty <- struct{}{}:
	default:
	}
}

// RunSnapshots keeps the relay's snapshot slots fresh: after a change, once
// things have been quiet for the debounce, and whenever the relay link comes
// up (Refresh).
func (d *Daemon) RunSnapshots(ctx context.Context) {
	wait := d.cfg.SnapshotDebounce
	if wait == 0 {
		wait = defaultSnapshotDebounce
	}
	var timer <-chan time.Time
	for {
		select {
		case <-d.dirty:
			if timer == nil {
				timer = time.After(wait)
			}
		case <-timer:
			timer = nil
			d.PushSnapshots(ctx)
		case <-ctx.Done():
			return
		}
	}
}

// Refresh schedules a snapshot write, for the relay link coming up.
func (d *Daemon) Refresh() { d.snapshotDirty() }
