package rpc

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	deckconfig "github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/workspace"
	"github.com/dev-dami/deck/pkg/remote/wire"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// Attention values of Session.attention.
const (
	attendApproval = "approval"
	attendQuestion = "question"
	attendError    = "error"
	attendBlocked  = "blocked"
)

// roomMethods are the optional calls a workspace with rooms serves; Info
// lists them in capabilities.
var roomMethods = []string{
	"IsolateSession", "ApplyIsolated", "UndoApply", "UnisolateSession", "RevertFiles", "UndoRevert",
	"ArchiveSession", "UnarchiveSession", "StartRace", "ReadImage", "SearchMessages",
}

const (
	// maxImageRead is the largest image ReadImage returns.
	maxImageRead = 10 << 20
	defaultHits  = 30
	maxHits      = 200
)

// maxRaceCount only keeps a request from asking for a huge slice; the
// engine has its own, lower, limit and says so.
const maxRaceCount = 100

// raceEntrants are the agents a StartRace asks for: its entrants, or count
// agents of kind from an older client.
func raceEntrants(r *deckv1.StartRaceRequest) []workspace.RaceEntrant {
	var out []workspace.RaceEntrant
	for _, e := range r.GetEntrants() {
		out = append(out, workspace.RaceEntrant{Agent: e.GetAgent(), Model: e.GetModel(), Effort: e.GetEffort()})
	}
	if len(out) == 0 {
		for range r.GetCount() {
			out = append(out, workspace.RaceEntrant{Agent: r.GetKind()})
		}
	}
	return out
}

// room is what the desktop shows of a session beyond wire.Session.
type room struct {
	attention, branch string
	archived          bool
}

func (r room) fill(p *deckv1.Session) {
	p.Attention, p.Branch, p.Archived = r.attention, r.branch, r.archived
}

// roomOf works out a session's room from its pending approvals, its agents,
// its task board and its thread.
func (s *Server) roomOf(sid string, approvals []wire.Approval) room {
	var r room
	if s.crew != nil && s.crew.rooms != nil {
		w := s.crew.rooms.Room(sid)
		r.branch, r.archived = w.Branch, w.Archived
	}
	r.attention = s.attention(sid, approvals)
	return r
}

// attention is what the session waits for from the person, the most
// pressing first.
func (s *Server) attention(sid string, approvals []wire.Approval) string {
	asks := false
	for _, a := range approvals {
		if a.Kind == wire.ApprovalCommand || a.Kind == wire.ApprovalEdit {
			return attendApproval
		}
		asks = true
	}
	if asks {
		return attendQuestion
	}
	if s.crew == nil {
		return ""
	}
	if s.crew.roster != nil {
		failed := false
		for _, a := range s.crew.roster.Roster(sid) {
			if a.Status == "approval" {
				return attendApproval
			}
			failed = failed || a.Status == "error"
		}
		if failed {
			return attendError
		}
	}
	if s.crew.orch != nil {
		for _, t := range s.crew.orch.Board(sid).Tasks {
			// A race entry that failed ends with its race, which reports it.
			if !t.Entry && (t.Status == "blocked" || t.Status == "failed") {
				return attendBlocked
			}
		}
	}
	return ""
}

// note fills a session with its room and remembers the room, so recheck
// knows what the desktop was last told.
func (s *Server) note(p *deckv1.Session, approvals []wire.Approval) {
	r := s.roomOf(p.GetId(), approvals)
	r.fill(p)
	s.roomMu.Lock()
	s.rooms[p.GetId()] = r
	s.roomMu.Unlock()
}

// mark notes the room of a session event, or forgets a deleted session.
func (s *Server) mark(e *deckv1.Event) {
	var p *deckv1.Session
	switch k := e.GetKind().(type) {
	case *deckv1.Event_SessionCreated:
		p = k.SessionCreated
	case *deckv1.Event_SessionUpdated:
		p = k.SessionUpdated
	case *deckv1.Event_SessionDeleted:
		s.roomMu.Lock()
		delete(s.rooms, k.SessionDeleted.GetId())
		s.roomMu.Unlock()
		return
	default:
		return
	}
	approvals, _ := s.be.Approvals(p.GetId())
	s.note(p, approvals)
}

// recheck sends session_updated when the session's room changed without a
// session event: an agent's status, or a room call. The lock is held
// throughout, so two rechecks cannot send their answers out of order.
func (s *Server) recheck(ctx context.Context, sid string) {
	s.roomMu.Lock()
	defer s.roomMu.Unlock()
	approvals, _ := s.be.Approvals(sid)
	r := s.roomOf(sid, approvals)
	if s.rooms[sid] == r {
		return
	}
	s.rooms[sid] = r
	all, err := s.be.Sessions(ctx)
	if err != nil {
		return
	}
	for _, v := range all {
		if v.ID == sid {
			p := toSession(v)
			r.fill(p)
			s.feed.publish(&deckv1.Event{SessionId: sid, Kind: &deckv1.Event_SessionUpdated{SessionUpdated: p}})
			return
		}
	}
}

// pumpWorkspace turns workspace state deltas into replayable RPC events.
func (s *Server) pumpWorkspace(ctx context.Context) {
	for ev := range s.crew.changes.SubscribeDeck(ctx) {
		c := ev.Payload
		if c.Kind == workspace.ChangeArtifact && c.Artifact != nil {
			s.feed.publish(&deckv1.Event{SessionId: c.SessionID, Kind: &deckv1.Event_ArtifactChanged{ArtifactChanged: c.Artifact}})
			continue
		}
		if c.Kind == workspace.ChangeHighlight && c.Highlight != nil {
			s.feed.publish(&deckv1.Event{SessionId: c.SessionID, Kind: &deckv1.Event_HighlightChanged{HighlightChanged: c.Highlight}})
			continue
		}
		if c.Kind == workspace.ChangeSummary && c.Summary != nil {
			s.feed.publish(&deckv1.Event{SessionId: c.SessionID, Kind: &deckv1.Event_SummaryChanged{SummaryChanged: c.Summary}})
			continue
		}
		if c.Kind != workspace.ChangeLease {
			continue
		}
		l := c.Lease
		s.feed.publish(&deckv1.Event{SessionId: c.SessionID, Kind: &deckv1.Event_LeaseChanged{LeaseChanged: &deckv1.LeaseChanged{
			SessionId: c.SessionID, Agent: l.Agent, Paths: l.Paths, Exclusive: l.Exclusive, Released: l.Released,
		}}})
	}
}

// clis are the agent CLIs Deck looks for, and where it found them.
func (s *Server) clis() []*deckv1.AgentCli {
	if s.crew == nil || s.crew.settings == nil {
		return nil
	}
	cfg := s.crew.settings.DeckSettings()
	out := make([]*deckv1.AgentCli, 0, len(deckconfig.Kinds))
	for _, k := range deckconfig.Kinds {
		p, err := exec.LookPath(cfg.Binary(k))
		out = append(out, &deckv1.AgentCli{Kind: k, Path: p, Found: err == nil})
	}
	return out
}

func (s *Server) capabilities() []string {
	var out []string
	if s.crew != nil && s.crew.artifacts != nil {
		out = append(out, artifactMethods...)
	}
	if s.crew != nil && s.crew.highlights != nil {
		out = append(out, "Highlights", "HighlightAction")
	}
	if s.browser != nil {
		out = append(out, "BrowserState", "BrowserAction")
	}
	rooms := s.crew != nil && s.crew.rooms != nil
	if rooms {
		out = append(out, roomMethods...)
	}
	if rooms || s.dir != "" {
		out = append(out, "ListFiles")
	}
	if s.crew != nil && s.crew.profiles != nil {
		out = append(out, profileMethods...)
	}
	if s.crew != nil && s.crew.limits != nil {
		out = append(out, limitMethods...)
	}
	if s.crew != nil && s.crew.work != nil {
		out = append(out, workMethods...)
	}
	return out
}

// roomFail keeps the engine's words; an unknown session is NotFound.
func roomFail(err error) error {
	if errors.Is(err, workspace.ErrNotFound) {
		return status.Error(codes.NotFound, "not found")
	}
	return refused(err)
}

// onRoom runs f on the session's thread, then tells the desktop what
// changed in the room.
func (s *Server) onRoom(ctx context.Context, sid string, f func(workspace.Rooms) error) error {
	if s.crew == nil || s.crew.rooms == nil {
		return errNoCrew
	}
	if err := needSession(sid); err != nil {
		return err
	}
	if err := f(s.crew.rooms); err != nil {
		return roomFail(err)
	}
	s.recheck(ctx, sid)
	return nil
}

func (s *Server) IsolateSession(ctx context.Context, r *deckv1.SessionRef) (*deckv1.Empty, error) {
	sid := r.GetSessionId()
	if err := s.onRoom(ctx, sid, func(w workspace.Rooms) error { return w.Isolate(ctx, sid) }); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Server) ApplyIsolated(ctx context.Context, r *deckv1.SessionRef) (*deckv1.FilesResponse, error) {
	sid := r.GetSessionId()
	out := &deckv1.FilesResponse{}
	err := s.onRoom(ctx, sid, func(w workspace.Rooms) (err error) {
		out.Files, err = w.ApplyIsolated(ctx, sid)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Server) UndoApply(ctx context.Context, r *deckv1.SessionRef) (*deckv1.FilesResponse, error) {
	sid := r.GetSessionId()
	out := &deckv1.FilesResponse{}
	err := s.onRoom(ctx, sid, func(w workspace.Rooms) (err error) {
		out.Files, err = w.UndoApply(ctx, sid)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Server) UnisolateSession(ctx context.Context, r *deckv1.SessionRef) (*deckv1.Empty, error) {
	sid := r.GetSessionId()
	if err := s.onRoom(ctx, sid, func(w workspace.Rooms) error { return w.Unisolate(ctx, sid) }); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Server) RevertFiles(ctx context.Context, r *deckv1.RevertFilesRequest) (*deckv1.FilesResponse, error) {
	sid := r.GetSessionId()
	if len(r.GetPaths()) == 0 {
		return nil, invalid("paths are required")
	}
	var since time.Time
	switch ms := r.GetSince(); {
	case ms < 0:
		return nil, invalid("since must not be negative")
	case ms > 0:
		since = time.UnixMilli(ms)
	}
	out := &deckv1.FilesResponse{}
	err := s.onRoom(ctx, sid, func(w workspace.Rooms) (err error) {
		out.Files, err = w.RevertFiles(ctx, sid, since, r.GetPaths())
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Server) UndoRevert(ctx context.Context, r *deckv1.SessionRef) (*deckv1.Empty, error) {
	sid := r.GetSessionId()
	if err := s.onRoom(ctx, sid, func(w workspace.Rooms) error { return w.UndoRevert(ctx, sid) }); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Server) ArchiveSession(ctx context.Context, r *deckv1.SessionRef) (*deckv1.Empty, error) {
	sid := r.GetSessionId()
	if err := s.onRoom(ctx, sid, func(w workspace.Rooms) error { return w.Archive(ctx, sid) }); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Server) UnarchiveSession(ctx context.Context, r *deckv1.SessionRef) (*deckv1.Empty, error) {
	sid := r.GetSessionId()
	if err := s.onRoom(ctx, sid, func(w workspace.Rooms) error { return w.Unarchive(ctx, sid) }); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Server) StartRace(ctx context.Context, r *deckv1.StartRaceRequest) (*deckv1.StartRaceResponse, error) {
	sid := r.GetSessionId()
	if strings.TrimSpace(r.GetBrief()) == "" {
		return nil, invalid("brief is required")
	}
	if len(r.GetEntrants()) == 0 && r.GetKind() == "" {
		return nil, invalid("entrants (or kind and count) are required")
	}
	if n := r.GetCount(); n < 0 || n > maxRaceCount || len(r.GetEntrants()) > maxRaceCount {
		return nil, invalid("count is out of range")
	}
	for _, e := range r.GetEntrants() {
		if strings.TrimSpace(e.GetAgent()) == "" {
			return nil, invalid("each entrant names an agent")
		}
	}
	out := &deckv1.StartRaceResponse{}
	err := s.onRoom(ctx, sid, func(w workspace.Rooms) (err error) {
		out.TaskId, err = w.StartRace(ctx, sid, r.GetBrief(), raceEntrants(r), r.GetJudge())
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ReadImage resolves the image inside the engine from the session and the
// message; the client never names a path.
func (s *Server) ReadImage(ctx context.Context, r *deckv1.ReadImageRequest) (*deckv1.ReadImageResponse, error) {
	if s.crew == nil || s.crew.rooms == nil {
		return nil, errNoCrew
	}
	if err := needSession(r.GetSessionId()); err != nil {
		return nil, err
	}
	if r.GetMessageId() == "" || r.GetIndex() < 0 {
		return nil, invalid("message_id and a non-negative index are required")
	}
	data, mime, err := s.crew.rooms.ReadImage(ctx, r.GetSessionId(), r.GetMessageId(), int(r.GetIndex()))
	if err != nil {
		return nil, roomFail(err)
	}
	if len(data) > maxImageRead {
		return nil, status.Error(codes.ResourceExhausted, "the image is larger than 10 MB")
	}
	return &deckv1.ReadImageResponse{Data: data, Mime: mime}, nil
}

func (s *Server) SearchMessages(ctx context.Context, r *deckv1.SearchMessagesRequest) (*deckv1.SearchMessagesResponse, error) {
	if s.crew == nil || s.crew.rooms == nil {
		return nil, errNoCrew
	}
	if strings.TrimSpace(r.GetQuery()) == "" {
		return nil, invalid("query is required")
	}
	limit := int(r.GetLimit())
	switch {
	case limit < 0:
		return nil, invalid("limit must not be negative")
	case limit == 0:
		limit = defaultHits
	}
	hits, err := s.crew.rooms.SearchMessages(ctx, r.GetQuery(), min(limit, maxHits))
	if err != nil {
		return nil, roomFail(err)
	}
	out := &deckv1.SearchMessagesResponse{Hits: make([]*deckv1.SearchHit, 0, len(hits))}
	for _, h := range hits {
		out.Hits = append(out.Hits, &deckv1.SearchHit{SessionId: h.SessionID, MessageId: h.MessageID, Agent: h.Agent, Snippet: h.Snippet, At: ms(h.At)})
	}
	return out, nil
}
