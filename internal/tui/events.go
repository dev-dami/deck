package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/session"
)

// sentMsg is the result of a command that talked to the manager.
type sentMsg struct {
	err error
}

// resumedMsg is the result of Resume: the thread and its stored history.
type resumedMsg struct {
	info    session.ThreadInfo
	history []tea.Msg
	err     error
	// shown: the thread's transcript was already on screen when the resume
	// was asked for, so the history is in it.
	shown bool
}

// closeStreams ends any text or reasoning of one agent that is still arriving.
func (m *model) closeStreams(v *threadView, id string, replay bool) {
	if it := v.open[id]; it != nil {
		it.streaming = false
		it.bump()
		if !replay {
			it.mdFade.Start(m.now)
		}
		delete(v.open, id)
	}
	m.closeReasoning(v, id)
}

func (m *model) closeAllStreams(v *threadView, replay bool) {
	for id := range v.open {
		m.closeStreams(v, id, replay)
	}
	for id := range v.openR {
		m.closeReasoning(v, id)
	}
}

func (m *model) closeReasoning(v *threadView, id string) {
	if it := v.openR[id]; it != nil {
		it.streaming = false
		it.bump()
		delete(v.openR, id)
	}
}

func (m *model) finishTools(v *threadView, id string, status agent.ToolStatus) {
	for _, it := range v.items {
		if it.kind == kTool && it.speaker == id && it.tool.Status == agent.ToolRunning {
			it.tool.Status = status
			it.bump()
		}
	}
}

// Deck's own messaging tools. The relay line says what they did, so their tool
// rows stay out of the transcript; reading the thread is a one-liner.
const (
	toolNone = iota
	toolMessage
	toolRead
)

func toolClass(name string) int {
	switch name {
	case "message", "send_message":
		return toolMessage
	case "read", "read_thread":
		return toolRead
	}
	return toolNone
}

// dispatch folds one message from the session manager into the transcripts.
// replay is true when re-showing stored history (no toasts, flashes or fades).
func (m *model) dispatch(msg tea.Msg, replay bool) []tea.Cmd {
	switch msg := msg.(type) {
	case session.EventMsg:
		return m.onEvent(msg, replay)

	case session.RelayMsg:
		v := m.view(msg.ThreadID)
		it := newItem(kRelay)
		it.from, it.rto, it.text, it.via, it.held = msg.From, msg.To, msg.Text, msg.Via, msg.Held
		v.add(it)
		if msg.Via == "mention" {
			v.absorbMention(msg.From, msg.Text)
		}

	case session.PausedMsg:
		v := m.view(msg.ThreadID)
		it := newItem(kPause)
		it.hops = msg.Hops
		if replay {
			it.pstate = pauseStale
		}
		v.add(it)
		v.follow = true

	case session.ContinuedMsg:
		v := m.view(msg.ThreadID)
		for i := len(v.items) - 1; i >= 0; i-- {
			if p := v.items[i]; p.kind == kPause && p.pstate != pauseDone {
				p.pstate = pauseDone
				p.bump()
				break
			}
		}
		for _, it := range v.items { // what was held has been delivered
			if it.kind == kRelay && it.held {
				it.held = false
				it.bump()
			}
		}

	case session.MemberAddedMsg:
		v := m.view(msg.ThreadID)
		first := len(v.known) == 0
		v.known[msg.AgentID] = msg.Kind
		v.order = append(v.order, msg.AgentID)
		if !first { // the first member needs no announcement
			it := newItem(kJoin)
			it.joinID = msg.AgentID
			v.add(it)
		}

	case session.NoticeMsg:
		return m.onNotice(msg)

	case session.ThreadChangedMsg:
		if !replay {
			m.onThreadChanged(msg)
		}

	case session.ModeChangedMsg:
		if !replay {
			m.onModeChanged(msg)
		}

	case session.ModelChangedMsg:
		if !replay {
			m.onModelChanged(msg)
		}

	case session.AutoApprovedMsg:
		m.onAuto(msg.ThreadID, msg.AgentID, msg.Summary, false)

	case session.AutoDeniedMsg:
		m.onAuto(msg.ThreadID, msg.AgentID, msg.Summary, true)

	case session.QueueChangedMsg:
		m.onQueueChanged(msg)

	case session.EditedMsg:
		m.applyEdit(msg)

	case session.RevertedMsg:
		return m.onReverted(msg, replay)

	case session.ApprovalMsg:
		return m.onApproval(msg)

	case session.ApprovalGoneMsg:
		m.dropApproval(msg.ID)
	}
	return nil
}

// onEvent folds one event into the transcript of its thread.
func (m *model) onEvent(msg session.EventMsg, replay bool) []tea.Cmd {
	var cmds []tea.Cmd
	ev := msg.Event
	v := m.view(msg.ThreadID)
	id := ev.AgentID
	if !replay && !ev.Delta && ev.Kind == agent.KindAssistantText && strings.TrimSpace(ev.Text) != "" {
		m.markUnread(msg.ThreadID)
	}

	if ev.Kind == agent.KindUserText {
		if !msg.FromUser() {
			return nil // an adapter's echo of a prompt
		}
		m.closeAllStreams(v, replay)
		if msg.Steer && msg.SteerOf != "" { // a queued message pushed into the turn: tag it, don't repeat it
			for _, o := range v.items {
				if o.kind == kUser && o.entryID == msg.SteerOf {
					o.steer = true
					o.bump()
					v.follow = true
					return nil
				}
			}
		}
		it := newItem(kUser)
		it.text = ev.Text
		it.entryID, it.images, it.steer = msg.EntryID, msg.Images, msg.Steer
		it.to, it.nmembers = msg.To, len(v.known)
		v.add(it)
		v.follow = true
		return nil
	}

	at := ev.Time
	if at.IsZero() {
		at = m.now
	}
	if id != "" && ev.Kind != agent.KindExited && ev.Kind != agent.KindSessionStarted {
		if s := v.turn(id); s.start.IsZero() {
			s.start = at
		}
	}

	switch ev.Kind {
	case agent.KindUsage:
		if ev.Usage != nil {
			v.turn(id).cost = ev.Usage.CostUSD // cumulative for the turn
			v.turn(id).tokens = ev.Usage.OutputTokens
		}

	case agent.KindAssistantText:
		m.closeReasoning(v, id)
		if ev.Delta {
			it := v.open[id]
			if it == nil {
				it = newItem(kText)
				it.speaker = id
				it.streaming = true
				v.open[id] = v.add(it)
			}
			it.text += ev.Text
			it.bump()
		} else {
			it := v.open[id]
			if it == nil {
				it = newItem(kText)
				it.speaker = id
				v.add(it)
			}
			it.text = ev.Text // the final text replaces what streamed
			it.streaming = false
			it.bump()
			delete(v.open, id)
			if !replay {
				it.mdFade.Start(m.now)
			}
		}

	case agent.KindReasoning:
		if ev.Delta {
			it := v.openR[id]
			if it == nil {
				it = newItem(kReasoning)
				it.speaker = id
				it.streaming = true
				v.openR[id] = v.add(it)
			}
			it.text += ev.Text
			it.bump()
		} else {
			it := v.openR[id]
			if it == nil {
				it = newItem(kReasoning)
				it.speaker = id
				v.add(it)
			}
			it.text = ev.Text
			it.streaming = false
			it.bump()
			delete(v.openR, id)
		}

	case agent.KindToolCall:
		if ev.Tool == nil {
			break
		}
		m.closeStreams(v, id, replay)
		key := id + "/" + ev.Tool.ID
		switch toolClass(ev.Tool.Name) {
		case toolMessage:
			v.hide[key] = true
			return cmds
		case toolRead:
			if v.tools[key] == nil {
				it := newItem(kRead)
				it.speaker = id
				v.add(it)
				v.tools[key] = it
			}
			return cmds
		}
		if it := v.tools[key]; it != nil && ev.Tool.ID != "" {
			it.tool = mergeTool(it.tool, *ev.Tool)
			it.bump()
			break
		}
		it := newItem(kTool)
		it.speaker = id
		it.tool = *ev.Tool
		v.add(it)
		if ev.Tool.ID != "" {
			v.tools[key] = it
		}

	case agent.KindToolResult:
		if ev.Tool == nil {
			break
		}
		m.closeStreams(v, id, replay)
		key := id + "/" + ev.Tool.ID
		if v.hide[key] || toolClass(ev.Tool.Name) == toolMessage {
			if ev.Tool.Status == agent.ToolError { // the message did not go out
				it := newItem(kError)
				it.speaker = id
				it.text = "message not delivered: " + oneLine(firstNonEmpty(ev.Tool.Output, ev.Text))
				v.add(it)
			}
			break
		}
		if it := v.tools[key]; it != nil && it.kind == kRead {
			break
		}
		if toolClass(ev.Tool.Name) == toolRead {
			break
		}
		it := v.tools[key]
		if it == nil || ev.Tool.ID == "" {
			it = newItem(kTool)
			it.speaker = id
			v.add(it)
			if ev.Tool.ID != "" {
				v.tools[key] = it
			}
		}
		it.tool = mergeTool(it.tool, *ev.Tool)
		it.bump()

	case agent.KindFileChange:
		if ev.Diff == nil {
			break
		}
		m.closeStreams(v, id, replay)
		v.turn(id).files[ev.Diff.Path] = true
		if it := findToolFor(v, id, ev.Diff.Path); it != nil {
			it.diff = ev.Diff
			it.bump()
			break
		}
		it := newItem(kTool)
		it.speaker = id
		it.tool = agent.Tool{Name: "Edit", Title: ev.Diff.Path, Status: agent.ToolOK}
		it.diff = ev.Diff
		v.add(it)

	case agent.KindError:
		m.closeStreams(v, id, replay)
		it := newItem(kError)
		it.speaker = id
		it.text = ev.Text
		v.add(it)

	case agent.KindTurnDone:
		m.closeStreams(v, id, replay)
		stats := v.turn(id)
		delete(v.stats, id)
		interrupted := strings.EqualFold(strings.TrimSpace(ev.Text), "interrupted")
		if interrupted {
			m.finishTools(v, id, agent.ToolError)
			it := newItem(kNote)
			it.speaker = id
			it.text = "interrupted"
			v.add(it)
			break
		}
		m.finishTools(v, id, agent.ToolOK)
		if foot := turnFooter(stats, at); foot != "" {
			it := newItem(kNote)
			it.speaker = id
			it.text = foot
			v.add(it)
		}
		if replay {
			break
		}
		v.flash.Start(m.now)
		cmds = append(cmds, m.toast.show(m.finishedText(id, stats), false, m.now), m.refreshSum(msg.ThreadID))
		if n := m.notify(m.finishedText(id, stats), threadTitleOf(m, msg.ThreadID)); n != nil {
			cmds = append(cmds, n)
		}

	case agent.KindExited:
		delete(v.stats, id)
		m.closeStreams(v, id, replay)
		m.finishTools(v, id, agent.ToolOK)
		name := m.agentName(id)
		if strings.TrimSpace(ev.Text) == "" {
			it := newItem(kNote)
			it.speaker = id
			it.text = name + " exited"
			v.add(it)
		} else {
			it := newItem(kExit)
			it.speaker = id
			it.text = ev.Text
			v.add(it)
		}
		if !replay {
			m.dropApprovalsFor(id)
		}
	}
	return cmds
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// finishedText is the turn-complete toast: "codex finished · 2 files · $0.12".
func (m *model) finishedText(id string, s *turnStats) string {
	parts := []string{m.agentName(id) + " finished"}
	if n := len(s.files); n > 0 {
		parts = append(parts, plural(n, "file", "files"))
	}
	if c := fmtCost(s.cost); c != "" {
		parts = append(parts, c)
	}
	return strings.Join(parts, " "+glyphDot+" ")
}

func mergeTool(old, nw agent.Tool) agent.Tool {
	if nw.Name == "" {
		nw.Name = old.Name
	}
	if nw.Title == "" {
		nw.Title = old.Title
	}
	if len(nw.Input) == 0 {
		nw.Input = old.Input
	}
	if nw.Output == "" && old.Output != "" && nw.Status == agent.ToolRunning {
		nw.Output = old.Output
	}
	return nw
}

// findToolFor finds the recent edit-like tool row of one agent that a file
// change belongs to.
func findToolFor(v *threadView, id, path string) *item {
	for i := len(v.items) - 1; i >= 0 && i >= len(v.items)-12; i-- {
		it := v.items[i]
		if it.kind != kTool || it.diff != nil || it.speaker != id {
			continue
		}
		t := oneLine(it.tool.Title)
		if t == path || (t != "" && (strings.HasSuffix(path, t) || strings.HasSuffix(t, path))) {
			return it
		}
	}
	return nil
}

// ---- approvals ----

func (m *model) onApproval(msg session.ApprovalMsg) []tea.Cmd {
	req := msg.ApprovalRequest
	for _, p := range m.pending {
		if p.req.ID == req.ID && p.req.AgentID == req.AgentID {
			return nil
		}
	}
	m.pending = append(m.pending, newPending(req, msg.ThreadID, m.now))
	m.view(msg.ThreadID).follow = true
	var cmds []tea.Cmd
	if b := m.bell(); b != nil {
		cmds = append(cmds, b)
	}
	if n := m.notify(m.agentName(req.AgentID)+" needs you", oneLine(req.ToolName+" "+req.Summary)); n != nil {
		cmds = append(cmds, n)
	}
	return cmds
}

func (m *model) dropApproval(id string) {
	for i, p := range m.pending {
		if p.req.ID == id {
			m.pending = append(m.pending[:i], m.pending[i+1:]...)
			break
		}
	}
	if m.prompt.active() && m.prompt.p != nil && m.prompt.p.req.ID == id {
		m.prompt = promptMode{}
		m.comp.Reset()
	}
}

func (m *model) dropApprovalsFor(agentID string) {
	var keep []*pendingApproval
	for _, p := range m.pending {
		if p.req.AgentID != agentID {
			keep = append(keep, p)
		}
	}
	m.pending = keep
}

// respond answers an approval and removes it from the UI at once.
func (m *model) respond(p *pendingApproval, d agent.Decision) tea.Cmd {
	m.dropApproval(p.req.ID)
	respond := p.req.Respond
	if respond == nil {
		return nil
	}
	return func() tea.Msg {
		respond(d)
		return nil
	}
}

// ---- commands that talk to the manager ----

func (m *model) postCmd(threadID, text string, images []agent.Image) tea.Cmd {
	mgr := m.mgr
	return func() tea.Msg {
		_, err := mgr.PostWith(threadID, text, images)
		return sentMsg{err: err}
	}
}

// addCmd starts an agent in a thread without sending it anything.
func (m *model) addCmd(threadID, kind string) tea.Cmd {
	mgr, model := m.mgr, m.newModel[kind]
	return func() tea.Msg {
		_, err := mgr.AddMember(threadID, kind, model)
		return sentMsg{err: err}
	}
}

func (m *model) continueCmd(threadID string) tea.Cmd {
	mgr := m.mgr
	return func() tea.Msg { return sentMsg{err: mgr.Continue(threadID)} }
}

func (m *model) interruptCmd(ids ...string) tea.Cmd {
	mgr := m.mgr
	return func() tea.Msg {
		var first error
		for _, id := range ids {
			if err := mgr.Interrupt(id); err != nil && first == nil {
				first = err
			}
		}
		return sentMsg{err: first}
	}
}

func (m *model) resumeCmd(threadID string) tea.Cmd {
	mgr := m.mgr
	v := m.views[threadID]
	shown := v != nil && len(v.items) > 0
	return func() tea.Msg {
		info, hist, err := mgr.Resume(threadID)
		return resumedMsg{info: info, history: hist, err: err, shown: shown}
	}
}

func (m *model) onResumed(msg resumedMsg) []tea.Cmd {
	if msg.err != nil {
		return []tea.Cmd{m.toast.show(msg.err.Error(), true, m.now)}
	}
	if len(msg.history) > 0 && !msg.shown {
		m.hydrate(msg.info.ID, msg.history)
	}
	m.threads = m.mgr.Threads()
	m.selectThread(msg.info.ID)
	m.focus = focusComposer
	return []tea.Cmd{m.refreshSum(msg.info.ID)}
}

// hydrate lays a resumed thread's stored history down first. Restarting its
// agents can put live lines (session started, notices) on screen before the
// history arrives; those move after it instead of hiding it.
func (m *model) hydrate(id string, history []tea.Msg) {
	live := m.views[id]
	v := newView(id)
	m.views[id] = v
	for _, h := range history {
		m.dispatch(h, true)
	}
	if live == nil {
		return
	}
	v.items = append(v.items, live.items...)
	for _, mp := range [][2]map[string]*item{{v.open, live.open}, {v.openR, live.openR}, {v.tools, live.tools}} {
		for k, it := range mp[1] {
			mp[0][k] = it
		}
	}
	for k, h := range live.hide {
		v.hide[k] = h
	}
	for k, st := range live.stats {
		if v.stats[k] == nil {
			v.stats[k] = st
		}
	}
	for _, a := range live.order {
		if _, ok := v.known[a]; !ok {
			v.known[a] = live.known[a]
			v.order = append(v.order, a)
		}
	}
}
