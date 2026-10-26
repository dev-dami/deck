package model

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/ui/completions"
	"github.com/dev-dami/deck/internal/ui/notice"
	"github.com/dev-dami/deck/internal/ui/tone"
	"github.com/dev-dami/deck/internal/workspace"
)

// The composer's sigils (docs/design/agents-and-mentions.md): "@" opens
// the agents and people, "$" is the same popup under its old name, and
// "#" opens the files. A file picked from "#" goes in the message as it
// did from "@": the engine and the agents see the same text.
const (
	sigilAgents    = "@"
	sigilAgentsAlt = "$"
	sigilFiles     = "#"
)

// completionSigil reports whether key opens the completions popup.
func completionSigil(key string) bool {
	return key == sigilAgents || key == sigilAgentsAlt || key == sigilFiles
}

// pathLike reports whether what follows an "@" looks like a file ("src/",
// "main.go", ".github"): it then offers files, as "@" did before "#".
// Agent and profile names hold neither "/" nor ".".
func pathLike(query string) bool {
	return strings.ContainsAny(query, "/.~\\")
}

// agentSigil is the sigil a picked agent goes in the message with: the one
// that opened the popup, "@" unless it was "$".
func (m *UI) agentSigil() string {
	if m.completionsSigil == sigilAgentsAlt {
		return sigilAgentsAlt
	}
	return sigilAgents
}

// openCompletions opens the popup for the sigil just typed at the cursor.
func (m *UI) openCompletions(sigil string) tea.Cmd {
	m.completionsOpen = true
	m.completionsQuery = ""
	m.completionsStartIndex = len(m.textarea.Value())
	m.completionsPositionStart = m.completionsPosition()
	m.completionsSigil = sigil
	if sigil == sigilFiles {
		return m.showFileCompletions()
	}
	m.completionsFiles = false
	return tea.Batch(m.openAgentCompletions(), m.loadProfiles(false))
}

// showFileCompletions switches the popup to the project's files, which load
// off the UI loop (completions.CompletionItemsLoadedMsg).
func (m *UI) showFileCompletions() tea.Cmd {
	m.completionsFiles = true
	depth, limit := config.Completions{}.Limits()
	if cfg := m.com.Config(); cfg != nil && cfg.Options != nil && cfg.Options.TUI != nil {
		depth, limit = cfg.Options.TUI.Completions.Limits()
	}
	return m.completions.Open(depth, limit)
}

// filterCompletions narrows the open popup to query, the text after its
// sigil. After "@" a path-like query switches it to files and back.
func (m *UI) filterCompletions(query string) tea.Cmd {
	m.completionsQuery = query
	if m.completionsSigil == sigilAgents {
		switch files := pathLike(query); {
		case files && !m.completionsFiles:
			// The files arrive later; their handler filters them.
			return m.showFileCompletions()
		case !files && m.completionsFiles:
			m.completionsFiles = false
			m.completions.SetAgentItems(m.agentCompletionItems())
		}
	}
	m.completions.Filter(query)
	return nil
}

// refreshAgentCompletions rebuilds the open agents popup (the models or
// profiles came in), keeping what was typed.
func (m *UI) refreshAgentCompletions() {
	if !m.completionsOpen || m.completionsFiles {
		return
	}
	m.completions.SetAgentItems(m.agentCompletionItems())
	if m.completionsQuery != "" {
		m.completions.Filter(m.completionsQuery)
	}
}

// profilesLoadedMsg carries the project's profiles, read off the UI loop;
// show lists them for /profiles.
type profilesLoadedMsg struct {
	profiles []workspace.Profile
	show     bool
}

// loadProfiles reads the profiles (deck.json) off the UI loop. Nothing when
// the workspace has none.
func (m *UI) loadProfiles(show bool) tea.Cmd {
	ps, ok := m.com.Workspace.(workspace.Profiles)
	if !ok {
		if show {
			return func() tea.Msg { return profilesLoadedMsg{show: true} }
		}
		return nil
	}
	return func() tea.Msg { return profilesLoadedMsg{profiles: ps.ListProfiles(), show: show} }
}

func (m *UI) applyProfiles(msg profilesLoadedMsg) {
	m.profiles = msg.profiles
	m.refreshAgentCompletions()
	if msg.show {
		m.Notify(profilesNotice(msg.profiles))
	}
}

// isProfilesSlash reports whether the message is the /profiles command.
func isProfilesSlash(content string) bool {
	switch strings.TrimSpace(content) {
	case "/profiles", "/profile":
		return true
	}
	return false
}

// profilesNotice lists the profiles, one per line as the roster shows a
// member ("✳ charles · reviewer"), with the kind and where it is kept.
func profilesNotice(ps []workspace.Profile) notice.Notice {
	n := notice.Notice{Level: notice.Info, Source: notice.SourceDeck}
	if len(ps) == 0 {
		n.Title = "No profiles"
		n.Body = `Name agents in deck.json: "profiles": [{"name": "charles", "kind": "claude", "role": "reviewer"}]. Then @charles brings charles in.`
		return n
	}
	n.Title = fmt.Sprintf("Profiles (%d)", len(ps))
	lines := make([]string, 0, len(ps))
	for _, p := range ps {
		lines = append(lines, profileLabel(p)+" ("+p.Kind+", "+p.Scope+")")
	}
	n.Body = strings.Join(lines, "\n")
	n.Action = &notice.Action{ID: notice.ActionNotices, Label: "See all"}
	return n
}

// profileLabel is how a profile or a member from one reads: the kind's
// glyph, the name and the role.
func profileLabel(p workspace.Profile) string {
	return withRole(tone.Mark(p.Kind)+" "+p.Name, p.Role)
}

func withRole(s, role string) string {
	if role = strings.Join(strings.Fields(role), " "); role != "" {
		return s + " · " + role
	}
	return s
}

// peopleItems are the "@" popup rows for who is not a kind: the profiles
// (a member of that name is reached, else one is added from the profile)
// and the teammates. A profile a member is already called is listed once,
// as that member.
func peopleItems(sigil string, lines []workspace.AgentLine, profiles []workspace.Profile, teammates []string) []completions.AgentItem {
	members := map[string]bool{}
	perKind := map[string]int{}
	for _, l := range lines {
		if l.Status != "exited" && l.Status != "ready" {
			members[strings.ToLower(l.ID)] = true
			perKind[l.Kind]++
		}
	}
	var out []completions.AgentItem
	for _, l := range lines {
		// instanceItems lists every member of a kind that has several.
		if l.Profile == "" || l.Status == "exited" || l.Status == "ready" || perKind[l.Kind] > 1 {
			continue
		}
		out = append(out, completions.AgentItem{
			Label: withRole(tone.Mark(l.Kind)+" "+l.ID, l.Role) + " · " + l.Status,
			Value: completions.AgentCompletionValue{Kind: l.Kind, Insert: sigil + l.ID},
		})
	}
	for _, p := range profiles {
		if members[strings.ToLower(p.Name)] {
			continue
		}
		out = append(out, completions.AgentItem{
			Label: profileLabel(p) + " · " + p.Kind + " profile",
			Value: completions.AgentCompletionValue{Kind: p.Kind, Insert: sigil + p.Name},
		})
	}
	for _, name := range teammates {
		out = append(out, completions.AgentItem{
			Label: "◆ " + name + " · teammate",
			Value: completions.AgentCompletionValue{Insert: sigil + name},
		})
	}
	return out
}

// teammateNames are the linked Decks' names, from the last snapshot.
func (m *UI) teammateNames() []string {
	if m.teamSnap == nil {
		return nil
	}
	out := make([]string, 0, len(m.teamSnap.Peers))
	for _, p := range m.teamSnap.Peers {
		out = append(out, p.Name)
	}
	return out
}
