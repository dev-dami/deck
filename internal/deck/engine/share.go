package engine

import (
	"context"
	"time"

	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/internal/deck/session"
)

// sharing serves this Deck's shared threads to teammates over the peer
// link (peer.Shared); the session checks each teammate's role.
type sharing struct{ mgr *session.Manager }

var _ peer.Shared = sharing{}

func sharedThread(st session.SharedThread) peer.SharedThread {
	return peer.SharedThread{ID: st.ID, Title: st.Title, Role: st.Role, Mode: st.Mode, Members: st.Members, Entries: st.Entries}
}

func (s sharing) List(name string) []peer.SharedThread {
	var out []peer.SharedThread
	for _, st := range s.mgr.SharedWith(name) {
		out = append(out, sharedThread(st))
	}
	return out
}

func (s sharing) Read(ctx context.Context, name, thread string, after int, wait time.Duration) (peer.Mirror, error) {
	mr, err := s.mgr.ReadShared(ctx, name, thread, after, wait)
	if err != nil {
		return peer.Mirror{}, err
	}
	out := peer.Mirror{Thread: sharedThread(mr.Thread), Lines: mr.Lines, Next: mr.Next}
	for _, p := range mr.Pending {
		out.Pending = append(out.Pending, peer.PendingApproval{ID: p.ID, Agent: p.Agent, Kind: p.Kind, Summary: p.Summary})
	}
	return out, nil
}

func (s sharing) Post(name, thread, text string) error { return s.mgr.PostShared(name, thread, text) }

func (s sharing) Answer(name, thread, approval string, allow bool) error {
	return s.mgr.AnswerShared(name, thread, approval, allow)
}
