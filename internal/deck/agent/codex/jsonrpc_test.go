package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"
)

// fakeServer is the far end of an in-memory pipe pair.
type fakeServer struct {
	r  *bufio.Reader
	w  io.WriteCloser
	mu sync.Mutex
}

func newPipePair() (*Client, *fakeServer) {
	cr, sw := io.Pipe() // server -> client
	sr, cw := io.Pipe() // client -> server
	c := NewClient(cr, cw, nil)
	return c, &fakeServer{r: bufio.NewReader(sr), w: sw}
}

func (s *fakeServer) read(t *testing.T) Message {
	t.Helper()
	line, err := s.r.ReadBytes('\n')
	if err != nil {
		t.Fatalf("server read: %v", err)
	}
	var m Message
	if err := json.Unmarshal(line, &m); err != nil {
		t.Fatalf("server decode %q: %v", line, err)
	}
	return m
}

func (s *fakeServer) write(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = io.WriteString(s.w, line+"\n")
}

func TestClientCallResultAndError(t *testing.T) {
	c, s := newPipePair()
	go func() {
		m := s.read(t)
		s.write(fmt.Sprintf(`{"id":%s,"result":{"thread":{"id":"th1"}}}`, m.ID))
		m = s.read(t)
		s.write(fmt.Sprintf(`{"id":%s,"error":{"code":-32600,"message":"nope"}}`, m.ID))
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var resp threadResponse
	if err := c.Call(ctx, methodThreadStart, map[string]string{"cwd": "x"}, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Thread.ID != "th1" {
		t.Fatalf("thread id = %q", resp.Thread.ID)
	}
	err := c.Call(ctx, "bogus", nil, nil)
	var re *RPCError
	if !errors.As(err, &re) || re.Code != -32600 || re.Message != "nope" {
		t.Fatalf("err = %v", err)
	}
}

func TestClientWireFormat(t *testing.T) {
	c, s := newPipePair()
	got := make(chan Message, 3)
	go func() {
		for i := 0; i < 3; i++ {
			got <- s.read(t)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go c.Call(ctx, "ping", map[string]int{"a": 1}, nil)
	req := <-got
	if req.Method != "ping" || !req.IsRequest() || string(req.Params) != `{"a":1}` {
		t.Fatalf("request = %+v", req)
	}
	if err := c.Notify("initialized", nil); err != nil {
		t.Fatal(err)
	}
	n := <-got
	if !n.IsNotification() || n.Method != "initialized" {
		t.Fatalf("notification = %+v", n)
	}
	if err := c.Respond(json.RawMessage(`"srv-7"`), map[string]string{"decision": "accept"}); err != nil {
		t.Fatal(err)
	}
	r := <-got
	if !r.IsResponse() || string(r.ID) != `"srv-7"` || string(r.Result) != `{"decision":"accept"}` {
		t.Fatalf("response = %+v (%s)", r, r.Raw)
	}
}

func TestClientIncomingOrderAndServerRequests(t *testing.T) {
	c, s := newPipePair()
	s.write(`{"method":"turn/started","params":{"n":1}}`)
	s.write("this is not json")
	s.write(`{"id":0,"method":"item/commandExecution/requestApproval","params":{"n":2}}`)
	s.write(`{"method":"turn/completed","params":{"n":3}}`)
	m1 := <-c.Incoming()
	m2 := <-c.Incoming()
	m3 := <-c.Incoming()
	if m1.Method != "turn/started" || !m1.IsNotification() {
		t.Fatalf("m1 = %+v", m1)
	}
	if m2.Method != reqCommandApproval || !m2.IsRequest() || string(m2.ID) != "0" {
		t.Fatalf("m2 = %+v", m2)
	}
	if m3.Method != "turn/completed" {
		t.Fatalf("m3 = %+v", m3)
	}
	if len(m2.Raw) == 0 {
		t.Fatal("Raw not kept")
	}
}

func TestClientSlowConsumerDoesNotBlockResponses(t *testing.T) {
	c, s := newPipePair()
	// Flood notifications nobody is reading, then answer a request.
	go func() {
		m := s.read(t)
		for i := 0; i < 2000; i++ {
			s.write(`{"method":"item/agentMessage/delta","params":{"delta":"x"}}`)
		}
		s.write(fmt.Sprintf(`{"id":%s,"result":{}}`, m.ID))
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Call(ctx, "x", nil, nil); err != nil {
		t.Fatalf("call blocked behind notifications: %v", err)
	}
	for i := 0; i < 2000; i++ {
		<-c.Incoming()
	}
}

func TestClientConcurrentCallsOutOfOrder(t *testing.T) {
	c, s := newPipePair()
	const n = 40
	go func() {
		ids := make([]json.RawMessage, 0, n)
		methods := make([]string, 0, n)
		for i := 0; i < n; i++ {
			m := s.read(t)
			ids = append(ids, m.ID)
			methods = append(methods, m.Method)
		}
		for i := n - 1; i >= 0; i-- { // reverse order
			s.write(fmt.Sprintf(`{"id":%s,"result":{"echo":%q}}`, ids[i], methods[i]))
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var out struct{ Echo string }
			name := fmt.Sprintf("m%d", i)
			if err := c.Call(ctx, name, nil, &out); err != nil {
				errs <- err
				return
			}
			if out.Echo != name {
				errs <- fmt.Errorf("call %s got %s", name, out.Echo)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestClientEOFFailsPendingAndClosesIncoming(t *testing.T) {
	c, s := newPipePair()
	go func() {
		s.read(t)
		s.write(`{"method":"last","params":{}}`)
		s.w.Close()
	}()
	err := c.Call(context.Background(), "x", nil, nil)
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
	m, ok := <-c.Incoming()
	if !ok || m.Method != "last" {
		t.Fatalf("queued message lost: %+v ok=%v", m, ok)
	}
	if _, ok := <-c.Incoming(); ok {
		t.Fatal("Incoming should be closed after EOF")
	}
	if err := c.Call(context.Background(), "again", nil, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("call after close: %v", err)
	}
}

func TestClientCallContextCancel(t *testing.T) {
	c, s := newPipePair()
	go func() { _, _ = s.r.ReadBytes(0x0a) }()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := c.Call(ctx, "x", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}

func TestClientTap(t *testing.T) {
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	var mu sync.Mutex
	var seen []string
	c := NewClient(cr, cw, func(dir byte, line []byte) {
		mu.Lock()
		seen = append(seen, string(dir)+string(line))
		mu.Unlock()
	})
	s := &fakeServer{r: bufio.NewReader(sr), w: sw}
	go func() {
		m := s.read(t)
		s.write(fmt.Sprintf(`{"id":%s,"result":{}}`, m.ID))
	}()
	if err := c.Call(context.Background(), "ping", nil, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 || seen[0] != `>{"id":1,"method":"ping"}` || seen[1] != `<{"id":1,"result":{}}` {
		t.Fatalf("tap saw %q", seen)
	}
}
