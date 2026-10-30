package model

import (
	"cmp"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dev-dami/deck/internal/ui/dialog"
	"github.com/dev-dami/deck/internal/ui/styles"
	"github.com/dev-dami/deck/internal/ui/tone"
	"github.com/dev-dami/deck/internal/ui/util"
	"github.com/dev-dami/deck/internal/workspace"
)

// The thread's lead (docs/design/lead-agent-and-subthreads.md): the one
// agent the person talks to. The editor's last row shows it, like the
// desktop composer's agent chip, and ctrl+l opens the Agent picker.

// leadTTL is the backstop interval for re-reading the lead, which the
// desktop or the phone may change too.
var leadTTL = 2 * time.Second

// leadMsg delivers the session's lead, read off the UI loop.
type leadMsg struct {
	// forSession is the session it was read for, gen the lead generation
	// at dispatch; a result that raced a switch or a pick is dropped.
	forSession string
	gen        uint64
	lead       workspace.Lead
}

// leadPickedMsg says an Agent picker pick reached the engine (or failed).
type leadPickedMsg struct {
	info util.InfoMsg
}

// dispatchLeadRefresh reads the session's lead off the Update goroutine;
// nil while a read is in flight or a pick is being applied.
func (m *UI) dispatchLeadRefresh() tea.Cmd {
	if m.leadInFlight || m.leadPicking || m.com == nil || m.com.Workspace == nil {
		return nil
	}
	m.leadInFlight = true
	ws := m.com.Workspace
	sessionID := m.currentSessionID()
	gen := m.leadGen
	return func() tea.Msg {
		return leadMsg{forSession: sessionID, gen: gen, lead: threadLead(ws, sessionID)}
	}
}

// applyLead stores a lead read, or reads again when it is stale.
func (m *UI) applyLead(msg leadMsg) tea.Cmd {
	m.leadInFlight = false
	if msg.forSession != m.currentSessionID() || msg.gen != m.leadGen {
		return m.dispatchLeadRefresh()
	}
	if msg.lead == m.lead && msg.forSession == m.leadSession {
		m.markScrollOnly()
	}
	m.setLead(msg.forSession, msg.lead)
	return nil
}

// setLead writes the lead of sessionID through the cache.
func (m *UI) setLead(sessionID string, l workspace.Lead) {
	m.lead, m.leadSession, m.leadCheckedAt = l, sessionID, time.Now()
}

// leadStale reports whether the backstop should read the lead again.
func (m *UI) leadStale() bool {
	return m.leadSession != m.currentSessionID() || time.Since(m.leadCheckedAt) >= leadTTL
}

// threadLead is the session's lead or, before it has one (or with no
// session), the agent a first message would start: the default agent with
// its configured model and effort.
func threadLead(ws any, sessionID string) workspace.Lead {
	if r, ok := ws.(interface{ Room(string) workspace.Room }); ok && sessionID != "" {
		if l := r.Room(sessionID).Lead; l.Kind != "" {
			return l
		}
	}
	se, ok := ws.(workspace.SettingsEditor)
	if !ok {
		return workspace.Lead{}
	}
	cfg := se.DeckSettings()
	kinds := se.AgentKinds()
	kind := cfg.DefaultAgent
	if !slices.Contains(kinds, kind) {
		if len(kinds) == 0 {
			return workspace.Lead{}
		}
		kind = kinds[0]
	}
	return workspace.Lead{Kind: kind, Model: cfg.Models.For(kind), Effort: cfg.Efforts.For(kind)}
}

// shownLead is the lead the editor shows: before a session exists, the
// agent picked for its first message, else the cached one; zero when not
// known yet.
func (m *UI) shownLead() workspace.Lead {
	if !m.hasSession() && m.pendingLead != nil {
		return pickedLead(*m.pendingLead)
	}
	if m.leadSession != m.currentSessionID() {
		return workspace.Lead{}
	}
	return m.lead
}

// pickedLead is the lead an Agent picker pick makes.
func pickedLead(p dialog.ActionSelectAgentModel) workspace.Lead {
	return workspace.Lead{Kind: p.Kind, Profile: p.Agent, Model: p.Model, Effort: p.Effort}
}

// leadLine is the editor's last row: the lead and the key that changes it.
func (m *UI) leadLine(width int) string {
	h := m.keyMap.Models.Help()
	return renderLeadLine(m.com.Styles, m.shownLead(), h.Key, h.Desc, width)
}

// renderLeadLine draws "✳ claude · opus · high  ctrl+l agent" in width
// cells, after a cell of inset. The hint goes first when it does not fit,
// then the label is cut; an unknown lead shows the hint alone.
func renderLeadLine(t *styles.Styles, l workspace.Lead, key, desc string, width int) string {
	avail := width - 1
	if avail <= 0 {
		return ""
	}
	muted := lipgloss.NewStyle().Foreground(t.Dialog.SecondaryText.GetForeground())
	// As the desktop composer's agent chip: the mark in the kind's tint,
	// the name in the text colour, the model and effort muted.
	name := lipgloss.NewStyle().Foreground(t.Dialog.NormalItem.GetForeground())
	hint := t.Help.ShortKey.Render(key) + " " + t.Help.ShortDesc.Render(desc)
	if l.Kind == "" {
		return " " + ansi.Truncate(hint, avail, "…")
	}
	label := lipgloss.NewStyle().Foreground(tone.Tint(l.Kind)).Render(tone.Mark(l.Kind)) + " " +
		name.Render(cmp.Or(l.Profile, l.Kind))
	if rest := appendLeadParts(nil, cmp.Or(l.Slug, l.Model), l.Effort); len(rest) > 0 {
		label += muted.Render(" · " + strings.Join(rest, " · "))
	}
	if lipgloss.Width(label)+2+lipgloss.Width(hint) <= avail {
		return " " + label + "  " + hint
	}
	return " " + ansi.Truncate(label, avail, "…")
}

func appendLeadParts(out []string, parts ...string) []string {
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// pickerAgents are the Agent picker's first step: every kind that listed
// models, then the named agents, with cur (the shown lead) marked. A named
// lead whose profile is gone is listed still, so the current one shows.
func pickerAgents(choices []workspace.ModelChoice, profiles []workspace.Profile, cur workspace.Lead) []dialog.AgentChoice {
	var out []dialog.AgentChoice
	found := false
	for _, c := range choices {
		if slices.ContainsFunc(out, func(a dialog.AgentChoice) bool { return a.Name == c.Kind }) {
			continue
		}
		a := dialog.AgentChoice{Name: c.Kind, Kind: c.Kind}
		if cur.Profile == "" && cur.Kind == c.Kind {
			a.Current, a.Model, a.Slug, a.Effort = true, cur.Model, cur.Slug, cur.Effort
			found = true
		}
		out = append(out, a)
	}
	for _, p := range profiles {
		a := dialog.AgentChoice{Name: p.Name, Kind: p.Kind, Role: p.Role, Named: true, Model: p.Model, Effort: p.Effort}
		if cur.Profile != "" && strings.EqualFold(cur.Profile, p.Name) {
			a.Current, a.Model, a.Slug, a.Effort = true, cmp.Or(cur.Model, p.Model), cur.Slug, cmp.Or(cur.Effort, p.Effort)
			found = true
		}
		out = append(out, a)
	}
	if !found && cur.Profile != "" {
		out = append(out, dialog.AgentChoice{
			Name: cur.Profile, Kind: cur.Kind, Named: true, Current: true, Model: cur.Model, Slug: cur.Slug, Effort: cur.Effort,
		})
	}
	return out
}
