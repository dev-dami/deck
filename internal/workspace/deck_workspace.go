package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
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
)

// DeckWorkspace is the in-process workspace with Deck's engine in place of
// Deck's own agent: every session is a Deck thread, its agents are the
// user's installed claude, codex, opencode and agy CLIs, and what they do is
// written into the message store the UI draws from. Sessions, messages,
// history, LSP, MCP and config still come from the embedded AppWorkspace.
type DeckWorkspace struct {
	*AppWorkspace
	eng *engine.Engine

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
		path:         filepath.Join(store.Config().Options.DataDirectory, "deck-threads.json"),
	}
	if b, err := os.ReadFile(w.path); err == nil {
		_ = json.Unmarshal(b, &w.threads)
		for s, t := range w.threads {
			w.back[t] = s
		}
	}
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
			_ = w.mgr().Interrupt(a.ID)
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
		kind := w.eng.Config.DefaultAgent
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
				continue // AgentRun stored it already
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
			to := make([]string, len(m.To))
			for i, t := range m.To {
				to[i] = "$" + t
			}
			w.say(m.ThreadID, m.From, "→ "+strings.Join(to, " ")+"\n\n"+m.Text)
		case decks.NoticeMsg:
			w.notice(w.sessionOf(m.ThreadID), m.Text)
		case decks.PausedMsg:
			w.notice(w.sessionOf(m.ThreadID), "Paused after the agents passed messages back and forth; send a message to go on.")
		case decks.MemberAddedMsg:
			w.notice(w.sessionOf(m.ThreadID), m.AgentID+" joined")
		}
	}
}

func (w *DeckWorkspace) onEvent(threadID string, ev deckagent.Event) {
	sid := w.sessionOf(threadID)
	if sid == "" || ev.AgentID == "" {
		return
	}
	w.mu.Lock()
	w.last = ev
	switch ev.Kind {
	case deckagent.KindTurnDone, deckagent.KindExited, deckagent.KindError:
		delete(w.since, ev.AgentID)
	default:
		if w.since[ev.AgentID].IsZero() {
			w.since[ev.AgentID] = time.Now()
		}
	}
	w.mu.Unlock()
	ctx := context.Background()
	t := w.turns[ev.AgentID]
	if t == nil {
		t = &turn{tools: map[string]bool{}, diffs: map[string]deckagent.FileDiff{}}
		w.turns[ev.AgentID] = t
	}
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
		if t.open && len(t.msg.ToolCalls()) > 0 {
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
			t.msg.AppendContent(ev.Text)
			t.streamed = true
		} else {
			setText(&t.msg, ev.Text, t.streamed)
			t.streamed = false
		}
		_ = w.app.Messages.Update(ctx, t.msg)
	case deckagent.KindReasoning:
		fresh()
		if ev.Delta {
			t.msg.AppendReasoningContent(ev.Text)
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
		if !t.tools[ev.Tool.ID] { // a result with no call seen: show the call too
			open()
			name, input := deckTool(ev.Tool)
			t.msg.AddToolCall(message.ToolCall{ID: ev.Tool.ID, Name: name, Input: input, Finished: true})
			t.tools[ev.Tool.ID] = true
			_ = w.app.Messages.Update(ctx, t.msg)
		}
		_, err := w.app.Messages.Create(ctx, sid, message.CreateMessageParams{
			Role:  message.Tool,
			Parts: []message.ContentPart{t.result(ev.Tool)},
		})
		if err != nil {
			slog.Error("deck: tool result", "error", err)
		}
	case deckagent.KindUsage:
		if ev.Usage != nil {
			t.usage = *ev.Usage
		}
	case deckagent.KindError:
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
		w.addUsage(sid, t.usage)
		delete(w.turns, ev.AgentID)
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

// addUsage adds a finished turn's cost to the session and shows how full the
// agent's context is.
func (w *DeckWorkspace) addUsage(sid string, u deckagent.Usage) {
	ctx := context.Background()
	sess, err := w.app.Sessions.Get(ctx, sid)
	if err != nil {
		return
	}
	sess.Cost += u.CostUSD
	if u.ContextTokens > 0 {
		sess.PromptTokens, sess.CompletionTokens = int64(u.ContextTokens), 0
	} else {
		sess.PromptTokens += int64(u.InputTokens)
		sess.CompletionTokens += int64(u.OutputTokens)
	}
	_, _ = w.app.Sessions.Save(ctx, sess)
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
				o.WriteString(l.Text + "\n")
			}
			if l.Op != deckagent.OpDel {
				n.WriteString(l.Text + "\n")
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
	case "Bash":
		cmd := str("command", "cmd")
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
		r.Metadata = enc(tools.BashResponseMetadata{Output: tool.Output})
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
		r.Metadata = enc(tools.ViewResponseMetadata{FilePath: in.FilePath, Content: tool.Output})
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
