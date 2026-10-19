package workspace

import (
	"errors"
	"strings"
	"time"
)

// What the UI shows and does for the shared task board of a session's thread
// (internal/deck/session/tasks.go). The UI type-asserts its Workspace to
// Orchestration, like it does to Activity.

// BoardTask is one task of the board, ready to draw.
type BoardTask struct {
	ID, Title string
	Status    string // pending, ready, running, blocked, done, failed, cancelled
	Owner     string // the owner's name in the thread; "" for nobody
	Deps      []string
	Brief     string
	Result    string
	Error     string
	Attempts  int
	Tries     int    // how many attempts the task gets
	Branch    string // the worktree branch of its subagent, when isolated
	Updated   time.Time
}

// BoardReport is a thread's task board.
type BoardReport struct {
	// Orchestrate is on while the scheduler runs ready tasks and members may
	// start subagents.
	Orchestrate bool
	Tasks       []BoardTask
}

// Open counts the tasks that are not done or cancelled.
func (r BoardReport) Open() int {
	n := 0
	for _, t := range r.Tasks {
		if t.Status != "done" && t.Status != "cancelled" {
			n++
		}
	}
	return n
}

// Orchestration reads and steers a session's task board.
type Orchestration interface {
	Board(sessionID string) BoardReport
	// CancelTask abandons a task, stopping its subagent.
	CancelTask(sessionID, taskID string) error
	// RetryTask puts a failed or blocked task back to ready with a fresh set
	// of attempts.
	RetryTask(sessionID, taskID string) error
	// SteerTask redirects whoever owns a task, mid-turn.
	SteerTask(sessionID, taskID, text string) error
	// SetOrchestrate turns the scheduler and member-started subagents on or off.
	SetOrchestrate(sessionID string, on bool) error
}

var _ Orchestration = (*DeckWorkspace)(nil)

// Board implements [Orchestration].
func (w *DeckWorkspace) Board(sessionID string) BoardReport {
	th, ok := w.threadOf(sessionID)
	if !ok {
		return BoardReport{}
	}
	names, branches := map[string]string{}, map[string]string{}
	for _, a := range th.Members {
		names[a.ID], branches[a.ID] = a.Name, a.Branch
	}
	rep := BoardReport{Orchestrate: th.Orchestrate}
	for _, tk := range w.mgr().Tasks(th.ID) {
		bt := BoardTask{
			ID: tk.ID, Title: tk.Title, Status: string(tk.Status), Deps: tk.Deps, Brief: tk.Brief,
			Result: tk.Result, Error: tk.Error, Attempts: tk.Attempts, Tries: tk.Tries(), Updated: tk.Updated,
		}
		if tk.Owner != "" {
			bt.Owner = names[tk.Owner]
			if bt.Owner == "" {
				bt.Owner = tk.Owner
			}
		}
		if tk.Run != nil {
			bt.Branch = branches[tk.Run.Member] // "" once applied work is discarded
		}
		rep.Tasks = append(rep.Tasks, bt)
	}
	return rep
}

func (w *DeckWorkspace) boardThread(sessionID string) (string, error) {
	th, ok := w.threadOf(sessionID)
	if !ok {
		return "", errors.New("no agents are running in this session yet")
	}
	return th.ID, nil
}

// CancelTask implements [Orchestration].
func (w *DeckWorkspace) CancelTask(sessionID, taskID string) error {
	tid, err := w.boardThread(sessionID)
	if err != nil {
		return err
	}
	_, err = w.mgr().CancelTask(tid, taskID, "")
	return err
}

// RetryTask implements [Orchestration].
func (w *DeckWorkspace) RetryTask(sessionID, taskID string) error {
	tid, err := w.boardThread(sessionID)
	if err != nil {
		return err
	}
	_, err = w.mgr().RetryTask(tid, taskID, "")
	return err
}

// SteerTask implements [Orchestration].
func (w *DeckWorkspace) SteerTask(sessionID, taskID, text string) error {
	tid, err := w.boardThread(sessionID)
	if err != nil {
		return err
	}
	return w.mgr().SteerTask(tid, taskID, "", strings.TrimSpace(text))
}

// Worktrees acts on the work an isolated subagent did on its own worktree,
// found through the task it ran.
type Worktrees interface {
	// ApplyTask writes what task taskID's subagent changed into the session's
	// tree, as uncommitted edits, whole or not at all, and returns the files.
	ApplyTask(sessionID, taskID string) ([]string, error)
	// DiscardTask removes that worktree and its branch.
	DiscardTask(sessionID, taskID string) error
}

var _ Worktrees = (*DeckWorkspace)(nil)

// taskMember is the subagent with a worktree that ran task taskID.
func (w *DeckWorkspace) taskMember(sessionID, taskID string) (threadID, member string, err error) {
	tid, err := w.boardThread(sessionID)
	if err != nil {
		return "", "", err
	}
	tk, ok := w.mgr().Task(tid, taskID)
	if !ok {
		return "", "", errors.New("no task " + taskID)
	}
	if tk.Run == nil || tk.Run.Branch == "" {
		return "", "", errors.New(taskID + " did not run on a worktree of its own")
	}
	return tid, tk.Run.Member, nil
}

// ApplyTask implements [Worktrees].
func (w *DeckWorkspace) ApplyTask(sessionID, taskID string) ([]string, error) {
	tid, member, err := w.taskMember(sessionID, taskID)
	if err != nil {
		return nil, err
	}
	return w.mgr().ApplyMember(tid, member)
}

// DiscardTask implements [Worktrees].
func (w *DeckWorkspace) DiscardTask(sessionID, taskID string) error {
	tid, member, err := w.taskMember(sessionID, taskID)
	if err != nil {
		return err
	}
	return w.mgr().DiscardMember(tid, member)
}

// SetOrchestrate implements [Orchestration].
func (w *DeckWorkspace) SetOrchestrate(sessionID string, on bool) error {
	tid, err := w.boardThread(sessionID)
	if err != nil {
		return err
	}
	return w.mgr().SetOrchestrate(tid, on)
}
