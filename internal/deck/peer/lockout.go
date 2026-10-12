package peer

import (
	"net"
	"sync"
	"time"
)

// lockout tracks failed handshakes per source: a remote IP, or an IP and a
// key fingerprint. After threshold failures the source is refused for a
// period that doubles with every lockout, from base up to max. A successful
// handshake clears the source's record. The table holds maxTracked sources;
// when all of them are locked the one that failed longest ago is forgotten.
type lockout struct {
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

// lockKey is the lockout key for a remote address: the IP for IPv4, the /64
// for IPv6 (one host can hold a whole /64).
func lockKey(addr net.Addr) string {
	host := addr.String()
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

func newLockout(threshold int, base, max time.Duration, now func() time.Time) *lockout {
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
	return &lockout{m: map[string]*lockState{}, threshold: threshold, base: base, max: max, now: now}
}

// locked reports whether ip is refused right now.
func (l *lockout) locked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.m[ip]
	return st != nil && l.now().Before(st.until)
}

// fail records a failed handshake from ip.
func (l *lockout) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	st := l.m[ip]
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
		l.m[ip] = st
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

// ok clears ip after a successful handshake.
func (l *lockout) ok(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, ip)
}

// prune drops records that are not locked and older than max; when that is
// not enough it drops every record that is not locked.
func (l *lockout) prune(now time.Time) {
	for ip, st := range l.m {
		if now.After(st.until) && now.Sub(st.last) > l.max {
			delete(l.m, ip)
		}
	}
	if len(l.m) < maxTracked {
		return
	}
	for ip, st := range l.m {
		if now.After(st.until) {
			delete(l.m, ip)
		}
	}
}

// evictOldest drops the record whose last failure is the oldest.
func (l *lockout) evictOldest() {
	var (
		oldest string
		at     time.Time
		found  bool
	)
	for ip, st := range l.m {
		if !found || st.last.Before(at) {
			oldest, at, found = ip, st.last, true
		}
	}
	if found {
		delete(l.m, oldest)
	}
}
