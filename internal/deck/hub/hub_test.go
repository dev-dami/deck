package hub

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/store"
)

// testEnv is an Env with an empty home and project, and no history.
func testEnv(t *testing.T) Env {
	t.Helper()
	dir := t.TempDir()
	return Env{AgentID: "codex", Dir: filepath.Join(dir, "proj"), Home: filepath.Join(dir, "home")}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, env Env, op, args string) (string, bool) {
	t.Helper()
	var raw json.RawMessage
	if args != "" {
		raw = json.RawMessage(args)
	}
	return Execute(context.Background(), env, op, raw)
}

func TestSearchEmptyQueryListsCatalog(t *testing.T) {
	out := Search(testEnv(t), json.RawMessage(`{}`))
	for _, o := range Ops() {
		if !strings.Contains(out, "  "+o.Name+" ") {
			t.Errorf("empty search misses %s:\n%s", o.Name, out)
		}
	}
	if !strings.Contains(out, "thread.send {to*, message*") {
		t.Errorf("signature missing:\n%s", out)
	}
	if !strings.Contains(out, "Skills:") {
		t.Errorf("skills count missing:\n%s", out)
	}
	// search is reachable through execute too.
	if got, isErr := run(t, testEnv(t), "search", `{"query":""}`); isErr || got != out {
		t.Errorf("execute search differs: %v", isErr)
	}
}

func TestSearchRanking(t *testing.T) {
	env := testEnv(t)
	out := Search(env, json.RawMessage(`{"query":"git log"}`))
	ops := strings.SplitN(out, "\n", 3)[1]
	if !strings.HasPrefix(strings.TrimSpace(ops), "git.log ") {
		t.Fatalf("git.log should rank first:\n%s", out)
	}
	out = Search(env, json.RawMessage(`{"query":"pull request checks"}`))
	if first := strings.TrimSpace(strings.SplitN(out, "\n", 3)[1]); !strings.HasPrefix(first, "gh.pr") {
		t.Fatalf("a gh op should rank first:\n%s", out)
	}

	writeFile(t, filepath.Join(env.Dir, ".claude", "skills", "pdf-maker", "SKILL.md"),
		"---\nname: pdf-maker\ndescription: Make PDF files from markdown.\n---\nUse pandoc.\n")
	writeFile(t, filepath.Join(env.Home, ".codex", "skills", "tidy", "SKILL.md"),
		"---\nname: tidy\ndescription: Tidy code.\n---\n")
	out = Search(env, json.RawMessage(`{"query":"pdf"}`))
	if !strings.Contains(out, "pdf-maker [claude-project] - Make PDF files from markdown.") {
		t.Fatalf("skill hit missing:\n%s", out)
	}
	if strings.Contains(out, "tidy [") {
		t.Fatalf("unrelated skill listed:\n%s", out)
	}
}

func TestExecuteUnknownOpAndBadArgs(t *testing.T) {
	env := testEnv(t)
	for _, c := range []struct{ op, args, want string }{
		{"git.lgo", "", "Unknown op"},
		{"nope", "", "Call search"},
		{"git.log", `{"count":3}`, `unknown argument "count"`},
		{"git.log", `{"n":"many"}`, "must be a number"},
		{"git.show", `{}`, `missing argument "ref"`},
		{"history.search", `{"query":""}`, "is empty"},
		{"git.diff", `[1]`, "JSON object"},
	} {
		got, isErr := run(t, env, c.op, c.args)
		if !isErr || !strings.Contains(got, c.want) {
			t.Errorf("%s %s = %q (isErr %v), want error with %q", c.op, c.args, got, isErr, c.want)
		}
		if !strings.Contains(got, "search") {
			t.Errorf("%s: error does not point at search: %q", c.op, got)
		}
	}
	if got, _ := run(t, env, "git.lgo", ""); !strings.Contains(got, "git.log") {
		t.Errorf("no suggestion: %q", got)
	}
	// The broker's ops are listed but not run here.
	if _, isErr := run(t, env, agent.OpThreadRead, ""); !isErr {
		t.Error("thread.read must not run in the hub")
	}
}

func TestSkillsReadStaysInsideTheSkill(t *testing.T) {
	env := testEnv(t)
	skill := filepath.Join(env.Dir, ".agents", "skills", "deploy")
	writeFile(t, filepath.Join(skill, "SKILL.md"), "---\nname: deploy\ndescription: Ship it.\n---\nRun make deploy.\n")
	writeFile(t, filepath.Join(skill, "ref", "notes.md"), "notes body")
	writeFile(t, filepath.Join(env.Dir, "secret.txt"), "TOP SECRET")

	got, isErr := run(t, env, "skills.read", `{"name":"deploy"}`)
	if isErr || !strings.Contains(got, "Run make deploy.") || !strings.Contains(got, "ref/notes.md") || !strings.Contains(got, "agents-project") {
		t.Fatalf("SKILL.md read = %q %v", got, isErr)
	}
	if got, isErr = run(t, env, "skills.read", `{"name":"deploy","file":"ref/notes.md"}`); isErr || !strings.Contains(got, "notes body") {
		t.Fatalf("file read = %q %v", got, isErr)
	}
	abs, _ := filepath.Abs(filepath.Join(env.Dir, "secret.txt"))
	for _, f := range []string{"../../../secret.txt", "..", "ref/../../../../secret.txt", "/etc/passwd", abs, `..\..\..\secret.txt`, "C:secret.txt"} {
		args, _ := json.Marshal(map[string]string{"name": "deploy", "file": f})
		got, isErr := run(t, env, "skills.read", string(args))
		if !isErr || strings.Contains(got, "TOP SECRET") {
			t.Errorf("file %q not refused: %q", f, got)
		}
	}
	// A link that leads out of the skill is refused too.
	if err := os.Symlink(filepath.Join(env.Dir, "secret.txt"), filepath.Join(skill, "link.txt")); err == nil {
		got, isErr := run(t, env, "skills.read", `{"name":"deploy","file":"link.txt"}`)
		if !isErr || strings.Contains(got, "TOP SECRET") {
			t.Errorf("symlink escape not refused: %q", got)
		}
	}
	if _, isErr := run(t, env, "skills.read", `{"name":"missing"}`); !isErr {
		t.Error("unknown skill must be an error")
	}
}

func TestSkillsListSourcesAndBuiltins(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.Home, ".claude", "skills", "writer", "SKILL.md"), "---\nname: writer\ndescription: Writes.\n---\n")
	writeFile(t, filepath.Join(env.Home, ".config", "opencode", "skill", "Odd Name", "SKILL.md"), "no front matter")
	writeFile(t, filepath.Join(env.Home, ".gemini", "antigravity", "skills", "grav", "SKILL.md"), "---\nname: grav\ndescription: G.\n---\n")
	got, isErr := run(t, env, "skills.list", "")
	if isErr {
		t.Fatal(got)
	}
	for _, want := range []string{"writer [claude-user]", "Odd Name [opencode-user]", "grav [antigravity-user]", "[deck-builtin]"} {
		if !strings.Contains(got, want) {
			t.Errorf("skills.list misses %q:\n%s", want, got)
		}
	}
	// A builtin reads from the binary.
	if got, isErr := run(t, env, "skills.read", `{"name":"jq","source":"deck"}`); isErr || got == "" {
		t.Errorf("builtin read = %q %v", got, isErr)
	}
}

func TestSkillsUsedDetection(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.Home, ".claude", "skills", "pdf", "SKILL.md"), "---\nname: pdf\ndescription: PDFs.\n---\n")
	env.Tools = []ToolUse{
		{Agent: "claude", Name: "Skill", Title: "pdf", Input: json.RawMessage(`{"skill":"pdf"}`)},
		{Agent: "codex", Name: "Bash", Title: "cat ~/.codex/skills/xlsx/SKILL.md"},
		{Agent: "opencode", Name: "Read", Title: `C:\Users\me\.claude\skills\pdf\SKILL.md`},
		{Agent: "agy", Name: "deck", Input: json.RawMessage(`{"op":"skills.read","args":{"name":"tidy"}}`)},
		{Agent: "claude", Name: "Bash", Title: "ls"},
	}
	got, isErr := run(t, env, "skills.used", "")
	if isErr {
		t.Fatal(got)
	}
	for _, want := range []string{
		"pdf - used by claude, opencode (Skill tool, read SKILL.md); available from claude-user",
		"xlsx - used by codex (read SKILL.md); not found",
		"tidy - used by agy (skills.read)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("skills.used misses %q:\n%s", want, got)
		}
	}
	env.Tools = nil
	if got, _ := run(t, env, "skills.used", ""); !strings.Contains(got, "No skill use") {
		t.Errorf("empty = %q", got)
	}
}

func TestHistorySearchAndRead(t *testing.T) {
	env := testEnv(t)
	st, err := store.New(filepath.Join(t.TempDir(), "threads"))
	if err != nil {
		t.Fatal(err)
	}
	env.Store = st
	env.Roots = []string{env.Dir}
	mine, err := st.Create(store.Thread{Title: "fix the pelican bug", Dir: env.Dir,
		Members: []store.Member{{ID: "claude", Kind: "claude", Name: "claude"}}})
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.Create(store.Thread{Title: "elsewhere", Dir: filepath.Join(t.TempDir(), "other"),
		Members: []store.Member{{ID: "codex", Kind: "codex", Name: "codex"}}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	add := func(id string, e store.Entry) {
		t.Helper()
		e.Time = now
		if err := st.AppendEntry(id, e); err != nil {
			t.Fatal(err)
		}
	}
	add(mine.ID, store.Entry{Type: store.EntryUser, Speaker: "dami", Event: agent.Event{Text: "the pelican parser drops the last token"}})
	add(mine.ID, store.Entry{Event: agent.Event{AgentID: "claude", Kind: agent.KindAssistantText, Text: "Fixed the pelican parser in parse.go; key ghp_abcdefghijklmnopqrstuvwxyz0123 removed."}})
	add(mine.ID, store.Entry{Event: agent.Event{AgentID: "claude", Kind: agent.KindToolResult, Tool: &agent.Tool{Name: "Bash", Title: "env", Output: "PELICAN_SECRET=hunter2"}}})
	add(other.ID, store.Entry{Type: store.EntryUser, Speaker: "dami", Event: agent.Event{Text: "pelican elsewhere"}})

	got, isErr := run(t, env, "history.search", `{"query":"pelican parser"}`)
	if isErr || !strings.Contains(got, "fix the pelican bug ["+mine.ID+"]") || !strings.Contains(got, "claude: Fixed the pelican parser") {
		t.Fatalf("history.search = %q %v", got, isErr)
	}
	if strings.Contains(got, other.ID) || strings.Contains(got, "hunter2") {
		t.Fatalf("another project's thread or a tool output leaked:\n%s", got)
	}
	if strings.Contains(got, "ghp_abcdefghijklmnopqrstuvwxyz0123") {
		t.Fatalf("token not redacted:\n%s", got)
	}
	// search shows history hits too.
	if out := Search(env, json.RawMessage(`{"query":"pelican"}`)); !strings.Contains(out, "History") || !strings.Contains(out, mine.ID) {
		t.Fatalf("search history section:\n%s", out)
	}

	got, isErr = run(t, env, "history.threads", "")
	if isErr || !strings.Contains(got, mine.ID) || strings.Contains(got, other.ID) || !strings.Contains(got, "[claude]") {
		t.Fatalf("history.threads = %q", got)
	}
	got, isErr = run(t, env, "history.read", `{"thread":"`+mine.ID+`","last":5}`)
	if isErr || !strings.Contains(got, "dami: the pelican parser drops the last token") || strings.Contains(got, "hunter2") {
		t.Fatalf("history.read = %q", got)
	}
	if got, isErr = run(t, env, "history.read", `{"thread":"`+other.ID+`"}`); !isErr {
		t.Fatalf("another project's thread was readable: %q", got)
	}
}

func TestGitOpsAreReadOnlyAndUseTheRunner(t *testing.T) {
	env := testEnv(t)
	var calls []string
	env.Run = func(_ context.Context, dir, stdin, name string, args ...string) (string, error) {
		if dir != env.Dir || stdin != "" {
			t.Errorf("ran in %q with stdin %q", dir, stdin)
		}
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "gh" && len(args) > 1 && args[0] == "run" {
			return `[{"databaseId":7,"displayTitle":"CI","status":"completed","conclusion":"success","workflowName":"test","headBranch":"main","event":"push","createdAt":"2026-10-01T10:00:00Z","url":"https://github.com/o/r/actions/runs/7"}]`, nil
		}
		return "out of " + args[0] + " https://user:hunter2@example.com/repo.git", nil
	}
	if got, isErr := run(t, env, "git.status", ""); isErr || strings.Contains(got, "hunter2") {
		t.Fatalf("git.status = %q %v", got, isErr)
	}
	run(t, env, "git.log", `{"n":500}`)
	run(t, env, "git.diff", `{"path":"a b.go","staged":true}`)
	run(t, env, "git.show", `{"ref":"HEAD~1"}`)
	if got, isErr := run(t, env, "gh.runs", `{"limit":"3"}`); isErr || !strings.Contains(got, "7 success") {
		t.Fatalf("gh.runs = %q %v", got, isErr)
	}
	for _, bad := range []string{`{"ref":"--output=/tmp/x"}`, `{"ref":"-p"}`} {
		if _, isErr := run(t, env, "git.show", bad); !isErr {
			t.Errorf("git.show %s was not refused", bad)
		}
	}
	if _, isErr := run(t, env, "git.diff", `{"path":"--output=x"}`); !isErr {
		t.Error("git.diff option path was not refused")
	}
	all := strings.Join(calls, "\n")
	for _, want := range []string{
		"git -c color.ui=false status --short --branch",
		"git log -n 200 ",
		"git -c color.ui=false diff --no-ext-diff --no-textconv --no-color --cached --patch-with-stat -- a b.go",
		"git -c color.ui=false show --no-ext-diff --no-textconv --no-color --stat -p HEAD~1 --",
		"gh run list --limit 3 ",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing call %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "--output") {
		t.Errorf("an option reached git:\n%s", all)
	}
}

func TestDeckJobs(t *testing.T) {
	env := testEnv(t)
	if _, isErr := run(t, env, "deck.jobs", ""); !isErr {
		t.Error("deck.jobs without activity must be an error")
	}
	env.Activity = func() string { return "Running now:\n  claude [Bash] npm test\n" }
	if got, isErr := run(t, env, "deck.jobs", ""); isErr || !strings.Contains(got, "npm test") {
		t.Errorf("deck.jobs = %q %v", got, isErr)
	}
}

func TestRedactAndCap(t *testing.T) {
	in := "a ghp_0123456789abcdefghijABCDEFGHIJ b sk-ant-api03-abcdefghijklmnopqrstuvwxyz c https://me:pw@host/x Bearer abcdefghijklmnopqrstuvwxyz0123"
	out := Redact(in)
	for _, leak := range []string{"ghp_0123", "sk-ant-api03", "me:pw", "abcdefghijklmnopqrstuvwxyz0123"} {
		if strings.Contains(out, leak) {
			t.Errorf("%q leaked: %s", leak, out)
		}
	}
	if !strings.Contains(out, "https://[redacted]@host/x") {
		t.Errorf("url userinfo: %s", out)
	}
	long := strings.Repeat("é", MaxOutput)
	c := Cap(long, MaxOutput)
	if len(c) > MaxOutput+200 || !strings.Contains(c, "truncated") {
		t.Errorf("cap: %d bytes", len(c))
	}
}
