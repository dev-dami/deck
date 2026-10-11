package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// TestMain doubles as a fake `codex app-server` when re-executed by the tests
// with DECK_FAKE_CODEX=1. The fake replays the recv side of a recorded session
// and records what the adapter sent into DECK_FAKE_OUT.
func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == stubMCPFlag {
		stubMCP(os.Args[2])
		return
	}
	if os.Getenv("DECK_FAKE_CODEX") == "1" {
		fakeCodex()
		return
	}
	os.Exit(m.Run())
}

func fakeCodex() {
	if len(os.Args) < 2 || os.Args[1] != "app-server" {
		os.Stderr.WriteString("fake codex: expected `app-server`\n")
		os.Exit(2)
	}
	var omu sync.Mutex
	out, _ := os.OpenFile(os.Getenv("DECK_FAKE_OUT"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	record := func(s string) {
		omu.Lock()
		defer omu.Unlock()
		out.WriteString(s + "\n")
	}
	cwd, _ := os.Getwd()
	record("cwd=" + cwd)
	record("env=" + os.Getenv("DECK_FAKE_EXTRA"))
	record("args=" + strings.Join(os.Args[1:], " "))
	record("tokenenv=" + os.Getenv("DECK_TEST_TOKEN"))

	var fixture []fixtureLine
	f, err := os.Open(os.Getenv("DECK_FAKE_FIXTURE"))
	if err != nil {
		os.Stderr.WriteString(err.Error())
		os.Exit(3)
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var l fixtureLine
		if json.Unmarshal(sc.Bytes(), &l) == nil {
			fixture = append(fixture, l)
		}
	}
	f.Close()

	sentID := map[string]string{} // method -> fixture request id
	recvResp := map[string]int{}  // fixture id -> index of response line
	for i, l := range fixture {
		var m Message
		_ = json.Unmarshal(l.Msg, &m)
		switch {
		case l.Dir == "send" && hasID(m.ID):
			sentID[m.Method] = string(m.ID)
		case l.Dir == "recv" && m.IsResponse():
			recvResp[string(m.ID)] = i
		}
	}

	var wmu sync.Mutex
	w := bufio.NewWriter(os.Stdout)
	write := func(b []byte) {
		wmu.Lock()
		defer wmu.Unlock()
		w.Write(b)
		w.WriteByte('\n')
		w.Flush()
	}
	reply := func(method string, clientID json.RawMessage) int {
		fid := sentID[method]
		idx, ok := recvResp[fid]
		if !ok {
			return -1
		}
		var m Message
		_ = json.Unmarshal(fixture[idx].Msg, &m)
		b, _ := json.Marshal(map[string]any{"id": clientID, "result": m.Result})
		write(b)
		return idx
	}

	respCh := make(chan []byte, 16)
	stream := func(from int) {
		for _, l := range fixture[from+1:] {
			if l.Dir != "recv" {
				continue
			}
			var m Message
			_ = json.Unmarshal(l.Msg, &m)
			if m.IsResponse() {
				continue
			}
			write(l.Msg)
			if m.IsRequest() {
				select {
				case r := <-respCh:
					record("response=" + string(r))
				case <-time.After(15 * time.Second):
					record("response=TIMEOUT")
					return
				}
			}
		}
	}

	in := bufio.NewReaderSize(os.Stdin, 1<<20)
	for {
		line, err := in.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var m Message
			_ = json.Unmarshal(line, &m)
			switch {
			case m.IsResponse():
				respCh <- append([]byte(nil), line...)
			case m.Method == methodInitialize:
				reply(methodInitialize, m.ID)
			case m.Method == notifyInitialized:
				record("initialized")
			case m.Method == methodThreadStart:
				record("thread/start=" + string(m.Params))
				reply(methodThreadStart, m.ID)
			case m.Method == methodThreadResume:
				record("thread/resume=" + string(m.Params))
				reply(methodThreadStart, m.ID)
			case m.Method == methodTurnStart:
				record("turn/start=" + string(m.Params))
				if idx := reply(methodTurnStart, m.ID); idx >= 0 {
					go stream(idx)
				}
			case m.Method == methodModelList:
				record("model/list=" + string(m.Params))
				reply(methodModelList, m.ID)
			case m.Method == methodTurnSteer:
				record("steer=" + string(m.Params))
				var b []byte
				if os.Getenv("DECK_FAKE_STEER_ERR") == "1" {
					b, _ = json.Marshal(map[string]any{"id": m.ID, "error": map[string]any{"code": -32600, "message": "no active turn to steer"}})
				} else {
					var p struct {
						ExpectedTurnID string `json:"expectedTurnId"`
					}
					_ = json.Unmarshal(m.Params, &p)
					b, _ = json.Marshal(map[string]any{"id": m.ID, "result": map[string]any{"turnId": p.ExpectedTurnID}})
				}
				write(b)
			case m.Method == methodRateLimits:
				record("rateLimits=" + string(m.Params))
				b, _ := json.Marshal(map[string]any{"id": m.ID, "result": map[string]any{
					"accountId": "acct-1",
					"rateLimits": map[string]any{
						"limitId":   "codex",
						"primary":   map[string]any{"usedPercent": 12, "windowDurationMins": 300, "resetsAt": 1760000000},
						"secondary": map[string]any{"usedPercent": 40},
						"planType":  nil,
					},
				}})
				write(b)
			case m.Method == methodAccountRead:
				record("account/read")
				b, _ := json.Marshal(map[string]any{"id": m.ID, "result": map[string]any{
					"account":            map[string]any{"type": "chatgpt", "email": "someone@example.com", "planType": "pro"},
					"requiresOpenaiAuth": true,
				}})
				write(b)
			case m.Method == methodTurnInterrupt:
				record("interrupt=" + string(m.Params))
				b, _ := json.Marshal(map[string]any{"id": m.ID, "result": map[string]any{}})
				write(b)
			}
		}
		if err != nil {
			return
		}
	}
}

type fakeEnv struct {
	out string
	env map[string]string
}

func newFakeEnv(t *testing.T, fixture string) fakeEnv {
	t.Helper()
	abs, err := filepath.Abs(fixture)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "fake.out")
	return fakeEnv{out: out, env: map[string]string{
		"DECK_FAKE_CODEX":   "1",
		"DECK_FAKE_FIXTURE": abs,
		"DECK_FAKE_OUT":     out,
		"DECK_FAKE_EXTRA":   "from-spec-env",
	}}
}

func (f fakeEnv) read(t *testing.T) string {
	t.Helper()
	b, _ := os.ReadFile(f.out)
	return string(b)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func drain(r agent.Run) (*[]agent.Event, *sync.Mutex) {
	var mu sync.Mutex
	var evs []agent.Event
	go func() {
		for e := range r.Events() {
			mu.Lock()
			evs = append(evs, e)
			mu.Unlock()
		}
	}()
	return &evs, &mu
}

func hasKind(evs *[]agent.Event, mu *sync.Mutex, k agent.Kind) bool {
	mu.Lock()
	defer mu.Unlock()
	for _, e := range *evs {
		if e.Kind == k {
			return true
		}
	}
	return false
}

func TestAdapterName(t *testing.T) {
	if got := New("codex").Name(); got != "codex" {
		t.Fatalf("Name() = %q", got)
	}
}

func TestAdapterStartMissingBinary(t *testing.T) {
	_, err := New("definitely-not-a-real-codex-binary").Start(context.Background(), agent.Spec{ID: "x", Dir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestAdapterEndToEndWithCommandApproval(t *testing.T) {
	fe := newFakeEnv(t, "testdata/session2.jsonl")
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	run, err := New("").Start(ctx, agent.Spec{
		ID: "cx1", Dir: dir, Model: "test-model", Binary: os.Args[0], Env: fe.env,
		FirstPrompt: "do the thing",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer run.Close()
	evs, mu := drain(run)

	// The approval request for the curl command.
	var req agent.ApprovalRequest
	select {
	case req = <-run.Approvals():
	case <-time.After(15 * time.Second):
		t.Fatal("no approval request")
	}
	if req.Kind != agent.ApproveCommand || req.ToolName != "Bash" || req.AgentID != "cx1" ||
		req.Summary != "curl.exe -sI https://example.com" || req.Cwd == "" || req.ID == "" {
		t.Fatalf("approval = %+v", req)
	}
	if run.SessionID() == "" {
		t.Fatal("no session id")
	}

	// Interrupt while the turn is running, then approve for the session.
	if err := run.Interrupt(); err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	req.Respond(agent.Decision{Allow: true, Always: true})
	req.Respond(agent.Decision{Allow: false}) // second call must be ignored

	waitFor(t, "turn_done", func() bool { return hasKind(evs, mu, agent.KindTurnDone) })

	out := fe.read(t)
	if !strings.Contains(out, "env=from-spec-env") {
		t.Errorf("spec env not passed:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "cwd="+strings.ToLower(dir)) && !strings.Contains(out, filepath.Base(dir)) {
		t.Errorf("cwd not set to spec.Dir:\n%s", out)
	}
	for _, want := range []string{
		"initialized",
		`"approvalPolicy":"on-request"`, `"sandbox":"workspace-write"`, `"model":"test-model"`,
		`"text":"do the thing"`, `"type":"text"`,
		"interrupt=", `"turnId":"01a11b34-74c5-7273-9f76-c82317257350"`,
		`"result":{"decision":"acceptForSession"}`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("fake did not see %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "response="); n != 1 {
		t.Errorf("expected exactly one approval response, got %d:\n%s", n, out)
	}

	mu.Lock()
	first := (*evs)[0]
	mu.Unlock()
	if first.Kind != agent.KindSessionStarted || first.SessionID != run.SessionID() {
		t.Errorf("first event = %+v", first)
	}

	// Close kills the process; the run must report KindExited and close its channels.
	if err := run.Close(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "exited", func() bool { return hasKind(evs, mu, agent.KindExited) })
	select {
	case _, ok := <-run.Approvals():
		if ok {
			t.Error("unexpected extra approval")
		}
	case <-time.After(5 * time.Second):
		t.Error("Approvals() not closed after exit")
	}
}

func TestAdapterResume(t *testing.T) {
	fe := newFakeEnv(t, "testdata/session1.jsonl")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	run, err := New(os.Args[0]).Start(ctx, agent.Spec{ID: "cx2", Dir: t.TempDir(), Env: fe.env, ResumeID: "old-thread-id"})
	if err != nil {
		t.Fatal(err)
	}
	defer run.Close()
	out := fe.read(t)
	if !strings.Contains(out, "thread/resume=") || !strings.Contains(out, `"threadId":"old-thread-id"`) {
		t.Errorf("resume not requested:\n%s", out)
	}
	if strings.Contains(out, "thread/start=") || strings.Contains(out, "turn/start=") {
		t.Errorf("unexpected start / first turn:\n%s", out)
	}
	if run.SessionID() == "" {
		t.Error("no session id after resume")
	}
	// A later Send starts a turn.
	if err := run.Send("again"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fe.read(t), `"text":"again"`) {
		t.Errorf("Send did not reach the server:\n%s", fe.read(t))
	}
}

func TestAdapterContextCancelKillsProcess(t *testing.T) {
	fe := newFakeEnv(t, "testdata/session1.jsonl")
	ctx, cancel := context.WithCancel(context.Background())
	run, err := New(os.Args[0]).Start(ctx, agent.Spec{ID: "cx3", Dir: t.TempDir(), Env: fe.env})
	if err != nil {
		t.Fatal(err)
	}
	evs, mu := drain(run)
	cancel()
	waitFor(t, "exited after ctx cancel", func() bool { return hasKind(evs, mu, agent.KindExited) })
}

func TestAdapterStartFailsWhenServerDies(t *testing.T) {
	// A fixture with no initialize response makes the fake never answer, but
	// exiting immediately is the realistic failure: use a bad fixture path.
	env := map[string]string{"DECK_FAKE_CODEX": "1", "DECK_FAKE_FIXTURE": filepath.Join(t.TempDir(), "missing.jsonl"), "DECK_FAKE_OUT": filepath.Join(t.TempDir(), "o")}
	_, err := New(os.Args[0]).Start(context.Background(), agent.Spec{ID: "cx4", Dir: t.TempDir(), Env: env})
	if err == nil {
		t.Fatal("expected start failure")
	}
}

// writeScript writes a synthetic session (same format as the recorded ones).
func writeScript(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "synthetic.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func send(msg string) string { return `{"dir":"send","msg":` + msg + `}` }
func recv(msg string) string { return `{"dir":"recv","msg":` + msg + `}` }

func TestAdapterFileChangePermissionsAndUnsupportedRequests(t *testing.T) {
	cwd := t.TempDir()
	file := filepath.Join(cwd, "pkg", "util.go")
	diff := "--- a/pkg/util.go\n+++ b/pkg/util.go\n@@ -1,2 +1,3 @@\n package pkg\n-var X = 1\n+var X = 2\n+var Y = 3\n"
	changes := `[{"path":` + jsonString(file) + `,"kind":{"type":"update","move_path":null},"diff":` + jsonString(diff) + `}]`
	script := writeScript(t,
		send(`{"id":1,"method":"initialize","params":{}}`),
		recv(`{"id":1,"result":{"userAgent":"x"}}`),
		send(`{"id":2,"method":"thread/start","params":{}}`),
		recv(`{"id":2,"result":{"thread":{"id":"thr-syn"}}}`),
		send(`{"id":3,"method":"turn/start","params":{}}`),
		recv(`{"id":3,"result":{"turn":{"id":"turn-syn","status":"inProgress"}}}`),
		recv(`{"method":"turn/started","params":{"threadId":"thr-syn","turn":{"id":"turn-syn","status":"inProgress"}}}`),
		recv(`{"method":"item/started","params":{"threadId":"thr-syn","turnId":"turn-syn","item":{"type":"fileChange","id":"fc-1","status":"inProgress","changes":`+changes+`}}}`),
		recv(`{"id":"req-9","method":"item/fileChange/requestApproval","params":{"threadId":"thr-syn","turnId":"turn-syn","itemId":"fc-1","startedAtMs":1,"reason":"write outside workspace"}}`),
		recv(`{"id":11,"method":"item/tool/requestUserInput","params":{"threadId":"thr-syn","turnId":"turn-syn","itemId":"q-1","questions":[]}}`),
		recv(`{"id":12,"method":"item/permissions/requestApproval","params":{"threadId":"thr-syn","turnId":"turn-syn","itemId":"p-1","cwd":`+jsonString(cwd)+`,"reason":"need network","permissions":{"network":{"enabled":true}},"startedAtMs":2}}`),
		recv(`{"method":"item/completed","params":{"threadId":"thr-syn","turnId":"turn-syn","item":{"type":"fileChange","id":"fc-1","status":"completed","changes":`+changes+`}}}`),
		recv(`{"method":"turn/completed","params":{"threadId":"thr-syn","turn":{"id":"turn-syn","status":"completed"}}}`),
	)
	fe := newFakeEnv(t, script)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	run, err := New(os.Args[0]).Start(ctx, agent.Spec{ID: "cx5", Dir: cwd, Env: fe.env, FirstPrompt: "edit"})
	if err != nil {
		t.Fatal(err)
	}
	defer run.Close()
	evs, mu := drain(run)

	var edit agent.ApprovalRequest
	select {
	case edit = <-run.Approvals():
	case <-time.After(15 * time.Second):
		t.Fatal("no edit approval")
	}
	if edit.Kind != agent.ApproveEdit || edit.ToolName != "Edit" || edit.ID != "fc-1" ||
		edit.Summary != filepath.Join("pkg", "util.go") || edit.Diff == nil || edit.Diff.Added != 2 || edit.Diff.Removed != 1 {
		t.Fatalf("edit approval = %+v diff=%+v", edit, edit.Diff)
	}
	edit.Respond(agent.Decision{Allow: false, Reason: "no thanks"})

	var perm agent.ApprovalRequest
	select {
	case perm = <-run.Approvals():
	case <-time.After(15 * time.Second):
		t.Fatal("no permissions approval")
	}
	if perm.Kind != agent.ApproveOther || perm.Summary != "need network" || perm.Cwd != cwd {
		t.Fatalf("permissions approval = %+v", perm)
	}
	perm.Respond(agent.Decision{Allow: true})

	waitFor(t, "turn_done", func() bool { return hasKind(evs, mu, agent.KindTurnDone) })
	out := fe.read(t)
	for _, want := range []string{
		`"id":"req-9","result":{"decision":"decline"}`, // string request id echoed verbatim
		`"id":11,"error":{"code":-32601`,               // unsupported request refused, not hung
		`"id":12,"result":{"permissions":{"network":{"enabled":true}},"scope":"turn"}`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("fake did not see %q:\n%s", want, out)
		}
	}

	mu.Lock()
	var got []agent.Kind
	for _, e := range *evs {
		if e.Kind != agent.KindLimits { // the start-up limits read lands whenever it lands
			got = append(got, e.Kind)
		}
	}
	mu.Unlock()
	want := []agent.Kind{agent.KindSessionStarted, agent.KindToolCall, agent.KindFileChange, agent.KindToolResult, agent.KindTurnDone}
	if len(got) < len(want) {
		t.Fatalf("events = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %v, want prefix %v", got, want)
		}
	}
}

func TestDecisionMapping(t *testing.T) {
	cases := []struct {
		d    agent.Decision
		want string
	}{
		{agent.Decision{Allow: true}, "accept"},
		{agent.Decision{Allow: true, Always: true}, "acceptForSession"},
		{agent.Decision{Allow: false}, "decline"},
		{agent.Decision{Allow: false, Always: true}, "decline"},
	}
	for _, c := range cases {
		if got := commandDecision(c.d); got != c.want {
			t.Errorf("commandDecision(%+v) = %q, want %q", c.d, got, c.want)
		}
		if got := fileChangeDecision(c.d); got != c.want {
			t.Errorf("fileChangeDecision(%+v) = %q, want %q", c.d, got, c.want)
		}
	}
}
