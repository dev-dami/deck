// Package artifacts stores room-scoped, immutable source snapshots. It never
// executes source or resolves a file name against the host filesystem.
package artifacts

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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
	MaxArtifacts     = 64
	MaxFiles         = 32
	MaxFileBytes     = 256 << 10
	MaxRevisionBytes = 1 << 20
	MaxRevisions     = 128
	MaxDocumentBytes = 16 << 20
	MaxHubRead       = 2 << 10
	MaxRead          = 256 << 10
	maxPageBytes     = 12 << 10
)

var (
	ErrInvalid   = errors.New("invalid artifact request")
	ErrNotFound  = errors.New("artifact or revision not found in this room")
	ErrLimit     = errors.New("artifact limit exceeded")
	ErrPlan      = errors.New("plan mode permits artifact reads, not source mutations")
	validSession = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
	validPath    = regexp.MustCompile(`^[A-Za-z0-9_./-]{1,160}$`)
)

// Conflict is also carried as a typed detail on an Aborted gRPC status.
type Conflict struct{ Detail *pb.ArtifactConflict }

func (c *Conflict) Error() string {
	b, _ := json.Marshal(c.Detail)
	return "artifact_conflict: " + string(b)
}

// Actor is authenticated by the engine, never read from tool arguments.
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
func (a Actor) name() string {
	if a.User {
		return "user"
	}
	return a.Agent
}

type revision struct {
	Info   *pb.ArtifactRevisionInfo  `json:"info"`
	Files  []*pb.ArtifactFileInput   `json:"files"`
	Builds []*pb.ArtifactBuildReport `json:"builds,omitempty"`
}
type record struct {
	Summary   *pb.ArtifactSummary `json:"summary"`
	Owner     string              `json:"owner"`
	Key       string              `json:"key"`
	Hash      string              `json:"hash"`
	Revisions []*revision         `json:"revisions"`
}
type document struct {
	Version int       `json:"version"`
	Items   []*record `json:"items"`
}
type turnRef struct{ turn, message string }
type Store struct {
	mu      sync.Mutex
	dir     string
	docs    map[string]*document
	deleted map[string]bool
	turns   map[string]map[string]turnRef
	changed func(string, *pb.ArtifactSummary)
}

// New with an empty directory is an explicitly ephemeral store for tests.
// changed runs after persistence, under the mutation lock; it must not reenter.
func New(dir string, changed func(string, *pb.ArtifactSummary)) *Store {
	return &Store{dir: dir, docs: map[string]*document{}, deleted: map[string]bool{}, turns: map[string]map[string]turnRef{}, changed: changed}
}
func copyPB[T proto.Message](p T) T { return proto.Clone(p).(T) }
func copyDoc(d *document) *document {
	b, _ := json.Marshal(d)
	var out document
	_ = json.Unmarshal(b, &out)
	return &out
}
func text(s string, n int, single, required bool) bool {
	if !utf8.ValidString(s) || len(s) > n || (required && strings.TrimSpace(s) == "") {
		return false
	}
	for _, r := range s {
		if r == 0 || (single && (unicode.IsControl(r) || r == '\u2028' || r == '\u2029')) {
			return false
		}
	}
	return true
}
func validSource(p *pb.HighlightSource) bool {
	return p == nil || (text(p.MessageId, 256, true, false) && text(p.TaskId, 128, true, false) && text(p.TurnId, 256, true, false) && !(p.MessageId != "" && p.TaskId != ""))
}
func validKind(k string) bool { return k == "markdown" || k == "html" || k == "svg" || k == "react" }
func filePath(p string) bool {
	if !validPath.MatchString(p) || strings.HasPrefix(p, "/") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return mimeOf(p) != ""
}
func mimeOf(p string) string {
	switch path.Ext(p) {
	case ".md":
		return "text/markdown"
	case ".html":
		return "text/html"
	case ".css":
		return "text/css"
	case ".js", ".jsx":
		return "text/javascript"
	case ".ts", ".tsx":
		return "text/typescript"
	case ".json":
		return "application/json"
	case ".svg":
		return "image/svg+xml"
	}
	return ""
}
func validateFiles(kind, entry string, files []*pb.ArtifactFileInput) error {
	if !validKind(kind) || !filePath(entry) || len(files) == 0 {
		return ErrInvalid
	}
	if len(files) > MaxFiles {
		return ErrLimit
	}
	ext := path.Ext(entry)
	if !(kind == "markdown" && ext == ".md" || kind == "html" && ext == ".html" || kind == "svg" && ext == ".svg" || kind == "react" && (ext == ".jsx" || ext == ".tsx")) {
		return ErrInvalid
	}
	seen, total, found := map[string]bool{}, 0, false
	for _, f := range files {
		if f == nil || !filePath(f.Path) || seen[f.Path] || !utf8.ValidString(f.Content) || strings.ContainsRune(f.Content, 0) {
			return ErrInvalid
		}
		if len(f.Content) > MaxFileBytes {
			return ErrLimit
		}
		seen[f.Path] = true
		total += len(f.Content)
		found = found || f.Path == entry
	}
	if total > MaxRevisionBytes {
		return ErrLimit
	}
	if !found {
		return ErrInvalid
	}
	return nil
}
func (s *Store) load(sid string) (*document, error) {
	if !validSession.MatchString(sid) {
		return nil, ErrInvalid
	}
	if s.deleted[sid] {
		return nil, ErrNotFound
	}
	if d := s.docs[sid]; d != nil {
		return d, nil
	}
	d := &document{Version: 1}
	if s.dir != "" {
		f, err := os.Open(filepath.Join(s.dir, sid+".json"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if err == nil {
			defer f.Close()
			b, err := io.ReadAll(io.LimitReader(f, MaxDocumentBytes+1))
			if err != nil {
				return nil, err
			}
			if len(b) > MaxDocumentBytes {
				return nil, ErrLimit
			}
			if err = json.Unmarshal(b, d); err != nil {
				return nil, fmt.Errorf("read artifacts: %w", err)
			}
			if err = validateDocument(sid, d); err != nil {
				return nil, err
			}
		}
	}
	s.docs[sid] = d
	return d, nil
}
func validateDocument(sid string, d *document) error {
	if d.Version != 1 || len(d.Items) > MaxArtifacts {
		return ErrInvalid
	}
	ids, keys := map[string]bool{}, map[string]bool{}
	for _, r := range d.Items {
		if r == nil || r.Summary == nil {
			return ErrInvalid
		}
		i := r.Summary
		key := r.Owner + "\x00" + r.Key
		if !validSession.MatchString(i.Id) || i.SessionId != sid || ids[i.Id] || keys[key] || !text(r.Owner, 256, true, true) || !text(r.Key, 128, true, true) || len(r.Hash) != 64 || !validKind(i.Kind) || len(r.Revisions) == 0 || len(r.Revisions) > MaxRevisions || i.CurrentRevision != uint32(len(r.Revisions)) || i.StateVersion == 0 {
			return ErrInvalid
		}
		ids[i.Id] = true
		keys[key] = true
		for n, v := range r.Revisions {
			if v == nil || v.Info == nil {
				return ErrInvalid
			}
			p := v.Info
			if p.Revision != uint32(n+1) || !text(p.Title, 160, true, true) || !text(p.Agent, 128, true, true) || !text(p.Note, 512, false, false) || !validSource(p.Source) || p.RestoredFromRevision >= p.Revision || p.CreatedAt <= 0 {
				return ErrInvalid
			}
			if err := validateFiles(i.Kind, p.Entrypoint, v.Files); err != nil {
				return err
			}
			if len(v.Builds) > 2 {
				return ErrInvalid
			}
			phases := map[string]bool{}
			for _, b := range v.Builds {
				if b == nil || phases[b.Phase] {
					return ErrInvalid
				}
				phases[b.Phase] = true
				if err := validateReport(&pb.ReportArtifactBuildRequest{Revision: p.Revision, ReportId: b.ReportId, RuntimeVersion: b.RuntimeVersion, ClientInstance: b.ClientInstance, Attempt: b.Attempt, Phase: b.Phase, Ok: b.Ok, Diagnostics: b.Diagnostics}, v); err != nil {
					return err
				}
			}
			b, _ := json.Marshal(v.Builds)
			if len(b) > 4096 {
				return ErrLimit
			}
		}
		last := r.Revisions[len(r.Revisions)-1].Info
		if i.Title != last.Title || i.Agent != last.Agent || !proto.Equal(i.Source, last.Source) || i.CreatedAt != r.Revisions[0].Info.CreatedAt || i.UpdatedAt != last.CreatedAt {
			return ErrInvalid
		}
	}
	return nil
}
func (s *Store) save(sid string, d *document, changes []*pb.ArtifactSummary) error {
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if len(b) > MaxDocumentBytes {
		return ErrLimit
	}
	if s.dir != "" {
		if err = os.MkdirAll(s.dir, 0700); err != nil {
			return err
		}
		f, err := os.CreateTemp(s.dir, ".artifacts-*.tmp")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		if _, err = f.Write(b); err != nil {
			_ = f.Close()
			return err
		}
		if err = f.Sync(); err != nil {
			_ = f.Close()
			return err
		}
		if err = f.Close(); err != nil {
			return err
		}
		if err = os.Rename(f.Name(), filepath.Join(s.dir, sid+".json")); err != nil {
			return err
		}
	}
	s.docs[sid] = d
	if s.changed != nil {
		for _, i := range changes {
			s.changed(sid, copyPB(i))
		}
	}
	return nil
}
func find(d *document, id string) (*record, error) {
	for _, r := range d.Items {
		if r.Summary.Id == id {
			return r, nil
		}
	}
	return nil, ErrNotFound
}
func getRevision(r *record, n uint32) (*revision, error) {
	if n == 0 {
		n = r.Summary.CurrentRevision
	}
	if n > uint32(len(r.Revisions)) {
		return nil, ErrNotFound
	}
	return r.Revisions[n-1], nil
}
func conflict(r *record, base uint32, reason string) error {
	return &Conflict{&pb.ArtifactConflict{Id: r.Summary.Id, BaseRevision: base, CurrentRevision: r.Summary.CurrentRevision, Reason: reason}}
}
func (s *Store) source(sid string, a Actor, p *pb.HighlightSource) *pb.HighlightSource {
	if p != nil {
		p = copyPB(p)
	}
	if !a.User {
		if p == nil {
			p = &pb.HighlightSource{}
		}
		p.TurnId = a.Turn
		if ref := s.turns[sid][a.Agent]; ref.turn == a.Turn && p.MessageId == "" && p.TaskId == "" {
			p.MessageId = ref.message
		}
	}
	return p
}

// Action atomically commits a full snapshot, or returns the durable original
// create on an identical retry. Caller checks plan mode and primary sources.
func (s *Store) Action(a Actor, req *pb.ArtifactActionRequest) (*pb.ArtifactActionResponse, error) {
	if req == nil || (!a.User && !text(a.Agent, 128, true, true)) || !text(a.Turn, 256, true, false) {
		return nil, ErrInvalid
	}
	r := copyPB(req)
	r.Title = strings.TrimSpace(r.Title)
	r.Note = strings.TrimSpace(r.Note)
	if !text(r.Note, 512, false, false) || !validSource(r.Source) {
		return nil, ErrInvalid
	}
	switch r.Action {
	case "create":
		if r.Id != "" || r.BaseRevision != 0 || r.RestoreRevision != 0 || !text(r.Key, 128, true, true) || !text(r.Title, 160, true, true) {
			return nil, ErrInvalid
		}
		if err := validateFiles(r.Kind, r.Entrypoint, r.Files); err != nil {
			return nil, err
		}
	case "update":
		if r.Id == "" || r.BaseRevision == 0 || r.Key != "" || r.Kind != "" || r.RestoreRevision != 0 || !text(r.Title, 160, true, true) {
			return nil, ErrInvalid
		}
	case "restore":
		if r.Id == "" || r.BaseRevision == 0 || r.RestoreRevision == 0 || r.Key != "" || r.Title != "" || r.Kind != "" || r.Entrypoint != "" || len(r.Files) > 0 || r.Source != nil {
			return nil, ErrInvalid
		}
	default:
		return nil, ErrInvalid
	}
	slices.SortFunc(r.Files, func(a, b *pb.ArtifactFileInput) int {
		if a == nil || b == nil {
			return 0
		}
		return strings.Compare(a.Path, b.Path)
	})
	// The idempotency hash excludes identity stamps that change between turns.
	fingerprint := copyPB(r)
	if fingerprint.Source != nil {
		fingerprint.Source.TurnId = ""
		if proto.Equal(fingerprint.Source, &pb.HighlightSource{}) {
			fingerprint.Source = nil
		}
	}
	data, _ := json.Marshal(fingerprint)
	digest := sha256.Sum256(data)
	hash := hex.EncodeToString(digest[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.load(r.SessionId)
	if err != nil {
		return nil, err
	}
	d := copyDoc(current)
	var rec *record
	if r.Action == "create" {
		for _, v := range d.Items {
			if v.Owner == a.owner() && v.Key == r.Key {
				if v.Hash != hash {
					return nil, conflict(v, 0, "create_key_reused")
				}
				return &pb.ArtifactActionResponse{Artifact: copyPB(v.Summary), Revision: copyPB(v.Revisions[0].Info), Replayed: true}, nil
			}
		}
		if len(d.Items) >= MaxArtifacts {
			return nil, ErrLimit
		}
		rec = &record{Owner: a.owner(), Key: r.Key, Hash: hash, Summary: &pb.ArtifactSummary{Id: store.NewID(), SessionId: r.SessionId, Kind: r.Kind}}
		d.Items = append(d.Items, rec)
	} else {
		rec, err = find(d, r.Id)
		if err != nil {
			return nil, err
		}
		if rec.Summary.CurrentRevision != r.BaseRevision {
			return nil, conflict(rec, r.BaseRevision, "stale_revision")
		}
		if len(rec.Revisions) >= MaxRevisions {
			return nil, ErrLimit
		}
		if r.Action == "restore" {
			old, err := getRevision(rec, r.RestoreRevision)
			if err != nil {
				return nil, err
			}
			r.Title, r.Entrypoint = old.Info.Title, old.Info.Entrypoint
			r.Files = old.Files
		} else if err := validateFiles(rec.Summary.Kind, r.Entrypoint, r.Files); err != nil {
			return nil, err
		}
	}
	at := max(time.Now().UnixMilli(), rec.Summary.UpdatedAt+1)
	info := &pb.ArtifactRevisionInfo{Revision: uint32(len(rec.Revisions) + 1), Title: r.Title, Entrypoint: r.Entrypoint, Agent: a.name(), Source: s.source(r.SessionId, a, r.Source), CreatedAt: at, Note: r.Note, RestoredFromRevision: r.RestoreRevision}
	rec.Revisions = append(rec.Revisions, &revision{Info: info, Files: r.Files})
	i := rec.Summary
	i.Title, i.Agent, i.Source = info.Title, info.Agent, info.Source
	i.CurrentRevision = info.Revision
	i.UpdatedAt = at
	if i.CreatedAt == 0 {
		i.CreatedAt = at
	}
	i.StateVersion++
	i.BuildStatus = ""
	if err = s.save(r.SessionId, d, []*pb.ArtifactSummary{i}); err != nil {
		return nil, err
	}
	return &pb.ArtifactActionResponse{Artifact: copyPB(i), Revision: copyPB(info)}, nil
}

func pageStart(sid, cursor string, limit uint32, n int) (int, int, error) {
	if len(cursor) > 512 {
		return 0, 0, ErrInvalid
	}
	if limit == 0 {
		limit = 10
	}
	if limit > 20 {
		return 0, 0, ErrInvalid
	}
	start := 0
	if cursor != "" {
		b, e := base64.RawURLEncoding.DecodeString(cursor)
		if e != nil {
			return 0, 0, ErrInvalid
		}
		prefix := sid + "\x00"
		if !strings.HasPrefix(string(b), prefix) {
			return 0, 0, ErrInvalid
		}
		start, e = strconv.Atoi(strings.TrimPrefix(string(b), prefix))
		if e != nil || start < 0 || start > n {
			return 0, 0, ErrInvalid
		}
	}
	return start, min(n, start+int(limit)), nil
}
func cursorAt(sid string, n int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(sid + "\x00" + strconv.Itoa(n)))
}
func (s *Store) List(r *pb.ArtifactsRequest) (*pb.ArtifactsResponse, error) {
	if r == nil {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.load(r.SessionId)
	if err != nil {
		return nil, err
	}
	start, end, err := pageStart(r.SessionId, r.Cursor, r.Limit, len(d.Items))
	if err != nil {
		return nil, err
	}
	out := &pb.ArtifactsResponse{}
	n := start
	for ; n < end; n++ {
		item := copyPB(d.Items[n].Summary)
		b, _ := json.Marshal(item)
		existing, _ := json.Marshal(out)
		if len(existing)+len(b) > maxPageBytes {
			break
		}
		out.Items = append(out.Items, item)
	}
	if n < len(d.Items) {
		out.NextCursor = cursorAt(r.SessionId, n)
	}
	return out, nil
}
func (s *Store) Revisions(r *pb.ArtifactRevisionsRequest) (*pb.ArtifactRevisionsResponse, error) {
	if r == nil {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.load(r.SessionId)
	if err != nil {
		return nil, err
	}
	rec, err := find(d, r.Id)
	if err != nil {
		return nil, err
	}
	key := r.SessionId + "/" + r.Id
	start, end, err := pageStart(key, r.Cursor, r.Limit, len(rec.Revisions))
	if err != nil {
		return nil, err
	}
	out := &pb.ArtifactRevisionsResponse{}
	n := start
	for ; n < end; n++ {
		item := copyPB(rec.Revisions[n].Info)
		b, _ := json.Marshal(item)
		existing, _ := json.Marshal(out)
		if len(existing)+len(b) > maxPageBytes {
			break
		}
		out.Items = append(out.Items, item)
	}
	if n < len(rec.Revisions) {
		out.NextCursor = cursorAt(key, n)
	}
	return out, nil
}
func (s *Store) Read(r *pb.ArtifactReadRequest) (*pb.ArtifactReadResponse, error) {
	return s.ReadFiltered(r, nil)
}

// ReadFiltered applies an engine-owned redactor to the whole file before
// pagination, so a credential cannot escape by straddling page boundaries.
// Offsets/total_bytes then describe the filtered view and Redacted marks it.
func (s *Store) ReadFiltered(r *pb.ArtifactReadRequest, filter func(string) string) (*pb.ArtifactReadResponse, error) {
	if r == nil || r.Limit > MaxRead {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.load(r.SessionId)
	if err != nil {
		return nil, err
	}
	rec, err := find(d, r.Id)
	if err != nil {
		return nil, err
	}
	rev, err := getRevision(rec, r.Revision)
	if err != nil {
		return nil, err
	}
	out := &pb.ArtifactReadResponse{Revision: rev.Info.Revision, Path: r.Path}
	if r.Path == "" {
		if r.Offset != 0 || r.Limit != 0 {
			return nil, ErrInvalid
		}
		out.Info = copyPB(rev.Info)
		out.Eof = true
		for _, f := range rev.Files {
			out.Files = append(out.Files, &pb.ArtifactFileInfo{Path: f.Path, MediaType: mimeOf(f.Path), SizeBytes: uint32(len(f.Content))})
		}
		for _, b := range rev.Builds {
			out.Builds = append(out.Builds, copyPB(b))
		}
		return out, nil
	}
	for _, f := range rev.Files {
		if f.Path == r.Path {
			content := f.Content
			if filter != nil {
				content = filter(content)
				out.Redacted = content != f.Content
			}
			start := int(r.Offset)
			if start > len(content) || start < len(content) && !utf8.RuneStart(content[start]) {
				return nil, ErrInvalid
			}
			limit := r.Limit
			if limit == 0 {
				limit = 64 << 10
			}
			end := min(len(content), start+int(limit))
			for end > start && end < len(content) && !utf8.RuneStart(content[end]) {
				end--
			}
			if end == start && start < len(content) {
				return nil, ErrInvalid
			}
			out.Content = content[start:end]
			out.NextOffset = uint32(end)
			out.TotalBytes = uint32(len(content))
			out.Eof = end == len(content)
			return out, nil
		}
	}
	return nil, ErrNotFound
}

func validateReport(r *pb.ReportArtifactBuildRequest, v *revision) error {
	if r == nil || r.Revision == 0 || !text(r.ReportId, 128, true, true) || !text(r.RuntimeVersion, 128, true, true) || !text(r.ClientInstance, 128, true, true) || r.Attempt == 0 || (r.Phase != "build" && r.Phase != "runtime") || len(r.Diagnostics) > 8 || (r.Ok && len(r.Diagnostics) > 0) {
		return ErrInvalid
	}
	for _, diag := range r.Diagnostics {
		if diag == nil || !text(diag.Message, 512, false, true) {
			return ErrInvalid
		}
		if diag.File != "" && diag.File != "<build>" && diag.File != "<runtime>" && diag.File != "<generated>" && !slices.ContainsFunc(v.Files, func(f *pb.ArtifactFileInput) bool { return f.Path == diag.File }) {
			return ErrInvalid
		}
	}
	return nil
}
func (s *Store) Report(r *pb.ReportArtifactBuildRequest) (*pb.ArtifactBuildReport, error) {
	if r == nil {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.load(r.SessionId)
	if err != nil {
		return nil, err
	}
	d := copyDoc(current)
	rec, err := find(d, r.Id)
	if err != nil {
		return nil, err
	}
	rev, err := getRevision(rec, r.Revision)
	if err != nil {
		return nil, err
	}
	if err = validateReport(r, rev); err != nil {
		return nil, err
	}
	for _, old := range rev.Builds {
		if old.ReportId == r.ReportId || old.ClientInstance == r.ClientInstance && (old.Attempt > r.Attempt || old.Phase == r.Phase && old.Attempt == r.Attempt) {
			return copyPB(old), nil
		}
	}
	value := &pb.ArtifactBuildReport{ReportId: r.ReportId, RuntimeVersion: r.RuntimeVersion, ClientInstance: r.ClientInstance, Attempt: r.Attempt, Phase: r.Phase, Ok: r.Ok, ReceivedAt: time.Now().UnixMilli()}
	for _, diag := range r.Diagnostics {
		value.Diagnostics = append(value.Diagnostics, copyPB(diag))
	}
	rev.Builds = slices.DeleteFunc(rev.Builds, func(b *pb.ArtifactBuildReport) bool {
		return b.RuntimeVersion != r.RuntimeVersion || b.Phase == r.Phase || r.Phase == "build" && b.ClientInstance == r.ClientInstance && b.Attempt < r.Attempt
	})
	rev.Builds = append(rev.Builds, value)
	b, _ := json.Marshal(rev.Builds)
	if len(b) > 4096 {
		return nil, ErrLimit
	}
	rec.Summary.StateVersion++
	rec.Summary.BuildStatus = ""
	for _, report := range rec.Revisions[len(rec.Revisions)-1].Builds {
		if !report.Ok {
			rec.Summary.BuildStatus = "error"
			break
		}
		rec.Summary.BuildStatus = "ok"
	}
	if err = s.save(r.SessionId, d, []*pb.ArtifactSummary{rec.Summary}); err != nil {
		return nil, err
	}
	return copyPB(value), nil
}

func (s *Store) BindTurn(sid, agent, turn, message string) error {
	if turn == "" || message == "" {
		return nil
	}
	if !text(agent, 128, true, true) || !text(turn, 256, true, true) || !text(message, 256, true, true) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.load(sid)
	if err != nil {
		return err
	}
	if s.turns[sid] == nil {
		s.turns[sid] = map[string]turnRef{}
	}
	s.turns[sid][agent] = turnRef{turn, message}
	d := copyDoc(current)
	var changes []*pb.ArtifactSummary
	for _, rec := range d.Items {
		changed := false
		for _, rev := range rec.Revisions {
			p := rev.Info
			if p.Agent == agent && p.Source != nil && p.Source.TurnId == turn && p.Source.MessageId == "" && p.Source.TaskId == "" {
				p.Source.MessageId = message
				changed = true
			}
		}
		if changed {
			rec.Summary.Source = copyPB(rec.Revisions[len(rec.Revisions)-1].Info).Source
			rec.Summary.StateVersion++
			changes = append(changes, rec.Summary)
		}
	}
	if len(changes) == 0 {
		return nil
	}
	return s.save(sid, d, changes)
}

// Delete stages the room document and rolls it back if primary room deletion
// fails. Successful deletion tombstones in-flight writes in this process.
func (s *Store) Delete(sid string, deleteRoom func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validSession.MatchString(sid) {
		return ErrInvalid
	}
	file, backup := "", ""
	if s.dir != "" {
		file = filepath.Join(s.dir, sid+".json")
		backup = filepath.Join(s.dir, ".delete-"+sid+"-"+store.NewID()+".tmp")
		if err := os.Rename(file, backup); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			backup = ""
		}
	}
	if deleteRoom != nil {
		if err := deleteRoom(); err != nil {
			if backup != "" {
				if rollback := os.Rename(backup, file); rollback != nil {
					return errors.Join(err, rollback)
				}
			}
			return err
		}
	}
	s.deleted[sid] = true
	delete(s.docs, sid)
	delete(s.turns, sid)
	// The room has been deleted: a leftover staging file is not a failed delete.
	if backup != "" {
		_ = os.Remove(backup)
	}
	return nil
}
