package session

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/store"
)

// longLog is a stored thread of turns user message, tool call, tool result
// with a few KB of output, reply and turn end: about 5 KB a turn.
func longLog(tb testing.TB, st *store.Store, dir string, turns int) string {
	tb.Helper()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	out := strings.Repeat("output line of a long command\n", 140)
	var es []store.Entry
	for i := range turns {
		es = append(es,
			store.Entry{Type: store.EntryUser, ID: fmt.Sprintf("u%d", i), Speaker: "tester", To: []string{"claude"},
				Event: agent.Event{Kind: agent.KindUserText, Text: fmt.Sprintf("message %d", i), Time: at}},
			store.Entry{ID: fmt.Sprintf("c%d", i), Event: agent.Event{AgentID: "claude", Kind: agent.KindToolCall, Time: at,
				Tool: &agent.Tool{ID: "t", Name: "Bash", Title: "go test ./..."}}},
			store.Entry{ID: fmt.Sprintf("r%d", i), Event: agent.Event{AgentID: "claude", Kind: agent.KindToolResult, Time: at,
				Tool: &agent.Tool{ID: "t", Name: "Bash", Title: "go test ./...", Output: out}}},
			store.Entry{ID: fmt.Sprintf("a%d", i), Event: agent.Event{AgentID: "claude", Kind: agent.KindAssistantText, Time: at,
				Text: fmt.Sprintf("reply %d: %s", i, strings.Repeat("words ", 80))}},
			store.Entry{ID: fmt.Sprintf("d%d", i), Event: agent.Event{AgentID: "claude", Kind: agent.KindTurnDone, Time: at}},
		)
	}
	th := store.Thread{Dir: dir, Mode: "ask", Members: []store.Member{{ID: "claude", Kind: "claude", Name: "claude", SessionID: "sess", Cursor: len(es)}}}
	th, err := st.CreateWith(th, es)
	if err != nil {
		tb.Fatal(err)
	}
	return th.ID
}

// BenchmarkResumeLongLog resumes a thread of 4000 turns (about 20 MB of
// log). live-MB is the heap still in use once the thread runs.
func BenchmarkResumeLongLog(b *testing.B) {
	dir := b.TempDir()
	st, err := store.New(dir)
	if err != nil {
		b.Fatal(err)
	}
	tid := longLog(b, st, b.TempDir(), 4000)
	var live float64
	b.ResetTimer()
	for range b.N {
		b.StopTimer()
		var before runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		m := NewManager(map[string]agent.Adapter{"claude": fake.NewTurns(0, nil)}, st, testCfg())
		b.StartTimer()
		if _, _, err := m.Resume(tid); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		var after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&after)
		live += float64(int64(after.HeapAlloc)-int64(before.HeapAlloc)) / (1 << 20)
		runtime.KeepAlive(m)
		m.Close()
		b.StartTimer()
	}
	b.ReportMetric(live/float64(b.N), "live-MB")
}

// BenchmarkForkLongLog forks a resumed thread of 4000 turns.
func BenchmarkForkLongLog(b *testing.B) {
	dir := b.TempDir()
	st, err := store.New(dir)
	if err != nil {
		b.Fatal(err)
	}
	tid := longLog(b, st, b.TempDir(), 4000)
	m := NewManager(map[string]agent.Adapter{"claude": fake.NewTurns(0, nil)}, st, testCfg())
	defer m.Close()
	if _, _, err := m.Resume(tid); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for range b.N {
		info, _, _, err := m.Fork(tid, "")
		if err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		if err := m.Delete(info.ID); err != nil {
			b.Fatal(err)
		}
		m.Flush()
		b.StartTimer()
	}
}
