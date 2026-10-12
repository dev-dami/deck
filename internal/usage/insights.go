package usage

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"sort"
	"time"

	"github.com/dev-dami/deck/internal/db"
)

// Behaviour kinds. Details qualify them: approvals are "manual" or "auto",
// relays "tool" or "mention", mode and model changes name the new value.
const (
	BehaviorInterrupt    = "interrupt"
	BehaviorSteer        = "steer"
	BehaviorQueueCancel  = "queue_cancel"
	BehaviorQueueEdit    = "queue_edit"
	BehaviorApproveAllow = "approve_allow"
	BehaviorApproveDeny  = "approve_deny"
	BehaviorModeChange   = "mode_change"
	BehaviorModelChange  = "model_change"
	BehaviorEffortChange = "effort_change"
	BehaviorRelay        = "relay"
	BehaviorHopPause     = "hop_pause"
	BehaviorContinue     = "continue"
	BehaviorError        = "error"
	BehaviorRetry        = "retry"
	// Task orchestration (internal/deck/session/tasks.go): a task steered,
	// retried by the scheduler, or failed for good.
	BehaviorTaskSteer = "task_steer"
	BehaviorTaskRetry = "task_retry"
	BehaviorTaskFail  = "task_fail"
)

// Behavior is one thing the user (or Deck on their behalf) did to steer the
// agents. It never holds prompt text.
type Behavior struct {
	SessionID string
	AgentID   string // "" for a thread-wide change, such as the mode
	Kind      string
	Detail    string
	Value     int       // 0 counts as 1; a hop pause carries the hop count
	At        time.Time // zero means now
}

// LimitSnapshot is one plan window of one agent kind at one time.
type LimitSnapshot struct {
	AgentKind    string
	WindowID     string
	Kind         string // "session", "weekly", "monthly", "other"
	Label        string
	UsedPct      float64 // 0-100
	ResetsAt     time.Time
	DurationMins int
	Plan         string
	At           time.Time // zero means now
}

// resetSlack is how far two reset times of a window may differ and still be
// the same reset: the CLIs round them differently from one report to the next.
const resetSlack = time.Minute

// Overview sums a range for the Usage view's headline.
type Overview struct {
	Since            time.Time // zero for all time
	Turns            int
	AllTurns         int // turns ever counted
	InputTokens      int64
	OutputTokens     int64
	CachedTokens     int64
	CacheWriteTokens int64
	ReasoningTokens  int64
	CostUSD          float64 // as the CLIs reported it; see Cost
	Duration         time.Duration
	TTFTAvg          time.Duration // over the turns whose TTFT is known
	TTFTP95          time.Duration
	CacheHitPct      float64        // 0-100: cached of all prompt tokens
	ByStatus         map[string]int // turns by StatusDone, StatusInterrupted, StatusFailed
}

// Tokens is input plus output.
func (o Overview) Tokens() int64 { return o.InputTokens + o.OutputTokens }

// Granularity is the bucket size of a Series.
type Granularity string

const (
	Hourly Granularity = "hour"
	Daily  Granularity = "day"
)

// Series is tokens and turns per agent kind over time.
type Series struct {
	Granularity Granularity
	Buckets     []time.Time // the start of each bucket, oldest first, no gaps
	Kinds       []string    // kinds with usage in the range, the most tokens first
	Tokens      map[string][]int64
	Turns       map[string][]int // per kind, aligned with Buckets
}

// BehaviorCount is how often one kind (and detail) of behaviour happened.
type BehaviorCount struct {
	Kind   string
	Detail string
	Count  int
	Value  int64 // the sum of the rows' values
}

// DayCount is a count on one local day.
type DayCount struct {
	Day   time.Time // local midnight
	Count int
}

// BehaviorReport is what the user did over a range.
type BehaviorReport struct {
	Since       time.Time
	Counts      []BehaviorCount
	Turns       int
	Interrupted int
	Failed      int
	// CancelRate is interrupted turns over turns; DenyRate denied approvals
	// over answered ones; RelayRate turns started by another agent (relay or
	// mention) over turns. Each is 0-1, 0 when there is nothing to divide.
	CancelRate float64
	DenyRate   float64
	RelayRate  float64
	// Daily is each kind's count per day, oldest first, no gaps.
	Daily map[string][]DayCount
}

// Count is how often kind happened, whatever its detail.
func (b BehaviorReport) Count(kind string) int {
	n := 0
	for _, c := range b.Counts {
		if c.Kind == kind {
			n += c.Count
		}
	}
	return n
}

// Turn is one recorded turn, for the trace list.
type Turn struct {
	ID               string
	SessionID        string
	AgentID          string
	Kind             string
	Model            string
	InputTokens      int64
	OutputTokens     int64
	CachedTokens     int64
	CacheWriteTokens int64
	ReasoningTokens  int64
	CostUSD          float64
	Duration         time.Duration
	TTFT             time.Duration
	APITime          time.Duration
	Status           string
	Trigger          string
	Mode             string
	At               time.Time
}

// Tokens is input plus output.
func (t Turn) Tokens() int64 { return t.InputTokens + t.OutputTokens }

// ModelStat sums one agent kind's model.
type ModelStat struct {
	Kind             string
	Model            string // "" when the agent never named it
	Turns            int
	InputTokens      int64
	OutputTokens     int64
	CachedTokens     int64
	CacheWriteTokens int64
	ReasoningTokens  int64
	CacheHitPct      float64
	TTFTAvg          time.Duration
}

// Tokens is input plus output.
func (m ModelStat) Tokens() int64 { return m.InputTokens + m.OutputTokens }

// KindCost is what one agent kind's CLI said its turns cost. Deck has no
// pricing of its own: a CLI that reports no cost (Reported false) has an
// unknown cost, not a zero one.
type KindCost struct {
	Kind      string
	Turns     int
	CostTurns int // turns that came with a cost
	CostUSD   float64
	Reported  bool
}

// cachedInInput are the agent kinds whose input token count already includes
// the cached tokens (OpenAI's convention); the others count them apart
// (Anthropic's).
var cachedInInput = map[string]bool{"codex": true}

// promptTokens is everything the prompts of kind's turns carried.
func promptTokens(kind string, input, cached, cacheWrite int64) int64 {
	if cachedInInput[kind] {
		return input
	}
	return input + cached + cacheWrite
}

func pct(part, whole int64) float64 {
	if whole <= 0 {
		return 0
	}
	return min(100, 100*float64(part)/float64(whole))
}

func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func fromUnix(s int64) time.Time {
	if s <= 0 {
		return time.Time{}
	}
	return time.Unix(s, 0)
}

func ms(n int64) time.Duration { return time.Duration(n) * time.Millisecond }

func (s *service) RecordBehavior(ctx context.Context, b Behavior) error {
	at := b.At
	if at.IsZero() {
		at = time.Now()
	}
	v := b.Value
	if v == 0 {
		v = 1
	}
	return s.q.InsertBehavior(ctx, db.InsertBehaviorParams{
		SessionID: b.SessionID,
		AgentID:   b.AgentID,
		Kind:      b.Kind,
		Detail:    b.Detail,
		Value:     int64(v),
		CreatedAt: at.Unix(),
	})
}

func (s *service) RecordLimits(ctx context.Context, snaps []LimitSnapshot) (int, error) {
	n := 0
	for _, sn := range snaps {
		if sn.AgentKind == "" || sn.WindowID == "" {
			continue
		}
		prev, err := s.q.LatestLimitSnapshot(ctx, db.LatestLimitSnapshotParams{AgentKind: sn.AgentKind, WindowID: sn.WindowID})
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return n, err
		default:
			moved := math.Abs(prev.UsedPct-sn.UsedPct) >= 1
			reset := prev.ResetsAt != unix(sn.ResetsAt)
			if reset && prev.ResetsAt > 0 && !sn.ResetsAt.IsZero() {
				d := time.Duration(prev.ResetsAt-sn.ResetsAt.Unix()) * time.Second
				reset = d > resetSlack || d < -resetSlack
			}
			if !moved && !reset {
				continue
			}
		}
		at := sn.At
		if at.IsZero() {
			at = time.Now()
		}
		err = s.q.InsertLimitSnapshot(ctx, db.InsertLimitSnapshotParams{
			AgentKind:    sn.AgentKind,
			WindowID:     sn.WindowID,
			Kind:         sn.Kind,
			Label:        sn.Label,
			UsedPct:      min(100, max(0, sn.UsedPct)),
			ResetsAt:     unix(sn.ResetsAt),
			DurationMins: int64(max(0, sn.DurationMins)),
			Plan:         sn.Plan,
			CreatedAt:    at.Unix(),
		})
		if err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (s *service) Overview(ctx context.Context, since time.Time) (Overview, error) {
	from := unix(since)
	o := Overview{Since: since, ByStatus: map[string]int{}}
	kinds, err := s.q.UsageKindStats(ctx, from)
	if err != nil {
		return Overview{}, err
	}
	var prompt, ttftSum, ttftN int64
	for _, k := range kinds {
		o.Turns += int(k.Turns)
		o.InputTokens += k.InputTokens
		o.OutputTokens += k.OutputTokens
		o.CachedTokens += k.CachedTokens
		o.CacheWriteTokens += k.CacheWriteTokens
		o.ReasoningTokens += k.ReasoningTokens
		o.CostUSD += k.CostUsd
		o.Duration += ms(k.DurationMs)
		prompt += promptTokens(k.Kind, k.InputTokens, k.CachedTokens, k.CacheWriteTokens)
		ttftSum += k.TtftSum
		ttftN += k.TtftTurns
	}
	o.CacheHitPct = pct(o.CachedTokens, prompt)
	if ttftN > 0 {
		o.TTFTAvg = ms(ttftSum / ttftN)
	}
	ttfts, err := s.q.UsageTTFTs(ctx, from)
	if err != nil {
		return Overview{}, err
	}
	o.TTFTP95 = ms(percentile(ttfts, 0.95))

	statuses, err := s.q.UsageByStatus(ctx, from)
	if err != nil {
		return Overview{}, err
	}
	for _, st := range statuses {
		o.ByStatus[st.Status] += int(st.Turns)
	}
	o.AllTurns = o.Turns
	if from > 0 {
		all, err := s.q.UsageTotals(ctx, 0)
		if err != nil {
			return Overview{}, err
		}
		o.AllTurns = int(all.Turns)
	}
	return o, nil
}

// percentile is the nearest-rank p-th percentile of sorted values.
func percentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(math.Ceil(p*float64(len(sorted)))) - 1
	return sorted[min(len(sorted)-1, max(0, i))]
}

// Bucket caps: how many hours or days a series lays out at most.
const (
	maxHours = 24 * 92
)

func (s *service) Series(ctx context.Context, since time.Time, g Granularity) (Series, error) {
	return s.series(ctx, since, g, time.Now())
}

func (s *service) series(ctx context.Context, since time.Time, g Granularity, now time.Time) (Series, error) {
	if g != Hourly {
		g = Daily
	}
	format, layout := "%Y-%m-%d", time.DateOnly
	if g == Hourly {
		format, layout = "%Y-%m-%d %H", "2006-01-02 15"
	}
	rows, err := s.q.UsageSeries(ctx, db.UsageSeriesParams{Format: format, Since: unix(since)})
	if err != nil {
		return Series{}, err
	}
	trunc := func(t time.Time) time.Time {
		if g == Hourly {
			y, m, d := t.Date()
			return time.Date(y, m, d, t.Hour(), 0, 0, 0, t.Location())
		}
		return Midnight(t)
	}
	next := func(t time.Time) time.Time {
		if g == Hourly {
			return t.Add(time.Hour)
		}
		return t.AddDate(0, 0, 1)
	}
	end := trunc(now)
	start := end
	if !since.IsZero() {
		start = trunc(since)
	} else if len(rows) > 0 {
		if t, err := time.ParseInLocation(layout, rows[0].Bucket, now.Location()); err == nil {
			start = t
		}
	}
	if g == Hourly {
		if earliest := end.Add(-(maxHours - 1) * time.Hour); start.Before(earliest) {
			start = earliest
		}
	} else if earliest := end.AddDate(0, 0, -(maxDays - 1)); start.Before(earliest) {
		start = earliest
	}

	out := Series{Granularity: g, Tokens: map[string][]int64{}, Turns: map[string][]int{}}
	index := map[string]int{}
	for t := start; !t.After(end); t = next(t) {
		index[t.Format(layout)] = len(out.Buckets)
		out.Buckets = append(out.Buckets, t)
	}
	totals := map[string]int64{}
	for _, r := range rows {
		i, ok := index[r.Bucket]
		if !ok {
			continue
		}
		if _, ok := out.Tokens[r.Kind]; !ok {
			out.Tokens[r.Kind] = make([]int64, len(out.Buckets))
			out.Turns[r.Kind] = make([]int, len(out.Buckets))
		}
		out.Tokens[r.Kind][i] += r.Tokens
		out.Turns[r.Kind][i] += int(r.Turns)
		totals[r.Kind] += r.Tokens
	}
	for k := range out.Tokens {
		out.Kinds = append(out.Kinds, k)
	}
	sort.Slice(out.Kinds, func(i, j int) bool {
		a, b := out.Kinds[i], out.Kinds[j]
		if totals[a] != totals[b] {
			return totals[a] > totals[b]
		}
		return a < b
	})
	return out, nil
}

func (s *service) Heat(ctx context.Context, weeks int) ([]Day, error) {
	return s.heat(ctx, weeks, time.Now())
}

func (s *service) heat(ctx context.Context, weeks int, now time.Time) ([]Day, error) {
	if weeks <= 0 {
		weeks = 1
	}
	today := Midnight(now)
	monday := today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7))
	start := monday.AddDate(0, 0, -7*(weeks-1))
	rows, err := s.q.UsageByDay(ctx, start.Unix())
	if err != nil {
		return nil, err
	}
	return fillDays(rows, start, now), nil
}

func (s *service) Behavior(ctx context.Context, since time.Time) (BehaviorReport, error) {
	return s.behavior(ctx, since, time.Now())
}

func (s *service) behavior(ctx context.Context, since, now time.Time) (BehaviorReport, error) {
	from := unix(since)
	rep := BehaviorReport{Since: since, Daily: map[string][]DayCount{}}
	counts, err := s.q.BehaviorCounts(ctx, from)
	if err != nil {
		return BehaviorReport{}, err
	}
	var allow, deny int
	for _, c := range counts {
		rep.Counts = append(rep.Counts, BehaviorCount{Kind: c.Kind, Detail: c.Detail, Count: int(c.Count), Value: c.Value})
		switch c.Kind {
		case BehaviorApproveAllow:
			allow += int(c.Count)
		case BehaviorApproveDeny:
			deny += int(c.Count)
		}
	}
	if allow+deny > 0 {
		rep.DenyRate = float64(deny) / float64(allow+deny)
	}

	statuses, err := s.q.UsageByStatus(ctx, from)
	if err != nil {
		return BehaviorReport{}, err
	}
	for _, st := range statuses {
		rep.Turns += int(st.Turns)
		switch st.Status {
		case StatusInterrupted:
			rep.Interrupted += int(st.Turns)
		case StatusFailed:
			rep.Failed += int(st.Turns)
		}
	}
	triggers, err := s.q.UsageByTrigger(ctx, from)
	if err != nil {
		return BehaviorReport{}, err
	}
	relayed := 0
	for _, tr := range triggers {
		if tr.Trigger == TriggerRelay || tr.Trigger == TriggerMention {
			relayed += int(tr.Turns)
		}
	}
	if rep.Turns > 0 {
		rep.CancelRate = float64(rep.Interrupted) / float64(rep.Turns)
		rep.RelayRate = float64(relayed) / float64(rep.Turns)
	}

	days, err := s.q.BehaviorByDay(ctx, from)
	if err != nil {
		return BehaviorReport{}, err
	}
	byKind := map[string][]db.UsageByDayRow{}
	for _, d := range days {
		byKind[d.Kind] = append(byKind[d.Kind], db.UsageByDayRow{Day: d.Day, Turns: d.Count})
	}
	first := since
	if first.IsZero() && len(days) > 0 {
		if t, err := time.ParseInLocation(time.DateOnly, days[0].Day, now.Location()); err == nil {
			first = t
		}
	}
	for k, rows := range byKind {
		for _, d := range fillDays(rows, first, now) {
			rep.Daily[k] = append(rep.Daily[k], DayCount{Day: d.Day, Count: d.Turns})
		}
	}
	return rep, nil
}

func limitRow(kind, window, wkind, label string, used float64, resets, mins int64, plan string, at int64) LimitSnapshot {
	return LimitSnapshot{
		AgentKind: kind, WindowID: window, Kind: wkind, Label: label, UsedPct: used,
		ResetsAt: fromUnix(resets), DurationMins: int(mins), Plan: plan, At: fromUnix(at),
	}
}

func (s *service) Limits(ctx context.Context) ([]LimitSnapshot, error) {
	rows, err := s.q.LatestLimitSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]LimitSnapshot, 0, len(rows))
	for _, r := range rows {
		out = append(out, limitRow(r.AgentKind, r.WindowID, r.Kind, r.Label, r.UsedPct, r.ResetsAt, r.DurationMins, r.Plan, r.CreatedAt))
	}
	return out, nil
}

func (s *service) LimitHistory(ctx context.Context, agentKind, windowID string, since time.Time) ([]LimitSnapshot, error) {
	rows, err := s.q.LimitHistory(ctx, db.LimitHistoryParams{AgentKind: agentKind, WindowID: windowID, Since: unix(since)})
	if err != nil {
		return nil, err
	}
	out := make([]LimitSnapshot, 0, len(rows))
	for _, r := range rows {
		out = append(out, limitRow(r.AgentKind, r.WindowID, r.Kind, r.Label, r.UsedPct, r.ResetsAt, r.DurationMins, r.Plan, r.CreatedAt))
	}
	return out, nil
}

func (s *service) Traces(ctx context.Context, limit int) ([]Turn, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.q.UsageRecent(ctx, int64(limit))
	if err != nil {
		return nil, err
	}
	out := make([]Turn, 0, len(rows))
	for _, r := range rows {
		out = append(out, Turn{
			ID: r.ID, SessionID: r.SessionID, AgentID: r.AgentID, Kind: r.Kind, Model: r.Model,
			InputTokens: r.InputTokens, OutputTokens: r.OutputTokens, CachedTokens: r.CachedTokens,
			CacheWriteTokens: r.CacheWriteTokens, ReasoningTokens: r.ReasoningTokens,
			CostUSD: r.CostUsd, Duration: ms(r.DurationMs), TTFT: ms(r.TtftMs), APITime: ms(r.ApiMs),
			Status: r.Status, Trigger: r.Trigger, Mode: r.Mode, At: fromUnix(r.CreatedAt),
		})
	}
	return out, nil
}

func (s *service) Trace(ctx context.Context, turnID string) ([]Span, error) {
	rows, err := s.q.TraceSpans(ctx, turnID)
	if err != nil {
		return nil, err
	}
	out := make([]Span, 0, len(rows))
	for _, r := range rows {
		out = append(out, Span{Kind: r.Kind, Name: r.Name, Start: ms(r.StartMs), Dur: ms(r.DurMs), Tokens: int(r.Tokens)})
	}
	return out, nil
}

func (s *service) Models(ctx context.Context, since time.Time) ([]ModelStat, error) {
	rows, err := s.q.UsageModelStats(ctx, unix(since))
	if err != nil {
		return nil, err
	}
	out := make([]ModelStat, 0, len(rows))
	for _, r := range rows {
		m := ModelStat{
			Kind: r.Kind, Model: r.Model, Turns: int(r.Turns),
			InputTokens: r.InputTokens, OutputTokens: r.OutputTokens, CachedTokens: r.CachedTokens,
			CacheWriteTokens: r.CacheWriteTokens, ReasoningTokens: r.ReasoningTokens,
			CacheHitPct: pct(r.CachedTokens, promptTokens(r.Kind, r.InputTokens, r.CachedTokens, r.CacheWriteTokens)),
		}
		if r.TtftTurns > 0 {
			m.TTFTAvg = ms(r.TtftSum / r.TtftTurns)
		}
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Tokens() > out[j].Tokens() })
	return out, nil
}

func (s *service) Cost(ctx context.Context, since time.Time) ([]KindCost, error) {
	rows, err := s.q.UsageKindStats(ctx, unix(since))
	if err != nil {
		return nil, err
	}
	out := make([]KindCost, 0, len(rows))
	for _, r := range rows {
		out = append(out, KindCost{
			Kind: r.Kind, Turns: int(r.Turns), CostTurns: int(r.CostTurns),
			CostUSD: r.CostUsd, Reported: r.CostTurns > 0,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Reported != out[j].Reported {
			return out[i].Reported
		}
		return out[i].CostUSD > out[j].CostUSD
	})
	return out, nil
}
