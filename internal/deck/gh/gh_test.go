package gh

import (
	"context"
	"strings"
	"testing"
	"time"
)

// call is one command the fake runner saw.
type call struct {
	name  string
	args  []string
	stdin string
}

// reply is what the fake runner answers to a command whose "name args"
// line starts with prefix.
type reply struct {
	prefix string
	out    string
	err    error
}

// fake is a [Runner] that answers from replies, first match wins, and
// records every call. `gh auth status` succeeds unless a reply says
// otherwise.
type fake struct {
	replies []reply
	calls   []call
}

func (f *fake) run(_ context.Context, _, stdin, name string, args ...string) (string, error) {
	f.calls = append(f.calls, call{name: name, args: args, stdin: stdin})
	line := name + " " + strings.Join(args, " ")
	for _, r := range f.replies {
		if strings.HasPrefix(line, r.prefix) {
			return r.out, r.err
		}
	}
	if line == "gh auth status" {
		return "", nil
	}
	return "", &ExitError{Cmd: name, Code: 1, Stderr: "unexpected: " + line}
}

func (f *fake) saw(prefix string) *call {
	for i, c := range f.calls {
		if strings.HasPrefix(c.name+" "+strings.Join(c.args, " "), prefix) {
			return &f.calls[i]
		}
	}
	return nil
}

func newFake(replies ...reply) (*Client, *fake) {
	f := &fake{replies: replies}
	return NewWithRunner("/repo", f.run), f
}

func TestStatus(t *testing.T) {
	c, _ := newFake(reply{prefix: "git status", out: strings.Join([]string{
		"# branch.oid 1234567890abcdef",
		"# branch.head feature/login",
		"# branch.upstream origin/feature/login",
		"# branch.ab +2 -1",
		"1 .M N... 100644 100644 100644 aaa bbb internal/a.go",
		"1 A. N... 000000 100644 100644 000 ccc internal/b.go",
		"? notes.txt",
		"! build/out",
	}, "\n")})
	st, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := Status{Branch: "feature/login", Upstream: "origin/feature/login", Ahead: 2, Behind: 1, Dirty: 3, Head: "1234567890abcdef"}
	if st != want {
		t.Fatalf("got %+v, want %+v", st, want)
	}
}

func TestStatusDetachedNoUpstream(t *testing.T) {
	c, _ := newFake(reply{prefix: "git status", out: "# branch.oid abc\n# branch.head (detached)\n"})
	st, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !st.Detached || st.Branch != "" || st.Upstream != "" || st.Dirty != 0 {
		t.Fatalf("got %+v", st)
	}
}

func TestRecentBranches(t *testing.T) {
	out := "main\x00origin/main\x001700000000\x00Merge pull request #3\n" +
		"wip\x00\x001700000100\x00try a thing\n"
	c, f := newFake(reply{prefix: "git for-each-ref", out: out})
	bs, err := c.RecentBranches(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 2 {
		t.Fatalf("got %d branches", len(bs))
	}
	if bs[0].Name != "main" || bs[0].Upstream != "origin/main" || bs[0].Subject != "Merge pull request #3" {
		t.Fatalf("first: %+v", bs[0])
	}
	if bs[1].Upstream != "" || !bs[1].Committed.Equal(time.Unix(1700000100, 0)) {
		t.Fatalf("second: %+v", bs[1])
	}
	if c := f.saw("git for-each-ref"); c == nil || !strings.Contains(strings.Join(c.args, " "), "--count=5") {
		t.Fatalf("count not passed: %+v", c)
	}
}

func TestPushSetsUpstreamOnce(t *testing.T) {
	c, f := newFake(
		reply{prefix: "git status", out: "# branch.head topic\n"},
		reply{prefix: "git push", out: ""},
	)
	if err := c.Push(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.saw("git push -u origin topic") == nil {
		t.Fatalf("calls: %+v", f.calls)
	}

	c, f = newFake(
		reply{prefix: "git status", out: "# branch.head topic\n# branch.upstream origin/topic\n# branch.ab +1 -0\n"},
		reply{prefix: "git push", out: ""},
	)
	if err := c.Push(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.saw("git push -u") != nil || f.saw("git push") == nil {
		t.Fatalf("calls: %+v", f.calls)
	}
}

func TestCommitAll(t *testing.T) {
	c, f := newFake(reply{prefix: "git add -A"}, reply{prefix: "git commit -m"})
	if err := c.CommitAll(context.Background(), "Fix the login form"); err != nil {
		t.Fatal(err)
	}
	cm := f.saw("git commit")
	if cm == nil || cm.args[2] != "Fix the login form" {
		t.Fatalf("calls: %+v", f.calls)
	}
	if err := c.CommitAll(context.Background(), "  "); err == nil {
		t.Fatal("empty message accepted")
	}
}

func TestDefaultBranch(t *testing.T) {
	c, _ := newFake(reply{prefix: "gh repo view", out: `{"defaultBranchRef":{"name":"trunk"}}`})
	if got := c.DefaultBranch(context.Background()); got != "trunk" {
		t.Fatalf("got %q", got)
	}

	c, _ = newFake(
		reply{prefix: "gh auth status", err: &ExitError{Cmd: "gh auth", Code: 1}},
		reply{prefix: "git symbolic-ref", out: "origin/develop\n"},
	)
	if got := c.DefaultBranch(context.Background()); got != "develop" {
		t.Fatalf("got %q", got)
	}

	c, _ = newFake(reply{prefix: "gh auth status", err: &ExitError{Cmd: "gh auth", Code: 1}})
	if got := c.DefaultBranch(context.Background()); got != "main" {
		t.Fatalf("got %q", got)
	}
}

func TestLogSinceFallsBackToLocalBase(t *testing.T) {
	c, _ := newFake(
		reply{prefix: "git log --format=%s origin/main..HEAD", err: &ExitError{Cmd: "git log", Code: 128}},
		reply{prefix: "git log --format=%s main..HEAD", out: "Add tests\nAdd the form\n"},
	)
	got, err := c.LogSince(context.Background(), "main")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != "Add tests|Add the form" {
		t.Fatalf("got %q", got)
	}
}

const prViewJSON = `{
  "number": 12,
  "title": "Add the login form",
  "state": "OPEN",
  "url": "https://github.com/acme/app/pull/12",
  "isDraft": false,
  "reviewDecision": "REVIEW_REQUIRED",
  "mergeable": "MERGEABLE",
  "baseRefName": "main",
  "headRefName": "feature/login",
  "author": {"login": "octocat"},
  "updatedAt": "2026-01-02T03:04:05Z",
  "statusCheckRollup": [
    {"__typename": "CheckRun", "name": "test (ubuntu-latest)", "workflowName": "CI", "status": "COMPLETED", "conclusion": "FAILURE",
     "detailsUrl": "https://github.com/acme/app/actions/runs/111/job/222"},
    {"__typename": "CheckRun", "name": "lint", "workflowName": "CI", "status": "IN_PROGRESS", "conclusion": "",
     "detailsUrl": "https://github.com/acme/app/actions/runs/111/job/223"},
    {"__typename": "CheckRun", "name": "docs", "workflowName": "CI", "status": "COMPLETED", "conclusion": "SKIPPED",
     "detailsUrl": "https://github.com/acme/app/actions/runs/111/job/224"},
    {"__typename": "StatusContext", "context": "review-bot", "state": "SUCCESS", "targetUrl": ""}
  ]
}`

func TestPRForBranch(t *testing.T) {
	c, f := newFake(reply{prefix: "gh pr view", out: prViewJSON})
	pr, err := c.PRForBranch(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if pr == nil || pr.Number != 12 || pr.HeadRefName != "feature/login" || pr.Author.Login != "octocat" {
		t.Fatalf("got %+v", pr)
	}
	if len(pr.Checks) != 4 {
		t.Fatalf("checks: %+v", pr.Checks)
	}
	if pr.Checks[0].Bucket != BucketFail || pr.Checks[0].RunID() != "111" || pr.Checks[0].JobID() != "222" {
		t.Fatalf("first check: %+v", pr.Checks[0])
	}
	if pr.Checks[3].Name != "review-bot" || pr.Checks[3].Bucket != BucketPass {
		t.Fatalf("status context: %+v", pr.Checks[3])
	}
	s := Summarize(pr.Checks)
	if s != (CheckSummary{Total: 4, Passed: 2, Failed: 1, Pending: 1}) || s.State() != ChecksPending {
		t.Fatalf("summary %+v %s", s, s.State())
	}
	if v := f.saw("gh pr view"); v == nil || v.args[2] != "--json" {
		t.Fatalf("current branch must not be named: %+v", v)
	}
}

func TestPRForBranchNone(t *testing.T) {
	c, _ := newFake(reply{prefix: "gh pr view", err: &ExitError{Cmd: "gh pr view", Code: 1,
		Stderr: `no pull requests found for branch "wip"`}})
	pr, err := c.PRForBranch(context.Background(), "wip")
	if err != nil || pr != nil {
		t.Fatalf("got %+v, %v", pr, err)
	}
}

func TestGHUnavailable(t *testing.T) {
	c, f := newFake(reply{prefix: "gh auth status", err: &ExitError{Cmd: "gh auth", Code: 1}})
	if _, err := c.PRList(context.Background()); err != ErrNoGH {
		t.Fatalf("got %v", err)
	}
	if _, err := c.PRForBranch(context.Background(), ""); err != ErrNoGH {
		t.Fatalf("got %v", err)
	}
	n := 0
	for _, cl := range f.calls {
		if cl.name == "gh" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("auth status asked %d times, want it cached", n)
	}
}

func TestPRList(t *testing.T) {
	out := `[{"number":4,"title":"Docs","headRefName":"docs","author":{"login":"octocat"},"isDraft":true,
	  "updatedAt":"2026-01-02T03:04:05Z","url":"https://github.com/acme/app/pull/4","statusCheckRollup":[]},
	 {"number":3,"title":"Fix","headRefName":"fix","author":{"login":"hubot"},"isDraft":false,
	  "updatedAt":"2026-01-01T03:04:05Z","url":"https://github.com/acme/app/pull/3",
	  "statusCheckRollup":[{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"SUCCESS","detailsUrl":""}]}]`
	c, _ := newFake(reply{prefix: "gh pr list", out: out})
	ps, err := c.PRList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || !ps[0].IsDraft || ps[1].Author.Login != "hubot" {
		t.Fatalf("got %+v", ps)
	}
	if Summarize(ps[0].Checks).State() != ChecksNone || Summarize(ps[1].Checks).State() != ChecksPassed {
		t.Fatalf("summaries wrong: %+v %+v", ps[0].Checks, ps[1].Checks)
	}
}

func TestChecksPendingExitIsNotAnError(t *testing.T) {
	out := `[{"name":"test","state":"IN_PROGRESS","bucket":"pending","link":"https://github.com/acme/app/actions/runs/9/job/10","workflow":"CI"},
	 {"name":"lint","state":"FAILURE","bucket":"fail","link":"https://github.com/acme/app/actions/runs/9/job/11","workflow":"CI"}]`
	c, _ := newFake(reply{prefix: "gh pr checks 12", out: out, err: &ExitError{Cmd: "gh pr checks", Code: 8}})
	cs, err := c.Checks(context.Background(), 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 || !cs[0].Pending() || !cs[1].Failed() {
		t.Fatalf("got %+v", cs)
	}
	if ids := FailedRunIDs(cs); len(ids) != 1 || ids[0] != "9" {
		t.Fatalf("failed runs %v", ids)
	}

	c, _ = newFake(reply{prefix: "gh pr checks 12", err: &ExitError{Cmd: "gh pr checks", Code: 1, Stderr: "boom"}})
	if _, err := c.Checks(context.Background(), 12); err == nil {
		t.Fatal("a real failure was swallowed")
	}
}

func TestCreatePR(t *testing.T) {
	c, f := newFake(reply{prefix: "gh pr create", out: "\nCreating pull request for topic into main\n\nhttps://github.com/acme/app/pull/57\n"})
	n, url, err := c.CreatePR(context.Background(), "Add the form", "- Add the form\n- Add tests", "main", true)
	if err != nil {
		t.Fatal(err)
	}
	if n != 57 || url != "https://github.com/acme/app/pull/57" {
		t.Fatalf("got %d %q", n, url)
	}
	cr := f.saw("gh pr create")
	args := strings.Join(cr.args, " ")
	if !strings.Contains(args, "--base main") || !strings.Contains(args, "--draft") || !strings.HasSuffix(args, "--body-file -") {
		t.Fatalf("args %q", args)
	}
	if cr.stdin != "- Add the form\n- Add tests" {
		t.Fatalf("body not on stdin: %q", cr.stdin)
	}
}

func TestMergeRejectsUnknownMethod(t *testing.T) {
	c, f := newFake(reply{prefix: "gh pr merge"})
	if err := c.Merge(context.Background(), 3, "octopus"); err == nil {
		t.Fatal("unknown method accepted")
	}
	if err := c.Merge(context.Background(), 3, "squash"); err != nil {
		t.Fatal(err)
	}
	if f.saw("gh pr merge 3 --squash") == nil {
		t.Fatalf("calls %+v", f.calls)
	}
}

func TestFailedLogTidies(t *testing.T) {
	var b strings.Builder
	for i := range 200 {
		b.WriteString("test (ubuntu-latest)\tRun go test\t2026-01-02T03:04:05.1234567Z \x1b[31mline ")
		b.WriteString(strings.Repeat("x", i%3))
		b.WriteString("\x1b[0m\n")
	}
	c, f := newFake(reply{prefix: "gh run view", out: b.String()})
	log, err := c.FailedLog(context.Background(), "111", "222")
	if err != nil {
		t.Fatal(err)
	}
	ls := strings.Split(log, "\n")
	if len(ls) != logTail {
		t.Fatalf("kept %d lines", len(ls))
	}
	if !strings.HasPrefix(ls[0], "line") || strings.Contains(log, "\x1b") || strings.Contains(log, "\t") {
		t.Fatalf("not tidied: %q", ls[0])
	}
	if f.saw("gh run view --job 222 --log-failed") == nil {
		t.Fatalf("calls %+v", f.calls)
	}
}

func TestCheckLinks(t *testing.T) {
	c := Check{Link: "https://github.com/acme/app/actions/runs/38113777286/job/114394499255"}
	if c.RunID() != "38113777286" || c.JobID() != "114394499255" {
		t.Fatalf("got %q %q", c.RunID(), c.JobID())
	}
	c = Check{Link: "https://example.com/report"}
	if c.RunID() != "" || c.JobID() != "" {
		t.Fatal("non-Actions link parsed")
	}
}

func TestCreateBranchAndPull(t *testing.T) {
	c, f := newFake(reply{prefix: "git checkout -b"}, reply{prefix: "git pull"})
	if err := c.CreateBranch(context.Background(), "feature/x"); err != nil {
		t.Fatal(err)
	}
	if f.saw("git checkout -b feature/x") == nil {
		t.Fatalf("calls %+v", f.calls)
	}
	if err := c.CreateBranch(context.Background(), "-x"); err == nil {
		t.Fatal("a branch name that reads as a flag must be refused")
	}
	if err := c.Pull(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.saw("git pull --ff-only") == nil {
		t.Fatalf("calls %+v", f.calls)
	}
}
