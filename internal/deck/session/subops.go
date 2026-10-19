package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aymanbagabas/go-udiff"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/store"
	"github.com/dev-dami/deck/internal/deck/sub"
)

// The Source Update Bridge as agents use it (the sub.* ops). Agents hand
// each other code changes as capsule ids, not as descriptions of edits: one
// captures what it changed, sends the id with thread.send, and the other
// verifies and applies it in its own work tree. A capsule handed to a
// subagent with its own worktree is applied there for it.
//
// Every capsule is captured and applied relative to the agent's root: its
// worktree when it has one, else the repository root of the thread's folder
// (the folder itself outside git). Paths in a capsule are therefore
// repository-relative and mean the same in every worktree of the project.

// subRoot is where agent a's capsules are captured and applied.
func (m *Manager) subRoot(a *agentState) string {
	if wt := a.getWorktree(); wt != nil {
		return wt.Path
	}
	t := a.thread
	if r := m.repoOf(t); r != nil {
		return r.Root
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.dir
}

// subBaseOf is what agent a's next capture diffs from, the capsule that base
// already includes (applied in a's tree before it was taken), and a few
// words on where the base came from.
func (m *Manager) subBaseOf(a *agentState) (base, parent sub.ID, where string) {
	a.mu.Lock()
	base, parent = a.subBase, a.subParent
	wt := a.worktree
	a.mu.Unlock()
	switch {
	case base != "":
		return base, parent, "your last snapshot"
	case wt != nil && wt.SubBase != "":
		return sub.ID(wt.SubBase), sub.ID(wt.SubParent), "the start of your worktree"
	}
	t := a.thread
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := len(t.log) - 1; i >= 0; i-- {
		if e := t.log[i]; e.Type == store.EntrySnap && isSubSnap(e.Snap) {
			return sub.ID(e.Snap), "", "the checkpoint before the user's last message"
		}
	}
	return "", "", ""
}

// setSubBase records a's tree as it is now as its next capture's base, with
// parent the capsule it includes.
func (m *Manager) setSubBase(ctx context.Context, s *sub.Store, a *agentState, parent sub.ID) {
	id, err := s.Snap(ctx, m.subRoot(a))
	if err != nil {
		return
	}
	a.mu.Lock()
	a.subBase, a.subParent = id, parent
	a.mu.Unlock()
}

// knownCapsule checks id names a capsule in the store.
func (m *Manager) knownCapsule(id string) error {
	s := m.subStore()
	if s == nil {
		return errors.New("the Source Update Bridge is not available")
	}
	if !sub.ID(id).Valid() {
		return fmt.Errorf("%q is not a capsule id (blake3:<64 hex>, from sub.patch.capture)", id)
	}
	if _, err := s.Capsule(sub.ID(id)); err != nil {
		if errors.Is(err, sub.ErrNotFound) {
			return fmt.Errorf("no capsule %s here (a peer's capsule needs sub.capsule.fetch first)", sub.ID(id).Short())
		}
		return err
	}
	return nil
}

func appendCapsule(list []string, id string) []string {
	for _, c := range list {
		if c == id {
			return list
		}
	}
	return append(list, id)
}

// recordCapsule adds capsule id to task taskID's capsules.
func (m *Manager) recordCapsule(t *thread, taskID, id string) {
	if taskID == "" || id == "" {
		return
	}
	t.mu.Lock()
	i := store.FindTask(t.tasks, taskID)
	var tk store.Task
	if i >= 0 {
		t.tasks[i].Capsules = appendCapsule(t.tasks[i].Capsules, id)
		t.tasks[i].Updated = time.Now()
		tk = cloneTask(t.tasks[i])
	}
	t.mu.Unlock()
	if i >= 0 {
		m.persistMeta(t)
		m.emit(TaskChangedMsg{ThreadID: t.id, Task: tk, From: tk.Status})
	}
}

// runningTask is the task a works on now, if exactly one.
func (m *Manager) runningTask(a *agentState) string {
	t := a.thread
	t.mu.Lock()
	defer t.mu.Unlock()
	id := ""
	for _, tk := range t.tasks {
		if tk.Owner == a.id && tk.Status == store.TaskRunning {
			if id != "" {
				return ""
			}
			id = tk.ID
		}
	}
	return id
}

// capsuleSummary is one line on a capsule: what it changes, who made it.
func capsuleSummary(s *sub.Store, id sub.ID) string {
	c, err := s.Capsule(id)
	if err != nil {
		return string(id)
	}
	ops, err := s.Ops(id)
	if err != nil {
		return string(id)
	}
	var add, mod, del int
	for _, o := range ops {
		switch o.Kind() {
		case "add":
			add++
		case "delete":
			del++
		default:
			mod++
		}
	}
	line := fmt.Sprintf("capsule %s: %d files (+%d ~%d -%d)", id, len(ops), add, mod, del)
	if c.Meta.Source != "" {
		line += " by " + c.Meta.Source
	}
	if c.Meta.Task != "" {
		line += " for " + c.Meta.Task
	}
	if c.Meta.Note != "" {
		line += ": " + oneLine(c.Meta.Note)
	}
	return line
}

// handCapsule is what thread.send does with a capsule: checks it, applies it
// in the worktree of each recipient that has its own, records it on the
// task, and returns the lines that go under the message.
func (m *Manager) handCapsule(t *thread, targets []*agentState, taskID, id string) (string, error) {
	if err := m.knownCapsule(id); err != nil {
		return "", err
	}
	s := m.subStore()
	lines := []string{"[" + capsuleSummary(s, sub.ID(id)) + "]"}
	shared := false
	for _, to := range targets {
		if to.getWorktree() == nil {
			shared = true
			continue
		}
		lines = append(lines, m.materialize(s, to, sub.ID(id)))
	}
	if shared {
		lines = append(lines, "Look at it with execute sub.patch.inspect {\"id\": \""+id+"\", \"diff\": true}; apply it with sub.patch.apply.")
	}
	m.recordCapsule(t, taskID, id)
	return strings.Join(lines, "\n"), nil
}

// materialize applies capsule id in a's own worktree and says how it went.
func (m *Manager) materialize(s *sub.Store, a *agentState, id sub.ID) string {
	ctx, cancel := context.WithTimeout(m.ctx, gitTimeout)
	defer cancel()
	err := s.Apply(ctx, m.subRoot(a), id)
	var ce *sub.ConflictError
	switch {
	case err == nil:
		m.setSubBase(ctx, s, a, id)
		return "Deck applied it to " + a.getName() + "'s worktree; capsules " + a.getName() + " captures from here build on it."
	case errors.As(err, &ce):
		return "It does not apply cleanly to " + a.getName() + "'s worktree (" + strings.Join(ce.Paths, ", ") + " changed there): use sub.merge.preview and sub.merge.resolve."
	}
	return "Deck could not apply it to " + a.getName() + "'s worktree: " + err.Error()
}

// ---- the sub ops agents call (hub.SubBridge) ----

// subBoard is the bridge as one agent's sub ops see it.
type subBoard struct {
	m *Manager
	a *agentState
}

// maxListed bounds the files a listing names.
const maxListed = 200

// SubOp runs one of the sub.* ops for the calling agent.
func (b subBoard) SubOp(ctx context.Context, op string, a map[string]any) (string, error) {
	m, me, t := b.m, b.a, b.a.thread
	s := m.subStore()
	if s == nil {
		return "", errors.New("the Source Update Bridge is not available (no Deck store)")
	}
	str := func(k string) string { v, _ := a[k].(string); return strings.TrimSpace(v) }
	id := sub.ID(str("id"))
	needCapsule := func() error { return m.knownCapsule(string(id)) }
	root := m.subRoot(me)

	switch op {
	case "sub.snapshot.create":
		snap, err := s.Snap(ctx, root)
		if err != nil {
			return "", err
		}
		_, parent, _ := m.subBaseOf(me)
		me.mu.Lock()
		me.subBase, me.subParent = snap, parent
		me.mu.Unlock()
		n := 0
		if sn, err := s.Snapshot(snap); err == nil {
			n = len(sn.Files)
		}
		return fmt.Sprintf("snapshot %s of %s: %d files. sub.patch.capture now diffs from it.", snap, root, n), nil

	case "sub.snapshot.inspect":
		sn, err := s.Snapshot(id)
		if err != nil {
			return "", err
		}
		prefix := strings.TrimPrefix(filepath.ToSlash(str("path")), "./")
		var paths []string
		for p := range sn.Files {
			if strings.HasPrefix(p, prefix) {
				paths = append(paths, p)
			}
		}
		sort.Strings(paths)
		var out strings.Builder
		fmt.Fprintf(&out, "snapshot %s: %d files", id, len(sn.Files))
		if prefix != "" {
			fmt.Fprintf(&out, ", %d under %s", len(paths), prefix)
		}
		out.WriteString("\n")
		for i, p := range paths {
			if i == maxListed {
				fmt.Fprintf(&out, "… and %d more\n", len(paths)-i)
				break
			}
			out.WriteString(p + "\n")
		}
		return out.String(), nil

	case "sub.patch.capture":
		base, parent, where := m.subBaseOf(me)
		if v := str("base"); v != "" {
			base, parent, where = sub.ID(v), "", "snapshot "+sub.ID(v).Short()
		}
		if base == "" {
			return "", errors.New("no base to diff from: call sub.snapshot.create before you change files")
		}
		task := str("task")
		if task == "" {
			task = m.runningTask(me)
		}
		cid, err := s.Capture(ctx, root, base, parent, sub.Meta{Source: me.getName(), Task: task, Note: str("note")})
		if err != nil {
			return "", err
		}
		if cid == "" {
			return "nothing changed since " + where + ".", nil
		}
		m.recordCapsule(t, task, string(cid))
		return capsuleSummary(s, cid) + "\nchanges since " + where + "." +
			"\nHand it over with thread.send {\"to\": ..., \"message\": ..., \"capsule\": \"" + string(cid) + "\"}.", nil

	case "sub.patch.inspect":
		if err := needCapsule(); err != nil {
			return "", err
		}
		chain, err := s.Chain(id)
		if err != nil {
			return "", err
		}
		ops, err := s.Ops(id)
		if err != nil {
			return "", err
		}
		var out strings.Builder
		out.WriteString(capsuleSummary(s, id) + "\n")
		if len(chain) > 1 {
			fmt.Fprintf(&out, "a chain of %d capsules\n", len(chain))
		}
		for i, o := range ops {
			if i == maxListed {
				fmt.Fprintf(&out, "… and %d more\n", len(ops)-i)
				break
			}
			fmt.Fprintf(&out, "%-6s %s\n", o.Kind(), o.Path)
		}
		if a["diff"] == true {
			for _, o := range ops {
				out.WriteString(opDiff(s, o))
			}
		}
		return out.String(), nil

	case "sub.patch.verify":
		if err := needCapsule(); err != nil {
			return "", err
		}
		if err := s.Verify(ctx, root, id); err != nil {
			return "", fmt.Errorf("%w; sub.merge.preview shows which files can still merge", err)
		}
		return "fits: " + capsuleSummary(s, id) + " applies to " + root + " as is.", nil

	case "sub.patch.apply":
		if err := needCapsule(); err != nil {
			return "", err
		}
		if err := s.Verify(ctx, root, id); err != nil {
			return "", fmt.Errorf("%w; nothing was written (sub.merge.preview shows what can merge)", err)
		}
		if err := m.askWrite(ctx, me, op, s, id, root); err != nil {
			return "", err
		}
		if err := s.Apply(ctx, root, id); err != nil {
			return "", fmt.Errorf("%w; nothing was written (sub.merge.preview shows what can merge)", err)
		}
		m.setSubBase(ctx, s, me, id)
		m.emit(NoticeMsg{ThreadID: t.id, Text: me.getName() + " applied " + capsuleSummary(s, id)})
		return "applied " + capsuleSummary(s, id) + " to " + root + ".", nil

	case "sub.patch.revert":
		if err := needCapsule(); err != nil {
			return "", err
		}
		if err := m.askWrite(ctx, me, op, s, id, root); err != nil {
			return "", err
		}
		if err := s.Revert(ctx, root, id); err != nil {
			return "", fmt.Errorf("%w; nothing was written", err)
		}
		_, parent, _ := m.subBaseOf(me)
		if parent == id {
			parent = ""
		}
		m.setSubBase(ctx, s, me, parent)
		m.emit(NoticeMsg{ThreadID: t.id, Text: me.getName() + " reverted capsule " + id.Short()})
		return "reverted " + capsuleSummary(s, id) + " in " + root + ".", nil

	case "sub.merge.preview":
		if err := needCapsule(); err != nil {
			return "", err
		}
		prev, err := s.PreviewMerge(ctx, root, id)
		if err != nil {
			return "", err
		}
		return previewText(prev), nil

	case "sub.merge.resolve":
		if err := needCapsule(); err != nil {
			return "", err
		}
		strategy := sub.Strategy(strings.ToLower(str("strategy")))
		if strategy == "none" {
			strategy = sub.Refuse
		}
		if strategy != sub.Refuse && strategy != sub.Theirs && strategy != sub.Ours {
			return "", fmt.Errorf("unknown strategy %q: none, theirs or ours", strategy)
		}
		if strategy == sub.Refuse {
			prev, err := s.PreviewMerge(ctx, root, id)
			if err != nil {
				return "", err
			}
			for _, p := range prev {
				if p.State == sub.StateConflict {
					return previewText(prev), errors.New("conflicts; nothing was written: settle them with strategy theirs or ours")
				}
			}
		}
		if err := m.askWrite(ctx, me, op, s, id, root); err != nil {
			return "", err
		}
		prev, err := s.MergeApply(ctx, root, id, strategy)
		if err != nil {
			return previewText(prev), fmt.Errorf("%w; nothing was written: settle it with strategy theirs or ours", err)
		}
		// The tree now holds a merge, not the capsule's files, so what is
		// captured next cannot chain onto it.
		m.setSubBase(ctx, s, me, "")
		m.emit(NoticeMsg{ThreadID: t.id, Text: me.getName() + " merged capsule " + id.Short()})
		return "merged into " + root + ":\n" + previewText(prev), nil

	case "sub.capsule.list":
		want := str("task")
		var out strings.Builder
		t.mu.Lock()
		for _, tk := range t.tasks {
			if (want != "" && tk.ID != want) || len(tk.Capsules) == 0 {
				continue
			}
			fmt.Fprintf(&out, "%s %s:\n", tk.ID, tk.Title)
			for _, c := range tk.Capsules {
				out.WriteString("  " + c + "\n")
			}
		}
		t.mu.Unlock()
		if out.Len() == 0 {
			return "no capsules on the task board.", nil
		}
		return out.String(), nil

	case "sub.capsule.publish":
		if err := needCapsule(); err != nil {
			return "", err
		}
		ids, err := s.Publish(id)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("published %s (%d objects). A paired peer allowed %q can now fetch it.", id, len(ids), "sub.objects"), nil

	case "sub.capsule.fetch":
		m.mu.Lock()
		fetch := m.subFetch
		m.mu.Unlock()
		if fetch == nil {
			return "", errors.New("peers are not running here, so nothing can be fetched")
		}
		if !id.Valid() {
			return "", fmt.Errorf("%q is not a capsule id", id)
		}
		n, err := fetch(ctx, str("peer"), id, s)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("fetched %s from %s (%d new objects).", capsuleSummary(s, id), str("peer"), n), nil
	}
	return "", fmt.Errorf("unknown sub op %q", op)
}

// SubFetcher pulls capsule id and everything it needs from the named peer
// into s, returning how many objects it had to transfer.
type SubFetcher func(ctx context.Context, peer string, id sub.ID, s *sub.Store) (int, error)

// SetSubFetch installs how sub.capsule.fetch reaches peers (the engine does,
// when the peer link runs).
func (m *Manager) SetSubFetch(f SubFetcher) {
	m.mu.Lock()
	m.subFetch = f
	m.mu.Unlock()
}

// SubStore is the Source Update Bridge's object store, nil when there is
// none (the engine serves peers from it).
func (m *Manager) SubStore() *sub.Store { return m.subStore() }

func previewText(prev []sub.Preview) string {
	var out strings.Builder
	for i, p := range prev {
		if i == maxListed {
			fmt.Fprintf(&out, "… and %d more\n", len(prev)-i)
			break
		}
		fmt.Fprintf(&out, "%-8s %-6s %s", p.State, p.Kind(), p.Path)
		if p.Reason != "" {
			out.WriteString(" (" + p.Reason + ")")
		}
		out.WriteString("\n")
	}
	return out.String()
}

// maxOpDiff bounds the patch shown for one file.
const maxOpDiff = 8 << 10

// opDiff is one op as a unified diff.
func opDiff(s *sub.Store, o sub.Op) string {
	a, err := subText(s, o.Before)
	if err != nil {
		return "--- " + o.Path + ": " + err.Error() + "\n"
	}
	b, err := subText(s, o.After)
	if err != nil {
		return "--- " + o.Path + ": " + err.Error() + "\n"
	}
	if strings.IndexByte(string(a), 0) >= 0 || strings.IndexByte(string(b), 0) >= 0 {
		return fmt.Sprintf("--- %s: binary, %d → %d bytes\n", o.Path, len(a), len(b))
	}
	from, to := "a/"+o.Path, "b/"+o.Path
	if o.Before == "" {
		from = "/dev/null"
	}
	if o.After == "" {
		to = "/dev/null"
	}
	d := udiff.Unified(from, to, string(a), string(b))
	if len(d) > maxOpDiff {
		d = d[:maxOpDiff] + "\n[... diff of " + o.Path + " cut ...]\n"
	}
	return d
}

// ---- garbage collection ----

// subGCEvery is how often CollectSub sweeps, and subGCGrace how old an
// unreferenced object must be before it goes.
const (
	subGCEvery = 24 * time.Hour
	subGCGrace = 14 * 24 * time.Hour
)

// CollectSub deletes the SUB objects nothing refers to any more, at most once
// a day: what every thread's checkpoints and reverts, task capsules and
// worktree bases still use is kept, and so is anything newer than two weeks
// (a capsule only mentioned in a message lives that long). It is slow on a
// big store; run it in the background.
func (m *Manager) CollectSub() {
	s := m.subStore()
	if s == nil || m.st == nil {
		return
	}
	stamp := filepath.Join(s.Dir(), "gc-stamp")
	if fi, err := os.Stat(stamp); err == nil && time.Since(fi.ModTime()) < subGCEvery {
		return
	}
	threads, err := m.st.List()
	if err != nil {
		return
	}
	var roots []sub.ID
	add := func(id string) {
		if isSubSnap(id) {
			roots = append(roots, sub.ID(id))
		}
	}
	for _, th := range threads {
		if m.ctx.Err() != nil {
			return
		}
		entries, meta, err := m.st.LoadEntries(th.ID)
		if err != nil {
			return // keep everything rather than drop what an unreadable thread uses
		}
		for _, e := range entries {
			add(e.Snap)
		}
		for _, tk := range meta.Tasks {
			for _, c := range tk.Capsules {
				add(c)
			}
		}
		for _, wt := range append([]*store.Worktree{meta.Worktree}, memberWorktrees(meta.Members)...) {
			if wt != nil {
				add(wt.SubBase)
				add(wt.SubParent)
			}
		}
	}
	m.mu.Lock()
	agents := make([]*agentState, 0, len(m.byID))
	for _, a := range m.byID {
		agents = append(agents, a)
	}
	m.mu.Unlock()
	for _, a := range agents {
		a.mu.Lock()
		add(string(a.subBase))
		add(string(a.subParent))
		a.mu.Unlock()
	}
	if _, err := s.GC(roots, subGCGrace); err == nil {
		_ = os.WriteFile(stamp, nil, 0o644)
		_ = os.Chtimes(stamp, time.Now(), time.Now())
	}
}

// seedWorktree readies a subagent's new worktree for SUB: the capsules
// already on its task are applied there (a reviewer starts from the changes
// it reviews), and the tree is snapshotted as the base its own capsules are
// captured from. It returns a line for the brief, "" when there is nothing
// to say.
func (m *Manager) seedWorktree(t *thread, a *agentState, wt *store.Worktree, taskID string) string {
	s := m.subStore()
	if s == nil {
		return ""
	}
	var capsules []string
	if taskID != "" {
		t.mu.Lock()
		if i := store.FindTask(t.tasks, taskID); i >= 0 {
			capsules = append(capsules, t.tasks[i].Capsules...)
		}
		t.mu.Unlock()
	}
	var notes []string
	var parent sub.ID
	for _, c := range capsules {
		notes = append(notes, m.materialize(s, a, sub.ID(c)))
		a.mu.Lock()
		parent = a.subParent
		a.mu.Unlock()
	}
	ctx, cancel := context.WithTimeout(m.ctx, gitTimeout)
	defer cancel()
	if base, err := s.Snap(ctx, wt.Path); err == nil {
		// A new value, not an edit of wt: saved metadata may share wt and be
		// read by the store's writer.
		seeded := *wt
		seeded.SubBase, seeded.SubParent = string(base), string(parent)
		a.mu.Lock()
		a.worktree = &seeded
		a.subBase, a.subParent = "", ""
		a.mu.Unlock()
	}
	if len(notes) == 0 {
		return ""
	}
	return "Capsules of this task:\n" + strings.Join(notes, "\n")
}

func memberWorktrees(ms []store.Member) []*store.Worktree {
	out := make([]*store.Worktree, 0, len(ms))
	for _, mb := range ms {
		out = append(out, mb.Worktree)
	}
	return out
}

// askMargin is the time an approved write keeps for itself out of the op's
// deadline.
const askMargin = 10 * time.Second

// askWrite asks permission for a sub op that writes the agent's tree, the
// way an agent's own file edit is asked: the thread's mode answers in plan
// (no) and yolo (yes), the user in ask. An unanswered request is withdrawn
// before the op's deadline, so it never applies after the agent gave up.
func (m *Manager) askWrite(ctx context.Context, a *agentState, op string, s *sub.Store, id sub.ID, root string) error {
	ops, err := s.Ops(id)
	if err != nil {
		return err
	}
	var names []string
	for i, o := range ops {
		if i == 5 {
			names = append(names, fmt.Sprintf("+%d more", len(ops)-i))
			break
		}
		names = append(names, o.Path)
	}
	if a.getWorktree() == nil {
		// Into the shared tree: not over another member's exclusive lease.
		paths := make([]string, len(ops))
		for i, o := range ops {
			paths[i] = o.Path
		}
		t := a.thread
		t.mu.Lock()
		err := t.leaseBlock(a, paths)
		t.mu.Unlock()
		if err != nil {
			return err
		}
	}
	summary := fmt.Sprintf("%s %s: %d files (%s) in %s", op, id.Short(), len(ops), strings.Join(names, ", "), root)
	return m.ask(ctx, a, agent.ApproveEdit, op, summary, root)
}

// ask raises an approval request for agent a and waits for the answer: nil
// when allowed. The thread's mode answers what it answers (enforce).
func (m *Manager) ask(ctx context.Context, a *agentState, kind agent.ApprovalKind, op, summary, cwd string) error {
	decided := make(chan agent.Decision, 1)
	req := agent.ApprovalRequest{
		ID: "sub-" + store.NewID(), Kind: kind, ToolName: op, Summary: summary, Cwd: cwd,
		Respond: func(d agent.Decision) { decided <- d },
	}
	m.onApproval(a, req)
	wait := 50 * time.Second
	if dl, ok := ctx.Deadline(); ok {
		wait = time.Until(dl) - askMargin
	}
	timer := time.NewTimer(max(wait, time.Second))
	defer timer.Stop()
	select {
	case d := <-decided:
		if !d.Allow {
			reason := "the user declined"
			if d.Reason != "" {
				reason += ": " + d.Reason
			}
			return errors.New(reason)
		}
		return nil
	case <-timer.C:
	case <-ctx.Done():
	}
	if a.claimPending(req.ID) {
		m.emit(ApprovalGoneMsg{ThreadID: a.thread.id, AgentID: a.id, ID: req.ID})
		return errors.New("the user did not answer in time; nothing was done (ask them, then call it again)")
	}
	// Answered just as time ran out.
	if d := <-decided; !d.Allow {
		return errors.New("the user declined")
	}
	return nil
}
