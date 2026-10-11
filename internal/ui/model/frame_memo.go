package model

import "github.com/dev-dami/deck/internal/workspace"

// frameMemo holds what one drawn frame reads more than once: the agent
// roster, read by the header, the sidebar's panes and the status line,
// and the git pane's rows, built for both its height and its render. It
// lives only while View draws, so Update and the commands it starts
// always read live state, and each frame reads it fresh.
type frameMemo struct {
	live    workspace.Roster
	roster  map[string][]workspace.AgentLine
	gitRows map[int][]gitRow
}

// Roster implements [workspace.Roster], reading each session's roster
// once per frame. Callers must not modify the returned lines.
func (f *frameMemo) Roster(sessionID string) []workspace.AgentLine {
	if lines, ok := f.roster[sessionID]; ok {
		return lines
	}
	lines := f.live.Roster(sessionID)
	if f.roster == nil {
		f.roster = map[string][]workspace.AgentLine{}
	}
	f.roster[sessionID] = lines
	return lines
}

// beginFrameMemo starts the memo for the frame View is about to draw;
// endFrameMemo drops it.
func (m *UI) beginFrameMemo() {
	m.frameMemo = &frameMemo{}
	if m.com != nil && m.com.Workspace != nil {
		if r, ok := m.com.Workspace.(workspace.Roster); ok {
			m.frameMemo.live = r
		}
	}
}

func (m *UI) endFrameMemo() { m.frameMemo = nil }

// roster returns the workspace's roster: the frame's snapshot while a
// frame is drawn, the live one otherwise. ok is false when the workspace
// has none.
func (m *UI) roster() (workspace.Roster, bool) {
	if f := m.frameMemo; f != nil {
		return f, f.live != nil
	}
	if m.com == nil || m.com.Workspace == nil {
		return nil, false
	}
	r, ok := m.com.Workspace.(workspace.Roster)
	return r, ok
}

// rosterOrNil is roster's result as a nil interface when there is none.
func (m *UI) rosterOrNil() workspace.Roster {
	if r, ok := m.roster(); ok {
		return r
	}
	return nil
}
