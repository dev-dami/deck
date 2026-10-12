package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/dev-dami/deck/internal/config"
	decks "github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/message"
	"github.com/dev-dami/deck/internal/pubsub"
)

// Permission modes as the remote control names them.
const (
	RemoteModePlan = "plan"
	RemoteModeAsk  = "ask"
	RemoteModeYolo = "yolo"
)

// DeckChangeKind says what a DeckChange is about.
type DeckChangeKind string

const (
	ChangeBusy   DeckChangeKind = "busy"
	ChangeQueue  DeckChangeKind = "queue"
	ChangeMode   DeckChangeKind = "mode"
	ChangeRoster DeckChangeKind = "roster"
	ChangeNotice DeckChangeKind = "notice"
)

// DeckChange is a state change a second UI cannot read from the message store:
// the TUI learns of these through its program, a headless remote through
// SubscribeDeck. SessionID is "" for workspace-wide changes (mode).
type DeckChange struct {
	Kind      DeckChangeKind
	SessionID string
	Busy      bool
	Mode      string
	Text      string
	Queue     []decks.QueuedItem
}

// RemoteControl is what a remote client needs beyond [Workspace], [AgentPicker]
// and [Roster]. The remote server type-asserts its Workspace to it.
type RemoteControl interface {
	RemoteMode() string
	// SetRemoteMode goes through the same switches as the TUI's tab and
	// ctrl+y, so both UIs agree on the mode.
	SetRemoteMode(mode string) error
	QueueItems(sessionID string) []decks.QueuedItem
	SteerQueued(itemID string) error
	CancelQueued(itemID string) error
	InterruptAgent(sessionID, agent string) error
	ContinueThread(sessionID string) error
	ChangedFiles(ctx context.Context, sessionID string) (decks.ChangeSet, error)
	SubscribeDeck(ctx context.Context) <-chan pubsub.Event[DeckChange]
	// EditableMessage reports whether EditMessage can change the row.
	EditableMessage(messageID string) bool
	// EditedMessage reports whether the user has rewritten the row.
	EditedMessage(messageID string) bool
	// EditMessage replaces the text of one of the user's earlier messages: the
	// agents it went to are told, and the row every UI shows is updated.
	EditMessage(ctx context.Context, sessionID, messageID, text string) error
}

// ErrNotEditable is returned for a message EditMessage cannot change: not the
// user's, or sent by a Deck that did not record it.
var ErrNotEditable = errors.New("this message can't be edited")

var _ RemoteControl = (*DeckWorkspace)(nil)

// changes returns the broker for DeckChange, made on first use so a
// DeckWorkspace built without NewDeckWorkspace still works.
func (w *DeckWorkspace) changes() *pubsub.Broker[DeckChange] {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.deckChanges == nil {
		w.deckChanges = pubsub.NewBroker[DeckChange]()
	}
	return w.deckChanges
}

func (w *DeckWorkspace) publish(c DeckChange) {
	w.changes().Publish(pubsub.UpdatedEvent, c)
}

// SubscribeDeck implements [RemoteControl].
func (w *DeckWorkspace) SubscribeDeck(ctx context.Context) <-chan pubsub.Event[DeckChange] {
	return w.changes().Subscribe(ctx)
}

// RemoteMode implements [RemoteControl].
func (w *DeckWorkspace) RemoteMode() string { return string(w.mode()) }

// SetRemoteMode implements [RemoteControl]. Plan leaves the yolo switch alone,
// as the TUI's tab does; ask and yolo leave plan.
func (w *DeckWorkspace) SetRemoteMode(mode string) error {
	switch mode {
	case RemoteModePlan:
		return w.AgentSetMain(config.AgentPlan)
	case RemoteModeAsk:
		w.AppWorkspace.PermissionSetSkipRequests(false)
		return w.AgentSetMain(config.AgentCoder)
	case RemoteModeYolo:
		w.AppWorkspace.PermissionSetSkipRequests(true)
		return w.AgentSetMain(config.AgentCoder)
	}
	return fmt.Errorf("unknown mode %q", mode)
}

// QueueItems implements [RemoteControl].
func (w *DeckWorkspace) QueueItems(sessionID string) []decks.QueuedItem { return w.queue(sessionID) }

// SteerQueued implements [RemoteControl].
func (w *DeckWorkspace) SteerQueued(itemID string) error { return w.mgr().Steer(itemID) }

// CancelQueued implements [RemoteControl].
func (w *DeckWorkspace) CancelQueued(itemID string) error { return w.mgr().CancelQueued(itemID) }

// InterruptAgent implements [RemoteControl]; agent is the member's name as the
// roster shows it.
func (w *DeckWorkspace) InterruptAgent(sessionID, agent string) error {
	th, ok := w.threadOf(sessionID)
	if !ok {
		return fmt.Errorf("session %s has no running agents", sessionID)
	}
	for _, a := range th.Members {
		if a.Name == agent || a.ID == agent {
			return w.mgr().Interrupt(a.ID)
		}
	}
	return fmt.Errorf("no agent %q in this session", agent)
}

// ContinueThread implements [RemoteControl].
func (w *DeckWorkspace) ContinueThread(sessionID string) error {
	th, ok := w.threadOf(sessionID)
	if !ok {
		return fmt.Errorf("session %s has no running agents", sessionID)
	}
	return w.mgr().Continue(th.ID)
}

// ChangedFiles implements [RemoteControl].
func (w *DeckWorkspace) ChangedFiles(ctx context.Context, sessionID string) (decks.ChangeSet, error) {
	th, ok := w.threadOf(sessionID)
	if !ok {
		return decks.ChangeSet{}, nil
	}
	return w.mgr().Changes(ctx, th.ID, "")
}

func (w *DeckWorkspace) entriesPath() string {
	if w.path == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(w.path), "deck-entries.json")
}

// editIndex is what deck-entries.json holds.
type editIndex struct {
	Entries map[string]string `json:"entries"`
	Edited  map[string]bool   `json:"edited,omitempty"`
}

// saveIndex writes the index; the caller holds no lock.
func (w *DeckWorkspace) saveIndex() {
	path := w.entriesPath()
	if path == "" {
		return
	}
	w.mu.Lock()
	data, err := json.Marshal(editIndex{Entries: w.entries, Edited: w.edited})
	w.mu.Unlock()
	if err != nil {
		return
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		slog.Warn("Could not save the message edit index", "error", err)
	}
}

// recordEntry remembers which Deck log entry a user message row became.
func (w *DeckWorkspace) recordEntry(messageID, entryID string) {
	if messageID == "" || entryID == "" {
		return
	}
	w.mu.Lock()
	if w.entries == nil {
		w.entries = map[string]string{}
	}
	w.entries[messageID] = entryID
	w.mu.Unlock()
	w.saveIndex()
}

// markEdited sets or clears a row's edited mark.
func (w *DeckWorkspace) markEdited(messageID string, edited bool) {
	w.mu.Lock()
	if w.edited == nil {
		w.edited = map[string]bool{}
	}
	if edited {
		w.edited[messageID] = true
	} else {
		delete(w.edited, messageID)
	}
	w.mu.Unlock()
	w.saveIndex()
}

// EditedMessage implements [RemoteControl].
func (w *DeckWorkspace) EditedMessage(messageID string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.edited[messageID]
}

// EditableMessage implements [RemoteControl].
func (w *DeckWorkspace) EditableMessage(messageID string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.entries[messageID]
	return ok
}

// EditMessage implements [RemoteControl].
func (w *DeckWorkspace) EditMessage(ctx context.Context, sessionID, messageID, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("a message can't be empty")
	}
	w.mu.Lock()
	entry, ok := w.entries[messageID]
	w.mu.Unlock()
	if !ok {
		return ErrNotEditable
	}
	row, err := w.app.Messages.Get(ctx, messageID)
	if err != nil || row.SessionID != sessionID || row.Role != message.User {
		return ErrNotEditable
	}
	tid, err := w.thread(sessionID)
	if err != nil {
		return err
	}
	if err := w.mgr().Edit(tid, entry, text); err != nil {
		return err
	}
	replaced := false
	for i, p := range row.Parts {
		if t, ok := p.(message.TextContent); ok && !t.Hidden && !replaced {
			t.Text = text
			row.Parts[i] = t
			replaced = true
		}
	}
	if !replaced {
		row.Parts = append([]message.ContentPart{message.TextContent{Text: text}}, row.Parts...)
	}
	// Marked before the update so the row's change event already carries it.
	wasEdited := w.EditedMessage(messageID)
	w.markEdited(messageID, true)
	if err := w.app.Messages.Update(ctx, row); err != nil {
		w.markEdited(messageID, wasEdited)
		return err
	}
	return nil
}
