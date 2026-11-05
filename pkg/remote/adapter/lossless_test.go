package adapter

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/message"
	"github.com/dev-dami/deck/internal/pubsub"
	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// TestLostSubscriptionSaysSoAndListensAgain: when the app cuts the
// adapter's subscription for falling behind, the adapter yields EvLost
// (so the feeds make their clients resync) and goes on with a fresh
// subscription instead of ending.
func TestLostSubscriptionSaysSoAndListensAgain(t *testing.T) {
	core, rc := newCore(), newRC()
	subs := make(chan chan pubsub.Event[tea.Msg], 2)
	var calls atomic.Int32
	var lost atomic.Bool
	events := func(context.Context) *pubsub.Subscription[tea.Msg] {
		calls.Add(1)
		ch := make(chan pubsub.Event[tea.Msg], 4)
		subs <- ch
		return pubsub.NewSubscription(ch, lost.Load)
	}
	b := NewWith(core, rc, nil, fakeRoster{}, events, "v", "proj")
	out := b.Events(t.Context())
	first := <-subs

	send := func(ch chan pubsub.Event[tea.Msg], id string) {
		ch <- pubsub.Event[tea.Msg]{Type: pubsub.UpdatedEvent, Payload: pubsub.Event[message.Message]{
			Type: pubsub.CreatedEvent, Payload: message.Message{ID: id, SessionID: "s1", Role: message.User},
		}}
	}
	next := func() remote.Event {
		select {
		case ev := <-out:
			return ev
		case <-time.After(time.Second):
			t.Fatal("no event")
			return remote.Event{}
		}
	}

	send(first, "m1")
	require.Equal(t, wire.EvMessageCreated, next().Name)
	lost.Store(true)
	close(first)
	require.Equal(t, remote.EvLost, next().Name)

	second := <-subs
	lost.Store(false)
	send(second, "m2")
	ev := next()
	require.Equal(t, wire.EvMessageCreated, ev.Name)
	require.Equal(t, "m2", ev.Payload.(wire.Message).ID)
	require.Equal(t, int32(2), calls.Load())

	// A close that is not a loss ends the stream, as before.
	close(second)
	select {
	case _, ok := <-out:
		require.False(t, ok)
	case <-time.After(time.Second):
		t.Fatal("the stream did not end")
	}
}

// TestHistoryOrderIgnoresIdsWithinASecond: rows of the same second come
// back in the order they were made (seq), not by their random ids, on
// every page.
func TestHistoryOrderIgnoresIdsWithinASecond(t *testing.T) {
	r := newRig(t)
	ids := []string{"f3", "a9", "c1", "0e", "b7"} // made in this order
	for i, id := range ids {
		r.core.messages["s1"] = append(r.core.messages["s1"], message.Message{
			ID: id, SessionID: "s1", Role: message.Assistant, CreatedAt: 1000, Seq: int64(i + 1),
		})
	}
	// The store may hand them over in any order.
	rows := r.core.messages["s1"]
	rows[0], rows[4] = rows[4], rows[0]

	ctx := context.Background()
	var got []string
	page, more, next, err := r.b.Messages(ctx, "s1", "", 2)
	require.NoError(t, err)
	for more {
		got = append(page2ids(page), got...)
		page, more, next, err = r.b.Messages(ctx, "s1", next, 2)
		require.NoError(t, err)
	}
	got = append(page2ids(page), got...)
	require.Equal(t, ids, got)
	require.Equal(t, int64(1), page[0].Order)
}

func page2ids(page []wire.Message) []string {
	out := make([]string, len(page))
	for i, m := range page {
		out[i] = m.ID
	}
	return out
}
