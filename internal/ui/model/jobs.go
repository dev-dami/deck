package model

import (
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Deck's own jobs: the operations Deck runs itself (a git push, a fetch, a
// gh call), listed in the Commands pane while they run. The UI owns the
// list; a tea.Cmd reports through JobStartedMsg and JobDoneMsg.

// deckJob is one of Deck's operations in flight.
type deckJob struct {
	ID      string
	Label   string // "git push", "Fetching…"
	Started time.Time
}

// jobState is the jobs in flight. Its zero value is ready.
type jobState struct {
	running map[string]deckJob
	next    int
}

// JobStartedMsg tells the UI a job began; send it from a tea.Cmd, or call
// m.jobStarted from Update.
type JobStartedMsg struct{ ID, Label string }

// JobDoneMsg tells the UI a job ended.
type JobDoneMsg struct {
	ID  string
	Err error
}

// JobStarted is a tea.Cmd that reports a job began.
func JobStarted(id, label string) tea.Cmd {
	return func() tea.Msg { return JobStartedMsg{ID: id, Label: label} }
}

// JobDone is a tea.Cmd that reports a job ended.
func JobDone(id string, err error) tea.Cmd {
	return func() tea.Msg { return JobDoneMsg{ID: id, Err: err} }
}

// newJobID hands out an id for a job of kind ("git", "gh").
func (j *jobState) newID(kind string) string {
	j.next++
	return kind + "-" + strconv.Itoa(j.next)
}

// jobStarted records a job.
func (m *UI) jobStarted(id, label string) {
	if id == "" {
		return
	}
	if m.jobs.running == nil {
		m.jobs.running = map[string]deckJob{}
	}
	label = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(label), "…"))
	if label == "" {
		label = id
	}
	m.jobs.running[id] = deckJob{ID: id, Label: label, Started: time.Now()}
}

// jobDone forgets a job. Its error is reported by whoever ran it.
func (m *UI) jobDone(id string, _ error) {
	delete(m.jobs.running, id)
}

// startJob records a new job of kind and returns its id, for a tea.Cmd to
// finish with JobDoneMsg (or for its result message to carry back).
func (m *UI) startJob(kind, label string) string {
	id := m.jobs.newID(kind)
	m.jobStarted(id, label)
	return id
}

// runningJobs is the jobs in flight, oldest first.
func (m *UI) runningJobs() []deckJob {
	out := make([]deckJob, 0, len(m.jobs.running))
	for _, j := range m.jobs.running {
		out = append(out, j)
	}
	sort.Slice(out, func(i, k int) bool {
		if !out[i].Started.Equal(out[k].Started) {
			return out[i].Started.Before(out[k].Started)
		}
		return out[i].ID < out[k].ID
	})
	return out
}
