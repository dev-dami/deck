package gh

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Status is the work tree's branch and how it stands against its upstream.
type Status struct {
	Branch   string // empty when HEAD is detached
	Upstream string // "origin/feature"; empty when the branch tracks nothing
	Ahead    int
	Behind   int
	Dirty    int // changed, staged, unmerged and untracked files
	Detached bool
}

// Status reads `git status --porcelain=v2 --branch`.
func (c *Client) Status(ctx context.Context) (Status, error) {
	out, err := c.git(ctx, localTimeout, "status", "--porcelain=v2", "--branch")
	if err != nil {
		return Status{}, err
	}
	return parseStatus(out), nil
}

// parseStatus reads porcelain v2 output: "# branch.*" headers, then one line
// per changed path.
func parseStatus(out string) Status {
	var s Status
	for _, l := range lines(out) {
		if rest, ok := strings.CutPrefix(l, "# "); ok {
			k, v, _ := strings.Cut(rest, " ")
			switch k {
			case "branch.head":
				if v == "(detached)" {
					s.Detached = true
				} else {
					s.Branch = v
				}
			case "branch.upstream":
				s.Upstream = v
			case "branch.ab":
				for _, f := range strings.Fields(v) {
					n, _ := strconv.Atoi(f[1:])
					switch f[0] {
					case '+':
						s.Ahead = n
					case '-':
						s.Behind = n
					}
				}
			}
			continue
		}
		if l == "" || strings.HasPrefix(l, "!") {
			continue
		}
		s.Dirty++
	}
	return s
}

// Branch is a local branch as the recent-branches list shows it.
type Branch struct {
	Name      string
	Upstream  string    // empty when it tracks nothing (never pushed)
	Committed time.Time // its last commit
	Subject   string    // its last commit's subject
}

// RecentBranches lists up to n local branches, the most recently committed
// to first.
func (c *Client) RecentBranches(ctx context.Context, n int) ([]Branch, error) {
	out, err := c.git(ctx, localTimeout, "for-each-ref", "--sort=-committerdate",
		"--count="+strconv.Itoa(n), "refs/heads",
		"--format=%(refname:short)%00%(upstream:short)%00%(committerdate:unix)%00%(subject)")
	if err != nil {
		return nil, err
	}
	return parseBranches(out), nil
}

func parseBranches(out string) []Branch {
	var bs []Branch
	for _, l := range lines(out) {
		f := strings.SplitN(l, "\x00", 4)
		if len(f) < 4 || f[0] == "" {
			continue
		}
		b := Branch{Name: f[0], Upstream: f[1], Subject: f[3]}
		if sec, err := strconv.ParseInt(f[2], 10, 64); err == nil {
			b.Committed = time.Unix(sec, 0)
		}
		bs = append(bs, b)
	}
	return bs
}

// Fetch updates the remote-tracking branches.
func (c *Client) Fetch(ctx context.Context) error {
	_, err := c.git(ctx, networkTimeout, "fetch", "--prune")
	return err
}

// Checkout switches to a local branch.
func (c *Client) Checkout(ctx context.Context, branch string) error {
	if branch == "" || strings.HasPrefix(branch, "-") {
		return errors.New("git checkout: bad branch name")
	}
	_, err := c.git(ctx, localTimeout, "checkout", branch, "--")
	return err
}

// CommitAll stages everything (`git add -A`) and commits it with msg.
func (c *Client) CommitAll(ctx context.Context, msg string) error {
	if strings.TrimSpace(msg) == "" {
		return errors.New("git commit: empty message")
	}
	if _, err := c.git(ctx, localTimeout, "add", "-A"); err != nil {
		return err
	}
	_, err := c.git(ctx, localTimeout, "commit", "-m", msg)
	return err
}

// Push pushes the current branch, setting origin as its upstream when it
// has none.
func (c *Client) Push(ctx context.Context) error {
	st, err := c.Status(ctx)
	if err != nil {
		return err
	}
	if st.Branch == "" {
		return errors.New("git push: HEAD is detached")
	}
	if st.Upstream == "" {
		_, err = c.git(ctx, networkTimeout, "push", "-u", "origin", st.Branch)
	} else {
		_, err = c.git(ctx, networkTimeout, "push")
	}
	return err
}

// DefaultBranch is the repository's default branch: GitHub's when gh can
// say, else what origin/HEAD points at, else "main".
func (c *Client) DefaultBranch(ctx context.Context) string {
	if c.Available(ctx) {
		if out, err := c.gh(ctx, localTimeout, "", "repo", "view", "--json", "defaultBranchRef"); err == nil {
			var v struct {
				DefaultBranchRef struct {
					Name string `json:"name"`
				} `json:"defaultBranchRef"`
			}
			if json.Unmarshal([]byte(out), &v) == nil && v.DefaultBranchRef.Name != "" {
				return v.DefaultBranchRef.Name
			}
		}
	}
	if out, err := c.git(ctx, localTimeout, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		ref := strings.TrimSpace(out)
		if _, name, ok := strings.Cut(ref, "/"); ok && name != "" {
			return name
		}
	}
	return "main"
}

// LastSubject is the subject of HEAD's commit.
func (c *Client) LastSubject(ctx context.Context) (string, error) {
	out, err := c.git(ctx, localTimeout, "log", "-1", "--format=%s")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// LogSince lists the subjects of the commits on HEAD that base does not
// have, newest first. base is tried as origin/<base> first, since the local
// branch may be stale.
func (c *Client) LogSince(ctx context.Context, base string) ([]string, error) {
	var lastErr error
	for _, ref := range []string{"origin/" + base, base} {
		out, err := c.git(ctx, localTimeout, "log", "--format=%s", ref+"..HEAD", "--")
		if err != nil {
			lastErr = err
			continue
		}
		return lines(out), nil
	}
	return nil, lastErr
}
