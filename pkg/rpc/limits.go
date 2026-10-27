package rpc

import (
	"context"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dev-dami/deck/internal/usage"
	"github.com/dev-dami/deck/internal/workspace"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// limitMethods are the optional calls a workspace with a limits desk serves.
var limitMethods = []string{"Limits", "CheckLimits", "StartWindow", "ResolveWait", "ScheduleSend", "CancelScheduled"}

// maxLimitHours caps the record Limits sends (history_hours).
const maxLimitHours = 7 * 24

func (s *Server) limitsDesk() (workspace.LimitsDesk, error) {
	if s.crew == nil || s.crew.limits == nil {
		return nil, status.Error(codes.Unimplemented, "this Deck does not handle plan limits")
	}
	return s.crew.limits, nil
}

// latestLimits is each window's newest report: this run's, which is newer
// than the record (a row is stored only when a window moved), over the
// record's.
func (s *Server) latestLimits(ctx context.Context) []*deckv1.UsageLimit {
	byKey := map[string]int{}
	var out []*deckv1.UsageLimit
	put := func(l *deckv1.UsageLimit) {
		k := l.GetAgentKind() + "\x00" + l.GetWindowId()
		if i, ok := byKey[k]; ok {
			if l.GetAt() >= out[i].GetAt() {
				out[i] = l
			}
			return
		}
		byKey[k] = len(out)
		out = append(out, l)
	}
	if s.usage != nil {
		if ls, err := s.usage.Limits(ctx); err == nil {
			for _, l := range ls {
				put(toUsageLimit(l))
			}
		}
	}
	if s.crew != nil && s.crew.usage != nil {
		for kind, ll := range s.crew.usage.LiveLimits() {
			for _, w := range ll.Windows {
				put(toUsageLimit(usage.LimitSnapshot{
					AgentKind: kind, WindowID: w.ID, Kind: w.Kind, Label: w.Label, UsedPct: w.UsedPct,
					ResetsAt: w.ResetsAt, DurationMins: w.DurationMins, Plan: ll.Plan, At: ll.At,
				}))
			}
		}
	}
	return out
}

func toLimitCheck(c workspace.LimitCheck) *deckv1.LimitCheck {
	return &deckv1.LimitCheck{Kind: c.Kind, CheckedAt: ms(c.At), Error: c.Err, Unsupported: c.Unsupported}
}

func (s *Server) Limits(ctx context.Context, r *deckv1.LimitsRequest) (*deckv1.LimitsResponse, error) {
	d, err := s.limitsDesk()
	if err != nil {
		return nil, err
	}
	out := &deckv1.LimitsResponse{
		Limits: s.latestLimits(ctx),
		Checks: each(d.LimitChecks(), toLimitCheck),
		Waits: each(d.Waits(), func(w workspace.AgentWait) *deckv1.AgentWait {
			return &deckv1.AgentWait{SessionId: w.SessionID, Agent: w.Agent, Kind: w.Kind, Since: ms(w.Since), Until: ms(w.Until), Window: w.Window}
		}),
		Scheduled: each(d.Scheduled(), func(x workspace.ScheduledSend) *deckv1.ScheduledSend {
			return &deckv1.ScheduledSend{Id: x.ID, SessionId: x.SessionID, Text: x.Text, At: ms(x.At), Created: ms(x.Created)}
		}),
		Kinds: s.be.Info().Kinds,
	}
	if h := r.GetHistoryHours(); h > 0 && s.usage != nil {
		from := time.Now().Add(-time.Duration(min(h, maxLimitHours)) * time.Hour)
		if ls, err := s.usage.LimitTimeline(ctx, from); err == nil {
			out.History = each(ls, toUsageLimit)
		}
	}
	return out, nil
}

func (s *Server) CheckLimits(ctx context.Context, r *deckv1.CheckLimitsRequest) (*deckv1.CheckLimitsResponse, error) {
	d, err := s.limitsDesk()
	if err != nil {
		return nil, err
	}
	checks := d.CheckLimits(ctx, strings.TrimSpace(r.GetKind()))
	return &deckv1.CheckLimitsResponse{Checks: each(checks, toLimitCheck), Limits: s.latestLimits(ctx)}, nil
}

func (s *Server) StartWindow(ctx context.Context, r *deckv1.StartWindowRequest) (*deckv1.StartWindowResponse, error) {
	d, err := s.limitsDesk()
	if err != nil {
		return nil, err
	}
	kind := strings.TrimSpace(r.GetKind())
	if kind == "" {
		return nil, invalid("say which agent's window to start")
	}
	model, err := d.StartWindow(ctx, kind)
	if err != nil {
		return nil, refused(err)
	}
	return &deckv1.StartWindowResponse{Model: model, Limits: s.latestLimits(ctx)}, nil
}

func (s *Server) ResolveWait(_ context.Context, r *deckv1.ResolveWaitRequest) (*deckv1.Empty, error) {
	d, err := s.limitsDesk()
	if err != nil {
		return nil, err
	}
	if r.GetCancel() {
		err = d.CancelWait(r.GetSessionId(), r.GetAgent())
	} else {
		err = d.ResumeWait(r.GetSessionId(), r.GetAgent())
	}
	return &deckv1.Empty{}, roomFail(err)
}

func (s *Server) ScheduleSend(ctx context.Context, r *deckv1.ScheduleSendRequest) (*deckv1.ScheduleSendResponse, error) {
	d, err := s.limitsDesk()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(r.GetText()) == "" {
		return nil, invalid("the message is empty")
	}
	if r.GetAt() <= 0 {
		return nil, invalid("when to send it is missing")
	}
	id, err := d.ScheduleSend(ctx, r.GetSessionId(), r.GetText(), time.UnixMilli(r.GetAt()))
	if err != nil {
		return nil, roomFail(err)
	}
	return &deckv1.ScheduleSendResponse{Id: id}, nil
}

func (s *Server) CancelScheduled(_ context.Context, r *deckv1.CancelScheduledRequest) (*deckv1.Empty, error) {
	d, err := s.limitsDesk()
	if err != nil {
		return nil, err
	}
	return &deckv1.Empty{}, roomFail(d.CancelScheduled(r.GetSessionId(), r.GetId(), r.GetSendNow()))
}
