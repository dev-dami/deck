package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dev-dami/deck/internal/deck/gh"
)

// git runs a read-only git command in the thread's work tree. With the real
// runner only as much output as a result can show is kept in memory (a diff
// of generated files can run to hundreds of megabytes); the rest is counted.
func git(ctx context.Context, env *Env, args ...string) (string, error) {
	if env.Dir == "" {
		return "", errors.New("this thread has no folder")
	}
	if env.Run != nil {
		return env.Run(ctx, env.Dir, "", "git", args...)
	}
	out, total, err := gh.ExecLimit(ctx, env.Dir, "", "git", MaxOutput+capSlack, args...)
	env.dropped += total - int64(len(out))
	return out, err
}

// safeArg refuses a value git could read as an option.
func safeArg(what, v string) error {
	if strings.HasPrefix(strings.TrimSpace(v), "-") {
		return fmt.Errorf("%s must not start with '-'", what)
	}
	if strings.ContainsAny(v, "\x00\n\r") {
		return fmt.Errorf("%s has control characters", what)
	}
	return nil
}

func opGitStatus(ctx context.Context, env *Env, _ args) (string, error) {
	return git(ctx, env, "-c", "color.ui=false", "status", "--short", "--branch")
}

func opGitLog(ctx context.Context, env *Env, a args) (string, error) {
	n := a.num("n", 20, 1, 200)
	return git(ctx, env, "log", "-n", strconv.Itoa(n), "--no-color", "--date=short", "--pretty=format:%h %ad %an  %s%d")
}

func opGitDiff(ctx context.Context, env *Env, a args) (string, error) {
	base := []string{"-c", "color.ui=false", "diff", "--no-ext-diff", "--no-textconv", "--no-color"}
	if a.flag("staged") {
		base = append(base, "--cached")
	}
	var tail []string
	if p := strings.TrimSpace(a.str("path")); p != "" {
		if err := safeArg("path", p); err != nil {
			return "", err
		}
		tail = []string{"--", p}
	}
	// One run: --patch-with-stat prints the stat, a blank line, then the patch.
	out, err := git(ctx, env, append(append(base, "--patch-with-stat"), tail...)...)
	if err != nil {
		return out, err
	}
	if strings.TrimSpace(out) == "" {
		if a.flag("staged") {
			return "No staged changes.", nil
		}
		return "No unstaged changes (pass staged:true for the index).", nil
	}
	return out, nil
}

func opGitShow(ctx context.Context, env *Env, a args) (string, error) {
	ref := strings.TrimSpace(a.str("ref"))
	if err := safeArg("ref", ref); err != nil {
		return "", err
	}
	return git(ctx, env, "-c", "color.ui=false", "show", "--no-ext-diff", "--no-textconv", "--no-color", "--stat", "-p", ref, "--")
}

func opGitBranches(ctx context.Context, env *Env, _ args) (string, error) {
	return git(ctx, env, "for-each-ref", "--sort=-committerdate", "--count=50",
		"--format=%(HEAD) %(refname:short)  %(objectname:short)  %(committerdate:relative)  %(upstream:short) %(upstream:track)",
		"refs/heads")
}

// ghClients keeps one gh client per folder for a while: a client remembers
// that gh is signed in, so `gh auth status` no longer runs before every gh
// op. A client is replaced after ghClientTTL, which asks gh again.
var ghClients = struct {
	sync.Mutex
	m map[string]ghEntry
}{m: map[string]ghEntry{}}

type ghEntry struct {
	c  *gh.Client
	at time.Time
}

const ghClientTTL = 10 * time.Minute

func ghClient(env *Env) (*gh.Client, error) {
	if env.Dir == "" {
		return nil, errors.New("this thread has no folder")
	}
	if env.Run != nil { // a custom runner (tests) gets a client of its own
		return gh.NewWithRunner(env.Dir, env.Run), nil
	}
	ghClients.Lock()
	defer ghClients.Unlock()
	e, ok := ghClients.m[env.Dir]
	if !ok || time.Since(e.at) > ghClientTTL {
		if len(ghClients.m) >= 64 {
			clear(ghClients.m)
		}
		e = ghEntry{c: gh.New(env.Dir), at: time.Now()}
		ghClients.m[env.Dir] = e
	}
	// Only a yes is kept: signed out, gh is asked again every time, as the
	// user may have just run `gh auth login`.
	e.c.Recheck()
	return e.c, nil
}

func checksLine(cs []gh.Check) string {
	if len(cs) == 0 {
		return "no checks"
	}
	s := gh.Summarize(cs)
	return fmt.Sprintf("checks %s (%d passed, %d failed, %d pending)", s.State(), s.Passed, s.Failed, s.Pending)
}

func opGHPRs(ctx context.Context, env *Env, _ args) (string, error) {
	c, err := ghClient(env)
	if err != nil {
		return "", err
	}
	prs, err := c.PRList(ctx)
	if err != nil {
		return "", err
	}
	if len(prs) == 0 {
		return "No open pull requests.", nil
	}
	var b strings.Builder
	for _, p := range prs {
		draft := ""
		if p.IsDraft {
			draft = " draft"
		}
		fmt.Fprintf(&b, "#%d %s [%s%s] by %s, updated %s, %s\n   %s\n", p.Number, p.Title, p.HeadRefName, draft,
			p.Author.Login, p.UpdatedAt.Format("2006-01-02"), checksLine(p.Checks), p.URL)
	}
	return b.String(), nil
}

func opGHPR(ctx context.Context, env *Env, a args) (string, error) {
	n := a.num("number", 0, 0, 1<<30)
	if n <= 0 {
		return "", errors.New("number must be a pull request number")
	}
	c, err := ghClient(env)
	if err != nil {
		return "", err
	}
	p, err := c.PRView(ctx, n)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "#%d %s\n%s  %s -> %s  by %s  draft=%v  review=%s  mergeable=%s  updated %s\n%s\n",
		p.Number, p.Title, p.State, p.HeadRefName, p.BaseRefName, p.Author.Login, p.IsDraft,
		orNone(p.ReviewDecision), orNone(p.Mergeable), p.UpdatedAt.Format("2006-01-02 15:04"), p.URL)
	cs, err := c.Checks(ctx, n)
	if err != nil {
		fmt.Fprintf(&b, "\nchecks: %v\n", err)
		return b.String(), nil
	}
	fmt.Fprintf(&b, "\n%s:\n", checksLine(cs))
	for _, ch := range cs {
		name := ch.Name
		if ch.Workflow != "" {
			name = ch.Workflow + " / " + name
		}
		fmt.Fprintf(&b, "  %-8s %s  %s\n", ch.Bucket, name, ch.Link)
	}
	return b.String(), nil
}

func orNone(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func opGHRuns(ctx context.Context, env *Env, a args) (string, error) {
	if env.Dir == "" {
		return "", errors.New("this thread has no folder")
	}
	n := a.num("limit", 10, 1, 50)
	out, err := env.runner()(ctx, env.Dir, "", "gh", "run", "list", "--limit", strconv.Itoa(n),
		"--json", "databaseId,displayTitle,status,conclusion,workflowName,headBranch,event,createdAt,url")
	if err != nil {
		return out, err
	}
	var runs []struct {
		ID         int64     `json:"databaseId"`
		Title      string    `json:"displayTitle"`
		Status     string    `json:"status"`
		Conclusion string    `json:"conclusion"`
		Workflow   string    `json:"workflowName"`
		Branch     string    `json:"headBranch"`
		Event      string    `json:"event"`
		Created    time.Time `json:"createdAt"`
		URL        string    `json:"url"`
	}
	if err := json.Unmarshal([]byte(out), &runs); err != nil {
		return "", fmt.Errorf("gh run list: %w", err)
	}
	if len(runs) == 0 {
		return "No workflow runs.", nil
	}
	var b strings.Builder
	for _, r := range runs {
		state := r.Conclusion
		if state == "" {
			state = r.Status
		}
		fmt.Fprintf(&b, "%d %-10s %s: %s [%s, %s] %s\n   %s\n", r.ID, state, r.Workflow, r.Title, r.Branch, r.Event,
			r.Created.Format("2006-01-02 15:04"), r.URL)
	}
	return b.String(), nil
}

func opDeckJobs(_ context.Context, env *Env, _ args) (string, error) {
	if env.Activity == nil {
		return "", errors.New("Deck's activity is not available here")
	}
	out := env.Activity()
	if strings.TrimSpace(out) == "" {
		return "No commands running and no background shells.", nil
	}
	return out, nil
}
