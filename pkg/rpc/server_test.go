package rpc

import (
	"bytes"
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/remotetest"
	"github.com/dev-dami/deck/pkg/remote/wire"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// rig serves a fake backend on a real Unix socket and dials it.
func rig(t *testing.T) (*remotetest.Fake, *Server, deckv1.DeckClient, string) {
	t.Helper()
	fake := remotetest.New()
	srv := New(fake, "secret")
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
	return fake, srv, c, sock
}

func ctxT(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestUnaryRoundTrip(t *testing.T) {
	fake, _, c, _ := rig(t)
	ctx := ctxT(t)

	info, err := c.Info(ctx, &deckv1.Empty{})
	if err != nil || info.GetProject() != "proj" || info.GetTheme().GetKinds()["claude"].GetMark() != "✻" {
		t.Fatalf("info = %v, %v", info, err)
	}
	list, err := c.ListSessions(ctx, &deckv1.Empty{})
	if err != nil || len(list.GetSessions()) != 1 || list.GetSessions()[0].GetId() != "s1" {
		t.Fatalf("sessions = %v, %v", list, err)
	}
	s, err := c.CreateSession(ctx, &deckv1.CreateSessionRequest{Title: "Two"})
	if err != nil || s.GetTitle() != "Two" {
		t.Fatalf("create = %v, %v", s, err)
	}
	if _, err := c.RenameSession(ctx, &deckv1.RenameSessionRequest{SessionId: s.GetId(), Title: "Deux"}); err != nil {
		t.Fatal(err)
	}
	if got := fake.Get(s.GetId()).Session.Title; got != "Deux" {
		t.Fatalf("renamed to %q", got)
	}

	// Images arrive as the bytes that were sent, no base64 on the way.
	png := []byte{0x89, 'P', 'N', 'G', 0, 1, 2, 0xff}
	res, err := c.Send(ctx, &deckv1.SendRequest{SessionId: "s1", Text: "look", Images: []*deckv1.Image{{Name: `C:\shots\a.png`, Mime: "image/png", Data: png}}})
	if err != nil || res.GetMessageId() == "" {
		t.Fatalf("send = %v, %v", res, err)
	}
	sent := fake.Sent()
	if len(sent) != 1 || !bytes.Equal(sent[0].Images[0].Data, png) || sent[0].Images[0].Name != "a.png" {
		t.Fatalf("sent = %+v", sent)
	}
	msgs, err := c.ListMessages(ctx, &deckv1.ListMessagesRequest{SessionId: "s1"})
	if err != nil || len(msgs.GetMessages()) == 0 || msgs.GetMessages()[len(msgs.GetMessages())-1].GetParts()[0].GetText() != "look" {
		t.Fatalf("messages = %v, %v", msgs, err)
	}

	if _, err := c.SetMode(ctx, &deckv1.SetModeRequest{SessionId: "s1", Mode: wire.ModeYolo}); err != nil {
		t.Fatalf("the desktop may switch to yolo: %v", err)
	}
	if m, err := c.GetMode(ctx, &deckv1.SessionRef{SessionId: "s1"}); err != nil || m.GetMode() != wire.ModeYolo {
		t.Fatalf("mode = %v, %v", m, err)
	}
}

func TestErrorsMapToCodes(t *testing.T) {
	_, _, c, _ := rig(t)
	ctx := ctxT(t)
	cases := []struct {
		err  error
		want codes.Code
	}{
		{func() error { _, err := c.Send(ctx, &deckv1.SendRequest{SessionId: "nope", Text: "x"}); return err }(), codes.NotFound},
		{func() error { _, err := c.Send(ctx, &deckv1.SendRequest{SessionId: "s1", Text: "  "}); return err }(), codes.InvalidArgument},
		{func() error {
			_, err := c.Send(ctx, &deckv1.SendRequest{SessionId: "s1", Images: []*deckv1.Image{{Mime: "text/plain", Data: []byte("x")}}})
			return err
		}(), codes.InvalidArgument},
		{func() error {
			_, err := c.SetMode(ctx, &deckv1.SetModeRequest{SessionId: "s1", Mode: "chaos"})
			return err
		}(), codes.InvalidArgument},
		{func() error {
			_, err := c.Answer(ctx, &deckv1.AnswerRequest{SessionId: "s1", Id: "a1", Decision: "maybe"})
			return err
		}(), codes.InvalidArgument},
	}
	for i, tc := range cases {
		if got := status.Code(tc.err); got != tc.want {
			t.Errorf("case %d: code %v (%v), want %v", i, got, tc.err, tc.want)
		}
	}
}

func TestWrongTokenRefused(t *testing.T) {
	_, _, _, sock := rig(t)
	cc, c, err := Dial(sock, "guess")
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	ctx := ctxT(t)
	if _, err := c.Info(ctx, &deckv1.Empty{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("info with a wrong token: %v", err)
	}
	st, err := c.Events(ctx, &deckv1.Subscribe{})
	if err == nil {
		_, err = st.Recv()
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("events with a wrong token: %v", err)
	}
}

func recv(t *testing.T, st deckv1.Deck_EventsClient) *deckv1.Event {
	t.Helper()
	ev, err := st.Recv()
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func TestEventsStreamAndResume(t *testing.T) {
	fake, srv, c, _ := rig(t)
	ctx := ctxT(t)

	st, err := c.Events(ctx, &deckv1.Subscribe{})
	if err != nil {
		t.Fatal(err)
	}
	ready := recv(t, st).GetReady()
	if ready == nil || ready.GetResync() || ready.GetEpoch() == "" {
		t.Fatalf("first event = %v", ready)
	}

	fake.Inject(remote.Event{Name: wire.EvBusy, SessionID: "s1", Payload: wire.BusyChanged{SessionID: "s1", Busy: true}})
	fake.Inject(remote.Event{Name: wire.EvDevicePaired, Payload: wire.Device{ID: "d"}}) // not for the desktop
	fake.Inject(remote.Event{Name: wire.EvMessageUpdated, SessionID: "s1", Scoped: true, Payload: wire.Message{ID: "m9", SessionID: "s1", Parts: []wire.Part{{Type: wire.PartText, Text: "hi"}}}})

	first := recv(t, st)
	if !first.GetBusy().GetBusy() || first.GetSeq() != ready.GetSeq()+1 {
		t.Fatalf("busy event = %v", first)
	}
	second := recv(t, st)
	if second.GetMessageUpdated().GetParts()[0].GetText() != "hi" || second.GetSeq() != first.GetSeq()+1 {
		t.Fatalf("message event = %v", second)
	}

	// A reconnect from the first event replays the second.
	st2, err := c.Events(ctx, &deckv1.Subscribe{Epoch: ready.GetEpoch(), Seq: first.GetSeq()})
	if err != nil {
		t.Fatal(err)
	}
	if r := recv(t, st2).GetReady(); r.GetResync() {
		t.Fatalf("resume was refused: %v", r)
	}
	if again := recv(t, st2); again.GetSeq() != second.GetSeq() || again.GetMessageUpdated() == nil {
		t.Fatalf("replayed = %v", again)
	}

	// Another epoch cannot be replayed.
	st3, err := c.Events(ctx, &deckv1.Subscribe{Epoch: "old", Seq: 3})
	if err != nil {
		t.Fatal(err)
	}
	if r := recv(t, st3).GetReady(); !r.GetResync() || r.GetEpoch() != srv.feed.epoch {
		t.Fatalf("other epoch: %v", r)
	}
}

func TestFeedFallsOutOfRing(t *testing.T) {
	f := newFeed()
	for range RingSize + 10 {
		f.publish(&deckv1.Event{})
	}
	_, r := f.subscribe(f.epoch, 1)
	if !r.GetResync() {
		t.Fatal("a position older than the ring must resync")
	}
	s, r := f.subscribe(f.epoch, f.seq-2)
	if r.GetResync() || len(s.ch) != 2 {
		t.Fatalf("recent position: resync %v, %d queued", r.GetResync(), len(s.ch))
	}
}
