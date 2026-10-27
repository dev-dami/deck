package adapter

import (
	"errors"

	deckagent "github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/workspace"
	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// toQuestion is an agent's question as the approval a client shows: kind
// question, with the questions themselves. It travels as an approval so
// the lists, counts, notifications and inbox that know approvals know it.
func toQuestion(q workspace.PendingQuestion) wire.Approval {
	a := wire.Approval{
		ID: q.ID, SessionID: q.SessionID, ToolCallID: q.ID, Agent: q.AgentID,
		Kind: wire.ApprovalQuestion, CreatedAt: q.At.UTC(),
	}
	for _, x := range q.Questions {
		wq := wire.Question{ID: x.ID, Header: x.Header, Text: x.Text, MultiSelect: x.Multi, Other: x.Other}
		for _, o := range x.Options {
			wq.Options = append(wq.Options, wire.QuestionOption{Label: o.Label, Description: o.Description})
		}
		a.Questions = append(a.Questions, wq)
	}
	if len(q.Questions) > 0 {
		a.Description = q.AgentID + " asks: " + q.Questions[0].Text
	}
	return a
}

// answerQuestion answers or declines the question id; allow is not an
// answer to a question.
func (b *Backend) answerQuestion(sessionID, id, decision string, answers []wire.QuestionAnswer) (bool, error) {
	var by map[string]deckagent.Answer
	switch decision {
	case wire.DecisionDeny:
	case wire.DecisionAnswer:
		by = make(map[string]deckagent.Answer, len(answers))
		for _, a := range answers {
			by[a.ID] = deckagent.Answer{Selected: a.Selected, Other: a.Other}
		}
	default:
		if b.hasQuestion(sessionID, id) {
			return false, remote.ErrInvalid
		}
		return false, remote.ErrNotFound
	}
	won, err := b.asks.AnswerQuestion(sessionID, id, by, decision == wire.DecisionDeny)
	switch {
	case errors.Is(err, workspace.ErrNotFound):
		return false, remote.ErrNotFound
	case err != nil:
		return false, &wire.Error{Code: wire.CodeInvalid, Msg: err.Error()}
	}
	return won, nil
}

func (b *Backend) hasQuestion(sessionID, id string) bool {
	for _, q := range b.asks.PendingQuestions(sessionID) {
		if q.ID == id {
			return true
		}
	}
	return false
}
