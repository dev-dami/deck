package app

import (
	"context"
	"sync"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"

	"github.com/dev-dami/deck/internal/message"
	"github.com/dev-dami/deck/internal/permission"
	"github.com/dev-dami/deck/internal/pubsub"
	"github.com/dev-dami/deck/internal/session"
)

// reliableSource is a service whose events can be subscribed to without
// loss; every service built on an embedded *pubsub.Broker is one.
type reliableSource[T any] interface {
	SubscribeReliable(ctx context.Context) *pubsub.Subscription[T]
}

// reliableEventsBuffer is the merged stream's buffer. Each source has a
// broker-sized buffer of its own in front of it.
const reliableEventsBuffer = 1024

// ReliableEvents streams what remote clients build their state from —
// message, session and permission events — in the shape [App.Events]
// delivers (each payload a pubsub.Event of the service's type, as a
// tea.Msg), but without ever dropping one. Each source is a reliable
// subscription read straight from its service, not through the lossy
// app-wide broker the TUI reads.
//
// The subscription's channel closes when ctx ends or the app shuts down,
// or as soon as any source cut this subscriber for falling behind. In
// the last case Lost reports true: events are missing, and the caller
// must make its clients resync before subscribing again.
func (app *App) ReliableEvents(ctx context.Context) *pubsub.Subscription[tea.Msg] {
	ctx, cancel := context.WithCancel(ctx)
	out := make(chan pubsub.Event[tea.Msg], reliableEventsBuffer)
	var lost atomic.Bool
	var wg sync.WaitGroup

	forward := func(start func(context.Context) (<-chan tea.Msg, func() bool)) {
		ch, sourceLost := start(ctx)
		wg.Go(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case msg, ok := <-ch:
					if !ok {
						if sourceLost() {
							lost.Store(true)
						}
						// One source ending ends the merged stream: a
						// subscriber hearing only some sources would
						// silently miss the others.
						cancel()
						return
					}
					select {
					case out <- pubsub.Event[tea.Msg]{Type: pubsub.UpdatedEvent, Payload: msg}:
					case <-ctx.Done():
						return
					}
				}
			}
		})
	}
	forward(reliably[session.Session](app.Sessions, app.Sessions.Subscribe))
	forward(reliably[message.Message](app.Messages, app.Messages.Subscribe))
	forward(reliably[permission.PermissionRequest](app.Permissions, app.Permissions.Subscribe))
	forward(reliably[permission.PermissionNotification](notifications(app.Permissions), app.Permissions.SubscribeNotifications))

	go func() {
		wg.Wait()
		cancel()
		close(out)
	}()
	return pubsub.NewSubscription(out, lost.Load)
}

// reliably subscribes to src reliably when it can. A source that cannot
// (a test double) falls back to its ordinary subscription, whose losses
// cannot be seen.
func reliably[T any](src any, plain func(context.Context) <-chan pubsub.Event[T]) func(context.Context) (<-chan tea.Msg, func() bool) {
	return func(ctx context.Context) (<-chan tea.Msg, func() bool) {
		var in <-chan pubsub.Event[T]
		lost := func() bool { return false }
		if r, ok := src.(reliableSource[T]); ok {
			sub := r.SubscribeReliable(ctx)
			in, lost = sub.C, sub.Lost
		} else {
			in = plain(ctx)
		}
		out := make(chan tea.Msg)
		go func() {
			defer close(out)
			for ev := range in {
				select {
				case out <- tea.Msg(ev):
				case <-ctx.Done():
					return
				}
			}
		}()
		return out, lost
	}
}

// reliableFunc makes a reliable subscribe method a reliableSource.
type reliableFunc[T any] func(context.Context) *pubsub.Subscription[T]

func (f reliableFunc[T]) SubscribeReliable(ctx context.Context) *pubsub.Subscription[T] {
	return f(ctx)
}

// notifications is the permission service's notification stream as a
// reliable source, or nil when the service cannot offer one.
func notifications(s permission.Service) any {
	r, ok := s.(interface {
		SubscribeNotificationsReliable(context.Context) *pubsub.Subscription[permission.PermissionNotification]
	})
	if !ok {
		return nil
	}
	return reliableFunc[permission.PermissionNotification](r.SubscribeNotificationsReliable)
}
