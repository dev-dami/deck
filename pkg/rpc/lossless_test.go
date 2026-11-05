package rpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/remotetest"
	"github.com/dev-dami/deck/pkg/remote/wire"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// engine is a backend with a live message store: like the real one, it
// changes its state first and then publishes the event saying so.
type engine struct {
	*remotetest.Fake
	events chan remote.Event

	mu    sync.Mutex // serialises change+publish, as one producer does
	msgs  map[string]wire.Message
	order int64
}

func newEngine() *engine {
	return &engine{Fake: remotetest.New(), events: make(chan remote.Event, 1<<16), msgs: map[string]wire.Message{}}
}

func (e *engine) Events(ctx context.Context) <-chan remote.Event {
	out := make(chan remote.Event)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-e.events:
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

func (e *engine) Messages(_ context.Context, sessionID, _ string, _ int) ([]wire.Message, bool, string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sorted(), false, "", nil
}

func (e *engine) sorted() []wire.Message {
	out := make([]wire.Message, 0, len(e.msgs))
	for _, m := range e.msgs {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out
}

// step makes one random change and publishes it.
func (e *engine) step(r *rand.Rand) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ids := make([]string, 0, len(e.msgs))
	for id := range e.msgs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	now := time.Unix(1700000000, 0) // every row in the same second
	switch op := r.IntN(10); {
	case len(ids) < 3 || (op < 2 && len(ids) < 60):
		e.order++
		m := wire.Message{ID: fmt.Sprintf("m%04d", r.IntN(10000)), SessionID: "s1", Role: wire.RoleAssistant,
			CreatedAt: now, UpdatedAt: now, Order: e.order, Parts: []wire.Part{{Type: wire.PartText, Text: ""}}}
		if _, dup := e.msgs[m.ID]; dup {
			return
		}
		e.msgs[m.ID] = m
		e.events <- remote.Event{Name: wire.EvMessageCreated, SessionID: "s1", Payload: m}
	case op < 3:
		id := ids[r.IntN(len(ids))]
		delete(e.msgs, id)
		e.events <- remote.Event{Name: wire.EvMessageDeleted, SessionID: "s1", Payload: wire.MessageDeleted{ID: id, SessionID: "s1"}}
	default:
		id := ids[r.IntN(len(ids))]
		m := e.msgs[id]
		parts := append([]wire.Part(nil), m.Parts...)
		parts[0].Text += fmt.Sprintf("w%d ", r.IntN(100))
		m.Parts = parts
		m.Finished = r.IntN(20) == 0
		e.msgs[id] = m
		e.events <- remote.Event{Name: wire.EvMessageUpdated, SessionID: "s1", Payload: m}
	}
}

func serveEngine(t *testing.T, be remote.Backend) (*Server, deckv1.DeckClient) {
	t.Helper()
	srv := New(be, "secret")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.Run(ctx)
	l, sock, closeSock, err := Listen(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g := srv.GRPC()
	go func() { _ = g.Serve(l) }()
	t.Cleanup(func() { g.Stop(); closeSock() })
	cc, c, err := Dial(sock, "secret")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	return srv, c
}

// clientState is a client's copy of the thread, built from a snapshot and
// events applied as upserts and deletions by id.
type clientState map[string]*deckv1.Message

func (cs clientState) apply(ev *deckv1.Event) error {
	switch {
	case ev.GetMessageCreated() != nil:
		cs[ev.GetMessageCreated().GetId()] = ev.GetMessageCreated()
	case ev.GetMessageUpdated() != nil:
		cs[ev.GetMessageUpdated().GetId()] = ev.GetMessageUpdated()
	case ev.GetMessageDeleted() != nil:
		delete(cs, ev.GetMessageDeleted().GetId())
	case ev.GetMessageAppendText() != nil:
		// The rule in deck.proto's MessageAppendText.
		a := ev.GetMessageAppendText()
		m, ok := cs[a.GetId()]
		if !ok {
			return nil // deleted before the snapshot was read: its deletion follows
		}
		if int(a.GetPart()) >= len(m.GetParts()) {
			return fmt.Errorf("append to part %d of %s, which has %d", a.GetPart(), a.GetId(), len(m.GetParts()))
		}
		m = proto.CloneOf(m)
		p := m.Parts[a.GetPart()]
		have, off, end := uint64(len(p.Text)), a.GetOffset(), a.GetOffset()+uint64(len(a.GetText()))
		switch {
		case have < off:
			return fmt.Errorf("append to %s at %d, the client has %d bytes", a.GetId(), off, have)
		case have < end:
			p.Text += a.GetText()[have-off:]
		}
		m.UpdatedAt = a.GetUpdatedAt()
		cs[a.GetId()] = m
	}
	return nil
}

func (cs clientState) list() []string {
	ms := make([]*deckv1.Message, 0, len(cs))
	for _, m := range cs {
		ms = append(ms, m)
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].GetOrder() < ms[j].GetOrder() })
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = fmt.Sprintf("%d %s %q %v", m.GetOrder(), m.GetId(), m.GetParts()[0].GetText(), m.GetFinished())
	}
	return out
}

func listOf(msgs []*deckv1.Message) []string {
	cs := clientState{}
	for _, m := range msgs {
		cs[m.GetId()] = m
	}
	return cs.list()
}

// TestSnapshotThenDeltasEqualsFinalState: a client that takes a snapshot
// while the engine is busy, subscribes from the snapshot's position and
// applies every event after it ends with exactly the engine's final
// state, in the engine's order, with no seq skipped.
func TestSnapshotThenDeltasEqualsFinalState(t *testing.T) {
	for _, seed := range []uint64{1, 2, 3, 4, 5} {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			e := newEngine()
			srv, c := serveEngine(t, e)
			ctx := ctxT(t)
			r := rand.New(rand.NewPCG(seed, seed))

			const steps = 1500
			snapAt := 200 + r.IntN(800)
			done := make(chan struct{})
			snapped := make(chan struct{})
			go func() {
				defer close(done)
				for i := range steps {
					if i == snapAt {
						<-snapped
					}
					e.step(r)
				}
			}()
			// Let the engine run ahead a little so events are in flight
			// when the snapshot is taken.
			for {
				if _, seq := srv.feed.position(); seq > 50 {
					break
				}
				time.Sleep(time.Millisecond)
			}
			snap, err := c.ListMessages(ctx, &deckv1.ListMessagesRequest{SessionId: "s1", Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			close(snapped)

			// One client takes whole messages, the other append-text
			// deltas where it can; both start from the same snapshot.
			type client struct {
				cs     clientState
				st     deckv1.Deck_EventsClient
				deltas int
			}
			var clients []*client
			for _, caps := range [][]string{nil, {CapAppendText}} {
				cl := &client{cs: clientState{}}
				for _, m := range snap.GetMessages() {
					cl.cs[m.GetId()] = m
				}
				cl.st, err = c.Events(ctx, &deckv1.Subscribe{Epoch: snap.GetEpoch(), Seq: snap.GetSeq(), Capabilities: caps})
				if err != nil {
					t.Fatal(err)
				}
				if ready := recv(t, cl.st).GetReady(); ready.GetResync() {
					t.Fatalf("resuming from the snapshot's position was refused: %v", ready)
				}
				clients = append(clients, cl)
			}
			<-done
			// A marker after the last change: once it arrives, every
			// change has.
			e.events <- remote.Event{Name: wire.EvBusy, SessionID: "end", Payload: wire.BusyChanged{SessionID: "end"}}
			want, err := c.ListMessages(ctx, &deckv1.ListMessagesRequest{SessionId: "s1", Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			for i, cl := range clients {
				for last := snap.GetSeq(); ; {
					ev := recv(t, cl.st)
					if ev.GetSeq() != last+1 {
						t.Fatalf("client %d: seq %d after %d: a gap", i, ev.GetSeq(), last)
					}
					last = ev.GetSeq()
					if ev.GetBusy().GetSessionId() == "end" {
						break
					}
					if ev.GetMessageAppendText() != nil {
						cl.deltas++
					}
					if err := cl.cs.apply(ev); err != nil {
						t.Fatalf("client %d: %v", i, err)
					}
				}
				got, exp := cl.cs.list(), listOf(want.GetMessages())
				if fmt.Sprint(got) != fmt.Sprint(exp) {
					t.Fatalf("client %d's state differs from the engine's:\n got %v\nwant %v", i, got, exp)
				}
			}
			if clients[0].deltas != 0 || clients[1].deltas == 0 {
				t.Fatalf("deltas: %d to the plain client, %d to the one that asked", clients[0].deltas, clients[1].deltas)
			}
		})
	}
}

// TestSlowSubscriberResumesOrResyncs publishes thousands of events to a
// subscriber that stalls. It is cut and reconnects from its last seq: it
// then either gets every later event, in order, or is told to resync.
// It is never handed a gap.
func TestSlowSubscriberResumesOrResyncs(t *testing.T) {
	e := newEngine()
	srv, c := serveEngine(t, e)
	ctx := ctxT(t)

	// Big enough that the transport's buffers cannot soak up the stall,
	// so the server's queue for the subscriber fills.
	const n = 8000
	body := strings.Repeat("x", 4096)
	st, err := c.Events(ctx, &deckv1.Subscribe{})
	if err != nil {
		t.Fatal(err)
	}
	ready := recv(t, st).GetReady()
	epoch, last := ready.GetEpoch(), ready.GetSeq()
	go func() {
		for i := range n {
			e.events <- remote.Event{Name: wire.EvMessageUpdated, SessionID: "s1", Payload: wire.Message{
				ID: fmt.Sprint("m", i%50), SessionID: "s1", Parts: []wire.Part{{Type: wire.PartText, Text: body}},
			}}
		}
	}()

	cuts, resyncs := 0, 0
	for last < n {
		ev, err := st.Recv()
		if err != nil {
			if status.Code(err) != codes.ResourceExhausted && !errors.Is(err, io.EOF) {
				t.Fatal(err)
			}
			cuts++
			st, err = c.Events(ctx, &deckv1.Subscribe{Epoch: epoch, Seq: last})
			if err != nil {
				t.Fatal(err)
			}
			r := recv(t, st).GetReady()
			if r.GetResync() {
				// Too far behind to replay: a client refetches and goes
				// on from the snapshot's position.
				resyncs++
				snap, err := c.ListSessions(ctx, &deckv1.Empty{})
				if err != nil {
					t.Fatal(err)
				}
				if snap.GetSeq() < r.GetSeq() {
					t.Fatalf("snapshot at %d is older than the stream's %d", snap.GetSeq(), r.GetSeq())
				}
				st, err = c.Events(ctx, &deckv1.Subscribe{Epoch: snap.GetEpoch(), Seq: snap.GetSeq()})
				if err != nil {
					t.Fatal(err)
				}
				if recv(t, st).GetReady().GetResync() {
					t.Fatal("resuming from a fresh snapshot was refused")
				}
				epoch, last = snap.GetEpoch(), snap.GetSeq()
			}
			continue
		}
		if ev.GetSeq() != last+1 {
			t.Fatalf("seq %d after %d: a gap", ev.GetSeq(), last)
		}
		last = ev.GetSeq()
		if last%1000 == 0 && cuts == 0 {
			time.Sleep(200 * time.Millisecond) // stall: the queue fills
		}
	}
	if cuts == 0 {
		t.Fatal("the stalled subscriber was never cut; the test did not exercise it")
	}
	t.Logf("cut %d times, resynced %d times", cuts, resyncs)
	_ = srv
}

// TestUpstreamLossStartsNewEpoch: when the backend says it lost events,
// every stream is ended and nobody can resume in the old epoch.
func TestUpstreamLossStartsNewEpoch(t *testing.T) {
	e := newEngine()
	srv, c := serveEngine(t, e)
	ctx := ctxT(t)
	st, err := c.Events(ctx, &deckv1.Subscribe{})
	if err != nil {
		t.Fatal(err)
	}
	ready := recv(t, st).GetReady()
	e.events <- remote.Event{Name: wire.EvBusy, SessionID: "s1", Payload: wire.BusyChanged{SessionID: "s1", Busy: true}}
	got := recv(t, st)
	e.events <- remote.Event{Name: remote.EvLost}
	for {
		_, err := st.Recv()
		if err != nil {
			if status.Code(err) != codes.ResourceExhausted {
				t.Fatalf("stream ended with %v", err)
			}
			break
		}
	}
	st, err = c.Events(ctx, &deckv1.Subscribe{Epoch: ready.GetEpoch(), Seq: got.GetSeq()})
	if err != nil {
		t.Fatal(err)
	}
	r := recv(t, st).GetReady()
	if !r.GetResync() || r.GetEpoch() == ready.GetEpoch() {
		t.Fatalf("after a loss: %v (old epoch %s)", r, ready.GetEpoch())
	}
	if ep, _ := srv.feed.position(); ep != r.GetEpoch() {
		t.Fatal("Ready names another epoch than the feed's")
	}
}
