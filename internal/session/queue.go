package session

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/store"
)

// qitem is a queued item plus the bookkeeping the Manager needs.
type qitem struct {
	QueuedItem
	idx int    // log index of the entry that queued it (a user message or an edit)
	src string // that entry's id
	old string // edit items: the text before the edit
}

func (q qitem) public() QueuedItem {
	it := q.QueuedItem
	it.Images = append([]agent.Image(nil), it.Images...)
	return it
}

// ---- images ----

func imagePaths(images []agent.Image) []string {
	out := make([]string, 0, len(images))
	for _, im := range images {
		out = append(out, im.Path)
	}
	return out
}

// mediaTypeFor guesses an image's media type from its file name.
func mediaTypeFor(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	return "image/png"
}

// imagesOf returns the images of a user entry: as the user attached them in this
// session, else rebuilt from the stored paths. t.mu is held.
func (t *thread) imagesOf(e store.Entry) []agent.Image {
	if im, ok := t.images[e.ID]; ok {
		return im
	}
	var out []agent.Image
	for _, p := range e.Images {
		out = append(out, agent.Image{Path: p, MediaType: mediaTypeFor(p)})
	}
	return out
}

// noImageLine tells a member that cannot view images what the user attached.
func noImageLine(images []agent.Image) string {
	names := make([]string, 0, len(images))
	for _, im := range images {
		names = append(names, filepath.Base(im.Path))
	}
	return fmt.Sprintf("[deck] The user attached %d image(s) that this agent can't view: %s", len(images), strings.Join(names, ", "))
}

// ---- the queue ----

func itemID(entryID, agentID string) string { return entryID + ":" + agentID }

// findItemLocked is the index of the queue item with the id, or -1. t.mu is held.
func (t *thread) findItemLocked(id string) int {
	for i, q := range t.queue {
		if q.ID == id {
			return i
		}
	}
	return -1
}

// syncQueueLocked enters into the queue the user messages and edits addressed to
// the busy member a that it has not been given yet. It reports whether the queue
// changed. t.mu is held.
func (t *thread) syncQueueLocked(a *agentState) bool {
	changed := false
	var edited map[string]string
	for i := a.cursor; i < len(t.log); i++ {
		e := t.log[i]
		if (e.Type != store.EntryUser && e.Type != store.EntryEdit) || e.Steer {
			continue
		}
		if t.inert[i] || t.skip[skipKey{i, a.id}] != 0 || !contains(e.To, a.id) {
			continue
		}
		id := itemID(e.ID, a.id)
		if t.findItemLocked(id) >= 0 {
			continue
		}
		q := qitem{idx: i, src: e.ID}
		q.QueuedItem = QueuedItem{ID: id, ThreadID: t.id, AgentID: a.id, At: e.Time}
		if e.Type == store.EntryUser {
			if edited == nil {
				edited = t.editedTexts()
			}
			q.Text = strings.TrimSpace(e.Text)
			if txt, ok := edited[e.ID]; ok {
				q.Text = strings.TrimSpace(txt)
			}
			q.Images = t.imagesOf(e)
			q.EntryID = e.ID
		} else {
			q.Text, q.old, q.Edit, q.EntryID = strings.TrimSpace(e.Text), e.Old, true, e.Ref
		}
		t.queue = append(t.queue, q)
		changed = true
	}
	if changed {
		sort.SliceStable(t.queue, func(i, j int) bool { return t.queue[i].idx < t.queue[j].idx })
	}
	return changed
}

// dropQueueLocked removes a's queue items: those logged before log index upto
// (they have just been delivered) or, with upto < 0, all except items whose
// steer is in flight. It reports whether anything was removed. t.mu is held.
func (t *thread) dropQueueLocked(a *agentState, upto int) bool {
	keep := t.queue[:0]
	changed := false
	for _, q := range t.queue {
		drop := q.AgentID == a.id && ((upto >= 0 && q.idx < upto) || (upto < 0 && !q.Steer))
		if drop {
			changed = true
			continue
		}
		keep = append(keep, q)
	}
	t.queue = keep
	return changed
}

// queueDelivered clears a's items logged before cursor after a prompt covering
// them was sent.
func (m *Manager) queueDelivered(t *thread, a *agentState, cursor int) {
	t.mu.Lock()
	changed := t.dropQueueLocked(a, cursor)
	t.mu.Unlock()
	if changed {
		m.emitQueue(t)
	}
}

// queuedFor lists the queue items that the user message entryID created.
func (t *thread) queuedFor(entryID string) []QueuedItem {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []QueuedItem
	for _, q := range t.queue {
		if q.src == entryID {
			out = append(out, q.public())
		}
	}
	return out
}

func (t *thread) snapshotQueueLocked() []QueuedItem {
	out := make([]QueuedItem, 0, len(t.queue))
	for _, q := range t.queue {
		out = append(out, q.public())
	}
	return out
}

// emitQueue announces the thread's whole queue. Snapshots are taken and sent
// under one lock so they reach the UI in the order they were taken.
func (m *Manager) emitQueue(t *thread) {
	t.qemit.Lock()
	defer t.qemit.Unlock()
	t.mu.Lock()
	items := t.snapshotQueueLocked()
	t.mu.Unlock()
	m.emit(QueueChangedMsg{ThreadID: t.id, Items: items})
}

// Queue lists the user's messages waiting for busy members of a thread, oldest
// first. Relays between agents are not included.
func (m *Manager) Queue(threadID string) []QueuedItem {
	t := m.thread(threadID)
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.snapshotQueueLocked()
}

// locate finds the thread and member a queue item belongs to.
func (m *Manager) locate(itemID string) (*thread, *agentState, bool) {
	m.mu.Lock()
	ts := append([]*thread(nil), m.torder...)
	m.mu.Unlock()
	for _, t := range ts {
		t.mu.Lock()
		if k := t.findItemLocked(itemID); k >= 0 {
			a := t.member(t.queue[k].AgentID)
			t.mu.Unlock()
			return t, a, a != nil
		}
		t.mu.Unlock()
	}
	return nil, nil, false
}

// CancelQueued removes a queued item without delivering it: the member never
// sees that message. It is an error if the item is no longer queued (it was
// delivered meanwhile) or its steer is in flight.
func (m *Manager) CancelQueued(itemID string) error {
	t, a, ok := m.locate(itemID)
	if !ok {
		return fmt.Errorf("session: no queued message %q", itemID)
	}
	t.mu.Lock()
	k := t.findItemLocked(itemID)
	if k < 0 {
		t.mu.Unlock()
		return fmt.Errorf("session: no queued message %q (already delivered?)", itemID)
	}
	q := t.queue[k]
	if q.Steer {
		t.mu.Unlock()
		return errors.New("session: that message is being delivered as a steer")
	}
	t.queue = append(t.queue[:k], t.queue[k+1:]...)
	t.skip[skipKey{q.idx, a.id}] = skipCancelled
	m.appendLocked(t, store.Entry{Type: store.EntryCancel, Ref: q.src, To: []string{a.id}})
	t.mu.Unlock()
	m.emitQueue(t)
	return nil
}

// rebuildSkips restores the skip set of a stored thread from its cancel and
// steer entries. t.mu is held or t is not shared yet.
func (t *thread) rebuildSkips() {
	byID := make(map[string]int, len(t.log))
	for i, e := range t.log {
		byID[e.ID] = i
	}
	for _, e := range t.log {
		switch {
		case e.Type == store.EntryCancel:
			if idx, ok := byID[e.Ref]; ok {
				for _, to := range e.To {
					t.skip[skipKey{idx, to}] = skipCancelled
				}
			}
		case e.Type == store.EntryUser && e.Steer && e.Old == "":
			if idx, ok := byID[e.Ref]; ok && t.log[idx].Type == store.EntryUser {
				for _, to := range e.To {
					t.skip[skipKey{idx, to}] = skipSteered
				}
			}
		}
	}
}

// ---- steering ----

// busy reports whether the member is in a turn (working, in a tool, or waiting
// for an approval).
func (a *agentState) busy() bool {
	switch a.status() {
	case StatusWorking, StatusTool, StatusApproval:
		return true
	}
	return false
}

// steerWait is how long a steer that has to interrupt the agent waits for the
// turn to end.
const steerWait = 5 * time.Second

// Steer takes a queued item out of the queue and delivers it to its member now,
// redirecting it mid-task. See the package comment.
func (m *Manager) Steer(itemID string) error {
	if m.isClosed() {
		return errClosed
	}
	t, a, ok := m.locate(itemID)
	if !ok {
		return fmt.Errorf("session: no queued message %q", itemID)
	}
	cfg := m.config()
	speaker := cfg.UserName
	if speaker == "" {
		speaker = "user"
	}

	t.mu.Lock()
	k := t.findItemLocked(itemID)
	if k < 0 {
		t.mu.Unlock()
		return fmt.Errorf("session: no queued message %q (already delivered?)", itemID)
	}
	if t.queue[k].Steer {
		t.mu.Unlock()
		return errors.New("session: that message is already being steered")
	}
	t.queue[k].Steer = true
	q := t.queue[k]
	e := store.Entry{
		Type: store.EntryUser, ID: store.NewID(), Speaker: speaker, To: []string{a.id},
		Steer: true, Ref: q.EntryID, Old: q.old,
		Event: agent.Event{Kind: agent.KindUserText, Text: q.Text},
	}
	if len(q.Images) > 0 {
		e.Images = imagePaths(q.Images)
		t.images[e.ID] = q.Images
	}
	t.skip[skipKey{q.idx, a.id}] = skipSteered
	steerIdx := m.appendLocked(t, e)
	msg := msgFor(t.id, e)
	t.mu.Unlock()
	m.emit(msg)
	m.emitQueue(t) // the item now shows as being steered

	if err := m.steerDeliver(t, a); err != nil {
		t.mu.Lock()
		if k := t.findItemLocked(itemID); k >= 0 {
			t.queue[k].Steer = false
		}
		delete(t.skip, skipKey{q.idx, a.id})
		t.inert[steerIdx] = true // the original goes out the normal way
		t.mu.Unlock()
		m.emitQueue(t)
		return err
	}
	return nil
}

// steerDeliver sends a the prompt built from the thread, which now ends in a
// steer entry.
func (m *Manager) steerDeliver(t *thread, a *agentState) error {
	if !a.busy() {
		return m.startAfterSteer(t, a)
	}
	if st, ok := a.getRun().(agent.Steerer); ok {
		err := m.steerNow(t, a, st)
		if !errors.Is(err, errNotBusy) {
			return err
		}
		return m.startAfterSteer(t, a)
	}
	// No way to inject into the running turn: stop it, then send.
	if err := m.Interrupt(a.id); err != nil {
		return err
	}
	deadline := time.Now().Add(steerWait)
	for a.busy() {
		if time.Now().After(deadline) {
			return fmt.Errorf("session: %s did not stop in time to be redirected", a.getName())
		}
		time.Sleep(2 * time.Millisecond)
	}
	return m.startAfterSteer(t, a)
}

var errNotBusy = errors.New("session: agent is idle")

func (m *Manager) startAfterSteer(t *thread, a *agentState) error {
	d, err := m.deliverEx(t, a, false)
	switch {
	case err != nil:
		return err
	case d == dExited:
		return fmt.Errorf("session: %s has exited", a.getName())
	case d == dQueued:
		return fmt.Errorf("session: %s is busy again; try once more", a.getName())
	}
	return nil
}

// steerNow injects the thread's pending prompt into a's running turn.
func (m *Manager) steerNow(t *thread, a *agentState, st agent.Steerer) error {
	if !a.busy() {
		return errNotBusy
	}
	t.mu.Lock()
	prompt, images, cursor, hold := m.buildPrompt(t, a)
	oldCursor, oldHold, oldKnown := a.cursor, a.holdFrom, a.knownMode
	a.cursor, a.holdFrom = cursor, hold
	if a.knownMode != t.mode {
		prompt = modeLine(t.mode) + "\n\n" + prompt
		a.knownMode = t.mode
	}
	t.mu.Unlock()
	if err := st.Steer(prompt, images); err != nil {
		t.mu.Lock()
		a.cursor, a.holdFrom, a.knownMode = oldCursor, oldHold, oldKnown
		t.mu.Unlock()
		return err
	}
	m.persistMeta(t)
	m.queueDelivered(t, a, cursor)
	return nil
}

// ---- editing ----

// Edit replaces the text of one of the user's earlier messages and tells the
// members it was addressed to. See the package comment.
func (m *Manager) Edit(threadID, entryID, newText string) error {
	t := m.thread(threadID)
	if t == nil {
		return fmt.Errorf("session: no thread %q", threadID)
	}
	if m.isClosed() {
		return errClosed
	}
	newText = strings.TrimSpace(newText)
	if newText == "" {
		return errors.New("session: empty message")
	}
	cfg := m.config()
	speaker := cfg.UserName
	if speaker == "" {
		speaker = "user"
	}

	t.mu.Lock()
	idx := -1
	for i, e := range t.log {
		if e.ID == entryID && entryID != "" {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.mu.Unlock()
		return fmt.Errorf("session: no message %q in this thread", entryID)
	}
	orig := t.log[idx]
	if orig.Type != store.EntryUser || orig.Steer {
		t.mu.Unlock()
		return errors.New("session: only the user's own messages can be edited")
	}
	old := strings.TrimSpace(orig.Text)
	if txt, ok := t.editedTexts()[orig.ID]; ok {
		old = strings.TrimSpace(txt)
	}
	if old == newText {
		t.mu.Unlock()
		return nil
	}

	// Members that already got the original are told about the edit; the others
	// (it is still queued for them) just get the new text in its place.
	var prompted []*agentState
	for _, id := range orig.To {
		a := t.member(id)
		if a == nil || a.status() == StatusExited || t.skip[skipKey{idx, id}] == skipCancelled {
			continue
		}
		if a.cursor > idx {
			prompted = append(prompted, a)
		}
	}
	e := store.Entry{Type: store.EntryEdit, Speaker: speaker, Ref: entryID, Old: old, To: ids(prompted),
		Event: agent.Event{Text: newText}}
	m.appendLocked(t, e)
	queueChanged := false
	for i := range t.queue {
		if q := &t.queue[i]; q.src == entryID && !q.Edit {
			q.Text, queueChanged = newText, true
		}
	}
	msg := msgFor(t.id, e)
	t.mu.Unlock()

	m.emit(msg)
	if queueChanged {
		m.emitQueue(t)
	}
	var first error
	for _, a := range prompted {
		if _, err := m.deliver(t, a); err != nil && first == nil {
			first = err
		}
	}
	return first
}
