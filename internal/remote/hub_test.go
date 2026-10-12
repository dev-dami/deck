package remote

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/remote/wire"
)

func ev(name string) Event { return Event{Name: name, Payload: map[string]string{"n": name}} }

func drain(s *subscriber) []wire.Frame {
	var out []wire.Frame
	for {
		select {
		case f := <-s.ch:
			out = append(out, f)
		default:
			return out
		}
	}
}

func TestPublishNumbersAndDelivers(t *testing.T) {
	h := NewHub()
	sub, _, _ := h.Subscribe(nil)
	h.Publish(ev("busy"))
	h.Publish(ev("notice"))
	got := drain(sub)
	require.Len(t, got, 2)
	require.Equal(t, uint64(1), got[0].Seq)
	require.Equal(t, "notice", got[1].E)
	require.Equal(t, wire.FrameEv, got[0].T)
	require.JSONEq(t, `{"n":"busy"}`, string(got[0].P))
}

func TestScopedEventsFollowFocus(t *testing.T) {
	h := NewHub()
	a, _, _ := h.Subscribe(nil)
	b, _, _ := h.Subscribe(nil)
	h.Focus(a, "s1")
	h.Focus(b, "s2")
	h.Publish(Event{Name: wire.EvMessageUpdated, SessionID: "s1", Scoped: true, Payload: 1})
	h.Publish(Event{Name: wire.EvSessionUpdated, SessionID: "s1", Payload: 2})
	require.Len(t, drain(a), 2)
	got := drain(b)
	require.Len(t, got, 1, "unfocused phone only hears the session summary")
	require.Equal(t, wire.EvSessionUpdated, got[0].E)
}

func TestUnfocusedHearsNoScopedEvents(t *testing.T) {
	h := NewHub()
	s, _, _ := h.Subscribe(nil)
	h.Publish(Event{Name: wire.EvMessageCreated, SessionID: "s1", Scoped: true, Payload: 1})
	require.Empty(t, drain(s))
}

func TestResumeReplaysMissedEvents(t *testing.T) {
	h := NewHub()
	for _, n := range []string{"a", "b", "c"} {
		h.Publish(ev(n))
	}
	_, replay, resync := h.Subscribe(&wire.Resume{Epoch: h.Epoch(), Seq: 1})
	require.False(t, resync)
	require.Len(t, replay, 2)
	require.Equal(t, "b", replay[0].E)
	require.Equal(t, "c", replay[1].E)
}

func TestResumeAtHeadReplaysNothing(t *testing.T) {
	h := NewHub()
	h.Publish(ev("a"))
	_, replay, resync := h.Subscribe(&wire.Resume{Epoch: h.Epoch(), Seq: 1})
	require.False(t, resync)
	require.Empty(t, replay)
}

func TestResumeResyncs(t *testing.T) {
	h := NewHub()
	h.Publish(ev("a"))
	_, _, resync := h.Subscribe(&wire.Resume{Epoch: "other", Seq: 1})
	require.True(t, resync, "different epoch")
	_, _, resync = h.Subscribe(&wire.Resume{Epoch: h.Epoch(), Seq: 99})
	require.True(t, resync, "ahead of the server")
}

func TestResumeFallenOutOfRingResyncs(t *testing.T) {
	h := NewHub()
	for range ringSize + 10 {
		h.Publish(ev("x"))
	}
	_, _, resync := h.Subscribe(&wire.Resume{Epoch: h.Epoch(), Seq: 3})
	require.True(t, resync)
	_, replay, resync := h.Subscribe(&wire.Resume{Epoch: h.Epoch(), Seq: h.Seq() - 5})
	require.False(t, resync)
	require.Len(t, replay, 5)
}

func TestReplaySkipsScopedRows(t *testing.T) {
	h := NewHub()
	h.Publish(Event{Name: wire.EvMessageUpdated, SessionID: "s1", Scoped: true, Payload: 1})
	h.Publish(ev("busy"))
	_, replay, _ := h.Subscribe(&wire.Resume{Epoch: h.Epoch(), Seq: 0})
	require.Len(t, replay, 1)
	require.Equal(t, "busy", replay[0].E)
}

func TestSlowSubscriberIsKickedNotWaitedFor(t *testing.T) {
	h := NewHub()
	slow, _, _ := h.Subscribe(nil)
	fast, _, _ := h.Subscribe(nil)
	for range subBuffer + 5 {
		h.Publish(ev("x"))
		drain(fast)
	}
	select {
	case <-slow.kicked:
	default:
		t.Fatal("slow subscriber was not kicked")
	}
	select {
	case <-fast.kicked:
		t.Fatal("fast subscriber was kicked")
	default:
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	h := NewHub()
	s, _, _ := h.Subscribe(nil)
	h.Unsubscribe(s)
	h.Publish(ev("a"))
	require.Empty(t, drain(s))
}

func TestUnserializablePayloadIsDropped(t *testing.T) {
	h := NewHub()
	s, _, _ := h.Subscribe(nil)
	h.Publish(Event{Name: "bad", Payload: make(chan int)})
	require.Empty(t, drain(s))
	require.Equal(t, uint64(0), h.Seq())
}
