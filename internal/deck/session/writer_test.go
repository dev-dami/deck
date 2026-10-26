package session

import (
	"fmt"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/store"
)

// TestWriterQueueStaysBoundedWhileItFallsBehind checks that the log entries
// threads append while the writer is held take one place per thread in its
// queue, however they interleave, and reach the store in order once it runs.
func TestWriterQueueStaysBoundedWhileItFallsBehind(t *testing.T) {
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(map[string]agent.Adapter{}, st, testCfg())
	t.Cleanup(m.Close)
	var ths []*thread
	for range 2 {
		tid, err := m.NewThread(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		ths = append(ths, m.thread(tid))
	}

	block := make(chan struct{})
	m.w.push(func() { <-block }) // hold the writer while the threads log
	for i := range 1000 {
		for _, th := range ths {
			th.mu.Lock()
			m.appendLocked(th, store.Entry{Type: store.EntryNotice, Event: eventText(fmt.Sprint(i))})
			th.mu.Unlock()
			m.persistMeta(th)
		}
	}
	// The hold, then a log write and a metadata write for each thread.
	if n := m.w.queued(); n > 1+2*len(ths) {
		t.Fatalf("%d operations queued for %d threads", n, len(ths))
	}
	close(block)
	m.Flush()

	for _, th := range ths {
		entries, _, err := st.LoadEntries(th.id)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1000 {
			t.Fatalf("stored %d entries, want 1000", len(entries))
		}
		for i, e := range entries {
			if e.Text != fmt.Sprint(i) {
				t.Fatalf("entry %d = %q", i, e.Text)
			}
		}
	}
}
