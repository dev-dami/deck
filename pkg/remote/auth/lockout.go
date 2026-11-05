package auth

import (
	"net"
	"sync"
	"time"
)

// Lockout tracks failed handshakes per source: a remote IP, or an IP and a
// key fingerprint. After threshold failures the source is refused for a
// period that doubles with every lockout, from base up to max. A successful
// handshake clears the source's record. The table holds maxTracked sources;
// when all of them are locked the one that failed longest ago is forgotten.
type Lockout struct {
	mu        sync.Mutex
	m         map[string]*lockState
	threshold int
	base, max time.Duration
	now       func() time.Time
}

type lockState struct {
	fails   int
	strikes int
	until   time.Time
	last    time.Time
}

// maxTracked bounds the table; beyond it, stale entries are dropped, then the
// oldest.
const maxTracked = 4096

// LockKey is the lockout key for a remote address string: the IP for IPv4,
// the /64 for IPv6 (one host can hold a whole /64), the string itself when it
// is not an address.
func LockKey(addr string) string {
	host := addr
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return host
	}
	if ip4 := ip.To4(); ip4 != nil {
		return ip4.String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

// NewLockout returns a lockout that refuses a source after threshold
// failures for base, doubling up to max. A nil now uses time.Now.
func NewLockout(threshold int, base, max time.Duration, now func() time.Time) *Lockout {
	if threshold <= 0 {
		threshold = 5
	}
	if base <= 0 {
		base = 30 * time.Second
	}
	if max < base {
		max = 15 * time.Minute
	}
	if now == nil {
		now = time.Now
	}
	return &Lockout{m: map[string]*lockState{}, threshold: threshold, base: base, max: max, now: now}
}

// Locked reports whether key is refused right now.
func (l *Lockout) Locked(key string) bool { return !l.Until(key).IsZero() }

// Until is when key's lockout ends; the zero time when it is not locked.
func (l *Lockout) Until(key string) time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.m[key]
	if st == nil || !l.now().Before(st.until) {
		return time.Time{}
	}
	return st.until
}

// Fail records a failed attempt from key.
func (l *Lockout) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	st := l.m[key]
	if st == nil {
		if len(l.m) >= maxTracked {
			l.prune(now)
			if len(l.m) >= maxTracked {
				// Full of live lockouts: make room by forgetting the
				// source that failed longest ago, rather than letting a new
				// source fail without limit.
				l.evictOldest()
			}
		}
		st = &lockState{}
		l.m[key] = st
	}
	st.last = now
	st.fails++
	if st.fails >= l.threshold {
		st.fails = 0
		st.strikes++
		d := l.base
		for i := 1; i < st.strikes && d < l.max; i++ {
			d *= 2
		}
		if d > l.max {
			d = l.max
		}
		st.until = now.Add(d)
	}
}

// OK clears key after a successful attempt.
func (l *Lockout) OK(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, key)
}

// Prune drops stale records; call it on a timer.
func (l *Lockout) Prune() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for key, st := range l.m {
		if now.After(st.until) && now.Sub(st.last) > l.max {
			delete(l.m, key)
		}
	}
}

// Len is how many sources are tracked.
func (l *Lockout) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.m)
}

// prune drops records that are not locked and older than max; when that is
// not enough it drops every record that is not locked. The caller holds mu.
func (l *Lockout) prune(now time.Time) {
	for key, st := range l.m {
		if now.After(st.until) && now.Sub(st.last) > l.max {
			delete(l.m, key)
		}
	}
	if len(l.m) < maxTracked {
		return
	}
	for key, st := range l.m {
		if now.After(st.until) {
			delete(l.m, key)
		}
	}
}

// evictOldest drops the record whose last failure is the oldest. The caller
// holds mu.
func (l *Lockout) evictOldest() {
	var (
		oldest string
		at     time.Time
		found  bool
	)
	for key, st := range l.m {
		if !found || st.last.Before(at) {
			oldest, at, found = key, st.last, true
		}
	}
	if found {
		delete(l.m, oldest)
	}
}
