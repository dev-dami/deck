package rpc

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dev-dami/deck/internal/deck/changes"
	decks "github.com/dev-dami/deck/internal/deck/session"
	subs "github.com/dev-dami/deck/internal/deck/sub"
	"github.com/dev-dami/deck/internal/workspace"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// workMethods are the optional calls a workspace with a work desk serves.
var workMethods = []string{"Work", "WorktreeAction", "CapsuleAction", "CapsuleFiles"}

// Paging of CapsuleFiles.
const (
	defaultCapsuleFiles = 100
	maxCapsuleFiles     = 500
)

func (s *Server) workDesk() (workspace.WorkDesk, error) {
	if s.crew == nil || s.crew.work == nil {
		return nil, status.Error(codes.Unimplemented, "this Deck does not handle worktrees and capsules")
	}
	return s.crew.work, nil
}

func toWorktree(in decks.WorktreeInfo) *deckv1.WorktreeView {
	return &deckv1.WorktreeView{
		Name: in.Name, Branch: in.Branch, Path: in.Path, Kind: in.Kind, Owner: in.Owner, OwnerName: in.OwnerName, Task: in.Task,
		Working: in.Working, Created: ms(in.Created), Fork: in.Fork, BaseRef: in.BaseRef,
		Ahead: int32(in.Ahead), Behind: int32(in.Behind), Dirty: int32(in.Dirty), Files: in.Files,
		LandedAt: ms(in.LandedAt), Landed: in.Landed, Error: in.Err,
	}
}

func toCapsule(c decks.CapsuleView) *deckv1.CapsuleView {
	out := &deckv1.CapsuleView{
		Name: c.Name, Source: c.Source, Worktree: c.Worktree, Authors: c.Authors, Note: c.Note, State: c.State,
		Tests: c.Tests, TestNote: c.TestNote, Created: ms(c.Created), Updated: ms(c.Updated),
	}
	for _, v := range c.Versions {
		pv := &deckv1.CapsuleVersionView{Id: v.ID, Fork: v.Fork, Head: v.Head, CommitsTotal: int32(v.CommitsTotal), Onto: v.Onto,
			Files: int32(v.Files), Add: int32(v.Add), Mod: int32(v.Mod), Del: int32(v.Del), By: v.By, Note: v.Note, Created: ms(v.Created)}
		for _, cm := range v.Commits {
			pv.Commits = append(pv.Commits, &deckv1.CapsuleCommit{Sha: cm.SHA, Subject: cm.Subject, Author: cm.Author})
		}
		out.Versions = append(out.Versions, pv)
	}
	for _, cm := range c.Comments {
		out.Comments = append(out.Comments, &deckv1.CapsuleComment{By: cm.By, Path: cm.Path, Text: cm.Text, At: ms(cm.At)})
	}
	for _, l := range c.Landings {
		out.Landings = append(out.Landings, &deckv1.CapsuleLanding{Version: int32(l.Version), Into: l.Into, At: ms(l.At), By: l.By, Reverted: l.Reverted})
	}
	return out
}

// landed is a land or check as an answer: a conflict is one, not an error.
func landed(res decks.LandResult, err error) (*deckv1.WorkActionResponse, error) {
	out := &deckv1.WorkActionResponse{Capsule: res.Capsule, Version: int32(res.Version), Checked: res.Checked, Conflicts: res.Conflicts()}
	for _, p := range res.Files {
		out.Files = append(out.Files, &deckv1.LandFile{Path: p.Path, Kind: p.Kind(), State: string(p.State), Reason: p.Reason})
	}
	var ce *subs.ConflictError
	if err != nil && !errors.As(err, &ce) {
		return nil, roomFail(err)
	}
	return out, nil
}

func (s *Server) Work(_ context.Context, r *deckv1.SessionRef) (*deckv1.WorkResponse, error) {
	d, err := s.workDesk()
	if err != nil {
		return nil, err
	}
	if err := needSession(r.GetSessionId()); err != nil {
		return nil, err
	}
	trees, err := d.Trees(r.GetSessionId())
	if err != nil {
		return nil, roomFail(err)
	}
	caps, err := d.NamedCapsules(r.GetSessionId())
	if err != nil {
		return nil, roomFail(err)
	}
	return &deckv1.WorkResponse{Worktrees: each(trees, toWorktree), Capsules: each(caps, toCapsule)}, nil
}

func (s *Server) WorktreeAction(_ context.Context, r *deckv1.WorktreeActionRequest) (*deckv1.WorkActionResponse, error) {
	d, err := s.workDesk()
	if err != nil {
		return nil, err
	}
	sid, name := r.GetSessionId(), strings.TrimSpace(r.GetName())
	if err := needSession(sid); err != nil {
		return nil, err
	}
	if name == "" && r.GetAction() != "create" {
		return nil, invalid("say which worktree")
	}
	switch r.GetAction() {
	case "create":
		in, err := d.CreateTree(sid, decks.TreeSpec{Name: name, Base: strings.TrimSpace(r.GetBase()), Task: strings.TrimSpace(r.GetTask())})
		if err != nil {
			return nil, roomFail(err)
		}
		return &deckv1.WorkActionResponse{Worktree: toWorktree(in), Text: "made " + in.Name}, nil
	case "sync", "rebase":
		in, err := d.SyncTree(sid, name, r.GetAction() == "rebase")
		var ce *changes.ConflictError
		if errors.As(err, &ce) {
			return &deckv1.WorkActionResponse{Worktree: toWorktree(in), Conflicts: ce.Paths, Text: "conflicts with " + in.BaseRef + "; nothing changed"}, nil
		}
		if err != nil {
			return nil, roomFail(err)
		}
		return &deckv1.WorkActionResponse{Worktree: toWorktree(in), Text: "synced with " + in.BaseRef}, nil
	case "check", "merge":
		return landed(d.LandTree(sid, name, strings.TrimSpace(r.GetInto()), r.GetAction() == "check", r.GetStrategy()))
	case "discard":
		return &deckv1.WorkActionResponse{Text: "discarded " + name}, roomFail(d.DiscardTree(sid, name))
	case "spawn":
		if strings.TrimSpace(r.GetBrief()) == "" {
			return nil, invalid("the agent needs a brief")
		}
		id, err := d.SpawnInTree(sid, name, strings.TrimSpace(r.GetKind()), r.GetBrief())
		if err != nil {
			return nil, roomFail(err)
		}
		return &deckv1.WorkActionResponse{AgentId: id, Text: "started " + id + " in " + name}, nil
	}
	return nil, invalid("unknown worktree action " + r.GetAction())
}

func (s *Server) CapsuleAction(_ context.Context, r *deckv1.CapsuleActionRequest) (*deckv1.WorkActionResponse, error) {
	d, err := s.workDesk()
	if err != nil {
		return nil, err
	}
	sid, ref := r.GetSessionId(), strings.TrimSpace(r.GetCapsule())
	if err := needSession(sid); err != nil {
		return nil, err
	}
	if ref == "" && r.GetAction() != "create" {
		return nil, invalid("say which capsule")
	}
	into := strings.TrimSpace(r.GetInto())
	if into == "main" {
		into = ""
	}
	switch r.GetAction() {
	case "create":
		name, ver, err := d.CreateCapsule(sid, decks.CapsuleSpec{Name: strings.TrimSpace(r.GetName()), Source: strings.TrimSpace(r.GetSource()),
			Worktree: strings.TrimSpace(r.GetWorktree()), Range: strings.TrimSpace(r.GetRange()), Note: strings.TrimSpace(r.GetText())})
		if err != nil {
			return nil, roomFail(err)
		}
		return &deckv1.WorkActionResponse{Capsule: name, Version: int32(ver)}, nil
	case "check", "land":
		return landed(d.LandCapsule(sid, ref, into, r.GetAction() == "check", r.GetStrategy()))
	case "rebase":
		return landed(d.RebaseCapsule(sid, ref, strings.TrimSpace(r.GetOnto())))
	case "revert":
		return &deckv1.WorkActionResponse{Capsule: ref}, roomFail(d.RevertCapsule(sid, ref, into))
	case "hand":
		if strings.TrimSpace(r.GetAgent()) == "" {
			return nil, invalid("say which agent")
		}
		return &deckv1.WorkActionResponse{Capsule: ref}, roomFail(d.HandCapsule(sid, ref, strings.TrimSpace(r.GetAgent()), r.GetText()))
	case "review":
		return &deckv1.WorkActionResponse{Capsule: ref}, roomFail(d.ReviewCapsule(sid, ref, strings.TrimSpace(r.GetPath()), r.GetText(), strings.TrimSpace(r.GetTests())))
	}
	return nil, invalid("unknown capsule action " + r.GetAction())
}

func (s *Server) CapsuleFiles(_ context.Context, r *deckv1.CapsuleFilesRequest) (*deckv1.CapsuleFilesResponse, error) {
	d, err := s.workDesk()
	if err != nil {
		return nil, err
	}
	sid, ref := r.GetSessionId(), strings.TrimSpace(r.GetCapsule())
	if err := needSession(sid); err != nil {
		return nil, err
	}
	if ref == "" {
		return nil, invalid("say which capsule")
	}
	if p := r.GetPath(); p != "" {
		diff, err := d.CapsuleFileDiff(sid, ref, p)
		if err != nil {
			return nil, roomFail(err)
		}
		return &deckv1.CapsuleFilesResponse{Diff: diff}, nil
	}
	limit := int(r.GetLimit())
	if limit <= 0 {
		limit = defaultCapsuleFiles
	}
	files, total, err := d.CapsuleFiles(sid, ref, int(r.GetOffset()), min(limit, maxCapsuleFiles))
	if err != nil {
		return nil, roomFail(err)
	}
	return &deckv1.CapsuleFilesResponse{Total: int32(total), Files: each(files, func(f subs.FileStat) *deckv1.CapsuleFile {
		return &deckv1.CapsuleFile{Path: f.Path, Kind: f.Kind(), Added: int32(f.Added), Removed: int32(f.Removed), Binary: f.Binary}
	})}, nil
}
