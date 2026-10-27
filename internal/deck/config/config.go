// Package config reads and writes Deck's settings file, ~/.deck/config.json.
//
// A missing file is not an error: Load returns Defaults. A file that exists
// may omit any key, and the missing keys keep their defaults.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// EnvReducedMotion, when set to "1", forces Motion to "off".
const EnvReducedMotion = "DECK_REDUCED_MOTION"

// Kinds are the CLIs Deck can drive, in the order they are offered.
var Kinds = []string{"claude", "codex", "opencode", "antigravity"}

// Models names the model each CLI should run. An empty value means the CLI's default.
type Models struct {
	Claude      string `json:"claude"`
	Codex       string `json:"codex"`
	Opencode    string `json:"opencode,omitempty"`
	Antigravity string `json:"antigravity,omitempty"`
}

// For is the value for a kind ("" for an unknown one).
func (m Models) For(kind string) string {
	switch kind {
	case "claude":
		return m.Claude
	case "codex":
		return m.Codex
	case "opencode":
		return m.Opencode
	case "antigravity":
		return m.Antigravity
	}
	return ""
}

// Set sets the value for a kind; an unknown kind is ignored.
func (m *Models) Set(kind, v string) {
	switch kind {
	case "claude":
		m.Claude = v
	case "codex":
		m.Codex = v
	case "opencode":
		m.Opencode = v
	case "antigravity":
		m.Antigravity = v
	}
}

// Config is Deck's user settings.
type Config struct {
	UserName          string `json:"user_name"`          // shown in the UI; defaults to git's user.name
	DefaultAgent      string `json:"default_agent"`      // "claude" or "codex"
	Motion            string `json:"motion"`             // "on" or "off"
	Bell              bool   `json:"bell"`               // ring the terminal bell when a turn needs attention
	TellAgentsName    bool   `json:"tell_agents_name"`   // tell agents the user's name (agent.Spec.TellName)
	ClaudeBinary      string `json:"claude_binary"`      // path or command name; empty means "claude"
	CodexBinary       string `json:"codex_binary"`       // path or command name; empty means "codex"
	OpencodeBinary    string `json:"opencode_binary"`    // path or command name; empty means "opencode"
	AntigravityBinary string `json:"antigravity_binary"` // path or command name; empty means "agy"
	DefaultMode       string `json:"default_mode"`       // "plan", "ask" or "yolo"; empty means "ask". "yolo" is ignored at startup (see StartMode)
	// TrackChanges snapshots a thread's git work tree with every message you
	// send, for the changes panel and its undo.
	TrackChanges bool `json:"track_changes"`
	// DeckAttribution credits Deck on commits and pull requests agents make,
	// and strips every other AI attribution from their commit messages.
	DeckAttribution bool `json:"deck_attribution"`
	// DeckAuthor makes Deck the author of the commits agents make (the user
	// stays the committer), so Deck counts among a repository's contributors.
	// It applies while DeckAttribution is on.
	DeckAuthor bool `json:"deck_author"`
	// AutoTitle has the default agent's model write a short title for a thread
	// once its first turn ends; the first prompt stands in until then.
	AutoTitle bool `json:"auto_title"`
	// MaxParallel is how many tasks the scheduler runs at once in a thread with
	// orchestration on; 0 means DefaultMaxParallel.
	MaxParallel int `json:"max_parallel"`
	// MaxDepth is how deep subagent trees go: 1 lets only the user's agents
	// start subagents, 2 lets those start their own, and so on; 0 means
	// DefaultMaxDepth.
	MaxDepth int `json:"max_depth"`
	// MaxChildren is how many live subagents one member may have; 0 means
	// DefaultMaxChildren.
	MaxChildren int `json:"max_children"`
	// MaxSubagents is how many subagents may be live in one thread at once,
	// whoever started them; 0 means DefaultMaxSubagents.
	MaxSubagents int `json:"max_subagents"`
	// KeepOrphans leaves a member's subagents running when it stops; by
	// default they are stopped with it and their tasks cancelled.
	KeepOrphans bool `json:"keep_orphans"`
	// MaxWork is how many subagents agents may start, and tasks the scheduler
	// may hand out, after each message of the user before the thread pauses
	// for them; 0 means DefaultMaxWork.
	MaxWork int `json:"max_work"`
	// Budget limits what a task run by subagents may spend (the task's own
	// budget, when it has one, wins field by field); zero fields are no limit.
	Budget Budget `json:"budget"`
	// RouteAbovePct moves a task that names no agent kind off the default
	// agent when one of that CLI's plan windows is at least this full, to the
	// installed kind with the most room; 0 means DefaultRouteAbovePct, and 100
	// or more never moves it.
	RouteAbovePct int `json:"route_above_pct"`
	// Review is the kind of agent that reviews a task run by subagents before
	// it is done, when the task names none: "" or "none" is no review,
	// "other" any installed kind other than the worker's, or a kind.
	Review string `json:"review"`
	Models Models `json:"models"`
	// Efforts names the reasoning effort each CLI should use; empty means the
	// model's default.
	Efforts Models `json:"efforts"`
	// Context shapes how much of a thread Deck puts in front of an agent
	// (docs/context-design.md). The zero value is Deck's defaults.
	Context Context `json:"context,omitzero"`
	// EditGate coordinates agents that edit the same files of a shared work
	// tree (session/coord.go). On by default.
	EditGate EditGate `json:"edit_gate,omitzero"`
	// Peers links this Deck to other Deck instances (docs/peers-design.md).
	// Nil, the default, means peers are off: no listener, no dialing. Keys
	// never live here; they are in ~/.deck/peers/.
	Peers *Peers `json:"peers,omitempty"`
}

// EditGate: before an agent edits a file another agent of the thread is
// changing or holds, Deck shows it that agent's changes and asks that agent
// first; the edit waits for the answer, at most TimeoutSec.
type EditGate struct {
	// Off turns the gate off: agents edit shared files without asking.
	Off bool `json:"off,omitempty"`
	// TimeoutSec is how long an edit waits for the owner's answer; 0 means
	// DefaultEditGateSec.
	TimeoutSec int `json:"timeout_sec,omitempty"`
}

// DefaultEditGateSec is how long an edit waits for its file's owner.
const DefaultEditGateSec = 120

// Defaults for the orchestration limits left at 0.
const (
	DefaultMaxParallel   = 3  // tasks the scheduler runs at once per thread
	DefaultMaxDepth      = 3  // levels of subagents below the user's agents
	DefaultMaxChildren   = 4  // live subagents per member
	DefaultMaxSubagents  = 12 // live subagents per thread
	DefaultMaxWork       = 24 // subagent starts and dispatches per user message
	DefaultRouteAbovePct = 90
)

// Budget is what a task may spend: tokens (input and output), the cost the
// CLIs report, and minutes since it started. 0 is no limit.
type Budget struct {
	Tokens  int     `json:"tokens,omitempty"`
	CostUSD float64 `json:"cost_usd,omitempty"`
	Minutes int     `json:"minutes,omitempty"`
}

// Context is the context section of the config: what each agent's CLI
// loads into its context besides Deck's own, and how much of the thread
// Deck puts there. Budgets are shares of the agent's context window (its
// model's, as the CLI reports it). A CLI that cannot do what a knob asks
// ignores it, and Deck says so once when it starts.
type Context struct {
	// IsolateGlobalConfig leaves out the user's global CLI config (Claude:
	// ~/.claude settings with their hooks and permissions, and
	// ~/.claude/CLAUDE.md). Off by default.
	IsolateGlobalConfig bool `json:"isolate_global_config,omitempty"`
	// InstructionFiles is which instruction files (CLAUDE.md, AGENTS.md)
	// load: "inherit" (the default, also ""), "project-only" or "none".
	InstructionFiles string `json:"instruction_files,omitempty"`
	// MCP is "deck-only" (the default, also "": only Deck's MCP server) or
	// "inherit" (the user's own servers as well).
	MCP string `json:"mcp,omitempty"`
	// CompactAtPct is how full the context may get before the CLI compacts
	// it, in percent of the window; 0 is the CLI's default.
	CompactAtPct int `json:"compact_at_pct,omitempty"`
	// PerAgent overrides the four knobs above for one kind of agent.
	PerAgent map[string]ContextOverride `json:"per_agent,omitempty"`
	// BriefingPct is how much of the window the briefing of what an agent
	// missed may take (0 means the default: 4%, and never less than Deck's
	// fixed budget for windows of 128k tokens and more). Clamped to 16k-160k
	// characters.
	BriefingPct float64 `json:"briefing_pct,omitempty"`
	// ToolOutputPct is how much of the window one search or execute result
	// may take (0 means the default: 1.5%, and never less than 20 KB for
	// windows of 128k tokens and more). Clamped to 8-64 KB.
	ToolOutputPct float64 `json:"tool_output_pct,omitempty"`
}

// ContextOverride is one kind of agent's context knobs; an unset one keeps
// the section's own.
type ContextOverride struct {
	IsolateGlobalConfig *bool  `json:"isolate_global_config,omitempty"`
	InstructionFiles    string `json:"instruction_files,omitempty"`
	MCP                 string `json:"mcp,omitempty"`
	CompactAtPct        *int   `json:"compact_at_pct,omitempty"`
}

// ContextPolicy is the context knobs for one kind of agent, overrides
// applied. Strings keep "" for a knob nobody set.
type ContextPolicy struct {
	IsolateGlobalConfig bool
	InstructionFiles    string
	MCP                 string
	CompactAtPct        int
}

// For is the policy of kind: the section's knobs with kind's overrides.
func (c Context) For(kind string) ContextPolicy {
	p := ContextPolicy{IsolateGlobalConfig: c.IsolateGlobalConfig, InstructionFiles: c.InstructionFiles, MCP: c.MCP, CompactAtPct: c.CompactAtPct}
	o, ok := c.PerAgent[kind]
	if !ok {
		return p
	}
	if o.IsolateGlobalConfig != nil {
		p.IsolateGlobalConfig = *o.IsolateGlobalConfig
	}
	if o.InstructionFiles != "" {
		p.InstructionFiles = o.InstructionFiles
	}
	if o.MCP != "" {
		p.MCP = o.MCP
	}
	if o.CompactAtPct != nil {
		p.CompactAtPct = *o.CompactAtPct
	}
	return p
}

// Limits of the context percentages.
const (
	MaxBriefingPct   = 25
	MaxToolOutputPct = 10
)

// validateKnobs reports the first invalid policy knob; at prefixes its keys.
func validateKnobs(at, files, mcp string, compact int) error {
	switch files {
	case "", "inherit", "project-only", "none":
	default:
		return fmt.Errorf("%sinstruction_files must be \"inherit\", \"project-only\" or \"none\", got %q", at, files)
	}
	switch mcp {
	case "", "deck-only", "inherit":
	default:
		return fmt.Errorf("%smcp must be \"deck-only\" or \"inherit\", got %q", at, mcp)
	}
	if compact < 0 || compact > 100 {
		return fmt.Errorf("%scompact_at_pct must be 0 (the CLI's default) to 100, got %d", at, compact)
	}
	return nil
}

// validate reports the first invalid value in the context section.
func (c Context) validate() error {
	if err := validateKnobs("context.", c.InstructionFiles, c.MCP, c.CompactAtPct); err != nil {
		return err
	}
	for kind, o := range c.PerAgent {
		if !slices.Contains(Kinds, kind) {
			return fmt.Errorf("context.per_agent: %q is not an agent kind (use %s)", kind, strings.Join(Kinds, ", "))
		}
		compact := 0
		if o.CompactAtPct != nil {
			compact = *o.CompactAtPct
		}
		if err := validateKnobs("context.per_agent."+kind+".", o.InstructionFiles, o.MCP, compact); err != nil {
			return err
		}
	}
	if c.BriefingPct < 0 || c.BriefingPct > MaxBriefingPct {
		return fmt.Errorf("context.briefing_pct must be 0 (the default) to %d, got %v", MaxBriefingPct, c.BriefingPct)
	}
	if c.ToolOutputPct < 0 || c.ToolOutputPct > MaxToolOutputPct {
		return fmt.Errorf("context.tool_output_pct must be 0 (the default) to %d, got %v", MaxToolOutputPct, c.ToolOutputPct)
	}
	return nil
}

// DefaultPeerPort is the port a peer listener uses when its address names none.
const DefaultPeerPort = "7722"

// Peer roles: what a remote Deck may ask of this one.
const (
	PeerRoleObserver   = "observer"   // read-only ops (the default)
	PeerRoleContractor = "contractor" // may also propose code contracts
)

// Peers is the peers section of the config.
type Peers struct {
	// Enabled turns the peer link on: the listener (when Listen.Enabled) and a
	// supervisor for every remote with Dial set. Off by default.
	Enabled bool `json:"enabled"`
	// Name is how this Deck introduces itself to peers; empty means the host name.
	Name   string       `json:"name,omitempty"`
	Listen PeerListen   `json:"listen"`
	Remote []PeerRemote `json:"remote,omitempty"`
	// ShareLimits lets teammates see how full this Deck's CLI plans are, in
	// peer.status. Off by default.
	ShareLimits bool `json:"share_limits,omitempty"`
	// Relay lets this Deck pass links between two peers pinned here, for
	// Decks that cannot reach each other. The two ends run SSH inside the
	// relayed channel, so this Deck sees only ciphertext. Off by default.
	Relay bool `json:"relay,omitempty"`
	// Sandbox sets the limits of the sandbox that ops contracts run in
	// (SandboxLimits).
	Sandbox *PeerSandbox `json:"sandbox,omitempty"`
}

// PeerListen is the embedded SSH listener. It is off by default.
type PeerListen struct {
	Enabled bool   `json:"enabled"`
	Addr    string `json:"addr,omitempty"` // host:port; empty means ":7722"
}

// PeerRemote is one paired Deck.
type PeerRemote struct {
	Name string `json:"name"`
	Addr string `json:"addr,omitempty"` // host:port to dial; empty for inbound-only peers
	Dial bool   `json:"dial"`           // keep a connection to it open
	Role string `json:"role,omitempty"` // "observer" (default) or "contractor"
	// Via names another remote that relays the link to this one: the dial
	// goes through it when Addr is empty or a direct dial fails.
	Via string `json:"via,omitempty"`
	// Relay lets this peer bring links from other peers pinned here (it
	// relays for them). Off by default.
	Relay bool `json:"relay,omitempty"`
	// AllowInbound lists the ops this peer may run here; empty means the
	// defaults (docs/peers-design.md section 2).
	AllowInbound []string `json:"allow_inbound,omitempty"`
	// MaxScope is the outer limit for ops contracts from this peer: the
	// presets (OpsPresets) it may ask for. Empty allows none.
	MaxScope []string `json:"max_scope,omitempty"`
}

// OpsPresets are the sandbox presets an ops contract can ask for: each
// lets the contract's agent run a fixed set of read-only commands.
var OpsPresets = []string{"podman-read", "systemd-read"}

// PeerSandbox sets the limits of the sandbox that ops contracts run in.
// Presets is reserved: it is parsed but not used.
type PeerSandbox struct {
	TTL       string              `json:"ttl,omitempty"`
	ScratchMB int                 `json:"scratch_mb,omitempty"`
	OutputKB  int                 `json:"output_kb,omitempty"`
	Presets   map[string][]string `json:"presets,omitempty"`
}

// The sandbox limits when peers.sandbox leaves them out.
const (
	DefaultSandboxTTL       = 2 * time.Hour
	DefaultSandboxScratchMB = 64
	DefaultSandboxOutputKB  = 64
)

// SandboxLimits are how long an ops contract may run, and the sizes of its
// scratch folder (MB) and of each command's output (KB), with the defaults
// for what peers.sandbox leaves out. p may be nil.
func (p *Peers) SandboxLimits() (ttl time.Duration, scratchMB, outputKB int) {
	ttl, scratchMB, outputKB = DefaultSandboxTTL, DefaultSandboxScratchMB, DefaultSandboxOutputKB
	if p == nil || p.Sandbox == nil {
		return
	}
	if d, err := time.ParseDuration(p.Sandbox.TTL); err == nil && d > 0 {
		ttl = d
	}
	if p.Sandbox.ScratchMB > 0 {
		scratchMB = p.Sandbox.ScratchMB
	}
	if p.Sandbox.OutputKB > 0 {
		outputKB = p.Sandbox.OutputKB
	}
	return
}

// PeersEnabled reports whether the peer link is on.
func (c Config) PeersEnabled() bool { return c.Peers != nil && c.Peers.Enabled }

// Teammates are the names of the paired Decks (nil without peers): no
// profile may take one.
func (c Config) Teammates() []string {
	if c.Peers == nil {
		return nil
	}
	out := make([]string, 0, len(c.Peers.Remote))
	for _, r := range c.Peers.Remote {
		out = append(out, r.Name)
	}
	return out
}

// ListenAddr is the listener's address, with the default port filled in.
func (l PeerListen) ListenAddr() string {
	if l.Addr == "" {
		return ":" + DefaultPeerPort
	}
	return l.Addr
}

// AllInterfaces reports whether the listener binds every interface (no host,
// 0.0.0.0 or ::) rather than one address such as a WireGuard IP.
func (l PeerListen) AllInterfaces() bool {
	host, _, err := net.SplitHostPort(l.ListenAddr())
	if err != nil {
		return false
	}
	if host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

// RoleOrDefault is the remote's role, "observer" when unset.
func (r PeerRemote) RoleOrDefault() string {
	if r.Role == "" {
		return PeerRoleObserver
	}
	return r.Role
}

// Find returns the configured remote with the given name.
func (p *Peers) Find(name string) (PeerRemote, bool) {
	if p == nil {
		return PeerRemote{}, false
	}
	for _, r := range p.Remote {
		if r.Name == name {
			return r, true
		}
	}
	return PeerRemote{}, false
}

// ValidPeerName reports whether s can name a peer: 1-32 of a-z, 0-9, '-', '_'
// and '.', starting with a letter or digit. Names show in the console and in
// file names, so they stay plain.
func ValidPeerName(s string) bool {
	if len(s) == 0 || len(s) > 32 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case i > 0 && (r == '-' || r == '_' || r == '.'):
		default:
			return false
		}
	}
	return true
}

// ValidPeerAddr reports whether s is a host:port with a port in 1-65535.
func ValidPeerAddr(s string) error {
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return err
	}
	if strings.ContainsAny(host, " \t/") {
		return fmt.Errorf("bad host %q", host)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("bad port %q", port)
	}
	return nil
}

// validate reports the first invalid value in the peers section.
func (p *Peers) validate() error {
	if p.Name != "" && !ValidPeerName(p.Name) {
		return fmt.Errorf("peers.name %q: use 1-32 of a-z, 0-9, '-', '_', '.'", p.Name)
	}
	if p.Listen.Addr != "" {
		if err := ValidPeerAddr(p.Listen.Addr); err != nil {
			return fmt.Errorf("peers.listen.addr %q: %v", p.Listen.Addr, err)
		}
	}
	seen := map[string]bool{}
	for i, r := range p.Remote {
		if !ValidPeerName(r.Name) {
			return fmt.Errorf("peers.remote[%d].name %q: use 1-32 of a-z, 0-9, '-', '_', '.'", i, r.Name)
		}
		if seen[r.Name] {
			return fmt.Errorf("peers.remote: duplicate name %q", r.Name)
		}
		seen[r.Name] = true
		if p.Name != "" && r.Name == p.Name {
			return fmt.Errorf("peers.remote[%d].name %q is this Deck's own name", i, r.Name)
		}
		if r.Addr != "" {
			if err := ValidPeerAddr(r.Addr); err != nil {
				return fmt.Errorf("peers.remote[%d].addr %q: %v", i, r.Addr, err)
			}
		} else if r.Dial && r.Via == "" {
			return fmt.Errorf("peers.remote[%d] %q: dial needs an addr or a via relay", i, r.Name)
		}
		if r.Via != "" && (!ValidPeerName(r.Via) || r.Via == r.Name) {
			return fmt.Errorf("peers.remote[%d].via %q: name another remote", i, r.Via)
		}
		switch r.Role {
		case "", PeerRoleObserver, PeerRoleContractor:
		default:
			return fmt.Errorf("peers.remote[%d].role must be \"observer\" or \"contractor\", got %q", i, r.Role)
		}
		// Ops are matched exactly, and execute trims the op it is sent
		// (agent.ParseExecInput), so an entry with spaces around it could
		// never match: refuse it instead of silently allowing nothing.
		for j, op := range r.AllowInbound {
			if strings.TrimSpace(op) == "" {
				return fmt.Errorf("peers.remote[%d].allow_inbound[%d] is empty", i, j)
			}
			if op != strings.TrimSpace(op) {
				return fmt.Errorf("peers.remote[%d].allow_inbound[%d] %q has spaces around it", i, j, op)
			}
		}
		for j, s := range r.MaxScope {
			if strings.TrimSpace(s) == "" {
				return fmt.Errorf("peers.remote[%d].max_scope[%d] is empty", i, j)
			}
			if s != strings.TrimSpace(s) {
				return fmt.Errorf("peers.remote[%d].max_scope[%d] %q has spaces around it", i, j, s)
			}
			if !slices.Contains(OpsPresets, s) {
				return fmt.Errorf("peers.remote[%d].max_scope[%d] %q is not a preset (use %s)", i, j, s, strings.Join(OpsPresets, ", "))
			}
		}
	}
	if sb := p.Sandbox; sb != nil {
		if d, err := time.ParseDuration(sb.TTL); sb.TTL != "" && (err != nil || d <= 0) {
			return fmt.Errorf("peers.sandbox.ttl %q: use a duration such as \"2h\"", sb.TTL)
		}
		if sb.ScratchMB < 0 || sb.OutputKB < 0 {
			return errors.New("peers.sandbox: scratch_mb and output_kb cannot be negative")
		}
	}
	for i, r := range p.Remote {
		if r.Via != "" && !seen[r.Via] {
			return fmt.Errorf("peers.remote[%d].via %q is not in peers.remote", i, r.Via)
		}
	}
	return nil
}

// gitUserName and osUserName are indirections so tests can stub them.
var (
	gitUserName = func() string {
		out, err := exec.Command("git", "config", "--global", "user.name").Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	osUserName = func() string {
		u, err := user.Current()
		if err != nil {
			return ""
		}
		name := u.Username
		// Windows reports DOMAIN\name; keep only the name.
		if i := strings.LastIndexAny(name, `\/`); i >= 0 {
			name = name[i+1:]
		}
		return name
	}
)

// Defaults returns the settings used when no file exists. UserName comes from
// `git config --global user.name`, falling back to the OS user name.
func Defaults() Config {
	name := gitUserName()
	if name == "" {
		name = osUserName()
	}
	return Config{
		UserName:          name,
		DefaultAgent:      "claude",
		Motion:            "on",
		Bell:              true,
		TellAgentsName:    false,
		ClaudeBinary:      "claude",
		CodexBinary:       "codex",
		OpencodeBinary:    "opencode",
		AntigravityBinary: "agy",
		DefaultMode:       "ask",
		TrackChanges:      true,
		DeckAttribution:   true,
		DeckAuthor:        true,
		AutoTitle:         true,
	}
}

// Dir returns ~/.deck.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".deck"), nil
}

// Path returns ~/.deck/config.json.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Load reads the config from Path.
func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}
	return LoadFrom(path)
}

// LoadFrom reads the config at path. A missing file yields Defaults. Keys
// absent from the file keep their defaults. The result is validated, and
// DECK_REDUCED_MOTION=1 turns Motion off. The environment override is not
// written back by Save unless the caller saves the returned value.
func LoadFrom(path string) (Config, error) {
	cfg := Defaults()
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// keep defaults
	case err != nil:
		return Config{}, fmt.Errorf("config: read %s: %w", path, err)
	default:
		if err := json.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("config: parse %s: %w", path, err)
		}
	}
	if os.Getenv(EnvReducedMotion) == "1" {
		cfg.Motion = "off"
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("config: %s: %w", path, err)
	}
	return cfg, nil
}

// Save validates c and writes it to Path.
func Save(c Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	return SaveTo(path, c)
}

// SaveTo validates c and writes it to path, creating the parent directory.
func SaveTo(path string, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(data, '\n'))
}

// LoadPeers reads only the peers section of the config at path and validates
// it, without the rest of LoadFrom's work (no defaults, no git, no
// environment): the peer manager re-reads it while Deck runs. A missing file
// or section is nil.
func LoadPeers(path string) (*Peers, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var f struct {
		Peers *Peers `json:"peers"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	if f.Peers != nil {
		if err := f.Peers.validate(); err != nil {
			return nil, fmt.Errorf("config: %s: %w", path, err)
		}
	}
	return f.Peers, nil
}

// UpdatePeers edits only the peers section of the config file at path: fn
// changes it in place and reports whether it did. Every other key is written
// back as it was, unknown keys included, and nothing that came from the
// environment (DECK_REDUCED_MOTION) is persisted, unlike a LoadFrom/SaveTo
// round trip. A missing file is created holding just the peers section.
// changed reports whether the file was written.
func UpdatePeers(path string, fn func(p *Peers) bool) (changed bool, err error) {
	raw := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return false, fmt.Errorf("config: read %s: %w", path, err)
	default:
		if err := json.Unmarshal(data, &raw); err != nil {
			return false, fmt.Errorf("config: parse %s: %w", path, err)
		}
	}
	p := &Peers{}
	if v, ok := raw["peers"]; ok && string(v) != "null" {
		if err := json.Unmarshal(v, p); err != nil {
			return false, fmt.Errorf("config: parse %s: peers: %w", path, err)
		}
	}
	if !fn(p) {
		return false, nil
	}
	if err := p.validate(); err != nil {
		return false, err
	}
	enc, err := json.Marshal(p)
	if err != nil {
		return false, err
	}
	raw["peers"] = enc
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return false, err
	}
	return true, writeFileAtomic(path, append(out, '\n'))
}

// writeFileAtomic writes data to a temp file next to path and renames it over
// path, creating the parent directory.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// Validate reports the first invalid value.
func (c Config) Validate() error {
	switch c.DefaultAgent {
	case "claude", "codex", "opencode", "antigravity":
	default:
		return fmt.Errorf("default_agent must be \"claude\", \"codex\", \"opencode\" or \"antigravity\", got %q", c.DefaultAgent)
	}
	switch c.Motion {
	case "on", "off":
	default:
		return fmt.Errorf("motion must be \"on\" or \"off\", got %q", c.Motion)
	}
	switch c.DefaultMode {
	case "", "plan", "ask", "yolo":
	default:
		return fmt.Errorf("default_mode must be \"plan\", \"ask\" or \"yolo\", got %q", c.DefaultMode)
	}
	for _, l := range []struct {
		key string
		n   int
	}{{"max_parallel", c.MaxParallel}, {"max_depth", c.MaxDepth}, {"max_children", c.MaxChildren}, {"max_subagents", c.MaxSubagents},
		{"max_work", c.MaxWork}, {"route_above_pct", c.RouteAbovePct}, {"budget.tokens", c.Budget.Tokens}, {"budget.minutes", c.Budget.Minutes}} {
		if l.n < 0 {
			return fmt.Errorf("%s must be 0 (the default) or more, got %d", l.key, l.n)
		}
	}
	if c.Budget.CostUSD < 0 {
		return fmt.Errorf("budget.cost_usd must be 0 (no limit) or more, got %v", c.Budget.CostUSD)
	}
	if c.Review != "" && c.Review != "none" && c.Review != "other" && !slices.Contains(Kinds, c.Review) {
		return fmt.Errorf("review must be \"none\", \"other\" or an agent kind, got %q", c.Review)
	}
	if err := c.Context.validate(); err != nil {
		return err
	}
	if c.Peers != nil {
		if err := c.Peers.validate(); err != nil {
			return err
		}
	}
	return nil
}

// StartMode is the permission mode new threads start in: DefaultMode, or "ask"
// when it is empty. "yolo" is never a starting mode - turning everything on
// without asking must be a deliberate act during the session, not a setting that
// applies silently - so a config that says yolo starts threads in "ask" and
// reports ignored == true (the UI shows a notice).
func (c Config) StartMode() (mode string, ignored bool) {
	switch c.DefaultMode {
	case "plan":
		return "plan", false
	case "yolo":
		return "ask", true
	}
	return "ask", false
}

// Binary is the configured command for a kind's CLI (its own name when unset;
// "agy" for antigravity, whose kind and binary names differ).
func (c Config) Binary(kind string) string {
	b := ""
	def := kind
	switch kind {
	case "claude":
		b = c.ClaudeBinary
	case "codex":
		b = c.CodexBinary
	case "opencode":
		b = c.OpencodeBinary
	case "antigravity":
		b = c.AntigravityBinary
		def = "agy"
	}
	if b == "" {
		return def
	}
	return b
}
