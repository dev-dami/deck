package usage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/db"
)

func newHabitsService(t *testing.T) Service {
	t.Helper()
	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	return NewServiceWithDB(db.New(conn), conn)
}

func TestToolClass(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]string{
		"Read": ClassRead, "Grep": ClassRead, "WebSearch": ClassRead, "ls": ClassRead,
		"Edit": ClassEdit, "MultiEdit": ClassEdit, "apply_patch": ClassEdit, "Write": ClassEdit,
		"Bash": ClassRun, "shell": ClassRun, "exec_command": ClassRun,
		"TodoWrite": ClassOther, "Task": ClassOther, "": ClassOther,
	} {
		require.Equal(t, want, ToolClass(name), name)
	}
}

func TestHabitsRhythmReviewAndRedirects(t *testing.T) {
	t.Parallel()
	svc := newHabitsService(t)
	ctx := t.Context()
	// Three days in a row, noon local time, so the streak is 3.
	day := Midnight(time.Now()).AddDate(0, 0, -2)
	at := func(d int, min int) time.Time {
		return day.AddDate(0, 0, d).Add(12*time.Hour + time.Duration(min)*time.Minute)
	}

	rec := func(session, agent, status, trigger string, end time.Time, dur time.Duration) {
		require.NoError(t, svc.Record(ctx, Record{
			SessionID: session, AgentID: agent, Kind: "claude", Status: status, Trigger: trigger,
			Duration: dur, At: end, InputTokens: 1,
		}))
	}
	// Day 0: a turn ends at 12:00; the user answers 10 minutes later (review
	// time 10m) and the answer follows an interrupted turn within a minute.
	rec("s", "claude", StatusDone, TriggerUser, at(0, 0), time.Minute)
	rec("s", "claude", StatusInterrupted, TriggerUser, at(0, 12), 2*time.Minute) // starts 12:10
	rec("s", "claude", StatusDone, TriggerUser, at(0, 14), 30*time.Second)       // starts 12:13:30 = 90s after the interrupt
	rec("s", "claude", StatusDone, TriggerRelay, at(1, 0), time.Minute)
	rec("s", "claude", StatusDone, TriggerUser, at(2, 0), time.Minute)

	h, err := svc.Habits(ctx, time.Time{})
	require.NoError(t, err)
	require.Equal(t, 3, h.ActiveDays)
	require.Equal(t, 3, h.Streak)
	require.Equal(t, 4, h.UserTurns)
	require.Equal(t, 1, h.Redirects, "the third turn follows the interrupted one within minutes")
	require.Equal(t, 0, h.FollowUps)
	require.Equal(t, 2, h.Review.N, "gaps of 10m and 90s; the first turn has no earlier one and the 24h gap is over the idle break")
	require.Equal(t, 90*time.Second, h.Review.Median, "nearest rank over [90s, 10m]")
	require.Equal(t, 10*time.Minute, h.Review.P90)
	_, hour, count := h.Peak()
	require.Equal(t, 12, hour)
	require.Equal(t, 3, count, "three turns ended in the same weekday-hour cell")
}

func TestHabitsApprovalsPromptsAndEdits(t *testing.T) {
	t.Parallel()
	svc := newHabitsService(t)
	ctx := t.Context()
	for _, v := range []int{300, 500, 4000, 9000} {
		require.NoError(t, svc.RecordBehavior(ctx, Behavior{SessionID: "s", Kind: BehaviorApprovalWait, Detail: "allow", Value: v}))
	}
	for _, v := range []int{10, 40, 200} {
		require.NoError(t, svc.RecordBehavior(ctx, Behavior{SessionID: "s", Kind: BehaviorPrompt, Value: v}))
	}
	require.NoError(t, svc.RecordBehavior(ctx, Behavior{SessionID: "s", Kind: BehaviorRevert, Value: 2}))
	require.NoError(t, svc.RecordBehavior(ctx, Behavior{SessionID: "s", Kind: BehaviorTurnEdit, Detail: "+10/-2", Value: 3}))
	require.NoError(t, svc.RecordBehavior(ctx, Behavior{SessionID: "s", Kind: BehaviorTurnEdit, Detail: "+5/-0", Value: 1}))

	h, err := svc.Habits(ctx, time.Time{})
	require.NoError(t, err)
	require.Equal(t, 4, h.Approvals.N)
	require.Equal(t, 500*time.Millisecond, h.Approvals.Median)
	require.InDelta(t, 50.0, h.Approvals.FastPct, 0.01, "300ms and 500ms are under a second")
	require.Equal(t, 9*time.Second, h.Approvals.P95)
	require.Equal(t, 3, h.Prompt.N)
	require.Equal(t, 40, h.Prompt.Median)
	require.Equal(t, 1, h.Reverts)
	require.Equal(t, 2, h.EditTurns)
	require.Equal(t, 4, h.EditFiles)
	require.Equal(t, 15, h.LinesAdded)
	require.Equal(t, 2, h.LinesRemoved)
}

func TestHabitsInterruptsAndFit(t *testing.T) {
	t.Parallel()
	svc := newHabitsService(t)
	ctx := t.Context()
	now := time.Now()
	require.NoError(t, svc.Record(ctx, Record{
		ID: "a", SessionID: "s", AgentID: "claude", Kind: "claude", Status: StatusInterrupted,
		Trigger: TriggerUser, Mode: "ask", At: now, InputTokens: 1,
		Spans: []Span{{Kind: SpanWait, Start: 0, Dur: time.Second}, {Kind: SpanTool, Name: "Bash", Start: time.Second, Dur: 2 * time.Second}},
	}))
	require.NoError(t, svc.Record(ctx, Record{
		ID: "b", SessionID: "s", AgentID: "claude", Kind: "claude", Status: StatusDone,
		Trigger: TriggerUser, Mode: "ask", At: now, InputTokens: 1,
		Spans: []Span{{Kind: SpanTool, Name: "Read", Dur: time.Second}, {Kind: SpanTool, Name: "Edit", Start: time.Second, Dur: time.Second}, {Kind: SpanTool, Name: "Edit", Start: 2 * time.Second, Dur: time.Second}},
	}))

	h, err := svc.Habits(ctx, time.Time{})
	require.NoError(t, err)
	require.Len(t, h.Interrupts, 1)
	require.Equal(t, InterruptStat{Mode: "ask", Kind: "claude", Where: SpanTool, Count: 1}, h.Interrupts[0])
	calls := map[string]int{}
	for _, f := range h.Fit {
		calls[f.Class] += f.Calls
	}
	require.Equal(t, map[string]int{ClassRun: 1, ClassRead: 1, ClassEdit: 2}, calls)
}

func TestHabitsWithoutDatabaseHandleIsEmpty(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t) // NewService: no conn
	h, err := svc.Habits(t.Context(), time.Time{})
	require.NoError(t, err)
	require.Zero(t, h.UserTurns)
}
