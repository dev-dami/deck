package workspace

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	deckagent "github.com/dev-dami/deck/internal/deck/agent"
)

// What the agents are running right now, for the sidebar's Subagents and
// Commands panes: their tool calls in flight, the subagents among them, and
// the shells Claude Code left running in the background.

// RunningTool is one tool call an agent has started and not finished.
type RunningTool struct {
	AgentID   string // Deck's agent id
	AgentName string // the agent's name in the roster ("claude", "codex-2")
	AgentKind string // "claude", "codex", "opencode", "antigravity"
	ToolID    string
	Name      string // the CLI's own tool name: "Bash", "Task", "run_command"
	Display   string // the name Deck's chat draws it under
	Title     string // the CLI's one-line summary
	// Summary is what the panes show: a subagent's description, a
	// command's command line.
	Summary string
	// Detail is more about it: a subagent's type and prompt, a command's
	// description.
	Detail  string
	Started time.Time
	Input   json.RawMessage
}

// BackgroundShell is a command an agent started in the background (Claude
// Code's Bash with run_in_background) that has not been seen to end.
type BackgroundShell struct {
	AgentID   string
	AgentName string
	AgentKind string
	ShellID   string
	Command   string
	Started   time.Time
	// Output is the tail of what the shell printed, as far as the agent
	// read it.
	Output string
}

// ActivityReport is what a session's agents are running.
type ActivityReport struct {
	Subagents  []RunningTool
	Commands   []RunningTool
	Background []BackgroundShell
	// Tools is every tool call in flight, subagents and commands included.
	Tools []RunningTool
}

// Activity reports what a session's agents are running. The UI
// type-asserts its Workspace to it; it is cheap enough to call per frame.
type Activity interface {
	Activity(sessionID string) ActivityReport
}

var _ Activity = (*DeckWorkspace)(nil)

// Activity implements [Activity].
func (w *DeckWorkspace) Activity(sessionID string) ActivityReport {
	return w.act.report(sessionID)
}

// activityText is what a thread's agents are running, as the deck.jobs op
// shows it to the agents: commands and subagents in flight, and background
// shells with the tail of their output.
func (w *DeckWorkspace) activityText(threadID string) string {
	sid := w.sessionOf(threadID)
	if sid == "" {
		return ""
	}
	r := w.act.report(sid)
	now := time.Now()
	ago := func(t time.Time) string { return now.Sub(t).Round(time.Second).String() }
	var b strings.Builder
	if len(r.Commands)+len(r.Subagents) > 0 {
		b.WriteString("Running now:\n")
		for _, c := range append(append([]RunningTool{}, r.Commands...), r.Subagents...) {
			fmt.Fprintf(&b, "  %s [%s] %s (%s)\n", c.AgentName, c.Name, firstLineOf(c.Summary), ago(c.Started))
		}
	}
	if len(r.Background) > 0 {
		b.WriteString("Background shells:\n")
		for _, s := range r.Background {
			fmt.Fprintf(&b, "  %s [%s] %s (started %s ago)\n", s.AgentName, s.ShellID, firstLineOf(s.Command), ago(s.Started))
			if out := strings.TrimSpace(s.Output); out != "" {
				fmt.Fprintf(&b, "    last output: %s\n", strings.ReplaceAll(tail(out, 400), "\n", "\n    "))
			}
		}
	}
	b.WriteString("(Deck's own git and gh jobs run by the user from the git pane are not listed.)\n")
	return b.String()
}

func firstLineOf(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + " ..."
	}
	return s
}

// observeActivity feeds an agent event to the activity tracker.
func (w *DeckWorkspace) observeActivity(sid string, ev deckagent.Event) {
	switch ev.Kind {
	case deckagent.KindToolCall, deckagent.KindToolResult,
		deckagent.KindTurnDone, deckagent.KindExited, deckagent.KindError:
	default:
		return
	}
	who := agentRef{id: ev.AgentID, name: ev.AgentID, kind: ev.AgentID}
	if a, ok := w.mgr().Agent(ev.AgentID); ok {
		who.name, who.kind = a.Name, a.Kind
	}
	w.act.observe(sid, who, ev, time.Now())
}

// agentRef names the agent behind an event.
type agentRef struct{ id, name, kind string }

// maxShellOutput caps the output kept per background shell.
const maxShellOutput = 4 << 10

// activity tracks running tool calls and background shells by session.
// Its zero value is ready to use.
type activity struct {
	mu      sync.Mutex
	running map[string]map[string]*RunningTool     // session -> tool id
	shells  map[string]map[string]*BackgroundShell // session -> shell id
}

// subagentTools are the tool names that launch a subagent.
var subagentTools = map[string]bool{
	"Task": true, "Agent": true, "task": true, "agent": true,
	"subagent": true, "spawn_agent": true, "SpawnAgent": true,
}

// commandTools are the tool names that run a shell command.
var commandTools = map[string]bool{
	"Bash": true, "bash": true, "run_command": true, "shell": true,
	"command_execution": true, "local_shell": true, "exec_command": true,
}

// killTools stop a background shell.
var killTools = map[string]bool{"KillShell": true, "KillBash": true}

// outputTools read a background shell's output.
var outputTools = map[string]bool{"BashOutput": true, "ShellOutput": true}

func isSubagent(name string) bool { return subagentTools[name] }
func isCommand(name string) bool  { return commandTools[name] }

func (a *activity) observe(sid string, who agentRef, ev deckagent.Event, now time.Time) {
	if sid == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	switch ev.Kind {
	case deckagent.KindToolCall:
		if ev.Tool == nil || ev.Tool.ID == "" {
			return
		}
		a.call(sid, who, ev.Tool, now)
	case deckagent.KindToolResult:
		if ev.Tool == nil {
			return
		}
		a.result(sid, who, ev.Tool, now)
	case deckagent.KindTurnDone, deckagent.KindError:
		a.dropAgent(sid, who.id, false)
	case deckagent.KindExited:
		a.dropAgent(sid, who.id, true)
	}
}

func (a *activity) call(sid string, who agentRef, t *deckagent.Tool, now time.Time) {
	if killTools[t.Name] {
		if id := shellIDOf(t.Input); id != "" {
			a.dropShell(sid, id)
		}
	}
	if a.running == nil {
		a.running = map[string]map[string]*RunningTool{}
	}
	byID := a.running[sid]
	if byID == nil {
		byID = map[string]*RunningTool{}
		a.running[sid] = byID
	}
	display, _ := deckTool(t)
	summary, detail := describe(t)
	if rt, ok := byID[t.ID]; ok { // an update of a call already seen
		rt.Title, rt.Summary, rt.Detail = t.Title, summary, detail
		if len(t.Input) > 0 {
			rt.Input = append(json.RawMessage(nil), t.Input...)
		}
		return
	}
	byID[t.ID] = &RunningTool{
		AgentID: who.id, AgentName: who.name, AgentKind: who.kind,
		ToolID: t.ID, Name: t.Name, Display: display, Title: t.Title,
		Summary: summary, Detail: detail, Started: now,
		Input: append(json.RawMessage(nil), t.Input...),
	}
}

func (a *activity) result(sid string, who agentRef, t *deckagent.Tool, now time.Time) {
	call := a.running[sid][t.ID]
	delete(a.running[sid], t.ID)
	input := t.Input
	started := now
	if call != nil {
		if len(input) == 0 {
			input = call.Input
		}
		started = call.Started
	}
	switch {
	case killTools[t.Name]:
		if id := shellIDOf(input); id != "" {
			a.dropShell(sid, id)
		}
	case outputTools[t.Name]:
		id := shellIDOf(input)
		sh := a.shells[sid][id]
		if sh == nil {
			return
		}
		if shellEnded(t.Output) {
			a.dropShell(sid, id)
			return
		}
		sh.Output = tail(t.Output, maxShellOutput)
	case isCommand(t.Name) && t.Status != deckagent.ToolError && inBackground(input):
		id := backgroundID(t.Output)
		if id == "" {
			id = t.ID
		}
		cmd := commandOf(t, input)
		if a.shells == nil {
			a.shells = map[string]map[string]*BackgroundShell{}
		}
		if a.shells[sid] == nil {
			a.shells[sid] = map[string]*BackgroundShell{}
		}
		a.shells[sid][id] = &BackgroundShell{
			AgentID: who.id, AgentName: who.name, AgentKind: who.kind,
			ShellID: id, Command: cmd, Started: started,
		}
	}
}

// dropAgent forgets an agent's calls in flight, and with shells its
// background shells too (the CLI that ran them is gone).
func (a *activity) dropAgent(sid, agentID string, shells bool) {
	for id, rt := range a.running[sid] {
		if rt.AgentID == agentID {
			delete(a.running[sid], id)
		}
	}
	if !shells {
		return
	}
	for id, sh := range a.shells[sid] {
		if sh.AgentID == agentID {
			delete(a.shells[sid], id)
		}
	}
}

func (a *activity) dropShell(sid, id string) {
	delete(a.shells[sid], id)
}

// report copies out what a session is running, oldest first.
func (a *activity) report(sid string) ActivityReport {
	a.mu.Lock()
	defer a.mu.Unlock()
	var r ActivityReport
	for _, rt := range a.running[sid] {
		c := *rt
		r.Tools = append(r.Tools, c)
		switch {
		case isSubagent(c.Name):
			r.Subagents = append(r.Subagents, c)
		case isCommand(c.Name):
			r.Commands = append(r.Commands, c)
		}
	}
	for _, sh := range a.shells[sid] {
		r.Background = append(r.Background, *sh)
	}
	byStart := func(s []RunningTool) {
		sort.Slice(s, func(i, j int) bool {
			if !s[i].Started.Equal(s[j].Started) {
				return s[i].Started.Before(s[j].Started)
			}
			return s[i].ToolID < s[j].ToolID
		})
	}
	byStart(r.Tools)
	byStart(r.Subagents)
	byStart(r.Commands)
	sort.Slice(r.Background, func(i, j int) bool {
		if !r.Background[i].Started.Equal(r.Background[j].Started) {
			return r.Background[i].Started.Before(r.Background[j].Started)
		}
		return r.Background[i].ShellID < r.Background[j].ShellID
	})
	return r
}

// inputMap decodes a tool's input, nil when it is not a JSON object.
func inputMap(raw json.RawMessage) map[string]any {
	var in map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &in) != nil {
		return nil
	}
	return in
}

func strOf(in map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := in[k].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// describe is the summary and detail the panes show for a call.
func describe(t *deckagent.Tool) (summary, detail string) {
	in := inputMap(t.Input)
	switch {
	case isSubagent(t.Name):
		summary = strOf(in, "description", "title", "name")
		kind := strOf(in, "subagent_type", "subagentType", "agent", "type")
		prompt := strOf(in, "prompt", "message", "task")
		if summary == "" {
			summary = firstLine(prompt, 80)
		}
		if summary == "" {
			summary = t.Title
		}
		if summary == "" {
			summary = "subagent"
		}
		var parts []string
		if kind != "" {
			parts = append(parts, kind)
		}
		if prompt != "" {
			parts = append(parts, prompt)
		}
		return summary, strings.Join(parts, "\n")
	case isCommand(t.Name):
		return commandOf(t, t.Input), strOf(in, "description")
	}
	return t.Title, ""
}

// commandOf is a command call's command line.
func commandOf(t *deckagent.Tool, input json.RawMessage) string {
	in := inputMap(input)
	if cmd := strOf(in, "command", "cmd", "CommandLine", "commandLine"); cmd != "" {
		return cmd
	}
	if arr, ok := in["command"].([]any); ok { // codex: ["bash", "-lc", "…"]
		var parts []string
		for _, p := range arr {
			if s, ok := p.(string); ok {
				parts = append(parts, s)
			}
		}
		if n := len(parts); n >= 3 && (parts[n-2] == "-lc" || parts[n-2] == "-c") {
			return parts[n-1]
		}
		return strings.Join(parts, " ")
	}
	return t.Title
}

// inBackground reports whether a command call asked to run in the
// background.
func inBackground(input json.RawMessage) bool {
	in := inputMap(input)
	for _, k := range []string{"run_in_background", "runInBackground", "background"} {
		if v, ok := in[k].(bool); ok && v {
			return true
		}
	}
	return false
}

// shellIDOf is the background shell a KillShell or BashOutput call names.
func shellIDOf(input json.RawMessage) string {
	return strOf(inputMap(input), "shell_id", "bash_id", "shellId", "bashId", "id")
}

var (
	// "Command running in background with ID: bash_1" and the like.
	bgIDText = regexp.MustCompile(`(?i)\b(?:shell[ _]?id|bash[ _]?id|with id|id)\s*[:=]\s*"?([A-Za-z0-9_.\-]+)`)
	// <status>completed</status>, "status": "killed", Status: failed…
	shellStatus = regexp.MustCompile(`(?i)status\W{0,4}(running|completed|complete|killed|failed|exited|done)`)
)

// backgroundID finds the shell id in a background Bash call's output.
func backgroundID(output string) string {
	if m := bgIDText.FindStringSubmatch(output); m != nil {
		return strings.TrimRight(m[1], ".")
	}
	return ""
}

// shellEnded reports whether a BashOutput result says the shell finished.
func shellEnded(output string) bool {
	m := shellStatus.FindStringSubmatch(output)
	if m == nil {
		return false
	}
	return !strings.EqualFold(m[1], "running")
}

// tail is the last n bytes of s, cut at a line start when it can be.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 && i < len(s)-1 {
		s = s[i+1:]
	}
	return s
}
