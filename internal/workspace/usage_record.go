package workspace

import (
	"context"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	deckagent "github.com/dev-dami/deck/internal/deck/agent"
	decks "github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/usage"
)

// What the workspace records for the Usage view beyond each turn's tokens:
// where each turn's time went (trace spans), what started it, how it ended,
// what the user did to steer the agents (behaviour), and the plan windows the
// CLIs report. None of it holds prompt or reply text.

// LiveLimits are an agent kind's plan limits as its CLI last reported them
// in this run of Deck.
type LiveLimits struct {
	deckagent.Limits
	At time.Time
}

// pendingTurn is what will start an agent's next turn: who addressed it, and
// when, if the agent was idle then (so the turn's clock starts at the post,
// not at the agent's first event).
type pendingTurn struct {
	trigger string
	at      time.Time
}

// reasoningApart are the agent kinds whose output token count leaves out the
// thinking tokens; the others count them in.
var reasoningApart = map[string]bool{"antigravity": true}

// charsPerToken estimates tokens from text when the CLI gives no split.
const charsPerToken = 4

// traceSpan is a span being built: wall times, and how much text it carried.
type traceSpan struct {
	kind, name string
	start, end time.Time
	chars      int
}

// traceBuilder lays out one turn's spans from its events: thinking and text
// (from their deltas), tool calls (call to result) and the wait for the first
// token. Token counts are the turn's output split over its think and text
// spans by how much text each carried.
type traceBuilder struct {
	began    time.Time
	first    time.Time // the first token: text, thinking or a tool call
	last     time.Time // the latest event
	spans    []*traceSpan
	open     *traceSpan            // the think or text span under way
	tools    map[string]*traceSpan // tool spans waiting for their result
	streamed map[string]bool       // span kinds that arrived as deltas
}

func newTraceBuilder(began time.Time) *traceBuilder {
	return &traceBuilder{began: began, last: began, tools: map[string]*traceSpan{}, streamed: map[string]bool{}}
}

// observe takes in one of the turn's events.
func (b *traceBuilder) observe(ev deckagent.Event) {
	at := ev.Time
	if at.IsZero() {
		at = time.Now()
	}
	if at.Before(b.began) {
		at = b.began
	}
	switch ev.Kind {
	case deckagent.KindAssistantText:
		b.text(usage.SpanText, ev, at)
	case deckagent.KindReasoning:
		b.text(usage.SpanThink, ev, at)
	case deckagent.KindToolCall:
		if ev.Tool == nil {
			return
		}
		b.mark(at)
		b.closeOpen()
		sp := &traceSpan{kind: usage.SpanTool, name: ev.Tool.Name, start: at, end: at}
		b.spans = append(b.spans, sp)
		b.tools[ev.Tool.ID] = sp
	case deckagent.KindToolResult:
		if ev.Tool == nil {
			return
		}
		if sp := b.tools[ev.Tool.ID]; sp != nil {
			sp.end = at
			delete(b.tools, ev.Tool.ID)
		} else { // a result with no call seen: it ran since the last event
			b.mark(at)
			b.closeOpen()
			b.spans = append(b.spans, &traceSpan{kind: usage.SpanTool, name: ev.Tool.Name, start: b.last, end: at})
		}
	default:
		return
	}
	b.last = at
}

// mark notes the first token.
func (b *traceBuilder) mark(at time.Time) {
	if b.first.IsZero() {
		b.first = at
	}
}

func (b *traceBuilder) text(kind string, ev deckagent.Event, at time.Time) {
	chars := 0
	switch {
	case ev.Delta:
		b.streamed[kind] = true
		chars = len([]rune(ev.Text))
	case !b.streamed[kind]:
		chars = len([]rune(ev.Text)) // the whole text at once
	}
	if b.open != nil && b.open.kind == kind {
		b.open.end = at
		b.open.chars += chars
		return
	}
	start := b.last
	if b.first.IsZero() {
		start = at // the wait span covers the time before the first token
	}
	b.mark(at)
	b.closeOpen()
	b.open = &traceSpan{kind: kind, start: start, end: at, chars: chars}
	b.spans = append(b.spans, b.open)
}

func (b *traceBuilder) closeOpen() { b.open = nil }

// ttft is the time from the start of the turn to its first token; 0 when no
// token came.
func (b *traceBuilder) ttft() time.Duration {
	if b.first.IsZero() {
		return 0
	}
	return b.first.Sub(b.began)
}

// finish closes the turn at end and returns its spans, with u's output
// tokens split over the think and text spans.
func (b *traceBuilder) finish(end time.Time, u deckagent.Usage, kind string) []usage.Span {
	b.closeOpen()
	for id, sp := range b.tools {
		if end.After(sp.end) {
			sp.end = end
		}
		delete(b.tools, id)
	}
	var chars [2]int // think, text
	var count [2]int
	idx := func(k string) int {
		switch k {
		case usage.SpanThink:
			return 0
		case usage.SpanText:
			return 1
		}
		return -1
	}
	for _, sp := range b.spans {
		if i := idx(sp.kind); i >= 0 {
			chars[i] += sp.chars
			count[i]++
		}
	}
	think := u.ReasoningTokens
	if think == 0 {
		think = chars[0] / charsPerToken
	}
	text := u.OutputTokens
	if !reasoningApart[kind] {
		text -= u.ReasoningTokens
	}
	if u.OutputTokens == 0 {
		text = chars[1] / charsPerToken
	}
	total := [2]int{max(0, think), max(0, text)}

	var out []usage.Span
	if !b.first.IsZero() && b.first.After(b.began) {
		out = append(out, usage.Span{Kind: usage.SpanWait, Dur: b.first.Sub(b.began)})
	}
	for _, sp := range b.spans {
		s := usage.Span{Kind: sp.kind, Name: sp.name, Start: max(0, sp.start.Sub(b.began)), Dur: max(0, sp.end.Sub(sp.start))}
		if i := idx(sp.kind); i >= 0 && total[i] > 0 {
			if chars[i] > 0 {
				s.Tokens = int(math.Round(float64(total[i]) * float64(sp.chars) / float64(chars[i])))
			} else {
				s.Tokens = total[i] / count[i]
			}
		}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

// turnStatus is how a turn ended: the TurnDone text ("interrupted",
// "failed"), an error during it, or the CLI exiting under it.
func turnStatus(ev deckagent.Event, failed bool) string {
	switch {
	case ev.Kind == deckagent.KindExited:
		return usage.StatusFailed
	case ev.Text == usage.StatusInterrupted:
		return usage.StatusInterrupted
	case ev.Text == usage.StatusFailed, failed:
		return usage.StatusFailed
	}
	return usage.StatusDone
}

// retrying reports whether an error event says the CLI will try again.
func retrying(text string) bool { return strings.HasSuffix(strings.TrimSpace(text), "(retrying)") }

// usageOK reports whether there is a usage record to write to.
func (w *DeckWorkspace) usageOK() bool {
	return w.AppWorkspace != nil && w.app != nil && w.app.Usage != nil
}

// recordBehavior notes one thing the user (or Deck for them) did. detail
// must never be prompt text.
func (w *DeckWorkspace) recordBehavior(sid, agentID, kind, detail string, value int) {
	if !w.usageOK() {
		return
	}
	err := w.app.Usage.RecordBehavior(context.Background(), usage.Behavior{
		SessionID: sid, AgentID: agentID, Kind: kind, Detail: detail, Value: value,
	})
	if err != nil {
		slog.Warn("deck: record behavior", "kind", kind, "error", err)
	}
}

// expect notes what will start each addressed agent's next turn.
func (w *DeckWorkspace) expect(threadID string, to []string, trigger string) {
	if len(to) == 0 {
		if th, ok := w.mgr().Thread(threadID); ok && th.LastSpeaker != "" {
			to = []string{th.LastSpeaker}
		}
	}
	now := time.Now()
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, id := range to {
		p := pendingTurn{trigger: trigger}
		if w.since[id].IsZero() { // idle: its turn starts now
			p.at = now
		}
		w.next[id] = p
	}
}

// onUserMessage handles a message the user posted (or steered in).
func (w *DeckWorkspace) onUserMessage(m decks.EventMsg) {
	trigger := usage.TriggerUser
	if m.Steer {
		trigger = usage.TriggerSteer
		sid := w.sessionOf(m.ThreadID)
		for _, to := range m.To {
			w.recordBehavior(sid, to, usage.BehaviorSteer, "", 1)
		}
	}
	w.expect(m.ThreadID, m.To, trigger)
}

// onRelay handles one agent's message to others.
func (w *DeckWorkspace) onRelay(m decks.RelayMsg) {
	detail, trigger := m.Via, usage.TriggerRelay
	if m.Via == "mention" {
		trigger = usage.TriggerMention
	}
	w.recordBehavior(w.sessionOf(m.ThreadID), m.From, usage.BehaviorRelay, detail, max(1, len(m.To)))
	if !m.Held {
		w.expect(m.ThreadID, m.To, trigger)
	}
}

// onLimits keeps an agent kind's plan limits for the live view and stores
// the windows that moved.
func (w *DeckWorkspace) onLimits(ev deckagent.Event) {
	if ev.Limits == nil {
		return
	}
	kind := ev.AgentID
	if a, ok := w.mgr().Agent(ev.AgentID); ok && a.Kind != "" {
		kind = a.Kind
	}
	at := ev.Time
	if at.IsZero() {
		at = time.Now()
	}
	w.mu.Lock()
	cur := mergeLimits(w.limits[kind].Limits, *ev.Limits)
	w.limits[kind] = LiveLimits{Limits: cur, At: at}
	w.mu.Unlock()

	if !w.usageOK() || len(ev.Limits.Windows) == 0 {
		return
	}
	snaps := make([]usage.LimitSnapshot, 0, len(ev.Limits.Windows))
	for _, lw := range ev.Limits.Windows {
		snaps = append(snaps, usage.LimitSnapshot{
			AgentKind: kind, WindowID: lw.ID, Kind: lw.Kind, Label: lw.Label, UsedPct: lw.UsedPct,
			ResetsAt: lw.ResetsAt, DurationMins: lw.DurationMins, Plan: cur.Plan, At: at,
		})
	}
	if _, err := w.app.Usage.RecordLimits(context.Background(), snaps); err != nil {
		slog.Warn("deck: record limits", "error", err)
	}
}

// mergeLimits folds a new report into what is known of a kind: windows by
// id (several agents of a kind share the account), the plan when named. A
// report with no windows does not wipe out windows already known.
func mergeLimits(old, nu deckagent.Limits) deckagent.Limits {
	out := deckagent.Limits{Plan: old.Plan, Unavailable: nu.Unavailable}
	if nu.Plan != "" {
		out.Plan = nu.Plan
	}
	byID := map[string]int{}
	for _, lw := range old.Windows {
		byID[lw.ID] = len(out.Windows)
		out.Windows = append(out.Windows, lw)
	}
	for _, lw := range nu.Windows {
		if i, ok := byID[lw.ID]; ok {
			out.Windows[i] = lw
			continue
		}
		byID[lw.ID] = len(out.Windows)
		out.Windows = append(out.Windows, lw)
	}
	if len(out.Windows) > 0 {
		out.Unavailable = ""
	}
	return out
}

// LiveLimits implements [UsageReader].
func (w *DeckWorkspace) LiveLimits() map[string]LiveLimits {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[string]LiveLimits, len(w.limits))
	for k, l := range w.limits {
		l.Windows = append([]deckagent.LimitWindow(nil), l.Windows...)
		out[k] = l
	}
	return out
}

// UsageService implements [UsageReader].
func (w *DeckWorkspace) UsageService() usage.Service {
	if !w.usageOK() {
		return nil
	}
	return w.app.Usage
}
