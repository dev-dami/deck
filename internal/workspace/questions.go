package workspace

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	deckagent "github.com/dev-dami/deck/internal/deck/agent"
	decks "github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/message"
	"github.com/dev-dami/deck/internal/question"
)

// PendingQuestion is a question an agent waits on the person to answer
// (deckagent.ApproveQuestion): Claude's AskUserQuestion, Codex's
// requestUserInput, a Pi extension's select prompt.
type PendingQuestion struct {
	ID        string
	SessionID string
	AgentID   string
	Questions []deckagent.Question
	At        time.Time
}

// QuestionDesk is how a second UI (the desktop, the phone) sees the
// questions agents wait on and answers them. *DeckWorkspace implements it;
// questions are kept until answered, declined or withdrawn, so a window that
// reconnects lists them again.
type QuestionDesk interface {
	PendingQuestions(sessionID string) []PendingQuestion
	// AnswerQuestion answers one, or declines it. resolved is false when
	// another UI answered first; ErrNotFound when id is not pending in the
	// session.
	AnswerQuestion(sessionID, id string, answers map[string]deckagent.Answer, decline bool) (resolved bool, err error)
}

var _ QuestionDesk = (*DeckWorkspace)(nil)

// openQuestion is a pending question and what settles it.
type openQuestion struct {
	PendingQuestion
	respond func(deckagent.Decision)
	line    string             // the conversation line that shows it
	tui     context.CancelFunc // stops its turn in the terminal UI's form
}

// onQuestion holds an agent's question for the person: a line in the
// conversation, the remote's question card, and the terminal UI's form,
// whichever answers first.
func (w *DeckWorkspace) onQuestion(m decks.ApprovalMsg) {
	req := m.ApprovalRequest
	sid := w.sessionOf(m.ThreadID)
	if sid == "" || len(req.Questions) == 0 {
		if req.Respond != nil {
			req.Respond(deckagent.Decision{Allow: false})
		}
		return
	}
	q := &openQuestion{
		PendingQuestion: PendingQuestion{ID: req.ID, SessionID: sid, AgentID: req.AgentID, Questions: req.Questions, At: time.Now()},
		respond:         req.Respond,
	}
	w.dbw.wait(sid)
	if msg, err := w.app.Messages.Create(context.Background(), sid, message.CreateMessageParams{
		Role:     message.System,
		Model:    "deck",
		Provider: "deck",
		Parts:    []message.ContentPart{message.TextContent{Text: questionLine(q.PendingQuestion, "")}, message.Finish{Reason: message.FinishReasonEndTurn, Time: time.Now().Unix()}},
	}); err == nil {
		q.line = msg.ID
	}
	ctx, cancel := context.WithCancel(context.Background())
	q.tui = cancel
	w.mu.Lock()
	if w.questions == nil {
		w.questions = map[string]*openQuestion{}
	}
	w.questions[q.ID] = q
	w.mu.Unlock()
	w.publish(DeckChange{Kind: ChangeQuestion, SessionID: sid, Question: &q.PendingQuestion})
	go w.askTUI(ctx, q.PendingQuestion)
}

// PendingQuestions implements [QuestionDesk]: one session's questions, or
// every session's when sessionID is "", oldest first.
func (w *DeckWorkspace) PendingQuestions(sessionID string) []PendingQuestion {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []PendingQuestion
	for _, q := range w.questions {
		if sessionID == "" || q.SessionID == sessionID {
			out = append(out, q.PendingQuestion)
		}
	}
	slices.SortFunc(out, func(a, b PendingQuestion) int { return a.At.Compare(b.At) })
	return out
}

// AnswerQuestion implements [QuestionDesk].
func (w *DeckWorkspace) AnswerQuestion(sessionID, id string, answers map[string]deckagent.Answer, decline bool) (bool, error) {
	w.mu.Lock()
	q, ok := w.questions[id]
	w.mu.Unlock()
	if !ok || q.SessionID != sessionID {
		return false, ErrNotFound
	}
	d := deckagent.Decision{Allow: !decline}
	if !decline {
		d.Answers = map[string]deckagent.Answer{}
		for _, qq := range q.Questions {
			if a, ok := answers[qq.ID]; ok && a.Text() != "" {
				d.Answers[qq.ID] = a
			}
		}
		if len(d.Answers) == 0 {
			return false, errors.New("answer at least one question, or skip them")
		}
	}
	return w.settleQuestion(id, d, false), nil
}

// settleQuestion answers question id once: the agent hears d, the line
// becomes the record of it and every UI drops its card. gone says the agent
// withdrew it (its turn stopped), so nothing is sent back. It reports
// whether this call settled it.
func (w *DeckWorkspace) settleQuestion(id string, d deckagent.Decision, gone bool) bool {
	w.mu.Lock()
	q, ok := w.questions[id]
	delete(w.questions, id)
	w.mu.Unlock()
	if !ok {
		return false
	}
	q.tui()
	if !gone && q.respond != nil {
		q.respond(d)
	}
	outcome := "skipped"
	switch {
	case gone:
		outcome = "gone"
	case d.Allow:
		outcome = "answered"
	}
	if q.line != "" {
		ctx := context.Background()
		w.dbw.wait(q.SessionID)
		if msg, err := w.app.Messages.Get(ctx, q.line); err == nil {
			msg.Parts = []message.ContentPart{message.TextContent{Text: questionLine(q.PendingQuestion, recordOf(q.Questions, d, outcome))}, message.Finish{Reason: message.FinishReasonEndTurn, Time: time.Now().Unix()}}
			_ = w.app.Messages.Update(ctx, msg)
		}
	}
	w.publish(DeckChange{Kind: ChangeQuestionGone, SessionID: q.SessionID, Question: &q.PendingQuestion, Answered: d.Allow && !gone})
	return true
}

// questionLine is the conversation's line for a question: what the agent
// asks, then, once settled, what came of it.
//
//	claude asks: How should I deliver the reviews?
//	claude asked: How should I deliver the reviews? → You answered: Post as comments
func questionLine(q PendingQuestion, record string) string {
	text := q.Questions[0].Text
	if n := len(q.Questions) - 1; n > 0 {
		text += fmt.Sprintf(" (+%d more)", n)
	}
	if record == "" {
		return "**" + q.AgentID + " asks:** " + text
	}
	return "**" + q.AgentID + " asked:** " + text + " → " + record
}

// recordOf says in one line how a question was settled.
func recordOf(qs []deckagent.Question, d deckagent.Decision, outcome string) string {
	switch outcome {
	case "gone":
		return "no answer: the agent stopped waiting"
	case "skipped":
		return "You skipped it"
	}
	var parts []string
	for _, q := range qs {
		if t := d.Answers[q.ID].Text(); t != "" {
			parts = append(parts, t)
		}
	}
	return "You answered: " + strings.Join(parts, "; ")
}

// askTUI puts a question in the terminal UI's form, one at a time (the form
// holds one). A UI without the form never answers, and ctx ends the wait
// when the question is settled elsewhere.
func (w *DeckWorkspace) askTUI(ctx context.Context, q PendingQuestion) {
	if w.app == nil || w.app.Questions == nil {
		return
	}
	select {
	case w.tuiSlot() <- struct{}{}:
	case <-ctx.Done():
		return
	}
	defer func() { <-w.tuiSlot() }()
	if ctx.Err() != nil {
		return
	}
	req, labels := tuiRequest(q)
	asked := make(chan struct{})
	go func() {
		// Settled elsewhere: dismiss the form. Cancel does that for the
		// question the service holds, ours once Ask has published it.
		select {
		case <-asked:
			return
		case <-ctx.Done():
		}
		for !w.app.Questions.Cancel() {
			select {
			case <-asked:
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()
	answers, err := w.app.Questions.Ask(context.Background(), req)
	close(asked)
	switch {
	case ctx.Err() != nil:
	case err != nil:
		w.settleQuestion(q.ID, deckagent.Decision{Allow: false}, false)
	default:
		d := deckagent.Decision{Allow: true, Answers: map[string]deckagent.Answer{}}
		for _, a := range answers {
			id, ok := labels.questions[a.QuestionID]
			if !ok {
				continue
			}
			var ans deckagent.Answer
			for _, sel := range a.SelectedIDs {
				if l, ok := labels.options[a.QuestionID+"/"+sel]; ok {
					ans.Selected = append(ans.Selected, l)
				}
			}
			ans.Other = strings.TrimSpace(a.FillInText)
			if ans.Text() != "" {
				d.Answers[id] = ans
			}
		}
		if len(d.Answers) == 0 {
			d = deckagent.Decision{Allow: false}
		}
		w.settleQuestion(q.ID, d, false)
	}
}

// tuiSlot is the one place in the terminal UI's question form.
func (w *DeckWorkspace) tuiSlot() chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.tuiForm == nil {
		w.tuiForm = make(chan struct{}, 1)
	}
	return w.tuiForm
}

// tuiLabels maps the terminal form's ids back to the agent's.
type tuiLabels struct {
	questions map[string]string // form question id -> Question.ID
	options   map[string]string // form question id + "/" + choice id -> label
}

// tuiRequest is q as the terminal UI's question form takes it, cut to the
// form's limits.
func tuiRequest(q PendingQuestion) (question.Request, tuiLabels) {
	labels := tuiLabels{questions: map[string]string{}, options: map[string]string{}}
	req := question.Request{ID: q.ID, SessionID: q.SessionID, ToolCallID: q.ID}
	for i, aq := range q.Questions {
		if i == question.MaxQuestions {
			break
		}
		qid := fmt.Sprintf("q%d", i)
		labels.questions[qid] = aq.ID
		tq := question.Question{
			ID: qid, Type: question.TypeFreeText, Label: cut(aq.Header, 40),
			Text: cut(aq.Text, question.MaxQuestionLength), Description: q.AgentID + " asks",
		}
		if len(aq.Options) >= 2 {
			tq.Type = question.TypeSingleChoice
			if aq.Multi {
				tq.Type = question.TypeMultiChoice
			}
			for j, o := range aq.Options {
				if j == question.MaxChoices {
					break
				}
				cid := fmt.Sprintf("o%d", j)
				labels.options[qid+"/"+cid] = o.Label
				tq.Choices = append(tq.Choices, question.Choice{ID: cid, Label: cut(o.Label, question.MaxChoiceLabelLength), Description: cut(o.Description, question.MaxChoiceDescriptionLength)})
			}
		}
		req.Questions = append(req.Questions, tq)
	}
	return req, labels
}

// cut shortens s to at most n bytes, ending in "…".
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n-len("…")], "") + "…"
}
