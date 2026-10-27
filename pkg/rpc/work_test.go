package rpc

import (
	"errors"
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dev-dami/deck/internal/deck/changes"
	decks "github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/deck/store"
	subs "github.com/dev-dami/deck/internal/deck/sub"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// fakeWork is a workspace with rooms and a work desk that records what it
// is asked.
type fakeWork struct {
	*fakeRooms
	asked []string
	limit int
}

func (f *fakeWork) Trees(string) ([]decks.WorktreeInfo, error) {
	return []decks.WorktreeInfo{{Name: "s1-big", Branch: "deck/s1-big", Kind: decks.TreeAgent, OwnerName: "codex", Ahead: 3, Behind: 1,
		Files: []string{"a.go"}, Created: time.UnixMilli(1000)}}, nil
}

func (f *fakeWork) NamedCapsules(string) ([]decks.CapsuleView, error) {
	return []decks.CapsuleView{{State: "open", CapsuleRec: store.CapsuleRec{Name: "s1-big", Source: "worktree",
		Versions: []store.CapsuleVersion{{ID: "blake3:x", Files: 2, Commits: []store.CommitRef{{SHA: "abc", Subject: "fix"}}}}}}}, nil
}

func (f *fakeWork) CreateTree(_ string, sp decks.TreeSpec) (decks.WorktreeInfo, error) {
	f.asked = append(f.asked, "create "+sp.Name+" "+sp.Base)
	return decks.WorktreeInfo{Name: "s1-" + sp.Name}, nil
}

func (f *fakeWork) SyncTree(_, name string, rebase bool) (decks.WorktreeInfo, error) {
	return decks.WorktreeInfo{Name: name, BaseRef: "main"}, &changes.ConflictError{Paths: []string{"a.go"}}
}

func (f *fakeWork) LandTree(_, name, into string, check bool, strategy string) (decks.LandResult, error) {
	res := decks.LandResult{Capsule: name, Version: 1, Checked: check, Files: []subs.Preview{
		{Op: subs.Op{Path: "a.go", Before: "x", After: "y"}, State: subs.StateConflict, Reason: "both sides changed the same lines"},
		{Op: subs.Op{Path: "b.go", After: "y"}, State: subs.StateFits},
	}}
	if strategy == "" && !check {
		return res, &subs.ConflictError{Paths: []string{"a.go"}}
	}
	return res, nil
}

func (f *fakeWork) DiscardTree(_, name string) error {
	f.asked = append(f.asked, "discard "+name)
	return nil
}

func (f *fakeWork) SpawnInTree(_, name, kind, brief string) (string, error) { return kind + "-2", nil }

func (f *fakeWork) CreateCapsule(_ string, sp decks.CapsuleSpec) (string, int, error) {
	if sp.Source == "bogus" {
		return "", 0, errors.New("a capsule comes from worktree, commits or diff")
	}
	return "commits-1", 1, nil
}

func (f *fakeWork) LandCapsule(_, ref, into string, check bool, strategy string) (decks.LandResult, error) {
	return f.LandTree("", ref, into, check, strategy)
}

func (f *fakeWork) RebaseCapsule(_, ref, onto string) (decks.LandResult, error) {
	return decks.LandResult{Capsule: ref, Version: 2}, nil
}

func (f *fakeWork) RevertCapsule(_, ref, from string) error { return nil }

func (f *fakeWork) HandCapsule(_, ref, agent, text string) error {
	f.asked = append(f.asked, "hand "+ref+" "+agent)
	return nil
}

func (f *fakeWork) ReviewCapsule(_, ref, path, comment, tests string) error {
	f.asked = append(f.asked, "review "+ref+" "+tests)
	return nil
}

func (f *fakeWork) CapsuleFiles(_, ref string, offset, limit int) ([]subs.FileStat, int, error) {
	f.limit = limit
	return []subs.FileStat{{Op: subs.Op{Path: "a.go", Before: "x", After: "y"}, Added: 3, Removed: 1}}, 900, nil
}

func (f *fakeWork) CapsuleFileDiff(_, ref, path string) (string, error) { return "--- a/" + path, nil }

func TestWorkCalls(t *testing.T) {
	f := &fakeWork{fakeRooms: newFakeRooms()}
	_, c := workspaceRig(t, f, "")
	ctx := ctxT(t)

	info, err := c.Info(ctx, &deckv1.Empty{})
	if err != nil || !slices.Contains(info.GetCapabilities(), "CapsuleFiles") {
		t.Fatalf("capabilities = %v %v", info.GetCapabilities(), err)
	}
	w, err := c.Work(ctx, &deckv1.SessionRef{SessionId: "s1"})
	if err != nil || len(w.GetWorktrees()) != 1 || w.GetWorktrees()[0].GetAhead() != 3 || w.GetCapsules()[0].GetVersions()[0].GetCommits()[0].GetSha() != "abc" {
		t.Fatalf("work = %+v %v", w, err)
	}

	// A sync conflict and a refused merge are answers, with the files.
	r, err := c.WorktreeAction(ctx, &deckv1.WorktreeActionRequest{SessionId: "s1", Name: "s1-big", Action: "sync"})
	if err != nil || !slices.Equal(r.GetConflicts(), []string{"a.go"}) {
		t.Fatalf("sync = %+v %v", r, err)
	}
	r, err = c.WorktreeAction(ctx, &deckv1.WorktreeActionRequest{SessionId: "s1", Name: "s1-big", Action: "merge"})
	if err != nil || len(r.GetConflicts()) != 1 || r.GetFiles()[0].GetState() != "conflict" || r.GetFiles()[1].GetKind() != "add" {
		t.Fatalf("merge = %+v %v", r, err)
	}
	if r, err := c.WorktreeAction(ctx, &deckv1.WorktreeActionRequest{SessionId: "s1", Name: "x", Action: "spawn", Kind: "codex", Brief: "go"}); err != nil || r.GetAgentId() != "codex-2" {
		t.Fatalf("spawn = %+v %v", r, err)
	}
	if _, err := c.WorktreeAction(ctx, &deckv1.WorktreeActionRequest{SessionId: "s1", Action: "sync"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("no name: %v", err)
	}
	if _, err := c.WorktreeAction(ctx, &deckv1.WorktreeActionRequest{SessionId: "s1", Name: "x", Action: "explode"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("unknown action: %v", err)
	}

	if r, err := c.CapsuleAction(ctx, &deckv1.CapsuleActionRequest{SessionId: "s1", Action: "create", Source: "commits", Range: "HEAD~2..HEAD"}); err != nil || r.GetCapsule() != "commits-1" {
		t.Fatalf("create = %+v %v", r, err)
	}
	if _, err := c.CapsuleAction(ctx, &deckv1.CapsuleActionRequest{SessionId: "s1", Action: "create", Source: "bogus"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("bad source: %v", err)
	}
	if _, err := c.CapsuleAction(ctx, &deckv1.CapsuleActionRequest{SessionId: "s1", Capsule: "c", Action: "hand", Agent: "codex"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CapsuleAction(ctx, &deckv1.CapsuleActionRequest{SessionId: "s1", Capsule: "c", Action: "review", Tests: "pass"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.asked, []string{"hand c codex", "review c pass"}) {
		t.Fatalf("asked %q", f.asked)
	}

	files, err := c.CapsuleFiles(ctx, &deckv1.CapsuleFilesRequest{SessionId: "s1", Capsule: "c", Limit: 10000})
	if err != nil || files.GetTotal() != 900 || f.limit != maxCapsuleFiles || files.GetFiles()[0].GetAdded() != 3 {
		t.Fatalf("files = %+v limit %d %v", files, f.limit, err)
	}
	if d, err := c.CapsuleFiles(ctx, &deckv1.CapsuleFilesRequest{SessionId: "s1", Capsule: "c", Path: "a.go"}); err != nil || d.GetDiff() != "--- a/a.go" {
		t.Fatalf("diff = %+v %v", d, err)
	}
}
