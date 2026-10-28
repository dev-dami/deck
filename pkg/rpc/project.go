package rpc

import (
	"context"
	"errors"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dev-dami/deck/internal/deck/gh"
	"github.com/dev-dami/deck/internal/usage"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// recentBranches is how many branches GitStatus lists.
const recentBranches = 30

// heatWeeks is how many weeks of days Usage lays out for the activity map.
const heatWeeks = 20

// maxHistoryDays caps the analytics record Usage sends (history_days).
const maxHistoryDays = 92

// WithProject gives the server the project folder, where git runs and
// terminals start, and Deck's usage record (nil when there is none).
func (s *Server) WithProject(dir string, u usage.Service) *Server {
	s.dir = dir
	s.git = gh.New(dir)
	s.usage = u
	s.terms = newTerminals(dir)
	return s
}

// Close ends what the server started on its own: the terminals.
func (s *Server) Close() {
	if s.browser != nil {
		s.browser.Close()
	}
	if s.terms != nil {
		s.terms.closeAll()
	}
}

func (s *Server) needGit() error {
	if s.git == nil {
		return status.Error(codes.FailedPrecondition, "this Deck has no project folder")
	}
	return nil
}

// gitFail maps a git or gh error to a status that keeps git's own words.
func gitFail(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gh.ErrNoGH) {
		return status.Error(codes.FailedPrecondition, "The GitHub CLI is not set up: "+err.Error()+".")
	}
	var ee *gh.ExitError
	if errors.As(err, &ee) {
		msg := strings.TrimSpace(ee.Stderr)
		if msg == "" {
			msg = ee.Error()
		}
		return status.Error(codes.FailedPrecondition, msg)
	}
	return fail(err)
}

func (s *Server) GitStatus(ctx context.Context, _ *deckv1.Empty) (*deckv1.GitStatusResponse, error) {
	if err := s.needGit(); err != nil {
		return nil, err
	}
	st, err := s.git.Status(ctx)
	if err != nil {
		// Not a work tree (or no git at all): say so rather than fail.
		return &deckv1.GitStatusResponse{Repo: false}, nil
	}
	out := &deckv1.GitStatusResponse{
		Repo:          true,
		Branch:        st.Branch,
		Upstream:      st.Upstream,
		Ahead:         int32(st.Ahead),
		Behind:        int32(st.Behind),
		Dirty:         int32(st.Dirty),
		Detached:      st.Detached,
		Head:          st.Head,
		DefaultBranch: s.git.DefaultBranch(ctx),
		Gh:            s.git.Available(ctx),
	}
	if bs, err := s.git.RecentBranches(ctx, recentBranches); err == nil {
		out.Branches = each(bs, func(b gh.Branch) *deckv1.GitBranch {
			return &deckv1.GitBranch{Name: b.Name, Upstream: b.Upstream, CommittedAt: ms(b.Committed), Subject: b.Subject}
		})
	}
	if out.Gh && st.Branch != "" {
		if pr, err := s.git.PRForBranch(ctx, ""); err == nil && pr != nil {
			out.PullRequest = toPR(pr)
		}
	}
	return out, nil
}

func toPR(p *gh.PR) *deckv1.GitPullRequest {
	return &deckv1.GitPullRequest{
		Number:         int32(p.Number),
		Title:          p.Title,
		State:          p.State,
		Url:            p.URL,
		Draft:          p.IsDraft,
		ReviewDecision: p.ReviewDecision,
		Mergeable:      p.Mergeable,
		Base:           p.BaseRefName,
		Checks: each(p.Checks, func(c gh.Check) *deckv1.GitCheck {
			return &deckv1.GitCheck{Name: c.Name, Bucket: c.Bucket, Link: c.Link}
		}),
	}
}

func (s *Server) GitCheckout(ctx context.Context, r *deckv1.GitCheckoutRequest) (*deckv1.Empty, error) {
	if err := s.needGit(); err != nil {
		return nil, err
	}
	b := strings.TrimSpace(r.GetBranch())
	if b == "" {
		return nil, invalid("a branch name is needed")
	}
	if r.GetCreate() {
		return &deckv1.Empty{}, gitFail(s.git.CreateBranch(ctx, b))
	}
	return &deckv1.Empty{}, gitFail(s.git.Checkout(ctx, b))
}

func (s *Server) GitCommit(ctx context.Context, r *deckv1.GitCommitRequest) (*deckv1.Empty, error) {
	if err := s.needGit(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(r.GetMessage()) == "" {
		return nil, invalid("a commit message is needed")
	}
	return &deckv1.Empty{}, gitFail(s.git.CommitAll(ctx, r.GetMessage()))
}

func (s *Server) GitPush(ctx context.Context, _ *deckv1.Empty) (*deckv1.Empty, error) {
	if err := s.needGit(); err != nil {
		return nil, err
	}
	return &deckv1.Empty{}, gitFail(s.git.Push(ctx))
}

func (s *Server) GitPull(ctx context.Context, _ *deckv1.Empty) (*deckv1.Empty, error) {
	if err := s.needGit(); err != nil {
		return nil, err
	}
	return &deckv1.Empty{}, gitFail(s.git.Pull(ctx))
}

func (s *Server) GitCreatePR(ctx context.Context, r *deckv1.GitCreatePRRequest) (*deckv1.GitPullRequest, error) {
	if err := s.needGit(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(r.GetTitle()) == "" {
		return nil, invalid("a title is needed")
	}
	n, url, err := s.git.CreatePR(ctx, r.GetTitle(), r.GetBody(), r.GetBase(), r.GetDraft())
	if err != nil {
		return nil, gitFail(err)
	}
	if pr, err := s.git.PRView(ctx, n); err == nil && pr != nil {
		return toPR(pr), nil
	}
	return &deckv1.GitPullRequest{Number: int32(n), Url: url, Title: r.GetTitle(), State: "OPEN", Draft: r.GetDraft()}, nil
}

func (s *Server) GitMergePR(ctx context.Context, r *deckv1.GitMergePRRequest) (*deckv1.Empty, error) {
	if err := s.needGit(); err != nil {
		return nil, err
	}
	pr, err := s.git.PRForBranch(ctx, "")
	if err != nil {
		return nil, gitFail(err)
	}
	if pr == nil {
		return nil, status.Error(codes.FailedPrecondition, "This branch has no pull request.")
	}
	method := r.GetMethod()
	if method == "" {
		method = gh.MergeMethods[0]
	}
	return &deckv1.Empty{}, gitFail(s.git.Merge(ctx, pr.Number, method))
}

func durMs(d time.Duration) int64 { return d.Milliseconds() }

func (s *Server) Usage(ctx context.Context, r *deckv1.UsageRequest) (*deckv1.UsageResponse, error) {
	if s.usage == nil {
		return &deckv1.UsageResponse{Available: false}, nil
	}
	if r.GetDays() < 0 {
		return nil, invalid("days must not be negative")
	}
	var since time.Time
	if d := r.GetDays(); d > 0 {
		y, m, day := time.Now().Date()
		since = time.Date(y, m, day, 0, 0, 0, 0, time.Local).AddDate(0, 0, -int(d-1))
	}
	rep, err := s.usage.Report(ctx, since, 10)
	if err != nil {
		return nil, fail(err)
	}
	ov, err := s.usage.Overview(ctx, since)
	if err != nil {
		return nil, fail(err)
	}
	out := &deckv1.UsageResponse{
		Available: true,
		Overview: &deckv1.UsageOverview{
			Turns:            int32(ov.Turns),
			AllTurns:         int32(ov.AllTurns),
			InputTokens:      ov.InputTokens,
			OutputTokens:     ov.OutputTokens,
			CachedTokens:     ov.CachedTokens,
			CacheWriteTokens: ov.CacheWriteTokens,
			ReasoningTokens:  ov.ReasoningTokens,
			CostUsd:          ov.CostUSD,
			DurationMs:       durMs(ov.Duration),
			TtftAvgMs:        durMs(ov.TTFTAvg),
			TtftP95Ms:        durMs(ov.TTFTP95),
			CacheHitPct:      ov.CacheHitPct,
			Done:             int32(ov.ByStatus[usage.StatusDone]),
			Interrupted:      int32(ov.ByStatus[usage.StatusInterrupted]),
			Failed:           int32(ov.ByStatus[usage.StatusFailed]),
		},
		Days:   each(rep.Days, toUsageDay),
		Kinds:  each(rep.Kinds, toUsageGroup),
		Models: each(rep.Models, toUsageGroup),
		Sessions: each(rep.Sessions, func(x usage.Session) *deckv1.UsageSession {
			return &deckv1.UsageSession{SessionId: x.SessionID, Title: x.Title, Turns: int32(x.Turns), Tokens: x.Tokens, CostUsd: x.CostUSD}
		}),
	}
	if ls, err := s.usage.Limits(ctx); err == nil {
		out.Limits = each(ls, toUsageLimit)
	}
	if hd := r.GetHistoryDays(); hd > 0 {
		from := time.Now().Add(-time.Duration(min(hd, maxHistoryDays)) * 24 * time.Hour)
		if hs, err := s.usage.Hourly(ctx, from); err == nil {
			out.Hours = each(hs, func(h usage.Hour) *deckv1.UsageHour {
				return &deckv1.UsageHour{At: ms(h.At), Kind: h.Kind, AgentId: h.AgentID, Turns: int32(h.Turns), Tokens: h.Tokens, CostUsd: h.CostUSD}
			})
		}
		if ls, err := s.usage.LimitTimeline(ctx, from); err == nil {
			out.LimitHistory = each(ls, toUsageLimit)
		}
	}
	if heat, err := s.usage.Heat(ctx, heatWeeks); err == nil {
		out.Heat = each(heat, toUsageDay)
	}
	if h, err := s.usage.Habits(ctx, since); err == nil {
		out.Habits = toHabits(h)
	}
	return out, nil
}

func toUsageLimit(l usage.LimitSnapshot) *deckv1.UsageLimit {
	return &deckv1.UsageLimit{
		AgentKind: l.AgentKind, WindowId: l.WindowID, Kind: l.Kind, Label: l.Label, UsedPct: l.UsedPct,
		ResetsAt: ms(l.ResetsAt), DurationMins: int32(l.DurationMins), Plan: l.Plan, At: ms(l.At),
	}
}

func toUsageDay(d usage.Day) *deckv1.UsageDay {
	return &deckv1.UsageDay{Day: ms(d.Day), Turns: int32(d.Turns), Tokens: d.Tokens, CostUsd: d.CostUSD}
}

func toUsageGroup(g usage.Group) *deckv1.UsageGroup {
	return &deckv1.UsageGroup{
		Kind: g.Kind, Model: g.Model, Turns: int32(g.Turns),
		InputTokens: g.InputTokens, OutputTokens: g.OutputTokens, CachedTokens: g.CachedTokens, CostUsd: g.CostUSD,
	}
}

func toHabits(h usage.Habits) *deckv1.UsageHabits {
	hours := make([]int32, 0, 7*24)
	for d := range h.Hours {
		for _, n := range h.Hours[d] {
			hours = append(hours, int32(n))
		}
	}
	return &deckv1.UsageHabits{
		ActiveDays:       int32(h.ActiveDays),
		Streak:           int32(h.Streak),
		Hours:            hours,
		ReviewMedianMs:   durMs(h.Review.Median),
		ReviewP90Ms:      durMs(h.Review.P90),
		ApprovalMedianMs: durMs(h.Approvals.Median),
		ApprovalFastPct:  h.Approvals.FastPct,
		UserTurns:        int32(h.UserTurns),
		Redirects:        int32(h.Redirects),
		FollowUps:        int32(h.FollowUps),
		PromptMedian:     int32(h.Prompt.Median),
		EditTurns:        int32(h.EditTurns),
		EditFiles:        int32(h.EditFiles),
		LinesAdded:       int32(h.LinesAdded),
		LinesRemoved:     int32(h.LinesRemoved),
		Reverts:          int32(h.Reverts),
		Fit: each(h.Fit, func(f usage.AgentFit) *deckv1.UsageFit {
			return &deckv1.UsageFit{Kind: f.Kind, Class: f.Class, Calls: int32(f.Calls)}
		}),
	}
}
