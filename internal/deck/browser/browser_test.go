package browser

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// serve answers every request on ch with its op as the text, as a desktop
// would.
func serve(b *Broker, id string, ch <-chan Request) {
	go func() {
		for r := range ch {
			b.Complete(id, Result{ID: r.ID, Text: "did " + r.Op})
		}
	}()
}

func TestNoBrowserAttached(t *testing.T) {
	b := NewBroker()
	_, err := b.Do(context.Background(), Request{Agent: "a", Op: "tabs"})
	if !errors.Is(err, ErrNoBrowser) {
		t.Fatalf("err = %v, want ErrNoBrowser", err)
	}
	// A window that is not the host does not count.
	_, detach := b.Attach("w1", false)
	defer detach()
	if _, err := b.Do(context.Background(), Request{Agent: "a", Op: "tabs"}); !errors.Is(err, ErrNoBrowser) {
		t.Fatalf("non-host window: err = %v, want ErrNoBrowser", err)
	}
}

func TestRequestAndResult(t *testing.T) {
	b := NewBroker()
	ch, detach := b.Attach("w1", true)
	defer detach()
	serve(b, "w1", ch)
	res, err := b.Do(context.Background(), Request{Agent: "claude2", Op: "snapshot"})
	if err != nil || res.Text != "did snapshot" || res.ID == "" {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
}

func TestHostWindowChoice(t *testing.T) {
	b := NewBroker()
	c1, d1 := b.Attach("w1", true)
	defer d1()
	c2, d2 := b.Attach("w2", false)
	defer d2()
	c3, d3 := b.Attach("w3", true) // the newest host wins
	defer d3()
	serve(b, "w3", c3)
	got := make(chan string, 4)
	for id, c := range map[string]<-chan Request{"w1": c1, "w2": c2} {
		go func() {
			for range c {
				got <- id
			}
		}()
	}
	for range 3 {
		if _, err := b.Do(context.Background(), Request{Agent: "a", Op: "tabs"}); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case id := <-got:
		t.Fatalf("window %s got a request, only w3 hosts the browser", id)
	default:
	}
	// When the host leaves, the older host takes over.
	d3()
	serve(b, "w1", c1)
	if hosted, n := b.Hosted(); !hosted || n != 2 {
		t.Fatalf("Hosted = %v, %d", hosted, n)
	}
}

func TestTimeoutFreesTheSlot(t *testing.T) {
	b := NewBroker()
	_, detach := b.Attach("w1", true) // never answers
	defer detach()
	_, err := b.Do(context.Background(), Request{Agent: "a", Op: "wait", Timeout: 30 * time.Millisecond})
	if err == nil || errors.Is(err, ErrNoBrowser) {
		t.Fatalf("err = %v, want a timeout", err)
	}
	b.mu.Lock()
	n := len(b.waiting)
	b.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d requests still waiting after the timeout", n)
	}
}

func TestContextCancel(t *testing.T) {
	b := NewBroker()
	_, detach := b.Attach("w1", true)
	defer detach()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	if _, err := b.Do(ctx, Request{Agent: "a", Op: "wait"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want Canceled", err)
	}
}

func TestDetachFailsPending(t *testing.T) {
	b := NewBroker()
	_, detach := b.Attach("w1", true)
	done := make(chan Result, 1)
	go func() {
		r, _ := b.Do(context.Background(), Request{Agent: "a", Op: "wait"})
		done <- r
	}()
	for {
		b.mu.Lock()
		n := len(b.waiting)
		b.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	detach()
	select {
	case r := <-done:
		if !r.IsError {
			t.Fatalf("result = %+v, want an error", r)
		}
	case <-time.After(time.Second):
		t.Fatal("a pending request outlived its window")
	}
}

func TestOnlyTheReceivingWindowAnswers(t *testing.T) {
	b := NewBroker()
	ch, d1 := b.Attach("w1", true)
	defer d1()
	_, d2 := b.Attach("w2", false)
	defer d2()
	done := make(chan Result, 1)
	go func() {
		r, _ := b.Do(context.Background(), Request{Agent: "a", Op: "tabs"})
		done <- r
	}()
	req := <-ch
	if b.Complete("w2", Result{ID: req.ID, Text: "forged"}) {
		t.Fatal("a window that did not get the request answered it")
	}
	if !b.Complete("w1", Result{ID: req.ID, Text: "ok"}) {
		t.Fatal("the receiving window was refused")
	}
	if r := <-done; r.Text != "ok" {
		t.Fatalf("result = %+v", r)
	}
	if b.Complete("w1", Result{ID: req.ID, Text: "twice"}) {
		t.Fatal("a request was answered twice")
	}
}

func TestConcurrentAgents(t *testing.T) {
	b := NewBroker()
	ch, detach := b.Attach("w1", true)
	defer detach()
	serve(b, "w1", ch)
	var wg sync.WaitGroup
	for i := range 12 {
		for _, op := range []string{"open", "snapshot"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r, err := b.Do(context.Background(), Request{Agent: fmt.Sprintf("agent%d", i), Op: op})
				if err != nil || r.Text != "did "+op {
					t.Errorf("agent%d %s: %+v, %v", i, op, r, err)
				}
			}()
		}
	}
	wg.Wait()
}

func TestPerAgentAndGlobalLimits(t *testing.T) {
	b := NewBroker()
	_, detach := b.Attach("w1", true) // never answers
	defer detach()
	wait := func(n int) {
		for {
			b.mu.Lock()
			got := len(b.waiting)
			b.mu.Unlock()
			if got == n {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for range MaxPerAgent {
		go b.Do(ctx, Request{Agent: "busy", Op: "wait"})
	}
	wait(MaxPerAgent)
	if _, err := b.Do(ctx, Request{Agent: "busy", Op: "wait"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("one agent over its limit: err = %v, want ErrBusy", err)
	}
	// Other agents fill the rest, then the global limit holds.
	for i := range MaxInFlight - MaxPerAgent {
		go b.Do(ctx, Request{Agent: fmt.Sprintf("other%d", i), Op: "wait"})
	}
	wait(MaxInFlight)
	if _, err := b.Do(ctx, Request{Agent: "late", Op: "wait"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("over the global limit: err = %v, want ErrBusy", err)
	}
}

func TestCheckURL(t *testing.T) {
	ok := map[string]string{
		"https://example.com/a?b=1": "https://example.com/a?b=1",
		"http://localhost:3000":     "http://localhost:3000",
		"example.com/docs":          "https://example.com/docs",
		"localhost:5173/app":        "http://localhost:5173/app",
		"  https://Example.com  ":   "https://Example.com",
		"http://127.0.0.1:8080/x#y": "http://127.0.0.1:8080/x#y",
		"https://[::1]:9000/":       "https://[::1]:9000/",
	}
	for in, want := range ok {
		u, err := CheckURL(in)
		if err != nil || u.String() != want {
			t.Errorf("CheckURL(%q) = %v, %v; want %s", in, u, err, want)
		}
	}
	for _, in := range []string{
		"", "file:///etc/passwd", "chrome://settings", "devtools://devtools/bundled/inspector.html", "javascript:alert(1)",
		"data:text/html,<b>x</b>", "about:blank", "view-source:https://example.com", "blob:https://example.com/x",
		"ftp://example.com/", "https://user:pw@example.com/", "https://", "chrome-extension://abc/x.html",
	} {
		if u, err := CheckURL(in); err == nil {
			t.Errorf("CheckURL(%q) = %v, want an error", in, u)
		}
	}
}

func TestLoopbackAndOrigin(t *testing.T) {
	for in, want := range map[string]bool{
		"http://localhost:3000": true, "https://127.0.0.1/": true, "http://[::1]:80/": true, "http://127.1.2.3/": true,
		"https://example.com": false, "http://localhost.evil.com/": false, "http://10.0.0.1/": false,
	} {
		u, err := CheckURL(in)
		if err != nil {
			t.Fatal(err)
		}
		if Loopback(u) != want {
			t.Errorf("Loopback(%s) = %v", in, !want)
		}
	}
	for in, want := range map[string]string{
		"https://Example.com:8443/a/b?c": "https://example.com:8443",
		"https://example.com:443/":       "https://example.com",
		"http://example.com:80/x":        "http://example.com",
		"http://example.com:443/":        "http://example.com:443",
		"https://[::1]:443/":             "https://[::1]",
		"http://localhost:3000":          "http://localhost:3000",
	} {
		u, err := CheckURL(in)
		if err != nil {
			t.Fatal(err)
		}
		if got := Origin(u); got != want {
			t.Errorf("Origin(%s) = %s, want %s", in, got, want)
		}
	}
}
