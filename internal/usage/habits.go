package usage

import (
	"context"
	"sort"
	"strings"
	"time"
)

// Habits are what the record says about how the user works, not what the
// agents used: when they work, how long they take to review, how fast they
// approve, when they cancel, and which agent they hand which kind of work.
// Everything is derived from numbers already kept (turn times, spans and
// behaviour rows); no prompt text is involved.

// Behaviour kinds recorded for Habits.
const (
	// BehaviorApprovalWait is how long a manual approval waited for the user:
	// Value is milliseconds (at least 1), Detail "allow" or "deny".
	BehaviorApprovalWait = "approval_wait"
	// BehaviorPrompt is the length in characters of a message the user sent.
	BehaviorPrompt = "prompt"
	// BehaviorRevert and BehaviorUnrevert are the user taking an agent's file
	// changes back, and taking that back; Value is the number of files.
	BehaviorRevert   = "revert"
	BehaviorUnrevert = "unrevert"
	// BehaviorTurnEdit is a turn that changed files: Value is the file count,
	// Detail "+added/-removed" lines.
	BehaviorTurnEdit = "turn_edit"
)

const (
	// maxHabitTurns caps the turns one Habits call reads.
	maxHabitTurns = 50000
	// idleBreak is the longest gap that still counts as thinking or reviewing;
	// a longer one is the user being away.
	idleBreak = 2 * time.Hour
	// followWindow is how soon after a turn a new user message counts as a
	// correction or follow-up of it.
	followWindow = 3 * time.Minute
	// fastApproval is the wait under which an approval was a reflex.
	fastApproval = time.Second
)

// Tool classes for AgentFit.
const (
	ClassRead  = "read"
	ClassEdit  = "edit"
	ClassRun   = "run"
	ClassOther = "other"
)

// Spread is the median and the 90th percentile of a set of durations.
type Spread struct {
	N      int
	Median time.Duration
	P90    time.Duration
}

// ApprovalHabit is how fast the user answers permission prompts.
type ApprovalHabit struct {
	Spread
	FastPct float64 // 0-100: answered in under a second
	P95     time.Duration
}

// InterruptStat counts cancelled turns by what they were doing when stopped.
type InterruptStat struct {
	Mode  string // plan, ask, yolo or ""
	Kind  string // agent kind
	Where string // wait, think, text, tool or none
	Count int
}

// AgentFit counts the tool calls of the turns the user started, by agent kind
// and class of tool.
type AgentFit struct {
	Kind  string
	Class string // ClassRead, ClassEdit, ClassRun or ClassOther
	Calls int
	Dur   time.Duration
}

// Habits is the report.
type Habits struct {
	Since time.Time
	// Hours counts finished turns by weekday (0 Monday … 6 Sunday) and hour.
	Hours      [7][24]int
	ActiveDays int
	// Streak is the longest run of consecutive days with a turn.
	Streak int
	// Review is the time between a turn ending and the user's next message:
	// how long they take to read, test and think.
	Review Spread
	// Redirects are user messages sent within a few minutes of an interrupted
	// turn; FollowUps those sent soon after a finished one. Both over UserTurns.
	Redirects, FollowUps, UserTurns int
	Approvals                       ApprovalHabit
	Interrupts                      []InterruptStat
	Fit                             []AgentFit
	// Prompt is the length in characters of the user's messages.
	Prompt struct {
		N      int
		Median int
		P90    int
	}
	Reverts, Unrevert int
	// EditTurns are turns that changed files, with the files and lines.
	EditTurns, EditFiles, LinesAdded, LinesRemoved int
}

// RedirectRate is the share of the user's turns that corrected an interrupted
// one, 0-1.
func (h Habits) RedirectRate() float64 { return rate(h.Redirects, h.UserTurns) }

// FollowUpRate is the share of the user's turns that followed a finished
// turn within minutes, 0-1.
func (h Habits) FollowUpRate() float64 { return rate(h.FollowUps, h.UserTurns) }

func rate(n, of int) float64 {
	if of <= 0 {
		return 0
	}
	return float64(n) / float64(of)
}

// Peak is the busiest weekday-hour cell and its count; count 0 when empty.
func (h Habits) Peak() (weekday, hour, count int) {
	for d := range h.Hours {
		for hr, n := range h.Hours[d] {
			if n > count {
				weekday, hour, count = d, hr, n
			}
		}
	}
	return
}

// ToolClass sorts a tool's name into read, edit or run work.
func ToolClass(name string) string {
	n := strings.ToLower(name)
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(n, s) {
				return true
			}
		}
		return false
	}
	switch {
	case has("todo", "task", "agent", "question", "plan"):
		return ClassOther
	case has("edit", "write", "patch", "replace", "create"):
		return ClassEdit
	case has("bash", "shell", "exec", "command", "run", "terminal"):
		return ClassRun
	case n == "ls", has("read", "grep", "glob", "search", "find", "list", "fetch", "view"):
		return ClassRead
	}
	return ClassOther
}

func (s *service) Habits(ctx context.Context, since time.Time) (Habits, error) {
	return s.habits(ctx, since, time.Local)
}

type habitTurn struct {
	session, agent, status, trigger string
	start, end                      time.Time
}

func (s *service) habits(ctx context.Context, since time.Time, loc *time.Location) (Habits, error) {
	h := Habits{Since: since}
	if s.conn == nil {
		return h, nil // no database handle: nothing to derive from
	}
	from := unix(since)

	rows, err := s.conn.QueryContext(ctx, `SELECT session_id, agent_id, status, "trigger", created_at, duration_ms
		FROM usage WHERE created_at >= ? ORDER BY created_at ASC LIMIT ?`, from, maxHabitTurns)
	if err != nil {
		return h, err
	}
	var turns []habitTurn
	for rows.Next() {
		var t habitTurn
		var end, dur int64
		if err := rows.Scan(&t.session, &t.agent, &t.status, &t.trigger, &end, &dur); err != nil {
			rows.Close()
			return h, err
		}
		t.end = time.Unix(end, 0).In(loc)
		t.start = t.end.Add(-ms(dur))
		turns = append(turns, t)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return h, err
	}
	rows.Close()
	deriveRhythm(&h, turns)

	if h.Approvals, err = s.approvalHabit(ctx, from); err != nil {
		return h, err
	}
	if h.Interrupts, err = s.interruptStats(ctx, from); err != nil {
		return h, err
	}
	if h.Fit, err = s.agentFit(ctx, from); err != nil {
		return h, err
	}
	if err := s.behaviourSums(ctx, from, &h); err != nil {
		return h, err
	}
	return h, nil
}

// deriveRhythm fills the hour map, the day streak, the review time and the
// redirect and follow-up counts from the turns, oldest first.
func deriveRhythm(h *Habits, turns []habitTurn) {
	days := map[time.Time]bool{}
	var gaps []int64
	var prevEnd time.Time
	last := map[string]habitTurn{} // session+agent -> its latest turn
	for _, t := range turns {
		wd := (int(t.end.Weekday()) + 6) % 7
		h.Hours[wd][t.end.Hour()]++
		days[Midnight(t.end)] = true

		if t.trigger == TriggerUser {
			h.UserTurns++
			if !prevEnd.IsZero() {
				if g := t.start.Sub(prevEnd); g >= 0 && g < idleBreak {
					gaps = append(gaps, int64(g/time.Millisecond))
				}
			}
			if p, ok := last[t.session+"\x00"+t.agent]; ok {
				if g := t.start.Sub(p.end); g >= 0 && g <= followWindow {
					if p.status == StatusInterrupted {
						h.Redirects++
					} else {
						h.FollowUps++
					}
				}
			}
		}
		if t.end.After(prevEnd) {
			prevEnd = t.end
		}
		last[t.session+"\x00"+t.agent] = t
	}
	h.ActiveDays = len(days)
	h.Streak = longestStreak(days)
	h.Review = spread(gaps)
}

// longestStreak is the longest run of consecutive local days in days.
func longestStreak(days map[time.Time]bool) int {
	var list []time.Time
	for d := range days {
		list = append(list, d)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Before(list[j]) })
	best, run := 0, 0
	for i, d := range list {
		if i > 0 && sameDay(list[i-1].AddDate(0, 0, 1), d) {
			run++
		} else {
			run = 1
		}
		best = max(best, run)
	}
	return best
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// spread is the median and p90 of millisecond values.
func spread(vals []int64) Spread {
	if len(vals) == 0 {
		return Spread{}
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
	return Spread{N: len(vals), Median: ms(percentile(vals, 0.5)), P90: ms(percentile(vals, 0.9))}
}

func (s *service) approvalHabit(ctx context.Context, from int64) (ApprovalHabit, error) {
	vals, err := s.behaviourValues(ctx, from, BehaviorApprovalWait)
	if err != nil || len(vals) == 0 {
		return ApprovalHabit{}, err
	}
	a := ApprovalHabit{Spread: spread(vals)}
	fast := 0
	for _, v := range vals { // spread sorted vals in place
		if ms(v) < fastApproval {
			fast++
		}
	}
	a.FastPct = 100 * float64(fast) / float64(len(vals))
	a.P95 = ms(percentile(vals, 0.95))
	return a, nil
}

// behaviourValues lists the Value of every row of kind since from.
func (s *service) behaviourValues(ctx context.Context, from int64, kind string) ([]int64, error) {
	rows, err := s.conn.QueryContext(ctx, `SELECT value FROM behavior WHERE kind = ? AND created_at >= ?`, kind, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *service) interruptStats(ctx context.Context, from int64) ([]InterruptStat, error) {
	rows, err := s.conn.QueryContext(ctx, `SELECT u.mode, u.kind,
			COALESCE((SELECT sp.kind FROM trace_span sp WHERE sp.turn_id = u.id ORDER BY sp.start_ms DESC, sp.id DESC LIMIT 1), 'none') AS last_span,
			COUNT(*)
		FROM usage u WHERE u.status = ? AND u.created_at >= ?
		GROUP BY u.mode, u.kind, last_span ORDER BY COUNT(*) DESC, u.kind ASC`, StatusInterrupted, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InterruptStat
	for rows.Next() {
		var st InterruptStat
		if err := rows.Scan(&st.Mode, &st.Kind, &st.Where, &st.Count); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *service) agentFit(ctx context.Context, from int64) ([]AgentFit, error) {
	rows, err := s.conn.QueryContext(ctx, `SELECT u.kind, sp.name, COUNT(*), COALESCE(SUM(sp.dur_ms), 0)
		FROM trace_span sp JOIN usage u ON u.id = sp.turn_id
		WHERE sp.kind = ? AND u."trigger" = ? AND u.created_at >= ?
		GROUP BY u.kind, sp.name`, SpanTool, TriggerUser, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct{ kind, class string }
	sum := map[key]*AgentFit{}
	for rows.Next() {
		var kind, name string
		var n, dur int64
		if err := rows.Scan(&kind, &name, &n, &dur); err != nil {
			return nil, err
		}
		k := key{kind, ToolClass(name)}
		f := sum[k]
		if f == nil {
			f = &AgentFit{Kind: kind, Class: k.class}
			sum[k] = f
		}
		f.Calls += int(n)
		f.Dur += ms(dur)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]AgentFit, 0, len(sum))
	for _, f := range sum {
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Calls > out[j].Calls
	})
	return out, nil
}

// behaviourSums fills the prompt length, revert and edit-turn figures.
func (s *service) behaviourSums(ctx context.Context, from int64, h *Habits) error {
	prompts, err := s.behaviourValues(ctx, from, BehaviorPrompt)
	if err != nil {
		return err
	}
	if len(prompts) > 0 {
		sort.Slice(prompts, func(i, j int) bool { return prompts[i] < prompts[j] })
		h.Prompt.N = len(prompts)
		h.Prompt.Median = int(percentile(prompts, 0.5))
		h.Prompt.P90 = int(percentile(prompts, 0.9))
	}
	rows, err := s.conn.QueryContext(ctx, `SELECT kind, detail, COUNT(*), COALESCE(SUM(value), 0)
		FROM behavior WHERE kind IN (?, ?, ?) AND created_at >= ? GROUP BY kind, detail`,
		BehaviorRevert, BehaviorUnrevert, BehaviorTurnEdit, from)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, detail string
		var n, sum int64
		if err := rows.Scan(&kind, &detail, &n, &sum); err != nil {
			return err
		}
		switch kind {
		case BehaviorRevert:
			h.Reverts += int(n)
		case BehaviorUnrevert:
			h.Unrevert += int(n)
		case BehaviorTurnEdit:
			h.EditTurns += int(n)
			h.EditFiles += int(sum)
			add, del := parseLines(detail)
			h.LinesAdded += add * int(n)
			h.LinesRemoved += del * int(n)
		}
	}
	return rows.Err()
}

// parseLines reads a BehaviorTurnEdit detail, "+12/-3".
func parseLines(detail string) (added, removed int) {
	plus, minus, ok := strings.Cut(detail, "/")
	if !ok {
		return 0, 0
	}
	added = atoi(strings.TrimPrefix(plus, "+"))
	removed = atoi(strings.TrimPrefix(minus, "-"))
	return added, removed
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
