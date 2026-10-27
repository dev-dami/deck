package adapter

import (
	"context"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	deckagent "github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/pubsub"
	"github.com/dev-dami/deck/internal/workspace"
	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// askRC is a workspace whose agents ask questions.
type askRC struct {
	*fakeRC
	mu       sync.Mutex
	pending  []workspace.PendingQuestion
	answered []string
}

func (r *askRC) PendingQuestions(sid string) []workspace.PendingQuestion {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []workspace.PendingQuestion
	for _, q := range r.pending {
		if sid == "" || q.SessionID == sid {
			out = append(out, q)
		}
	}
	return out
}

func (r *askRC) AnswerQuestion(sid, id string, answers map[string]deckagent.Answer, decline bool) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, q := range r.pending {
		if q.ID == id && q.SessionID == sid {
			r.pending = append(r.pending[:i], r.pending[i+1:]...)
			if decline {
				r.answered = append(r.answered, "decline:"+id)
			} else {
				r.answered = append(r.answered, "answer:"+id+"="+answers["Which way?"].Text())
			}
			return true, nil
		}
	}
	return false, workspace.ErrNotFound
}

func newAskRig(t *testing.T) (*rig, *askRC) {
	t.Helper()
	rc := &askRC{fakeRC: newRC()}
	r := &rig{core: newCore(), rc: rc.fakeRC, app: make(chan pubsub.Event[tea.Msg], 16)}
	events := func(context.Context) <-chan pubsub.Event[tea.Msg] { return r.app }
	r.b = NewWith(r.core, rc, nil, fakeRoster{}, events, "v", "proj")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r.events = r.b.Events(ctx)
	return r, rc
}

var askedQ = workspace.PendingQuestion{
	ID: "toolu_1", SessionID: "s1", AgentID: "claude1", At: time.Unix(100, 0),
	Questions: []deckagent.Question{{ID: "Which way?", Header: "Way", Text: "Which way?", Other: true,
		Options: []deckagent.QuestionOption{{Label: "Left", Description: "the short one"}, {Label: "Right"}}}},
}

func TestQuestionsTravelAsQuestionApprovals(t *testing.T) {
	r, rc := newAskRig(t)
	rc.pending = []workspace.PendingQuestion{askedQ}
	q := askedQ
	r.rc.changes <- pubsub.Event[workspace.DeckChange]{Payload: workspace.DeckChange{Kind: workspace.ChangeQuestion, SessionID: "s1", Question: &q}}
	ev := r.next(t)
	require.Equal(t, wire.EvApprovalRequested, ev.Name)
	a := ev.Payload.(wire.Approval)
	require.Equal(t, wire.ApprovalQuestion, a.Kind)
	require.Equal(t, "claude1", a.Agent)
	require.Equal(t, "claude1 asks: Which way?", a.Description)
	require.Equal(t, []wire.Question{{ID: "Which way?", Header: "Way", Text: "Which way?", Other: true,
		Options: []wire.QuestionOption{{Label: "Left", Description: "the short one"}, {Label: "Right"}}}}, a.Questions)
	ev = r.next(t)
	require.Equal(t, wire.EvSessionUpdated, ev.Name)
	require.Equal(t, 1, ev.Payload.(wire.Session).Pending, "a question counts as pending")

	// A reconnecting window lists it.
	list, err := r.b.Approvals("s1")
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "toolu_1", list[0].ID)
	list, _ = r.b.Approvals("s2")
	require.Empty(t, list)

	// allow does not answer a question; another session's id is not found.
	_, err = r.b.Answer("s1", "toolu_1", wire.DecisionAllow, nil)
	require.ErrorIs(t, err, remote.ErrInvalid)
	_, err = r.b.Answer("s2", "toolu_1", wire.DecisionAnswer, []wire.QuestionAnswer{{ID: "Which way?", Selected: []string{"Left"}}})
	require.ErrorIs(t, err, remote.ErrNotFound)

	won, err := r.b.Answer("s1", "toolu_1", wire.DecisionAnswer, []wire.QuestionAnswer{{ID: "Which way?", Selected: []string{"Left"}, Other: "then up"}})
	require.NoError(t, err)
	require.True(t, won)
	require.Equal(t, []string{"answer:toolu_1=Left, then up"}, rc.answered)

	r.rc.changes <- pubsub.Event[workspace.DeckChange]{Payload: workspace.DeckChange{Kind: workspace.ChangeQuestionGone, SessionID: "s1", Question: &q, Answered: true}}
	ev = r.next(t)
	require.Equal(t, wire.EvApprovalResolved, ev.Name)
	require.Equal(t, wire.ApprovalResolved{ID: "toolu_1", SessionID: "s1", Granted: true}, ev.Payload)
	ev = r.next(t)
	require.Equal(t, 0, ev.Payload.(wire.Session).Pending)
}

func TestQuestionDeclined(t *testing.T) {
	r, rc := newAskRig(t)
	rc.pending = []workspace.PendingQuestion{askedQ}
	won, err := r.b.Answer("s1", "toolu_1", wire.DecisionDeny, nil)
	require.NoError(t, err)
	require.True(t, won)
	require.Equal(t, []string{"decline:toolu_1"}, rc.answered)
	_, err = r.b.Answer("s1", "toolu_1", wire.DecisionDeny, nil)
	require.ErrorIs(t, err, remote.ErrNotFound)
}
