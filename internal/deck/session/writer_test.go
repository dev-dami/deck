package session

import (
	"sync"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/store"
)

// TestWriterCoalescesAppendsInOrder checks that entries queued back to back
// for one thread become one write, and that other operations keep their place.
func TestWriterCoalescesAppendsInOrder(t *testing.T) {
	var mu sync.Mutex
	var got []string
	block := make(chan struct{})
	w := newWriter(func(th *thread, es []store.Entry) {
		mu.Lock()
		defer mu.Unlock()
		s := th.id + ":"
		for _, e := range es {
			s += e.Text
		}
		got = append(got, s)
	})
	defer w.stop()
	w.push(func() { <-block }) // hold the writer while the queue fills
	a, b := &thread{id: "a"}, &thread{id: "b"}
	ent := func(s string) store.Entry { return store.Entry{Event: agent.Event{Text: s}} }
	w.pushAppend(a, ent("1"))
	w.pushAppend(a, ent("2"))
	w.pushAppend(b, ent("3"))
	w.push(func() { mu.Lock(); got = append(got, "f"); mu.Unlock() })
	w.pushAppend(b, ent("4"))
	w.pushAppend(b, ent("5"))
	close(block)
	w.flush()
	mu.Lock()
	defer mu.Unlock()
	want := []string{"a:12", "b:3", "f", "b:45"}
	if len(got) != len(want) {
		t.Fatalf("ops = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ops = %q, want %q", got, want)
		}
	}
}
