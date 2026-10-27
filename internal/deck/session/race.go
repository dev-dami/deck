package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/changes"
	"github.com/dev-dami/deck/internal/deck/store"
)

// Races (docs/multiplayer-design.md §6): several subagents do one brief, each
// on a git worktree of its own, and the user keeps the best result. A race is
// a task (Race set) whose subtasks are its entries, one for each subagent.
// Deck owns the race task. When an entry ends, Deck records what it changed
// (Diff); when all have ended, the race is done with a summary of them, which
// goes to whoever started it, and an optional judge agent ranks them in a task
// of its own. KeepEntry applies one entry to the tree the race started from,
// cancels the entries still running and discards the others' worktrees.

// maxRaceEntries caps one race; MaxSubagents caps the thread as always.
const maxRaceEntries = 6

// Entrant is one agent of a race. Kinds mix and repeat: a race may be
// codex, claude and claude again on the same model.
type Entrant struct {
	// Agent is an agent kind ("claude") or a named agent's name ("ada").
	Agent string
	// Model and Effort are what it runs; "" is the kind's (or the named
	// agent's) own.
	Model, Effort string
}

// RaceSpec describes a race.
type RaceSpec struct {
	Title string
	Brief string
	// Entrants has one agent for each entry, in order.
	Entrants []Entrant
	// Judge is the agent kind, or a named agent, that ranks the entries; ""
	// is none.
	Judge string
	// Review is who reviews each entry before it ends (review.go).
	Review string
}

// RaceResult says what Race started.
type RaceResult struct {
	TaskID  string   // the race's task
	Entries []string // its entries' tasks
}

// Race starts a race in thread threadID. parentID is the member asking, or ""
// for the user; the entries are its subagents, within the limits of Spawn.
func (m *Manager) Race(threadID, parentID string, sp RaceSpec) (RaceResult, error) {
	if m.isClosed() {
		return RaceResult{}, errClosed
	}
	t := m.thread(threadID)
	if t == nil {
		return RaceResult{}, fmt.Errorf("session: no thread %q", threadID)
	}
	brief := strings.TrimSpace(sp.Brief)
	switch n := len(sp.Entrants); {
	case brief == "":
		return RaceResult{}, errors.New("session: a race needs a brief")
	case n < 2 || n > maxRaceEntries:
		return RaceResult{}, fmt.Errorf("session: a race has 2 to %d entries, not %d", maxRaceEntries, n)
	}
	// Each entry's task holds its kind; its spawn names the agent, so a
	// named agent's entry starts a member from that profile.
	entrants := make([]Entrant, len(sp.Entrants))
	kinds := make([]string, len(sp.Entrants))
	for i, e := range sp.Entrants {
		agent, kind, ok := m.raceAgent(t, e.Agent)
		if !ok {
			return RaceResult{}, fmt.Errorf("session: no agent kind or named agent %q", e.Agent)
		}
		entrants[i] = Entrant{Agent: agent, Model: strings.TrimSpace(e.Model), Effort: strings.ToLower(strings.TrimSpace(e.Effort))}
		kinds[i] = kind
	}
	if sp.Judge != "" {
		// A named agent judges as its kind.
		_, kind, ok := m.raceAgent(t, sp.Judge)
		if !ok {
			return RaceResult{}, fmt.Errorf("session: no agent kind or named agent %q", sp.Judge)
		}
		sp.Judge = kind
	}
	if err := m.checkReview(sp.Review); err != nil {
		return RaceResult{}, err
	}
	// Check all that can refuse before the first entry starts. A member's
	// race, unlike its single spawn, waits for orchestration.
	t.mu.Lock()
	off := !t.orchestrate
	t.mu.Unlock()
	if parentID != "" && off {
		return RaceResult{}, errors.New("session: orchestration is off for this thread; the user turns it on")
	}
	if _, _, err := m.spawnRoom(t, parentID, len(entrants)); err != nil {
		return RaceResult{}, err
	}
	t.mu.Lock()
	dir := t.dir
	t.mu.Unlock()
	ctx, cancel := context.WithTimeout(m.ctx, gitTimeout)
	_, err := changes.Open(ctx, dir)
	cancel()
	if err != nil {
		return RaceResult{}, errors.New("session: a race needs a git repository: each entry works on a worktree of its own")
	}

	title := clipText(oneLine(or(sp.Title, brief)), 60)
	race, err := m.addTask(t, TaskSpec{Title: "race: " + title, Brief: brief, Creator: parentID, Owner: deckSpeaker, Claimed: true, Race: true, Judge: sp.Judge})
	if err != nil {
		return RaceResult{}, err
	}
	res := RaceResult{TaskID: race.ID}
	// Every entry is on the board before the first starts: an entry that
	// cannot start ends at once, and must not end the race alone.
	for i, e := range entrants {
		tk, err := m.addTask(t, TaskSpec{Title: title, Brief: brief, Creator: parentID, Parent: race.ID, MaxAttempts: 1, Isolate: true,
			Kind: kinds[i], Model: e.Model, Effort: e.Effort, Claimed: true, Review: sp.Review})
		if err != nil {
			_, _ = m.moveTask(t, race.ID, store.TaskCancelled, parentID, "could not start: "+err.Error(), nil)
			return RaceResult{}, err
		}
		res.Entries = append(res.Entries, tk.ID)
	}
	note := fmt.Sprintf("\n\n[deck] This is a race: %d agents do this same task, each on a worktree of its own, and the user keeps the best result. Work alone; do not message the others. End with a short summary: what you changed, and which tests you ran with their results.", len(entrants))
	for i, id := range res.Entries {
		// An entry that cannot start fails with the reason, and the race goes
		// on with the others.
		_, _ = m.Spawn(threadID, parentID, SpawnSpec{Kind: entrants[i].Agent, TaskID: id, Brief: brief + note, Isolate: true})
	}
	return res, nil
}

// raceAgent resolves an entrant's agent, a kind or a named agent, to its
// name as the Deck spells it and the kind it runs. ok is false for anything
// else.
func (m *Manager) raceAgent(t *thread, name string) (agent, kind string, ok bool) {
	name = TrimSigil(name)
	if k, isKind := kindOf(name, m.Kinds()); isKind {
		return k, k, true
	}
	if p, named := m.profileNamed(t, name); named {
		if _, known := m.adapters[p.Kind]; known {
			return p.Name, p.Kind, true
		}
	}
	return "", "", false
}

// raceOf is the index of the race task tk is an entry of, or -1. t.mu is held.
func (t *thread) raceOf(tk store.Task) int {
	if tk.Parent == "" {
		return -1
	}
	if i := store.FindTask(t.tasks, tk.Parent); i >= 0 && t.tasks[i].Race {
		return i
	}
	return -1
}

// entryEnded reports whether a race entry will do no more work.
func entryEnded(tk store.Task) bool { return tk.Status == store.TaskDone || lostForGood(tk) }

// entryPending reports whether entry tk still has to end or have its changes
// recorded.
func entryPending(tk store.Task) bool {
	return !entryEnded(tk) || tk.Run != nil && tk.Run.Branch != "" && tk.Diff == nil
}

// raceStep runs after every change to task tk. When tk is a race entry that
// ended, it records what the entry changed, then reports the race if that was
// the last entry.
func (m *Manager) raceStep(t *thread, tk store.Task) {
	if !entryEnded(tk) {
		return
	}
	t.mu.Lock()
	i := t.raceOf(tk)
	t.mu.Unlock()
	if i < 0 {
		return
	}
	if tk.Run != nil && tk.Run.Branch != "" && tk.Diff == nil {
		d := m.entryDiff(t, tk.Run.Member)
		t.mu.Lock()
		j := store.FindTask(t.tasks, tk.ID)
		if j >= 0 {
			t.tasks[j].Diff = &d
			tk = cloneTask(t.tasks[j])
		}
		t.mu.Unlock()
		if j >= 0 {
			m.persistMeta(t)
			m.emit(TaskChangedMsg{ThreadID: t.id, Task: tk, From: tk.Status})
		}
	}
	m.settleRace(t, tk.Parent)
}

// entryDiff is what member id changed on its worktree; empty when that cannot
// be read.
func (m *Manager) entryDiff(t *thread, id string) store.DiffStat {
	ctx, cancel := context.WithTimeout(m.ctx, gitTimeout)
	defer cancel()
	_, _, wt, main, tree, err := m.memberTree(ctx, t.id, id)
	if err != nil {
		return store.DiffStat{}
	}
	patch, files, _, err := main.Pending(ctx, tree, wt.Base)
	if err != nil {
		return store.DiffStat{}
	}
	d := store.DiffStat{Files: files}
	for _, l := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(l, "+++ "), strings.HasPrefix(l, "--- "):
		case strings.HasPrefix(l, "+"):
			d.Added++
		case strings.HasPrefix(l, "-"):
			d.Removed++
		}
	}
	return d
}

// settleRace finishes race id once each of its entries has ended: the race is
// done with a summary of them, and its judge, if it has one, starts.
func (m *Manager) settleRace(t *thread, id string) {
	t.mu.Lock()
	i := store.FindTask(t.tasks, id)
	if i < 0 || t.tasks[i].Status != store.TaskRunning || t.raced[t.tasks[i].ID] {
		t.mu.Unlock()
		return
	}
	race := cloneTask(t.tasks[i])
	var lines, ranks []string
	for _, o := range t.tasks {
		if !strings.EqualFold(o.Parent, race.ID) {
			continue
		}
		if entryPending(o) {
			t.mu.Unlock()
			return
		}
		line := entryLine(t, o)
		lines = append(lines, line)
		if o.Run == nil || o.Run.Branch == "" {
			continue
		}
		where := " (no worktree)"
		if a := t.member(o.Run.Member); a != nil && a.getWorktree() != nil {
			where = fmt.Sprintf(" (`git -C %s diff %s` shows its changes)", a.getWorktree().Path, a.getWorktree().Base)
		}
		ranks = append(ranks, line+where)
	}
	if t.raced == nil {
		t.raced = map[string]bool{}
	}
	t.raced[race.ID] = true
	t.mu.Unlock()

	summary := strings.Join(lines, "\n")
	judge := race.Judge != "" && len(ranks) > 1
	if judge {
		summary += fmt.Sprintf("\nA %s judge ranks them next, in a task of its own.", race.Judge)
	}
	summary += "\nThe user keeps one entry; the others are discarded."
	_, _ = m.moveTask(t, race.ID, store.TaskDone, "", summary, nil)
	if !judge {
		return
	}
	// After the race is done, so that its summary arrives before the ranking.
	brief := fmt.Sprintf("Rank %d attempts at one task, best first, with one line on why for each. Each worked on a git worktree of its own. Read their changes, and run the tests if that helps, but do not change any files.\n\nThe task: %s\n\nThe attempts:\n%s",
		len(ranks), race.Brief, strings.Join(ranks, "\n"))
	if _, err := m.Spawn(t.id, race.Creator, SpawnSpec{Kind: race.Judge, Title: "rank the entries of " + race.ID, Brief: brief, Review: store.ReviewNone}); err != nil {
		m.notice(t, "the judge of "+race.ID+" could not start: "+err.Error())
	}
}

// entryLine is a race entry on one line: "t4 claude2 done: 2 files +10 -3,
// 12k tokens, 4 min: its result". t.mu is held.
func entryLine(t *thread, tk store.Task) string {
	who := tk.Kind // an entry that never started has no owner
	if tk.Owner != "" {
		who = t.name(tk.Owner)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s %s", tk.ID, who, tk.Status)
	var facts []string
	if d := tk.Diff; d != nil {
		facts = append(facts, fmt.Sprintf("%d files +%d -%d", len(d.Files), d.Added, d.Removed))
	}
	if n := tk.Spent.Tokens; n > 0 {
		facts = append(facts, kTokens(n)+" tokens")
	}
	if !tk.Started.IsZero() {
		facts = append(facts, fmt.Sprintf("%d min", int(tk.Updated.Sub(tk.Started).Round(time.Minute).Minutes())))
	}
	if len(facts) > 0 {
		b.WriteString(": " + strings.Join(facts, ", "))
	}
	switch {
	case tk.Status == store.TaskDone && tk.Result != "":
		b.WriteString(": " + clipText(oneLine(tk.Result), 200))
	case tk.Error != "":
		b.WriteString(": " + clipText(oneLine(tk.Error), 200))
	}
	return b.String()
}

// KeepEntry applies what race entry taskID changed to the tree the race
// started from, as uncommitted edits, whole or not at all. Then it cancels
// the entries still running, discards the other entries' worktrees and
// closes the race. It returns the files applied.
func (m *Manager) KeepEntry(threadID, taskID string) ([]string, error) {
	t := m.thread(threadID)
	if t == nil {
		return nil, fmt.Errorf("session: no thread %q", threadID)
	}
	t.mu.Lock()
	i := store.FindTask(t.tasks, taskID)
	if i < 0 {
		t.mu.Unlock()
		return nil, store.ErrNoTask
	}
	tk := cloneTask(t.tasks[i])
	r := t.raceOf(tk)
	t.mu.Unlock()
	switch {
	case r < 0:
		return nil, fmt.Errorf("session: %s is not a race entry", tk.ID)
	case tk.Run == nil || tk.Run.Branch == "":
		return nil, fmt.Errorf("session: %s has no worktree to keep", tk.ID)
	case tk.Status == store.TaskRunning, tk.Status == store.TaskReview:
		return nil, fmt.Errorf("session: %s is still %s; wait for it to end, or cancel it", tk.ID, tk.Status)
	}
	files, err := m.ApplyMember(threadID, tk.Run.Member)
	if err != nil {
		return nil, err
	}

	type other struct {
		id, member string
		open       bool
	}
	var others []other
	t.mu.Lock()
	raceID := tk.Parent
	if j := store.FindTask(t.tasks, raceID); j >= 0 {
		raceID = t.tasks[j].ID
	}
	if t.raced == nil {
		t.raced = map[string]bool{}
	}
	t.raced[raceID] = true // no summary after this
	for _, o := range t.tasks {
		if !strings.EqualFold(o.Parent, raceID) || o.ID == tk.ID {
			continue
		}
		var member string
		if o.Run != nil {
			member = o.Run.Member
		}
		others = append(others, other{o.ID, member, !entryEnded(o)})
	}
	note := fmt.Sprintf("the user kept %s (%s): %d files applied", tk.ID, t.name(tk.Owner), len(files))
	if j := store.FindTask(t.tasks, raceID); j >= 0 && t.tasks[j].Status == store.TaskDone {
		note = t.tasks[j].Result + "\n" + note // keep the summary
	}
	t.mu.Unlock()

	for _, o := range others {
		if o.open {
			_, _ = m.moveTask(t, o.id, store.TaskCancelled, "", "the user kept "+tk.ID, nil)
		}
		if o.member != "" {
			_ = m.DiscardMember(threadID, o.member) // nothing to do when it has no worktree left
		}
	}
	if _, err := m.moveTask(t, raceID, store.TaskDone, "", note, nil); err != nil {
		return files, err
	}
	return files, nil
}

// parseEntries reads the agents of a race as task.race gives them: kinds or
// named agents separated by commas or spaces, one for each entry, so a name
// given twice races two of it ("claude, claude, codex").
func parseEntries(s string) []Entrant {
	var out []Entrant
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		out = append(out, Entrant{Agent: f})
	}
	return out
}
