package hub

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

func drain(c *Client) []wire.Frame {
	var out []wire.Frame
	for {
		select {
		case f := <-c.Frames():
			out = append(out, f)
		default:
			return out
		}
	}
}

func TestR11_EveryEventHasASeq(t *testing.T) {
	h := New()
	c, replay, resync := h.Subscribe(nil)
	require.False(t, replay)
	require.False(t, resync)
	h.Emit(wire.EvBusy, wire.BusyChanged{SessionID: "s1", Busy: true})
	h.Emit(wire.EvDeviceRevoked, wire.DeviceRevoked{ID: "d1"})
	got := drain(c)
	require.Len(t, got, 2)
	for i, f := range got {
		require.Equal(t, wire.FrameEv, f.T)
		require.NotNil(t, f.Ev)
		require.Equal(t, uint64(i+1), f.Ev.Seq)
		b, err := json.Marshal(f)
		require.NoError(t, err)
		require.Contains(t, string(b), `"seq":`+string(rune('0'+i+1)))
	}
	require.Equal(t, wire.EvDeviceRevoked, got[1].Ev.E)
	require.JSONEq(t, `{"id":"d1"}`, string(got[1].P))
}

func TestScopedEventsFollowFocus(t *testing.T) {
	h := New()
	a, _, _ := h.Subscribe(&wire.Resume{Epoch: h.Epoch(), Focus: "s1"})
	b, _, _ := h.Subscribe(nil)
	require.Equal(t, "s1", a.Focus(), "focus is restored from hello.resume")
	h.Publish(remote.Event{Name: wire.EvMessageCreated, SessionID: "s1", Scoped: true, Payload: wire.Message{ID: "m"}})
	h.Publish(remote.Event{Name: wire.EvMessageCreated, SessionID: "s2", Scoped: true, Payload: wire.Message{ID: "n"}})
	h.Emit(wire.EvRosterChanged, wire.RosterChanged{SessionID: "s2"})
	require.Len(t, drain(a), 2, "its own session's rows and the unscoped event")
	require.Len(t, drain(b), 1, "only the unscoped event")
	h.Focus(b, "s2")
	h.Publish(remote.Event{Name: wire.EvMessageUpdated, SessionID: "s2", Scoped: true, Payload: wire.Message{ID: "n"}})
	require.Len(t, drain(b), 1)
	require.Empty(t, drain(a))
}

func TestResyncRules(t *testing.T) {
	h := New()
	for i := 0; i < 3; i++ {
		h.Emit(wire.EvBusy, wire.BusyChanged{SessionID: "s", Busy: i%2 == 0})
	}
	h.Publish(remote.Event{Name: wire.EvMessageCreated, SessionID: "s", Scoped: true, Payload: wire.Message{}})

	c, replay, resync := h.Subscribe(&wire.Resume{Epoch: "other", Seq: 1})
	require.True(t, resync, "another epoch")
	require.False(t, replay)
	require.Empty(t, drain(c))

	_, replay, resync = h.Subscribe(&wire.Resume{Epoch: h.Epoch(), Seq: 99})
	require.True(t, resync, "ahead of the hub")
	require.False(t, replay)

	_, replay, resync = h.Subscribe(&wire.Resume{Epoch: h.Epoch(), Seq: 4})
	require.False(t, resync, "up to date")
	require.False(t, replay)

	c, replay, resync = h.Subscribe(&wire.Resume{Epoch: h.Epoch(), Seq: 1})
	require.False(t, resync)
	require.True(t, replay)
	got := drain(c)
	require.Len(t, got, 2, "events 2 and 3; the scoped row is not replayed")
	require.Equal(t, uint64(2), got[0].Ev.Seq)
	require.Equal(t, uint64(3), got[1].Ev.Seq)

	c, replay, resync = h.Subscribe(&wire.Resume{Epoch: h.Epoch(), Seq: 0})
	require.False(t, resync)
	require.True(t, replay)
	require.Len(t, drain(c), 3)
}

func TestReplayFallsOutOfTheRing(t *testing.T) {
	h := New()
	for i := 0; i < RingSize+5; i++ {
		h.Emit(wire.EvBusy, wire.BusyChanged{})
	}
	_, _, resync := h.Subscribe(&wire.Resume{Epoch: h.Epoch(), Seq: 4})
	require.True(t, resync, "seq 5 left the ring")
	c, replay, resync := h.Subscribe(&wire.Resume{Epoch: h.Epoch(), Seq: uint64(RingSize + 4)})
	require.False(t, resync)
	require.True(t, replay)
	require.Len(t, drain(c), 1)
}

func TestR12_ReplayGoesThroughTheBoundedQueue(t *testing.T) {
	h := New()
	for i := 0; i < QueueSize+1; i++ {
		h.Emit(wire.EvBusy, wire.BusyChanged{})
	}
	c, replay, resync := h.Subscribe(&wire.Resume{Epoch: h.Epoch(), Seq: 0})
	require.True(t, resync, "more frames than the queue holds is a resync, never a kick")
	require.False(t, replay)
	select {
	case <-c.Kicked():
		t.Fatal("replay must not kick a phone")
	default:
	}
	require.Empty(t, drain(c))

	c, replay, resync = h.Subscribe(&wire.Resume{Epoch: h.Epoch(), Seq: 1})
	require.False(t, resync)
	require.True(t, replay)
	require.Len(t, drain(c), QueueSize, "exactly a queue's worth is replayed")
}

func TestSlowClientIsKicked(t *testing.T) {
	h := New()
	c, _, _ := h.Subscribe(nil)
	for i := 0; i < QueueSize; i++ {
		h.Emit(wire.EvBusy, wire.BusyChanged{})
	}
	select {
	case <-c.Kicked():
		t.Fatal("a full queue is not yet too slow")
	default:
	}
	h.Emit(wire.EvBusy, wire.BusyChanged{})
	<-c.Kicked()
	h.Unsubscribe(c)
	require.Zero(t, h.Clients())
}
