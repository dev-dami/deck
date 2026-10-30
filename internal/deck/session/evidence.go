package session

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/store"
)

// Done needs evidence (issue #103). A task can name a check, the command
// whose passing run proves the work done ("go test ./pkg/x"): on task
// creation, in agent.spawn, or in a thread.send hand-off's contract. Deck
// watches every member's tool results: each edit, and each command it ran
// with how it exited. Such a task becomes done only when the newest run of
// its check by the people doing the work (its owner, its subagent, whoever
// reports it) since their last edit exited 0. Without that, task.done (or
// the reply that would settle a hand-off) moves it to review instead, where
// Deck itself is the reviewer: it sends the task back to its owner with
// "no passing run of <check> since the last change". The rounds are bounded
// like a reviewer's: after store.MaxReviewChanges send-backs the task is
// blocked for the user. Tasks without a check behave as before; the user
// can always move a task by hand.
//
// The verdict is taken when the turn of whoever reports the task ends, not
// when task.done is called: an adapter's events reach Deck through a buffer,
// so the op can overtake the result of the command run just before it, and
// an edit made after task.done must count too. At the turn's end every
// event of the turn has been read (turnEndOf runs on the member's pump, at
// its turn_done), so task.done records a claim, the turn's end weighs it,
// and afterTurn moves the task (settleClaims).

// checkReviewer is the Reviewer of a task in review for want of evidence:
// Deck itself, not a member, so no reviewer subagent is started for it
// (startReview skips a task with a reviewer).
const checkReviewer = "deck:check"

// maxRuns is how many command runs Deck remembers per member.
const maxRuns = 32

// flowState is a thread's record of its members' work since their last
// edits (evidence.go) and of what their running turns did (turnend.go),
// guarded by thread.mu.
type flowState struct {
	// seq orders the edits and runs of every member of the thread.
	seq  uint64
	work map[string]*workLog
	ends map[string]turnFacts
	// claims are the task.done calls on checked tasks that each member's
	// running turn made, by member; verified the verdicts taken at the end
	// of a turn for the tasks it settles, by task id, which the move to done
	// then uses (gateDoneLocked).
	claims   map[string][]doneClaim
	verified map[string]bool
}

// doneClaim is a task.done on a task with a check, waiting for the end of
// its caller's turn. ok is the verdict, set then.
type doneClaim struct {
	task, result string
	add          func(*store.Task) // the capsule it reported, if any
	ok           bool
}

// workLog is one member's last edit and its command runs since about then,
// oldest first.
type workLog struct {
	edit uint64
	runs []checkRun
}

// checkRun is one command a member ran and whether it exited 0.
type checkRun struct {
	cmd string
	seq uint64
	ok  bool
}

// noteWork records an edit or a finished command of a's in its thread's
// work log. Events of a CLI-native subagent never get here (onSubagent).
func (m *Manager) noteWork(t *thread, a *agentState, ev agent.Event) {
	edit, cmd, ok, seen := workOf(ev)
	if !seen {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	f := &t.flow
	if f.work == nil {
		f.work = map[string]*workLog{}
	}
	w := f.work[a.id]
	if w == nil {
		w = &workLog{}
		f.work[a.id] = w
	}
	f.seq++
	if edit {
		w.edit = f.seq
		w.runs = w.runs[:0] // runs before an edit prove nothing about it
		return
	}
	w.runs = append(w.runs, checkRun{cmd: cmd, seq: f.seq, ok: ok})
	if len(w.runs) > maxRuns {
		w.runs = append(w.runs[:0], w.runs[len(w.runs)-maxRuns:]...)
	}
}

// workOf reads one event: an edit (a file change, or an edit tool that
// succeeded), or a command that finished, with whether it exited 0 (the
// adapters report a non-zero exit as a tool error). seen is false for
// every other event.
func workOf(ev agent.Event) (edit bool, cmd string, ok, seen bool) {
	switch {
	case ev.Delta:
	case ev.Kind == agent.KindFileChange && ev.Diff != nil:
		return true, "", false, true
	case ev.Kind == agent.KindToolResult && ev.Tool != nil:
		switch tl := ev.Tool; {
		case isEditTool(tl.Name) && tl.Status == agent.ToolOK:
			return true, "", false, true
		case isCommandTool(tl.Name) && tl.Status != agent.ToolRunning:
			if c := commandOf(tl); c != "" {
				return false, c, tl.Status == agent.ToolOK, true
			}
		}
	}
	return false, "", false, false
}

// commandOf is the command line a command tool ran: its input's "command"
// (a string, or an argv), else its title.
func commandOf(tl *agent.Tool) string {
	var in struct {
		Command json.RawMessage `json:"command"`
		Cmd     json.RawMessage `json:"cmd"`
	}
	if json.Unmarshal(tl.Input, &in) == nil {
		for _, raw := range []json.RawMessage{in.Command, in.Cmd} {
			var s string
			if json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != "" {
				return s
			}
			var argv []string
			if json.Unmarshal(raw, &argv) == nil && len(argv) > 0 {
				return strings.Join(argv, " ")
			}
		}
	}
	return tl.Title
}

// checkMatches reports whether command line cmd runs check in a way its
// exit code speaks for: the check's words appear in it, ending at a word
// boundary (flags may follow), and nothing after them can hide a failure
// (a pipe, "||", ";" or "&"; "&&" is fine), nor a "!" before them invert it.
func checkMatches(cmd, check string) bool {
	c := strings.Join(strings.Fields(check), " ")
	x := strings.Join(strings.Fields(cmd), " ")
	if c == "" {
		return false
	}
	for from := 0; ; {
		i := strings.Index(x[from:], c)
		if i < 0 {
			return false
		}
		i += from
		from = i + 1
		if i > 0 && !strings.ContainsRune(" ;&|('\"", rune(x[i-1])) {
			continue // in the middle of a word: "xgo test"
		}
		rest := x[i+len(c):]
		if rest != "" && !strings.ContainsRune(" ;&|)'\"", rune(rest[0])) {
			continue // "go test ./pkg/x" is not "go test ./pkg/xyz"
		}
		if strings.HasSuffix(strings.TrimSpace(x[:i]), "!") {
			return false
		}
		tail := strings.ReplaceAll(rest, "&&", "")
		return !strings.ContainsAny(tail, "|;&")
	}
}

// evidenceLocked reports whether task tk's check has a passing run since the
// last change: among its workers (its owner, its subagent, and by, who
// reports it), the newest run of the check after the newest edit of any of
// them exited 0. t.mu is held.
func (t *thread) evidenceLocked(tk store.Task, by string) bool {
	check := tk.CheckOf()
	if check == "" {
		return true
	}
	workers := []string{tk.Owner, by}
	if tk.Run != nil {
		workers = append(workers, tk.Run.Member)
	}
	var edit uint64
	for _, id := range workers {
		if w := t.flow.work[id]; id != "" && w != nil {
			edit = max(edit, w.edit)
		}
	}
	var last *checkRun
	for _, id := range workers {
		w := t.flow.work[id]
		if id == "" || w == nil {
			continue
		}
		for k := range w.runs {
			r := &w.runs[k]
			if r.seq > edit && checkMatches(r.cmd, check) && (last == nil || r.seq > last.seq) {
				last = r
			}
		}
	}
	return last != nil && last.ok
}

// verifiedLocked is the verdict on tk's evidence: the one the end of a turn
// took for it, used once, else the evidence as it stands. t.mu is held.
func (t *thread) verifiedLocked(tk store.Task, by string) bool {
	if ok, taken := t.flow.verified[tk.ID]; taken {
		delete(t.flow.verified, tk.ID)
		return ok
	}
	return t.evidenceLocked(tk, by)
}

// claimDone is task.done on a task with a check by member a: it is weighed
// when a's turn ends (settleClaims). A later claim on the same task in the
// same turn replaces the earlier one. It returns what the op answers.
func (m *Manager) claimDone(a *agentState, tk store.Task, result string, add func(*store.Task)) string {
	t := a.thread
	t.mu.Lock()
	if t.flow.claims == nil {
		t.flow.claims = map[string][]doneClaim{}
	}
	cs := t.flow.claims[a.id]
	for i := range cs {
		if cs[i].task == tk.ID {
			cs = append(cs[:i], cs[i+1:]...)
			break
		}
	}
	t.flow.claims[a.id] = append(cs, doneClaim{task: tk.ID, result: result, add: add})
	t.mu.Unlock()
	check := tk.CheckOf()
	return fmt.Sprintf("noted: %s is done once Deck sees a passing run of `%s` after your last change; it looks when your turn ends. Run it now if you have not; without one the task comes back to you.", tk.ID, check)
}

// weighClaimsLocked takes the verdicts on a's claims and on the checked task
// its reply settles (settles, "" for none), at the end of its turn, when
// every event of the turn has been read. It returns the claims, each with
// its verdict, for settleClaims, and records the verdict on the settled task
// for the move settleHanded makes; it reports that verdict. t.mu is held.
func (t *thread) weighClaimsLocked(a *agentState, settles string) ([]doneClaim, bool) {
	cs := t.flow.claims[a.id]
	delete(t.flow.claims, a.id)
	for i := range cs {
		if k := store.FindTask(t.tasks, cs[i].task); k >= 0 {
			cs[i].ok = t.evidenceLocked(t.tasks[k], a.id)
		}
	}
	k := store.FindTask(t.tasks, settles)
	if settles == "" || k < 0 {
		return cs, false
	}
	ok := t.evidenceLocked(t.tasks[k], a.id)
	if t.tasks[k].CheckOf() != "" {
		if t.flow.verified == nil {
			t.flow.verified = map[string]bool{}
		}
		t.flow.verified[settles] = ok
	}
	return cs, ok
}

// settleClaims moves the tasks a's turn reported done with a check: to done
// when the turn's end found the evidence, else to review and back to their
// owner (gateDoneLocked). It runs on the thread's relay worker, before
// settleHanded.
func (m *Manager) settleClaims(t *thread, a *agentState, cs []doneClaim) {
	for _, c := range cs {
		t.mu.Lock()
		if t.flow.verified == nil {
			t.flow.verified = map[string]bool{}
		}
		t.flow.verified[c.task] = c.ok
		t.mu.Unlock()
		if _, err := m.moveTask(t, c.task, store.TaskDone, a.id, c.result, c.add); err != nil {
			t.mu.Lock()
			delete(t.flow.verified, c.task) // the move did not use it
			t.mu.Unlock()
		}
	}
}

// forgetVerdict drops the verdict the end of a turn took for task id, once
// the move it was for has had its chance: a later move weighs afresh.
func (m *Manager) forgetVerdict(t *thread, id string) {
	if id == "" {
		return
	}
	t.mu.Lock()
	delete(t.flow.verified, id)
	t.mu.Unlock()
}

// noEvidence is the note of a task sent back for want of evidence.
func noEvidence(check string) string {
	return fmt.Sprintf("no passing run of `%s` since the last change", check)
}

// gateDoneLocked holds back a move of tk to done that lacks the evidence its
// check asks for: it returns the status the task moves to instead (review,
// or blocked once store.MaxReviewChanges send-backs are spent), its note,
// and whether Deck must send the task back to its owner (sendBack). A move
// by the user or Deck (by ""), one out of a reviewer's review, and a task
// with no check pass unchanged. t.mu is held.
func (m *Manager) gateDoneLocked(t *thread, tk *store.Task, from store.TaskStatus, by, note string) (store.TaskStatus, string, bool) {
	check := tk.CheckOf()
	work := from == store.TaskRunning || from == store.TaskReview && tk.Reviewer == checkReviewer
	if check == "" || by == "" {
		return store.TaskDone, note, false
	}
	if ok := t.verifiedLocked(*tk, by); ok || !work {
		return store.TaskDone, note, false
	}
	why := noEvidence(check)
	if tk.Reviews >= store.MaxReviewChanges {
		why = fmt.Sprintf("review did not converge: after %d rounds, still %s", tk.Reviews, why)
		return store.TaskBlocked, strings.TrimSpace(note + "\n[" + why + "]"), false
	}
	tk.Reviewer = checkReviewer
	return store.TaskReview, strings.TrimSpace(note + "\n[" + why + "]"), true
}

// sendBack returns task tk, which went to review for want of evidence, to
// its owner: the task runs again, and the owner gets a note (a relay from
// Deck tied to the task, so its next reply settles it again, settleHanded)
// to run the check after its last change. An owner that has stopped leaves
// the task blocked for the user.
func (m *Manager) sendBack(t *thread, tk store.Task) {
	check := tk.CheckOf()
	t.mu.Lock()
	owner := t.member(tk.Owner)
	t.mu.Unlock()
	if owner == nil || !owner.alive() {
		_, _ = m.moveTask(t, tk.ID, store.TaskBlocked, "", noEvidence(check)+", and its owner has stopped", nil)
		return
	}
	text := fmt.Sprintf("[deck] Task %s \"%s\" is not done yet: %s (round %d of %d). Run `%s` now, after your last change, and report the task with task.done once it passes. If it cannot pass, say why with task.fail.",
		tk.ID, tk.Title, noEvidence(check), tk.Reviews+1, store.MaxReviewChanges, check)
	e := store.Entry{Type: store.EntryRelay, ID: store.NewID(), Speaker: deckSpeaker, To: []string{owner.id}, Via: "review", TaskID: tk.ID, Event: eventText(text)}
	_, err := m.moveTask(t, tk.ID, store.TaskRunning, "", "sent back: "+noEvidence(check), func(tk *store.Task) {
		tk.Reviews++
		if tk.Contract == nil {
			tk.Contract = &store.Contract{Check: check}
		}
		// Under the same lock as the move, as review.changes does: the
		// owner's turn cannot settle the task before the note is logged.
		m.appendLocked(t, e)
	})
	if err != nil {
		return
	}
	m.emit(msgFor(t.id, e))
	_, _ = m.deliver(t, owner)
}
