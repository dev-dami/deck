package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
)

// Message is one decoded JSON-RPC line. Codex's app-server speaks JSON-RPC 2.0
// without the "jsonrpc" member, one JSON object per line.
type Message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
	// Raw is the exact line as received (without the trailing newline).
	Raw json.RawMessage `json:"-"`
}

// IsRequest reports whether the message is a server-initiated request that
// needs a response.
func (m Message) IsRequest() bool { return m.Method != "" && hasID(m.ID) }

// IsNotification reports whether the message is a notification (no id).
func (m Message) IsNotification() bool { return m.Method != "" && !hasID(m.ID) }

// IsResponse reports whether the message answers one of our requests.
func (m Message) IsResponse() bool { return m.Method == "" && hasID(m.ID) }

func hasID(id json.RawMessage) bool {
	return len(id) > 0 && !bytes.Equal(bytes.TrimSpace(id), []byte("null"))
}

// RPCError is a JSON-RPC error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("codex rpc error %d: %s", e.Code, e.Message) }

// Tap observes every line crossing the wire. dir is '>' for lines we send and
// '<' for lines we receive. The slice must not be retained.
type Tap func(dir byte, line []byte)

// ErrClosed is returned by calls made after the connection ended.
var ErrClosed = errors.New("codex: connection closed")

// Client is a small concurrency-safe JSON-RPC client over a line-delimited stream.
type Client struct {
	w   io.Writer
	wmu sync.Mutex
	tap Tap

	nextID atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan Message
	err     error // set once the read loop ends

	q    *queue
	out  chan Message
	done chan struct{}
}

// NewClient starts reading r and writing to w. Notifications and server
// requests are delivered, in wire order, on Incoming(). tap may be nil.
func NewClient(r io.Reader, w io.Writer, tap Tap) *Client {
	c := &Client{
		w:       w,
		tap:     tap,
		pending: map[int64]chan Message{},
		q:       newQueue(),
		out:     make(chan Message),
		done:    make(chan struct{}),
	}
	go c.readLoop(r)
	go c.pump()
	return c
}

// Incoming yields notifications and server requests in the order they arrived.
// It is closed after the stream ends and everything has been delivered.
func (c *Client) Incoming() <-chan Message { return c.out }

// Done is closed when the read side has ended.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err is the reason the read side ended (nil before that, io.EOF on a clean end).
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *Client) readLoop(r io.Reader) {
	br := bufio.NewReaderSize(r, 64*1024)
	var rerr error
	for {
		line, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			c.handleLine(bytes.TrimRight(line, "\r\n"))
		}
		if err != nil {
			rerr = err
			break
		}
	}
	c.mu.Lock()
	c.err = rerr
	pend := c.pending
	c.pending = map[int64]chan Message{}
	c.mu.Unlock()
	for _, ch := range pend {
		close(ch) // waiter sees a zero Message + closed channel => ErrClosed
	}
	close(c.done)
	c.q.close()
}

func (c *Client) handleLine(line []byte) {
	if c.tap != nil {
		c.tap('<', line)
	}
	var m Message
	if err := json.Unmarshal(line, &m); err != nil {
		return // not JSON (stray log line); ignore
	}
	m.Raw = append(json.RawMessage(nil), line...)
	if m.IsResponse() {
		id, err := strconv.ParseInt(string(bytes.TrimSpace(m.ID)), 10, 64)
		if err != nil {
			return
		}
		c.mu.Lock()
		ch := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if ch != nil {
			ch <- m // buffered(1)
		}
		return
	}
	if m.Method != "" {
		c.q.push(m)
	}
}

// pump moves messages from the unbounded queue to the out channel so a slow
// consumer can never stall response handling.
func (c *Client) pump() {
	for {
		m, ok := c.q.pop()
		if !ok {
			close(c.out)
			return
		}
		c.out <- m
	}
}

func (c *Client) writeLine(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.tap != nil {
		c.tap('>', bytes.TrimRight(b, "\n"))
	}
	_, err = c.w.Write(b)
	return err
}

type outRequest struct {
	ID     int64  `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type outNotification struct {
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type outResponse struct {
	ID     json.RawMessage `json:"id"`
	Result any             `json:"result"`
}

type outError struct {
	ID    json.RawMessage `json:"id"`
	Error RPCError        `json:"error"`
}

// Call sends a request and waits for its response, decoding Result into result
// (which may be nil). A JSON-RPC error response is returned as *RPCError.
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	id := c.nextID.Add(1)
	ch := make(chan Message, 1)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return ErrClosed
	}
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.writeLine(outRequest{ID: id, Method: method, Params: params}); err != nil {
		c.forget(id)
		return err
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return ErrClosed
		}
		if m.Error != nil {
			return m.Error
		}
		if result != nil && len(m.Result) > 0 {
			return json.Unmarshal(m.Result, result)
		}
		return nil
	case <-ctx.Done():
		c.forget(id)
		return ctx.Err()
	}
}

func (c *Client) forget(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// Notify sends a notification.
func (c *Client) Notify(method string, params any) error {
	return c.writeLine(outNotification{Method: method, Params: params})
}

// Respond answers a server request. id is the request's id exactly as received.
func (c *Client) Respond(id json.RawMessage, result any) error {
	if result == nil {
		result = struct{}{}
	}
	return c.writeLine(outResponse{ID: id, Result: result})
}

// RespondError answers a server request with a JSON-RPC error.
func (c *Client) RespondError(id json.RawMessage, code int, msg string) error {
	return c.writeLine(outError{ID: id, Error: RPCError{Code: code, Message: msg}})
}

// queue is an unbounded FIFO of Messages.
type queue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []Message
	closed bool
}

func newQueue() *queue {
	q := &queue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *queue) push(m Message) {
	q.mu.Lock()
	q.items = append(q.items, m)
	q.mu.Unlock()
	q.cond.Signal()
}

func (q *queue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.cond.Broadcast()
}

func (q *queue) pop() (Message, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.items) == 0 {
		return Message{}, false
	}
	m := q.items[0]
	q.items[0] = Message{}
	q.items = q.items[1:]
	return m, true
}
