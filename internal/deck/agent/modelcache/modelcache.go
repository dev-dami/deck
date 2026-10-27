// Package modelcache holds an adapter's model list. Listing models spawns the
// agent's CLI, which can take seconds, so the list is loaded once and shared:
// concurrent callers wait on the same load, no lock is held while the CLI
// runs, and a caller that gives up does not cancel (or poison) the load.
package modelcache

import (
	"context"
	"sync"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// Cache is one adapter's model list. The zero value is ready to use.
type Cache struct {
	// RetryAfter is how long a failed load is remembered, so a picker opened
	// again right away does not spawn the CLI again; 0 remembers no failure.
	RetryAfter time.Duration

	mu      sync.Mutex
	list    []agent.ModelInfo
	err     error
	errAt   time.Time
	loading *load
	now     func() time.Time // tests
}

type load struct {
	done chan struct{}
	list []agent.ModelInfo
	err  error
}

// Get returns a copy of the cached list, loading it with fn if there is
// none. fn runs detached from ctx (it keeps ctx's values, and should bound
// itself with its own timeout): when ctx ends first Get returns ctx.Err()
// and the load goes on, its result kept for the next caller. A non-nil list
// with a nil error is kept for the Cache's lifetime.
func (c *Cache) Get(ctx context.Context, fn func(context.Context) ([]agent.ModelInfo, error)) ([]agent.ModelInfo, error) {
	c.mu.Lock()
	if c.list != nil {
		out := clone(c.list)
		c.mu.Unlock()
		return out, nil
	}
	if c.err != nil && c.clock().Sub(c.errAt) < c.RetryAfter {
		err := c.err
		c.mu.Unlock()
		return nil, err
	}
	l := c.loading
	if l == nil {
		l = &load{done: make(chan struct{})}
		c.loading = l
		go c.run(context.WithoutCancel(ctx), l, fn)
	}
	c.mu.Unlock()

	select {
	case <-l.done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if l.err != nil {
		return nil, l.err
	}
	return clone(l.list), nil
}

func (c *Cache) run(ctx context.Context, l *load, fn func(context.Context) ([]agent.ModelInfo, error)) {
	l.list, l.err = fn(ctx)
	c.mu.Lock()
	// A Reset during the load disowned it: its result is not kept.
	if c.loading == l {
		switch {
		case l.err != nil:
			c.err, c.errAt = l.err, c.clock()
		case l.list != nil:
			c.list, c.err = l.list, nil
		}
		c.loading = nil
	}
	c.mu.Unlock()
	close(l.done)
}

// Cached returns a copy of the list if one is loaded, else nil.
func (c *Cache) Cached() []agent.ModelInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.list == nil {
		return nil
	}
	return clone(c.list)
}

// Reset forgets the loaded list and any remembered failure, so the next Get
// loads again. A load under way still finishes for its own callers, but its
// result is not kept.
func (c *Cache) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.list, c.err, c.errAt = nil, nil, time.Time{}
	c.loading = nil
}

func (c *Cache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func clone(l []agent.ModelInfo) []agent.ModelInfo {
	return append([]agent.ModelInfo(nil), l...)
}
