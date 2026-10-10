package fake

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// Hub is an in-process agent.Hub for tests and the demo. Fake agents call it
// through Step.Message / Step.ReadThread; a session.Manager consumes Messages()
// and installs the thread reader.
type Hub struct {
	msgs chan agent.Message

	mu     sync.Mutex
	reader func(string) string
	seq    int64
}

var _ agent.Hub = (*Hub)(nil)

// NewHub returns a Hub with a buffered message channel.
func NewHub() *Hub { return &Hub{msgs: make(chan agent.Message, 64)} }

// Messages is the stream of send_message calls.
func (h *Hub) Messages() <-chan agent.Message { return h.msgs }

// SetThreadReader installs the read_thread provider.
func (h *Hub) SetThreadReader(f func(agentID string) string) {
	h.mu.Lock()
	h.reader = f
	h.mu.Unlock()
}

// ReadThread answers a read_thread call for agentID ("" when no reader is set).
func (h *Hub) ReadThread(agentID string) string {
	h.mu.Lock()
	f := h.reader
	h.mu.Unlock()
	if f == nil {
		return ""
	}
	return f(agentID)
}

// Send raises a send_message call from agent `from` and waits for the answer.
// It gives up when stop closes or after 10 seconds.
func (h *Hub) Send(stop <-chan struct{}, from, to, text string) (string, error) {
	type result struct {
		res string
		err error
	}
	done := make(chan result, 1)
	var once sync.Once
	msg := agent.Message{
		ID:   fmt.Sprintf("fake-msg-%d", atomic.AddInt64(&h.seq, 1)),
		From: from, To: to, Text: text,
		Respond: func(res string, err error) { once.Do(func() { done <- result{res, err} }) },
	}
	timeout := time.After(10 * time.Second)
	select {
	case h.msgs <- msg:
	case <-stop:
		return "", errors.New("interrupted")
	case <-timeout:
		return "", errors.New("hub is not reading messages")
	}
	select {
	case r := <-done:
		return r.res, r.err
	case <-stop:
		return "", errors.New("interrupted")
	case <-timeout:
		return "", errors.New("hub did not answer")
	}
}
