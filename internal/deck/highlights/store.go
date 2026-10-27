// Package highlights stores explicit findings and short agent updates. It
// neither summarizes conversations nor changes approval/task state.
package highlights

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/dev-dami/deck/internal/deck/store"
	pb "github.com/dev-dami/deck/pkg/rpc/deckv1"
	"google.golang.org/protobuf/proto"
)

const (
	MaxItems         = 1000
	MaxSummaries     = 128
	MaxDocumentBytes = 16 << 20
)

var (
	ErrInvalid   = errors.New("invalid highlight action")
	ErrNotFound  = errors.New("highlight not found in this room")
	ErrForbidden = errors.New("only the author or user may change this highlight")
	validSession = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
)

// Actor comes from the authenticated engine caller or the user's RPC. An
// agent cannot choose User, Agent or Turn through op arguments.
type Actor struct {
	User        bool
	Agent, Turn string
}

func (a Actor) owner() string {
	if a.User {
		return "user"
	}
	return "agent:" + a.Agent
}

type Change struct {
	SessionID string
	Item      *pb.HighlightItem
	Summary   *pb.AgentSummary
}
type record struct {
	Owner string            `json:"owner"`
	Item  *pb.HighlightItem `json:"item"`
}
type document struct {
	Version   int                `json:"version"`
	Items     []record           `json:"items"`
	Summaries []*pb.AgentSummary `json:"summaries"`
}
type turnRef struct{ Turn, Message string }

type Store struct {
	mu      sync.Mutex
	dir     string
	docs    map[string]*document
	deleted map[string]bool
	turns   map[string]map[string]turnRef
	changed func(Change)
}

// An empty directory is an explicitly ephemeral store (tests).
func New(dir string, changed func(Change)) *Store {
	return &Store{dir: dir, docs: map[string]*document{}, deleted: map[string]bool{}, turns: map[string]map[string]turnRef{}, changed: changed}
}
func copyPB[T proto.Message](value T) T { return proto.Clone(value).(T) }
func copyDoc(d *document) *document {
	out := &document{Version: 1}
	for _, r := range d.Items {
		out.Items = append(out.Items, record{Owner: r.Owner, Item: copyPB(r.Item)})
	}
	for _, s := range d.Summaries {
		out.Summaries = append(out.Summaries, copyPB(s))
	}
	return out
}

func (s *Store) load(session string) (*document, error) {
	if !validSession.MatchString(session) {
		return nil, ErrInvalid
	}
	if s.deleted[session] {
		return nil, ErrNotFound
	}
	if d := s.docs[session]; d != nil {
		return d, nil
	}
	d := &document{Version: 1}
	if s.dir != "" {
		f, err := os.Open(filepath.Join(s.dir, session+".json"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if err == nil {
			defer f.Close()
			data, err := io.ReadAll(io.LimitReader(f, MaxDocumentBytes+1))
			if err != nil {
				return nil, err
			}
			if len(data) > MaxDocumentBytes {
				return nil, errors.New("highlight store exceeds size limit")
			}
			if err := json.Unmarshal(data, d); err != nil {
				return nil, fmt.Errorf("read highlights: %w", err)
			}
			if err := validateDocument(session, d); err != nil {
				return nil, err
			}
		}
	}
	s.docs[session] = d
	return d, nil
}

func validateDocument(session string, d *document) error {
	if d.Version != 1 || len(d.Items) > MaxItems || len(d.Summaries) > MaxSummaries {
		return errors.New("unsupported or oversized highlight store")
	}
	ids := map[string]bool{}
	keys := map[string]bool{}
	agents := map[string]bool{}
	for _, r := range d.Items {
		i := r.Item
		if i == nil || i.Id == "" || ids[i.Id] || i.SessionId != session || r.Owner == "" || !validKind(i.Kind) || !validText(i.Title, 160, true, true) || !validText(i.Detail, 2000, false, false) || !validKey(i.Key) || !validSource(i.Source) {
			return errors.New("invalid persisted highlight")
		}
		if i.State != "open" && i.State != "resolved" && i.State != "dismissed" {
			return errors.New("invalid persisted highlight state")
		}
		key := r.Owner + "\x00" + i.Key
		if keys[key] {
			return errors.New("duplicate persisted highlight key")
		}
		keys[key] = true
		ids[i.Id] = true
	}
	for _, a := range d.Summaries {
		if a == nil || a.SessionId != session || a.Agent == "" || agents[a.Agent] || !validText(a.Text, 280, true, true) || !validSource(a.Source) {
			return errors.New("invalid persisted agent summary")
		}
		agents[a.Agent] = true
	}
	return nil
}

func (s *Store) save(session string, d *document, changes []Change) error {
	if s.dir != "" {
		data, err := json.Marshal(d)
		if err != nil {
			return err
		}
		if len(data) > MaxDocumentBytes {
			return errors.New("highlight store exceeds size limit")
		}
		if err := os.MkdirAll(s.dir, 0700); err != nil {
			return err
		}
		file, err := os.CreateTemp(s.dir, ".highlights-*.tmp")
		if err != nil {
			return err
		}
		path := file.Name()
		defer os.Remove(path)
		if _, err = file.Write(data); err != nil {
			file.Close()
			return err
		}
		if err = file.Sync(); err != nil {
			file.Close()
			return err
		}
		if err = file.Close(); err != nil {
			return err
		}
		if err = os.Rename(path, filepath.Join(s.dir, session+".json")); err != nil {
			return err
		}
	}
	s.docs[session] = d
	// Publish under the mutation lock, after durability, to preserve update
	// order. The callback must not re-enter this store.
	if s.changed != nil {
		for _, change := range changes {
			s.changed(change)
		}
	}
	return nil
}

func (s *Store) Snapshot(session string) (*pb.HighlightsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.load(session)
	if err != nil {
		return nil, err
	}
	out := &pb.HighlightsResponse{}
	for _, r := range d.Items {
		out.Items = append(out.Items, copyPB(r.Item))
	}
	for _, a := range d.Summaries {
		out.Summaries = append(out.Summaries, copyPB(a))
	}
	return out, nil
}

// Action validates and atomically applies one explicit action. The workspace
// additionally validates primary source refs against its message/task stores.
func (s *Store) Action(actor Actor, r *pb.HighlightActionRequest, pinAuthor string) (*pb.HighlightItem, error) {
	if r == nil || (!actor.User && actor.Agent == "") {
		return nil, ErrInvalid
	}
	r = copyPB(r)
	if !validKeyOptional(r.Key) || !validText(r.Title, 160, true, false) || !validText(r.Detail, 2000, false, false) || !validSource(r.Source) {
		return nil, ErrInvalid
	}
	if r.Kind != "" && !validKind(r.Kind) {
		return nil, ErrInvalid
	}
	if len(r.Ids) > 0 && r.Action != "seen" {
		return nil, ErrInvalid
	}
	if r.Source != nil && !actor.User {
		r.Source.TurnId = actor.Turn
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.load(r.SessionId)
	if err != nil {
		return nil, err
	}
	d := copyDoc(current)
	if r.Action == "seen" {
		if !actor.User || len(r.Ids) > MaxItems || r.Key != "" || r.Title != "" || r.Detail != "" || r.Kind != "" || r.Source != nil {
			return nil, ErrInvalid
		}
		ids := append([]string{}, r.Ids...)
		if r.Id != "" {
			ids = append(ids, r.Id)
		}
		if len(ids) == 0 {
			return nil, ErrInvalid
		}
		wanted := map[string]bool{}
		for _, id := range ids {
			wanted[id] = true
		}
		for _, rec := range d.Items {
			delete(wanted, rec.Item.Id)
		}
		if len(wanted) > 0 {
			return nil, ErrNotFound
		}
		wanted = map[string]bool{}
		for _, id := range ids {
			wanted[id] = true
		}
		var changes []Change
		var first *pb.HighlightItem
		for _, rec := range d.Items {
			if wanted[rec.Item.Id] {
				if first == nil {
					first = rec.Item
				}
				if rec.Item.Unread {
					rec.Item.Unread = false
					rec.Item.UpdatedAt = nextTime(rec.Item.UpdatedAt)
					changes = append(changes, Change{SessionID: r.SessionId, Item: copyPB(rec.Item)})
				}
			}
		}
		if len(changes) > 0 {
			if err := s.save(r.SessionId, d, changes); err != nil {
				return nil, err
			}
		}
		return copyPB(first), nil
	}
	if r.Action == "pin" {
		if !actor.User || r.Source == nil || r.Source.MessageId == "" || pinAuthor == "" {
			return nil, ErrInvalid
		}
		r.Key = "pin:" + r.Source.MessageId
	}
	var found *record
	for index := range d.Items {
		rec := &d.Items[index]
		if (r.Id != "" && rec.Item.Id == r.Id) || (r.Id == "" && r.Key != "" && rec.Owner == actor.owner() && rec.Item.Key == r.Key) {
			found = rec
			break
		}
	}
	if found != nil && !actor.User && found.Owner != actor.owner() {
		return nil, ErrForbidden
	}
	if r.Id != "" && r.Key != "" && (found == nil || found.Item.Key != r.Key) {
		return nil, ErrInvalid
	}
	var item *pb.HighlightItem
	switch r.Action {
	case "add", "pin":
		if r.Id != "" || !validKey(r.Key) || !validText(r.Title, 160, true, true) {
			return nil, ErrInvalid
		}
		if found == nil {
			if len(d.Items) >= MaxItems {
				return nil, errors.New("this room reached its highlight limit")
			}
			agent := actor.Agent
			if actor.User {
				agent = "user"
			}
			if r.Action == "pin" {
				agent = pinAuthor
			}
			item = &pb.HighlightItem{Id: store.NewID(), SessionId: r.SessionId, Agent: agent, Key: r.Key, State: "open", CreatedAt: time.Now().UnixMilli(), Unread: true}
			d.Items = append(d.Items, record{Owner: actor.owner(), Item: item})
		} else {
			item = found.Item
		}
		kind := r.Kind
		if kind == "" {
			kind = "finding"
		}
		item.Kind = kind
		item.Title = strings.TrimSpace(r.Title)
		item.Detail = strings.TrimSpace(r.Detail)
		item.Source = s.source(r.SessionId, actor, r.Source)
	case "update":
		if found == nil {
			return nil, ErrNotFound
		}
		item = found.Item
		if r.Title != "" {
			if !validText(r.Title, 160, true, true) {
				return nil, ErrInvalid
			}
			item.Title = strings.TrimSpace(r.Title)
		}
		if r.Kind != "" {
			item.Kind = r.Kind
		}
		// Detail is a proto3 string: update always replaces it, including clear.
		item.Detail = strings.TrimSpace(r.Detail)
		if r.Source != nil {
			item.Source = s.source(r.SessionId, actor, r.Source)
		}
	case "resolve", "dismiss":
		if found == nil {
			return nil, ErrNotFound
		}
		item = found.Item
		if r.Title != "" || r.Detail != "" || r.Kind != "" || r.Source != nil {
			return nil, ErrInvalid
		}
		if r.Action == "resolve" {
			item.State = "resolved"
		} else {
			item.State = "dismissed"
		}
	default:
		return nil, ErrInvalid
	}
	if found != nil {
		old := findItem(current, item.Id)
		if proto.Equal(old, item) {
			return copyPB(old), nil
		}
		if item.Title != old.Title || item.Detail != old.Detail || item.Kind != old.Kind || !proto.Equal(item.Source, old.Source) {
			item.Unread = true
		}
	}
	item.UpdatedAt = nextTime(item.UpdatedAt)
	if err := s.save(r.SessionId, d, []Change{{SessionID: r.SessionId, Item: copyPB(item)}}); err != nil {
		return nil, err
	}
	return copyPB(item), nil
}

// Delete stages the file while the room is deleted. A database failure rolls
// it back; successful deletion tombstones in-flight writes in this process.
// deleteRoom must not re-enter this store.
func (s *Store) Delete(session string, deleteRoom func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validSession.MatchString(session) {
		return ErrInvalid
	}
	path, backup := "", ""
	if s.dir != "" {
		path = filepath.Join(s.dir, session+".json")
		backup = filepath.Join(s.dir, ".delete-"+session+"-"+store.NewID()+".tmp")
		if err := os.Rename(path, backup); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			backup = ""
		}
	}
	if deleteRoom != nil {
		if err := deleteRoom(); err != nil {
			if backup != "" {
				if rollback := os.Rename(backup, path); rollback != nil {
					return errors.Join(err, rollback)
				}
			}
			return err
		}
	}
	s.deleted[session] = true
	delete(s.docs, session)
	delete(s.turns, session)
	if backup != "" {
		return os.Remove(backup)
	}
	return nil
}

func findItem(d *document, id string) *pb.HighlightItem {
	for _, r := range d.Items {
		if r.Item.Id == id {
			return r.Item
		}
	}
	return nil
}
func nextTime(previous int64) int64 { return max(time.Now().UnixMilli(), previous+1) }
func validKind(kind string) bool {
	return kind == "needs_you" || kind == "blocker" || kind == "finding"
}
func validKey(key string) bool         { return key != "" && validText(key, 128, true, true) }
func validKeyOptional(key string) bool { return key == "" || validKey(key) }
func validText(text string, maxRunes int, single, required bool) bool {
	if !utf8.ValidString(text) || utf8.RuneCountInString(text) > maxRunes || (required && strings.TrimSpace(text) == "") {
		return false
	}
	for _, r := range text {
		if r == 0 || (single && (unicode.IsControl(r) || r == '\u2028' || r == '\u2029')) {
			return false
		}
	}
	return true
}
func validSource(source *pb.HighlightSource) bool {
	if source == nil {
		return true
	}
	if source.MessageId != "" && source.TaskId != "" {
		return false
	}
	return validText(source.MessageId, 256, true, false) && validText(source.TaskId, 128, true, false) && validText(source.TurnId, 256, true, false)
}
func (s *Store) source(session string, actor Actor, source *pb.HighlightSource) *pb.HighlightSource {
	if source == nil {
		if actor.User {
			return nil
		}
		source = &pb.HighlightSource{}
	} else {
		source = copyPB(source)
	}
	if !actor.User {
		source.TurnId = actor.Turn
	}
	if source.MessageId == "" && source.TaskId == "" && source.TurnId != "" {
		if ref := s.turns[session][actor.Agent]; ref.Turn == source.TurnId {
			source.MessageId = ref.Message
		}
	}
	return source
}

func (s *Store) Summary(session string, actor Actor, text string, source *pb.HighlightSource) (*pb.AgentSummary, error) {
	text = strings.TrimSpace(text)
	if actor.User || actor.Agent == "" || !validText(text, 280, true, true) || !validSource(source) {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.load(session)
	if err != nil {
		return nil, err
	}
	d := copyDoc(current)
	summary := &pb.AgentSummary{SessionId: session, Agent: actor.Agent, Text: text, Source: s.source(session, actor, source)}
	index := -1
	for i, a := range d.Summaries {
		if a.Agent == actor.Agent {
			index = i
			summary.UpdatedAt = a.UpdatedAt
			break
		}
	}
	if index >= 0 && proto.Equal(d.Summaries[index], summary) {
		return copyPB(summary), nil
	}
	summary.UpdatedAt = nextTime(summary.UpdatedAt)
	if index >= 0 {
		d.Summaries[index] = summary
	} else {
		if len(d.Summaries) >= MaxSummaries {
			return nil, errors.New("this room reached its summary limit")
		}
		d.Summaries = append(d.Summaries, summary)
	}
	if err := s.save(session, d, []Change{{SessionID: session, Summary: copyPB(summary)}}); err != nil {
		return nil, err
	}
	return copyPB(summary), nil
}

// BindTurn links a source stamped before the UI's message existed. It changes
// provenance only: unread/resolved state and content timestamps are preserved.
func (s *Store) BindTurn(session, agent, turn, message string) error {
	if turn == "" || message == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.load(session)
	if err != nil {
		return err
	}
	if s.turns[session] == nil {
		s.turns[session] = map[string]turnRef{}
	}
	s.turns[session][agent] = turnRef{Turn: turn, Message: message}
	d := copyDoc(current)
	var changes []Change
	for _, r := range d.Items {
		item := r.Item
		if item.Agent == agent && item.Source != nil && item.Source.TurnId == turn && item.Source.MessageId == "" && item.Source.TaskId == "" {
			item.Source.MessageId = message
			changes = append(changes, Change{SessionID: session, Item: copyPB(item)})
		}
	}
	for _, a := range d.Summaries {
		if a.Agent == agent && a.Source != nil && a.Source.TurnId == turn && a.Source.MessageId == "" && a.Source.TaskId == "" {
			a.Source.MessageId = message
			changes = append(changes, Change{SessionID: session, Summary: copyPB(a)})
		}
	}
	if len(changes) == 0 {
		return nil
	}
	return s.save(session, d, changes)
}
