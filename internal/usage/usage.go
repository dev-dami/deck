// Package usage keeps a local record of what each finished agent turn used
// (tokens, cost, time) and sums it up for the Usage view. It is local only:
// rows live in Deck's own database and nothing here talks to the network.
package usage

import (
	"context"
	"database/sql"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/dev-dami/deck/internal/db"
)

// Record is one finished agent turn.
type Record struct {
	// ID identifies the turn; its trace spans refer to it. "" means a new one.
	ID           string
	SessionID    string
	AgentID      string
	Kind         string // "claude", "codex", "opencode", "antigravity"
	Model        string // what the agent ran; "" when it never said
	InputTokens  int
	OutputTokens int
	CachedTokens int
	// CacheWriteTokens went into the prompt cache; ReasoningTokens were spent
	// thinking.
	CacheWriteTokens int
	ReasoningTokens  int
	CostUSD          float64
	Duration         time.Duration
	// TTFT is the time to the first token and APITime the time spent in model
	// requests; 0 when not known.
	TTFT    time.Duration
	APITime time.Duration
	Status  string    // StatusDone ("" too), StatusInterrupted or StatusFailed
	Trigger string    // TriggerUser, TriggerRelay, TriggerMention, TriggerSteer; "" = not known
	Mode    string    // the thread's permission mode: "plan", "ask" or "yolo"
	At      time.Time // when the turn finished; zero means now
	// Spans are where the turn's time went, written with it.
	Spans []Span
}

// How a turn ended.
const (
	StatusDone        = "done"
	StatusInterrupted = "interrupted"
	StatusFailed      = "failed"
)

// What started a turn.
const (
	TriggerUser    = "user"    // the user's message
	TriggerRelay   = "relay"   // another agent's thread.send
	TriggerMention = "mention" // another agent's reply naming it
	TriggerSteer   = "steer"   // the user steered a queued message in
)

// Span kinds.
const (
	SpanThink = "think"
	SpanText  = "text"
	SpanTool  = "tool"
	SpanWait  = "wait" // from the start of the turn to its first token
)

// Span is one stretch of a turn: thinking, writing, a tool call, or the wait
// for the first token. Start is from the beginning of the turn.
type Span struct {
	Kind   string // SpanThink, SpanText, SpanTool or SpanWait
	Name   string // the tool's name for a tool span; never prompt text
	Start  time.Duration
	Dur    time.Duration
	Tokens int // output tokens of a think or text span (estimated, see the recorder)
}

// Totals sums a range.
type Totals struct {
	Turns        int
	InputTokens  int64
	OutputTokens int64
	CachedTokens int64
	CostUSD      float64
	Duration     time.Duration
}

// Tokens is input plus output.
func (t Totals) Tokens() int64 { return t.InputTokens + t.OutputTokens }

// Day is one local calendar day.
type Day struct {
	Day     time.Time // local midnight
	Turns   int
	Tokens  int64
	CostUSD float64
}

// Group sums the turns of one agent kind, or one kind's model.
type Group struct {
	Kind         string
	Model        string // "" on a per-kind group, or when the agent never named its model
	Turns        int
	InputTokens  int64
	OutputTokens int64
	CachedTokens int64
	CostUSD      float64
}

// Tokens is input plus output.
func (g Group) Tokens() int64 { return g.InputTokens + g.OutputTokens }

// Session sums one session's turns.
type Session struct {
	SessionID string
	Title     string // "" when the session is gone
	Turns     int
	Tokens    int64
	CostUSD   float64
}

// Report is everything the Usage view shows for one range.
type Report struct {
	Since    time.Time // zero for all time
	Totals   Totals
	AllTurns int   // turns ever counted, to tell "nothing in this range" from "nothing yet"
	Days     []Day // oldest first, one per day with no gaps
	Kinds    []Group
	Models   []Group
	Sessions []Session // the costliest first
}

// Empty reports whether nothing was counted in the range.
func (r Report) Empty() bool { return r.Totals.Turns == 0 }

// Service records turns and reports on them.
type Service interface {
	Record(ctx context.Context, r Record) error
	// Report sums the turns since since (zero: all of them), with the top
	// costliest sessions.
	Report(ctx context.Context, since time.Time, top int) (Report, error)

	// RecordBehavior notes one thing the user (or Deck for them) did.
	RecordBehavior(ctx context.Context, b Behavior) error
	// RecordLimits stores plan windows, skipping each that has not moved
	// since its last row (used_pct by less than 1, same reset). It returns
	// how many rows it wrote.
	RecordLimits(ctx context.Context, snaps []LimitSnapshot) (int, error)

	// Overview sums the turns since since (zero: all of them).
	Overview(ctx context.Context, since time.Time) (Overview, error)
	// Series is tokens and turns per agent kind, by hour or day.
	Series(ctx context.Context, since time.Time, g Granularity) (Series, error)
	// Heat is the tokens of each day of the last weeks weeks, from a Monday.
	Heat(ctx context.Context, weeks int) ([]Day, error)
	// Behavior counts what the user did, with rates and a daily series.
	Behavior(ctx context.Context, since time.Time) (BehaviorReport, error)
	// Habits derives how the user works (rhythm, review time, approval speed,
	// cancels, which agent gets what) from the stored turns and behaviour.
	// It is empty when the service has no database handle.
	Habits(ctx context.Context, since time.Time) (Habits, error)
	// Limits is the newest stored row of every plan window, by agent kind.
	Limits(ctx context.Context) ([]LimitSnapshot, error)
	// LimitHistory is one window's stored rows since since, oldest first.
	LimitHistory(ctx context.Context, agentKind, windowID string, since time.Time) ([]LimitSnapshot, error)
	// LimitTimeline is every window's stored rows since since, each window
	// led by its last row before since, by kind and window, oldest first.
	LimitTimeline(ctx context.Context, since time.Time) ([]LimitSnapshot, error)
	// Hourly is the turns, tokens and cost of each hour since since, by
	// agent kind and agent, oldest first. Hours with no turns are left out.
	Hourly(ctx context.Context, since time.Time) ([]Hour, error)
	// Traces are the latest turns, newest first.
	Traces(ctx context.Context, limit int) ([]Turn, error)
	// Trace is one turn's spans, in order.
	Trace(ctx context.Context, turnID string) ([]Span, error)
	// Models sums the turns of each agent kind's models.
	Models(ctx context.Context, since time.Time) ([]ModelStat, error)
	// Cost is what each agent kind's CLI reported the turns cost.
	Cost(ctx context.Context, since time.Time) ([]KindCost, error)
}

type service struct {
	q db.Querier
	// conn and txq, when set, let Record write a turn and its spans in one
	// transaction: one commit (one WAL sync) instead of one per row.
	conn *sql.DB
	txq  *db.Queries
	// pruned is when RecordLimits last dropped old snapshots (Unix seconds).
	pruned atomic.Int64
}

// NewService returns a [Service] over Deck's database.
func NewService(q db.Querier) Service {
	return &service{q: q}
}

// NewServiceWithDB is [NewService] that also writes each recorded turn and
// its trace spans in a single transaction on conn.
func NewServiceWithDB(q *db.Queries, conn *sql.DB) Service {
	return &service{q: q, conn: conn, txq: q}
}

func (s *service) Record(ctx context.Context, r Record) error {
	if s.conn == nil || len(r.Spans) == 0 {
		return s.record(ctx, s.q, r)
	}
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := s.record(ctx, s.txq.WithTx(tx), r); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *service) record(ctx context.Context, q db.Querier, r Record) error {
	at := r.At
	if at.IsZero() {
		at = time.Now()
	}
	id := r.ID
	if id == "" {
		id = uuid.New().String()
	}
	status := r.Status
	if status == "" {
		status = StatusDone
	}
	err := q.InsertUsage(ctx, db.InsertUsageParams{
		ID:               id,
		SessionID:        r.SessionID,
		AgentID:          r.AgentID,
		Kind:             r.Kind,
		Model:            r.Model,
		InputTokens:      int64(max(0, r.InputTokens)),
		OutputTokens:     int64(max(0, r.OutputTokens)),
		CachedTokens:     int64(max(0, r.CachedTokens)),
		CostUsd:          max(0, r.CostUSD),
		DurationMs:       max(0, r.Duration.Milliseconds()),
		CreatedAt:        at.Unix(),
		CacheWriteTokens: int64(max(0, r.CacheWriteTokens)),
		ReasoningTokens:  int64(max(0, r.ReasoningTokens)),
		Status:           status,
		TtftMs:           max(0, r.TTFT.Milliseconds()),
		ApiMs:            max(0, r.APITime.Milliseconds()),
		Trigger:          r.Trigger,
		Mode:             r.Mode,
	})
	if err != nil {
		return err
	}
	for _, sp := range r.Spans {
		err := q.InsertTraceSpan(ctx, db.InsertTraceSpanParams{
			TurnID:    id,
			SessionID: r.SessionID,
			AgentID:   r.AgentID,
			Kind:      sp.Kind,
			Name:      sp.Name,
			StartMs:   max(0, sp.Start.Milliseconds()),
			DurMs:     max(0, sp.Dur.Milliseconds()),
			Tokens:    int64(max(0, sp.Tokens)),
			CreatedAt: at.Unix(),
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *service) Report(ctx context.Context, since time.Time, top int) (Report, error) {
	from := int64(0)
	if !since.IsZero() {
		from = since.Unix()
	}
	rep := Report{Since: since}

	tot, err := s.q.UsageTotals(ctx, from)
	if err != nil {
		return Report{}, err
	}
	rep.Totals = Totals{
		Turns:        int(tot.Turns),
		InputTokens:  tot.InputTokens,
		OutputTokens: tot.OutputTokens,
		CachedTokens: tot.CachedTokens,
		CostUSD:      tot.CostUsd,
		Duration:     time.Duration(tot.DurationMs) * time.Millisecond,
	}
	rep.AllTurns = rep.Totals.Turns
	if from > 0 {
		all, err := s.q.UsageTotals(ctx, 0)
		if err != nil {
			return Report{}, err
		}
		rep.AllTurns = int(all.Turns)
	}

	days, err := s.q.UsageByDay(ctx, from)
	if err != nil {
		return Report{}, err
	}
	rep.Days = fillDays(days, since, time.Now())

	kinds, err := s.q.UsageByKind(ctx, from)
	if err != nil {
		return Report{}, err
	}
	for _, k := range kinds {
		rep.Kinds = append(rep.Kinds, Group{
			Kind: k.Kind, Turns: int(k.Turns),
			InputTokens: k.InputTokens, OutputTokens: k.OutputTokens, CachedTokens: k.CachedTokens,
			CostUSD: k.CostUsd,
		})
	}

	models, err := s.q.UsageByModel(ctx, from)
	if err != nil {
		return Report{}, err
	}
	for _, m := range models {
		rep.Models = append(rep.Models, Group{
			Kind: m.Kind, Model: m.Model, Turns: int(m.Turns),
			InputTokens: m.InputTokens, OutputTokens: m.OutputTokens, CachedTokens: m.CachedTokens,
			CostUSD: m.CostUsd,
		})
	}

	if top > 0 {
		sessions, err := s.q.UsageTopSessions(ctx, db.UsageTopSessionsParams{Since: from, Limit: int64(top)})
		if err != nil {
			return Report{}, err
		}
		for _, ss := range sessions {
			rep.Sessions = append(rep.Sessions, Session{
				SessionID: ss.SessionID, Title: ss.Title, Turns: int(ss.Turns), Tokens: ss.Tokens, CostUSD: ss.CostUsd,
			})
		}
	}
	return rep, nil
}

// maxDays caps how many days an all-time report lays out.
const maxDays = 366

// fillDays lays the per-day rows out one per day, from the start of the
// range (or the first day with usage, for all time) to today, so a quiet day
// shows as a gap rather than disappearing.
func fillDays(rows []db.UsageByDayRow, since, now time.Time) []Day {
	byDay := make(map[string]db.UsageByDayRow, len(rows))
	for _, r := range rows {
		byDay[r.Day] = r
	}
	today := Midnight(now)
	start := today
	if !since.IsZero() {
		start = Midnight(since)
	} else if len(rows) > 0 {
		if d, err := time.ParseInLocation(time.DateOnly, rows[0].Day, now.Location()); err == nil {
			start = d
		}
	}
	if earliest := today.AddDate(0, 0, -(maxDays - 1)); start.Before(earliest) {
		start = earliest
	}
	var out []Day
	for d := start; !d.After(today); d = d.AddDate(0, 0, 1) {
		day := Day{Day: d}
		if r, ok := byDay[d.Format(time.DateOnly)]; ok {
			day.Turns, day.Tokens, day.CostUSD = int(r.Turns), r.Tokens, r.CostUsd
		}
		out = append(out, day)
	}
	return out
}

// Midnight is the start of t's local day.
func Midnight(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// Since is the start of a range of days ending today: 1 is today, 7 the
// last seven days, and 0 (or less) all time, the zero time.
func Since(days int, now time.Time) time.Time {
	if days <= 0 {
		return time.Time{}
	}
	return Midnight(now).AddDate(0, 0, -(days - 1))
}
