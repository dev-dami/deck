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
	"strconv"
	"strings"
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
	RouteAbovePct int    `json:"route_above_pct"`
	Models        Models `json:"models"`
	// Efforts names the reasoning effort each CLI should use; empty means the
	// model's default.
	Efforts Models `json:"efforts"`
	// Peers links this Deck to other Deck instances (docs/peers-design.md).
	// Nil, the default, means peers are off: no listener, no dialing. Keys
	// never live here; they are in ~/.deck/peers/.
	Peers *Peers `json:"peers,omitempty"`
}

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

// DefaultPeerPort is the port a peer listener uses when its address names none.
const DefaultPeerPort = "7722"

// Peer roles: what a remote Deck may ask of this one.
const (
	PeerRoleObserver   = "observer"   // read-only ops (the default)
	PeerRoleContractor = "contractor" // may also propose contracts (later phases)
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
	// Sandbox is reserved for the virtual bash phase; it is parsed but unused.
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
	// AllowInbound lists the ops this peer may run here; empty means the
	// defaults (docs/peers-design.md section 2).
	AllowInbound []string `json:"allow_inbound,omitempty"`
	// MaxScope is the outer limit for contracts from this peer (later phases).
	MaxScope []string `json:"max_scope,omitempty"`
}

// PeerSandbox configures the virtual bash used by contracts (not used yet).
type PeerSandbox struct {
	TTL       string              `json:"ttl,omitempty"`
	ScratchMB int                 `json:"scratch_mb,omitempty"`
	OutputKB  int                 `json:"output_kb,omitempty"`
	Presets   map[string][]string `json:"presets,omitempty"`
}

// PeersEnabled reports whether the peer link is on.
func (c Config) PeersEnabled() bool { return c.Peers != nil && c.Peers.Enabled }

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
		} else if r.Dial {
			return fmt.Errorf("peers.remote[%d] %q: dial needs an addr", i, r.Name)
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
