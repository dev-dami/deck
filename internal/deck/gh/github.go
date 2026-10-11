package gh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Available reports whether gh is installed and signed in (`gh auth status`
// exits 0). Only the exit code is looked at. A yes is kept for the run; a no
// is asked again after a while, so signing in mid-run is noticed.
func (c *Client) Available(ctx context.Context) bool {
	c.mu.Lock()
	if c.availSeen && (c.avail || time.Since(c.availAt) < availTTL) {
		ok := c.avail
		c.mu.Unlock()
		return ok
	}
	c.mu.Unlock()
	_, err := c.gh(ctx, localTimeout, "", "auth", "status")
	ok := err == nil
	c.mu.Lock()
	c.avail, c.availAt, c.availSeen = ok, time.Now(), true
	c.mu.Unlock()
	return ok
}

// Recheck forgets a "no" from [Client.Available], so the next call asks gh
// again (the user may have signed in since).
func (c *Client) Recheck() {
	c.mu.Lock()
	if !c.avail {
		c.availSeen = false
	}
	c.mu.Unlock()
}

// Installed reports whether a gh binary is on PATH, without running it.
func Installed() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

// Author is who opened a pull request.
type Author struct {
	Login string `json:"login"`
}

// PR is a pull request as `gh pr view/list --json` reports it, with its
// status check rollup read into [Check]s.
type PR struct {
	Number         int       `json:"number"`
	Title          string    `json:"title"`
	State          string    `json:"state"` // OPEN, CLOSED, MERGED
	URL            string    `json:"url"`
	IsDraft        bool      `json:"isDraft"`
	ReviewDecision string    `json:"reviewDecision"` // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED or ""
	Mergeable      string    `json:"mergeable"`      // MERGEABLE, CONFLICTING, UNKNOWN
	BaseRefName    string    `json:"baseRefName"`
	HeadRefName    string    `json:"headRefName"`
	Author         Author    `json:"author"`
	UpdatedAt      time.Time `json:"updatedAt"`
	Rollup         []rollup  `json:"statusCheckRollup"`

	// Checks is Rollup read into checks.
	Checks []Check `json:"-"`
}

// rollup is one entry of statusCheckRollup: a CheckRun (Actions and apps) or
// a StatusContext (commit statuses).
type rollup struct {
	Typename     string `json:"__typename"`
	Name         string `json:"name"`
	WorkflowName string `json:"workflowName"`
	Status       string `json:"status"`
	Conclusion   string `json:"conclusion"`
	DetailsURL   string `json:"detailsUrl"`
	Context      string `json:"context"`
	State        string `json:"state"`
	TargetURL    string `json:"targetUrl"`
}

func (r rollup) check() Check {
	if r.Typename == "StatusContext" {
		b := BucketPending
		switch r.State {
		case "SUCCESS":
			b = BucketPass
		case "FAILURE", "ERROR":
			b = BucketFail
		}
		return Check{Name: r.Context, State: r.State, Bucket: b, Link: r.TargetURL}
	}
	b := BucketPending
	if r.Status == "COMPLETED" {
		switch r.Conclusion {
		case "SUCCESS":
			b = BucketPass
		case "NEUTRAL", "SKIPPED", "STALE":
			b = BucketSkipping
		case "CANCELLED":
			b = BucketCancel
		default: // FAILURE, TIMED_OUT, ACTION_REQUIRED, STARTUP_FAILURE
			b = BucketFail
		}
	}
	state := r.Conclusion
	if state == "" {
		state = r.Status
	}
	return Check{Name: r.Name, Workflow: r.WorkflowName, State: state, Bucket: b, Link: r.DetailsURL}
}

func (p *PR) readRollup() {
	p.Checks = p.Checks[:0]
	for _, r := range p.Rollup {
		p.Checks = append(p.Checks, r.check())
	}
}

const prFields = "number,title,state,url,isDraft,reviewDecision,mergeable,baseRefName,headRefName,author,updatedAt,statusCheckRollup"

// PRForBranch is the pull request for branch ("" = the current branch), or
// nil when it has none.
func (c *Client) PRForBranch(ctx context.Context, branch string) (*PR, error) {
	if !c.Available(ctx) {
		return nil, ErrNoGH
	}
	args := []string{"pr", "view"}
	if branch != "" {
		args = append(args, branch)
	}
	args = append(args, "--json", prFields)
	out, err := c.gh(ctx, localTimeout, "", args...)
	if err != nil {
		if strings.Contains(strings.ToLower(stderrOf(err)), "no pull requests found") {
			return nil, nil
		}
		return nil, err
	}
	return parsePR(out)
}

// PRView is pull request n.
func (c *Client) PRView(ctx context.Context, n int) (*PR, error) {
	if !c.Available(ctx) {
		return nil, ErrNoGH
	}
	out, err := c.gh(ctx, localTimeout, "", "pr", "view", strconv.Itoa(n), "--json", prFields)
	if err != nil {
		return nil, err
	}
	return parsePR(out)
}

func parsePR(out string) (*PR, error) {
	var p PR
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		return nil, fmt.Errorf("gh pr view: %w", err)
	}
	p.readRollup()
	return &p, nil
}

// PRList is the repository's open pull requests, the most recently updated
// first (at most 30).
func (c *Client) PRList(ctx context.Context) ([]PR, error) {
	if !c.Available(ctx) {
		return nil, ErrNoGH
	}
	out, err := c.gh(ctx, localTimeout, "", "pr", "list", "--json",
		"number,title,headRefName,author,isDraft,updatedAt,url,statusCheckRollup", "--limit", "30")
	if err != nil {
		return nil, err
	}
	return parsePRList(out)
}

func parsePRList(out string) ([]PR, error) {
	var ps []PR
	if err := json.Unmarshal([]byte(out), &ps); err != nil {
		return nil, fmt.Errorf("gh pr list: %w", err)
	}
	for i := range ps {
		ps[i].readRollup()
	}
	return ps, nil
}

// Checks lists pull request n's checks with `gh pr checks`. gh exits 8 while
// some are still pending; that is a normal answer here, not a failure.
func (c *Client) Checks(ctx context.Context, n int) ([]Check, error) {
	if !c.Available(ctx) {
		return nil, ErrNoGH
	}
	out, err := c.gh(ctx, localTimeout, "", "pr", "checks", strconv.Itoa(n), "--json", "name,state,bucket,link,workflow")
	if err != nil && !(exitCode(err) == 8 && strings.TrimSpace(out) != "") {
		if strings.Contains(strings.ToLower(stderrOf(err)), "no checks reported") {
			return nil, nil
		}
		return nil, err
	}
	var cs []Check
	if err := json.Unmarshal([]byte(out), &cs); err != nil {
		return nil, fmt.Errorf("gh pr checks: %w", err)
	}
	return cs, nil
}

// prURL finds a pull request link in gh's output.
var prURL = regexp.MustCompile(`https://\S+/pull/(\d+)`)

// CreatePR opens a pull request from the current branch into base. The body
// goes in on stdin, so it is never mangled by argument quoting.
func (c *Client) CreatePR(ctx context.Context, title, body, base string, draft bool) (int, string, error) {
	if !c.Available(ctx) {
		return 0, "", ErrNoGH
	}
	if strings.TrimSpace(title) == "" {
		return 0, "", errors.New("gh pr create: a title is needed")
	}
	args := []string{"pr", "create", "--title", title}
	if base != "" {
		args = append(args, "--base", base)
	}
	if draft {
		args = append(args, "--draft")
	}
	args = append(args, "--body-file", "-")
	out, err := c.gh(ctx, networkTimeout, body, args...)
	if err != nil {
		return 0, "", err
	}
	n, url := parseCreated(out)
	if n == 0 {
		return 0, "", fmt.Errorf("gh pr create: no pull request link in %q", firstLine(out))
	}
	return n, url, nil
}

// parseCreated finds the new pull request's number and link in gh's output.
func parseCreated(out string) (int, string) {
	ms := prURL.FindAllStringSubmatch(out, -1)
	if len(ms) == 0 {
		return 0, ""
	}
	m := ms[len(ms)-1]
	n, _ := strconv.Atoi(m[1])
	return n, m[0]
}

// MergeMethods are the ways [Client.Merge] can merge, the default first.
var MergeMethods = []string{"squash", "merge", "rebase"}

// Merge merges pull request n by method: "squash", "merge" or "rebase".
func (c *Client) Merge(ctx context.Context, n int, method string) error {
	if !c.Available(ctx) {
		return ErrNoGH
	}
	switch method {
	case "squash", "merge", "rebase":
	default:
		return fmt.Errorf("gh pr merge: unknown method %q", method)
	}
	_, err := c.gh(ctx, networkTimeout, "", "pr", "merge", strconv.Itoa(n), "--"+method)
	return err
}

// logTail is how many lines of a failed log are kept.
const logTail = 150

// FailedLog is the tail of the failed steps' log of a workflow run, or of
// one job of it when jobID is set.
func (c *Client) FailedLog(ctx context.Context, runID, jobID string) (string, error) {
	if !c.Available(ctx) {
		return "", ErrNoGH
	}
	args := []string{"run", "view"}
	if jobID != "" {
		args = append(args, "--job", jobID)
	} else {
		args = append(args, runID)
	}
	args = append(args, "--log-failed")
	out, err := c.gh(ctx, networkTimeout, "", args...)
	if err != nil {
		return "", err
	}
	return tidyLog(out, logTail), nil
}

// ansiSeq matches terminal escape sequences left in CI logs.
var ansiSeq = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")

// logStamp matches the timestamp Actions puts on each log line.
var logStamp = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?Z ?`)

// tidyLog drops the "job<TAB>step<TAB>" prefix, the timestamp and escape
// sequences from each line of a `gh run view --log` output and keeps the
// last n lines.
func tidyLog(out string, n int) string {
	ls := lines(out)
	if len(ls) > n {
		ls = ls[len(ls)-n:]
	}
	for i, l := range ls {
		if parts := strings.SplitN(l, "\t", 3); len(parts) == 3 {
			l = parts[2]
		}
		l = logStamp.ReplaceAllString(l, "")
		l = ansiSeq.ReplaceAllString(l, "")
		ls[i] = strings.TrimPrefix(l, "\ufeff")
	}
	return strings.Join(ls, "\n")
}

// RerunFailed re-runs the failed jobs of a workflow run.
func (c *Client) RerunFailed(ctx context.Context, runID string) error {
	if !c.Available(ctx) {
		return ErrNoGH
	}
	_, err := c.gh(ctx, localTimeout, "", "run", "rerun", runID, "--failed")
	return err
}

// OpenWeb opens pull request n in the browser.
func (c *Client) OpenWeb(ctx context.Context, n int) error {
	if !c.Available(ctx) {
		return ErrNoGH
	}
	_, err := c.gh(ctx, localTimeout, "", "pr", "view", strconv.Itoa(n), "--web")
	return err
}

// CheckoutPR checks out pull request n's branch.
func (c *Client) CheckoutPR(ctx context.Context, n int) error {
	if !c.Available(ctx) {
		return ErrNoGH
	}
	_, err := c.gh(ctx, networkTimeout, "", "pr", "checkout", strconv.Itoa(n))
	return err
}
