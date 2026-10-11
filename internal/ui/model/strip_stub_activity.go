package model

import tea "charm.land/bubbletea/v2"

// focusPaneActivity focuses a sidebar pane. It stands in for the strip's
// m.focusPane until the strip lands: replace with m.focusPane on merge.
func (m *UI) focusPaneActivity(id paneID) tea.Cmd { return nil }
