package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dev-dami/deck/internal/deck/artifacts"
	"github.com/dev-dami/deck/internal/deck/hub"
	"github.com/dev-dami/deck/internal/pubsub"
	pb "github.com/dev-dami/deck/pkg/rpc/deckv1"
	"google.golang.org/protobuf/proto"
)

type ArtifactsDesk interface {
	Artifacts(context.Context, *pb.ArtifactsRequest) (*pb.ArtifactsResponse, error)
	ArtifactRevisions(context.Context, *pb.ArtifactRevisionsRequest) (*pb.ArtifactRevisionsResponse, error)
	ArtifactRead(context.Context, *pb.ArtifactReadRequest) (*pb.ArtifactReadResponse, error)
	ArtifactAction(context.Context, *pb.ArtifactActionRequest) (*pb.ArtifactActionResponse, error)
	ReportArtifactBuild(context.Context, *pb.ReportArtifactBuildRequest) (*pb.ArtifactBuildReport, error)
}

var _ ArtifactsDesk = (*DeckWorkspace)(nil)

func (w *DeckWorkspace) artifactStore() *artifacts.Store {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.artifacts == nil {
		dir := ""
		if w.path != "" {
			dir = filepath.Join(filepath.Dir(w.path), "artifacts")
		}
		w.artifacts = artifacts.New(dir, func(sid string, item *pb.ArtifactSummary) {
			w.changes().PublishMustDeliver(context.Background(), pubsub.UpdatedEvent, DeckChange{Kind: ChangeArtifact, SessionID: sid, Artifact: item})
		})
	}
	return w.artifacts
}
func (w *DeckWorkspace) Artifacts(ctx context.Context, r *pb.ArtifactsRequest) (*pb.ArtifactsResponse, error) {
	if r == nil {
		return nil, artifacts.ErrInvalid
	}
	if err := w.topSession(ctx, r.SessionId); err != nil {
		return nil, err
	}
	return w.artifactStore().List(r)
}
func (w *DeckWorkspace) ArtifactRevisions(ctx context.Context, r *pb.ArtifactRevisionsRequest) (*pb.ArtifactRevisionsResponse, error) {
	if r == nil {
		return nil, artifacts.ErrInvalid
	}
	if err := w.topSession(ctx, r.SessionId); err != nil {
		return nil, err
	}
	return w.artifactStore().Revisions(r)
}
func (w *DeckWorkspace) ArtifactRead(ctx context.Context, r *pb.ArtifactReadRequest) (*pb.ArtifactReadResponse, error) {
	if r == nil {
		return nil, artifacts.ErrInvalid
	}
	if err := w.topSession(ctx, r.SessionId); err != nil {
		return nil, err
	}
	return w.artifactStore().Read(r)
}
func (w *DeckWorkspace) ArtifactAction(ctx context.Context, r *pb.ArtifactActionRequest) (*pb.ArtifactActionResponse, error) {
	if r == nil {
		return nil, artifacts.ErrInvalid
	}
	if err := w.topSession(ctx, r.SessionId); err != nil {
		return nil, err
	}
	if w.RemoteMode(r.SessionId) == RemoteModePlan {
		return nil, artifacts.ErrPlan
	}
	r = proto.Clone(r).(*pb.ArtifactActionRequest)
	if r.Source != nil && r.Source.TurnId != "" {
		return nil, artifacts.ErrInvalid
	}
	if _, err := w.highlightSource(ctx, r.SessionId, "", r.Source, false); err != nil {
		return nil, err
	}
	return w.artifactStore().Action(artifacts.Actor{User: true}, r)
}
func (w *DeckWorkspace) ReportArtifactBuild(ctx context.Context, r *pb.ReportArtifactBuildRequest) (*pb.ArtifactBuildReport, error) {
	if r == nil {
		return nil, artifacts.ErrInvalid
	}
	if err := w.topSession(ctx, r.SessionId); err != nil {
		return nil, err
	}
	return w.artifactStore().Report(r)
}
func (w *DeckWorkspace) runArtifacts(ctx context.Context, thread, agent, turn, op string, raw json.RawMessage) (string, error) {
	sid := w.sessionOf(thread)
	if sid == "" {
		return "", ErrNotFound
	}
	if err := w.topSession(ctx, sid); err != nil {
		return "", err
	}
	encode := func(value any, err error) (string, error) {
		if err != nil {
			return "", err
		}
		b, err := json.Marshal(value)
		return string(b), err
	}
	decode := func(value any) error {
		d := json.NewDecoder(strings.NewReader(string(raw)))
		d.DisallowUnknownFields()
		if err := d.Decode(value); err != nil {
			return fmt.Errorf("%w: %v", artifacts.ErrInvalid, err)
		}
		return nil
	}
	switch op {
	case "artifact.list":
		var r pb.ArtifactsRequest
		if err := decode(&r); err != nil {
			return "", err
		}
		r.SessionId = sid
		return encode(w.artifactStore().List(&r))
	case "artifact.revisions":
		var r pb.ArtifactRevisionsRequest
		if err := decode(&r); err != nil {
			return "", err
		}
		r.SessionId = sid
		return encode(w.artifactStore().Revisions(&r))
	case "artifact.read":
		var r pb.ArtifactReadRequest
		if err := decode(&r); err != nil {
			return "", err
		}
		r.SessionId = sid
		if r.Limit > artifacts.MaxHubRead {
			return "", artifacts.ErrInvalid
		}
		if r.Path != "" && r.Limit == 0 {
			r.Limit = 1024
		}
		out, err := w.artifactStore().ReadFiltered(&r, hub.Redact)
		if err != nil {
			return "", err
		}
		// Preserve Deck's credential redaction policy, but never pretend such a
		// page is a byte-exact copy that an agent can blindly write back.
		return encode(struct {
			*pb.ArtifactReadResponse
			Untrusted bool `json:"untrusted"`
		}{out, true}, nil)
	case "artifact.create", "artifact.update", "artifact.restore":
		if w.RemoteMode(sid) == RemoteModePlan {
			return "", artifacts.ErrPlan
		}
		var r pb.ArtifactActionRequest
		if err := decode(&r); err != nil {
			return "", err
		}
		r.SessionId = sid
		r.Action = strings.TrimPrefix(op, "artifact.")
		if _, err := w.highlightSource(ctx, sid, thread, r.Source, false); err != nil {
			return "", err
		}
		return encode(w.artifactStore().Action(artifacts.Actor{Agent: agent, Turn: turn}, &r))
	default:
		return "", artifacts.ErrInvalid
	}
}
func (w *DeckWorkspace) bindArtifactTurn(sid, agent, turn, msg string) {
	if err := w.artifactStore().BindTurn(sid, agent, turn, msg); err != nil {
		w.publish(DeckChange{Kind: ChangeNotice, SessionID: sid, Text: "Could not link an artifact to its source: " + err.Error()})
	}
}
