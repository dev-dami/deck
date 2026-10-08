package motion

import (
	"sort"
	"sync"
	"time"
)

const (
	budgetWindow    = 30
	budgetThreshold = 12 * time.Millisecond
)

// Budget watches frame render times and degrades to reduced motion when the
// terminal is too slow: if the p90 of the last 30 frames exceeds 12ms.
type Budget struct {
	mu       sync.Mutex
	samples  [budgetWindow]time.Duration
	n        int
	degraded bool
}

// NewBudget returns an empty Budget.
func NewBudget() *Budget { return &Budget{} }

// Observe records one frame's render duration.
func (b *Budget) Observe(d time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.samples[b.n%budgetWindow] = d
	b.n++
	if b.degraded || b.n < budgetWindow {
		return
	}
	s := make([]time.Duration, budgetWindow)
	copy(s, b.samples[:])
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	p90 := s[(budgetWindow*9+9)/10-1] // nearest rank
	if p90 > budgetThreshold {
		b.degraded = true
		SetReduced(true)
	}
}

// Degraded reports whether the budget has switched motion to reduced.
func (b *Budget) Degraded() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.degraded
}
