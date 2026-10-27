package highlights

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	pb "github.com/dev-dami/deck/pkg/rpc/deckv1"
	"github.com/stretchr/testify/require"
)

func add(t *testing.T, s *Store, actor Actor, key string) *pb.HighlightItem {
	t.Helper()
	item, err := s.Action(actor, &pb.HighlightActionRequest{SessionId: "room", Action: "add", Key: key, Kind: "blocker", Title: "Build needs attention"}, "")
	require.NoError(t, err)
	return item
}
func TestHighlightsDedupeReadStateExplicitResolutionAndPersistence(t *testing.T) {
	dir := t.TempDir()
	var events []Change
	s := New(dir, func(c Change) { events = append(events, c) })
	actor := Actor{Agent: "codex", Turn: "turn1"}
	item := add(t, s, actor, "build")
	require.True(t, item.Unread)
	require.Equal(t, "open", item.State)
	require.Equal(t, "codex", item.Agent)
	require.Equal(t, "turn1", item.Source.TurnId)
	_, err := s.Action(Actor{User: true}, &pb.HighlightActionRequest{SessionId: "room", Action: "seen", Ids: []string{item.Id}}, "")
	require.NoError(t, err)
	same := add(t, s, actor, "build")
	require.Equal(t, item.Id, same.Id)
	require.False(t, same.Unread)
	require.Equal(t, "open", same.State)
	require.Len(t, events, 2)
	_, err = s.Action(actor, &pb.HighlightActionRequest{SessionId: "room", Action: "resolve", Id: item.Id}, "")
	require.NoError(t, err)
	same = add(t, s, actor, "build")
	require.Equal(t, "resolved", same.State)
	require.False(t, same.Unread)
	same, err = s.Action(actor, &pb.HighlightActionRequest{SessionId: "room", Action: "update", Id: item.Id, Title: "Build fixed; review deployment"}, "")
	require.NoError(t, err)
	require.True(t, same.Unread)
	require.Equal(t, "resolved", same.State)
	snapshot, err := New(dir, nil).Snapshot("room")
	require.NoError(t, err)
	require.Len(t, snapshot.Items, 1)
	require.Equal(t, same, snapshot.Items[0])
	snapshot.Items[0].Title = "tampered"
	clean, err := s.Snapshot("room")
	require.NoError(t, err)
	require.Equal(t, "Build fixed; review deployment", clean.Items[0].Title)
}

func TestHighlightsOwnershipScopeAndPinnedAuthor(t *testing.T) {
	s := New("", nil)
	codex := Actor{Agent: "codex"}
	claude := Actor{Agent: "claude"}
	first := add(t, s, codex, "build")
	second := add(t, s, claude, "build")
	require.NotEqual(t, first.Id, second.Id)
	_, err := s.Action(claude, &pb.HighlightActionRequest{SessionId: "room", Action: "dismiss", Id: first.Id}, "")
	require.ErrorIs(t, err, ErrForbidden)
	_, err = s.Action(claude, &pb.HighlightActionRequest{SessionId: "other-room", Action: "resolve", Id: first.Id}, "")
	require.ErrorIs(t, err, ErrNotFound)
	pin := &pb.HighlightActionRequest{SessionId: "room", Action: "pin", Key: "arbitrary-client-key", Title: "Review this message", Source: &pb.HighlightSource{MessageId: "message-1"}}
	pinned, err := s.Action(Actor{User: true}, pin, "codex")
	require.NoError(t, err)
	require.Equal(t, "codex", pinned.Agent)
	require.Equal(t, "pin:message-1", pinned.Key)
	again, err := s.Action(Actor{User: true}, pin, "codex")
	require.NoError(t, err)
	require.Equal(t, pinned.Id, again.Id)
	_, err = s.Action(codex, &pb.HighlightActionRequest{SessionId: "room", Action: "resolve", Id: pinned.Id}, "")
	require.ErrorIs(t, err, ErrForbidden)
	_, err = s.Action(Actor{User: true}, &pb.HighlightActionRequest{SessionId: "room", Action: "dismiss", Id: first.Id}, "")
	require.NoError(t, err)
}

func TestHighlightsSeenBatchIsAtomicAndDoesNotResolve(t *testing.T) {
	s := New("", nil)
	one := add(t, s, Actor{Agent: "codex"}, "a")
	two := add(t, s, Actor{Agent: "codex"}, "b")
	_, err := s.Action(Actor{User: true}, &pb.HighlightActionRequest{SessionId: "room", Action: "seen", Ids: []string{one.Id, "another-room-id"}}, "")
	require.ErrorIs(t, err, ErrNotFound)
	before, _ := s.Snapshot("room")
	require.True(t, before.Items[0].Unread)
	_, err = s.Action(Actor{User: true}, &pb.HighlightActionRequest{SessionId: "room", Action: "seen", Ids: []string{one.Id, two.Id}}, "")
	require.NoError(t, err)
	after, _ := s.Snapshot("room")
	for _, item := range after.Items {
		require.False(t, item.Unread)
		require.Equal(t, "open", item.State)
	}
	_, err = s.Action(Actor{Agent: "codex"}, &pb.HighlightActionRequest{SessionId: "room", Action: "seen", Ids: []string{one.Id}}, "")
	require.ErrorIs(t, err, ErrInvalid)
}

func TestHighlightsSummaryAndTurnSourceBindingSurviveReload(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, nil)
	actor := Actor{Agent: "codex", Turn: "real-turn"}
	item := add(t, s, actor, "source")
	summary, err := s.Summary("room", actor, "Waiting for your deployment choice", &pb.HighlightSource{TurnId: "forged-turn"})
	require.NoError(t, err)
	require.Equal(t, "real-turn", summary.Source.TurnId)
	require.NoError(t, s.BindTurn("room", "codex", "real-turn", "original-message"))
	after, err := New(dir, nil).Snapshot("room")
	require.NoError(t, err)
	require.Equal(t, "original-message", after.Items[0].Source.MessageId)
	require.Equal(t, "original-message", after.Summaries[0].Source.MessageId)
	require.Equal(t, item.Unread, after.Items[0].Unread)
	require.Equal(t, item.UpdatedAt, after.Items[0].UpdatedAt)
	require.NoError(t, s.BindTurn("room", "codex", "next-turn", "next-message"))
	fresh := add(t, s, Actor{Agent: "codex", Turn: "next-turn"}, "later")
	require.Equal(t, "next-message", fresh.Source.MessageId)
	again, err := s.Summary("room", actor, "Waiting for your deployment choice", nil)
	require.NoError(t, err)
	require.Equal(t, "real-turn", again.Source.TurnId)
	_, err = s.Summary("room", actor, strings.Repeat("x", 281), nil)
	require.ErrorIs(t, err, ErrInvalid)
	_, err = s.Summary("room", Actor{User: true}, "impersonate", nil)
	require.ErrorIs(t, err, ErrInvalid)
}

func TestHighlightsFailedWriteDoesNotPublishOrChangeState(t *testing.T) {
	dir := t.TempDir()
	events := 0
	s := New(dir, func(Change) { events++ })
	_, err := s.Snapshot("room")
	require.NoError(t, err)
	// A directory at the destination forces atomic replacement to fail.
	require.NoError(t, os.Mkdir(filepath.Join(dir, "room.json"), 0700))
	_, err = s.Action(Actor{Agent: "codex"}, &pb.HighlightActionRequest{SessionId: "room", Action: "add", Key: "k", Title: "Preserve old state"}, "")
	require.Error(t, err)
	require.Zero(t, events)
	state, err := s.Snapshot("room")
	require.NoError(t, err)
	require.Empty(t, state.Items)
	require.NoError(t, os.Remove(filepath.Join(dir, "room.json")))
	add(t, s, Actor{Agent: "codex"}, "k")
	require.Equal(t, 1, events)
}

func TestHighlightsValidateInputsAndRejectCorruptPersistence(t *testing.T) {
	s := New("", nil)
	actor := Actor{Agent: "codex"}
	for _, r := range []*pb.HighlightActionRequest{
		{SessionId: "../outside", Action: "add", Key: "k", Title: "Title"},
		{SessionId: "room", Action: "add", Key: "k", Title: "two\nlines"},
		{SessionId: "room", Action: "add", Key: "k", Title: strings.Repeat("a", 161)},
		{SessionId: "room", Action: "add", Key: "k", Title: "Title", Detail: strings.Repeat("a", 2001)},
		{SessionId: "room", Action: "add", Key: "k", Title: "Title", Kind: "urgent"},
		{SessionId: "room", Action: "add", Key: "k", Title: "Title", Source: &pb.HighlightSource{MessageId: "message", TaskId: "task"}},
	} {
		_, err := s.Action(actor, r, "")
		require.ErrorIs(t, err, ErrInvalid)
	}
	item := add(t, s, actor, "valid")
	_, err := s.Action(actor, &pb.HighlightActionRequest{SessionId: "room", Action: "update", Id: item.Id, Title: " "}, "")
	require.ErrorIs(t, err, ErrInvalid)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "room.json"), []byte(`{"version":1,"items":[{"owner":"agent:codex","item":null}]}`), 0600))
	_, err = New(dir, nil).Snapshot("room")
	require.Error(t, err)
}

func TestHighlightsConcurrentDedupe(t *testing.T) {
	s := New(t.TempDir(), nil)
	var wg sync.WaitGroup
	failures := make(chan error, 20)
	for n := 0; n < 20; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Action(Actor{Agent: "codex", Turn: "turn"}, &pb.HighlightActionRequest{SessionId: "room", Action: "add", Key: "one-key", Title: "One item"}, "")
			if err != nil {
				failures <- err
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	state, err := New(s.dir, nil).Snapshot("room")
	require.NoError(t, err)
	require.Len(t, state.Items, 1)
}

func TestHighlightsRoomDeletionRollbackAndLateWrite(t *testing.T) {
	s := New(t.TempDir(), nil)
	actor := Actor{Agent: "codex"}
	item := add(t, s, actor, "persist")
	failed := errors.New("database deletion failed")
	require.ErrorIs(t, s.Delete("room", func() error { return failed }), failed)
	state, err := New(s.dir, nil).Snapshot("room")
	require.NoError(t, err)
	require.Equal(t, item.Id, state.Items[0].Id)
	require.NoError(t, s.Delete("room", func() error { return nil }))
	_, err = s.Action(actor, &pb.HighlightActionRequest{SessionId: "room", Action: "add", Key: "late", Title: "Late write"}, "")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = os.Stat(filepath.Join(s.dir, "room.json"))
	require.ErrorIs(t, err, os.ErrNotExist)
}
