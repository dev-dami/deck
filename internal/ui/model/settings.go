package model

import (
	tea "charm.land/bubbletea/v2"

	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/ui/dialog"
	"github.com/dev-dami/deck/internal/ui/motion"
	"github.com/dev-dami/deck/internal/ui/util"
	"github.com/dev-dami/deck/internal/workspace"
)

// openSettingsDialog opens the settings dialog, on its Usage page when id
// is [dialog.SettingsUsageID] and its Team page for
// [dialog.SettingsTeamID]. An open one is brought to the front on that
// page.
func (m *UI) openSettingsDialog(id string) tea.Cmd {
	tab := dialog.SettingsTabGeneral
	switch id {
	case dialog.SettingsUsageID:
		tab = dialog.SettingsTabUsage
	case dialog.SettingsTeamID:
		tab = dialog.SettingsTabTeam
	}
	if d, ok := m.dialog.Dialog(dialog.SettingsID).(*dialog.Settings); ok {
		m.dialog.BringToFront(dialog.SettingsID)
		if id != dialog.SettingsID && d.Tab() != tab {
			return d.SetTab(tab)
		}
		return nil
	}
	d := dialog.NewSettings(m.com, tab)
	m.dialog.OpenDialog(d)
	return d.InitialCmd()
}

// openPeerPairDialog opens the Add a teammate wizard over the settings, or
// brings an open one to the front.
func (m *UI) openPeerPairDialog() {
	if m.dialog.ContainsDialog(dialog.PeerPairID) {
		m.dialog.BringToFront(dialog.PeerPairID)
		return
	}
	m.dialog.OpenDialog(dialog.NewPeerPair(m.com))
}

// openUsageDialog opens the full Usage view, or brings an open one to the
// front.
func (m *UI) openUsageDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.UsageID) {
		m.dialog.BringToFront(dialog.UsageID)
		return nil
	}
	d := dialog.NewUsage(m.com)
	m.dialog.OpenDialog(d)
	return d.InitialCmd()
}

// applyDeckSettings saves a change to Deck's own settings, off the UI loop.
// The dialog shows the change already.
func (m *UI) applyDeckSettings(msg dialog.ActionSetDeckSettings) tea.Cmd {
	editor, ok := m.com.Workspace.(workspace.SettingsEditor)
	if !ok || msg.Edit == nil {
		return util.ReportWarn("Deck settings are not available here")
	}
	return func() tea.Msg {
		if err := editor.SetDeckSettings(msg.Edit); err != nil {
			return util.ReportError(err)()
		}
		// Motion is read by the animations on every frame (an atomic, not
		// model state), so it takes effect at once.
		motion.Set(editor.DeckSettings().Motion)
		return util.NewInfoMsg(msg.Label)
	}
}

// setTUIOption saves one TUI option, off the UI loop.
func (m *UI) setTUIOption(msg dialog.ActionSetTUIOption) tea.Cmd {
	return func() tea.Msg {
		if err := m.com.Workspace.SetConfigField(config.ScopeGlobal, msg.Key, msg.Value); err != nil {
			return util.ReportError(err)()
		}
		return util.NewInfoMsg(msg.Label)
	}
}
