package workspace

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/app"
	"github.com/dev-dami/deck/internal/db"
	deckagent "github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/message"
	"github.com/dev-dami/deck/internal/session"
	"github.com/stretchr/testify/require"
)

func TestDBWriterRunsWritesInOrderOffTheCaller(t *testing.T) {
	d := newDBWriter()
	release := make(chan struct{})
	var mu sync.Mutex
	var ran []int
	note := func(i int) func() {
		return func() {
			mu.Lock()
			ran = append(ran, i)
			mu.Unlock()
		}
	}
	// A slow write does not hold up the caller that queued it.
	d.do("s1", func() { <-release })
	for i := range 10 {
		sid := "s1"
		if i%2 == 1 {
			sid = "s2"
		}
		d.do(sid, note(i))
	}
	mu.Lock()
	require.Empty(t, ran, "writes ran before the slow one ahead of them")
	mu.Unlock()

	waited := make(chan struct{})
	go func() {
		d.wait("s2")
		close(waited)
	}()
	select {
	case <-waited:
		t.Fatal("wait returned while s2's writes were still queued")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-waited
	mu.Lock()
	require.Equal(t, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, ran, "writes run in the order they were queued")
	mu.Unlock()

	// Nothing queued: wait returns at once, and the counts are gone.
	d.wait("s1")
	d.wait("other")
	d.mu.Lock()
	require.Empty(t, d.pending)
	d.mu.Unlock()
	require.True(t, d.flush(time.Second))
}

func TestDBWriterFlushTimesOut(t *testing.T) {
	d := newDBWriter()
	release := make(chan struct{})
	defer close(release)
	d.do("s", func() { <-release })
	require.False(t, d.flush(10*time.Millisecond), "flush must give up on a stuck write")
}

// slowToolInserts is a store whose tool-result inserts take a while.
type slowToolInserts struct {
	db.Querier
	delay time.Duration
}

func (q slowToolInserts) CreateMessage(ctx context.Context, arg db.CreateMessageParams) (db.Message, error) {
	if arg.Role == string(message.Tool) {
		time.Sleep(q.delay)
	}
	return q.Querier.CreateMessage(ctx, arg)
}

func TestToolResultsAreStoredOffThePumpInOrder(t *testing.T) {
	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	q := db.New(conn)
	sess, err := session.NewService(q, conn).Create(t.Context(), "s")
	require.NoError(t, err)
	const delay = 300 * time.Millisecond
	a := &app.App{Messages: message.NewService(slowToolInserts{q, delay}), Sessions: session.NewService(q, conn)}

	w := newPickerWorkspace(t, pickerCfg())
	w.AppWorkspace = &AppWorkspace{app: a}
	w.dbw = newDBWriter()
	w.turns, w.busy, w.next = map[string]*turn{}, map[string]bool{}, map[string]pendingTurn{}
	w.back["t1"] = sess.ID

	ev := func(k deckagent.Kind, text string, tool *deckagent.Tool) {
		w.onEvent("t1", deckagent.Event{AgentID: "claude", Kind: k, Text: text, Tool: tool, Time: time.Now()})
	}
	ev(deckagent.KindToolCall, "", &deckagent.Tool{ID: "c1", Name: "Bash", Title: "ls", Status: deckagent.ToolRunning})
	began := time.Now()
	ev(deckagent.KindToolResult, "", &deckagent.Tool{ID: "c1", Name: "Bash", Output: "a.go", Status: deckagent.ToolOK})
	require.Less(t, time.Since(began), delay/2, "the pump waited for the tool-result insert")
	ev(deckagent.KindAssistantText, "done", nil)
	ev(deckagent.KindTurnDone, "", nil)
	require.NoError(t, a.Messages.FlushAll(t.Context()))

	// Rows land in event order, the tool result before the text after it.
	rows, err := conn.QueryContext(t.Context(), `SELECT role FROM messages WHERE session_id = ? ORDER BY rowid`, sess.ID)
	require.NoError(t, err)
	defer rows.Close()
	var roles []string
	for rows.Next() {
		var r string
		require.NoError(t, rows.Scan(&r))
		roles = append(roles, r)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []string{"assistant", "tool", "assistant"}, roles)
}

func TestNilDBWriterWritesInline(t *testing.T) {
	var d *dbWriter
	ran := false
	d.do("s", func() { ran = true })
	require.True(t, ran)
	d.wait("s")
	require.True(t, d.flush(0))
}
