package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"

	"github.com/dev-dami/deck/internal/app"
	"github.com/dev-dami/deck/internal/config"
	deckagent "github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/engine"
	decks "github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/message"
	"github.com/dev-dami/deck/internal/notify"
	"github.com/dev-dami/deck/internal/permission"
	"github.com/dev-dami/deck/internal/pubsub"
	"github.com/dev-dami/deck/internal/session"
	tools "github.com/dev-dami/deck/internal/toolspec"
	"github.com/dev-dami/deck/internal/usage"
)

// DeckWorkspace is the in-process workspace with Deck's engine in place of
// Deck's own agent: every session is a Deck thread, its agents are the
// user's installed claude, codex, opencode and agy CLIs, and what they do is
// written into the message store the UI draws from. Sessions, messages,
// history, LSP, MCP and config still come from the embedded AppWorkspace.
type DeckWorkspace struct {
	*AppWorkspace
	eng   *engine.Engine
	cfgMu sync.Mutex // guards eng.Config, which the settings and model pickers change

	mu      sync.Mutex
	threads map[string]string // Deck session id -> Deck thread id
	back    map[string]string // Deck thread id -> Deck session id
	path    string            // where the session/thread pairs are kept

	// turns is the message each agent is writing, by Deck agent id. Only
	// the pump goroutine touches it.
	turns map[string]*turn
	// approvals waiting in the UI, by Deck approval id, to cancel when the
	// agent withdraws them.
	approvals map[string]context.CancelFunc
	last      deckagent.Event // the most recent agent event, for AgentModel

	prog *tea.Program    // where busy/idle edges are sent
	busy map[string]bool // last busy state sent, by session
	plan bool            // the UI is in plan mode

	models map[string][]deckagent.ModelInfo // each CLI's models, once asked
	since  map[string]time.Time             // when each busy agent's turn began
	warned map[string]bool                  // agents told they cannot ask in ask mode
	next   map[string]pendingTurn           // what starts each agent's next turn
	limits map[string]LiveLimits            // plan limits by agent kind, as last reported
	// lastMode is the permission mode the last recorded mode change set.
	// Only the pump goroutine touches it.
	lastMode deckagent.Mode

	act activity // tool calls in flight and background shells, by session
}

// turn is what is known about one agent's current reply.
type turn struct {
	msg      message.Message
	open     bool // msg exists in the store
	streamed bool // text arrived as deltas
	thinking bool // reasoning arrived as deltas
	tools    map[string]bool
	diffs    map[string]deckagent.FileDiff // by path, for edit results
	usage    deckagent.Usage
	began    time.Time // when the turn began, kept past an error for the usage record
	trace    *traceBuilder
	trigger  string // what started the turn (usage.Trigger*); "" = not known
	failed   bool   // an error ended part of the turn
	// text and reasoning hold msg's streamed text, so a delta costs its own
	// length instead of a copy of the reply so far.
	text, reasoning message.StreamBuffer
}

// NewDeckWorkspace wraps a and drives eng's agents through it.
func NewDeckWorkspace(a *app.App, store *config.ConfigStore, eng *engine.Engine) *DeckWorkspace {
	w := &DeckWorkspace{
		AppWorkspace: NewAppWorkspace(a, store),
		eng:          eng,
		threads:      map[string]string{},
		back:         map[string]string{},
		turns:        map[string]*turn{},
		approvals:    map[string]context.CancelFunc{},
		busy:         map[string]bool{},
		models:       map[string][]deckagent.ModelInfo{},
		since:        map[string]time.Time{},
		warned:       map[string]bool{},
		next:         map[string]pendingTurn{},
		limits:       map[string]LiveLimits{},
		lastMode:     deckagent.ModeAsk,
		path:         filepath.Join(store.Config().Options.DataDirectory, "deck-threads.json"),
	}
	if b, err := os.ReadFile(w.path); err == nil {
		_ = json.Unmarshal(b, &w.threads)
		for s, t := range w.threads {
			w.back[t] = s
		}
	}
	// The agents' deck.jobs op reads what this workspace's activity tracker sees.
	eng.Manager.SetActivity(w.activityText)
	go w.pump()
	return w
}

func (w *DeckWorkspace) mgr() *decks.Manager { return w.eng.Manager }

// thread returns the live Deck thread for a session, creating or resuming it.
func (w *DeckWorkspace) thread(sessionID string) (string, error) {
	w.mu.Lock()
	id, ok := w.threads[sessionID]
	w.mu.Unlock()
	if ok {
		if _, live := w.mgr().Thread(id); live {
			return id, nil
		}
		if _, _, err := w.mgr().Resume(id); err == nil {
			_ = w.mgr().SetMode(id, w.mode())
			return id, nil
		}
	}
	id, err := w.mgr().NewThread(w.store.WorkingDir())
	if err != nil {
		return "", err
	}
	_ = w.mgr().SetMode(id, w.mode())
	w.mu.Lock()
	w.threads[sessionID], w.back[id] = id, sessionID
	b, _ := json.Marshal(w.threads)
	w.mu.Unlock()
	_ = os.MkdirAll(filepath.Dir(w.path), 0o755)
	_ = os.WriteFile(w.path, b, 0o600)
	return id, nil
}

func (w *DeckWorkspace) sessionOf(threadID string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.back[threadID]
}

func (w *DeckWorkspace) threadOf(sessionID string) (decks.ThreadInfo, bool) {
	w.mu.Lock()
	id, ok := w.threads[sessionID]
	w.mu.Unlock()
	if !ok {
		return decks.ThreadInfo{}, false
	}
	return w.mgr().Thread(id)
}

// -- Agent --

// AgentRun posts the prompt to the session's thread: to the agents it names
// ($claude, $codex, $all), or to whoever spoke last.
func (w *DeckWorkspace) AgentRun(ctx context.Context, sessionID, prompt string, attachments ...message.Attachment) error {
	tid, err := w.thread(sessionID)
	if err != nil {
		return err
	}
	var images []deckagent.Image
	var parts []message.ContentPart
	parts = append(parts, message.TextContent{Text: prompt})
	send := prompt
	for _, a := range attachments {
		if a.IsText() { // a pasted block or an attached file: sent inline
			send += `

<file name="` + a.FileName + `">
` + string(a.Content) + `
</file>`
			parts = append(parts, message.BinaryContent{Path: a.FileName, MIMEType: a.MimeType, Data: a.Content})
			continue
		}
		if !a.IsImage() {
			continue
		}
		p := a.FilePath
		if p == "" {
			f, err := os.CreateTemp("", "deck-*"+filepath.Ext(a.FileName))
			if err != nil {
				continue
			}
			_, _ = f.Write(a.Content)
			f.Close()
			p = f.Name()
		}
		images = append(images, deckagent.Image{Path: p, MediaType: a.MimeType})
		parts = append(parts, message.BinaryContent{Path: a.FileName, MIMEType: a.MimeType, Data: a.Content})
	}
	if _, err := w.app.Messages.Create(ctx, sessionID, message.CreateMessageParams{Role: message.User, Parts: parts}); err != nil {
		return err
	}
	if _, err := w.mgr().PostWith(tid, send, images); err != nil {
		w.notice(sessionID, err.Error())
		return nil
	}
	if sess, err := w.app.Sessions.Get(ctx, sessionID); err == nil && (sess.Title == "" || sess.Title == "New Session") {
		sess.Title = firstLine(prompt, 60)
		_, _ = w.app.Sessions.Save(ctx, sess)
	}
	return nil
}

func (w *DeckWorkspace) AgentRunChannel(ctx context.Context, _ string, sessionID, prompt string, attachments ...message.Attachment) error {
	return w.AgentRun(ctx, sessionID, prompt, attachments...)
}

// AgentCancel interrupts every agent at work in the session.
func (w *DeckWorkspace) AgentCancel(sessionID string) {
	th, ok := w.threadOf(sessionID)
	if !ok {
		return
	}
	for _, a := range th.Members {
		if busy(a) {
			if w.mgr().Interrupt(a.ID) == nil {
				w.recordBehavior(sessionID, a.ID, usage.BehaviorInterrupt, "", 1)
			}
		}
	}
}

func busy(a decks.AgentInfo) bool {
	switch a.Status {
	case decks.StatusWorking, decks.StatusTool, decks.StatusApproval:
		return true
	}
	return false
}

func (w *DeckWorkspace) AgentIsBusy() bool {
	for _, a := range w.mgr().Agents() {
		if busy(a) {
			return true
		}
	}
	return false
}

func (w *DeckWorkspace) AgentIsSessionBusy(sessionID string) bool {
	th, ok := w.threadOf(sessionID)
	if !ok {
		return false
	}
	for _, a := range th.Members {
		if busy(a) {
			return true
		}
	}
	return false
}

// AgentModel describes the agent that spoke last.
func (w *DeckWorkspace) AgentModel() AgentModel {
	id := w.lastAgent()
	a, ok := w.mgr().Agent(id)
	if !ok {
		kind := w.DeckSettings().DefaultAgent
		return AgentModel{
			CatwalkCfg: catwalk.Model{ID: kind, Name: kind},
			ModelCfg:   config.SelectedModel{Provider: kind, Model: kind},
		}
	}
	name := a.RunningModel
	if name == "" {
		name = a.Model
	}
	if name == "" {
		name = a.Kind
	}
	return AgentModel{
		CatwalkCfg: catwalk.Model{ID: name, Name: a.Name + " · " + name, ContextWindow: int64(a.ContextWindow)},
		ModelCfg:   config.SelectedModel{Provider: a.Kind, Model: name},
	}
}

func (w *DeckWorkspace) lastAgent() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.last.AgentID
}

func (w *DeckWorkspace) AgentIsReady() bool   { return true }
func (w *DeckWorkspace) AgentReadyErr() error { return nil }

func (w *DeckWorkspace) queue(sessionID string) []decks.QueuedItem {
	w.mu.Lock()
	id, ok := w.threads[sessionID]
	w.mu.Unlock()
	if !ok {
		return nil
	}
	return w.mgr().Queue(id)
}

func (w *DeckWorkspace) AgentQueuedPrompts(sessionID string) int { return len(w.queue(sessionID)) }

func (w *DeckWorkspace) AgentQueuedPromptsList(sessionID string) []string {
	var out []string
	for _, q := range w.queue(sessionID) {
		out = append(out, q.Text)
	}
	return out
}

func (w *DeckWorkspace) AgentClearQueue(sessionID string) {
	for _, q := range w.queue(sessionID) {
		_ = w.mgr().CancelQueued(q.ID)
	}
}

// AgentSetMain switches between plan and code: in plan mode the agents may
// read but change nothing.
func (w *DeckWorkspace) AgentSetMain(agentID string) error {
	w.mu.Lock()
	w.plan = agentID == config.AgentPlan
	w.mu.Unlock()
	return w.applyMode()
}

// PermissionSetSkipRequests is yolo: every agent's requests are approved,
// the agents themselves run in their own unattended modes.
func (w *DeckWorkspace) PermissionSetSkipRequests(skip bool) {
	w.AppWorkspace.PermissionSetSkipRequests(skip)
	_ = w.applyMode()
}

// mode is the Deck permission mode the UI's plan and yolo switches add up to.
func (w *DeckWorkspace) mode() deckagent.Mode {
	w.mu.Lock()
	plan := w.plan
	w.mu.Unlock()
	switch {
	case plan:
		return deckagent.ModePlan
	case w.AppWorkspace.PermissionSkipRequests():
		return deckagent.ModeYolo
	}
	return deckagent.ModeAsk
}

// applyMode puts every live thread in the current mode.
func (w *DeckWorkspace) applyMode() error {
	mode := w.mode()
	w.mu.Lock()
	ids := make([]string, 0, len(w.threads))
	for _, id := range w.threads {
		ids = append(ids, id)
	}
	w.mu.Unlock()
	var first error
	for _, id := range ids {
		if _, live := w.mgr().Thread(id); !live {
			continue
		}
		if err := w.mgr().SetMode(id, mode); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (w *DeckWorkspace) AgentSummarize(context.Context, string) error {
	return errors.New("each agent manages its own context; ask it to /compact")
}

func (w *DeckWorkspace) UpdateAgentModel(context.Context) error             { return nil }
func (w *DeckWorkspace) InitCoderAgent(context.Context) error               { return nil }
func (w *DeckWorkspace) InitCoderAgentNonInteractive(context.Context) error { return nil }

// -- Events --

// Subscribe also tells the engine the program is there (nothing to do: the
// pump writes to the stores, whose events the AppWorkspace forwards).
func (w *DeckWorkspace) Subscribe(program *tea.Program) {
	w.mu.Lock()
	w.prog = program
	w.mu.Unlock()
	w.AppWorkspace.Subscribe(program)
}

// edge tells the UI when a session goes from idle to busy or back, so its
// busy indicator and "done" notification follow the agents.
func (w *DeckWorkspace) edge(sid string, failed bool) {
	now := w.AgentIsSessionBusy(sid)
	w.mu.Lock()
	was, prog := w.busy[sid], w.prog
	w.busy[sid] = now
	w.mu.Unlock()
	if now == was || prog == nil {
		return
	}
	n := notify.Notification{SessionID: sid, Type: notify.TypeAgentStarted}
	if !now {
		n.Type = notify.TypeAgentFinished
		if failed {
			n.Type = notify.TypeAgentError
		}
		if sess, err := w.app.Sessions.Get(context.Background(), sid); err == nil {
			n.SessionTitle = sess.Title
		}
	}
	go prog.Send(pubsub.Event[notify.Notification]{Type: pubsub.UpdatedEvent, Payload: n})
}

// pump turns the engine's events into messages in the store.
func (w *DeckWorkspace) pump() {
	for msg := range w.mgr().Updates() {
		switch m := msg.(type) {
		case decks.EventMsg:
			if m.FromUser() {
				w.onUserMessage(m) // AgentRun stored the message already
				continue
			}
			w.onEvent(m.ThreadID, m.Event)
		case decks.ApprovalMsg:
			w.onApproval(m)
		case decks.ApprovalGoneMsg:
			w.mu.Lock()
			cancel := w.approvals[m.ID]
			delete(w.approvals, m.ID)
			w.mu.Unlock()
			if cancel != nil {
				cancel()
			}
		case decks.RelayMsg:
			w.onRelay(m)
			to := make([]string, len(m.To))
			for i, t := range m.To {
				to[i] = "$" + t
			}
			w.say(m.ThreadID, m.From, "→ "+strings.Join(to, " ")+"\n\n"+m.Text)
		case decks.NoticeMsg:
			w.notice(w.sessionOf(m.ThreadID), m.Text)
		case decks.PausedMsg:
			w.recordBehavior(w.sessionOf(m.ThreadID), "", usage.BehaviorHopPause, "", m.Hops)
			w.notice(w.sessionOf(m.ThreadID), "Paused after the agents passed messages back and forth; send a message to go on.")
		case decks.ContinuedMsg:
			w.recordBehavior(w.sessionOf(m.ThreadID), "", usage.BehaviorContinue, "", 1)
		case decks.MemberAddedMsg:
			w.notice(w.sessionOf(m.ThreadID), m.AgentID+" joined")
		case decks.AutoApprovedMsg:
			w.recordBehavior(w.sessionOf(m.ThreadID), m.AgentID, usage.BehaviorApproveAllow, "auto", 1)
		case decks.AutoDeniedMsg:
			w.recordBehavior(w.sessionOf(m.ThreadID), m.AgentID, usage.BehaviorApproveDeny, "auto", 1)
		case decks.ModeChangedMsg:
			// One switch in the UI changes every live thread, and a new
			// thread is put in the current mode: count the switch once.
			if m.Mode != w.lastMode {
				w.lastMode = m.Mode
				w.recordBehavior(w.sessionOf(m.ThreadID), "", usage.BehaviorModeChange, string(m.Mode), 1)
			}
		case decks.ModelChangedMsg:
			if m.Changed == "effort" {
				w.recordBehavior(w.sessionOf(m.ThreadID), m.AgentID, usage.BehaviorEffortChange, m.Effort, 1)
			} else {
				w.recordBehavior(w.sessionOf(m.ThreadID), m.AgentID, usage.BehaviorModelChange, m.Model, 1)
			}
		case decks.EditedMsg:
			w.recordBehavior(w.sessionOf(m.ThreadID), "", usage.BehaviorQueueEdit, "", 1)
		case decks.QueueCancelledMsg:
			w.recordBehavior(w.sessionOf(m.ThreadID), m.AgentID, usage.BehaviorQueueCancel, "", 1)
		case decks.TaskChangedMsg:
			w.projectBoard(m.ThreadID)
			switch {
			case m.Task.Status == "failed" && m.From != "failed": // not an edit of a failed task
				detail := "retry"
				if m.Task.Attempts >= m.Task.Tries() {
					detail = "final"
				}
				w.recordBehavior(w.sessionOf(m.ThreadID), m.Task.Owner, usage.BehaviorTaskFail, detail, 1)
			case m.From == "failed" && m.Task.Status == "ready":
				w.recordBehavior(w.sessionOf(m.ThreadID), "", usage.BehaviorTaskRetry, "", 1)
			}
		case decks.TaskSteeredMsg:
			detail := "user"
			if m.By != "" {
				detail = "agent"
			}
			w.recordBehavior(w.sessionOf(m.ThreadID), m.AgentID, usage.BehaviorTaskSteer, detail, 1)
		case decks.RevertedMsg:
			kind := usage.BehaviorRevert
			if m.Undo {
				kind = usage.BehaviorUnrevert
			}
			w.recordBehavior(w.sessionOf(m.ThreadID), "", kind, "", max(1, len(m.Files)))
		case decks.ThreadChangedMsg:
			if !m.Gone {
				w.syncTitle(m.ThreadID)
			}
		}
	}
}

// syncTitle copies a thread's title (model-written or renamed) onto its Crush
// session, which is what the sidebar and notices show.
func (w *DeckWorkspace) syncTitle(threadID string) {
	sid := w.sessionOf(threadID)
	if sid == "" {
		return
	}
	info, ok := w.mgr().Thread(threadID)
	if !ok || info.Title == "" {
		return
	}
	ctx := context.Background()
	if sess, err := w.app.Sessions.Get(ctx, sid); err == nil && sess.Title != info.Title {
		sess.Title = info.Title
		_, _ = w.app.Sessions.Save(ctx, sess)
	}
}

func (w *DeckWorkspace) onEvent(threadID string, ev deckagent.Event) {
	if ev.Kind == deckagent.KindLimits {
		if ev.AgentID != "" {
			w.onLimits(ev) // about the account: no turn, no message
		}
		return
	}
	sid := w.sessionOf(threadID)
	if sid == "" || ev.AgentID == "" {
		return
	}
	var began time.Time // when a finishing turn began; zero when unknown
	var start time.Time // when the turn this event belongs to began; zero when unknown
	w.mu.Lock()
	w.last = ev
	switch ev.Kind {
	case deckagent.KindSessionStarted:
		// The CLI is up (codex says so at start-up, Claude at each turn):
		// not yet a turn.
	case deckagent.KindTurnDone, deckagent.KindExited, deckagent.KindError:
		began = w.since[ev.AgentID]
		delete(w.since, ev.AgentID)
		start = began
	default:
		if w.since[ev.AgentID].IsZero() {
			// A turn starts: at the post that addressed the agent, when
			// that found it idle, else now.
			now := time.Now()
			if p := w.next[ev.AgentID]; !p.at.IsZero() && p.at.Before(now) {
				now = p.at
			}
			w.since[ev.AgentID] = now
		}
		start = w.since[ev.AgentID]
	}
	w.mu.Unlock()
	if ev.Kind == deckagent.KindSessionStarted {
		w.warnNoAsk(sid, ev.AgentID)
		w.observeActivity(sid, ev)
		w.edge(sid, false)
		return
	}
	w.observeActivity(sid, ev)
	ctx := context.Background()
	t := w.turns[ev.AgentID]
	if t == nil {
		traceStart := start
		if traceStart.IsZero() {
			traceStart = time.Now()
		}
		t = &turn{tools: map[string]bool{}, diffs: map[string]deckagent.FileDiff{}, began: start, trace: newTraceBuilder(traceStart)}
		w.mu.Lock()
		t.trigger = w.next[ev.AgentID].trigger
		delete(w.next, ev.AgentID)
		w.mu.Unlock()
		w.turns[ev.AgentID] = t
	}
	t.trace.observe(ev)
	open := func() {
		if t.open {
			return
		}
		kind := ev.AgentID
		if a, ok := w.mgr().Agent(ev.AgentID); ok {
			kind = a.Kind
		}
		m, err := w.app.Messages.Create(ctx, sid, message.CreateMessageParams{Role: message.Assistant, Model: ev.AgentID, Provider: kind})
		if err != nil {
			slog.Error("deck: create message", "error", err)
			return
		}
		t.msg, t.open, t.streamed, t.thinking = m, true, false, false
	}
	// Text after a tool call starts a new message, as a new step does.
	fresh := func() {
		if t.open && t.msg.HasToolCalls() {
			t.msg.AddFinish(message.FinishReasonToolUse, "", "")
			_ = w.app.Messages.Update(ctx, t.msg)
			t.open = false
		}
		open()
	}
	switch ev.Kind {
	case deckagent.KindAssistantText:
		fresh()
		if ev.Delta {
			t.msg.AppendContentBuf(&t.text, ev.Text)
			t.streamed = true
		} else {
			setText(&t.msg, ev.Text, t.streamed)
			t.streamed = false
		}
		_ = w.app.Messages.Update(ctx, t.msg)
	case deckagent.KindReasoning:
		fresh()
		if ev.Delta {
			t.msg.AppendReasoningContentBuf(&t.reasoning, ev.Text)
			t.thinking = true
		} else if !t.thinking {
			t.msg.AppendReasoningContent(ev.Text)
		}
		_ = w.app.Messages.Update(ctx, t.msg)
	case deckagent.KindToolCall:
		if ev.Tool == nil {
			return
		}
		open()
		if t.msg.ReasoningContent().Thinking != "" {
			t.msg.FinishThinking()
		}
		name, input := deckTool(ev.Tool)
		t.msg.AddToolCall(message.ToolCall{ID: ev.Tool.ID, Name: name, Input: input, Finished: true})
		t.tools[ev.Tool.ID] = true
		_ = w.app.Messages.Update(ctx, t.msg)
		if ev.Tool.Name == "TodoWrite" {
			w.setTodos(sid, ev.Tool.Input)
		}
	case deckagent.KindFileChange:
		if ev.Diff != nil {
			t.diffs[ev.Diff.Path] = *ev.Diff
		}
	case deckagent.KindToolResult:
		if ev.Tool == nil {
			return
		}
		name, input := deckTool(ev.Tool)
		if !t.tools[ev.Tool.ID] { // a result with no call seen: show the call too
			open()
			t.msg.AddToolCall(message.ToolCall{ID: ev.Tool.ID, Name: name, Input: input, Finished: true})
			t.tools[ev.Tool.ID] = true
			_ = w.app.Messages.Update(ctx, t.msg)
		}
		_, err := w.app.Messages.Create(ctx, sid, message.CreateMessageParams{
			Role:  message.Tool,
			Parts: []message.ContentPart{t.resultAs(ev.Tool, name, input)},
		})
		if err != nil {
			slog.Error("deck: tool result", "error", err)
		}
	case deckagent.KindUsage:
		if u := ev.Usage; u != nil {
			if u.InputTokens == 0 && u.OutputTokens == 0 && u.CachedTokens == 0 && u.CostUSD == 0 {
				// A mid-turn context update: the turn's totals come later.
				t.usage.ContextTokens, t.usage.ContextWindow = u.ContextTokens, u.ContextWindow
			} else {
				t.usage = *u
			}
		}
	case deckagent.KindError:
		if !began.IsZero() && (t.began.IsZero() || began.Before(t.began)) {
			t.began = began
		}
		if retrying(ev.Text) {
			w.recordBehavior(sid, ev.AgentID, usage.BehaviorRetry, "", 1)
		} else {
			t.failed = true
			w.recordBehavior(sid, ev.AgentID, usage.BehaviorError, "", 1)
		}
		open()
		t.msg.AddFinish(message.FinishReasonError, "Error", ev.Text)
		_ = w.app.Messages.Update(ctx, t.msg)
		t.open = false
	case deckagent.KindTurnDone, deckagent.KindExited:
		if t.open {
			reason := message.FinishReasonEndTurn
			if ev.Text == "interrupted" {
				reason = message.FinishReasonCanceled
			}
			if t.msg.ReasoningContent().Thinking != "" {
				t.msg.FinishThinking()
			}
			t.msg.AddFinish(reason, "", "")
			_ = w.app.Messages.Update(ctx, t.msg)
		}
		// The turn's own start outlives an error mid-turn, which restarts
		// the agent's clock.
		if !t.began.IsZero() && (began.IsZero() || t.began.Before(began)) {
			began = t.began
		}
		var took time.Duration
		end := time.Now()
		if !began.IsZero() {
			took = end.Sub(began)
		}
		if n := len(t.diffs); n > 0 {
			added, removed := 0, 0
			for _, d := range t.diffs {
				added, removed = added+d.Added, removed+d.Removed
			}
			w.recordBehavior(sid, ev.AgentID, usage.BehaviorTurnEdit, fmt.Sprintf("+%d/-%d", added, removed), n)
		}
		meta := turnMeta{status: turnStatus(ev, t.failed), trigger: t.trigger, trace: t.trace, end: end}
		if th, ok := w.mgr().Thread(threadID); ok {
			meta.mode = string(th.Mode)
		}
		w.addUsage(sid, ev.AgentID, t.usage, took, meta)
		delete(w.turns, ev.AgentID)
	}
	if ev.Delta {
		// A delta comes from an agent at work: once the session shows
		// busy it changes nothing, and checking costs a look at every
		// member. The event that ends the turn does the check.
		w.mu.Lock()
		shown := w.busy[sid]
		w.mu.Unlock()
		if shown {
			return
		}
	}
	w.edge(sid, ev.Kind == deckagent.KindError)
}

// setTodos shows an agent's to-do list (Claude Code's TodoWrite) in the
// session's to-do panel.
func (w *DeckWorkspace) setTodos(sid string, input json.RawMessage) {
	var in struct {
		Todos []struct {
			Content    string `json:"content"`
			Status     string `json:"status"`
			ActiveForm string `json:"activeForm"`
		} `json:"todos"`
	}
	if json.Unmarshal(input, &in) != nil || len(in.Todos) == 0 {
		return
	}
	if th, ok := w.threadOf(sid); ok && len(w.mgr().Tasks(th.ID)) > 0 {
		return // the thread's task board is the to-do list (projectBoard)
	}
	ctx := context.Background()
	sess, err := w.app.Sessions.Get(ctx, sid)
	if err != nil {
		return
	}
	sess.Todos = sess.Todos[:0]
	for _, td := range in.Todos {
		sess.Todos = append(sess.Todos, session.Todo{Content: td.Content, Status: session.TodoStatus(td.Status), ActiveForm: td.ActiveForm})
	}
	_, _ = w.app.Sessions.Save(ctx, sess)
}

// projectBoard shows the thread's task board as the session's to-do list, so
// the to-do pill follows the board. Cancelled tasks are left out.
func (w *DeckWorkspace) projectBoard(threadID string) {
	sid := w.sessionOf(threadID)
	tasks := w.mgr().Tasks(threadID)
	if sid == "" || len(tasks) == 0 {
		return
	}
	names := map[string]string{}
	if th, ok := w.mgr().Thread(threadID); ok {
		for _, a := range th.Members {
			names[a.ID] = a.Name
		}
	}
	ctx := context.Background()
	sess, err := w.app.Sessions.Get(ctx, sid)
	if err != nil {
		return
	}
	var todos []session.Todo
	for _, tk := range tasks {
		status := session.TodoStatusPending
		switch tk.Status {
		case "cancelled":
			continue
		case "done":
			status = session.TodoStatusCompleted
		case "running":
			status = session.TodoStatusInProgress
		}
		text := tk.ID + " " + tk.Title
		if tk.Owner != "" {
			text += " (" + names[tk.Owner] + ")"
		}
		todos = append(todos, session.Todo{Content: text, Status: status, ActiveForm: text})
	}
	if slices.Equal(todos, sess.Todos) {
		return // a board change that does not show (a note, a brief): no write
	}
	sess.Todos = todos
	_, _ = w.app.Sessions.Save(ctx, sess)
}

// SaveSession is AppWorkspace's, and a title the user gave the session is the
// thread's title from then on (RenameThread): no automatic title replaces it,
// and syncTitle has nothing to undo.
func (w *DeckWorkspace) SaveSession(ctx context.Context, sess session.Session) (session.Session, error) {
	if cur, err := w.app.Sessions.Get(ctx, sess.ID); err == nil && cur.Title != sess.Title {
		w.mu.Lock()
		tid, ok := w.threads[sess.ID]
		w.mu.Unlock()
		if ok {
			_ = w.mgr().RenameThread(tid, sess.Title)
		}
	}
	return w.AppWorkspace.SaveSession(ctx, sess)
}

// addUsage adds a finished turn's tokens and cost to the session and records
// the turn for the Usage view. How full each agent's context is comes from
// the roster, not from the session's token counts, which only ever add up.
func (w *DeckWorkspace) addUsage(sid, agentID string, u deckagent.Usage, took time.Duration, meta turnMeta) {
	if u.InputTokens == 0 && u.OutputTokens == 0 && u.CachedTokens == 0 && u.CostUSD == 0 && took == 0 {
		return // no turn ran (an exit after the turn was already counted)
	}
	ctx := context.Background()
	if sess, err := w.app.Sessions.Get(ctx, sid); err == nil {
		sess.Cost += u.CostUSD
		sess.PromptTokens += int64(u.InputTokens)
		sess.CompletionTokens += int64(u.OutputTokens)
		_, _ = w.app.Sessions.Save(ctx, sess)
	}
	w.recordUsage(ctx, sid, agentID, u, took, meta)
}

// setText puts the final text of a reply in the message: it replaces what
// streamed in, or follows what came before.
func setText(m *message.Message, text string, streamed bool) {
	for i, p := range m.Parts {
		if c, ok := p.(message.TextContent); ok {
			if streamed {
				m.Parts[i] = message.TextContent{Text: text}
			} else {
				m.Parts[i] = message.TextContent{Text: c.Text + "\n\n" + text}
			}
			return
		}
	}
	m.Parts = append(m.Parts, message.TextContent{Text: text})
}

// say writes a whole message from an agent (a relay to another agent).
func (w *DeckWorkspace) say(threadID, agentID, text string) {
	sid := w.sessionOf(threadID)
	if sid == "" {
		return
	}
	kind := agentID
	if a, ok := w.mgr().Agent(agentID); ok {
		kind = a.Kind
	}
	_, _ = w.app.Messages.Create(context.Background(), sid, message.CreateMessageParams{
		Role:     message.Assistant,
		Model:    agentID,
		Provider: kind,
		Parts:    []message.ContentPart{message.TextContent{Text: text}, message.Finish{Reason: message.FinishReasonEndTurn, Time: time.Now().Unix()}},
	})
}

// notice shows a note from Deck itself.
func (w *DeckWorkspace) notice(sid, text string) {
	if sid == "" {
		return
	}
	_, _ = w.app.Messages.Create(context.Background(), sid, message.CreateMessageParams{
		Role:     message.Assistant,
		Model:    "deck",
		Provider: "deck",
		Parts:    []message.ContentPart{message.TextContent{Text: "_" + text + "_"}, message.Finish{Reason: message.FinishReasonEndTurn, Time: time.Now().Unix()}},
	})
}

// noAsk are the agent kinds whose CLI cannot hand Deck an approval prompt:
// in ask mode they are refused whatever needs approval.
var noAsk = map[string]bool{"antigravity": true}

// warnNoAsk tells the user, once per agent, when an agent that cannot ask
// starts in ask mode.
func (w *DeckWorkspace) warnNoAsk(sid, agentID string) {
	a, ok := w.mgr().Agent(agentID)
	if !ok || !noAsk[a.Kind] || w.mode() != deckagent.ModeAsk {
		return
	}
	w.mu.Lock()
	seen := w.warned[agentID]
	w.warned[agentID] = true
	w.mu.Unlock()
	if !seen {
		w.notice(sid, a.Name+" cannot ask you for approval: in ask mode it is refused commands and edits that need one. Use yolo mode to let it run them, or plan mode to keep it read-only.")
	}
}

// onApproval asks the user through Deck's permission dialog and answers the
// agent with what they chose.
func (w *DeckWorkspace) onApproval(m decks.ApprovalMsg) {
	sid := w.sessionOf(m.ThreadID)
	req := m.ApprovalRequest
	if sid == "" {
		if req.Respond != nil {
			req.Respond(deckagent.Decision{Allow: false, Reason: "no session"})
		}
		return
	}
	opts := permission.CreatePermissionRequest{
		SessionID:   sid,
		ToolCallID:  req.ID,
		Description: req.AgentID + " wants to " + strings.ToLower(string(req.Kind)) + ": " + req.Summary,
		Action:      "execute",
		Path:        req.Cwd,
	}
	switch req.Kind {
	case deckagent.ApproveCommand:
		opts.ToolName = tools.BashToolName
		opts.Params = tools.BashPermissionsParams{Command: req.Summary, WorkingDir: req.Cwd, Description: req.AgentID}
	case deckagent.ApproveEdit:
		opts.ToolName = tools.EditToolName
		opts.Action = "write"
		path := req.Summary
		p := tools.EditPermissionsParams{FilePath: path}
		if req.Diff != nil {
			p.FilePath = req.Diff.Path
			p.OldContent, p.NewContent = sides(*req.Diff)
		}
		opts.Params, opts.Path = p, p.FilePath
	default:
		opts.ToolName = req.ToolName
		if opts.ToolName == "" {
			opts.ToolName = "tool"
		}
		b, _ := json.Marshal(map[string]string{"agent": req.AgentID, "request": req.Summary})
		opts.Params = string(b)
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.mu.Lock()
	w.approvals[req.ID] = cancel
	w.mu.Unlock()
	asked := time.Now()
	go func() {
		defer cancel()
		ok, err := w.app.Permissions.Request(ctx, opts)
		w.mu.Lock()
		_, live := w.approvals[req.ID]
		delete(w.approvals, req.ID)
		w.mu.Unlock()
		if !live || req.Respond == nil {
			return
		}
		if err != nil {
			req.Respond(deckagent.Decision{Allow: false, Reason: err.Error()})
			return
		}
		kind := usage.BehaviorApproveDeny
		if ok {
			kind = usage.BehaviorApproveAllow
		}
		w.recordBehavior(sid, req.AgentID, kind, "manual", 1)
		verdict := "deny"
		if ok {
			verdict = "allow"
		}
		w.recordBehavior(sid, req.AgentID, usage.BehaviorApprovalWait, verdict, int(max(1, time.Since(asked).Milliseconds())))
		req.Respond(deckagent.Decision{Allow: ok})
	}()
}

// sides rebuilds the before and after text a diff's hunks cover.
func sides(d deckagent.FileDiff) (before, after string) {
	var o, n strings.Builder
	for i, h := range d.Hunks {
		if i > 0 {
			o.WriteString("…\n")
			n.WriteString("…\n")
		}
		for _, l := range h.Lines {
			if l.Op != deckagent.OpAdd {
				o.WriteString(l.Text)
				o.WriteByte('\n')
			}
			if l.Op != deckagent.OpDel {
				n.WriteString(l.Text)
				n.WriteByte('\n')
			}
		}
	}
	return o.String(), n.String()
}

// deckTool names a Deck tool call the way Deck's chat draws it, with the
// input its renderer reads.
func deckTool(t *deckagent.Tool) (name, input string) {
	var in map[string]any
	_ = json.Unmarshal(t.Input, &in)
	str := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := in[k].(string); ok && v != "" {
				return v
			}
		}
		return ""
	}
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return string(b)
	}
	path := func() string {
		if p := str("file_path", "filePath", "path"); p != "" {
			return p
		}
		return t.Title
	}
	switch t.Name {
	case "Bash", "run_command":
		cmd := str("command", "cmd", "CommandLine")
		if cmd == "" {
			cmd = t.Title
		}
		return tools.BashToolName, enc(tools.BashParams{Command: cmd, Description: str("description")})
	case "Read":
		return tools.ViewToolName, enc(tools.ViewParams{FilePath: path()})
	case "Edit", "MultiEdit":
		return tools.EditToolName, enc(tools.EditParams{FilePath: path(), OldString: str("old_string", "oldString"), NewString: str("new_string", "newString")})
	case "Write":
		return tools.WriteToolName, enc(tools.WriteParams{FilePath: path(), Content: str("content")})
	case "Grep":
		return tools.GrepToolName, enc(tools.GrepParams{Pattern: str("pattern"), Path: str("path"), Include: str("include", "glob")})
	case "Glob":
		return tools.GlobToolName, enc(tools.GlobParams{Pattern: str("pattern"), Path: str("path")})
	case "LS":
		return tools.LSToolName, enc(tools.LSParams{Path: path()})
	case "WebFetch":
		return tools.FetchToolName, enc(tools.FetchParams{URL: str("url"), Format: "markdown"})
	}
	if len(t.Input) > 0 {
		return t.Name, string(t.Input)
	}
	return t.Name, enc(map[string]string{"input": t.Title})
}

// result is a finished tool call as Deck's renderers read it.
func (t *turn) result(tool *deckagent.Tool) message.ToolResult {
	name, input := deckTool(tool)
	return t.resultAs(tool, name, input)
}

// resultAs is result for a tool deckTool has already named.
//
// The output is stored once, in Content: the bash and view renderers fall
// back to it when the metadata has none, so repeating it there would only
// double every result's size in the database.
func (t *turn) resultAs(tool *deckagent.Tool, name, input string) message.ToolResult {
	r := message.ToolResult{ToolCallID: tool.ID, Name: name, Content: tool.Output, IsError: tool.Status == deckagent.ToolError}
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return string(b)
	}
	var in struct {
		FilePath string `json:"file_path"`
	}
	_ = json.Unmarshal([]byte(input), &in)
	switch name {
	case tools.BashToolName:
		var meta tools.BashResponseMetadata
		if tool.Output == tools.BashNoOutput {
			// The renderer takes this Content to mean "nothing to show",
			// but here it is the command's real output.
			meta.Output = tool.Output
		}
		r.Metadata = enc(meta)
	case tools.EditToolName, tools.WriteToolName:
		d, ok := t.diffs[in.FilePath]
		if !ok {
			for p, dd := range t.diffs {
				if strings.HasSuffix(filepath.ToSlash(in.FilePath), filepath.ToSlash(p)) {
					d, ok = dd, true
					break
				}
			}
		}
		if ok {
			before, after := sides(d)
			r.Metadata = enc(tools.EditResponseMetadata{Additions: d.Added, Removals: d.Removed, OldContent: before, NewContent: after})
		}
	case tools.ViewToolName:
		r.Metadata = enc(tools.ViewResponseMetadata{FilePath: in.FilePath})
	}
	return r
}

func firstLine(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > n {
		s = string(r[:n-1]) + "…"
	}
	return s
}

// LSPStart does nothing: Deck's agents run their own language tooling, so
// Deck starts no language servers of its own.
func (w *DeckWorkspace) LSPStart(context.Context, string) {}
