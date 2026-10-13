package peer

import (
	"net"
	"time"
)

// What the phone remote control (pkg/remote) borrows from the peers feature:
// the per-source lockout and the private-file helpers, so there is one of
// each in the repository.

// Lockout is the per-source failed-handshake lockout of lockout.go, exported
// for pkg/remote/auth.
type Lockout struct{ l *lockout }

// NewLockout returns a lockout that refuses a source after threshold
// failures for base, doubling up to max. A nil now uses time.Now.
func NewLockout(threshold int, base, max time.Duration, now func() time.Time) *Lockout {
	return &Lockout{l: newLockout(threshold, base, max, now)}
}

// Until is when key's lockout ends; the zero time when it is not locked.
func (l *Lockout) Until(key string) time.Time {
	l.l.mu.Lock()
	defer l.l.mu.Unlock()
	st := l.l.m[key]
	if st == nil || !l.l.now().Before(st.until) {
		return time.Time{}
	}
	return st.until
}

// Fail records a failed attempt from key.
func (l *Lockout) Fail(key string) { l.l.fail(key) }

// OK clears key after a successful attempt.
func (l *Lockout) OK(key string) { l.l.ok(key) }

// Prune drops stale records; call it on a timer.
func (l *Lockout) Prune() {
	l.l.mu.Lock()
	defer l.l.mu.Unlock()
	now := l.l.now()
	for ip, st := range l.l.m {
		if now.After(st.until) && now.Sub(st.last) > l.l.max {
			delete(l.l.m, ip)
		}
	}
}

// Len is how many sources are tracked.
func (l *Lockout) Len() int {
	l.l.mu.Lock()
	defer l.l.mu.Unlock()
	return len(l.l.m)
}

// LockKey is the lockout key for a remote address string: the IP for IPv4,
// the /64 for IPv6, the string itself when it is not an address.
func LockKey(addr string) string {
	return lockKey(strAddr(addr))
}

// strAddr is a net.Addr over a "host:port" string.
type strAddr string

func (a strAddr) Network() string { return "tcp" }
func (a strAddr) String() string  { return string(a) }

var _ net.Addr = strAddr("")

// RestrictPrivate limits path to the current user: mode bits on Unix, a
// one-entry DACL on Windows.
func RestrictPrivate(path string, isDir bool) error { return restrictPath(path, isDir) }

// WritePrivateFile writes data to path atomically, restricted to the
// current user before anything is written; the directory is created and
// restricted too.
func WritePrivateFile(path string, data []byte) error { return writePrivateFile(path, data, false) }
