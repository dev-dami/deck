package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/tui/motion"
	"github.com/dev-dami/deck/internal/tui/theme"
	"github.com/dev-dami/deck/internal/tui/yolo"
)

// Permission modes in the UI: a chip at the right of the line above the
// composer (click it, or press shift+tab, to cycle plan -> ask -> yolo), the
// rainbow sweep that plays before the chip flips to yolo, and the quiet
// transcript lines that report what the mode did.

// Glyphs of the three modes.
const (
	glyphPlan = "◇"
	glyphAsk  = "●"
	glyphYolo = "▲"
)

// flashLife is how long the chip glows after it flips to yolo.
const flashLife = 600 * time.Millisecond

// fightState is the yolo animation in flight. The session already is in yolo
// while it plays; the chip keeps showing the mode it came from until it ends.
type fightState struct {
	anim   *yolo.Anim
	active bool
	thread string
	from   agent.Mode
}

// chipHitKind says what a click on a region of the chip line does.
type chipHitKind int

const (
	hitMode  chipHitKind = iota + 1 // cycle the permission mode
	hitModel                        // open the model picker
)

// chipHit is a clickable span of the chip line: cells [x0, x1) from the left
// edge of the main area.
type chipHit struct {
	x0, x1 int
	kind   chipHitKind
	agent  string // hitModel: the member, "" for the model a new agent will start with
}

func modeWord(mode agent.Mode) string {
	switch mode {
	case agent.ModePlan:
		return "plan"
	case agent.ModeYolo:
		return "yolo"
	}
	return "ask"
}

func modeGlyph(mode agent.Mode) string {
	switch mode {
	case agent.ModePlan:
		return glyphPlan
	case agent.ModeYolo:
		return glyphYolo
	}
	return glyphAsk
}

// modeOf is the mode a thread is shown in: the real one, except while the yolo
// fight plays, when the chip still shows the mode it came from.
func (m *model) modeOf(th session.ThreadInfo) agent.Mode {
	if m.fight.active && m.fight.thread == th.ID {
		return m.fight.from
	}
	if th.Mode != "" {
		return th.Mode
	}
	return m.mgr.Mode(th.ID)
}

// curMode is the shown mode of the selected thread (the start mode when there
// is none yet).
func (m *model) curMode() agent.Mode {
	th, _ := m.selThread()
	return m.modeOf(th)
}

// modeChip is the styled "◇ plan" / "● ask" / "▲ yolo".
func (m *model) modeChip() string {
	mode := m.curMode()
	text := modeGlyph(mode) + " " + modeWord(mode)
	switch mode {
	case agent.ModePlan:
		return m.ramp(theme.Muted, theme.Accent, 0.55).Render(text)
	case agent.ModeYolo:
		s := lipgloss.NewStyle().Foreground(theme.Danger).Bold(true)
		if m.modeFlash.Moving(m.now) {
			s = lipglossFg(m.blend(theme.Fg, theme.Danger, m.modeFlash.Progress(m.now))).Bold(true)
		}
		return s.Render(text)
	}
	return sMuted.Render(text)
}

// ruleStyle is the hairline above the composer: faint red while in yolo.
func (m *model) ruleStyle() lipgloss.Style {
	if m.curMode() == agent.ModeYolo {
		return m.ramp(theme.Faint, theme.Danger, 0.5)
	}
	return st(theme.Rule)
}

// ---- switching ----

// cycleMode is shift+tab and a click on the chip: plan -> ask -> yolo -> plan.
func (m *model) cycleMode() []tea.Cmd {
	m.skipFight()
	id, err := m.ensureThread()
	if err != nil {
		return []tea.Cmd{m.toast.show(err.Error(), true, m.now)}
	}
	return m.setMode(id, session.NextMode(m.mgr.Mode(id)))
}

// setMode changes the thread's mode in the session at once. Switching into yolo
// then plays the fight (cosmetic: the session is already in yolo) and flips the
// chip at its end; every other switch, and any switch in reduced motion, shows
// at once. If the session refuses, nothing plays and the toast says why.
func (m *model) setMode(id string, next agent.Mode) []tea.Cmd {
	prev := m.mgr.Mode(id)
	if prev == next {
		return nil
	}
	if err := m.mgr.SetMode(id, next); err != nil {
		return []tea.Cmd{m.toast.show(err.Error(), true, m.now)}
	}
	if next == agent.ModeYolo && id == m.sel && !motion.UserReduced() {
		f := &m.fight
		f.anim.SetDark(m.dark)
		f.anim.Start(m.now)
		if !f.anim.Done(m.now) {
			f.active, f.thread, f.from = true, id, prev
			return nil
		}
	}
	m.modeLanded(id, next)
	return nil
}

// skipFight ends a fight in progress right now (any key, click, thread switch).
func (m *model) skipFight() {
	if !m.fight.active {
		return
	}
	m.fight.anim.Skip()
	m.finishFight()
}

// stepFight finishes the fight once the animation is done.
func (m *model) stepFight() {
	if m.fight.active && !m.fight.anim.Advance(m.now) {
		m.finishFight()
	}
}

func (m *model) finishFight() {
	id := m.fight.thread
	m.fight.active = false
	if m.mgr.Mode(id) == agent.ModeYolo {
		m.modeLanded(id, agent.ModeYolo)
	}
}

// fightShown reports whether the rainbow is on screen: the selected thread is
// switching to yolo.
func (m *model) fightShown() bool { return m.fight.active && m.fight.thread == m.sel }

// modeLanded is the moment a mode shows on the chip: the transcript says what
// it means and, for yolo, the chip flashes. Each mode is announced once.
func (m *model) modeLanded(id string, mode agent.Mode) {
	if m.noted[id] == mode {
		return
	}
	m.noted[id] = mode
	it := newItem(kMode)
	it.mode = mode
	m.view(id).add(it)
	if mode == agent.ModeYolo {
		m.modeFlash.Start(m.now)
	}
}

// onModeChanged follows a mode change the session reports. Ones this model made
// itself have landed already (or are about to, when the fight ends).
func (m *model) onModeChanged(msg session.ModeChangedMsg) {
	if msg.Mode != m.mgr.Mode(msg.ThreadID) { // overtaken by a later change
		return
	}
	if m.fight.active && m.fight.thread == msg.ThreadID && msg.Mode == agent.ModeYolo {
		return // the fight lands it
	}
	m.modeLanded(msg.ThreadID, msg.Mode)
}

// modeText is the transcript line announcing a mode.
func modeText(mode agent.Mode) string {
	switch mode {
	case agent.ModePlan:
		return "plan — agents investigate and propose; edits and commands are declined"
	case agent.ModeYolo:
		return "yolo on — Deck approves everything in this thread"
	}
	return "ask — agents ask before they edit or run anything"
}

func (it *item) renderMode(c *rctx) []string {
	glyph := sFaint
	if it.mode == agent.ModeYolo {
		glyph = sDanger
	}
	ls := wrapText(modeText(it.mode), max(8, c.tw-4))
	out := make([]string, len(ls))
	for i, l := range ls {
		if i == 0 {
			out[i] = "  " + glyph.Render(modeGlyph(it.mode)) + " " + sFaint.Render(l)
			continue
		}
		out[i] = "    " + sFaint.Render(l)
	}
	return out
}

func (it *item) renderModel(c *rctx) []string {
	kind := c.m.kindOf(it.joinID)
	name := c.m.agentName(it.joinID)
	what := " now on " + it.text
	if it.text == "" {
		what = " now on its default model"
	}
	line := "  " + tintStyle(kind).Render(theme.Mark(kind)+" "+name) + sFaint.Render(what)
	return []string{trunc(line, c.w)}
}

// renderAuto is the faint line for something Deck decided on its own.
func (it *item) renderAuto(c *rctx) []string {
	sum := oneLine(it.text)
	var head, tail string
	if it.denied {
		head = sWarn.Render("⊘") + " "
		tail = "declined (" + modeWord(agent.ModePlan) + ")  " + sum
	} else {
		head = sAdd.Render("✓") + " "
		tail = "auto-approved  " + sum
	}
	return []string{"  " + head + sFaint.Render(trunc(tail, max(4, c.w-6)))}
}

// ---- messages from the session ----

// onAuto adds the line for a request Deck answered itself.
func (m *model) onAuto(thread, agentID, summary string, denied bool) {
	it := newItem(kAuto)
	it.speaker, it.text, it.denied = agentID, summary, denied
	m.view(thread).add(it)
}

// onModelChanged follows a model switch the session reports.
func (m *model) onModelChanged(msg session.ModelChangedMsg) {
	it := newItem(kModel)
	it.joinID, it.text = msg.AgentID, msg.Model
	m.view(msg.ThreadID).add(it)
}

// onNotice shows a note from Deck: in the thread it belongs to, or, with no
// thread, once as a warning toast.
func (m *model) onNotice(msg session.NoticeMsg) []tea.Cmd {
	if msg.ThreadID != "" {
		it := newItem(kNotice)
		it.text = msg.Text
		m.view(msg.ThreadID).add(it)
		return nil
	}
	if m.seenGlobal[msg.Text] {
		return nil
	}
	m.seenGlobal[msg.Text] = true
	return []tea.Cmd{m.toast.showWarn(msg.Text, m.now)}
}
