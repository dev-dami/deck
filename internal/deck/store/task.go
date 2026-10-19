package store

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// A thread's shared task board: what is to be done, who owns it, what it waits
// for and how it ended. The tasks live in the thread's metadata; every change
// is also logged as an EntryTask so the history shows how the work went.

// TaskStatus is where a task is in its life.
type TaskStatus string

const (
	TaskPending   TaskStatus = "pending"   // created; waiting for its dependencies or for someone to start it
	TaskReady     TaskStatus = "ready"     // every dependency is done; can be started
	TaskRunning   TaskStatus = "running"   // an owner is working on it
	TaskBlocked   TaskStatus = "blocked"   // cannot go on: a dependency failed, or it needs the user
	TaskDone      TaskStatus = "done"      // finished; Result says how
	TaskFailed    TaskStatus = "failed"    // ended without success; Error says why. May be retried
	TaskCancelled TaskStatus = "cancelled" // abandoned
)

// Valid reports whether s is a status.
func (s TaskStatus) Valid() bool {
	switch s {
	case TaskPending, TaskReady, TaskRunning, TaskBlocked, TaskDone, TaskFailed, TaskCancelled:
		return true
	}
	return false
}

// Final reports whether no further work will happen on a task in status s
// unless it is reopened by hand: done and cancelled. A failed task is not
// final: it can be retried.
func (s TaskStatus) Final() bool { return s == TaskDone || s == TaskCancelled }

// taskNext lists the statuses each status may move to.
var taskNext = map[TaskStatus][]TaskStatus{
	TaskPending:   {TaskReady, TaskRunning, TaskBlocked, TaskDone, TaskCancelled},
	TaskReady:     {TaskPending, TaskRunning, TaskBlocked, TaskDone, TaskCancelled},
	TaskRunning:   {TaskReady, TaskBlocked, TaskDone, TaskFailed, TaskCancelled},
	TaskBlocked:   {TaskPending, TaskReady, TaskCancelled},
	TaskFailed:    {TaskPending, TaskReady, TaskCancelled},
	TaskDone:      nil,
	TaskCancelled: nil,
}

// CanTransition reports whether a task may move from one status to another.
// Staying where it is counts as a move that is allowed.
func CanTransition(from, to TaskStatus) bool {
	if from == to {
		return true
	}
	for _, s := range taskNext[from] {
		if s == to {
			return true
		}
	}
	return false
}

// What happens to the tasks that wait for one that failed (Task.OnFail).
const (
	OnFailBlock    = "block"    // they become blocked until the failure is dealt with (the default)
	OnFailCancel   = "cancel"   // they are cancelled
	OnFailContinue = "continue" // the failure is ignored and they go on
)

// DefaultMaxAttempts is how many times a task is tried when it says nothing.
const DefaultMaxAttempts = 2

// RunRef says which Deck-managed run is working on a task.
type RunRef struct {
	Member string `json:"member"`           // the subagent's member id
	Branch string `json:"branch,omitempty"` // its worktree branch, when isolated
	Path   string `json:"path,omitempty"`   // its worktree directory
}

// Task is one unit of work on the board. IDs are per thread: "t1", "t2", ...
type Task struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Brief is what the owner is asked to do; empty means the title says it.
	Brief string `json:"brief,omitempty"`
	// Creator and Owner are member ids; "" is the user for Creator, nobody
	// for Owner.
	Creator string `json:"creator,omitempty"`
	Owner   string `json:"owner,omitempty"`
	// Parent is the task this one is part of.
	Parent string `json:"parent,omitempty"`
	// Deps are the tasks that must be done first.
	Deps   []string   `json:"deps,omitempty"`
	Status TaskStatus `json:"status"`
	// Result is what the owner reported when it finished; Error why it failed
	// or is blocked.
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
	// Attempts is how many times the task was started; MaxAttempts how many
	// tries it gets (0 means DefaultMaxAttempts).
	Attempts    int    `json:"attempts,omitempty"`
	MaxAttempts int    `json:"max_attempts,omitempty"`
	OnFail      string `json:"on_fail,omitempty"` // OnFailBlock (default), OnFailCancel or OnFailContinue
	// Contract is what a delegation of the task promised (see thread.send).
	Contract *Contract `json:"contract,omitempty"`
	Run      *RunRef   `json:"run,omitempty"`
	// Capsules are the SUB capsules holding the task's code changes, oldest
	// first (sub.patch.capture, thread.send capsule, task.done capsule).
	Capsules []string `json:"capsules,omitempty"`
	// Isolate runs the task's subagent on its own git worktree.
	Isolate bool `json:"isolate,omitempty"`
	// Race marks a race: each of its subtasks (its entries) does the whole
	// brief on a worktree of its own, and the user keeps one. Judge is the
	// kind of agent that ranks the entries when they end; "" is none.
	Race  bool   `json:"race,omitempty"`
	Judge string `json:"judge,omitempty"`
	// Diff is what a race entry changed on its worktree, recorded when it
	// ended.
	Diff *DiffStat `json:"diff,omitempty"`
	// Remote is the teammate's Deck that does the task under a code
	// contract; Owner then names it, and its agent once one took the task
	// ("vps/claude").
	Remote string `json:"remote,omitempty"`
	// Kind, Model and Effort pick the agent a subagent for the task runs; ""
	// is the default agent and the configured model and effort.
	Kind   string `json:"kind,omitempty"`
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	// Budget is what the task may spend, over the config's default budget;
	// Spent is what its owner and the owner's subagents have spent on it
	// since Started, when it last started running.
	Budget  *Budget   `json:"budget,omitempty"`
	Spent   Spend     `json:"spent,omitzero"`
	Started time.Time `json:"started,omitzero"`
	// BlockedBy is the task whose failure blocked this one (the scheduler);
	// RetryAt is when a failed task is tried again.
	BlockedBy string    `json:"blocked_by,omitempty"`
	RetryAt   time.Time `json:"retry_at,omitempty"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
}

// Budget is what a task may spend; zero fields are no limit (see the config's
// budget for the defaults).
type Budget struct {
	Tokens  int     `json:"tokens,omitempty"`
	CostUSD float64 `json:"cost_usd,omitempty"`
	Minutes int     `json:"minutes,omitempty"`
}

// DiffStat is the size of a change: the files it touches and its added and
// removed lines.
type DiffStat struct {
	Files   []string `json:"files,omitempty"`
	Added   int      `json:"added,omitempty"`
	Removed int      `json:"removed,omitempty"`
}

// Spend is what was spent on a task: tokens (input and output) and the cost
// the CLIs reported.
type Spend struct {
	Tokens  int     `json:"tokens,omitempty"`
	CostUSD float64 `json:"cost_usd,omitempty"`
}

// Contract is the terms a task was handed over under: what to deliver and how
// to tell it is finished.
type Contract struct {
	Expect   string `json:"expect,omitempty"`    // what the delegator expects back
	DoneWhen string `json:"done_when,omitempty"` // the condition that means the task is done
	// ExplicitDone means the owner must report the task with task.done; its
	// last reply alone does not finish it.
	ExplicitDone bool `json:"explicit_done,omitempty"`
}

// Tries is the task's attempt limit.
func (t Task) Tries() int {
	if t.MaxAttempts > 0 {
		return t.MaxAttempts
	}
	return DefaultMaxAttempts
}

// FailPolicy is the task's OnFail, defaulting to OnFailBlock.
func (t Task) FailPolicy() string {
	switch t.OnFail {
	case OnFailCancel, OnFailContinue:
		return t.OnFail
	}
	return OnFailBlock
}

// TaskEvent is the log's record of one change to the board (EntryTask).
type TaskEvent struct {
	ID    string     `json:"id"`
	Title string     `json:"title,omitempty"`
	From  TaskStatus `json:"from,omitempty"` // "" for a task just created
	To    TaskStatus `json:"to"`
	Owner string     `json:"owner,omitempty"`
	// Note is the result, error or reason that went with the change.
	Note string `json:"note,omitempty"`
}

// Errors from the board.
var (
	ErrNoTask          = errors.New("no such task")
	ErrTaskTransition  = errors.New("task cannot move there")
	ErrTaskCycle       = errors.New("tasks would depend on each other")
	ErrTaskUnknownDep  = errors.New("unknown dependency")
	ErrTaskEmptyTitle  = errors.New("a task needs a title")
	ErrTaskUnknownPart = errors.New("unknown parent task")
)

// FindTask returns the index of the task with id in tasks, or -1.
func FindTask(tasks []Task, id string) int {
	id = strings.TrimSpace(id)
	for i := range tasks {
		if strings.EqualFold(tasks[i].ID, id) {
			return i
		}
	}
	return -1
}

// NextTaskID is the id for the next task of a board: one past the highest
// "t<n>" in use.
func NextTaskID(tasks []Task) string {
	n := 0
	for _, t := range tasks {
		if v, err := strconv.Atoi(strings.TrimPrefix(strings.ToLower(t.ID), "t")); err == nil && v > n {
			n = v
		}
	}
	return fmt.Sprintf("t%d", n+1)
}

// DependsOn reports whether the task id depends on target, directly or through
// other tasks.
func DependsOn(tasks []Task, id, target string) bool {
	seen := map[string]bool{}
	var walk func(string) bool
	walk = func(cur string) bool {
		if seen[cur] {
			return false
		}
		seen[cur] = true
		i := FindTask(tasks, cur)
		if i < 0 {
			return false
		}
		for _, d := range tasks[i].Deps {
			if strings.EqualFold(d, target) || walk(d) {
				return true
			}
		}
		return false
	}
	return walk(id)
}

// ValidateDeps checks the dependencies a task would have: each must exist, and
// none may lead back to the task (a cycle). self may not exist on the board
// yet (a task being created).
func ValidateDeps(tasks []Task, self string, deps []string) error {
	for _, d := range deps {
		if FindTask(tasks, d) < 0 {
			return fmt.Errorf("%w %q", ErrTaskUnknownDep, d)
		}
		if strings.EqualFold(d, self) || DependsOn(tasks, d, self) {
			return fmt.Errorf("%w (%s and %s)", ErrTaskCycle, self, d)
		}
	}
	return nil
}
