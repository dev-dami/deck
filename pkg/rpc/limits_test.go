package rpc

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dev-dami/deck/internal/workspace"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// fakeLimits is a workspace with rooms and a limits desk that records what
// it is asked.
type fakeLimits struct {
	*fakeRooms
	waits     []workspace.AgentWait
	scheduled []workspace.ScheduledSend
	resolved  []string
}

func (f *fakeLimits) CheckLimits(_ context.Context, kind string) []workspace.LimitCheck {
	if kind == "opencode" {
		return []workspace.LimitCheck{{Kind: kind, At: time.UnixMilli(5000), Err: "it does not report plan limits", Unsupported: true}}
	}
	return []workspace.LimitCheck{{Kind: "claude", At: time.UnixMilli(4000)}, {Kind: "opencode", At: time.UnixMilli(4000), Unsupported: true, Err: "it does not report plan limits"}}
}

func (f *fakeLimits) LimitChecks() []workspace.LimitCheck {
	return []workspace.LimitCheck{{Kind: "claude", At: time.UnixMilli(4000)}}
}

func (f *fakeLimits) StartWindow(_ context.Context, kind string) (string, error) {
	if kind != "claude" {
		return "", errors.New(kind + " has no rolling window")
	}
	return "haiku", nil
}

func (f *fakeLimits) Waits() []workspace.AgentWait { return f.waits }

func (f *fakeLimits) ResumeWait(sid, agent string) error {
	if agent != "claude" {
		return workspace.ErrNotFound
	}
	f.resolved = append(f.resolved, "resume "+sid+" "+agent)
	return nil
}

func (f *fakeLimits) CancelWait(sid, agent string) error {
	f.resolved = append(f.resolved, "cancel "+sid+" "+agent)
	return nil
}

func (f *fakeLimits) ScheduleSend(_ context.Context, sid, text string, at time.Time) (string, error) {
	f.scheduled = append(f.scheduled, workspace.ScheduledSend{SessionID: sid, ID: "at-1", Text: text, At: at})
	return "at-1", nil
}

func (f *fakeLimits) Scheduled() []workspace.ScheduledSend { return f.scheduled }

func (f *fakeLimits) CancelScheduled(sid, id string, now bool) error {
	f.scheduled = nil
	return nil
}

func TestLimitCalls(t *testing.T) {
	f := &fakeLimits{fakeRooms: newFakeRooms(), waits: []workspace.AgentWait{
		{SessionID: "s1", Agent: "claude", Kind: "claude", Since: time.UnixMilli(1000), Until: time.UnixMilli(9000), Window: "five_hour"},
	}}
	_, c := workspaceRig(t, f, "")
	ctx := ctxT(t)

	info, err := c.Info(ctx, &deckv1.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range limitMethods {
		if !slices.Contains(info.GetCapabilities(), m) {
			t.Fatalf("capabilities %v lack %s", info.GetCapabilities(), m)
		}
	}

	lr, err := c.Limits(ctx, &deckv1.LimitsRequest{HistoryHours: 6})
	if err != nil {
		t.Fatal(err)
	}
	if w := lr.GetWaits(); len(w) != 1 || w[0].GetUntil() != 9000 || w[0].GetAgent() != "claude" || w[0].GetWindow() != "five_hour" {
		t.Errorf("waits %v", w)
	}
	if ch := lr.GetChecks(); len(ch) != 1 || ch[0].GetCheckedAt() != 4000 {
		t.Errorf("checks %v", ch)
	}

	cr, err := c.CheckLimits(ctx, &deckv1.CheckLimitsRequest{Kind: "opencode"})
	if err != nil {
		t.Fatal(err)
	}
	if ch := cr.GetChecks(); len(ch) != 1 || !ch[0].GetUnsupported() || ch[0].GetError() == "" {
		t.Errorf("opencode check %v", ch)
	}

	sw, err := c.StartWindow(ctx, &deckv1.StartWindowRequest{Kind: "claude"})
	if err != nil || sw.GetModel() != "haiku" {
		t.Errorf("start window %v %v", sw, err)
	}
	if _, err := c.StartWindow(ctx, &deckv1.StartWindowRequest{Kind: "codex"}); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("a refused start: %v", err)
	}
	if _, err := c.StartWindow(ctx, &deckv1.StartWindowRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("no kind: %v", err)
	}

	if _, err := c.ResolveWait(ctx, &deckv1.ResolveWaitRequest{SessionId: "s1", Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ResolveWait(ctx, &deckv1.ResolveWaitRequest{SessionId: "s1", Agent: "claude", Cancel: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ResolveWait(ctx, &deckv1.ResolveWaitRequest{SessionId: "s1", Agent: "nobody"}); status.Code(err) != codes.NotFound {
		t.Errorf("unknown agent: %v", err)
	}
	if !slices.Equal(f.resolved, []string{"resume s1 claude", "cancel s1 claude"}) {
		t.Errorf("resolved %v", f.resolved)
	}

	if _, err := c.ScheduleSend(ctx, &deckv1.ScheduleSendRequest{SessionId: "s1", Text: "  "}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("empty text: %v", err)
	}
	sr, err := c.ScheduleSend(ctx, &deckv1.ScheduleSendRequest{SessionId: "s1", Text: "go on", At: 12345})
	if err != nil || sr.GetId() != "at-1" {
		t.Fatalf("schedule %v %v", sr, err)
	}
	if len(f.scheduled) != 1 || f.scheduled[0].At.UnixMilli() != 12345 {
		t.Errorf("scheduled %v", f.scheduled)
	}
	if _, err := c.CancelScheduled(ctx, &deckv1.CancelScheduledRequest{SessionId: "s1", Id: "at-1"}); err != nil || len(f.scheduled) != 0 {
		t.Errorf("cancel scheduled %v %v", err, f.scheduled)
	}
}
