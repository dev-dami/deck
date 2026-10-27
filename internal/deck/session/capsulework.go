package session

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/changes"
	"github.com/dev-dami/deck/internal/deck/store"
	"github.com/dev-dami/deck/internal/deck/sub"
)

// Named capsules: big work as a unit of change. A named capsule keeps
// versions, each a SUB capsule; one comes from a whole worktree branch,
// a range of commits, or the uncommitted diff of a tree (sub.patch.capture
// stays the quick path), with the commits it spans, its authors, notes,
// test status and review comments. It lands in the shared tree or a
// worktree with a three-way merge, is rebased or stacked into a new
// version, reverted, and handed to an agent like any capsule. Files are
// read from git one blob at a time and listed a page at a time, so a
// capsule of thousands of files stays cheap.

// Capsule sources.
const (
	FromWorktree = "worktree"
	FromCommits  = "commits"
	FromDiff     = "diff"
)

// maxVersionCommits bounds the commits a version lists.
const maxVersionCommits = 50

func cloneCapsules(cs []store.CapsuleRec) []store.CapsuleRec {
	out := slices.Clone(cs)
	for i := range out {
		out[i].Versions = slices.Clone(out[i].Versions)
		out[i].Comments = slices.Clone(out[i].Comments)
		out[i].Landings = slices.Clone(out[i].Landings)
		out[i].Authors = slices.Clone(out[i].Authors)
	}
	return out
}

// gitCapsule stores what turns tree-ish from into tree-ish to in repository
// r as a capsule, its files read straight from git's objects.
func (m *Manager) gitCapsule(ctx context.Context, r *changes.Repo, from, to string, meta sub.Meta) (sub.ID, int, error) {
	s := m.subStore()
	if s == nil {
		return "", 0, errors.New("the Source Update Bridge is not available")
	}
	chs, err := r.Changed(ctx, from, to)
	if err != nil {
		return "", 0, err
	}
	var blobs []string
	skipped := 0
	for _, c := range chs {
		if c.Special {
			skipped++
			continue
		}
		for _, b := range []string{c.OldBlob, c.NewBlob} {
			if b != "" {
				blobs = append(blobs, b)
			}
		}
	}
	files := make(map[string]sub.ID, len(blobs))
	err = r.Blobs(ctx, blobs, func(id string, b []byte) error {
		if _, ok := files[id]; ok {
			return nil
		}
		fid, err := s.PutFile(b)
		files[id] = fid
		return err
	})
	if err != nil {
		return "", 0, err
	}
	ops := make([]sub.Op, 0, len(chs))
	for _, c := range chs {
		if c.Special {
			continue
		}
		ops = append(ops, sub.Op{Path: c.Path, Before: files[c.OldBlob], After: files[c.NewBlob], Exec: c.NewExec, BeforeExec: c.OldExec})
	}
	id, err := s.Build(ops, "", meta)
	return id, skipped, err
}

// version describes capsule id as a version of a named capsule.
func (m *Manager) version(id sub.ID, by, note string) store.CapsuleVersion {
	v := store.CapsuleVersion{ID: string(id), By: by, Note: note, Created: time.Now()}
	if ops, err := m.subStore().Ops(id); err == nil {
		v.Files = len(ops)
		for _, o := range ops {
			switch o.Kind() {
			case "add":
				v.Add++
			case "delete":
				v.Del++
			default:
				v.Mod++
			}
		}
	}
	return v
}

// withCommits records the commits fork..head of r on v.
func withCommits(ctx context.Context, r *changes.Repo, v *store.CapsuleVersion, fork, head string) {
	v.Fork, v.Head = fork, head
	cs, err := r.Commits(ctx, fork, head)
	if err != nil {
		return
	}
	v.CommitsTotal = len(cs)
	for _, c := range cs[:min(len(cs), maxVersionCommits)] {
		v.Commits = append(v.Commits, store.CommitRef{SHA: c.SHA, Subject: c.Subject, Author: c.Author})
	}
}

// addVersion adds v to the named capsule name (made when there is none),
// unless it is the same capsule as the latest version; it returns the
// capsule's name and the version's number.
func (m *Manager) addVersion(t *thread, name, source, worktree string, v store.CapsuleVersion) (string, int) {
	t.mu.Lock()
	caps := cloneCapsules(t.capsules)
	i := slices.IndexFunc(caps, func(c store.CapsuleRec) bool { return c.Name == name })
	if i < 0 {
		caps = append(caps, store.CapsuleRec{Name: name, Source: source, Worktree: worktree, Created: v.Created})
		i = len(caps) - 1
	}
	c := &caps[i]
	n := len(c.Versions)
	if n == 0 || c.Versions[n-1].ID != v.ID {
		c.Versions = append(c.Versions, v)
		n++
	}
	if v.By != "" && !slices.Contains(c.Authors, v.By) {
		c.Authors = append(c.Authors, v.By)
	}
	if v.Note != "" && c.Note == "" {
		c.Note = v.Note
	}
	c.Updated = time.Now()
	t.capsules = caps
	t.mu.Unlock()
	m.persistMeta(t)
	return name, n
}

// captureTree makes the worktree's changes since its fork, committed or
// not, the next version of the capsule named after it.
func (m *Manager) captureTree(ctx context.Context, t *thread, wt *store.Worktree, by, note string) (string, int, error) {
	r, err := changes.Open(ctx, wt.Path)
	if err != nil {
		return "", 0, fmt.Errorf("session: the worktree is gone from %s", wt.Path)
	}
	fork := wt.Fork
	if fork == "" {
		fork = wt.Base
	}
	tree, err := r.Snapshot(ctx)
	if err != nil {
		return "", 0, err
	}
	name := treeName(wt)
	id, _, err := m.gitCapsule(ctx, r, fork, tree, sub.Meta{Source: by, Task: wt.Task, Note: or(note, "worktree "+name)})
	if err != nil {
		return "", 0, err
	}
	if id == "" {
		return "", 0, fmt.Errorf("session: worktree %s changed nothing since it forked", name)
	}
	v := m.version(id, by, note)
	if head, err := r.Commit(ctx, "HEAD"); err == nil {
		withCommits(ctx, r, &v, fork, head)
	}
	n, ver := m.addVersion(t, name, FromWorktree, wt.Branch, v)
	return n, ver, nil
}

// CapsuleSpec says what a named capsule is made from.
type CapsuleSpec struct {
	Name   string
	Source string // FromWorktree, FromCommits or FromDiff
	// Worktree names the worktree for FromWorktree. Range is the commits
	// for FromCommits: "a..b", or one commit. Root is the tree whose
	// repository they are read from (and whose diff FromDiff takes); ""
	// is the shared tree.
	Worktree, Range, Root string
	Note                  string
	By                    string // member id, "" for the user
}

// CreateCapsule makes (or adds a version to) a named capsule, and returns
// its name and the version's number.
func (m *Manager) CreateCapsule(threadID string, sp CapsuleSpec) (string, int, error) {
	t := m.thread(threadID)
	if t == nil {
		return "", 0, fmt.Errorf("session: no thread %q", threadID)
	}
	ctx, cancel := context.WithTimeout(m.ctx, gitTimeout)
	defer cancel()
	by := m.byName(t, sp.By)
	if sp.Source == FromWorktree {
		l, err := t.findLine(sp.Worktree)
		if err != nil {
			return "", 0, err
		}
		name, ver, err := m.captureTree(ctx, t, l.wt, by, sp.Note)
		if err == nil && sp.Name != "" && sp.Name != name {
			m.renameCapsule(t, name, sp.Name)
			name = sp.Name
		}
		return name, ver, err
	}
	root := sp.Root
	if root == "" {
		root = t.mainDir()
	}
	r, err := changes.Open(ctx, root)
	if err != nil {
		return "", 0, errors.New("session: a capsule from git needs a git repository")
	}
	var from, to string
	switch sp.Source {
	case FromCommits:
		rng := strings.TrimSpace(sp.Range)
		if rng == "" {
			return "", 0, errors.New("session: say which commits: \"a..b\", or one commit")
		}
		a, b, ok := strings.Cut(rng, "..")
		if !ok {
			a, b = rng+"^", rng
		}
		if from, err = r.Commit(ctx, a); err != nil {
			return "", 0, fmt.Errorf("session: no commit %q", a)
		}
		if to, err = r.Commit(ctx, or(b, "HEAD")); err != nil {
			return "", 0, fmt.Errorf("session: no commit %q", b)
		}
	case FromDiff, "":
		sp.Source = FromDiff
		if from, err = r.Commit(ctx, "HEAD"); err != nil {
			return "", 0, err
		}
		if to, err = r.Snapshot(ctx); err != nil {
			return "", 0, err
		}
	default:
		return "", 0, fmt.Errorf("session: a capsule comes from worktree, commits or diff, not %q", sp.Source)
	}
	name := slug(sp.Name)
	if name == "" {
		name = fmt.Sprintf("%s-%d", sp.Source, len(t.capsuleList())+1)
	}
	id, _, err := m.gitCapsule(ctx, r, from, to, sub.Meta{Source: by, Note: or(sp.Note, name)})
	if err != nil {
		return "", 0, err
	}
	if id == "" {
		return "", 0, errors.New("session: nothing changed there")
	}
	v := m.version(id, by, sp.Note)
	if sp.Source == FromCommits {
		withCommits(ctx, r, &v, from, to)
	} else {
		v.Fork = from
	}
	name, ver := m.addVersion(t, name, sp.Source, "", v)
	m.notice(t, fmt.Sprintf("capsule %s v%d: %d files from %s", name, ver, v.Files, sp.Source))
	return name, ver, nil
}

func (t *thread) capsuleList() []store.CapsuleRec {
	t.mu.Lock()
	defer t.mu.Unlock()
	return cloneCapsules(t.capsules)
}

func (m *Manager) renameCapsule(t *thread, from, to string) {
	t.mu.Lock()
	caps := cloneCapsules(t.capsules)
	if i := slices.IndexFunc(caps, func(c store.CapsuleRec) bool { return c.Name == from }); i >= 0 &&
		!slices.ContainsFunc(caps, func(c store.CapsuleRec) bool { return c.Name == to }) {
		caps[i].Name = to
		t.capsules = caps
	}
	t.mu.Unlock()
	m.persistMeta(t)
}

// findCapsule resolves ref: a name ("name" for its latest version,
// "name@2" for one), or a capsule id that is a version of one. version
// counts from 1.
func (t *thread) findCapsule(ref string) (store.CapsuleRec, int, error) {
	ref = strings.TrimSpace(ref)
	name, at, _ := strings.Cut(ref, "@")
	for _, c := range t.capsuleList() {
		if c.Name == name && len(c.Versions) > 0 {
			v := len(c.Versions)
			if at != "" {
				n, err := strconv.Atoi(strings.TrimPrefix(at, "v"))
				if err != nil || n < 1 || n > v {
					return store.CapsuleRec{}, 0, fmt.Errorf("capsule %s has versions 1 to %d", name, v)
				}
				v = n
			}
			return c, v, nil
		}
		for i, v := range c.Versions {
			if v.ID == ref {
				return c, i + 1, nil
			}
		}
	}
	return store.CapsuleRec{}, 0, fmt.Errorf("no capsule %q in this thread (capsule.list names them)", ref)
}

// targetRoot is where into ("" or "main" for the shared tree, else a
// worktree's name) is on disk.
func (m *Manager) targetRoot(ctx context.Context, t *thread, into string) (string, error) {
	if into == "" || into == "main" {
		r, err := changes.Open(ctx, t.mainDir())
		if err != nil {
			return t.mainDir(), nil
		}
		return r.Root, nil
	}
	l, err := t.findLine(into)
	if err != nil {
		return "", err
	}
	return l.wt.Path, nil
}

// land merges version ver of capsule rec into into, or with check only
// reports how it would.
func (m *Manager) land(ctx context.Context, t *thread, rec string, ver int, into string, check bool, strategy sub.Strategy, by string) (LandResult, error) {
	c, _, err := t.findCapsule(rec)
	if err != nil {
		return LandResult{}, err
	}
	id := sub.ID(c.Versions[ver-1].ID)
	res := LandResult{Capsule: c.Name, Version: ver, ID: string(id), Into: into, Checked: check}
	root, err := m.targetRoot(ctx, t, into)
	if err != nil {
		return res, err
	}
	s := m.subStore()
	if check {
		res.Files, err = s.PreviewMerge(ctx, root, id)
		return res, err
	}
	if into == "" && by != "" {
		// Into the shared tree: not over another member's exclusive lease.
		if a := m.memberOf(t, by); a != nil {
			ops, _ := s.Ops(id)
			paths := make([]string, len(ops))
			for i, o := range ops {
				paths[i] = o.Path
			}
			t.mu.Lock()
			err := t.leaseBlock(a, paths)
			t.mu.Unlock()
			if err != nil {
				return res, err
			}
		}
	}
	res.Files, err = s.MergeApply(ctx, root, id, strategy)
	if err != nil {
		return res, err
	}
	m.recordLanding(t, c.Name, store.CapsuleLanding{Version: ver, Into: or(into, "main"), At: time.Now(), By: m.byName(t, by)})
	m.notice(t, fmt.Sprintf("landed capsule %s v%d (%d files) in %s", c.Name, ver, len(res.Files), or(into, "the shared tree")))
	return res, nil
}

func (m *Manager) memberOf(t *thread, id string) *agentState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.member(id)
}

func (m *Manager) recordLanding(t *thread, name string, l store.CapsuleLanding) {
	m.editCapsule(t, name, func(c *store.CapsuleRec) { c.Landings = append(c.Landings, l) })
}

// editCapsule changes the named capsule name with fn.
func (m *Manager) editCapsule(t *thread, name string, fn func(c *store.CapsuleRec)) bool {
	t.mu.Lock()
	caps := cloneCapsules(t.capsules)
	i := slices.IndexFunc(caps, func(c store.CapsuleRec) bool { return c.Name == name })
	if i >= 0 {
		fn(&caps[i])
		caps[i].Updated = time.Now()
		t.capsules = caps
	}
	t.mu.Unlock()
	if i >= 0 {
		m.persistMeta(t)
	}
	return i >= 0
}

// LandCapsule merges a named capsule (ref: "name", "name@2" or a version's
// id) into the shared tree or the worktree into, three-way; check only
// reports how each file would land. by is the member asking, "" for the
// user.
func (m *Manager) LandCapsule(threadID, ref, into string, check bool, strategy sub.Strategy, by string) (LandResult, error) {
	t := m.thread(threadID)
	if t == nil {
		return LandResult{}, fmt.Errorf("session: no thread %q", threadID)
	}
	c, ver, err := t.findCapsule(ref)
	if err != nil {
		return LandResult{}, err
	}
	ctx, cancel := context.WithTimeout(m.ctx, gitTimeout)
	defer cancel()
	return m.land(ctx, t, c.Name+"@"+strconv.Itoa(ver), ver, into, check, strategy, by)
}

// RevertCapsule undoes a landed version in the tree it landed in (from:
// "" or "main" for the shared tree, else a worktree), all or nothing.
func (m *Manager) RevertCapsule(threadID, ref, from, by string) error {
	t := m.thread(threadID)
	if t == nil {
		return fmt.Errorf("session: no thread %q", threadID)
	}
	c, ver, err := t.findCapsule(ref)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(m.ctx, gitTimeout)
	defer cancel()
	root, err := m.targetRoot(ctx, t, from)
	if err != nil {
		return err
	}
	if err := m.subStore().Revert(ctx, root, sub.ID(c.Versions[ver-1].ID)); err != nil {
		return err
	}
	m.recordLanding(t, c.Name, store.CapsuleLanding{Version: ver, Into: or(from, "main"), At: time.Now(), By: m.byName(t, by), Reverted: true})
	m.notice(t, fmt.Sprintf("reverted capsule %s v%d from %s", c.Name, ver, or(from, "the shared tree")))
	return nil
}

// RebaseCapsule redoes a named capsule's latest version against a new
// base and keeps the result as its next version: onto is "main" (the
// shared tree as it is now), a worktree's name, or another capsule
// ("capsule:name"), which it is then stacked on. Files both changed are
// merged three-way; any conflict stores nothing and comes back in the
// previews.
func (m *Manager) RebaseCapsule(threadID, ref, onto, by string) (LandResult, error) {
	t := m.thread(threadID)
	if t == nil {
		return LandResult{}, fmt.Errorf("session: no thread %q", threadID)
	}
	c, ver, err := t.findCapsule(ref)
	if err != nil {
		return LandResult{}, err
	}
	ctx, cancel := context.WithTimeout(m.ctx, gitTimeout)
	defer cancel()
	s := m.subStore()
	id := sub.ID(c.Versions[ver-1].ID)
	who := m.byName(t, by)
	meta := sub.Meta{Source: who, Note: c.Name}
	var nid sub.ID
	var prev []sub.Preview
	if under, ok := strings.CutPrefix(onto, "capsule:"); ok {
		uc, uv, err := t.findCapsule(under)
		if err != nil {
			return LandResult{}, err
		}
		nid, prev, err = s.Stack(ctx, id, sub.ID(uc.Versions[uv-1].ID), meta)
		if err != nil {
			return LandResult{Capsule: c.Name, Files: prev, Checked: true}, err
		}
	} else {
		root, err := m.targetRoot(ctx, t, onto)
		if err != nil {
			return LandResult{}, err
		}
		nid, prev, err = s.Rebase(ctx, id, s.TreeOnto(root), "", meta)
		if err != nil {
			return LandResult{Capsule: c.Name, Files: prev, Checked: true}, err
		}
	}
	if nid == "" {
		return LandResult{Capsule: c.Name, Files: prev, Checked: true}, errors.New("session: the new base has all of it already")
	}
	v := m.version(nid, who, "")
	v.Onto = or(onto, "main")
	_, n := m.addVersion(t, c.Name, c.Source, c.Worktree, v)
	m.notice(t, fmt.Sprintf("rebased capsule %s onto %s as v%d", c.Name, v.Onto, n))
	return LandResult{Capsule: c.Name, Version: n, ID: string(nid), Files: prev}, nil
}

// ReviewCapsule adds a review comment (on path, or the whole capsule)
// and/or sets its test status: "pass", "fail" or "none".
func (m *Manager) ReviewCapsule(threadID, ref, by, path, comment, tests, testNote string) error {
	t := m.thread(threadID)
	if t == nil {
		return fmt.Errorf("session: no thread %q", threadID)
	}
	c, _, err := t.findCapsule(ref)
	if err != nil {
		return err
	}
	switch tests {
	case "", "pass", "fail", "none":
	default:
		return fmt.Errorf("session: tests are pass, fail or none, not %q", tests)
	}
	who := or(m.byName(t, by), "you")
	m.editCapsule(t, c.Name, func(c *store.CapsuleRec) {
		if strings.TrimSpace(comment) != "" {
			c.Comments = append(c.Comments, store.CapsuleComment{By: who, Path: path, Text: strings.TrimSpace(comment), At: time.Now()})
		}
		switch tests {
		case "none":
			c.Tests, c.TestNote = "", ""
		case "pass", "fail":
			c.Tests, c.TestNote = tests, strings.TrimSpace(testNote)
		}
	})
	return nil
}

// HandCapsule gives a named capsule's version to agent to (a name or kind),
// as the user's message: an agent with a worktree of its own gets it
// applied there, as with thread.send.
func (m *Manager) HandCapsule(threadID, ref, to, message string) error {
	t := m.thread(threadID)
	if t == nil {
		return fmt.Errorf("session: no thread %q", threadID)
	}
	c, ver, err := t.findCapsule(ref)
	if err != nil {
		return err
	}
	t.mu.Lock()
	a := t.find(nil, to)
	t.mu.Unlock()
	if a == nil {
		return fmt.Errorf("session: no agent %q in this thread", to)
	}
	lines, err := m.handCapsule(t, []*agentState{a}, "", c.Versions[ver-1].ID)
	if err != nil {
		return err
	}
	msg := strings.TrimSpace(message)
	if msg == "" {
		msg = fmt.Sprintf("Here is capsule %s v%d: review it and take it on.", c.Name, ver)
	}
	return m.Post(threadID, "@"+a.getName()+" "+msg+"\n"+lines)
}

// CapsuleView is a named capsule with where it stands.
type CapsuleView struct {
	store.CapsuleRec
	// State is "open", "landed" (its latest landing stands) or "reverted".
	State string
}

// CapsuleViews lists the thread's named capsules, newest first.
func (m *Manager) CapsuleViews(threadID string) []CapsuleView {
	t := m.thread(threadID)
	if t == nil {
		return nil
	}
	caps := t.capsuleList()
	out := make([]CapsuleView, 0, len(caps))
	for i := len(caps) - 1; i >= 0; i-- {
		c := caps[i]
		st := "open"
		if n := len(c.Landings); n > 0 {
			st = "landed"
			if c.Landings[n-1].Reverted {
				st = "reverted"
			}
		}
		out = append(out, CapsuleView{CapsuleRec: c, State: st})
	}
	return out
}

// CapsuleFiles is a page of a capsule version's files, with line counts,
// and how many it has in all.
func (m *Manager) CapsuleFiles(threadID, ref string, offset, limit int) ([]sub.FileStat, int, error) {
	t := m.thread(threadID)
	if t == nil {
		return nil, 0, fmt.Errorf("session: no thread %q", threadID)
	}
	c, ver, err := t.findCapsule(ref)
	if err != nil {
		return nil, 0, err
	}
	return m.subStore().Page(sub.ID(c.Versions[ver-1].ID), offset, limit)
}

// CapsuleFileDiff is one file of a capsule version as a unified diff.
func (m *Manager) CapsuleFileDiff(threadID, ref, path string) (string, error) {
	t := m.thread(threadID)
	if t == nil {
		return "", fmt.Errorf("session: no thread %q", threadID)
	}
	c, ver, err := t.findCapsule(ref)
	if err != nil {
		return "", err
	}
	s := m.subStore()
	ops, err := s.Ops(sub.ID(c.Versions[ver-1].ID))
	if err != nil {
		return "", err
	}
	for _, o := range ops {
		if o.Path == path {
			return opDiff(s, o), nil
		}
	}
	return "", fmt.Errorf("capsule %s does not change %s", c.Name, path)
}

// ---- the capsule ops agents call ----

// capsuleOp runs one of the capsule.* ops for agent me.
func (m *Manager) capsuleOp(ctx context.Context, me *agentState, op string, a map[string]any) (string, error) {
	t := me.thread
	str := func(k string) string { v, _ := a[k].(string); return strings.TrimSpace(v) }
	ref := or(str("name"), str("id"))
	switch op {
	case "capsule.create":
		sp := CapsuleSpec{Name: str("name"), Source: strings.ToLower(str("from")), Worktree: str("worktree"), Range: str("range"), Note: str("note"), By: me.id}
		if sp.Source != FromWorktree {
			sp.Root = m.subRoot(me)
		}
		if sp.Source == FromWorktree && sp.Worktree == "" {
			if wt := me.getWorktree(); wt != nil {
				sp.Worktree = treeName(wt)
			}
		}
		name, ver, err := m.CreateCapsule(t.id, sp)
		if err != nil {
			return "", err
		}
		c, _, _ := t.findCapsule(fmt.Sprintf("%s@%d", name, ver))
		v := c.Versions[ver-1]
		return fmt.Sprintf("capsule %s v%d (%s): %d files (+%d ~%d -%d), %d commits.\nHand it over with thread.send {capsule: %q}; capsule.land lands it.",
			name, ver, v.ID, v.Files, v.Add, v.Mod, v.Del, v.CommitsTotal, v.ID), nil

	case "capsule.list":
		views := m.CapsuleViews(t.id)
		if len(views) == 0 {
			return "no named capsules. capsule.create {from: worktree|commits|diff} makes one.", nil
		}
		var b strings.Builder
		for _, c := range views {
			v := c.Versions[len(c.Versions)-1]
			fmt.Fprintf(&b, "%s v%d (%s, %s): %d files, by %s, tests %s", c.Name, len(c.Versions), c.Source, c.State, v.Files, strings.Join(c.Authors, ", "), or(c.Tests, "not run"))
			if len(c.Comments) > 0 {
				fmt.Fprintf(&b, ", %d comments", len(c.Comments))
			}
			b.WriteString("\n")
		}
		return b.String(), nil

	case "capsule.land":
		strategy, err := parseStrategy(str("strategy"))
		if err != nil {
			return "", err
		}
		into, check := str("into"), a["check"] == true
		if into == "main" {
			into = ""
		}
		if !check {
			if err := m.ask(ctx, me, agent.ApproveEdit, op, "land capsule "+ref+" in "+or(into, "the shared tree"), t.mainDir()); err != nil {
				return "", err
			}
		}
		res, err := m.LandCapsule(t.id, ref, into, check, strategy, me.id)
		if err != nil {
			return previewText(res.Files), err
		}
		verb := "landed"
		if check {
			verb = "would land"
		}
		return fmt.Sprintf("%s %s v%d in %s:\n%s", verb, res.Capsule, res.Version, or(into, "the shared tree"), previewText(res.Files)), nil

	case "capsule.rebase":
		res, err := m.RebaseCapsule(t.id, ref, or(str("onto"), "main"), me.id)
		if err != nil {
			return previewText(res.Files), err
		}
		return fmt.Sprintf("capsule %s v%d (%s) is rebased onto %s:\n%s", res.Capsule, res.Version, res.ID, or(str("onto"), "main"), previewText(res.Files)), nil

	case "capsule.revert":
		from := str("from")
		if from == "main" {
			from = ""
		}
		if err := m.ask(ctx, me, agent.ApproveEdit, op, "revert capsule "+ref+" from "+or(from, "the shared tree"), t.mainDir()); err != nil {
			return "", err
		}
		if err := m.RevertCapsule(t.id, ref, from, me.id); err != nil {
			return "", err
		}
		return "reverted " + ref + ".", nil

	case "capsule.review":
		if err := m.ReviewCapsule(t.id, ref, me.id, str("path"), str("comment"), strings.ToLower(str("tests")), str("test_note")); err != nil {
			return "", err
		}
		return "noted on capsule " + ref + ".", nil
	}
	return "", fmt.Errorf("unknown capsule op %q", op)
}
