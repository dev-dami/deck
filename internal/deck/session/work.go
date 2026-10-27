package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/changes"
	"github.com/dev-dami/deck/internal/deck/store"
	"github.com/dev-dami/deck/internal/deck/sub"
)

// Worktrees as lines of work. Every worktree Deck makes (an isolated
// thread's, a subagent's, a race entry's, or one made on its own with
// worktree.create or from the Work tab) is a branch Deck tracks: its owner
// and task, the commit it forked from and the branch it keeps up with, how
// far ahead and behind that it is, and what it changed. Deck syncs it with
// its base (merge or rebase, stopping cleanly on conflicts), lands it in
// the shared tree as a capsule with a three-way merge, and prunes it once
// it is landed and nobody works in it. The thread's own worktrees, apart
// from its agents', are kept with the thread (store.Thread.Trees), so all
// of them survive a restart.

// The kinds of worktree a thread has.
const (
	TreeThread = "thread" // the thread works on it (Isolate)
	TreeAgent  = "agent"  // one member's own
	TreeOwn    = "tree"   // the thread's, made on its own
)

// WorktreeInfo is one worktree as the Work tab and worktree.list show it.
type WorktreeInfo struct {
	// Name is its branch without "deck/": what ops and the UI call it.
	Name, Branch, Path, Main string
	Fork, BaseRef            string
	Kind                     string
	Owner, OwnerName, Task   string
	Working                  bool // its owner is in a turn
	Created                  time.Time
	// Ahead and Behind count commits against BaseRef; Dirty the files not
	// committed; Files what it changed since Fork, committed or not.
	Ahead, Behind, Dirty int
	Files                []string
	// LandedAt is when it was last landed in the shared tree; Landed is set
	// while nothing changed since.
	LandedAt time.Time
	Landed   bool
	Err      string
}

// maxTreeFiles bounds the files a WorktreeInfo names.
const maxTreeFiles = 400

// workLine is one worktree of a thread and who has it.
type workLine struct {
	wt   *store.Worktree
	kind string
	a    *agentState // its member, for TreeAgent
}

func treeName(wt *store.Worktree) string { return strings.TrimPrefix(wt.Branch, "deck/") }

// lines lists the thread's worktrees: its own, then its members', then the
// ones made on their own.
func (t *thread) lines() []workLine {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []workLine
	if t.worktree != nil {
		out = append(out, workLine{wt: t.worktree, kind: TreeThread})
	}
	for _, a := range t.members {
		if wt := a.getWorktree(); wt != nil {
			out = append(out, workLine{wt: wt, kind: TreeAgent, a: a})
		}
	}
	for _, wt := range t.trees {
		out = append(out, workLine{wt: wt, kind: TreeOwn})
	}
	return out
}

// findLine is the worktree name names: its name or its branch.
func (t *thread) findLine(name string) (workLine, error) {
	name = strings.TrimSpace(name)
	for _, l := range t.lines() {
		if name == treeName(l.wt) || name == l.wt.Branch {
			return l, nil
		}
	}
	return workLine{}, fmt.Errorf("no worktree %q in this thread (worktree.list names them)", name)
}

// mainDir is the shared tree the thread came from: its folder, or the one
// it was isolated from.
func (t *thread) mainDir() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.worktree != nil && t.worktree.Main != "" {
		return t.worktree.Main
	}
	return t.dir
}

// newLine is the line of a worktree just made from base in repository r.
func (m *Manager) newLine(r *changes.Repo, base, owner string) store.Line {
	ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
	defer cancel()
	ref := r.Branch(ctx)
	if ref == "" {
		ref = base
	}
	return store.Line{Fork: base, BaseRef: ref, Owner: owner, Created: time.Now()}
}

// treePath is where a worktree of thread t's repository r named name goes.
func (m *Manager) treePath(t *thread, r *changes.Repo, name string) string {
	return filepath.Join(filepath.Dir(m.st.Dir()), "worktrees", filepath.Base(r.Root)+"-"+name)
}

// slug makes s fit a branch name: lower case letters, digits and dashes.
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		switch {
		case ok:
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 32 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

// TreeSpec says how to make a worktree.
type TreeSpec struct {
	Name string // "" picks one
	Base string // branch or commit to start from; "" is the shared tree's HEAD
	Task string
	// Owner is the member it is for; "" leaves it the thread's.
	Owner string
}

// CreateTree makes a worktree of the thread's repository on a branch of its
// own, tracked with the thread until it is landed or discarded.
func (m *Manager) CreateTree(threadID string, sp TreeSpec) (WorktreeInfo, error) {
	t := m.thread(threadID)
	if t == nil {
		return WorktreeInfo{}, fmt.Errorf("session: no thread %q", threadID)
	}
	if m.st == nil {
		return WorktreeInfo{}, errors.New("session: no store")
	}
	ctx, cancel := context.WithTimeout(m.ctx, gitTimeout)
	defer cancel()
	r, err := changes.Open(ctx, t.mainDir())
	if err != nil {
		return WorktreeInfo{}, errors.New("session: a worktree needs a git repository")
	}
	id := t.id
	if len(id) > 6 {
		id = id[:6]
	}
	name := slug(sp.Name)
	if name == "" {
		name = slug(sp.Task)
	}
	if name == "" {
		name = fmt.Sprintf("w%d", len(t.lines())+1)
	}
	name = id + "-" + name
	for _, l := range t.lines() {
		if treeName(l.wt) == name {
			return WorktreeInfo{}, fmt.Errorf("session: there is a worktree %q already", name)
		}
	}
	path := m.treePath(t, r, name)
	base, err := r.AddWorktreeAt(ctx, path, "deck/"+name, strings.TrimSpace(sp.Base))
	if err != nil {
		return WorktreeInfo{}, err
	}
	ln := m.newLine(r, base, sp.Owner)
	if sp.Base != "" {
		ln.BaseRef = strings.TrimSpace(sp.Base)
	}
	ln.Task = sp.Task
	wt := &store.Worktree{Path: path, Branch: "deck/" + name, Main: t.mainDir(), Base: base, Line: ln}
	if s := m.subStore(); s != nil {
		if snap, err := s.Snap(ctx, path); err == nil {
			wt.SubBase = string(snap)
		}
	}
	t.mu.Lock()
	t.trees = append(slices.Clone(t.trees), wt)
	t.mu.Unlock()
	m.persistMeta(t)
	m.notice(t, fmt.Sprintf("made worktree %s on branch %s from %s", name, wt.Branch, shortRef(ln.BaseRef)))
	return m.lineInfo(ctx, t, workLine{wt: wt, kind: TreeOwn}), nil
}

func shortRef(ref string) string {
	if len(ref) == 40 && strings.Trim(ref, "0123456789abcdef") == "" {
		return ref[:10]
	}
	return ref
}

// Trees lists the thread's worktrees with where each stands.
func (m *Manager) Trees(threadID string) []WorktreeInfo {
	t := m.thread(threadID)
	if t == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(m.ctx, gitTimeout)
	defer cancel()
	var out []WorktreeInfo
	for _, l := range t.lines() {
		out = append(out, m.lineInfo(ctx, t, l))
	}
	return out
}

// lineInfo is where worktree l stands now.
func (m *Manager) lineInfo(ctx context.Context, t *thread, l workLine) WorktreeInfo {
	wt := l.wt
	in := WorktreeInfo{Name: treeName(wt), Branch: wt.Branch, Path: wt.Path, Main: wt.Main, Fork: wt.Fork, BaseRef: wt.BaseRef,
		Kind: l.kind, Owner: wt.Owner, Task: wt.Task, Created: wt.Created, LandedAt: wt.LandedAt}
	if l.a != nil {
		in.Owner, in.OwnerName, in.Working = l.a.id, l.a.getName(), l.a.busy()
		if in.Task == "" {
			in.Task = m.runningTask(l.a)
		}
	} else if in.Owner != "" {
		t.mu.Lock()
		if a := t.member(in.Owner); a != nil {
			in.OwnerName, in.Working = a.getName(), a.busy()
		}
		t.mu.Unlock()
	}
	if in.Fork == "" {
		in.Fork = wt.Base // stored before lines: Base is the fork until an apply
	}
	r, err := changes.Open(ctx, wt.Path)
	if err != nil {
		in.Err = "the worktree is gone from " + wt.Path
		return in
	}
	if in.BaseRef != "" {
		if a, b, err := r.Divergence(ctx, in.BaseRef, "HEAD"); err == nil {
			in.Ahead, in.Behind = a, b
		}
	}
	if d, err := r.Dirty(ctx); err == nil {
		in.Dirty = len(d)
	}
	if tree, err := r.Snapshot(ctx); err == nil {
		in.Landed = wt.Landed != "" && tree == wt.Landed
		if chs, err := r.Changed(ctx, in.Fork, tree); err == nil {
			for _, c := range chs {
				if len(in.Files) == maxTreeFiles {
					break
				}
				in.Files = append(in.Files, c.Path)
			}
		}
	}
	return in
}

// TreeStatus is one worktree's WorktreeInfo.
func (m *Manager) TreeStatus(threadID, name string) (WorktreeInfo, error) {
	t := m.thread(threadID)
	if t == nil {
		return WorktreeInfo{}, fmt.Errorf("session: no thread %q", threadID)
	}
	l, err := t.findLine(name)
	if err != nil {
		return WorktreeInfo{}, err
	}
	ctx, cancel := context.WithTimeout(m.ctx, gitTimeout)
	defer cancel()
	return m.lineInfo(ctx, t, l), nil
}

// errOwnerWorking refuses to move a worktree under an agent mid-turn.
func errOwnerWorking(name string) error {
	return fmt.Errorf("session: %s is working in it; try again when its turn ends", name)
}

// SyncTree brings the worktree's base branch into it: its uncommitted work
// is committed, then the base merged in (or the branch rebased onto it). A
// conflict leaves it as it was and comes back as a *changes.ConflictError.
// by is the member asking ("" for the user); another member's worktree is
// not synced under it while it works.
func (m *Manager) SyncTree(threadID, name string, rebase bool, by string) (WorktreeInfo, error) {
	t := m.thread(threadID)
	if t == nil {
		return WorktreeInfo{}, fmt.Errorf("session: no thread %q", threadID)
	}
	l, err := t.findLine(name)
	if err != nil {
		return WorktreeInfo{}, err
	}
	if l.a != nil && l.a.id != by && l.a.busy() {
		return WorktreeInfo{}, errOwnerWorking(l.a.getName())
	}
	ctx, cancel := context.WithTimeout(m.ctx, gitTimeout)
	defer cancel()
	r, err := changes.Open(ctx, l.wt.Path)
	if err != nil {
		return WorktreeInfo{}, fmt.Errorf("session: the worktree is gone from %s", l.wt.Path)
	}
	ref := l.wt.BaseRef
	if ref == "" {
		return WorktreeInfo{}, errors.New("session: this worktree has no base branch to sync with")
	}
	how := "merged"
	if rebase {
		how = "rebased onto"
	}
	err = r.Sync(ctx, ref, "deck: work in progress on "+treeName(l.wt), rebase)
	var ce *changes.ConflictError
	switch {
	case errors.As(err, &ce):
		m.notice(t, fmt.Sprintf("could not sync %s with %s: %d files conflict (%s); it is as it was", treeName(l.wt), shortRef(ref), len(ce.Paths), strings.Join(ce.Paths[:min(3, len(ce.Paths))], ", ")))
		return m.lineInfo(ctx, t, l), err
	case err != nil:
		return WorktreeInfo{}, err
	}
	m.notice(t, fmt.Sprintf("%s %s %s", treeName(l.wt), how, shortRef(ref)))
	return m.lineInfo(ctx, t, l), nil
}

// LandResult is how landing a worktree or capsule went (or would go, for
// a check): each file's state, the capsule version it landed, and where.
type LandResult struct {
	Capsule string // the named capsule
	Version int    // its version, from 1
	ID      string // the SUB capsule
	Into    string
	Files   []sub.Preview
	Checked bool // nothing was written
}

// Conflicts are the files that conflict.
func (r LandResult) Conflicts() []string {
	var out []string
	for _, p := range r.Files {
		if p.State == sub.StateConflict {
			out = append(out, p.Path)
		}
	}
	return out
}

// LandTree lands a worktree's changes since its fork (commits and
// uncommitted edits) in the shared tree, or in another worktree (into), as
// uncommitted edits: it becomes a version of the capsule named after the
// worktree, which is merged three-way. check only reports how each file
// would land. Conflicts write nothing unless strategy settles them. A
// worktree of its own whose owner is not working is pruned once landed.
func (m *Manager) LandTree(threadID, name, into string, check bool, strategy sub.Strategy, by string) (LandResult, error) {
	t := m.thread(threadID)
	if t == nil {
		return LandResult{}, fmt.Errorf("session: no thread %q", threadID)
	}
	l, err := t.findLine(name)
	if err != nil {
		return LandResult{}, err
	}
	ctx, cancel := context.WithTimeout(m.ctx, gitTimeout)
	defer cancel()
	rec, ver, err := m.captureTree(ctx, t, l.wt, m.byName(t, by), "")
	if err != nil {
		return LandResult{}, err
	}
	res, err := m.land(ctx, t, rec, ver, into, check, strategy, by)
	if err != nil || check {
		return res, err
	}
	if r, err := changes.Open(ctx, l.wt.Path); err == nil {
		if tree, err := r.Snapshot(ctx); err == nil {
			m.markLanded(t, l, tree)
		}
	}
	if l.kind == TreeOwn && (l.wt.Owner == "" || m.memberIdle(t, l.wt.Owner)) && into == "" {
		m.pruneTrees(t)
	}
	return res, nil
}

// markLanded records worktree l as landed at tree.
func (m *Manager) markLanded(t *thread, l workLine, tree string) {
	next := *l.wt
	next.Landed, next.LandedAt = tree, time.Now()
	m.replaceLine(t, l, &next)
}

// replaceLine swaps worktree l for next where it is kept. A stored
// worktree is never edited in place: saved metadata may share it.
func (m *Manager) replaceLine(t *thread, l workLine, next *store.Worktree) {
	switch l.kind {
	case TreeThread:
		t.mu.Lock()
		if t.worktree == l.wt {
			t.worktree = next
		}
		t.mu.Unlock()
	case TreeAgent:
		l.a.mu.Lock()
		if l.a.worktree == l.wt {
			l.a.worktree = next
		}
		l.a.mu.Unlock()
	default:
		t.mu.Lock()
		trees := slices.Clone(t.trees)
		for i, wt := range trees {
			if wt == l.wt {
				trees[i] = next
			}
		}
		t.trees = trees
		t.mu.Unlock()
	}
	m.persistMeta(t)
}

// DiscardTree removes a worktree and its branch, whatever it changed. A
// member's worktree goes with that member's run (DiscardMember); the
// thread's own isolation is undone (Unisolate). by is the member asking
// ("" for the user): a member may discard its own worktree or one nobody
// owns, not another's.
func (m *Manager) DiscardTree(threadID, name, by string) error {
	t := m.thread(threadID)
	if t == nil {
		return fmt.Errorf("session: no thread %q", threadID)
	}
	l, err := t.findLine(name)
	if err != nil {
		return err
	}
	owner := l.wt.Owner
	if l.a != nil {
		owner = l.a.id
	}
	if by != "" && owner != "" && owner != by {
		return errors.New("session: that worktree is another agent's; ask it, or the user, to discard it")
	}
	switch l.kind {
	case TreeThread:
		if by != "" {
			return errors.New("session: the thread's own worktree is the user's to discard")
		}
		_, _, err := m.Unisolate(threadID, true)
		return err
	case TreeAgent:
		return m.DiscardMember(threadID, l.a.id)
	}
	t.mu.Lock()
	t.trees = slices.DeleteFunc(slices.Clone(t.trees), func(wt *store.Worktree) bool { return wt == l.wt })
	t.mu.Unlock()
	m.persistMeta(t)
	m.dropWorktree(l.wt)
	m.notice(t, "discarded worktree "+treeName(l.wt))
	return nil
}

// takeTree hands one of the thread's own worktrees to member owner, for a
// spawn: it leaves the thread's list and becomes that member's. It returns
// the worktree and the directory in it matching the thread's.
func (m *Manager) takeTree(t *thread, name, owner, task string) (*store.Worktree, string, error) {
	l, err := t.findLine(name)
	if err != nil {
		return nil, "", err
	}
	if l.kind != TreeOwn {
		return nil, "", fmt.Errorf("session: worktree %s is not free (it is a %s worktree)", name, l.kind)
	}
	next := *l.wt
	next.Owner = owner
	if task != "" && next.Task == "" {
		next.Task = task
	}
	t.mu.Lock()
	t.trees = slices.DeleteFunc(slices.Clone(t.trees), func(wt *store.Worktree) bool { return wt == l.wt })
	dir := t.dir
	t.mu.Unlock()
	target := next.Path
	if rel, err := filepath.Rel(next.Main, dir); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		target = filepath.Join(next.Path, rel)
	}
	return &next, target, nil
}

// returnTree puts back a worktree takeTree took, for a spawn that failed.
func (m *Manager) returnTree(t *thread, wt *store.Worktree) {
	if wt == nil {
		return
	}
	back := *wt
	back.Owner = ""
	t.mu.Lock()
	t.trees = append(slices.Clone(t.trees), &back)
	t.mu.Unlock()
	m.persistMeta(t)
}

// pruneTrees forgets the thread's own worktrees that are gone from disk,
// and removes those landed and unchanged since whose owner is not working,
// and exited members' worktrees landed and unchanged since. Work that was
// never landed is never removed here.
func (m *Manager) pruneTrees(t *thread) {
	ctx, cancel := context.WithTimeout(m.ctx, gitTimeout)
	defer cancel()
	var gone []workLine
	for _, l := range t.lines() {
		if l.kind == TreeThread {
			continue
		}
		if l.a != nil && l.a.status() != StatusExited {
			continue
		}
		if l.a == nil && l.wt.Owner != "" && !m.memberIdle(t, l.wt.Owner) {
			continue
		}
		if _, err := os.Stat(l.wt.Path); errors.Is(err, os.ErrNotExist) {
			gone = append(gone, l)
			continue
		}
		if l.wt.Landed == "" {
			continue
		}
		r, err := changes.Open(ctx, l.wt.Path)
		if err != nil {
			continue
		}
		if tree, err := r.Snapshot(ctx); err == nil && tree == l.wt.Landed {
			gone = append(gone, l)
		}
	}
	if len(gone) == 0 {
		return
	}
	var names []string
	for _, l := range gone {
		names = append(names, treeName(l.wt))
		if l.a != nil {
			l.a.mu.Lock()
			if l.a.worktree == l.wt {
				l.a.worktree = nil
			}
			l.a.mu.Unlock()
		} else {
			t.mu.Lock()
			t.trees = slices.DeleteFunc(slices.Clone(t.trees), func(wt *store.Worktree) bool { return wt == l.wt })
			t.mu.Unlock()
		}
		m.dropWorktree(l.wt)
	}
	m.persistMeta(t)
	m.notice(t, "pruned landed worktrees: "+strings.Join(names, ", "))
}

// byName is member id's name, "" for the user.
func (m *Manager) byName(t *thread, id string) string {
	if id == "" {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if a := t.member(id); a != nil {
		return a.getName()
	}
	return id
}

// longTask reports whether the scheduler should give task tk a worktree of
// its own under orchestration though it did not ask: long work (a budget
// of half an hour or more, or a task with parts of its own) in a git
// repository.
func (m *Manager) longTask(t *thread, tk store.Task) bool {
	t.mu.Lock()
	on := t.orchestrate
	parts := false
	for _, o := range t.tasks {
		if o.Parent == tk.ID {
			parts = true
			break
		}
	}
	t.mu.Unlock()
	if !on || m.repoOf(t) == nil {
		return false
	}
	return parts || (tk.Budget != nil && tk.Budget.Minutes >= 30)
}

// ---- the worktree ops agents call ----

// treeOp runs one of the worktree.* ops for agent me.
func (m *Manager) treeOp(ctx context.Context, me *agentState, op string, a map[string]any) (string, error) {
	t := me.thread
	str := func(k string) string { v, _ := a[k].(string); return strings.TrimSpace(v) }
	name := str("name")
	switch op {
	case "worktree.create":
		in, err := m.CreateTree(t.id, TreeSpec{Name: name, Base: str("base"), Task: str("task"), Owner: me.id})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("worktree %s: %s on branch %s from %s.\nWork there (cd %s); the shared tree is untouched. worktree.sync brings %s in, worktree.merge lands it in the shared tree; agent.spawn {worktree: %q} starts a helper in it.",
			in.Name, in.Path, in.Branch, shortRef(in.BaseRef), in.Path, shortRef(in.BaseRef), in.Name), nil

	case "worktree.list":
		ins := m.Trees(t.id)
		if len(ins) == 0 {
			return "no worktrees in this thread. worktree.create makes one.", nil
		}
		var b strings.Builder
		for _, in := range ins {
			b.WriteString(treeLine(in) + "\n")
		}
		return b.String(), nil

	case "worktree.status":
		in, err := m.TreeStatus(t.id, name)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		b.WriteString(treeLine(in) + "\n")
		fmt.Fprintf(&b, "path %s, forked at %s\n", in.Path, shortRef(in.Fork))
		for i, f := range in.Files {
			if i == 50 {
				fmt.Fprintf(&b, "… and %d more\n", len(in.Files)-i)
				break
			}
			b.WriteString("  " + f + "\n")
		}
		return b.String(), nil

	case "worktree.sync":
		in, err := m.SyncTree(t.id, name, a["rebase"] == true, me.id)
		var ce *changes.ConflictError
		if errors.As(err, &ce) {
			return "", fmt.Errorf("conflicts with %s in %s; nothing changed. Sync again after settling them, or merge them by hand in %s", shortRef(in.BaseRef), strings.Join(ce.Paths, ", "), in.Path)
		}
		if err != nil {
			return "", err
		}
		return "synced: " + treeLine(in), nil

	case "worktree.merge":
		check := a["check"] == true
		strategy, err := parseStrategy(str("strategy"))
		if err != nil {
			return "", err
		}
		into := str("into")
		if into == "main" {
			into = ""
		}
		if !check {
			if err := m.ask(ctx, me, agent.ApproveEdit, op, "land worktree "+name+" in "+or(into, "the shared tree"), t.mainDir()); err != nil {
				return "", err
			}
		}
		res, err := m.LandTree(t.id, name, into, check, strategy, me.id)
		if err != nil {
			if len(res.Files) > 0 {
				return previewText(res.Files), err
			}
			return "", err
		}
		verb := "landed"
		if check {
			verb = "would land"
		}
		return fmt.Sprintf("%s %s v%d (%s) in %s:\n%s", verb, res.Capsule, res.Version, res.ID, or(res.Into, "the shared tree"), previewText(res.Files)), nil

	case "worktree.discard":
		if err := m.DiscardTree(t.id, name, me.id); err != nil {
			return "", err
		}
		return "discarded worktree " + name + " and its branch.", nil
	}
	return "", fmt.Errorf("unknown worktree op %q", op)
}

// treeLine is one worktree in a line of text.
func treeLine(in WorktreeInfo) string {
	who := in.OwnerName
	if who == "" {
		who = "nobody"
	}
	s := fmt.Sprintf("%s (%s, %s): %s, +%d -%d vs %s, %d files changed, %d uncommitted", in.Name, in.Kind, who, in.Branch, in.Ahead, in.Behind, shortRef(in.BaseRef), len(in.Files), in.Dirty)
	if in.Task != "" {
		s += ", task " + in.Task
	}
	if in.Landed {
		s += ", landed"
	}
	if in.Err != "" {
		s += " [" + in.Err + "]"
	}
	return s
}

// spawnTree reads agent.spawn's worktree argument: true (or "new") for a
// worktree of its own, a worktree's name to start the helper in that one.
func spawnTree(isolate bool, v any) (bool, string) {
	var raw []byte
	switch x := v.(type) {
	case json.RawMessage:
		raw = x
	case bool:
		return isolate || x, ""
	case string:
		raw, _ = json.Marshal(x)
	default:
		return isolate, ""
	}
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return isolate || b, ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return isolate, ""
	}
	switch s = strings.TrimSpace(s); strings.ToLower(s) {
	case "", "false", "no":
		return isolate, ""
	case "true", "new", "yes":
		return true, ""
	}
	return false, s
}

func parseStrategy(s string) (sub.Strategy, error) {
	switch st := sub.Strategy(strings.ToLower(s)); st {
	case "", "none":
		return sub.Refuse, nil
	case sub.Theirs, sub.Ours:
		return st, nil
	}
	return "", fmt.Errorf("unknown strategy %q: none, theirs or ours", s)
}
