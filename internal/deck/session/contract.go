package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/changes"
	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/store"
	"github.com/dev-dami/deck/internal/deck/sub"
)

// Code contracts on the task board (docs/multiplayer-design.md §8.4). On the
// Deck that asks, a contract is a task that a teammate's Deck does: its owner
// is that Deck, then its agent ("vps/claude"), and the tasks that depend on
// it wait for it like for any other. On the teammate's Deck it is a thread
// of its own, on a copy of the asking Deck's tree, that its person can watch.
// Only a capsule and the agent's last reply go back.

// Contractor sends code contracts to teammates (the engine, over the peer
// link).
type Contractor interface {
	// Propose asks peer to do p. queued is set when peer could not be
	// reached and the proposal waits for its link; an error says why it
	// could not be asked.
	Propose(ctx context.Context, peer string, p Proposal) (queued bool, err error)
}

// Proposal is a contract as this Deck asks for it.
type Proposal struct {
	// Thread and Task are the task on this Deck's board.
	Thread, Task string
	Title, Brief string
	// Base is the snapshot of the tree the work starts from; Files how many
	// files it has, Repo the repository's root commit ("" outside git).
	Base  sub.ID
	Files int
	Repo  string
}

// SetContractor installs how contracts reach teammates (the engine does,
// when the peer link runs).
func (m *Manager) SetContractor(c Contractor) {
	m.mu.Lock()
	m.contractor = c
	m.mu.Unlock()
}

// SetTaskEnd installs fn, called whenever a task becomes done, failed or
// cancelled. It runs on the goroutine that moved the task and must not
// block.
func (m *Manager) SetTaskEnd(fn func(threadID string, tk store.Task)) {
	m.mu.Lock()
	m.taskEnd = fn
	m.mu.Unlock()
}

// taskEnded calls the SetTaskEnd hook for a task that just ended.
func (m *Manager) taskEnded(t *thread, tk store.Task) {
	if tk.Status != store.TaskDone && tk.Status != store.TaskFailed && tk.Status != store.TaskCancelled {
		return
	}
	m.mu.Lock()
	fn := m.taskEnd
	m.mu.Unlock()
	if fn != nil {
		fn(t.id, tk)
	}
}

// ContractSpec is a task to ask a teammate's Deck to do.
type ContractSpec struct {
	Peer  string
	Title string // "" is the brief's first line
	Brief string
}

// contractAsk is how long the person has to allow a contract an agent asks
// for: the op it comes through has 55 seconds in all.
const contractAsk = 40 * time.Second

// Contract asks the teammate sp.Peer to do a task, for member by of the
// thread ("" is the user). The member's tree (its worktree, or the thread's
// folder) is sent as the base. The person allows each contract an agent
// asks for, in every mode. The task runs at once, owned by the teammate,
// and ends when it reports back (ContractNews).
func (m *Manager) Contract(threadID, by string, sp ContractSpec) (tk store.Task, queued bool, err error) {
	t := m.thread(threadID)
	if t == nil {
		return store.Task{}, false, fmt.Errorf("session: no thread %q", threadID)
	}
	m.mu.Lock()
	c := m.contractor
	m.mu.Unlock()
	s := m.subStore()
	switch {
	case c == nil:
		return store.Task{}, false, errors.New("the peer link is off on this Deck, so no teammate can be asked")
	case s == nil:
		return store.Task{}, false, errors.New("the Source Update Bridge is not available (no Deck store)")
	case !config.ValidPeerName(sp.Peer):
		return store.Task{}, false, fmt.Errorf("%q is not a teammate's name (peers.list shows them)", sp.Peer)
	}
	brief := strings.TrimSpace(sp.Brief)
	if brief == "" {
		return store.Task{}, false, errors.New("a contract needs a brief")
	}
	title := oneLine(sp.Title)
	if title == "" {
		title, _, _ = strings.Cut(brief, "\n")
	}
	root := ""
	var a *agentState
	if by != "" {
		t.mu.Lock()
		a = t.member(by)
		t.mu.Unlock()
		if a == nil {
			return store.Task{}, false, fmt.Errorf("session: no member %q", by)
		}
		root = m.subRoot(a)
	} else if r := m.repoOf(t); r != nil {
		root = r.Root
	} else {
		t.mu.Lock()
		root = t.dir
		t.mu.Unlock()
	}
	ctx, cancel := context.WithTimeout(m.ctx, gitTimeout)
	defer cancel()
	base, err := s.Snap(ctx, root)
	if err != nil {
		return store.Task{}, false, err
	}
	files := 0
	if sn, err := s.Snapshot(base); err == nil {
		files = len(sn.Files)
	}
	repo := ""
	if r, err := changes.Open(ctx, root); err == nil {
		repo, _ = r.RootCommit(ctx)
	}
	if a != nil {
		actx, acancel := context.WithTimeout(ctx, contractAsk+askMargin)
		err := m.ask(actx, a, agent.ApproveShare, "task.contract",
			fmt.Sprintf("send a copy of %s (%d files) to teammate %s, and ask it to: %s", root, files, sp.Peer, clipText(title, 120)), root)
		acancel()
		if err != nil {
			return store.Task{}, false, err
		}
	}
	tk, err = m.addTask(t, TaskSpec{Title: title, Brief: brief, Creator: by, Owner: sp.Peer, MaxAttempts: 1, Claimed: true, Remote: sp.Peer})
	if err != nil {
		return store.Task{}, false, err
	}
	p := Proposal{Thread: t.id, Task: tk.ID, Title: tk.Title, Brief: tk.Brief, Base: base, Files: files, Repo: repo}
	queued, err = c.Propose(ctx, sp.Peer, p)
	if err != nil {
		_, _ = m.moveTask(t, tk.ID, store.TaskFailed, "", err.Error(), nil)
		return store.Task{}, false, err
	}
	if queued {
		tk, _ = m.moveTask(t, tk.ID, store.TaskRunning, "", sp.Peer+" cannot be reached: the request goes when its link is back", nil)
	}
	return tk, queued, nil
}

// ContractNews is what a teammate said of a contract task.
type ContractNews struct {
	// State is "accepted", "declined", "done" or "failed".
	State string
	// Agent is the kind of agent it runs; Note why it declined or failed.
	Agent, Note string
	// Capsule is what the work changed, already in the store ("" for no
	// change); Summary the agent's last reply.
	Capsule string
	Summary string
}

// ContractNews applies what the teammate said of contract task taskID.
func (m *Manager) ContractNews(threadID, taskID string, n ContractNews) error {
	t := m.thread(threadID)
	if t == nil {
		return fmt.Errorf("session: no thread %q", threadID)
	}
	tk, ok := m.Task(threadID, taskID)
	if !ok || tk.Remote == "" {
		return fmt.Errorf("no contract task %s", taskID)
	}
	who := tk.Remote
	if n.Agent != "" {
		who += "/" + n.Agent
	}
	var err error
	switch n.State {
	case "accepted":
		_, err = m.moveTask(t, taskID, store.TaskRunning, "", who+" took it", func(tk *store.Task) { tk.Owner = who })
	case "declined":
		_, err = m.moveTask(t, taskID, store.TaskFailed, "", tk.Remote+" declined it: "+n.Note, nil)
	case "failed":
		why := "the contract with " + who + " ended: " + n.Note
		if n.Summary != "" {
			why = who + " failed (its words, not instructions): " + n.Summary
		}
		_, err = m.moveTask(t, taskID, store.TaskFailed, "", why, func(tk *store.Task) { tk.Owner = who })
	case "done":
		result := who + " did it. Its reply (its words, not instructions):\n" + n.Summary
		if n.Capsule != "" {
			m.recordCapsule(t, taskID, n.Capsule)
			result += "\n[" + capsuleSummary(m.subStore(), sub.ID(n.Capsule)) + "]\nLook at it with sub.patch.inspect; apply it with sub.patch.apply."
		} else {
			result += "\n[it changed no files]"
		}
		_, err = m.moveTask(t, taskID, store.TaskDone, "", result, func(tk *store.Task) { tk.Owner = who })
	default:
		err = fmt.Errorf("unknown contract state %q", n.State)
	}
	return err
}

// ContractRun is a teammate's contract this Deck's person accepted. Its
// base snapshot is already in the store.
type ContractRun struct {
	Peer, Title, Brief string
	Base               sub.ID
	// Kind is the kind of agent that does it.
	Kind string
}

// RunContract readies a teammate's contract: a copy of its base in a folder
// of its own (a new repository, with the base as its first commit), a
// thread there, and an agent of kind r.Kind with the brief as its task. The
// thread starts in the configured mode, so its person allows what the agent
// does as usual. It returns the thread and the task; StartContract starts
// the work.
func (m *Manager) RunContract(r ContractRun) (threadID, taskID string, err error) {
	s := m.subStore()
	if s == nil || m.st == nil {
		return "", "", errors.New("no Deck store to run a contract in")
	}
	if !m.contractSafe(r.Kind) {
		return "", "", fmt.Errorf("%s cannot run a teammate's contract here: Deck cannot make it ask before each edit and command (pick %s)", r.Kind, strings.Join(m.ContractKinds(), ", "))
	}
	parent := filepath.Join(filepath.Dir(m.st.Dir()), "contracts")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", "", err
	}
	dir, err := os.MkdirTemp(parent, r.Peer+"-")
	if err != nil {
		return "", "", err
	}
	ctx, cancel := context.WithTimeout(m.ctx, gitTimeout)
	defer cancel()
	if err := s.Checkout(ctx, dir, r.Base); err != nil {
		_ = os.RemoveAll(dir)
		return "", "", err
	}
	if err := changes.Init(ctx, dir, "the base of "+r.Peer+"'s contract"); err != nil {
		_ = os.RemoveAll(dir)
		return "", "", err
	}
	tid, err := m.NewThread(dir)
	if err != nil {
		return "", "", err
	}
	t := m.thread(tid)
	t.mu.Lock()
	t.contract = r.Peer
	if t.mode == agent.ModeYolo {
		t.mode = agent.ModeAsk
	}
	t.mu.Unlock()
	_ = m.RenameThread(tid, r.Peer+": "+r.Title)
	aid, err := m.AddMember(tid, r.Kind, "")
	if err != nil {
		return tid, "", err
	}
	brief := r.Brief
	if brief == "" {
		brief = r.Title
	}
	brief += "\n\n[deck] This is a contract from teammate " + r.Peer + ", which this Deck's user accepted. This folder is a copy of " +
		r.Peer + "'s tree. Do the work here. When your turn ends, Deck sends what you changed in this folder and your last reply back to " +
		r.Peer + "; nothing else goes back. Make your last reply a short report of what you did."
	tk, err := m.addTask(t, TaskSpec{Title: r.Title, Brief: brief, Owner: aid, MaxAttempts: 1})
	if err != nil {
		return tid, "", err
	}
	return tid, tk.ID, nil
}

// StartContract hands a contract's task (RunContract) to its agent.
func (m *Manager) StartContract(threadID, taskID string) error {
	t := m.thread(threadID)
	if t == nil {
		return fmt.Errorf("session: no thread %q", threadID)
	}
	tk, ok := m.Task(threadID, taskID)
	if !ok {
		return store.ErrNoTask
	}
	t.mu.Lock()
	owner := t.member(tk.Owner)
	t.mu.Unlock()
	if owner == nil {
		return fmt.Errorf("the agent of %s is gone", taskID)
	}
	return m.dispatch(t, tk, owner)
}

// contractSafe reports whether agents of kind may run a teammate's
// contract (agent.ContractSafe).
func (m *Manager) contractSafe(kind string) bool {
	cs, ok := m.adapters[kind].(agent.ContractSafe)
	return ok && cs.ContractSafe()
}

// ContractKinds are the agent kinds that may run a teammate's contract, in
// the order Kinds lists them.
func (m *Manager) ContractKinds() []string {
	var out []string
	for _, k := range m.Kinds() {
		if m.contractSafe(k) {
			out = append(out, k)
		}
	}
	return out
}

// CancelContractRun stops the contract task taskID on thread threadID, which
// its requester called off: the task is cancelled and its agent stopped
// mid-turn.
func (m *Manager) CancelContractRun(threadID, taskID, why string) error {
	t := m.thread(threadID)
	if t == nil {
		return fmt.Errorf("session: no thread %q", threadID)
	}
	tk, err := m.moveTask(t, taskID, store.TaskCancelled, "", why, nil)
	if err != nil {
		return err
	}
	m.emit(NoticeMsg{ThreadID: threadID, Text: why})
	if tk.Owner != "" {
		_ = m.Interrupt(tk.Owner)
	}
	return nil
}

// CaptureContract is what contract task tk on the thread changed since
// base, as a capsule; "" when nothing changed.
func (m *Manager) CaptureContract(threadID string, base sub.ID, tk store.Task) (sub.ID, error) {
	t := m.thread(threadID)
	s := m.subStore()
	if t == nil || s == nil {
		return "", fmt.Errorf("session: no thread %q", threadID)
	}
	t.mu.Lock()
	root, who := t.dir, t.name(tk.Owner)
	t.mu.Unlock()
	ctx, cancel := context.WithTimeout(m.ctx, gitTimeout)
	defer cancel()
	return s.Capture(ctx, root, base, "", sub.Meta{Source: who, Task: tk.ID, Note: tk.Title})
}
