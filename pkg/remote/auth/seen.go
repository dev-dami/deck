package auth

import (
	"context"
	"time"
)

// Last-seen is written off the connect path (R16): Touch only notes the
// time, Flush writes what changed at most once a minute per device, and a
// lock that cannot be taken fails the write, never a connect.

// seenGranularity bounds how often a device's last_seen is written.
const seenGranularity = time.Minute

// Touch notes that device id was seen now.
func (s *Store) Touch(id string) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if last, ok := s.written[id]; ok && now.Sub(last) < seenGranularity {
		return
	}
	s.pending[id] = now
}

// Flush writes the pending last-seen times. It returns ErrLocked when the
// file was busy; the times stay pending for the next flush.
func (s *Store) Flush() error {
	s.mu.Lock()
	batch := s.pending
	s.pending = map[string]time.Time{}
	s.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	err := s.update(func(f *file) (bool, error) {
		changed := false
		for i := range f.Devices {
			if at, ok := batch[f.Devices[i].ID]; ok && at.After(f.Devices[i].LastSeen) {
				f.Devices[i].LastSeen = at.UTC().Truncate(time.Second)
				changed = true
			}
		}
		return changed, nil
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		for id, at := range batch {
			if _, ok := s.pending[id]; !ok {
				s.pending[id] = at
			}
		}
		return err
	}
	for id, at := range batch {
		s.written[id] = at
	}
	return nil
}

// Run flushes last-seen times every seenGranularity until ctx ends, then
// once more.
func (s *Store) Run(ctx context.Context) {
	t := time.NewTicker(seenGranularity)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = s.Flush()
			return
		case <-t.C:
			_ = s.Flush()
		}
	}
}
