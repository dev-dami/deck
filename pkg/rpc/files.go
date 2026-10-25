package rpc

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/sahilm/fuzzy"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dev-dami/deck/internal/fsext"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

const (
	defaultFiles = 100
	maxFiles     = 500
	// maxWalk bounds the walk of a very large tree; the query narrows what
	// the picker shows.
	maxWalk = 50000
)

// ListFiles lists the thread's tree, its worktree when isolated, through
// the ignore rules the agents' own listing uses (.gitignore, .deckignore
// and the usual build and cache folders).
func (s *Server) ListFiles(_ context.Context, r *deckv1.ListFilesRequest) (*deckv1.ListFilesResponse, error) {
	if err := needSession(r.GetSessionId()); err != nil {
		return nil, err
	}
	limit := int(r.GetLimit())
	switch {
	case limit < 0:
		return nil, invalid("limit must not be negative")
	case limit == 0:
		limit = defaultFiles
	}
	limit = min(limit, maxFiles)
	dir := s.dir
	if s.crew != nil && s.crew.rooms != nil {
		if d := s.crew.rooms.Room(r.GetSessionId()).Dir; d != "" {
			dir = d
		}
	}
	if dir == "" {
		return nil, status.Error(codes.FailedPrecondition, "this Deck has no project folder")
	}
	paths, truncated, err := listFiles(dir, r.GetQuery(), limit)
	if err != nil {
		return nil, fail(err)
	}
	return &deckv1.ListFilesResponse{Paths: paths, Truncated: truncated}, nil
}

// listFiles returns up to limit files under dir, relative to it with
// forward slashes: those that match query, best match first, or all of
// them in order when query is empty. truncated says more would match.
func listFiles(dir, query string, limit int) ([]string, bool, error) {
	found, cut, err := fsext.ListDirectory(dir, nil, 0, maxWalk)
	if err != nil {
		return nil, false, err
	}
	files := make([]string, 0, len(found))
	for _, p := range found {
		if strings.HasSuffix(p, string(filepath.Separator)) {
			continue // a folder
		}
		if rel, err := filepath.Rel(dir, p); err == nil {
			files = append(files, filepath.ToSlash(rel))
		}
	}
	if q := strings.TrimSpace(query); q != "" {
		matches := fuzzy.Find(q, files)
		files = make([]string, len(matches))
		for i, m := range matches {
			files[i] = m.Str
		}
	} else {
		slices.Sort(files)
	}
	if len(files) > limit {
		return files[:limit], true, nil
	}
	return files, cut, nil
}
