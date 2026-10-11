package notice

import "time"

// Toast timing.
const (
	// MaxToasts is how many toasts show at once; older ones make way.
	MaxToasts = 3
	// ToastTTL is how long a toast shows; ErrorTTL for errors.
	ToastTTL = 5 * time.Second
	ErrorTTL = 10 * time.Second
	// SlideIn is how long a toast takes to slide in when motion is on.
	SlideIn = 180 * time.Millisecond
)

// TTL is how long a notice's toast shows.
func TTL(l Level) time.Duration {
	if l == Error {
		return ErrorTTL
	}
	return ToastTTL
}

// Toast is one notice on screen.
type Toast struct {
	Notice Notice
	Shown  time.Time // when it appeared
	// left is how long it has to go; it runs down only while not paused.
	left    time.Duration
	resumed time.Time // when left last started running down
	paused  bool
}

// Remaining is how long the toast has left at now.
func (t Toast) Remaining(now time.Time) time.Duration {
	if t.paused {
		return t.left
	}
	return t.left - now.Sub(t.resumed)
}

// Paused reports whether the pointer is holding the toast.
func (t Toast) Paused() bool { return t.paused }

// Toasts are the notices on screen, newest first. The zero value is ready.
type Toasts struct {
	items []Toast
}

// Add shows a notice, dropping the oldest toast past MaxToasts.
func (ts *Toasts) Add(n Notice, now time.Time) {
	t := Toast{Notice: n, Shown: now, left: TTL(n.Level), resumed: now}
	ts.items = append([]Toast{t}, ts.items...)
	if len(ts.items) > MaxToasts {
		ts.items = ts.items[:MaxToasts]
	}
}

// List is the toasts on screen, newest first.
func (ts *Toasts) List() []Toast { return ts.items }

// Len is how many toasts are on screen.
func (ts *Toasts) Len() int { return len(ts.items) }

// Expire drops the toasts whose time is up and reports whether any went.
func (ts *Toasts) Expire(now time.Time) bool {
	kept := ts.items[:0]
	for _, t := range ts.items {
		if t.Remaining(now) > 0 {
			kept = append(kept, t)
		}
	}
	changed := len(kept) != len(ts.items)
	ts.items = kept
	return changed
}

// Dismiss drops one toast.
func (ts *Toasts) Dismiss(id int) bool {
	for i, t := range ts.items {
		if t.Notice.ID == id {
			ts.items = append(ts.items[:i], ts.items[i+1:]...)
			return true
		}
	}
	return false
}

// Clear drops every toast.
func (ts *Toasts) Clear() { ts.items = nil }

// Hover pauses the toast with id (0 for none) and lets the others run
// down again. It reports whether anything changed.
func (ts *Toasts) Hover(id int, now time.Time) bool {
	changed := false
	for i := range ts.items {
		t := &ts.items[i]
		hold := t.Notice.ID == id
		switch {
		case hold && !t.paused:
			t.left = t.Remaining(now)
			t.paused = true
			changed = true
		case !hold && t.paused:
			t.resumed = now
			t.paused = false
			changed = true
		}
	}
	return changed
}

// Running reports whether any toast is running down (so a timer is
// needed).
func (ts *Toasts) Running() bool {
	for _, t := range ts.items {
		if !t.paused {
			return true
		}
	}
	return false
}

// Next is how long until something about the toasts changes on its own:
// the soonest expiry, or the next slide-in frame while one is sliding in.
// It is 0 when nothing will.
func (ts *Toasts) Next(now time.Time, motion bool) time.Duration {
	var next time.Duration
	found := false
	for _, t := range ts.items {
		if motion && now.Sub(t.Shown) < SlideIn {
			return time.Second / 30
		}
		if t.paused {
			continue
		}
		if r := t.Remaining(now); !found || r < next {
			next, found = r, true
		}
	}
	if found && next < time.Millisecond {
		return time.Millisecond
	}
	return next
}

// SlideProgress is how far in a toast has slid at now, from 0 to 1. With
// motion off it is always 1.
func SlideProgress(t Toast, now time.Time, motion bool) float64 {
	if !motion {
		return 1
	}
	p := float64(now.Sub(t.Shown)) / float64(SlideIn)
	switch {
	case p < 0:
		return 0
	case p > 1:
		return 1
	}
	// Ease out.
	return 1 - (1-p)*(1-p)
}
