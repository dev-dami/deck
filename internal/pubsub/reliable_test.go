package pubsub

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestReliableSlowSubscriberLosesNothing publishes thousands of events to
// a subscriber that reads slowly, alongside a lossy one that never reads.
// The reliable subscriber either gets every event in order, or is cut and
// told so; it is never fed a gap.
func TestReliableSlowSubscriberLosesNothing(t *testing.T) {
	const n = 20000
	b := NewBrokerWithOptions[int](64)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = b.Subscribe(ctx) // never read: every Publish drops for it
	sub := b.SubscribeReliable(ctx)

	var got []int
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range sub.C {
			got = append(got, ev.Payload)
			if len(got)%500 == 0 {
				time.Sleep(time.Millisecond) // a slow reader
			}
		}
	}()
	for i := range n {
		b.Publish(UpdatedEvent, i)
	}
	cancel()
	<-done

	for i, v := range got {
		if v != i {
			t.Fatalf("event %d is %d: a gap or reorder", i, v)
		}
	}
	if len(got) < n && !sub.Lost() {
		t.Fatalf("got %d of %d events and the subscription does not say it lost any", len(got), n)
	}
	if len(got) == n && sub.Lost() {
		t.Fatalf("got every event but the subscription says it lost some")
	}
	if sub.Lost() && b.CutCount() != 1 {
		t.Fatalf("CutCount = %d, want 1", b.CutCount())
	}
	if b.DropCount() != n-64 {
		t.Fatalf("DropCount = %d, want %d (the lossy subscriber's drops are still counted)", b.DropCount(), n-64)
	}
}

// TestReliableKeepsUpWhenBufferSuffices: a reader that keeps up, with
// concurrent publishers, gets every event and is never cut.
func TestReliableKeepsUpWhenBufferSuffices(t *testing.T) {
	const writers, per = 8, 2000
	b := NewBrokerWithOptions[int](writers * per)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub := b.SubscribeReliable(ctx)

	var wg sync.WaitGroup
	for w := range writers {
		wg.Go(func() {
			for i := range per {
				if i%2 == 0 {
					b.Publish(UpdatedEvent, w*per+i)
				} else {
					b.PublishMustDeliver(context.Background(), UpdatedEvent, w*per+i)
				}
			}
		})
	}
	wg.Wait()

	seen := make(map[int]bool, writers*per)
	last := make([]int, writers)
	for i := range last {
		last[i] = -1
	}
	for range writers * per {
		ev := <-sub.C
		w, i := ev.Payload/per, ev.Payload%per
		if i <= last[w] {
			t.Fatalf("writer %d: event %d after %d", w, i, last[w])
		}
		last[w] = i
		seen[ev.Payload] = true
	}
	if len(seen) != writers*per || sub.Lost() || b.CutCount() != 0 {
		t.Fatalf("saw %d distinct, lost=%v, cut=%d", len(seen), sub.Lost(), b.CutCount())
	}
}

// TestReliableResumeAfterCut: a cut subscriber resubscribes and gets
// every later event; the cut is the signal to rebuild from a snapshot.
func TestReliableResumeAfterCut(t *testing.T) {
	b := NewBrokerWithOptions[int](4)
	ctx := t.Context()
	sub := b.SubscribeReliable(ctx)
	for i := range 10 {
		b.Publish(UpdatedEvent, i)
	}
	var got []int
	for ev := range sub.C {
		got = append(got, ev.Payload)
	}
	if !sub.Lost() || len(got) != 4 {
		t.Fatalf("lost=%v got=%v, want cut after the 4 that fit", sub.Lost(), got)
	}
	again := b.SubscribeReliable(ctx)
	b.Publish(UpdatedEvent, 10)
	if ev := <-again.C; ev.Payload != 10 {
		t.Fatalf("after resubscribing got %d", ev.Payload)
	}
}

func TestReliableEndsWithoutLossOnCancelAndShutdown(t *testing.T) {
	b := NewBroker[int]()
	ctx, cancel := context.WithCancel(context.Background())
	sub := b.SubscribeReliable(ctx)
	cancel()
	for range sub.C {
	}
	if sub.Lost() {
		t.Fatal("a cancelled subscription is not a loss")
	}

	other := b.SubscribeReliable(context.Background())
	b.Shutdown()
	for range other.C {
	}
	if other.Lost() {
		t.Fatal("shutting down is not a loss")
	}
	b.Publish(UpdatedEvent, 1) // no panic on a shut broker
}

func TestCutReliablePropagatesUpstreamLoss(t *testing.T) {
	b := NewBroker[int]()
	ctx := t.Context()
	sub := b.SubscribeReliable(ctx)
	plain := b.Subscribe(ctx)
	b.CutReliable()
	for range sub.C {
	}
	if !sub.Lost() {
		t.Fatal("CutReliable must mark reliable subscribers lost")
	}
	b.Publish(UpdatedEvent, 7)
	if ev := <-plain; ev.Payload != 7 {
		t.Fatal("ordinary subscribers are untouched")
	}
}

func TestMustDeliverCutsReliableAfterTimeout(t *testing.T) {
	b := NewBrokerWithOptions[int](1)
	b.SetMustDeliverTimeout(5 * time.Millisecond)
	sub := b.SubscribeReliable(t.Context())
	b.PublishMustDeliver(context.Background(), UpdatedEvent, 1)
	b.PublishMustDeliver(context.Background(), UpdatedEvent, 2) // nobody reads
	var got []int
	for ev := range sub.C {
		got = append(got, ev.Payload)
	}
	if !sub.Lost() || len(got) != 1 || got[0] != 1 {
		t.Fatalf("lost=%v got=%v", sub.Lost(), got)
	}
}
