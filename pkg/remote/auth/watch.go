package auth

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// PollInterval is how often Watch checks devices.json when the file system
// sends no notification (R16: the fallback, not the mechanism).
var PollInterval = 3 * time.Second

// Watch calls onChange, from its own goroutine, whenever devices.json
// changes after this call until ctx ends: on a file-system notification,
// else by a stat every PollInterval. Several changes close together may be
// reported once. The baseline is taken before Watch returns, so a caller
// that reads the file afterwards misses no change.
func (s *Store) Watch(ctx context.Context, onChange func()) {
	last := s.stamp()
	var notify chan fsnotify.Event
	if w, err := fsnotify.NewWatcher(); err == nil && w.Add(s.dir) == nil {
		notify = make(chan fsnotify.Event, 16)
		go func() {
			defer w.Close()
			for {
				select {
				case <-ctx.Done():
					return
				case ev, ok := <-w.Events:
					if !ok {
						return
					}
					if filepath.Base(ev.Name) == DevicesFile {
						select {
						case notify <- ev:
						default:
						}
					}
				case _, ok := <-w.Errors:
					if !ok {
						return
					}
				}
			}
		}()
	}
	go s.watch(ctx, last, notify, onChange)
}

// stamp is what a stat says about the file.
type stamp struct {
	mod  time.Time
	size int64
	ok   bool
}

func (s *Store) stamp() stamp {
	fi, err := os.Stat(s.path(DevicesFile))
	if err != nil {
		return stamp{}
	}
	return stamp{mod: fi.ModTime(), size: fi.Size(), ok: true}
}

func (s *Store) watch(ctx context.Context, last stamp, notify <-chan fsnotify.Event, onChange func()) {
	tick := time.NewTicker(PollInterval)
	defer tick.Stop()
	var settle <-chan time.Time
	check := func() {
		cur := s.stamp()
		if cur != last {
			last = cur
			onChange()
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-notify:
			// A rename lands as a burst: check once it settles, and keep
			// checking while writes keep coming.
			if settle == nil {
				settle = time.After(50 * time.Millisecond)
			}
		case <-settle:
			settle = nil
			check()
		case <-tick.C:
			check()
		}
	}
}
