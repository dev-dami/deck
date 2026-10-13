package model

import (
	tea "charm.land/bubbletea/v2"
)

// The sidebar is a strip of panes laid out like niri's scrollable tiling:
// panes sit in columns, each column stacks one or more panes and splits its
// height between them by what they need, and the strip scrolls sideways so
// the focused column is always in view. Panes can be closed and reopened.
//
// A pane is not a Bubble Tea model (see internal/ui/AGENTS.md): it is a
// small renderer with imperative hooks that the UI calls. It reads what it
// shows from the UI, and does IO only through the tea.Cmds it returns.

// paneID names a kind of pane. There is at most one pane of each kind.
type paneID string

const (
	paneSession  paneID = "session"  // title, folder and branch, files touched
	paneAgents   paneID = "agents"   // the session's agents: model, status, context, cost
	paneGit      paneID = "git"      // the git and GitHub browser
	paneTasks    paneID = "tasks"    // the agents' running subagents
	paneCommands paneID = "commands" // running and background commands, Deck's own jobs
	paneNotices  paneID = "notices"  // the notification history
	paneUsage    paneID = "usage"    // plan limits, today's tokens, the live turn
)

// pane is one sidebar pane.
type pane interface {
	// id is the pane's kind.
	id() paneID
	// title is the pane's name in its header, e.g. "Git".
	title(m *UI) string
	// badge is a short note drawn after the title (a count, "#12 ✓"), or "".
	badge(m *UI) string
	// width is the column width the pane would like, in cells; 0 takes the
	// default. A column is as wide as its widest pane wants.
	width() int
	// height is how many lines the pane's body needs at width; the column
	// shares its height out by these, so a pane with little to show stays
	// small. Return 0 to collapse to just the header.
	height(m *UI, width int) int
	// render draws the body (without the header) in width x height. focused
	// is true while the user is in this pane.
	render(m *UI, width, height int, focused bool) string
	// key handles a key while the pane is focused. handled false lets the
	// strip use it (moving between panes, closing).
	key(m *UI, msg tea.KeyPressMsg) (handled bool, cmd tea.Cmd)
	// click handles a mouse click at x, y inside the body.
	click(m *UI, x, y int) tea.Cmd
}

// Scrolling. render gets the body's height. A pane may return more lines
// than that: the strip then scrolls the body itself, keeping an offset per
// pane that the wheel and the j/k, ↑/↓, pgup/pgdn and g/G keys move (keys
// only when the pane's key did not handle them; j/k move to the next pane
// once the body is at its end). The strip shows ↑/↓ in the header while
// there is more above/below, and click's y counts content lines, offset
// included, so a pane can map it straight to its own line. A pane with its
// own cursor or scroll (a list) instead returns at most height lines,
// handles the keys in key and may implement paneWheeler to take the wheel.

// paneWheeler is optional: a pane that scrolls itself takes the wheel over
// its body. delta is in lines, negative up. handled false lets the strip
// scroll the body.
type paneWheeler interface {
	wheel(m *UI, delta int) (handled bool)
}

// paneFactories builds each kind of pane. Pane files register themselves in
// an init func, so adding a pane never touches the strip.
var paneFactories = map[paneID]func() pane{}

func registerPane(id paneID, f func() pane) { paneFactories[id] = f }

// defaultColumns is the strip a new session starts with.
var defaultColumns = [][]paneID{
	{paneSession, paneAgents, paneTeam},
	{paneGit},
	{paneTasks, paneCommands},
	{paneUsage},
}
