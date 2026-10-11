// Package hub is the catalog behind Deck's two MCP tools, search and execute.
//
// Every agent Deck runs sees exactly those two tools on the "deck" MCP
// server. Capabilities are ops: search finds them (together with the skills
// any agent has and the thread history), and execute runs one. One table,
// [Ops], drives both, so adding a capability never adds a tool.
//
// Everything here is read-only except thread.send, which the approval broker
// runs itself (as it does thread.read). Git and GitHub go through the user's
// own git and gh, in a clean environment (internal/deck/gh); Deck never reads
// credentials, and every result is passed through [Redact].
package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/gh"
	"github.com/dev-dami/deck/internal/deck/store"
)

// OpSearch is the op name the search tool travels under.
const OpSearch = "search"

// OpSkillsUsed is the one op that reads [Env.Tools]; callers can skip
// gathering the thread's tool calls for every other op.
const OpSkillsUsed = "skills.used"

// Limits.
const (
	// Timeout bounds one op.
	Timeout = 55 * time.Second
	// MaxOutput is the most text one call returns.
	MaxOutput = 20 << 10
	// capSlack is how much text past MaxOutput is kept (and redacted) before
	// the cut: enough that a secret straddling the end of what is kept can
	// never reach the part that is shown.
	capSlack = 64 << 10
)

// Store is the part of the thread store history ops read.
type Store interface {
	List() ([]store.Thread, error)
	LoadEntries(id string) ([]store.Entry, store.Thread, error)
}

// ToolUse is one tool call an agent made in the current thread.
type ToolUse struct {
	Agent string // the agent's name in the thread
	Name  string // the tool's display name ("Skill", "Read", "Bash", "deck")
	Title string
	Input json.RawMessage
}

// Env is what one call runs against; the caller resolves it per call.
type Env struct {
	AgentID  string
	ThreadID string
	// Dir is the thread's working directory; git and gh run there.
	Dir string
	// Roots are the folders that count as this project for history: Dir
	// and, for a thread in its own worktree, the shared tree it came from.
	Roots []string
	Store Store // nil: no history
	// Tools are the current thread's tool calls (skills.used).
	Tools []ToolUse
	// Home overrides the user's home directory (tests).
	Home string
	// Run runs git and gh; nil means gh.Exec (clean environment).
	Run gh.Runner
	// Activity reports the commands Deck's agents are running; nil when
	// unavailable.
	Activity func() string

	// dropped counts output bytes a command wrote past what was kept in
	// memory, so the truncation note still gives the full size.
	dropped int64
}

func (e *Env) runner() gh.Runner {
	if e.Run != nil {
		return e.Run
	}
	return gh.Exec
}

func (e *Env) home() string {
	if e.Home != "" {
		return e.Home
	}
	h, _ := os.UserHomeDir()
	return h
}

// Arg types.
const (
	tString = "string"
	tInt    = "int"
	tBool   = "bool"
)

// Arg is one argument of an op.
type Arg struct {
	Name     string
	Type     string // "string", "int" or "bool"
	Required bool
	Desc     string
}

// Op is one capability execute can run.
type Op struct {
	Name string
	Desc string
	Args []Arg
	// run is nil for the ops the broker runs itself (thread.send, thread.read).
	run func(ctx context.Context, env *Env, a args) (string, error)
}

// Signature is the op's argument list as search shows it: "{to*, message*}".
func (o Op) Signature() string {
	if len(o.Args) == 0 {
		return "{}"
	}
	parts := make([]string, len(o.Args))
	for i, a := range o.Args {
		s := a.Name
		if a.Required {
			s += "*"
		}
		if a.Type != tString {
			s += ":" + a.Type
		}
		parts[i] = s
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// Ops is the catalog, in the order search lists it.
func Ops() []Op { return ops }

var ops []Op

func init() {
	ops = []Op{
		{Name: agent.OpThreadSend, Desc: "Message another agent in this thread (to: an agent name, or \"all\"). The user sees it.",
			Args: []Arg{{Name: "to", Type: tString, Required: true, Desc: "agent name or \"all\""}, {Name: "message", Type: tString, Required: true}}},
		{Name: agent.OpThreadRead, Desc: "Read the current shared thread: what the user and every agent said and did."},
		{Name: "history.threads", Desc: "List this project's Deck threads, past and current: id, title, updated, agents.",
			Args: []Arg{{Name: "limit", Type: tInt}}, run: opHistoryThreads},
		{Name: "history.search", Desc: "Search this project's Deck threads (chat history) for words; returns thread, speaker and snippet.",
			Args: []Arg{{Name: "query", Type: tString, Required: true}, {Name: "limit", Type: tInt}}, run: opHistorySearch},
		{Name: "history.read", Desc: "Read one of this project's Deck threads (by id from history.threads), its last entries.",
			Args: []Arg{{Name: "thread", Type: tString, Required: true}, {Name: "last", Type: tInt}}, run: opHistoryRead},
		{Name: "skills.list", Desc: "List every skill any agent can see (Deck, Claude, Codex, OpenCode, Antigravity; project and user), with its source.",
			run: opSkillsList},
		{Name: "skills.read", Desc: "Read a skill's SKILL.md, or one file inside that skill's folder, so you can follow it whichever agent owns it.",
			Args: []Arg{{Name: "name", Type: tString, Required: true}, {Name: "source", Type: tString, Desc: "e.g. claude-user, when names clash"}, {Name: "file", Type: tString, Desc: "path inside the skill folder"}}, run: opSkillsRead},
		{Name: OpSkillsUsed, Desc: "Skills the agents in this thread have used so far (Skill tool calls, SKILL.md reads), so you can pick them up.",
			run: opSkillsUsed},
		{Name: "git.status", Desc: "git status of the thread's work tree: branch, upstream, changed files.", run: opGitStatus},
		{Name: "git.log", Desc: "Recent git commits (default 20).", Args: []Arg{{Name: "n", Type: tInt}}, run: opGitLog},
		{Name: "git.diff", Desc: "Uncommitted git diff (stat and patch), optionally for one path or the staged changes.",
			Args: []Arg{{Name: "path", Type: tString}, {Name: "staged", Type: tBool}}, run: opGitDiff},
		{Name: "git.show", Desc: "Show one git commit (or ref:path) with its patch.",
			Args: []Arg{{Name: "ref", Type: tString, Required: true}}, run: opGitShow},
		{Name: "git.branches", Desc: "Local git branches, most recent first, with upstream and ahead/behind.", run: opGitBranches},
		{Name: "gh.prs", Desc: "Open GitHub pull requests (via the user's gh), with checks.", run: opGHPRs},
		{Name: "gh.pr", Desc: "One GitHub pull request: state, branches, review and its checks.",
			Args: []Arg{{Name: "number", Type: tInt, Required: true}}, run: opGHPR},
		{Name: "gh.runs", Desc: "Recent GitHub Actions workflow runs (default 10).", Args: []Arg{{Name: "limit", Type: tInt}}, run: opGHRuns},
		{Name: "deck.jobs", Desc: "Commands Deck's agents are running now and shells left running in the background.", run: opDeckJobs},
	}
}

func findOp(name string) (Op, bool) {
	for _, o := range ops {
		if o.Name == name {
			return o, true
		}
	}
	return Op{}, false
}

// args are an op's decoded arguments.
type args map[string]any

func (a args) str(k string) string {
	s, _ := a[k].(string)
	return s
}

func (a args) num(k string, def, lo, hi int) int {
	n, ok := a[k].(int)
	if !ok {
		return def
	}
	return min(max(n, lo), hi)
}

func (a args) flag(k string) bool {
	b, _ := a[k].(bool)
	return b
}

// parseArgs checks raw against the op's spec.
func parseArgs(o Op, raw json.RawMessage) (args, error) {
	out := args{}
	var in map[string]json.RawMessage
	if t := strings.TrimSpace(string(raw)); t != "" && t != "null" {
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, fmt.Errorf("args must be a JSON object")
		}
	}
	known := map[string]Arg{}
	for _, a := range o.Args {
		known[a.Name] = a
	}
	var unknown []string
	for k := range in {
		if _, ok := known[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("unknown argument %q", unknown[0])
	}
	for _, a := range o.Args {
		v, ok := in[a.Name]
		if !ok || string(v) == "null" {
			if a.Required {
				return nil, fmt.Errorf("missing argument %q", a.Name)
			}
			continue
		}
		switch a.Type {
		case tString:
			var s string
			if json.Unmarshal(v, &s) != nil {
				return nil, fmt.Errorf("argument %q must be a string", a.Name)
			}
			if a.Required && strings.TrimSpace(s) == "" {
				return nil, fmt.Errorf("argument %q is empty", a.Name)
			}
			out[a.Name] = s
		case tInt:
			var f float64
			if json.Unmarshal(v, &f) != nil {
				var s string
				if json.Unmarshal(v, &s) != nil {
					return nil, fmt.Errorf("argument %q must be a number", a.Name)
				}
				n, err := strconv.Atoi(strings.TrimSpace(s))
				if err != nil {
					return nil, fmt.Errorf("argument %q must be a number", a.Name)
				}
				f = float64(n)
			}
			out[a.Name] = int(f)
		case tBool:
			var b bool
			if json.Unmarshal(v, &b) != nil {
				var s string
				if json.Unmarshal(v, &s) != nil {
					return nil, fmt.Errorf("argument %q must be true or false", a.Name)
				}
				pb, err := strconv.ParseBool(strings.TrimSpace(s))
				if err != nil {
					return nil, fmt.Errorf("argument %q must be true or false", a.Name)
				}
				b = pb
			}
			out[a.Name] = b
		}
	}
	return out, nil
}

// Execute runs one op. Unknown ops and bad arguments come back as errors
// that point the agent at search.
func Execute(ctx context.Context, env Env, op string, raw json.RawMessage) (text string, isErr bool) {
	op = strings.TrimSpace(op)
	if op == OpSearch {
		return Search(env, raw), false
	}
	o, ok := findOp(op)
	if !ok {
		msg := fmt.Sprintf("Unknown op %q.", op)
		if near := nearestOps(op, 3); len(near) > 0 {
			msg += " Did you mean " + strings.Join(near, ", ") + "?"
		}
		return msg + " Call search (an empty query lists every op).", true
	}
	a, err := parseArgs(o, raw)
	if err != nil {
		return fmt.Sprintf("%s: %v. Its arguments are %s; call search with %q for details.", o.Name, err, o.Signature(), o.Name), true
	}
	if o.run == nil {
		return o.Name + " is answered by Deck's hub; call it through the execute tool.", true
	}
	if _, has := ctx.Deadline(); !has {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, Timeout)
		defer cancel()
	}
	out, err := o.run(ctx, &env, a)
	if err != nil {
		msg := err.Error()
		if ctx.Err() != nil {
			msg = o.Name + " timed out"
		}
		return capOut(strings.TrimSpace(out+"\n"+msg), env.dropped), true
	}
	if strings.TrimSpace(out) == "" {
		out = "(nothing)"
	}
	return capOut(out, env.dropped), false
}

// capOut is Cap(Redact(s), MaxOutput) without redacting text that can never
// be shown: only the first MaxOutput+capSlack bytes are redacted. dropped is
// output that never reached s, counted in the note's total.
func capOut(s string, dropped int64) string {
	if lim := MaxOutput + capSlack; len(s) > lim {
		cut := lim
		for cut > 0 && (s[cut]&0xC0) == 0x80 {
			cut--
		}
		dropped += int64(len(s) - cut)
		s = s[:cut]
	}
	s = Redact(s)
	return capTotal(s, MaxOutput, int64(len(s))+dropped)
}

// Cap trims s to at most n bytes (on a rune boundary) with a note.
func Cap(s string, n int) string { return capTotal(s, n, int64(len(s))) }

// capTotal is Cap for an s that is the head of total bytes of output.
func capTotal(s string, n int, total int64) string {
	if total <= int64(n) && len(s) <= n {
		return s
	}
	cut := min(n, len(s))
	for cut > 0 && cut < len(s) && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + fmt.Sprintf("\n[... output truncated at %d KB of %d KB; narrow the request ...]", n>>10, (total+1023)>>10)
}
