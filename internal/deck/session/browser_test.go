package session

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/browser"
)

// browserRig is a thread with a claude and a codex and a desktop window
// that hosts the browser. It answers every request with "did <op>" and keeps
// them.
type browserRig struct {
	*rig
	tid, claude, codex string
	mu                 sync.Mutex
	got                []browser.Request
}

func newBrowserRig(t *testing.T, attached bool) *browserRig {
	t.Helper()
	b := &browserRig{rig: newRig(t, nil, nil)}
	b.tid = b.thread(t)
	b.claude, b.codex = b.add(t, b.tid, "claude"), b.add(t, b.tid, "codex")
	if attached {
		ch, detach := b.m.Browser().Attach("w1", true)
		t.Cleanup(detach)
		go func() {
			for r := range ch {
				b.mu.Lock()
				b.got = append(b.got, r)
				b.mu.Unlock()
				res := browser.Result{ID: r.ID, Text: "did " + r.Op}
				if r.Op == "screenshot" {
					res.Image, res.Mime = []byte("\xff\xd8jpeg"), "image/jpeg"
				}
				b.m.Browser().Complete("w1", res)
			}
		}()
	}
	return b
}

func (b *browserRig) requests() []browser.Request {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]browser.Request(nil), b.got...)
}

// op runs a browser op as agent id, and gives the answer once it came.
func (b *browserRig) op(id, op string, args any) <-chan opResult {
	raw, _ := json.Marshal(args)
	out := make(chan opResult, 1)
	go func() {
		text, isErr := b.m.execOp(id, op, raw)
		out <- opResult{text, isErr}
	}()
	return out
}

type opResult struct {
	text  string
	isErr bool
}

func (b *browserRig) answer(t *testing.T, r <-chan opResult) opResult {
	t.Helper()
	select {
	case res := <-r:
		return res
	case <-time.After(5 * time.Second):
		t.Fatal("the op did not return")
		return opResult{}
	}
}

// approval waits for the person to be asked something and returns the ask.
func (b *browserRig) approval(t *testing.T, n int) ApprovalMsg {
	t.Helper()
	waitFor(t, "an approval", func() bool { return len(b.c.approvals()) >= n })
	return b.c.approvals()[n-1]
}

func TestBrowserOpWithoutADesktop(t *testing.T) {
	b := newBrowserRig(t, false)
	res := b.answer(t, b.op(b.claude, "browser.tabs", nil))
	if !res.isErr || !strings.Contains(res.text, "no browser attached") || !strings.Contains(res.text, "desktop app") {
		t.Fatalf("result = %+v", res)
	}
}

func TestBrowserOpenAsksOncePerSite(t *testing.T) {
	b := newBrowserRig(t, true)
	r := b.op(b.claude, "browser.open", map[string]any{"url": "https://example.com/docs"})
	ap := b.approval(t, 1)
	if ap.Kind != agent.ApproveOther || !strings.Contains(ap.Summary, "open https://example.com in the browser") {
		t.Fatalf("approval = %+v", ap.ApprovalRequest)
	}
	if len(b.requests()) != 0 {
		t.Fatal("the page was opened before the person answered")
	}
	ap.Respond(agent.Decision{Allow: true})
	if res := b.answer(t, r); res.isErr || res.text != "did open" {
		t.Fatalf("result = %+v", res)
	}
	got := b.requests()
	if len(got) != 1 || got[0].Agent != b.claude || got[0].Kind != "claude" || got[0].Op != "open" || got[0].Free {
		t.Fatalf("requests = %+v", got)
	}
	var args map[string]string
	_ = json.Unmarshal(got[0].Args, &args)
	if args["url"] != "https://example.com/docs" {
		t.Fatalf("args = %s", got[0].Args)
	}

	// Another agent of the thread opens the same site without a question; the
	// request carries the sites approved so far.
	if res := b.answer(t, b.op(b.codex, "browser.open", map[string]any{"url": "https://example.com/other"})); res.isErr {
		t.Fatalf("second agent: %+v", res)
	}
	if n := len(b.c.approvals()); n != 1 {
		t.Fatalf("%d approvals, want 1", n)
	}
	if got := b.requests()[1]; got.Agent != b.codex || len(got.Allowed) != 1 || got.Allowed[0] != "https://example.com" {
		t.Fatalf("second request = %+v", got)
	}

	// Another site asks again; a no stops it, with the reason.
	r = b.op(b.claude, "browser.open", map[string]any{"url": "https://other.org"})
	b.approval(t, 2).Respond(agent.Decision{Allow: false, Reason: "not that one"})
	res := b.answer(t, r)
	if !res.isErr || !strings.Contains(res.text, "declined") || !strings.Contains(res.text, "not that one") {
		t.Fatalf("declined result = %+v", res)
	}
	if len(b.requests()) != 2 {
		t.Fatal("a declined page reached the browser")
	}
}

func TestBrowserLoopbackAndSchemes(t *testing.T) {
	b := newBrowserRig(t, true)
	if res := b.answer(t, b.op(b.claude, "browser.open", map[string]any{"url": "localhost:5173/app"})); res.isErr {
		t.Fatalf("a dev server asked: %+v", res)
	}
	if n := len(b.c.approvals()); n != 0 {
		t.Fatalf("%d approvals for a loopback page", n)
	}
	for _, u := range []string{"file:///etc/passwd", "chrome://settings", "devtools://x", "javascript:alert(1)", "https://u:p@example.com"} {
		res := b.answer(t, b.op(b.claude, "browser.open", map[string]any{"url": u}))
		if !res.isErr {
			t.Errorf("%s was opened: %+v", u, res)
		}
	}
	if n := len(b.requests()); n != 1 {
		t.Fatalf("%d requests reached the browser, want only the dev server", n)
	}
	if n := len(b.c.approvals()); n != 0 {
		t.Fatalf("a refused scheme asked the person (%d)", n)
	}
}

func TestBrowserModes(t *testing.T) {
	b := newBrowserRig(t, true)
	// Yolo: nothing asked, and the browser is told any site may load.
	if err := b.m.SetMode(b.tid, agent.ModeYolo); err != nil {
		t.Fatal(err)
	}
	if res := b.answer(t, b.op(b.claude, "browser.open", map[string]any{"url": "https://example.com"})); res.isErr {
		t.Fatalf("yolo open: %+v", res)
	}
	if res := b.answer(t, b.op(b.claude, "browser.eval", map[string]any{"js": "document.title"})); res.isErr {
		t.Fatalf("yolo eval: %+v", res)
	}
	if got := b.requests(); len(got) != 2 || !got[0].Free || !got[1].Free {
		t.Fatalf("requests = %+v", got)
	}

	// Ask: eval always asks, every time.
	if err := b.m.SetMode(b.tid, agent.ModeAsk); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		r := b.op(b.claude, "browser.eval", map[string]any{"js": "document.cookie"})
		ap := b.approval(t, i)
		if !strings.Contains(ap.Summary, "JavaScript") || !strings.Contains(ap.Summary, "document.cookie") {
			t.Fatalf("eval approval = %+v", ap.ApprovalRequest)
		}
		ap.Respond(agent.Decision{Allow: i == 1})
		if res := b.answer(t, r); res.isErr != (i != 1) {
			t.Fatalf("eval %d: %+v", i, res)
		}
	}

	// Plan: reading goes on, acting does not.
	if err := b.m.SetMode(b.tid, agent.ModePlan); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"click", "type", "press", "select", "eval"} {
		args := map[string]any{"ref": "e1", "text": "x", "key": "Enter", "value": "x", "js": "1"}
		for k := range args {
			switch {
			case op == "click" && k != "ref", op == "type" && k != "ref" && k != "text", op == "press" && k != "key",
				op == "select" && k != "ref" && k != "value", op == "eval" && k != "js":
				delete(args, k)
			}
		}
		res := b.answer(t, b.op(b.claude, "browser."+op, args))
		if !res.isErr || !strings.Contains(res.text, "plan mode") {
			t.Errorf("plan %s: %+v", op, res)
		}
	}
	before := len(b.requests())
	for op, args := range map[string]map[string]any{"tabs": nil, "snapshot": nil, "scroll": {"dy": 100}, "wait": {"ms": 50}} {
		if res := b.answer(t, b.op(b.claude, "browser."+op, args)); res.isErr {
			t.Errorf("plan %s: %+v", op, res)
		}
	}
	if n := len(b.requests()) - before; n != 4 {
		t.Fatalf("%d reading ops reached the browser in plan mode, want 4", n)
	}
}

func TestBrowserScreenshotIsAFile(t *testing.T) {
	b := newBrowserRig(t, true)
	if err := b.m.SetMode(b.tid, agent.ModeYolo); err != nil {
		t.Fatal(err)
	}
	res := b.answer(t, b.op(b.claude, "browser.screenshot", nil))
	if res.isErr {
		t.Fatalf("result = %+v", res)
	}
	_, path, ok := strings.Cut(res.text, "screenshot: ")
	if !ok {
		t.Fatalf("no path in %q", res.text)
	}
	data, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil || string(data) != "\xff\xd8jpeg" || !strings.HasSuffix(strings.TrimSpace(path), ".jpg") {
		t.Fatalf("file = %q, %v", data, err)
	}
	os.Remove(strings.TrimSpace(path))
}

func TestBrowserManyAgentsAtOnce(t *testing.T) {
	b := newBrowserRig(t, true)
	if err := b.m.SetMode(b.tid, agent.ModeYolo); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, id := range []string{b.claude, b.codex} {
		for range 3 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if res := b.answer(t, b.op(id, "browser.snapshot", nil)); res.isErr || res.text != "did snapshot" {
					t.Errorf("%s: %+v", id, res)
				}
			}()
		}
	}
	wg.Wait()
	if n := len(b.requests()); n != 6 {
		t.Fatalf("%d requests, want 6", n)
	}
}

func TestBrowserTimeoutFollowsTheCall(t *testing.T) {
	ctxless := browserTimeout(t.Context(), "snapshot", nil)
	if ctxless != browser.DefaultTimeout {
		t.Errorf("default = %s", ctxless)
	}
	if d := browserTimeout(t.Context(), "wait", map[string]any{"timeout": 20}); d != 25*time.Second {
		t.Errorf("wait 20s = %s", d)
	}
	if d := browserTimeout(t.Context(), "wait", map[string]any{"ms": 2000}); d != 8*time.Second {
		t.Errorf("wait 2000ms = %s", d)
	}
}
