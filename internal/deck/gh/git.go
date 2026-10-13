package gh

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
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
	Head     string // HEAD's commit id; "(initial)" before the first commit
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
			case "branch.oid":
				s.Head = v
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

// ChangedFile is one changed path in the work tree, as `git status` lists
// it.
type ChangedFile struct {
	// Code is one letter: M modified, A added, D deleted, R renamed, C
	// copied, T type changed, U unmerged, ? untracked.
	Code string
	Path string
	// Orig is a rename's or copy's old path.
	Orig string
	// Staged is set when the change (or part of it) is in the index.
	Staged bool
}

// ChangedFiles lists the work tree's changed, staged, unmerged and
// untracked paths.
func (c *Client) ChangedFiles(ctx context.Context) ([]ChangedFile, error) {
	_, files, err := c.StatusFiles(ctx)
	return files, err
}

// StatusFiles is [Client.Status] and [Client.ChangedFiles] from one read.
func (c *Client) StatusFiles(ctx context.Context) (Status, []ChangedFile, error) {
	out, err := c.git(ctx, localTimeout, "status", "--porcelain=v2", "--branch")
	if err != nil {
		return Status{}, nil, err
	}
	return parseStatus(out), parseChangedFiles(out), nil
}

// parseChangedFiles reads the path lines of porcelain v2 output:
//
//	1 XY sub mH mI mW hH hI path
//	2 XY sub mH mI mW hH hI Xscore path<TAB>orig
//	u XY sub m1 m2 m3 mW h1 h2 h3 path
//	? path
func parseChangedFiles(out string) []ChangedFile {
	var fs []ChangedFile
	for _, l := range lines(out) {
		if len(l) < 3 || l[1] != ' ' {
			continue
		}
		switch l[0] {
		case '?':
			fs = append(fs, ChangedFile{Code: "?", Path: l[2:]})
		case '1':
			if f := strings.SplitN(l, " ", 9); len(f) == 9 {
				fs = append(fs, changedFile(f[1], f[8], ""))
			}
		case '2':
			if f := strings.SplitN(l, " ", 10); len(f) == 10 {
				path, orig, _ := strings.Cut(f[9], "\t")
				fs = append(fs, changedFile(f[1], path, orig))
			}
		case 'u':
			if f := strings.SplitN(l, " ", 11); len(f) == 11 {
				fs = append(fs, ChangedFile{Code: "U", Path: f[10]})
			}
		}
	}
	return fs
}

// changedFile is a tracked path's change from its XY status: the index
// side when it changed there, else the work tree side.
func changedFile(xy, path, orig string) ChangedFile {
	f := ChangedFile{Path: path, Orig: orig, Code: "M"}
	if len(xy) < 2 {
		return f
	}
	if xy[0] != '.' {
		f.Code, f.Staged = string(xy[0]), true
	} else if xy[1] != '.' {
		f.Code = string(xy[1])
	}
	return f
}

// Commit is one commit as the recent commits list shows it.
type Commit struct {
	Hash    string // abbreviated
	When    time.Time
	Author  string
	Subject string

	// ID is the full hash and Parents the full hashes of its parents,
	// first parent first; set by [Client.Graph].
	ID      string
	Parents []string
	// Refs are the branches, tags and HEAD pointing at it; set by
	// [Client.Graph].
	Refs []Ref
}

// RefKind is what kind of name a [Ref] is.
type RefKind int

const (
	RefBranch RefKind = iota // a local branch
	RefRemote                // a remote-tracking branch, "origin/main"
	RefTag                   // a tag
	RefHead                  // a detached HEAD
)

// Ref is a name pointing at a commit.
type Ref struct {
	Name string
	Kind RefKind
	// Head is set on the branch HEAD points at.
	Head bool
}

// Log lists up to n commits of HEAD, the newest first. A repository with no
// commits yet has none.
func (c *Client) Log(ctx context.Context, n int) ([]Commit, error) {
	out, err := c.git(ctx, localTimeout, "log", "-n", strconv.Itoa(n), "--format=%h%x00%ct%x00%an%x00%s", "--")
	if err != nil {
		if strings.Contains(stderrOf(err), "does not have any commits") {
			return nil, nil
		}
		return nil, err
	}
	return parseLog(out), nil
}

func parseLog(out string) []Commit {
	var cs []Commit
	for _, l := range lines(out) {
		f := strings.SplitN(l, "\x00", 4)
		if len(f) < 4 || f[0] == "" {
			continue
		}
		cs = append(cs, Commit{Hash: f[0], When: unixTime(f[1]), Author: f[2], Subject: f[3]})
	}
	return cs
}

// graphFormat is one commit per line, its fields split by \x1f: the
// abbreviated and full hash, the parents' full hashes, the decorations, the
// author, the commit time and the subject.
const graphFormat = "--format=%h%x1f%H%x1f%P%x1f%D%x1f%an%x1f%ct%x1f%s"

// Graph lists up to n commits of HEAD and the local branches in
// topological order (children before parents), with their parents and the
// names pointing at them, for drawing a commit graph. A repository with no
// commits yet has none.
func (c *Client) Graph(ctx context.Context, n int) ([]Commit, error) {
	out, err := c.git(ctx, localTimeout, "log", "--topo-order", "--decorate=full", "--no-show-signature",
		"-n", strconv.Itoa(n), graphFormat, "HEAD", "--branches", "--")
	if err != nil {
		s := stderrOf(err)
		if strings.Contains(s, "does not have any commits") || strings.Contains(s, "unknown revision") {
			return nil, nil
		}
		return nil, err
	}
	return parseGraph(out), nil
}

func parseGraph(out string) []Commit {
	var cs []Commit
	for _, l := range lines(out) {
		f := strings.SplitN(l, "\x1f", 7)
		if len(f) < 7 || f[0] == "" || f[1] == "" {
			continue
		}
		cs = append(cs, Commit{
			Hash:    f[0],
			ID:      f[1],
			Parents: strings.Fields(f[2]),
			Refs:    parseRefs(f[3]),
			Author:  f[4],
			When:    unixTime(f[5]),
			Subject: f[6],
		})
	}
	return cs
}

// parseRefs reads %D made with --decorate=full: "HEAD -> refs/heads/main,
// refs/remotes/origin/main, tag: refs/tags/v1". Names it does not know
// (the stash, notes, a remote's HEAD) are left out.
func parseRefs(d string) []Ref {
	var refs []Ref
	for _, item := range strings.Split(d, ", ") {
		item = strings.TrimSpace(item)
		head := false
		if rest, ok := strings.CutPrefix(item, "HEAD -> "); ok {
			item, head = rest, true
		}
		item = strings.TrimPrefix(item, "tag: ")
		switch {
		case item == "HEAD":
			refs = append(refs, Ref{Name: "HEAD", Kind: RefHead})
		case strings.HasPrefix(item, "refs/heads/"):
			refs = append(refs, Ref{Name: strings.TrimPrefix(item, "refs/heads/"), Kind: RefBranch, Head: head})
		case strings.HasPrefix(item, "refs/remotes/"):
			name := strings.TrimPrefix(item, "refs/remotes/")
			if strings.HasSuffix(name, "/HEAD") {
				continue
			}
			refs = append(refs, Ref{Name: name, Kind: RefRemote})
		case strings.HasPrefix(item, "refs/tags/"):
			refs = append(refs, Ref{Name: strings.TrimPrefix(item, "refs/tags/"), Kind: RefTag})
		}
	}
	return refs
}

func unixTime(s string) time.Time {
	sec, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

// FileStat is one file a commit touched.
type FileStat struct {
	Path           string
	Added, Deleted int
	Binary         bool
}

// CommitDetail is one commit with its message body and the files it
// changed.
type CommitDetail struct {
	Commit
	Body           string
	Files          []FileStat
	Added, Deleted int
}

// hashRe is what a commit hash (or a prefix of one) looks like.
var hashRe = regexp.MustCompile(`^[0-9a-fA-F]{4,64}$`)

// Show reads commit hash: its message and the files it changed, by line
// counts. A merge commit lists no files.
func (c *Client) Show(ctx context.Context, hash string) (*CommitDetail, error) {
	if !hashRe.MatchString(hash) {
		return nil, errors.New("git show: bad commit hash")
	}
	out, err := c.git(ctx, localTimeout, "show", "--numstat", "--format=%h%x00%ct%x00%an%x00%s%x00%b%x01", hash, "--")
	if err != nil {
		return nil, err
	}
	return parseShow(out)
}

// parseShow reads `git show --numstat` output made with a header that ends
// in \x01: the header's fields, then one "added<TAB>deleted<TAB>path" line
// per file ("-" counts for a binary file).
func parseShow(out string) (*CommitDetail, error) {
	head, rest, ok := strings.Cut(out, "\x01")
	if !ok {
		return nil, errors.New("git show: unexpected output")
	}
	f := strings.SplitN(head, "\x00", 5)
	if len(f) < 5 {
		return nil, errors.New("git show: unexpected output")
	}
	d := &CommitDetail{
		Commit: Commit{Hash: strings.TrimSpace(f[0]), When: unixTime(f[1]), Author: f[2], Subject: f[3]},
		Body:   strings.TrimSpace(f[4]),
	}
	for _, l := range lines(rest) {
		p := strings.SplitN(l, "\t", 3)
		if len(p) < 3 {
			continue
		}
		fs := FileStat{Path: p[2]}
		if p[0] == "-" && p[1] == "-" {
			fs.Binary = true
		} else {
			fs.Added, _ = strconv.Atoi(p[0])
			fs.Deleted, _ = strconv.Atoi(p[1])
		}
		d.Added += fs.Added
		d.Deleted += fs.Deleted
		d.Files = append(d.Files, fs)
	}
	return d, nil
}
