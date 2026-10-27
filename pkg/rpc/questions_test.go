package rpc

import (
	"reflect"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dev-dami/deck/pkg/remote/wire"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// A question travels as an approval of kind question with its questions,
// and Answer carries the person's answers back.
func TestQuestionApprovalRoundTrip(t *testing.T) {
	fake, _, c, _ := rig(t)
	ctx := ctxT(t)
	fake.Get("s1").Approvals = append(fake.Get("s1").Approvals, wire.Approval{
		ID: "toolu_1", SessionID: "s1", Agent: "claude", Kind: wire.ApprovalQuestion, Description: "claude asks: Which way?",
		Questions: []wire.Question{{ID: "Which way?", Header: "Way", Text: "Which way?", MultiSelect: true, Other: true,
			Options: []wire.QuestionOption{{Label: "Left", Description: "short"}, {Label: "Right"}}}},
	})
	list, err := c.Approvals(ctx, &deckv1.SessionRef{SessionId: "s1"})
	if err != nil || len(list.GetApprovals()) != 1 {
		t.Fatalf("approvals = %v, %v", list, err)
	}
	a := list.GetApprovals()[0]
	q := a.GetQuestions()
	if a.GetKind() != "question" || len(q) != 1 || q[0].GetHeader() != "Way" || !q[0].GetMultiSelect() || !q[0].GetOther() ||
		len(q[0].GetOptions()) != 2 || q[0].GetOptions()[0].GetDescription() != "short" {
		t.Fatalf("question = %v", a)
	}
	if _, err := c.Answer(ctx, &deckv1.AnswerRequest{SessionId: "s1", Id: "toolu_1", Decision: "answer"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("an answer without answers: %v", err)
	}
	res, err := c.Answer(ctx, &deckv1.AnswerRequest{SessionId: "s1", Id: "toolu_1", Decision: "answer",
		Answers: []*deckv1.QuestionAnswer{{Id: "Which way?", Selected: []string{"Left", "Right"}, Other: "both"}}})
	if err != nil || !res.GetResolved() {
		t.Fatalf("answer = %v, %v", res, err)
	}
	want := []wire.QuestionAnswer{{ID: "Which way?", Selected: []string{"Left", "Right"}, Other: "both"}}
	if got := fake.Replies("toolu_1"); !reflect.DeepEqual(got, want) || fake.Answered()["toolu_1"] != "answer" {
		t.Fatalf("replies = %+v (%v)", got, fake.Answered())
	}
}
