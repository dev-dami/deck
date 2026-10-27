package modelcache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

func TestConcurrentCallersShareOneLoad(t *testing.T) {
	var c Cache
	var calls atomic.Int32
	release := make(chan struct{})
	fn := func(context.Context) ([]agent.ModelInfo, error) {
		calls.Add(1)
		<-release
		return []agent.ModelInfo{{ID: "a"}}, nil
	}
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			if l, err := c.Get(context.Background(), fn); err != nil || len(l) != 1 {
				t.Errorf("Get = %v, %v", l, err)
			}
		})
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Fatalf("loaded %d times, want 1", n)
	}
	l, _ := c.Get(context.Background(), fn)
	l[0].ID = "mutated"
	if c.Cached()[0].ID != "a" {
		t.Fatal("a caller's copy changed the cache")
	}
}

func TestCallerGivingUpDoesNotStopTheLoad(t *testing.T) {
	var c Cache
	release := make(chan struct{})
	fn := func(ctx context.Context) ([]agent.ModelInfo, error) {
		<-release
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return []agent.ModelInfo{{ID: "a"}}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Get(ctx, fn); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	close(release)
	l, err := c.Get(context.Background(), fn)
	if err != nil || len(l) != 1 {
		t.Fatalf("Get after give-up = %v, %v", l, err)
	}
}

func TestFailureRememberedBriefly(t *testing.T) {
	now := time.Unix(0, 0)
	c := Cache{RetryAfter: time.Second, now: func() time.Time { return now }}
	calls := 0
	fn := func(context.Context) ([]agent.ModelInfo, error) {
		calls++
		return nil, errors.New("boom")
	}
	for range 3 {
		if _, err := c.Get(context.Background(), fn); err == nil {
			t.Fatal("expected error")
		}
	}
	if calls != 1 {
		t.Fatalf("loaded %d times within RetryAfter, want 1", calls)
	}
	now = now.Add(2 * time.Second)
	_, _ = c.Get(context.Background(), fn)
	if calls != 2 {
		t.Fatalf("loaded %d times after RetryAfter, want 2", calls)
	}
	if c.Cached() != nil {
		t.Fatal("a failure was kept as the list")
	}
}

func TestResetLoadsAgainAndDropsALoadUnderWay(t *testing.T) {
	var c Cache
	var calls atomic.Int32
	fn := func(context.Context) ([]agent.ModelInfo, error) {
		n := calls.Add(1)
		return []agent.ModelInfo{{ID: string(rune('a' + n - 1))}}, nil
	}
	if l, _ := c.Get(context.Background(), fn); l[0].ID != "a" {
		t.Fatalf("first = %v", l)
	}
	c.Reset()
	if c.Cached() != nil {
		t.Fatal("Reset kept the list")
	}
	if l, _ := c.Get(context.Background(), fn); l[0].ID != "b" {
		t.Fatalf("after Reset = %v, want a new load", l)
	}

	// A load that finishes after a Reset is not kept.
	var d Cache
	release := make(chan struct{})
	finished := make(chan struct{})
	slow := func(context.Context) ([]agent.ModelInfo, error) {
		defer close(finished)
		<-release
		return []agent.ModelInfo{{ID: "stale"}}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = d.Get(ctx, slow)
	d.Reset()
	close(release)
	<-finished
	time.Sleep(10 * time.Millisecond)
	if d.Cached() != nil {
		t.Fatalf("kept %v from a load the Reset disowned", d.Cached())
	}
}
