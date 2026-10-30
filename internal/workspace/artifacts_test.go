package workspace

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	deckagent "github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/artifacts"
	"github.com/dev-dami/deck/internal/message"
	pb "github.com/dev-dami/deck/pkg/rpc/deckv1"
	"github.com/stretchr/testify/require"
)

func workspaceArtifact(sid string) *pb.ArtifactActionRequest {
	return &pb.ArtifactActionRequest{SessionId: sid, Action: "create", Key: "app", Title: "App", Kind: "react", Entrypoint: "App.tsx", Files: []*pb.ArtifactFileInput{{Path: "App.tsx", Content: strings.Repeat("x", 4096)}}}
}

func TestArtifactWorkspaceSourceScopePlanAndDeletion(t *testing.T) {
	w, a, sid := newAskWorkspace(t)
	w.path = filepath.Join(t.TempDir(), "deck-threads.json")
	tid, err := w.mgr().NewThread(t.TempDir())
	require.NoError(t, err)
	w.threads[sid] = tid
	w.back[tid] = sid
	require.NoError(t, w.mgr().SetMode(tid, deckagent.ModeAsk))
	r := workspaceArtifact(sid)
	r.Action = ""
	r.SessionId = ""
	r.Source = &pb.HighlightSource{TurnId: "forged"}
	raw, err := json.Marshal(r)
	require.NoError(t, err)
	out, err := w.runArtifacts(t.Context(), tid, "claude", "actual-turn", "artifact.create", raw)
	require.NoError(t, err)
	var created pb.ArtifactActionResponse
	require.NoError(t, json.Unmarshal([]byte(out), &created))
	id := created.Artifact.Id
	require.Equal(t, "actual-turn", created.Revision.Source.TurnId)
	msg, err := a.Messages.Create(t.Context(), sid, message.CreateMessageParams{Role: message.Assistant, Model: "claude"})
	require.NoError(t, err)
	w.bindArtifactTurn(sid, "claude", "actual-turn", msg.ID)
	meta, err := w.ArtifactRead(t.Context(), &pb.ArtifactReadRequest{SessionId: sid, Id: id})
	require.NoError(t, err)
	require.Equal(t, msg.ID, meta.Info.Source.MessageId)
	_, err = w.runArtifacts(t.Context(), tid, "codex", "turn", "artifact.read", json.RawMessage(`{"id":"`+id+`","path":"App.tsx","limit":2049}`))
	require.ErrorIs(t, err, artifacts.ErrInvalid)
	page, err := w.runArtifacts(t.Context(), tid, "codex", "turn", "artifact.read", json.RawMessage(`{"id":"`+id+`","path":"App.tsx"}`))
	require.NoError(t, err)
	var read pb.ArtifactReadResponse
	require.NoError(t, json.Unmarshal([]byte(page), &read))
	require.Len(t, read.Content, 1024)
	require.False(t, read.Eof)
	other, err := a.Sessions.Create(t.Context(), "another")
	require.NoError(t, err)
	_, err = w.ArtifactRead(t.Context(), &pb.ArtifactReadRequest{SessionId: other.ID, Id: id})
	require.ErrorIs(t, err, artifacts.ErrNotFound)
	require.NoError(t, w.mgr().SetMode(tid, deckagent.ModePlan))
	_, err = w.ArtifactAction(t.Context(), workspaceArtifact(sid))
	require.ErrorIs(t, err, artifacts.ErrPlan)
	_, err = w.runArtifacts(t.Context(), tid, "codex", "turn", "artifact.create", raw)
	require.ErrorIs(t, err, artifacts.ErrPlan)
	_, err = w.Artifacts(t.Context(), &pb.ArtifactsRequest{SessionId: sid})
	require.NoError(t, err)
	require.NoError(t, w.DeleteSession(t.Context(), sid))
	_, err = w.ArtifactRead(t.Context(), &pb.ArtifactReadRequest{SessionId: sid, Id: id})
	require.ErrorIs(t, err, ErrNotFound)
	reloaded, err := artifacts.New(filepath.Join(filepath.Dir(w.path), "artifacts"), nil).List(&pb.ArtifactsRequest{SessionId: sid})
	require.NoError(t, err)
	require.Empty(t, reloaded.Items)
}

func TestArtifactUserCannotForgeSourceAndHubLabelsRedaction(t *testing.T) {
	w, _, sid := newAskWorkspace(t)
	tid, err := w.mgr().NewThread(t.TempDir())
	require.NoError(t, err)
	w.threads[sid], w.back[tid] = tid, sid
	require.NoError(t, w.mgr().SetMode(tid, deckagent.ModeAsk))
	r := workspaceArtifact(sid)
	r.Source = &pb.HighlightSource{TurnId: "forged"}
	_, err = w.ArtifactAction(t.Context(), r)
	require.ErrorIs(t, err, artifacts.ErrInvalid)
	r.Source = &pb.HighlightSource{MessageId: "missing"}
	_, err = w.ArtifactAction(t.Context(), r)
	require.ErrorIs(t, err, ErrNotFound)
	r.Source = nil
	r.Files[0].Content = strings.Repeat(" ", 1020) + "ghp_" + strings.Repeat("a", 24)
	created, err := w.ArtifactAction(t.Context(), r)
	require.NoError(t, err)
	out, err := w.runArtifacts(t.Context(), tid, "codex", "turn", "artifact.read", json.RawMessage(`{"id":"`+created.Artifact.Id+`","path":"App.tsx"}`))
	require.NoError(t, err)
	var page pb.ArtifactReadResponse
	require.NoError(t, json.Unmarshal([]byte(out), &page))
	require.True(t, page.Redacted)
	require.NotContains(t, page.Content, "ghp_")
	require.Equal(t, uint32(1030), page.TotalBytes)
	full, err := w.ArtifactRead(t.Context(), &pb.ArtifactReadRequest{SessionId: sid, Id: created.Artifact.Id, Path: "App.tsx"})
	require.NoError(t, err)
	require.Contains(t, full.Content, "ghp_")
	require.False(t, full.Redacted)
}
