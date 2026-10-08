package codex

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/agent"
)

type fixtureLine struct {
	Dir string          `json:"dir"` // "send" | "recv"
	Msg json.RawMessage `json:"msg"`
}

func loadFixture(t testing.TB, name string) []fixtureLine {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []fixtureLine
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var l fixtureLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out = append(out, l)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

type replayResult struct {
	events   []agent.Event
	requests []Message // server requests seen
	tr       *translator
}

// replay feeds the received side of a fixture through a translator.
func replay(t testing.TB, name string) replayResult {
	t.Helper()
	lines := loadFixture(t, name)
	cwd := ""
	sent := map[string]string{} // request id -> method
	for _, l := range lines {
		if l.Dir != "send" {
			continue
		}
		var m Message
		_ = json.Unmarshal(l.Msg, &m)
		if hasID(m.ID) {
			sent[string(m.ID)] = m.Method
		}
		if m.Method == methodThreadStart {
			var p threadStartParams
			_ = json.Unmarshal(m.Params, &p)
			cwd = p.Cwd
		}
	}
	tr := newTranslator("codex1", cwd)
	tick := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	tr.now = func() time.Time { return tick }
	var res replayResult
	res.tr = tr
	for _, l := range lines {
		if l.Dir != "recv" {
			continue
		}
		var m Message
		if err := json.Unmarshal(l.Msg, &m); err != nil {
			t.Fatal(err)
		}
		m.Raw = l.Msg
		switch {
		case m.IsResponse():
			if sent[string(m.ID)] == methodThreadStart {
				var tr threadResponse
				_ = json.Unmarshal(m.Result, &tr)
				res.events = append(res.events, res.tr.sessionStarted(tr.Thread.ID, tr.Model, m.Raw))
			}
		case m.IsRequest():
			res.requests = append(res.requests, m)
		case m.IsNotification():
			res.events = append(res.events, res.tr.notification(m.Method, m.Params, m.Raw)...)
		}
	}
	return res
}

func kinds(evs []agent.Event) []agent.Kind {
	out := make([]agent.Kind, len(evs))
	for i, e := range evs {
		out[i] = e.Kind
	}
	return out
}

// collapse merges runs of delta events of the same kind into one marker so the
// sequence assertion does not depend on how the model chunked its text.
func collapse(evs []agent.Event) []string {
	var out []string
	for _, e := range evs {
		s := string(e.Kind)
		if e.Delta {
			s += "(delta)"
		}
		if len(out) > 0 && out[len(out)-1] == s && e.Delta {
			continue
		}
		out = append(out, s)
	}
	return out
}

func TestReplaySession1(t *testing.T) {
	res := replay(t, "session1.jsonl")
	got := strings.Join(collapse(res.events), " ")
	want := strings.Join([]string{
		"session_started",
		"user_text",
		"assistant_text(delta)", "assistant_text", // commentary message
		"tool_call", "tool_result", // create file
		"tool_call", "tool_result", // type hello.txt
		"usage",
		"assistant_text(delta)", "assistant_text", // DONE
		"usage",
		"turn_done",
	}, " ")
	if got != want {
		t.Fatalf("event sequence:\n got  %s\n want %s", got, want)
	}

	ev := res.events
	if ev[0].SessionID == "" || ev[0].AgentID != "codex1" || len(ev[0].Raw) == 0 {
		t.Fatalf("bad session event: %+v", ev[0])
	}
	for i, e := range ev {
		if len(e.Raw) == 0 {
			t.Errorf("event %d (%s) has no Raw", i, e.Kind)
		}
		if e.AgentID != "codex1" || e.Time.IsZero() {
			t.Errorf("event %d missing AgentID/Time", i)
		}
	}
	if ev[1].Text != "Create a file hello.txt containing hi, then run `type hello.txt`, then reply DONE." {
		t.Errorf("user text = %q", ev[1].Text)
	}

	var calls, results []agent.Event
	var finals []string
	var sawDelta bool
	var turnDone agent.Event
	var usages []agent.Event
	for _, e := range ev {
		switch e.Kind {
		case agent.KindToolCall:
			calls = append(calls, e)
		case agent.KindToolResult:
			results = append(results, e)
		case agent.KindAssistantText:
			if e.Delta {
				sawDelta = true
			} else {
				finals = append(finals, e.Text)
			}
		case agent.KindUsage:
			usages = append(usages, e)
		case agent.KindTurnDone:
			turnDone = e
		}
	}
	if !sawDelta {
		t.Error("no streaming deltas")
	}
	if len(finals) != 2 || finals[1] != "DONE" {
		t.Fatalf("final texts = %q", finals)
	}
	// The recorded text came through mojibake'd ("â€™"); the translator repairs it.
	if !strings.Contains(finals[0], "I’ll create hello.txt") && !strings.Contains(finals[0], "I'll create hello.txt") {
		t.Errorf("first final text = %q", finals[0])
	}
	if len(calls) != 2 || len(results) != 2 {
		t.Fatalf("calls=%d results=%d", len(calls), len(results))
	}
	if c := calls[0].Tool; c.Name != "Bash" || c.Title != "Set-Content -LiteralPath hello.txt -Value hi" || c.Status != agent.ToolRunning || c.ID == "" {
		t.Errorf("call0 = %+v", c)
	}
	if c := calls[1].Tool; c.Title != "type hello.txt" {
		t.Errorf("call1 = %+v", c)
	}
	if r := results[1].Tool; r.Status != agent.ToolOK || r.Output != "hi\r\n" || r.ID != calls[1].Tool.ID {
		t.Errorf("result1 = %+v", r)
	}
	if r := results[0].Tool; r.Status != agent.ToolOK {
		t.Errorf("result0 = %+v", r)
	}
	// Usage is per-turn cumulative: 15570 after the first model call, 32148 after the second.
	if u := usages[0].Usage; u.InputTokens != 15570 || u.OutputTokens != 61 || u.CachedTokens != 12544 {
		t.Errorf("usage0 = %+v", u)
	}
	if u := usages[1].Usage; u.InputTokens != 32148 || u.OutputTokens != 66 {
		t.Errorf("usage1 = %+v", u)
	}
	if turnDone.Text != "" {
		t.Errorf("turn_done text = %q", turnDone.Text)
	}
}

func TestReplaySession2FileChangeAndDeclinedCommand(t *testing.T) {
	res := replay(t, "session2.jsonl")
	got := strings.Join(collapse(res.events), " ")
	want := strings.Join([]string{
		"session_started",
		"user_text",
		"assistant_text(delta)", "assistant_text", // commentary
		"tool_call",   // fileChange
		"file_change", // per file
		"tool_result", // fileChange ok
		"usage",
		"tool_call", "tool_result", // curl: declined
		"usage",
		"assistant_text(delta)", "assistant_text", // DONE
		"usage",
		"turn_done",
	}, " ")
	if got != want {
		t.Fatalf("event sequence:\n got  %s\n want %s", got, want)
	}
	var fc *agent.FileDiff
	var results []agent.Event
	for _, e := range res.events {
		if e.Kind == agent.KindFileChange {
			fc = e.Diff
		}
		if e.Kind == agent.KindToolResult {
			results = append(results, e)
		}
	}
	if fc == nil {
		t.Fatal("no file change")
	}
	// The fixture was recorded on Windows, so its working directory is a Windows
	// path. Only a Windows host can make a path under it relative; elsewhere the
	// absolute path is kept, so compare the file name there.
	name := func(p string) string {
		if runtime.GOOS == "windows" {
			return p
		}
		return p[strings.LastIndexAny(p, `\/`)+1:]
	}
	if name(fc.Path) != "hello.txt" || !fc.New || fc.Added < 1 || fc.Removed != 0 || len(fc.Hunks) != 1 {
		t.Errorf("file change = %+v", fc)
	}
	if results[0].Tool.Name != "Edit" || results[0].Tool.Status != agent.ToolOK || name(results[0].Tool.Title) != "hello.txt" {
		t.Errorf("edit result = %+v", results[0].Tool)
	}
	curl := results[1].Tool
	if curl.Status != agent.ToolError || curl.Title != "curl.exe -sI https://example.com" {
		t.Errorf("curl result = %+v", curl)
	}
	// Per-turn usage keeps growing within the turn.
	var last agent.Usage
	for _, e := range res.events {
		if e.Kind == agent.KindUsage {
			last = *e.Usage
		}
	}
	if last.InputTokens != 49671 || last.OutputTokens != 168 {
		t.Errorf("last usage = %+v", last)
	}
	if len(res.requests) != 1 || res.requests[0].Method != reqCommandApproval {
		t.Fatalf("requests = %+v", res.requests)
	}
}

func notif(t *testing.T, tr *translator, method, params string) []agent.Event {
	t.Helper()
	return tr.notification(method, json.RawMessage(params), json.RawMessage(`{"method":"`+method+`"}`))
}

func TestTranslateUpdateFileChangeFromUnifiedDiff(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "proj")
	tr := newTranslator("c1", cwd)
	path := filepath.Join(cwd, "src", "main.go")
	diff := "--- a/src/main.go\\n+++ b/src/main.go\\n@@ -1,3 +1,4 @@\\n package main\\n-var a = 1\\n+var a = 2\\n+var b = 3\\n func main() {}\\n"
	params := `{"threadId":"t","turnId":"u","item":{"type":"fileChange","id":"fc1","status":"inProgress","changes":[{"path":` +
		jsonString(path) + `,"kind":{"type":"update","move_path":null},"diff":"` + diff + `"}]}}`
	started := notif(t, tr, noteItemStarted, params)
	if len(started) != 1 || started[0].Kind != agent.KindToolCall || started[0].Tool.Name != "Edit" || started[0].Tool.Title != filepath.Join("src", "main.go") {
		t.Fatalf("started = %+v", started)
	}
	if ds := tr.fileDiffsFor("fc1"); len(ds) != 1 || ds[0].Added != 2 || ds[0].Removed != 1 {
		t.Fatalf("pending diffs = %+v", ds)
	}
	done := notif(t, tr, noteItemCompleted, strings.Replace(params, `"inProgress"`, `"completed"`, 1))
	if len(done) != 2 || done[0].Kind != agent.KindFileChange || done[1].Kind != agent.KindToolResult {
		t.Fatalf("done = %v", kinds(done))
	}
	d := done[0].Diff
	if d.Path != filepath.Join("src", "main.go") || d.New || d.Added != 2 || d.Removed != 1 || len(d.Hunks) != 1 || d.Hunks[0].NewLines != 4 {
		t.Fatalf("diff = %+v", d)
	}
	if tr.fileDiffsFor("fc1") != nil {
		t.Error("item state should be dropped after completion")
	}
}

func TestTranslateDeclinedAndRenamedFileChange(t *testing.T) {
	tr := newTranslator("c1", "")
	params := `{"threadId":"t","turnId":"u","item":{"type":"fileChange","id":"fc2","status":"declined","changes":[{"path":"a.txt","kind":{"type":"update","move_path":"b.txt"},"diff":"--- a/a.txt\n+++ b/b.txt\n@@ -1 +1 @@\n-x\n+y\n"}]}}`
	evs := notif(t, tr, noteItemCompleted, params)
	if len(evs) != 1 || evs[0].Kind != agent.KindToolResult || evs[0].Tool.Status != agent.ToolError || evs[0].Tool.Output != "declined" {
		t.Fatalf("declined = %+v", evs)
	}
	params = strings.Replace(params, `"declined"`, `"completed"`, 1)
	evs = notif(t, tr, noteItemCompleted, params)
	d := evs[0].Diff
	if d.Path != "b.txt" || d.OldPath != "a.txt" || d.Added != 1 || d.Removed != 1 {
		t.Fatalf("rename = %+v", d)
	}
}

func TestTranslateTurnFailedAndError(t *testing.T) {
	tr := newTranslator("c1", "")
	called := []string{}
	tr.onTurn = func(id string) { called = append(called, id) }
	notif(t, tr, noteTurnStarted, `{"threadId":"t","turn":{"id":"u1","status":"inProgress"}}`)
	evs := notif(t, tr, noteError, `{"threadId":"t","turnId":"u1","willRetry":true,"error":{"message":"stream disconnected"}}`)
	if len(evs) != 1 || evs[0].Kind != agent.KindError || evs[0].Text != "stream disconnected (retrying)" {
		t.Fatalf("error = %+v", evs)
	}
	evs = notif(t, tr, noteTurnCompleted, `{"threadId":"t","turn":{"id":"u1","status":"failed","error":{"message":"boom"}}}`)
	if got := kinds(evs); len(got) != 2 || got[0] != agent.KindError || got[1] != agent.KindTurnDone || evs[0].Text != "boom" || evs[1].Text != "failed" {
		t.Fatalf("failed turn = %+v", evs)
	}
	evs = notif(t, tr, noteTurnCompleted, `{"threadId":"t","turn":{"id":"u1","status":"interrupted"}}`)
	if len(evs) != 1 || evs[0].Kind != agent.KindTurnDone || evs[0].Text != "interrupted" {
		t.Fatalf("interrupted = %+v", evs)
	}
	if strings.Join(called, ",") != "u1,," {
		t.Errorf("onTurn calls = %q", called)
	}
}

func TestTranslateFailedCommandAndReasoning(t *testing.T) {
	tr := newTranslator("c1", "")
	notif(t, tr, noteReasoningSummary, `{"itemId":"r1","delta":"Thinking"}`)
	evs := notif(t, tr, noteItemCompleted, `{"threadId":"t","turnId":"u","item":{"type":"reasoning","id":"r1","summary":["Thinking about it"],"content":[]}}`)
	if len(evs) != 1 || evs[0].Kind != agent.KindReasoning || evs[0].Delta || evs[0].Text != "Thinking about it" {
		t.Fatalf("reasoning = %+v", evs)
	}
	notif(t, tr, noteItemStarted, `{"threadId":"t","turnId":"u","item":{"type":"commandExecution","id":"c1","command":"go test","cwd":"/x","status":"inProgress","commandActions":[]}}`)
	notif(t, tr, noteCommandOutput, `{"itemId":"c1","delta":"FAIL\n"}`)
	evs = notif(t, tr, noteItemCompleted, `{"threadId":"t","turnId":"u","item":{"type":"commandExecution","id":"c1","command":"go test","cwd":"/x","status":"failed","exitCode":1,"commandActions":[]}}`)
	if len(evs) != 1 || evs[0].Tool.Status != agent.ToolError || evs[0].Tool.Output != "FAIL\n" || evs[0].Tool.Title != "go test" {
		t.Fatalf("failed cmd = %+v", evs[0].Tool)
	}
}

func TestFixMojibake(t *testing.T) {
	cases := map[string]string{
		"Iâ€™ll":         "I’ll",
		"plain ascii":    "plain ascii",
		"café":           "café", // valid already: untouched
		"already ’ fine": "already ’ fine",
		"â€“ dash":       "– dash",
		"Ã©":             "é",
		"日本":             "日本",
		"â€":             "â€", // truncated sequence: not valid UTF-8, untouched
	}
	for in, want := range cases {
		if got := fixMojibake(in); got != want {
			t.Errorf("fixMojibake(%q) = %q, want %q", in, got, want)
		}
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
