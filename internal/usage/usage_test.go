package usage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/db"
)

func newTestService(t *testing.T) (Service, *db.Queries) {
	t.Helper()
	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	q := db.New(conn)
	return NewService(q), q
}

func TestRecordAndReportRoundTrip(t *testing.T) {
	t.Parallel()
	svc, q := newTestService(t)
	ctx := t.Context()

	_, err := q.CreateSession(ctx, db.CreateSessionParams{ID: "s1", Title: "First session"})
	require.NoError(t, err)

	now := time.Now()
	today := Midnight(now).Add(time.Minute)
	earlier := Midnight(now).AddDate(0, 0, -3).Add(time.Hour)

	records := []Record{
		{SessionID: "s1", AgentID: "claude-1", Kind: "claude", Model: "opus", InputTokens: 1000, OutputTokens: 200, CachedTokens: 5000, CostUSD: 0.50, Duration: 30 * time.Second, At: today},
		{SessionID: "s1", AgentID: "claude-1", Kind: "claude", Model: "opus", InputTokens: 500, OutputTokens: 100, CostUSD: 0.25, Duration: 15 * time.Second, At: today},
		{SessionID: "s2", AgentID: "codex-1", Kind: "codex", Model: "", InputTokens: 300, OutputTokens: 50, CostUSD: 0, Duration: time.Minute, At: earlier},
	}
	for _, r := range records {
		require.NoError(t, svc.Record(ctx, r))
	}

	// Today: only the two claude turns.
	rep, err := svc.Report(ctx, Since(1, now), 5)
	require.NoError(t, err)
	require.False(t, rep.Empty())
	require.Equal(t, 3, rep.AllTurns)
	require.Equal(t, 2, rep.Totals.Turns)
	require.Equal(t, int64(1500), rep.Totals.InputTokens)
	require.Equal(t, int64(300), rep.Totals.OutputTokens)
	require.Equal(t, int64(5000), rep.Totals.CachedTokens)
	require.InDelta(t, 0.75, rep.Totals.CostUSD, 1e-9)
	require.Equal(t, 45*time.Second, rep.Totals.Duration)
	require.Len(t, rep.Days, 1)
	require.Equal(t, int64(1800), rep.Days[0].Tokens)
	require.Len(t, rep.Kinds, 1)
	require.Equal(t, "claude", rep.Kinds[0].Kind)

	// All time: both kinds, the costliest first, and the gap days filled.
	rep, err = svc.Report(ctx, time.Time{}, 5)
	require.NoError(t, err)
	require.Equal(t, 3, rep.Totals.Turns)
	require.Equal(t, 3, rep.AllTurns)
	require.Len(t, rep.Days, 4, "three days ago through today, one per day")
	require.Equal(t, 1, rep.Days[0].Turns)
	require.Equal(t, 0, rep.Days[1].Turns)
	require.Equal(t, 0, rep.Days[2].Turns)
	require.Equal(t, 2, rep.Days[3].Turns)

	require.Len(t, rep.Kinds, 2)
	require.Equal(t, "claude", rep.Kinds[0].Kind)
	require.Equal(t, 2, rep.Kinds[0].Turns)
	require.Equal(t, int64(1800), rep.Kinds[0].Tokens())
	require.Equal(t, "codex", rep.Kinds[1].Kind)

	require.Len(t, rep.Models, 2)
	require.Equal(t, "claude", rep.Models[0].Kind)
	require.Equal(t, "opus", rep.Models[0].Model)
	require.Equal(t, 2, rep.Models[0].Turns)
	require.Equal(t, int64(5000), rep.Models[0].CachedTokens)
	require.InDelta(t, 0.75, rep.Models[0].CostUSD, 1e-9)
	require.Empty(t, rep.Models[1].Model)

	require.Len(t, rep.Sessions, 2)
	require.Equal(t, "s1", rep.Sessions[0].SessionID)
	require.Equal(t, "First session", rep.Sessions[0].Title)
	require.Equal(t, 2, rep.Sessions[0].Turns)
	require.Equal(t, "s2", rep.Sessions[1].SessionID)
	require.Empty(t, rep.Sessions[1].Title, "a session that is gone has no title")

	// The top list is capped.
	rep, err = svc.Report(ctx, time.Time{}, 1)
	require.NoError(t, err)
	require.Len(t, rep.Sessions, 1)
}

func TestReportEmpty(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	rep, err := svc.Report(t.Context(), Since(7, time.Now()), 5)
	require.NoError(t, err)
	require.True(t, rep.Empty())
	require.Zero(t, rep.AllTurns)
	require.Len(t, rep.Days, 7)
	require.Empty(t, rep.Kinds)
	require.Empty(t, rep.Sessions)
}

func TestRecordClampsNegatives(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	require.NoError(t, svc.Record(t.Context(), Record{SessionID: "s", AgentID: "a", Kind: "claude", InputTokens: -5, CostUSD: -1, Duration: -time.Second}))
	rep, err := svc.Report(t.Context(), time.Time{}, 5)
	require.NoError(t, err)
	require.Equal(t, 1, rep.Totals.Turns)
	require.Zero(t, rep.Totals.InputTokens)
	require.Zero(t, rep.Totals.CostUSD)
}

func TestSince(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 11, 15, 4, 5, 0, time.Local)
	require.True(t, Since(0, now).IsZero())
	require.Equal(t, time.Date(2026, 10, 11, 0, 0, 0, 0, time.Local), Since(1, now))
	require.Equal(t, time.Date(2026, 10, 5, 0, 0, 0, 0, time.Local), Since(7, now))
	require.Equal(t, time.Date(2026, 9, 12, 0, 0, 0, 0, time.Local), Since(30, now))
}

func TestFillDaysCapsAllTime(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.Local)
	rows := []db.UsageByDayRow{{Day: "2020-01-01", Turns: 1}, {Day: "2026-10-11", Turns: 2, Tokens: 10}}
	days := fillDays(rows, time.Time{}, now)
	require.Len(t, days, maxDays)
	require.Equal(t, 2, days[len(days)-1].Turns)
	require.Equal(t, int64(10), days[len(days)-1].Tokens)
}
