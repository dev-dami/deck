package artifacts

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	pb "github.com/dev-dami/deck/pkg/rpc/deckv1"
	"github.com/stretchr/testify/require"
)

func createRequest() *pb.ArtifactActionRequest {
	return &pb.ArtifactActionRequest{SessionId: "room", Action: "create", Key: "calculator", Title: "Calculator", Kind: "react", Entrypoint: "App.tsx", Files: []*pb.ArtifactFileInput{{Path: "App.tsx", Content: "export default function App() { return <div>Hi</div> }"}, {Path: "style.css", Content: "div { color: red }"}}}
}
func createArtifact(t *testing.T, s *Store) *pb.ArtifactActionResponse {
	t.Helper()
	out, err := s.Action(Actor{Agent: "claude", Turn: "turn-one"}, createRequest())
	require.NoError(t, err)
	return out
}
func updateRequest(id string, base uint32) *pb.ArtifactActionRequest {
	r := createRequest()
	r.Action = "update"
	r.Id = id
	r.Key = ""
	r.Kind = ""
	r.BaseRevision = base
	r.Title = "Updated"
	return r
}

func TestSnapshotsCollaborationRestoreAndRestart(t *testing.T) {
	dir := t.TempDir()
	var changes []*pb.ArtifactSummary
	s := New(dir, func(_ string, i *pb.ArtifactSummary) { changes = append(changes, i) })
	created := createArtifact(t, s)
	id := created.Artifact.Id
	require.Equal(t, "claude", created.Revision.Agent)
	require.Equal(t, "turn-one", created.Revision.Source.TurnId)
	r := updateRequest(id, 1)
	r.Files[0].Content = "export default () => <div>Second</div>"
	r.Files = r.Files[:1]
	updated, err := s.Action(Actor{Agent: "codex", Turn: "turn-two"}, r)
	require.NoError(t, err)
	require.Equal(t, uint32(2), updated.Artifact.CurrentRevision)
	require.Equal(t, "codex", updated.Revision.Agent)
	meta, err := s.Read(&pb.ArtifactReadRequest{SessionId: "room", Id: id})
	require.NoError(t, err)
	require.Len(t, meta.Files, 1)
	_, err = s.Action(Actor{Agent: "claude"}, updateRequest(id, 1))
	var c *Conflict
	require.ErrorAs(t, err, &c)
	require.Equal(t, uint32(2), c.Detail.CurrentRevision)
	restored, err := s.Action(Actor{User: true}, &pb.ArtifactActionRequest{SessionId: "room", Action: "restore", Id: id, BaseRevision: 2, RestoreRevision: 1})
	require.NoError(t, err)
	require.Equal(t, uint32(3), restored.Artifact.CurrentRevision)
	require.Equal(t, "Calculator", restored.Artifact.Title)
	require.Equal(t, "user", restored.Revision.Agent)
	require.Equal(t, uint32(1), restored.Revision.RestoredFromRevision)
	s = New(dir, nil)
	meta, err = s.Read(&pb.ArtifactReadRequest{SessionId: "room", Id: id})
	require.NoError(t, err)
	require.Len(t, meta.Files, 2)
	require.Equal(t, uint32(3), meta.Revision)
	first, err := s.Read(&pb.ArtifactReadRequest{SessionId: "room", Id: id, Revision: 1, Path: "App.tsx"})
	require.NoError(t, err)
	require.Equal(t, createRequest().Files[0].Content, first.Content)
	retry := createRequest()
	out, err := s.Action(Actor{Agent: "claude", Turn: "a-later-turn"}, retry)
	require.NoError(t, err)
	require.True(t, out.Replayed)
	require.Equal(t, id, out.Artifact.Id)
	retry.Files[0].Content = "different"
	_, err = s.Action(Actor{Agent: "claude"}, retry)
	require.ErrorAs(t, err, &c)
	require.Equal(t, "create_key_reused", c.Detail.Reason)
	require.Len(t, changes, 3)
}

func TestConcurrentBaseRevisionCommitsOnce(t *testing.T) {
	s := New("", nil)
	id := createArtifact(t, s).Artifact.Id
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, name := range []string{"claude", "codex"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			<-start
			_, err := s.Action(Actor{Agent: name}, updateRequest(id, 1))
			results <- err
		}(name)
	}
	close(start)
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else {
			var c *Conflict
			require.ErrorAs(t, err, &c)
			conflicts++
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, conflicts)
}

func TestUTF8PagesReadLimitsAndIsolation(t *testing.T) {
	s := New("", nil)
	r := createRequest()
	body := strings.Repeat("a🙂é", 10000)
	r.Files[0].Content = body
	idOut, err := s.Action(Actor{User: true}, r)
	require.NoError(t, err)
	id := idOut.Artifact.Id
	var joined strings.Builder
	var offset uint32
	for {
		page, err := s.Read(&pb.ArtifactReadRequest{SessionId: "room", Id: id, Revision: 1, Path: "App.tsx", Offset: offset, Limit: 2048})
		require.NoError(t, err)
		require.LessOrEqual(t, len(page.Content), 2048)
		joined.WriteString(page.Content)
		require.Greater(t, page.NextOffset, offset)
		offset = page.NextOffset
		if page.Eof {
			break
		}
	}
	require.Equal(t, body, joined.String())
	large, err := s.Read(&pb.ArtifactReadRequest{SessionId: "room", Id: id, Path: "App.tsx", Limit: MaxRead})
	require.NoError(t, err)
	require.Equal(t, body, large.Content)
	_, err = s.Read(&pb.ArtifactReadRequest{SessionId: "room", Id: id, Path: "App.tsx", Offset: 2})
	require.ErrorIs(t, err, ErrInvalid)
	_, err = s.Read(&pb.ArtifactReadRequest{SessionId: "room", Id: id, Path: "App.tsx", Limit: MaxRead + 1})
	require.ErrorIs(t, err, ErrInvalid)
	_, err = s.Read(&pb.ArtifactReadRequest{SessionId: "other", Id: id})
	require.ErrorIs(t, err, ErrNotFound)
	_, err = s.Read(&pb.ArtifactReadRequest{SessionId: "../room", Id: id})
	require.ErrorIs(t, err, ErrInvalid)
}

func TestPathsLimitsAndDefensiveCopies(t *testing.T) {
	for _, bad := range []string{"../App.tsx", "/App.tsx", "src/../App.tsx", "src//App.tsx", "C:/App.tsx", `src\App.tsx`, "http://example/App.tsx", "src/./App.tsx", "App.exe"} {
		t.Run(bad, func(t *testing.T) {
			r := createRequest()
			r.Entrypoint = bad
			r.Files[0].Path = bad
			_, err := New("", nil).Action(Actor{User: true}, r)
			require.ErrorIs(t, err, ErrInvalid)
		})
	}
	s := New("", nil)
	r := createRequest()
	r.Files[0].Content = strings.Repeat("x", MaxFileBytes+1)
	_, err := s.Action(Actor{User: true}, r)
	require.ErrorIs(t, err, ErrLimit)
	r = createRequest()
	r.Files = append(r.Files, r.Files[0])
	_, err = s.Action(Actor{User: true}, r)
	require.ErrorIs(t, err, ErrInvalid)
	r = createRequest()
	created, err := s.Action(Actor{User: true}, r)
	require.NoError(t, err)
	id := created.Artifact.Id
	r.Files[0].Content = "caller mutation"
	created.Artifact.Title = "caller mutation"
	page, err := s.Read(&pb.ArtifactReadRequest{SessionId: "room", Id: id, Path: "App.tsx"})
	require.NoError(t, err)
	require.Equal(t, createRequest().Files[0].Content, page.Content)
	meta, err := s.Read(&pb.ArtifactReadRequest{SessionId: "room", Id: id})
	require.NoError(t, err)
	meta.Files[0].Path = "mutated"
	again, err := s.Read(&pb.ArtifactReadRequest{SessionId: "room", Id: id})
	require.NoError(t, err)
	require.Equal(t, "App.tsx", again.Files[0].Path)
}

func TestBuildFeedbackStaleAttemptsAndSourceHistory(t *testing.T) {
	dir := t.TempDir()
	changes := 0
	s := New(dir, func(string, *pb.ArtifactSummary) { changes++ })
	created := createArtifact(t, s)
	id := created.Artifact.Id
	r := &pb.ReportArtifactBuildRequest{SessionId: "room", Id: id, Revision: 1, ReportId: "r2", RuntimeVersion: "react-19.2.0", ClientInstance: "desktop", Attempt: 2, Phase: "build", Diagnostics: []*pb.ArtifactDiagnostic{{File: "App.tsx", Line: 3, Column: 1, Message: "Expected closing tag"}}}
	_, err := s.Report(r)
	require.NoError(t, err)
	meta, err := s.Read(&pb.ArtifactReadRequest{SessionId: "room", Id: id})
	require.NoError(t, err)
	require.Len(t, meta.Builds, 1)
	require.Equal(t, "Expected closing tag", meta.Builds[0].Diagnostics[0].Message)
	r.Attempt = 1
	r.ReportId = "late"
	r.Ok = true
	r.Diagnostics = nil
	old, err := s.Report(r)
	require.NoError(t, err)
	require.False(t, old.Ok)
	require.Equal(t, uint32(2), old.Attempt)
	require.Equal(t, 2, changes)
	r.Attempt = 3
	r.ReportId = "r3"
	_, err = s.Report(r)
	require.NoError(t, err)
	list, err := s.List(&pb.ArtifactsRequest{SessionId: "room"})
	require.NoError(t, err)
	require.Equal(t, "ok", list.Items[0].BuildStatus)
	require.Equal(t, uint32(1), list.Items[0].CurrentRevision)
	require.Equal(t, created.Artifact.UpdatedAt, list.Items[0].UpdatedAt)
	require.Equal(t, uint32(3), list.Items[0].StateVersion)
	r.Phase = "runtime"
	r.Ok = false
	r.ReportId = "runtime"
	r.Diagnostics = []*pb.ArtifactDiagnostic{{File: "<runtime>", Message: "Unhandled exception"}}
	_, err = s.Report(r)
	require.NoError(t, err)
	meta, err = New(dir, nil).Read(&pb.ArtifactReadRequest{SessionId: "room", Id: id})
	require.NoError(t, err)
	require.Len(t, meta.Builds, 2)
	r.Revision = 0
	_, err = s.Report(r)
	require.ErrorIs(t, err, ErrInvalid)
	r.Revision = 1
	r.Diagnostics[0].File = "../secret"
	_, err = s.Report(r)
	require.ErrorIs(t, err, ErrInvalid)
}

func TestWriteFailureAndDeleteRollbackKeepPriorState(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, nil)
	id := createArtifact(t, s).Artifact.Id
	err := s.Delete("room", func() error { return errors.New("database failure") })
	require.ErrorContains(t, err, "database failure")
	_, err = New(dir, nil).Read(&pb.ArtifactReadRequest{SessionId: "room", Id: id})
	require.NoError(t, err)
	badDir := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(badDir, []byte("x"), 0600))
	s.dir = badDir
	_, err = s.Action(Actor{User: true}, updateRequest(id, 1))
	require.Error(t, err)
	meta, err := s.Read(&pb.ArtifactReadRequest{SessionId: "room", Id: id})
	require.NoError(t, err)
	require.Equal(t, uint32(1), meta.Revision)
	s.dir = dir
	require.NoError(t, s.Delete("room", func() error { return nil }))
	_, err = s.Action(Actor{User: true}, createRequest())
	require.ErrorIs(t, err, ErrNotFound)
	_, err = os.Stat(filepath.Join(dir, "room.json"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestLoadRejectsCorruptSnapshotsAndCursorScope(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, nil)
	id := createArtifact(t, s).Artifact.Id
	r := createRequest()
	r.Key = "second"
	_, err := s.Action(Actor{User: true}, r)
	require.NoError(t, err)
	list, err := s.List(&pb.ArtifactsRequest{SessionId: "room", Limit: 1})
	require.NoError(t, err)
	require.Len(t, list.Items, 1)
	require.NotEmpty(t, list.NextCursor)
	_, err = s.List(&pb.ArtifactsRequest{SessionId: "other", Cursor: list.NextCursor})
	require.ErrorIs(t, err, ErrInvalid)
	next, err := s.List(&pb.ArtifactsRequest{SessionId: "room", Cursor: list.NextCursor, Limit: 1})
	require.NoError(t, err)
	require.Len(t, next.Items, 1)
	require.Empty(t, next.NextCursor)
	b, err := os.ReadFile(filepath.Join(dir, "room.json"))
	require.NoError(t, err)
	var d document
	require.NoError(t, json.Unmarshal(b, &d))
	d.Items[0].Revisions[0].Files[0].Path = "../secret"
	b, err = json.Marshal(d)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "room.json"), b, 0600))
	_, err = New(dir, nil).Read(&pb.ArtifactReadRequest{SessionId: "room", Id: id})
	require.ErrorIs(t, err, ErrInvalid)
}

func TestBindTurnLinksExistingAndFutureRevisions(t *testing.T) {
	s := New("", nil)
	id := createArtifact(t, s).Artifact.Id
	require.NoError(t, s.BindTurn("room", "claude", "turn-one", "message-one"))
	meta, err := s.Read(&pb.ArtifactReadRequest{SessionId: "room", Id: id})
	require.NoError(t, err)
	require.Equal(t, "message-one", meta.Info.Source.MessageId)
	r := createRequest()
	r.Key = "next"
	out, err := s.Action(Actor{Agent: "claude", Turn: "turn-one"}, r)
	require.NoError(t, err)
	require.Equal(t, "message-one", out.Revision.Source.MessageId)
	require.ErrorIs(t, s.BindTurn("room", "claude", "turn-one", strings.Repeat("x", 257)), ErrInvalid)
}

func TestTitlesCannotInjectUnicodeLineBreaks(t *testing.T) {
	s := New("", nil)
	r := createRequest()
	r.Title = "First\u2028Second"
	_, err := s.Action(Actor{User: true}, r)
	require.ErrorIs(t, err, ErrInvalid)
	r.Title = "First\u2029Second"
	_, err = s.Action(Actor{User: true}, r)
	require.ErrorIs(t, err, ErrInvalid)
}
