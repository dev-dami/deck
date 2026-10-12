package store

import (
	"errors"
	"testing"
)

func TestCanTransition(t *testing.T) {
	ok := [][2]TaskStatus{
		{TaskPending, TaskReady}, {TaskReady, TaskRunning}, {TaskRunning, TaskDone}, {TaskRunning, TaskFailed},
		{TaskFailed, TaskReady}, {TaskBlocked, TaskPending}, {TaskPending, TaskCancelled}, {TaskRunning, TaskRunning},
	}
	for _, c := range ok {
		if !CanTransition(c[0], c[1]) {
			t.Errorf("%s -> %s should be allowed", c[0], c[1])
		}
	}
	bad := [][2]TaskStatus{
		{TaskDone, TaskRunning}, {TaskDone, TaskPending}, {TaskCancelled, TaskReady},
		{TaskPending, TaskFailed}, {TaskReady, TaskFailed}, {TaskBlocked, TaskDone},
	}
	for _, c := range bad {
		if CanTransition(c[0], c[1]) {
			t.Errorf("%s -> %s should be refused", c[0], c[1])
		}
	}
}

func TestStatusKinds(t *testing.T) {
	if !TaskDone.Final() || !TaskCancelled.Final() || TaskFailed.Final() {
		t.Error("done and cancelled are final; failed can be retried")
	}
	if !TaskFailed.Settled() || TaskRunning.Settled() {
		t.Error("failed is settled, running is not")
	}
	if TaskStatus("nope").Valid() || !TaskReady.Valid() {
		t.Error("Valid")
	}
}

func TestNextTaskIDAndFind(t *testing.T) {
	tasks := []Task{{ID: "t1"}, {ID: "t7"}, {ID: "custom"}}
	if got := NextTaskID(tasks); got != "t8" {
		t.Errorf("NextTaskID = %q", got)
	}
	if got := NextTaskID(nil); got != "t1" {
		t.Errorf("NextTaskID(nil) = %q", got)
	}
	if FindTask(tasks, " T7 ") != 1 || FindTask(tasks, "t2") != -1 {
		t.Error("FindTask is case-insensitive and exact")
	}
}

func TestValidateDeps(t *testing.T) {
	tasks := []Task{{ID: "t1"}, {ID: "t2", Deps: []string{"t1"}}, {ID: "t3", Deps: []string{"t2"}}}
	if err := ValidateDeps(tasks, "t4", []string{"t3", "t1"}); err != nil {
		t.Errorf("fine deps: %v", err)
	}
	if err := ValidateDeps(tasks, "t4", []string{"nope"}); !errors.Is(err, ErrTaskUnknownDep) {
		t.Errorf("unknown dep: %v", err)
	}
	if err := ValidateDeps(tasks, "t1", []string{"t3"}); !errors.Is(err, ErrTaskCycle) {
		t.Errorf("t1 after t3 closes a cycle: %v", err)
	}
	if err := ValidateDeps(tasks, "t2", []string{"t2"}); !errors.Is(err, ErrTaskCycle) {
		t.Errorf("self dependency: %v", err)
	}
}

func TestTaskDefaults(t *testing.T) {
	if (Task{}).Tries() != DefaultMaxAttempts || (Task{MaxAttempts: 5}).Tries() != 5 {
		t.Error("Tries")
	}
	if (Task{}).FailPolicy() != OnFailBlock || (Task{OnFail: "cancel"}).FailPolicy() != OnFailCancel || (Task{OnFail: "bogus"}).FailPolicy() != OnFailBlock {
		t.Error("FailPolicy")
	}
}
