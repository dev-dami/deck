package auth

import (
	"sync"
	"time"
)

// Limiter counts failed pair/auth attempts. A client address is blocked after
// PerAddr failures inside Window; once GlobalMax failures land inside
// GlobalWindow, pairing is disabled until restart. Secrets are 256-bit, so this
// guards against noise and DoS rather than guessing.
type Limiter struct {
	PerAddr      int
	Window       time.Duration
	GlobalMax    int
	GlobalWindow time.Duration

	now func() time.Time

	mu       sync.Mutex
	byAddr   map[string][]time.Time
	global   []time.Time
	disabled bool
}

// NewLimiter returns a Limiter with the defaults: 5 per address per minute and
// 20 overall per 10 minutes.
func NewLimiter(now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{
		PerAddr: 5, Window: time.Minute,
		GlobalMax: 20, GlobalWindow: 10 * time.Minute,
		now: now, byAddr: map[string][]time.Time{},
	}
}

func prune(ts []time.Time, cutoff time.Time) []time.Time {
	i := 0
	for i < len(ts) && ts[i].Before(cutoff) {
		i++
	}
	return ts[i:]
}

// Allowed reports whether addr may try again.
func (l *Limiter) Allowed(addr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.disabled {
		return false
	}
	l.byAddr[addr] = prune(l.byAddr[addr], l.now().Add(-l.Window))
	return len(l.byAddr[addr]) < l.PerAddr
}

// Fail records a failed attempt and reports whether pairing is now disabled
// for everyone.
func (l *Limiter) Fail(addr string) (disabled bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.byAddr[addr] = append(prune(l.byAddr[addr], now.Add(-l.Window)), now)
	l.global = append(prune(l.global, now.Add(-l.GlobalWindow)), now)
	if len(l.global) >= l.GlobalMax {
		l.disabled = true
	}
	return l.disabled
}

// Disabled reports whether the global lockout has tripped.
func (l *Limiter) Disabled() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.disabled
}
