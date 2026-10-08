package tui

import (
	"strings"

	"github.com/dev-dami/deck/internal/session"
)

// Action says what a submitted composer line means.
type Action int

// Actions produced by ParseInput.
const (
	ActNone    Action = iota // nothing to do
	ActSend                  // send Text to the agent AgentID
	ActStart                 // start a new agent of Kind with prompt Text
	ActSelect                // "@name" alone: just select that agent
	ActNewKind               // "@kind" alone: next message starts a new agent of Kind
	ActCommand               // slash command Cmd/Arg
	ActError                 // Err explains what is wrong
)

// Parsed is the result of ParseInput.
type Parsed struct {
	Action  Action
	AgentID string
	Kind    string
	Text    string
	Cmd     string // command name without the slash
	Arg     string
	Err     string
}

// Commands are the five slash commands, in menu order.
var Commands = []struct{ Name, Args, Desc string }{
	{"new", "", "start a fresh agent"},
	{"resume", "", "continue a saved thread"},
	{"model", "<name>", "model for the next agent"},
	{"cd", "<path>", "directory for new agents"},
	{"name", "<name>", "your display name"},
}

func isCommand(name string) bool {
	for _, c := range Commands {
		if c.Name == name {
			return true
		}
	}
	return false
}

func hasKind(kinds []string, k string) bool {
	for _, x := range kinds {
		if x == k {
			return true
		}
	}
	return false
}

// findRunning resolves a name or id to a running (not exited) agent.
func findRunning(agents []session.AgentInfo, name string) (session.AgentInfo, bool) {
	for _, a := range agents {
		if a.Status != session.StatusExited && (a.Name == name || a.ID == name) {
			return a, true
		}
	}
	return session.AgentInfo{}, false
}

// ParseInput decides what a submitted line means.
//
//   - "/cmd arg"            a slash command
//   - "@name msg"           send to that running agent, from anywhere
//   - "@kind msg"           start a new agent of that kind when no running
//     agent has that name (or always, in new-agent mode)
//   - "msg"                 the selected agent; a new default agent when the
//     deck is empty or in new-agent mode
func ParseInput(text string, agents []session.AgentInfo, selected string, newMode bool, newKind string, kinds []string) Parsed {
	t := strings.TrimSpace(text)
	if t == "" {
		if len(agents) == 0 || newMode {
			return Parsed{Action: ActStart, Kind: newKind}
		}
		return Parsed{Action: ActNone}
	}
	head, rest := splitToken(t)
	switch {
	case strings.HasPrefix(head, "/"):
		return Parsed{Action: ActCommand, Cmd: strings.TrimPrefix(head, "/"), Arg: rest}
	case strings.HasPrefix(head, "@"):
		name := strings.TrimPrefix(head, "@")
		if name == "" {
			return Parsed{Action: ActError, Err: "name an agent after @"}
		}
		if a, ok := findRunning(agents, name); ok && !(newMode && hasKind(kinds, name)) {
			if rest == "" {
				return Parsed{Action: ActSelect, AgentID: a.ID}
			}
			return Parsed{Action: ActSend, AgentID: a.ID, Text: rest}
		}
		if hasKind(kinds, name) {
			if rest == "" {
				return Parsed{Action: ActNewKind, Kind: name}
			}
			return Parsed{Action: ActStart, Kind: name, Text: rest}
		}
		return Parsed{Action: ActError, Err: "no agent @" + name}
	}
	if len(agents) == 0 || newMode || selected == "" {
		return Parsed{Action: ActStart, Kind: newKind, Text: t}
	}
	for _, a := range agents {
		if a.ID == selected {
			if a.Status == session.StatusExited {
				return Parsed{Action: ActError, Err: a.Name + " has exited, /new starts another"}
			}
			return Parsed{Action: ActSend, AgentID: a.ID, Text: t}
		}
	}
	return Parsed{Action: ActStart, Kind: newKind, Text: t}
}

// splitToken splits "head rest" at the first whitespace; rest is trimmed.
func splitToken(s string) (head, rest string) {
	i := strings.IndexAny(s, " \t\r\n")
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimSpace(s[i+1:])
}
