package auth

import (
	"context"
	"time"

	"github.com/dev-dami/deck/internal/deck/peer"
)

// Limits are the two lockouts of R1: one per source address (peer's, keyed
// by IP or IPv6 /64) and one per device id, keyed only by ids that exist.
// There is no global one. The socket consults them only after a token
// lookup failed, so a valid token is never refused for someone else's
// failures; the pair route consults the source lockout first, by design,
// since a pairing code is short enough to guess. Both are pruned on a
// timer (R4).
type Limits struct {
	sources *peer.Lockout
	devices *peer.Lockout
	now     func() time.Time
}

// Lockout parameters: failures before a lockout, its first length, its cap.
const (
	SourceFailures = 5
	SourceLockout  = 30 * time.Second
	DeviceFailures = 10
	DeviceLockout  = time.Minute
	MaxLockout     = 15 * time.Minute
	pruneEvery     = time.Minute
)

// NewLimits returns empty limits; a nil now uses time.Now.
func NewLimits(now func() time.Time) *Limits {
	if now == nil {
		now = time.Now
	}
	return &Limits{
		sources: peer.NewLockout(SourceFailures, SourceLockout, MaxLockout, now),
		devices: peer.NewLockout(DeviceFailures, DeviceLockout, MaxLockout, now),
		now:     now,
	}
}

// Locked reports whether addr or device id is locked out and for how long.
func (l *Limits) Locked(addr, id string) (time.Duration, bool) {
	var until time.Time
	if t := l.sources.Until(peer.LockKey(addr)); t.After(until) {
		until = t
	}
	if id != "" {
		if t := l.devices.Until(id); t.After(until) {
			until = t
		}
	}
	if until.IsZero() {
		return 0, false
	}
	return until.Sub(l.now()), true
}

// Fail records a failed attempt from addr, for device id when the token
// named one that exists.
func (l *Limits) Fail(addr, id string) {
	l.sources.Fail(peer.LockKey(addr))
	if id != "" {
		l.devices.Fail(id)
	}
}

// OK clears addr and id after a successful attempt.
func (l *Limits) OK(addr, id string) {
	l.sources.OK(peer.LockKey(addr))
	if id != "" {
		l.devices.OK(id)
	}
}

// Prune drops stale records.
func (l *Limits) Prune() {
	l.sources.Prune()
	l.devices.Prune()
}

// Tracked is how many sources and devices are on record.
func (l *Limits) Tracked() int { return l.sources.Len() + l.devices.Len() }

// Run prunes every minute until ctx ends.
func (l *Limits) Run(ctx context.Context) {
	t := time.NewTicker(pruneEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.Prune()
		}
	}
}
