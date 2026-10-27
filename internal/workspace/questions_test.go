package workspace

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/app"
	"github.com/dev-dami/deck/internal/db"
	deckagent "github.com/dev-dami/deck/internal/deck/agent"
	decks "github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/message"
	"github.com/dev-dami/deck/internal/question"
	"github.com/dev-dami/deck/internal/session"
)

// newAskWorkspace is a DeckWorkspace with a message store, a question
// service (the terminal UI's form) and one session, "t1"'s.
func newAskWorkspace(t *testing.T) (*DeckWorkspace, *app.App, string) {
	t.Helper()
	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	q := db.New(conn)
	sess, err := session.NewService(q, conn).Create(t.Context(), "s")
	require.NoError(t, err)
	a := &app.App{Messages: message.NewService(q), Sessions: session.NewService(q, conn), Questions: question.NewService()}
	w := newPickerWorkspace(t, pickerCfg())
	w.AppWorkspace = &AppWorkspace{app: a}
	w.back["t1"] = sess.ID
	return w, a, sess.ID
}

func askMsg(id string, got chan<- deckagent.Decision) decks.ApprovalMsg {
	return decks.ApprovalMsg{ThreadID: "t1", ApprovalRequest: deckagent.ApprovalRequest{
		ID: id, AgentID: "claude", Kind: deckagent.ApproveQuestion, ToolName: "AskUserQuestion",
		Questions: []deckagent.Question{
			{ID: "How should I deliver them?", Header: "Delivery", Text: "How should I deliver them?", Other: true,
				Options: []deckagent.QuestionOption{{Label: "Post as comments"}, {Label: "Summary only"}}},
			{ID: "Re-run?", Text: "Re-run?", Multi: true, Options: []deckagent.QuestionOption{{Label: "Lint"}, {Label: "Unit"}}},
		},
		Respond: func(d deckagent.Decision) { got <- d },
	}}
}

// line is the text of the conversation's only system line.
func line(t *testing.T, a *app.App, sid string) string {
	t.Helper()
	msgs, err := a.Messages.List(t.Context(), sid)
	require.NoError(t, err)
	var out []string
	for _, m := range msgs {
		if m.Role == message.System {
			out = append(out, m.Content().Text)
		}
	}
	require.Len(t, out, 1)
	return out[0]
}

func TestQuestionIsHeldUntilAnswered(t *testing.T) {
	w, a, sid := newAskWorkspace(t)
	sub := w.SubscribeDeck(t.Context())
	got := make(chan deckagent.Decision, 1)
	w.onApproval(askMsg("toolu_1", got))

	ch := <-sub
	require.Equal(t, ChangeQuestion, ch.Payload.Kind)
	require.Equal(t, "toolu_1", ch.Payload.Question.ID)
	pend := w.PendingQuestions(sid)
	require.Len(t, pend, 1)
	require.Equal(t, "claude", pend[0].AgentID)
	require.Empty(t, w.PendingQuestions("other"))
	require.Equal(t, "**claude asks:** How should I deliver them? (+1 more)", line(t, a, sid))

	_, err := w.AnswerQuestion("other", "toolu_1", nil, true)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = w.AnswerQuestion(sid, "toolu_1", map[string]deckagent.Answer{}, false)
	require.Error(t, err, "an answer with nothing in it")

	won, err := w.AnswerQuestion(sid, "toolu_1", map[string]deckagent.Answer{
		"How should I deliver them?": {Selected: []string{"Post as comments"}},
		"Re-run?":                    {Selected: []string{"Lint", "Unit"}},
		"not asked":                  {Other: "x"},
	}, false)
	require.NoError(t, err)
	require.True(t, won)
	d := <-got
	require.True(t, d.Allow)
	require.Len(t, d.Answers, 2)
	require.Equal(t, "Lint, Unit", d.Answers["Re-run?"].Text())

	ch = <-sub
	require.Equal(t, ChangeQuestionGone, ch.Payload.Kind)
	require.True(t, ch.Payload.Answered)
	require.Empty(t, w.PendingQuestions(""))
	require.Equal(t, "**claude asked:** How should I deliver them? (+1 more) → You answered: Post as comments; Lint, Unit", line(t, a, sid))

	won, err = w.AnswerQuestion(sid, "toolu_1", nil, true)
	require.ErrorIs(t, err, ErrNotFound)
	require.False(t, won)
}

func TestQuestionDeclinedOrWithdrawn(t *testing.T) {
	w, a, sid := newAskWorkspace(t)
	got := make(chan deckagent.Decision, 1)
	w.onApproval(askMsg("toolu_1", got))
	won, err := w.AnswerQuestion(sid, "toolu_1", nil, true)
	require.NoError(t, err)
	require.True(t, won)
	require.False(t, (<-got).Allow)
	require.Contains(t, line(t, a, sid), "→ You skipped it")

	// The agent withdraws it (its turn stopped): nothing is sent back.
	w2, a2, sid2 := newAskWorkspace(t)
	got2 := make(chan deckagent.Decision, 1)
	w2.onApproval(askMsg("toolu_2", got2))
	w2.settleQuestion("toolu_2", deckagent.Decision{}, true)
	require.Empty(t, w2.PendingQuestions(""))
	require.Contains(t, line(t, a2, sid2), "no answer")
	select {
	case d := <-got2:
		t.Fatalf("answered a withdrawn question: %+v", d)
	default:
	}
}

// The terminal UI's form gets the question too, and its answer settles it.
func TestQuestionAnsweredInTheTerminalForm(t *testing.T) {
	w, a, sid := newAskWorkspace(t)
	reqs := a.Questions.Subscribe(t.Context())
	got := make(chan deckagent.Decision, 1)
	w.onApproval(askMsg("toolu_1", got))
	var req question.Request
	select {
	case ev := <-reqs:
		req = ev.Payload
	case <-time.After(5 * time.Second):
		t.Fatal("the form was not asked")
	}
	require.Len(t, req.Questions, 2)
	require.Equal(t, question.TypeSingleChoice, req.Questions[0].Type)
	require.Equal(t, question.TypeMultiChoice, req.Questions[1].Type)
	require.Equal(t, "Delivery", req.Questions[0].Label)
	q0 := req.Questions[0]
	require.True(t, a.Questions.Answer([]question.Answer{
		{QuestionID: q0.ID, SelectedIDs: []string{q0.Choices[1].ID}},
		{QuestionID: req.Questions[1].ID, FillInText: "only lint"},
	}))
	select {
	case d := <-got:
		require.True(t, d.Allow)
		require.Equal(t, "Summary only", d.Answers["How should I deliver them?"].Text())
		require.Equal(t, "only lint", d.Answers["Re-run?"].Text())
	case <-time.After(5 * time.Second):
		t.Fatal("no answer")
	}
	require.Empty(t, w.PendingQuestions(sid))
}

// Answered elsewhere, the question leaves the terminal form.
func TestQuestionAnsweredElsewhereLeavesTheForm(t *testing.T) {
	w, a, sid := newAskWorkspace(t)
	reqs := a.Questions.Subscribe(t.Context())
	notes := a.Questions.SubscribeNotifications(t.Context())
	got := make(chan deckagent.Decision, 1)
	w.onApproval(askMsg("toolu_1", got))
	<-reqs
	_, err := w.AnswerQuestion(sid, "toolu_1", nil, true)
	require.NoError(t, err)
	select {
	case n := <-notes:
		require.Equal(t, "toolu_1", n.Payload.BatchID)
	case <-time.After(5 * time.Second):
		t.Fatal("the form was not dismissed")
	}
	require.False(t, (<-got).Allow)
}
