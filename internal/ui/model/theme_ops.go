package model

import (
	"log/slog"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/ui/dialog"
	"github.com/dev-dami/deck/internal/ui/styles"
	"github.com/dev-dami/deck/internal/ui/util"
)

// Theme actions that write the config (and theme files) do so in a
// command: setting a config field rewrites the config file and reloads
// it, which is file I/O the event loop must not wait on. The outcome comes
// back as a message and is applied on the Update goroutine exactly as the
// action used to apply it inline.

// activeThemeField is the config field holding the active theme's name.
const activeThemeField = "options.tui.active_theme"

// themeSwitchedMsg reports that switching to a theme was saved, or why not.
type themeSwitchedMsg struct {
	name   string
	styles styles.Styles
	err    error
}

// switchThemeCmd saves name as the active theme; s is its loaded styles.
func (m *UI) switchThemeCmd(name string, s styles.Styles) tea.Cmd {
	ws := m.com.Workspace
	return func() tea.Msg {
		err := ws.SetConfigField(config.ScopeGlobal, activeThemeField, name)
		return themeSwitchedMsg{name: name, styles: s, err: err}
	}
}

func (m *UI) handleThemeSwitched(msg themeSwitchedMsg) tea.Cmd {
	if msg.err != nil {
		if m.preThemeStyles != nil {
			m.applyTheme(*m.preThemeStyles)
			m.preThemeStyles = nil
		}
		return util.ReportError(msg.err)
	}
	m.applyTheme(msg.styles)
	m.preThemeStyles = nil
	m.userThemeSelected = true
	m.dialog.CloseDialog(dialog.ThemeID)
	return util.ReportInfo("Theme switched to " + msg.name)
}

// themeCreatedMsg reports that a new theme was written and made active,
// or why not.
type themeCreatedMsg struct {
	name string
	err  error
}

// createThemeCmd writes a new theme name from the resolved palette of
// base (or of the default theme when base no longer resolves) and makes
// it the active theme.
func (m *UI) createThemeCmd(name, base string) tea.Cmd {
	ws := m.com.Workspace
	return func() tea.Msg {
		exported, err := styles.ExportResolvedPalette(base)
		if err != nil {
			// Fall back to the default theme when the base theme is no
			// longer resolvable (e.g. a user theme that was since deleted).
			exported, err = styles.ExportResolvedPalette("deck")
			if err != nil {
				return themeCreatedMsg{name: name, err: err}
			}
		}
		savePath, err := styles.ThemePath(name)
		if err != nil {
			return themeCreatedMsg{name: name, err: err}
		}
		// ExportResolvedPalette already pins Base to the built-in root the
		// palette was resolved from. Keep it so the new theme stays
		// loadable even if a user theme used as the source is later
		// deleted or renamed.
		if err := styles.SaveThemeFile(savePath, exported); err != nil {
			return themeCreatedMsg{name: name, err: err}
		}
		err = ws.SetConfigField(config.ScopeGlobal, activeThemeField, name)
		return themeCreatedMsg{name: name, err: err}
	}
}

func (m *UI) handleThemeCreated(msg themeCreatedMsg) tea.Cmd {
	if msg.err != nil {
		return util.ReportError(msg.err)
	}
	m.dialog.CloseDialog(dialog.ThemeNewID)
	m.dialog.CloseDialog(dialog.ThemeID)
	m.openThemeEditorDialog(msg.name)
	return util.ReportInfo("Created new theme: " + msg.name)
}

// themeRenamedMsg reports that a theme was renamed, or why not.
type themeRenamedMsg struct {
	oldName, newName string
	err              error
}

// renameThemeCmd renames theme oldName to newName and, when it is the
// active theme (wasActive), points the config at the new name, renaming
// the file back if that fails.
func (m *UI) renameThemeCmd(oldName, newName string, wasActive bool) tea.Cmd {
	ws := m.com.Workspace
	return func() tea.Msg {
		oldPath, newPath, err := styles.RenameThemeFile(oldName, newName)
		if err != nil {
			return themeRenamedMsg{oldName: oldName, newName: newName, err: err}
		}
		if wasActive {
			if err := ws.SetConfigField(config.ScopeGlobal, activeThemeField, newName); err != nil {
				if rollbackErr := os.Rename(newPath, oldPath); rollbackErr != nil {
					slog.Error("Failed to roll back theme rename", "error", rollbackErr)
				}
				return themeRenamedMsg{oldName: oldName, newName: newName, err: err}
			}
		}
		return themeRenamedMsg{oldName: oldName, newName: newName}
	}
}

func (m *UI) handleThemeRenamed(msg themeRenamedMsg) tea.Cmd {
	if msg.err != nil {
		return util.ReportError(msg.err)
	}
	m.dialog.CloseDialog(dialog.ThemeID)
	m.openThemeDialog()
	return util.ReportInfo("Renamed theme " + msg.oldName + " to " + msg.newName)
}

// themeDeletedMsg reports that a theme was deleted, or why not. When it
// was the active theme, styles holds the default theme now active.
type themeDeletedMsg struct {
	name      string
	wasActive bool
	styles    styles.Styles
	err       error
}

// deleteThemeCmd deletes theme name and, when it is the active theme
// (wasActive), resets the config to the default theme and loads it.
func (m *UI) deleteThemeCmd(name string, wasActive bool) tea.Cmd {
	ws := m.com.Workspace
	return func() tea.Msg {
		msg := themeDeletedMsg{name: name, wasActive: wasActive}
		if msg.err = styles.DeleteThemeFile(name); msg.err != nil {
			return msg
		}
		if !wasActive {
			return msg
		}
		def := styles.DefaultThemeName()
		if msg.err = ws.SetConfigField(config.ScopeGlobal, activeThemeField, def); msg.err != nil {
			return msg
		}
		msg.styles, msg.err = styles.LoadTheme(def)
		return msg
	}
}

func (m *UI) handleThemeDeleted(msg themeDeletedMsg) tea.Cmd {
	if msg.err != nil {
		return util.ReportError(msg.err)
	}
	if msg.wasActive {
		m.applyTheme(msg.styles)
		m.preThemeStyles = nil
	}
	m.dialog.CloseDialog(dialog.ThemeID)
	m.openThemeDialog()
	return util.ReportInfo("Deleted theme " + msg.name)
}
