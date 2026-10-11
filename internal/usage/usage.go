// Package usage keeps a local record of what each finished agent turn used
// (tokens, cost, time) and sums it up for the Usage view. It is local only:
// rows live in Deck's own database and nothing here talks to the network.
package usage

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/dev-dami/deck/internal/db"
)

// Record is one finished agent turn.
type Record struct {
	SessionID    string
	AgentID      string
	Kind         string // "claude", "codex", "opencode", "antigravity"
	Model        string // what the agent ran; "" when it never said
	InputTokens  int
	OutputTokens int
	CachedTokens int
	CostUSD      float64
	Duration     time.Duration
	At           time.Time // when the turn finished; zero means now
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
}

type service struct {
	q db.Querier
}

// NewService returns a [Service] over Deck's database.
func NewService(q db.Querier) Service {
	return &service{q: q}
}

func (s *service) Record(ctx context.Context, r Record) error {
	at := r.At
	if at.IsZero() {
		at = time.Now()
	}
	return s.q.InsertUsage(ctx, db.InsertUsageParams{
		ID:           uuid.New().String(),
		SessionID:    r.SessionID,
		AgentID:      r.AgentID,
		Kind:         r.Kind,
		Model:        r.Model,
		InputTokens:  int64(max(0, r.InputTokens)),
		OutputTokens: int64(max(0, r.OutputTokens)),
		CachedTokens: int64(max(0, r.CachedTokens)),
		CostUsd:      max(0, r.CostUSD),
		DurationMs:   max(0, r.Duration.Milliseconds()),
		CreatedAt:    at.Unix(),
	})
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
