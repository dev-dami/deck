package app

import (
	"context"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/message"
	"github.com/dev-dami/deck/internal/permission"
	"github.com/dev-dami/deck/internal/pubsub"
	"github.com/dev-dami/deck/internal/session"
	"github.com/stretchr/testify/require"
)

// brokerMessages is a message.Service whose events come from a broker of
// the test's size.
type brokerMessages struct {
	message.Service
	b *pubsub.Broker[message.Message]
}

func (m brokerMessages) Subscribe(ctx context.Context) <-chan pubsub.Event[message.Message] {
	return m.b.Subscribe(ctx)
}

func (m brokerMessages) SubscribeReliable(ctx context.Context) *pubsub.Subscription[message.Message] {
	return m.b.SubscribeReliable(ctx)
}

type brokerSessions struct {
	session.Service
	b *pubsub.Broker[session.Session]
}

func (s brokerSessions) Subscribe(ctx context.Context) <-chan pubsub.Event[session.Session] {
	return s.b.Subscribe(ctx)
}

func (s brokerSessions) SubscribeReliable(ctx context.Context) *pubsub.Subscription[session.Session] {
	return s.b.SubscribeReliable(ctx)
}

func reliableApp(t *testing.T, buffer int) (*App, *pubsub.Broker[message.Message], *pubsub.Broker[session.Session]) {
	msgs := pubsub.NewBrokerWithOptions[message.Message](buffer)
	sess := pubsub.NewBrokerWithOptions[session.Session](buffer)
	return &App{
		Messages:    brokerMessages{b: msgs},
		Sessions:    brokerSessions{b: sess},
		Permissions: permission.NewPermissionService(t.TempDir(), false, nil),
	}, msgs, sess
}

func TestReliableEventsDeliversEveryEventInOrder(t *testing.T) {
	const n = 3000
	app, msgs, sess := reliableApp(t, n+1) // room for all: no cut
	ctx, cancel := context.WithCancel(context.Background())
	sub := app.ReliableEvents(ctx)

	go func() {
		for i := range n {
			msgs.Publish(pubsub.UpdatedEvent, message.Message{ID: "m", Seq: int64(i)})
		}
		sess.Publish(pubsub.CreatedEvent, session.Session{ID: "s"})
	}()
	next := int64(0)
	sawSession := false
	for next < n || !sawSession {
		select {
		case ev, ok := <-sub.C:
			require.True(t, ok, "ended at %d (lost=%v)", next, sub.Lost())
			switch p := ev.Payload.(type) {
			case pubsub.Event[message.Message]:
				require.Equal(t, next, p.Payload.Seq, "a gap or reorder")
				next++
			case pubsub.Event[session.Session]:
				sawSession = true
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("stalled at %d", next)
		}
	}
	cancel()
	for range sub.C {
	}
	require.False(t, sub.Lost(), "ending the context is not a loss")
}

// TestReliableEventsReportsLoss: a subscriber that stops reading is cut
// and told so; what it did receive is a gap-free prefix.
func TestReliableEventsReportsLoss(t *testing.T) {
	app, msgs, _ := reliableApp(t, 8)
	sub := app.ReliableEvents(t.Context())
	for i := range reliableEventsBuffer + 100 {
		msgs.Publish(pubsub.UpdatedEvent, message.Message{ID: "m", Seq: int64(i)})
	}
	next := int64(0)
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-sub.C:
			if !ok {
				require.True(t, sub.Lost(), "closed early without saying events were lost")
				require.Less(t, next, int64(reliableEventsBuffer+100))
				return
			}
			p := ev.Payload.(pubsub.Event[message.Message])
			require.Equal(t, next, p.Payload.Seq)
			next++
		case <-timeout:
			t.Fatal("the stalled subscriber was never cut")
		}
	}
}

func TestReliableEventsEndsCleanlyOnShutdown(t *testing.T) {
	app, msgs, _ := reliableApp(t, 8)
	sub := app.ReliableEvents(t.Context())
	msgs.Shutdown()
	for range sub.C {
	}
	require.False(t, sub.Lost())
}
