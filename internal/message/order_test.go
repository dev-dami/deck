package message

import (
	"context"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/db"
	"github.com/dev-dami/deck/internal/pubsub"
	"github.com/dev-dami/deck/internal/session"
	"github.com/stretchr/testify/require"
)

// TestSeqOrdersRowsOfTheSameSecond: rows created in the same second come
// back in the order they were made, whatever their random ids, and each
// session numbers its own rows from 1.
func TestSeqOrdersRowsOfTheSameSecond(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	conn, err := db.Connect(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	q := db.New(conn)
	sessions := session.NewService(q, conn)
	a, err := sessions.Create(ctx, "a")
	require.NoError(t, err)
	b, err := sessions.Create(ctx, "b")
	require.NoError(t, err)
	svc := NewService(q)

	const n = 40
	var want []string
	for i := range n {
		m, err := svc.Create(ctx, a.ID, CreateMessageParams{Role: User, Parts: []ContentPart{TextContent{Text: "x"}}})
		require.NoError(t, err)
		require.Equal(t, int64(i+1), m.Seq)
		want = append(want, m.ID)
		if i%10 == 0 {
			other, err := svc.Create(ctx, b.ID, CreateMessageParams{Role: User})
			require.NoError(t, err)
			require.Equal(t, int64(i/10+1), other.Seq, "each session counts its own rows")
		}
	}
	// Every row in one second: only seq can order them now.
	_, err = conn.ExecContext(ctx, `UPDATE messages SET created_at = 1700000000`)
	require.NoError(t, err)

	for range 3 { // the same answer every time
		got, err := svc.List(ctx, a.ID)
		require.NoError(t, err)
		ids := make([]string, len(got))
		for i, m := range got {
			ids[i] = m.ID
		}
		require.Equal(t, want, ids)
	}

	// From a summary, exactly the rows from it on, none of the same
	// second before it.
	from, err := svc.ListFromSummary(ctx, a.ID, want[25])
	require.NoError(t, err)
	require.Len(t, from, n-25)
	require.Equal(t, want[25], from[0].ID)

	last, err := svc.ListUserMessages(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, want[n-1], last[0].ID, "newest first")

	// A deleted row leaves the order of the others alone.
	require.NoError(t, svc.Delete(ctx, want[3]))
	got, err := svc.List(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, want[4], got[3].ID)
}

// TestDelete_WaitsForInFlightFlush: a deletion that races a flush already
// writing goes out after that flush's update, so no subscriber sees the
// row come back after it was deleted.
func TestDelete_WaitsForInFlightFlush(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	conn, err := db.Connect(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	q := db.New(conn)
	sess, err := session.NewService(q, conn).Create(ctx, "test")
	require.NoError(t, err)
	slow := &slowUpdateQuerier{Querier: q, release: make(chan struct{}), started: make(chan struct{})}
	svc := NewService(slow, WithDebounce(5*time.Millisecond))
	events := collect(ctx, svc.Subscribe(ctx))

	msg, err := svc.Create(ctx, sess.ID, CreateMessageParams{Role: Assistant})
	require.NoError(t, err)
	msg.AppendContent("streaming")
	require.NoError(t, svc.Update(ctx, msg))
	select {
	case <-slow.started:
	case <-time.After(time.Second):
		t.Fatal("the debounced flush never started writing")
	}

	deleted := make(chan error, 1)
	go func() { deleted <- svc.Delete(context.Background(), msg.ID) }()
	select {
	case err := <-deleted:
		t.Fatalf("Delete returned %v while a flush of the row was writing", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(slow.release)
	require.NoError(t, <-deleted)

	require.Eventually(t, func() bool { return len(events.snapshot()) == 3 }, time.Second, 5*time.Millisecond)
	var types []pubsub.EventType
	for _, ev := range events.snapshot() {
		types = append(types, ev.Type)
	}
	require.Equal(t, []pubsub.EventType{pubsub.CreatedEvent, pubsub.UpdatedEvent, pubsub.DeletedEvent}, types)
}
