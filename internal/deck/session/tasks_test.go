package session

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/deck/store"
)

// taskRig is a thread with one claude and one codex member, neither busy.
func taskRig(t *testing.T) (r *rig, tid, claude, codex string) {
	t.Helper()
	r = newRig(t, nil, nil)
	tid = r.thread(t)
	claude = r.add(t, tid, "claude")
	codex = r.add(t, tid, "codex")
	return r, tid, claude, codex
}

func execOK(t *testing.T, r *rig, agent, op, args string) string {
	t.Helper()
	out, isErr := r.hub.Exec(agent, op, json.RawMessage(args))
	if isErr {
		t.Fatalf("%s %s: %s", agent, op, out)
	}
	return out
}

func execErr(t *testing.T, r *rig, agent, op, args string) string {
	t.Helper()
	out, isErr := r.hub.Exec(agent, op, json.RawMessage(args))
	if !isErr {
		t.Fatalf("%s %s succeeded: %s", agent, op, out)
	}
	return out
}

func TestTaskOpsBuildABoardWithDependencies(t *testing.T) {
	r, tid, claude, codex := taskRig(t)
	execOK(t, r, claude, "task.create", `{"title":"write the parser"}`)
	execOK(t, r, claude, "task.create", `{"title":"test the parser","deps":"t1","owner":"codex"}`)

	tasks := r.m.Tasks(tid)
	if len(tasks) != 2 || tasks[0].ID != "t1" || tasks[1].ID != "t2" {
		t.Fatalf("tasks = %+v", tasks)
	}
	if tasks[0].Status != store.TaskReady || tasks[1].Status != store.TaskPending {
		t.Fatalf("t1 has no deps so it is ready, t2 waits: %s, %s", tasks[0].Status, tasks[1].Status)
	}
	if tasks[1].Owner != codex || tasks[1].Creator != claude {
		t.Fatalf("t2 owner/creator = %q/%q", tasks[1].Owner, tasks[1].Creator)
	}

	execOK(t, r, claude, "task.update", `{"id":"t1","status":"running"}`)
	if got := r.m.Tasks(tid)[0]; got.Owner != claude || got.Attempts != 1 {
		t.Fatalf("starting claims an unowned task: %+v", got)
	}
	execOK(t, r, claude, "task.done", `{"id":"t1","result":"parser in parse.go"}`)
	tasks = r.m.Tasks(tid)
	if tasks[0].Status != store.TaskDone || tasks[0].Result != "parser in parse.go" {
		t.Fatalf("t1 = %+v", tasks[0])
	}
	if tasks[1].Status != store.TaskReady {
		t.Fatalf("t2 should be ready once t1 is done, is %s", tasks[1].Status)
	}
	list := execOK(t, r, codex, "task.list", `{"status":"ready"}`)
	if !strings.Contains(list, "t2 [ready] codex after t1: test the parser") || strings.Contains(list, "t1 [") {
		t.Fatalf("list = %q", list)
	}
}

func TestTaskOpsRefuseWhatTheBoardForbids(t *testing.T) {
	r, _, claude, codex := taskRig(t)
	execOK(t, r, claude, "task.create", `{"title":"a"}`)
	execOK(t, r, claude, "task.create", `{"title":"b","deps":"t1"}`)

	if out := execErr(t, r, claude, "task.update", `{"id":"t1","deps":"t2"}`); !strings.Contains(out, "depend on each other") {
		t.Errorf("cycle: %s", out)
	}
	if out := execErr(t, r, claude, "task.create", `{"title":"c","deps":"t9"}`); !strings.Contains(out, "unknown dependency") {
		t.Errorf("unknown dep: %s", out)
	}
	if out := execErr(t, r, claude, "task.update", `{"id":"t1","status":"done"}`); !strings.Contains(out, "task.done") {
		t.Errorf("agents finish through task.done: %s", out)
	}
	// codex cannot finish a task that claude owns and created.
	execOK(t, r, claude, "task.update", `{"id":"t1","status":"running"}`)
	if out := execErr(t, r, codex, "task.done", `{"id":"t1"}`); !strings.Contains(out, "belongs to someone else") {
		t.Errorf("ownership: %s", out)
	}
	execOK(t, r, claude, "task.done", `{"id":"t1","result":"ok"}`)
	if out := execErr(t, r, claude, "task.fail", `{"id":"t1"}`); !strings.Contains(out, "cannot move") {
		t.Errorf("a done task stays done: %s", out)
	}
}

func TestTaskBoardSurvivesArchiveAndResume(t *testing.T) {
	r, tid, claude, _ := taskRig(t)
	execOK(t, r, claude, "task.create", `{"title":"keep me","brief":"details"}`)
	execOK(t, r, claude, "task.update", `{"id":"t1","status":"running"}`)
	r.m.Flush()
	th := stored(t, r, tid)
	if len(th.Tasks) != 1 || th.Tasks[0].Status != store.TaskRunning || th.Tasks[0].Brief != "details" {
		t.Fatalf("stored tasks = %+v", th.Tasks)
	}
	entries, _, err := r.st.LoadEntries(tid)
	if err != nil {
		t.Fatal(err)
	}
	evs := 0
	for _, e := range entries {
		if e.Type == store.EntryTask && e.Task != nil {
			evs++
		}
	}
	if evs < 3 { // created, ready, running
		t.Fatalf("logged %d task events, want at least 3", evs)
	}
	if err := r.m.Archive(tid); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.m.Resume(tid); err != nil {
		t.Fatal(err)
	}
	got := r.m.Tasks(tid)
	if len(got) != 1 || got[0].Title != "keep me" || got[0].Status != store.TaskRunning {
		t.Fatalf("resumed tasks = %+v", got)
	}
	if tk, err := r.m.CreateTask(tid, TaskSpec{Title: "next"}); err != nil || tk.ID != "t2" {
		t.Fatalf("ids go on after a resume: %+v %v", tk, err)
	}
}

func TestTaskChangesAreAnnouncedAndBriefed(t *testing.T) {
	r, _, claude, codex := taskRig(t)
	execOK(t, r, claude, "task.create", `{"title":"ship it","owner":"codex"}`)
	waitFor(t, "TaskChangedMsg", func() bool {
		return r.c.count(func(m tea.Msg) bool { tm, ok := m.(TaskChangedMsg); return ok && tm.Task.ID == "t1" && tm.From == "" }) == 1
	})
	br := r.m.readThread(codex)
	if !strings.Contains(br, `task t1 "ship it" created`) {
		t.Fatalf("thread.read does not show the task:\n%s", br)
	}
}
