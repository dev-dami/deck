package fake

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// Hub is an in-process agent.Hub for tests and the demo. Fake agents call it
// through Step.Message / Step.ReadThread / Step.Exec; a session.Manager
// consumes Messages() and installs the thread reader and the executor.
type Hub struct {
	msgs chan agent.Message

	mu     sync.Mutex
	reader func(string) string
	exec   agent.Executor
	seq    int64
}

var _ agent.Hub = (*Hub)(nil)

// NewHub returns a Hub with a buffered message channel.
func NewHub() *Hub { return &Hub{msgs: make(chan agent.Message, 64)} }

// Messages is the stream of thread.send calls.
func (h *Hub) Messages() <-chan agent.Message { return h.msgs }

// SetThreadReader installs the thread.read provider.
func (h *Hub) SetThreadReader(f func(agentID string) string) {
	h.mu.Lock()
	h.reader = f
	h.mu.Unlock()
}

// SetExecutor installs the provider of search and the other execute ops.
func (h *Hub) SetExecutor(f agent.Executor) {
	h.mu.Lock()
	h.exec = f
	h.mu.Unlock()
}

// Exec answers an execute call (op "search" for the search tool) for agentID.
func (h *Hub) Exec(agentID, op string, args json.RawMessage) (string, bool) {
	h.mu.Lock()
	f := h.exec
	h.mu.Unlock()
	if f == nil {
		return "no executor", true
	}
	return f(agentID, op, args)
}

// ReadThread answers a thread.read call for agentID ("" when no reader is set).
func (h *Hub) ReadThread(agentID string) string {
	h.mu.Lock()
	f := h.reader
	h.mu.Unlock()
	if f == nil {
		return ""
	}
	return f(agentID)
}

// Send raises a thread.send call from agent `from` and waits for the answer.
// It gives up when stop closes or after 10 seconds.
func (h *Hub) Send(stop <-chan struct{}, from, to, text string) (string, error) {
	return h.SendMessage(stop, agent.Message{From: from, To: to, Text: text})
}

// SendMessage is Send for a message that carries more than a recipient and a
// text (a handover under a contract); m.ID and m.Respond are set here.
func (h *Hub) SendMessage(stop <-chan struct{}, m agent.Message) (string, error) {
	type result struct {
		res string
		err error
	}
	done := make(chan result, 1)
	var once sync.Once
	msg := m
	msg.ID = fmt.Sprintf("fake-msg-%d", atomic.AddInt64(&h.seq, 1))
	msg.Respond = func(res string, err error) { once.Do(func() { done <- result{res, err} }) }
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
