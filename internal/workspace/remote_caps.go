package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	deckagent "github.com/dev-dami/deck/internal/deck/agent"
	deckconfig "github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/peer"
	decks "github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/message"
	"github.com/dev-dami/deck/internal/pubsub"
	"github.com/dev-dami/deck/internal/ui/styles"
	"github.com/dev-dami/deck/internal/ui/tone"
	"github.com/dev-dami/deck/internal/usage"
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

// DeckChange is a state change a second UI cannot read from the message
// store: the TUI learns of these through its program, the remote through
// SubscribeDeck. Every change names its session.
type DeckChange struct {
	Kind      DeckChangeKind
	SessionID string
	Busy      bool
	Mode      string
	Text      string
	Queue     []decks.QueuedItem
}

// Theme is the TUI's resolved palette and each agent kind's tone, for a
// client that draws with CSS instead of lipgloss.
type Theme struct {
	// Palette maps styles palette tokens (its JSON names) to "#rrggbb".
	Palette map[string]string
	Kinds   map[string]KindTone
}

// KindTone is one agent kind's colour and mark.
type KindTone struct {
	Tint string
	Mark string
}

// RemoteControl is what the phone remote control needs from the
// DeckWorkspace beyond [Workspace], [AgentPicker] and [Roster]
// (docs/remote-contract.md §5). Every method that names a session answers
// about that session alone: an item or message of another session is
// ErrNotFound.
type RemoteControl interface {
	// RemoteMode is the session's permission mode (plan, ask or yolo).
	RemoteMode(sessionID string) string
	// SetRemoteMode puts the session's thread in mode.
	SetRemoteMode(sessionID, mode string) error
	QueueItems(sessionID string) []decks.QueuedItem
	SteerQueued(sessionID, itemID string) error
	CancelQueued(sessionID, itemID string) error
	// InterruptAgent stops one member, named by its id or name.
	InterruptAgent(sessionID, agent string) error
	ContinueThread(sessionID string) error
	ChangedFiles(ctx context.Context, sessionID string) (decks.ChangeSet, error)
	// PostMessage is AgentRun that returns the id of the user row it made.
	PostMessage(ctx context.Context, sessionID, prompt string, attachments ...message.Attachment) (string, error)
	// EditableMessage reports whether EditMessage can change the row.
	EditableMessage(sessionID, messageID string) bool
	// EditMessage replaces the text of one of the user's earlier messages:
	// the agents it went to are told, and the row every UI shows is updated.
	EditMessage(ctx context.Context, sessionID, messageID, text string) error
	SubscribeDeck(ctx context.Context) <-chan pubsub.Event[DeckChange]
	Theme() Theme
}

// Errors RemoteControl reports.
var (
	// ErrNotFound is an id that is not in the session named.
	ErrNotFound = errors.New("not found")
	// ErrNotEditable is a message EditMessage cannot change: not the
	// user's, not in the session, or sent by a Deck that did not record it.
	ErrNotEditable = errors.New("this message can't be edited")
)

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
	if c.SessionID == "" {
		return
	}
	w.changes().Publish(pubsub.UpdatedEvent, c)
}

// SubscribeDeck implements [RemoteControl].
func (w *DeckWorkspace) SubscribeDeck(ctx context.Context) <-chan pubsub.Event[DeckChange] {
	return w.changes().Subscribe(ctx)
}

// RemoteMode implements [RemoteControl]: the thread's mode, or the
// workspace's for a session with no thread yet.
func (w *DeckWorkspace) RemoteMode(sessionID string) string {
	if th, ok := w.threadOf(sessionID); ok {
		return string(th.Mode)
	}
	return string(w.mode())
}

// SetRemoteMode implements [RemoteControl]. Modes are per thread, as the
// TUI's shift+tab is; the session's thread is made when it has none.
func (w *DeckWorkspace) SetRemoteMode(sessionID, mode string) error {
	switch mode {
	case RemoteModePlan, RemoteModeAsk, RemoteModeYolo:
	default:
		return fmt.Errorf("unknown mode %q", mode)
	}
	tid, err := w.thread(sessionID)
	if err != nil {
		return err
	}
	return w.mgr().SetMode(tid, deckagent.Mode(mode))
}

// QueueItems implements [RemoteControl].
func (w *DeckWorkspace) QueueItems(sessionID string) []decks.QueuedItem { return w.queue(sessionID) }

// queued reports whether itemID waits in the session's queue.
func (w *DeckWorkspace) queued(sessionID, itemID string) bool {
	for _, q := range w.queue(sessionID) {
		if q.ID == itemID {
			return true
		}
	}
	return false
}

// SteerQueued implements [RemoteControl].
func (w *DeckWorkspace) SteerQueued(sessionID, itemID string) error {
	if !w.queued(sessionID, itemID) {
		return ErrNotFound
	}
	return w.mgr().Steer(itemID)
}

// CancelQueued implements [RemoteControl].
func (w *DeckWorkspace) CancelQueued(sessionID, itemID string) error {
	if !w.queued(sessionID, itemID) {
		return ErrNotFound
	}
	return w.mgr().CancelQueued(itemID)
}

// InterruptAgent implements [RemoteControl].
func (w *DeckWorkspace) InterruptAgent(sessionID, agent string) error {
	th, ok := w.threadOf(sessionID)
	if !ok {
		return ErrNotFound
	}
	for _, a := range th.Members {
		if a.Name == agent || a.ID == agent {
			if err := w.mgr().Interrupt(a.ID); err != nil {
				return err
			}
			w.recordBehavior(sessionID, a.ID, usage.BehaviorInterrupt, "", 1)
			return nil
		}
	}
	return ErrNotFound
}

// ContinueThread implements [RemoteControl].
func (w *DeckWorkspace) ContinueThread(sessionID string) error {
	th, ok := w.threadOf(sessionID)
	if !ok {
		return ErrNotFound
	}
	return w.mgr().Continue(th.ID)
}

// ChangedFiles implements [RemoteControl]; a session with no thread has
// changed nothing.
func (w *DeckWorkspace) ChangedFiles(ctx context.Context, sessionID string) (decks.ChangeSet, error) {
	th, ok := w.threadOf(sessionID)
	if !ok {
		return decks.ChangeSet{}, nil
	}
	return w.mgr().Changes(ctx, th.ID, "")
}

// PostMessage implements [RemoteControl].
func (w *DeckWorkspace) PostMessage(ctx context.Context, sessionID, prompt string, attachments ...message.Attachment) (string, error) {
	return w.post(ctx, sessionID, prompt, attachments...)
}

// entryRef is which Deck log entry a user message row became, so the row
// can be edited.
type entryRef struct {
	SessionID string `json:"session_id"`
	EntryID   string `json:"entry_id"`
	// N orders the entries, so the oldest go first past maxEntries.
	N int64 `json:"n,omitempty"`
}

// maxEntries bounds the edit index: only recent prompts are worth
// editing from a phone, and the file is rewritten on every prompt.
const maxEntries = 2000

func (w *DeckWorkspace) entriesPath() string {
	if w.path == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(w.path), "deck-entries.json")
}

// loadEntries reads the edit index once, at start.
func (w *DeckWorkspace) loadEntries() {
	path := w.entriesPath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var idx map[string]entryRef
	if json.Unmarshal(data, &idx) == nil {
		w.mu.Lock()
		for _, ref := range idx {
			w.entrySeq = max(w.entrySeq, ref.N)
		}
		w.entries = idx
		w.mu.Unlock()
	}
}

// saveEntries writes the edit index atomically, one save at a time so an
// older snapshot never lands last; the caller holds no lock.
func (w *DeckWorkspace) saveEntries() {
	path := w.entriesPath()
	if path == "" {
		return
	}
	w.saveMu.Lock()
	defer w.saveMu.Unlock()
	w.mu.Lock()
	data, err := json.Marshal(w.entries)
	w.mu.Unlock()
	if err != nil {
		return
	}
	if err := peer.WritePrivateFile(path, data); err != nil {
		slog.Warn("Could not save the message edit index", "error", err)
	}
}

// trimEntries drops the oldest entries past maxEntries; w.mu is held.
func (w *DeckWorkspace) trimEntries() {
	if len(w.entries) <= maxEntries {
		return
	}
	ns := make([]int64, 0, len(w.entries))
	for _, ref := range w.entries {
		ns = append(ns, ref.N)
	}
	slices.Sort(ns)
	cut := ns[len(ns)-maxEntries]
	for id, ref := range w.entries {
		if ref.N < cut {
			delete(w.entries, id)
		}
	}
}

// recordEntry remembers which Deck log entry a user message row became.
func (w *DeckWorkspace) recordEntry(sessionID, messageID, entryID string) {
	if messageID == "" || entryID == "" {
		return
	}
	w.mu.Lock()
	if w.entries == nil {
		w.entries = map[string]entryRef{}
	}
	w.entrySeq++
	w.entries[messageID] = entryRef{SessionID: sessionID, EntryID: entryID, N: w.entrySeq}
	w.trimEntries()
	w.mu.Unlock()
	w.saveEntries()
}

// entryOf returns the log entry of a user row in the session.
func (w *DeckWorkspace) entryOf(sessionID, messageID string) (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	ref, ok := w.entries[messageID]
	if !ok || ref.SessionID != sessionID {
		return "", false
	}
	return ref.EntryID, true
}

// EditableMessage implements [RemoteControl].
func (w *DeckWorkspace) EditableMessage(sessionID, messageID string) bool {
	_, ok := w.entryOf(sessionID, messageID)
	return ok
}

// EditMessage implements [RemoteControl].
func (w *DeckWorkspace) EditMessage(ctx context.Context, sessionID, messageID, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("a message can't be empty")
	}
	entry, ok := w.entryOf(sessionID, messageID)
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
	return w.app.Messages.Update(ctx, row)
}

// Theme implements [RemoteControl]: the configured theme's resolved
// palette and each agent kind's tone as the TUI draws it now.
func (w *DeckWorkspace) Theme() Theme {
	name := ""
	if cfg := w.Config(); cfg != nil && cfg.Options != nil && cfg.Options.TUI != nil {
		name = cfg.Options.TUI.ActiveTheme
	}
	return themeFor(name)
}

// themeFor resolves a theme by name, falling back to Deck's own.
func themeFor(name string) Theme {
	if name == "" {
		name = "deck"
	}
	tf, err := styles.ExportResolvedPalette(name)
	if err != nil {
		tf, err = styles.ExportResolvedPalette("deck")
	}
	t := Theme{Palette: map[string]string{}, Kinds: map[string]KindTone{}}
	if err == nil {
		if data, err := json.Marshal(tf.Palette); err == nil {
			_ = json.Unmarshal(data, &t.Palette)
		}
	}
	for _, kind := range deckconfig.Kinds {
		t.Kinds[kind] = KindTone{Tint: tone.Hex(tone.Tint(kind)), Mark: tone.Mark(kind)}
	}
	return t
}
