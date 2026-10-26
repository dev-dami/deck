// Package profile is Deck's named agents. A profile is a recipe for an
// agent: the CLI it runs (its kind), a model and effort, a one-line role,
// standing instructions, an optional voice and the paths it keeps to. It
// is addressed by name, "@charles", like a person
// (docs/design/agents-and-mentions.md).
//
// Profiles live in the "profiles" list of deck.json, globally and in the
// project; internal/config reads and writes them. This package holds the
// type and the rules a profile must meet.
package profile

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"
)

// Scopes: where a profile is stored.
const (
	ScopeGlobal  = "global"  // the user's deck.json
	ScopeProject = "project" // the project's deck.json
)

// MaxNameRunes is the longest name a profile may have.
const MaxNameRunes = 32

// Reserved are the names no member or profile may take: "all" addresses
// everyone, and "user" and "deck" speak in every thread.
var Reserved = []string{"all", "user", "deck"}

// Profile is one named agent.
type Profile struct {
	// Name is how it is addressed and shown ("charles").
	Name string `json:"name"`
	// Kind is the CLI it runs: "claude", "codex", "opencode" or "antigravity".
	Kind string `json:"kind"`
	// Model and Effort default to the kind's when empty.
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	// Role is one line ("reviewer"), shown in the roster and briefed.
	Role string `json:"role,omitempty"`
	// Instructions are standing instructions, appended to the briefing.
	Instructions string `json:"instructions,omitempty"`
	// Voice is one line of personality, briefed after the instructions.
	Voice string `json:"voice,omitempty"`
	// Paths are globs the agent should keep to.
	Paths []string `json:"paths,omitempty"`
	// Color is a CSS colour for its hue; empty means the kind's.
	Color string `json:"color,omitempty"`
	// Scope is where it was read from (ScopeGlobal or ScopeProject). It is
	// not stored: the file it is in says it.
	Scope string `json:"-"`
}

// badNameChars are the characters a name may not hold: whitespace, the
// sigils of agents ("@", "$") and context ("#"), "/" (paths) and the kind
// suffixes "*" and "+".
const badNameChars = " \t\r\n@$#/*+"

// CheckName applies the rules every member name meets (Manager.Rename):
// non-empty, and no whitespace, "@", "$", "#", "/", "*" or "+".
func CheckName(name string) error {
	if name == "" || strings.ContainsAny(name, badNameChars) {
		return fmt.Errorf("invalid name %q: names cannot be empty or hold spaces or any of @ $ # / * +", name)
	}
	return nil
}

// IsReserved reports whether name is one of Reserved.
func IsReserved(name string) bool {
	for _, r := range Reserved {
		if strings.EqualFold(name, r) {
			return true
		}
	}
	return false
}

// Validate checks p against the rules of a profile: a name that passes
// CheckName, that a mention can reach (letters, digits, "-" and "_"), that
// is not reserved, an agent kind or a teammate's name; a kind among kinds;
// a one-line role and voice; paths that are valid globs; and a plain
// colour. Names compare case-insensitively. Its errors match ErrInvalid
// (errors.Is).
func Validate(p Profile, kinds, teammates []string) error {
	if err := validate(p, kinds, teammates); err != nil {
		return invalid{err}
	}
	return nil
}

// ErrInvalid matches the errors of Validate: a profile that breaks a rule.
var ErrInvalid = errors.New("invalid profile")

// invalid is an error of Validate: its own message, matching ErrInvalid.
type invalid struct{ error }

func (e invalid) Unwrap() error      { return e.error }
func (invalid) Is(target error) bool { return target == ErrInvalid }

func validate(p Profile, kinds, teammates []string) error {
	if err := CheckName(p.Name); err != nil {
		return err
	}
	if utf8.RuneCountInString(p.Name) > MaxNameRunes {
		return fmt.Errorf("name %q is longer than %d characters", p.Name, MaxNameRunes)
	}
	for i := 0; i < len(p.Name); i++ {
		if !nameByte(p.Name[i]) {
			return fmt.Errorf("name %q can only hold letters, digits, - and _, so that @%s reaches it", p.Name, p.Name)
		}
	}
	if IsReserved(p.Name) {
		return fmt.Errorf("name %q is reserved", p.Name)
	}
	for _, k := range kinds {
		if strings.EqualFold(p.Name, k) {
			return fmt.Errorf("name %q is an agent kind", p.Name)
		}
	}
	for _, m := range teammates {
		if strings.EqualFold(p.Name, m) {
			return fmt.Errorf("name %q is a teammate's", p.Name)
		}
	}
	if p.Kind == "" {
		return fmt.Errorf("profile %q needs a kind (%s)", p.Name, strings.Join(kinds, ", "))
	}
	known := false
	for _, k := range kinds {
		known = known || k == p.Kind
	}
	if !known {
		return fmt.Errorf("profile %q: no agent kind %q (kinds: %s)", p.Name, p.Kind, strings.Join(kinds, ", "))
	}
	if strings.ContainsAny(p.Role, "\r\n") {
		return fmt.Errorf("profile %q: the role is one line", p.Name)
	}
	if strings.ContainsAny(p.Voice, "\r\n") {
		return fmt.Errorf("profile %q: the voice is one line", p.Name)
	}
	for _, g := range p.Paths {
		if strings.TrimSpace(g) == "" {
			return fmt.Errorf("profile %q: an empty path", p.Name)
		}
		if _, err := path.Match(g, ""); err != nil {
			return fmt.Errorf("profile %q: path %q is not a valid glob", p.Name, g)
		}
	}
	if p.Color != "" && !plainColor(p.Color) {
		return fmt.Errorf("profile %q: colour %q is not a CSS colour", p.Name, p.Color)
	}
	switch p.Scope {
	case "", ScopeGlobal, ScopeProject:
	default:
		return fmt.Errorf("profile %q: no scope %q (global or project)", p.Name, p.Scope)
	}
	return nil
}

// ErrNotFound is returned for a profile that is not there.
var ErrNotFound = errors.New("no such profile")

// Find returns the profile called name (case-insensitively).
func Find(ps []Profile, name string) (Profile, bool) {
	for _, p := range ps {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return Profile{}, false
}

// Names lists the profiles' names.
func Names(ps []Profile) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}

// Merge lays over onto base: a profile of over replaces the one of base with
// the same name (case-insensitively) in its place, and the others follow.
func Merge(base, over []Profile) []Profile {
	out := append([]Profile(nil), base...)
	for _, p := range over {
		replaced := false
		for i := range out {
			if strings.EqualFold(out[i].Name, p.Name) {
				out[i], replaced = p, true
				break
			}
		}
		if !replaced {
			out = append(out, p)
		}
	}
	return out
}

// Without drops the profiles whose names are in names (teammates, say).
func Without(ps []Profile, names []string) []Profile {
	var out []Profile
	for _, p := range ps {
		drop := false
		for _, n := range names {
			drop = drop || strings.EqualFold(p.Name, n)
		}
		if !drop {
			out = append(out, p)
		}
	}
	return out
}

// Briefing is what a member started from p is told about itself, after
// Deck's own briefing: its role, standing instructions, voice and paths.
// "" when p says nothing of them.
func (p Profile) Briefing() string {
	var b strings.Builder
	if r := oneLine(p.Role); r != "" {
		fmt.Fprintf(&b, "Your role: %s.\n", strings.TrimSuffix(r, "."))
	}
	if in := strings.TrimSpace(p.Instructions); in != "" {
		b.WriteString("Standing instructions:\n" + in + "\n")
	}
	if v := oneLine(p.Voice); v != "" {
		fmt.Fprintf(&b, "Voice: %s.\n", strings.TrimSuffix(v, "."))
	}
	if len(p.Paths) > 0 {
		fmt.Fprintf(&b, "Keep your changes to these paths: %s.\n", strings.Join(p.Paths, ", "))
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func nameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

// plainColor accepts what a CSS colour is written with ("#a3e", "teal",
// "oklch(70% 0.1 200)") and nothing that could end a declaration.
func plainColor(s string) bool {
	if len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(nameByte(c) || c == '#' || c == '(' || c == ')' || c == '%' || c == '.' || c == ',' || c == ' ' || c == '/') {
			return false
		}
	}
	return true
}
