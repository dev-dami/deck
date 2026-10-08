package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/agent"
)

type startedMsg struct {
	id  string
	err error
}

type sentMsg struct {
	id  string
	err error
}

type resumedMsg struct {
	id      string
	history []agent.Event
	err     error
}

// closeStreams ends any text or reasoning that is still arriving.
func (m *model) closeStreams(v *agentView, replay bool) {
	if it := v.open; it != nil {
		it.streaming = false
		it.bump()
		if !replay {
			it.mdFade.Start(m.now)
		}
		v.open = nil
	}
	if it := v.openR; it != nil {
		it.streaming = false
		it.bump()
		v.openR = nil
	}
}

func (m *model) finishTools(v *agentView, status agent.ToolStatus) {
	for _, it := range v.items {
		if it.kind == kTool && it.tool.Status == agent.ToolRunning {
			it.tool.Status = status
			it.bump()
		}
	}
}

// onSessionEvent folds one agent event into the transcript. replay is true
// when re-showing stored history (no toasts, flashes or fades).
func (m *model) onSessionEvent(ev agent.Event, replay bool) []tea.Cmd {
	var cmds []tea.Cmd
	v := m.view(ev.AgentID)

	switch ev.Kind {
	case agent.KindUserText:
		m.closeStreams(v, replay)
		it := newItem(kUser)
		it.text = ev.Text
		v.add(it)
		v.follow = true
		v.files = map[string]bool{}
		v.turnCost = 0

	case agent.KindUsage:
		if ev.Usage != nil {
			v.turnCost = ev.Usage.CostUSD // cumulative for the turn
		}

	case agent.KindAssistantText:
		m.closeReasoning(v)
		if ev.Delta {
			if v.open == nil {
				it := newItem(kText)
				it.streaming = true
				v.open = v.add(it)
			}
			v.open.text += ev.Text
			v.open.bump()
		} else {
			it := v.open
			if it == nil {
				it = v.add(newItem(kText))
			}
			it.text = ev.Text // the final text replaces what streamed
			it.streaming = false
			it.bump()
			v.open = nil
			if !replay {
				it.mdFade.Start(m.now)
			}
		}

	case agent.KindReasoning:
		if ev.Delta {
			if v.openR == nil {
				it := newItem(kReasoning)
				it.streaming = true
				v.openR = v.add(it)
			}
			v.openR.text += ev.Text
			v.openR.bump()
		} else {
			it := v.openR
			if it == nil {
				it = v.add(newItem(kReasoning))
			}
			it.text = ev.Text
			it.streaming = false
			it.bump()
			v.openR = nil
		}

	case agent.KindToolCall:
		if ev.Tool == nil {
			break
		}
		m.closeStreams(v, replay)
		if it := v.tools[ev.Tool.ID]; it != nil && ev.Tool.ID != "" {
			it.tool = mergeTool(it.tool, *ev.Tool)
			it.bump()
			break
		}
		it := newItem(kTool)
		it.tool = *ev.Tool
		v.add(it)
		if ev.Tool.ID != "" {
			v.tools[ev.Tool.ID] = it
		}

	case agent.KindToolResult:
		if ev.Tool == nil {
			break
		}
		m.closeStreams(v, replay)
		it := v.tools[ev.Tool.ID]
		if it == nil || ev.Tool.ID == "" {
			it = v.add(newItem(kTool))
			if ev.Tool.ID != "" {
				v.tools[ev.Tool.ID] = it
			}
		}
		it.tool = mergeTool(it.tool, *ev.Tool)
		it.bump()

	case agent.KindFileChange:
		if ev.Diff == nil {
			break
		}
		m.closeStreams(v, replay)
		v.files[ev.Diff.Path] = true
		if it := findToolFor(v, ev.Diff.Path); it != nil {
			it.diff = ev.Diff
			it.bump()
			break
		}
		it := newItem(kTool)
		it.tool = agent.Tool{Name: "Edit", Title: ev.Diff.Path, Status: agent.ToolOK}
		it.diff = ev.Diff
		v.add(it)

	case agent.KindError:
		m.closeStreams(v, replay)
		it := newItem(kError)
		it.text = ev.Text
		v.add(it)

	case agent.KindTurnDone:
		m.closeStreams(v, replay)
		interrupted := strings.EqualFold(strings.TrimSpace(ev.Text), "interrupted")
		if interrupted {
			m.finishTools(v, agent.ToolError)
			it := newItem(kNote)
			it.text = "interrupted"
			v.add(it)
			break
		}
		m.finishTools(v, agent.ToolOK)
		if replay {
			break
		}
		v.flash.Start(m.now)
		cmds = append(cmds, m.toast.show(m.finishedText(v, ev.AgentID), false, m.now))

	case agent.KindExited:
		m.closeStreams(v, replay)
		m.finishTools(v, agent.ToolOK)
		name := m.agentName(ev.AgentID)
		if strings.TrimSpace(ev.Text) == "" {
			it := newItem(kNote)
			it.text = name + " exited"
			v.add(it)
		} else {
			it := newItem(kExit)
			it.text = ev.Text
			v.add(it)
		}
		if !replay {
			m.dropApprovalsFor(ev.AgentID)
		}
	}
	return cmds
}

func (m *model) closeReasoning(v *agentView) {
	if it := v.openR; it != nil {
		it.streaming = false
		it.bump()
		v.openR = nil
	}
}

// finishedText is the turn-complete toast: "codex finished · 2 files · $0.12".
func (m *model) finishedText(v *agentView, id string) string {
	parts := []string{m.agentName(id) + " finished"}
	if n := len(v.files); n > 0 {
		parts = append(parts, plural(n, "file", "files"))
	}
	if c := fmtCost(v.turnCost); c != "" {
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

// findToolFor finds the recent edit-like tool row a file change belongs to.
func findToolFor(v *agentView, path string) *item {
	for i := len(v.items) - 1; i >= 0 && i >= len(v.items)-12; i-- {
		it := v.items[i]
		if it.kind != kTool || it.diff != nil {
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

func (m *model) onApproval(req agent.ApprovalRequest) []tea.Cmd {
	for _, p := range m.pending {
		if p.req.ID == req.ID && p.req.AgentID == req.AgentID {
			return nil
		}
	}
	m.pending = append(m.pending, newPending(req, m.now))
	m.view(req.AgentID).follow = true
	var cmds []tea.Cmd
	if b := m.bell(); b != nil {
		cmds = append(cmds, b)
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

// ---- start / resume results ----

func (m *model) startCmd(kind, prompt, dir, model string) tea.Cmd {
	mgr := m.mgr
	return func() tea.Msg {
		id, err := mgr.Start(kind, prompt, dir, model)
		return startedMsg{id: id, err: err}
	}
}

func (m *model) sendCmd(id, prompt string) tea.Cmd {
	mgr := m.mgr
	return func() tea.Msg { return sentMsg{id: id, err: mgr.Send(id, prompt)} }
}

func (m *model) interruptCmd(id string) tea.Cmd {
	mgr := m.mgr
	return func() tea.Msg { return sentMsg{id: id, err: mgr.Interrupt(id)} }
}

func (m *model) resumeCmd(threadID string) tea.Cmd {
	mgr := m.mgr
	return func() tea.Msg {
		id, hist, err := mgr.Resume(threadID)
		return resumedMsg{id: id, history: hist, err: err}
	}
}

func (m *model) onStarted(msg startedMsg) []tea.Cmd {
	if msg.err != nil {
		return []tea.Cmd{m.toast.show(msg.err.Error(), true, m.now)}
	}
	m.newKind = m.cfg.DefaultAgent
	m.selectAgent(msg.id)
	m.focus = focusComposer
	return nil
}

func (m *model) onResumed(msg resumedMsg) []tea.Cmd {
	if msg.err != nil {
		return []tea.Cmd{m.toast.show(msg.err.Error(), true, m.now)}
	}
	v := m.view(msg.id)
	if len(msg.history) > 0 && len(v.items) == 0 {
		for _, ev := range msg.history {
			m.onSessionEvent(ev, true)
		}
	}
	m.selectAgent(msg.id)
	m.focus = focusComposer
	return nil
}
