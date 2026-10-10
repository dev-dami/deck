package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sahilm/fuzzy"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/tui/motion"
	"github.com/dev-dami/deck/internal/tui/theme"
)

// The model picker: an upward menu, in the style of the @ and / menus, that
// lists the models a CLI itself offers. In a thread with several agents it
// first asks which one. The composer is borrowed for the filter (your draft
// waits and comes back), so typing narrows the list and text that matches
// nothing can still be used as a model id.

type pickStage int

const (
	pickMembers pickStage = iota + 1
	pickModels
	pickThreads // ctrl+k: switch to another thread
)

// modelListTimeout bounds how long listing a CLI's models may take.
const modelListTimeout = 15 * time.Second

type picker struct {
	open  bool
	stage pickStage
	h     motion.Spring
	draft string // the composer text to give back on close

	thread  string
	kind    string // models stage: the CLI whose models are listed
	agentID string // models stage: the member to switch, "" for the model new agents start with
	cur     string // models stage: the model it runs now
	running string // models stage: the slug the CLI reports it runs (member only)

	members []session.AgentInfo // member stage
	models  []agent.ModelInfo
	loading bool
	loadErr error
	noList  bool // the CLI cannot list its models
	seq     int  // identifies the load in flight

	items []menuItem
	q     string // the filter the rows were built for
	built bool   // items match q and the stage
	sel   int

	rowItem []int // item index of each row last drawn, -1 for title/info
}

func (p *picker) visible() bool { return p.open }

// modelsMsg is the result of listing a CLI's models.
type modelsMsg struct {
	seq  int
	kind string
	list []agent.ModelInfo
	err  error
}

func (m *model) loadModelsCmd(seq int, kind string) tea.Cmd {
	mgr := m.mgr
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), modelListTimeout)
		defer cancel()
		list, err := mgr.Models(ctx, kind)
		return modelsMsg{seq: seq, kind: kind, list: list, err: err}
	}
}

// openPicker opens the picker for one member (agentID), or, with "", for the
// selected thread: its only member, a chooser between several, or the kind a
// new agent will start as.
func (m *model) openPicker(agentID string) []tea.Cmd {
	if m.ov.open || m.prompt.active() {
		return nil
	}
	m.skipFight()
	th, _ := m.selThread()
	var live []session.AgentInfo
	for _, a := range th.Members {
		if a.Status != session.StatusExited {
			live = append(live, a)
		}
	}
	p := &m.pick
	p.draft = m.comp.Value()
	m.comp.Reset()
	m.focus = focusComposer
	*p = picker{open: true, h: p.h, draft: p.draft, thread: th.ID, seq: p.seq}
	m.menu.kind = 0
	m.menu.h.Set(0)

	switch {
	case agentID != "":
		a, ok := m.agentInfo(agentID)
		if !ok {
			m.closePicker()
			return nil
		}
		return []tea.Cmd{m.pickModels(a.Kind, a.ID, a.Model)}
	case len(live) >= 2:
		p.stage, p.members = pickMembers, live
		m.syncPick()
		return nil
	case len(live) == 1:
		return []tea.Cmd{m.pickModels(live[0].Kind, live[0].ID, live[0].Model)}
	}
	return []tea.Cmd{m.pickModels(m.newKind, "", m.newModelFor(m.newKind))}
}

// openPickerKind opens the picker for the model new agents of a kind start with.
func (m *model) openPickerKind(kind string) []tea.Cmd {
	cmds := m.openPicker("")
	if m.pick.open && m.pick.agentID == "" && m.pick.kind != kind {
		cmds = []tea.Cmd{m.pickModels(kind, "", m.newModelFor(kind))}
	}
	return cmds
}

// pickModels moves to the model list of a kind and starts loading it.
func (m *model) pickModels(kind, agentID, cur string) tea.Cmd {
	p := &m.pick
	p.stage, p.kind, p.agentID, p.cur = pickModels, kind, agentID, cur
	p.running = ""
	if a, ok := m.agentInfo(agentID); ok && agentID != "" {
		p.running = a.RunningModel
	}
	p.models, p.loadErr, p.noList, p.loading = nil, nil, false, true
	p.seq++
	p.built = false
	p.sel = 0
	m.comp.Reset()
	m.syncPick()
	return m.loadModelsCmd(p.seq, kind)
}

func (m *model) onModels(msg modelsMsg) {
	p := &m.pick
	if !p.open || p.stage != pickModels || msg.seq != p.seq {
		return
	}
	p.loading = false
	p.models, p.loadErr = msg.list, msg.err
	p.noList = msg.err == nil && len(msg.list) == 0
	p.built = false
	m.syncPick()
}

func (m *model) closePicker() {
	p := &m.pick
	if !p.open {
		return
	}
	p.open = false
	p.seq++ // a load still in flight is dropped
	p.h.Set(0)
	if p.draft == "" {
		m.comp.Reset()
	} else {
		m.comp.SetValue(p.draft)
	}
	p.draft = ""
}

// isCurrent reports whether a listed model is what the target runs now.
func (p *picker) isCurrent(mi agent.ModelInfo) bool {
	return mi.ID == p.cur || (p.running != "" && mi.ID == p.running) || (p.cur == "" && mi.Default) || (p.cur == "default" && mi.Default)
}

// slugOf is what a model is called on the command line: its id, or "default"
// for the CLI's own default (an empty id).
func slugOf(mi agent.ModelInfo) string {
	if mi.ID == "" {
		return "default"
	}
	return mi.ID
}

// syncPick rebuilds the rows from the filter in the composer. Models are listed
// by slug (the id you would pass to the CLI); the display name and description
// follow, faint. With no filter every model shows, in the CLI's order.
func (m *model) syncPick() {
	p := &m.pick
	if !p.open {
		return
	}
	q := strings.TrimSpace(m.comp.Value())
	if p.built && q == p.q {
		return
	}
	p.built, p.q = true, q
	var all []menuItem
	var hay []string
	switch p.stage {
	case pickMembers:
		for _, a := range p.members {
			all = append(all, menuItem{label: a.Name, kind: a.Kind, val: a.ID, desc: m.modelLabel(a)})
			hay = append(hay, a.Name+" "+a.Kind)
		}
	case pickThreads:
		for _, t := range m.rows {
			var names []string
			for _, a := range t.Members {
				names = append(names, a.Name)
			}
			desc := strings.Join(names, " + ")
			if !t.Active.IsZero() {
				desc = strings.TrimPrefix(desc+" "+glyphDot+" "+ago(t.Active, m.now), " "+glyphDot+" ")
			}
			it := menuItem{label: threadTitle(t), desc: desc, val: t.ID, cur: t.ID == m.sel}
			if t.Resolved {
				it.tag = "resolved"
			}
			all = append(all, it)
			hay = append(hay, threadTitle(t)+" "+strings.Join(names, " "))
		}
	case pickModels:
		for _, mi := range p.models {
			slug := slugOf(mi)
			desc := mi.Description
			if mi.Name != "" && !strings.EqualFold(mi.Name, slug) {
				desc = strings.TrimSpace(mi.Name + " " + glyphDot + " " + desc)
				desc = strings.TrimSuffix(desc, " "+glyphDot)
			}
			it := menuItem{label: slug, desc: desc, val: mi.ID, cur: p.isCurrent(mi)}
			if mi.Default {
				it.tag = "default"
			}
			all = append(all, it)
			hay = append(hay, slug+" "+mi.Name)
		}
	}
	if q == "" {
		p.items = all
	} else {
		p.items = nil
		for _, mt := range fuzzy.Find(q, hay) {
			it := all[mt.Index]
			for _, i := range mt.MatchedIndexes {
				if i < len([]rune(it.label)) {
					it.idx = append(it.idx, i)
				}
			}
			p.items = append(p.items, it)
		}
		if p.stage == pickModels {
			exact := false
			for _, mi := range p.models {
				if strings.EqualFold(slugOf(mi), q) {
					exact = true
				}
			}
			if !exact {
				p.items = append(p.items, menuItem{label: "use " + q, val: q, free: true})
			}
		}
	}
	p.sel = 0
	if q == "" && p.stage == pickModels {
		for i, it := range p.items {
			if it.cur {
				p.sel = i
			}
		}
	}
	p.h.Set(1) // no rise animation: the list shows whole at once
	p.h.Snap()
}

// pickInfo is the faint line above the choices while models load or fail.
func (m *model) pickInfo() []string {
	p := &m.pick
	if p.stage != pickModels {
		return nil
	}
	switch {
	case p.loading:
		return []string{"   " + sFaint.Render("loading models from "+p.kind+"…")}
	case p.loadErr != nil:
		return []string{"   " + sFaint.Render("couldn't list models: "+oneLine(p.loadErr.Error())+" — type a model id")}
	case p.noList:
		return []string{"   " + sFaint.Render(p.kind+" doesn't list its models — type a model id")}
	}
	return nil
}

// pickRows renders the whole picker: a title, the choices (a window around the
// cursor when they don't fit in max rows). rowItem records which item each
// rendered row is, for mouse clicks.
func (m *model) pickRows(w, maxRows int) []string {
	p := &m.pick
	title := "Select a model"
	switch p.stage {
	case pickMembers:
		title = "Whose model?"
	case pickThreads:
		title = "Switch thread"
	}
	head := " " + sBold.Render(title)
	if p.stage == pickModels && p.running != "" {
		head += sFaint.Render(" " + glyphDot + " running " + p.running)
	}
	out := []string{trunc(head, w)}
	p.rowItem = []int{-1}
	for _, l := range m.pickInfo() {
		out = append(out, trunc(l, w))
		p.rowItem = append(p.rowItem, -1)
	}
	room := max(3, maxRows-len(out))
	n := min(len(p.items), room)
	start := 0
	if p.sel >= n {
		start = p.sel - n + 1
	}
	labelW := 0
	for _, it := range p.items {
		if !it.free {
			labelW = max(labelW, sw(it.label))
		}
	}
	labelW = min(labelW, max(8, w/2))
	for i := start; i < start+n && i < len(p.items); i++ {
		out = append(out, m.pickRow(p.items[i], i, i == p.sel, labelW, w))
		p.rowItem = append(p.rowItem, i)
	}
	return out
}

func (m *model) pickRow(it menuItem, i int, selected bool, labelW, w int) string {
	p := &m.pick
	cursor := "  "
	if selected {
		cursor = sAccent.Render("❯ ")
	}
	num := "   "
	if p.q == "" && i < 9 && !it.free {
		num = sFaint.Render(fmt.Sprintf("%d. ", i+1))
	}
	if p.stage == pickMembers {
		ts := tintStyle(it.kind)
		if selected {
			ts = ts.Bold(true)
		}
		mark := theme.Mark(it.kind) + " "
		name := highlightMatches(mark+it.label, shiftIdx(it.idx, len([]rune(mark))), 0, ts)
		return trunc(" "+cursor+num+name+sFaint.Render("  "+it.desc), w)
	}
	style := sFg
	switch {
	case it.free:
		style = sMuted
	case selected:
		style = sBold
	}
	label := highlightMatches(trunc(it.label, labelW), it.idx, 0, style)
	if it.free {
		label = style.Render(trunc(it.label, max(4, w-8)))
	}
	left := " " + cursor + num + padTo(label, labelW)
	right := ""
	if it.cur {
		right = sAccent.Render("✓ current")
	} else if it.tag != "" {
		right = sFaint.Render(it.tag)
	}
	if it.desc != "" {
		room := w - sw(left) - 2 - sw(right) - 2
		if room >= 6 {
			left += "  " + sFaint.Render(trunc(oneLine(it.desc), room))
		}
	}
	if right != "" {
		return rightAlign(left, right, w-1)
	}
	return trunc(left, w)
}

func shiftIdx(idx []int, by int) []int {
	if len(idx) == 0 {
		return nil
	}
	out := make([]int, len(idx))
	for i, v := range idx {
		out[i] = v + by
	}
	return out
}

// pickLines is the picker as drawn over the bottom of the transcript.
func (m *model) pickLines(w int) []string {
	if !m.pick.open {
		return nil
	}
	return m.pickRows(w, max(6, m.g.trH-1))
}

// pickChipLine is the chip line while the picker is open.
func (m *model) pickChipLine(w int) string {
	p := &m.pick
	var what string
	switch {
	case p.stage == pickMembers:
		what = sMuted.Render("pick the agent whose model to change")
	case p.stage == pickThreads:
		what = sMuted.Render("type to find a thread by title or agent")
	case p.agentID != "":
		a, _ := m.agentInfo(p.agentID)
		what = sMuted.Render("model for ") + m.chip(a)
	default:
		what = sMuted.Render("model for the next ") + tintStyle(p.kind).Render(p.kind)
	}
	if q := strings.TrimSpace(m.comp.Value()); q != "" {
		what += sFaint.Render("  "+glyphDot+" filter: ") + sFg.Render(q)
	}
	return trunc("  "+what, w)
}

// ---- keys and mouse ----

func (m *model) pickKey(msg tea.KeyMsg) []tea.Cmd {
	p := &m.pick
	switch msg.String() {
	case "esc":
		m.closePicker()
	case "up", "ctrl+p":
		if n := len(p.items); n > 0 {
			p.sel = (p.sel - 1 + n) % n
		}
	case "down", "ctrl+n":
		if n := len(p.items); n > 0 {
			p.sel = (p.sel + 1) % n
		}
	case "enter":
		return m.pickChoose()
	case "tab", "shift+tab":
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		if strings.TrimSpace(m.comp.Value()) == "" {
			if i := int(msg.String()[0] - '1'); i < len(p.items) {
				p.sel = i
				return m.pickChoose()
			}
			return nil
		}
		return m.editKey(msg)
	default:
		return m.editKey(msg)
	}
	return nil
}

// pickChoose applies the highlighted row.
func (m *model) pickChoose() []tea.Cmd {
	p := &m.pick
	if p.sel < 0 || p.sel >= len(p.items) {
		if q := strings.TrimSpace(m.comp.Value()); q != "" && p.stage == pickModels {
			return m.applyModel(q)
		}
		return nil
	}
	it := p.items[p.sel]
	if p.stage == pickThreads {
		m.closePicker()
		m.selectThread(it.val)
		return nil
	}
	if p.stage == pickMembers {
		a, ok := m.agentInfo(it.val)
		if !ok {
			return nil
		}
		return []tea.Cmd{m.pickModels(a.Kind, a.ID, a.Model)}
	}
	return m.applyModel(it.val)
}

// applyModel switches the target to a model and closes the picker. A live
// member is switched in the session; with no member it sets what new agents of
// the kind start with.
func (m *model) applyModel(id string) []tea.Cmd {
	p := &m.pick
	kind, agentID := p.kind, p.agentID
	m.closePicker()
	if agentID != "" {
		if err := m.mgr.SetModel(agentID, id); err != nil {
			return []tea.Cmd{m.toast.show(err.Error(), true, m.now)}
		}
		return nil
	}
	if id == "" || id == "default" {
		delete(m.newModel, kind)
		m.newDefault[kind] = true
		m.mgr.SetDefaultModel(kind, "")
		m.pushConfig()
		return []tea.Cmd{m.toast.show("next "+kind+" uses its default model", false, m.now)}
	}
	m.newModel[kind] = id
	delete(m.newDefault, kind)
	m.mgr.SetDefaultModel(kind, id)
	m.pushConfig()
	return []tea.Cmd{m.toast.show("next "+kind+" starts on "+id, false, m.now)}
}

// pickMouse handles a click while the picker is open: a choice picks it,
// anywhere else closes the picker.
func (m *model) pickMouse(msg tea.MouseMsg) []tea.Cmd {
	g := m.g
	p := &m.pick
	lines := m.pickLines(g.mainW)
	top := g.topY + g.trH - len(lines)
	if msg.X >= g.mainX && msg.Y >= top && msg.Y < g.topY+g.trH {
		if row := msg.Y - top; row < len(p.rowItem) && p.rowItem[row] >= 0 {
			p.sel = p.rowItem[row]
			return m.pickChoose()
		}
		return nil
	}
	m.closePicker()
	return nil
}
