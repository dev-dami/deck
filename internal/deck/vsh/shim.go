package vsh

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// neverGrantable are base names no shim may have: package runners and
// fetchers, shells and interpreters, privilege tools, and runners that start
// another program.
var neverGrantable = map[string]bool{
	"bunx": true, "bun": true, "npx": true, "npm": true, "pnpm": true, "yarn": true, "uvx": true, "uv": true,
	"pipx": true, "pip": true, "go": true, "cargo": true, "gem": true,
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "mksh": true, "ash": true, "fish": true,
	"csh": true, "tcsh": true, "busybox": true, "python": true, "node": true, "deno": true, "perl": true,
	"ruby": true, "php": true, "lua": true, "tclsh": true, "osascript": true, "pwsh": true, "powershell": true, "cmd": true,
	"sudo": true, "su": true, "doas": true, "pkexec": true, "runuser": true,
	"curl": true, "wget": true, "ssh": true, "nc": true, "ncat": true, "socat": true,
	"env": true, "xargs": true, "nohup": true, "nice": true, "timeout": true, "setsid": true, "strace": true, "chroot": true,
}

// containerTools and destructiveVerbs: these verbs are never grantable on
// podman or docker, wherever they appear in a pattern.
var (
	containerTools   = map[string]bool{"podman": true, "docker": true}
	destructiveVerbs = map[string]bool{
		"rm": true, "rmi": true, "run": true, "exec": true, "cp": true, "build": true, "create": true,
		"start": true, "stop": true, "restart": true, "kill": true, "commit": true, "push": true, "pull": true,
		"load": true, "import": true, "prune": true, "system": true, "volume": true, "network": true,
	}
)

// baseName is the lower-case base name of a program without .exe and
// without a version suffix, so python3.12 and pip3 become python and pip.
func baseName(path string) string {
	n := strings.TrimSuffix(strings.ToLower(filepath.Base(path)), ".exe")
	return strings.TrimRight(n, "0123456789.-")
}

var shimName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// shim is a checked Shim with its parsed patterns.
type shim struct {
	Shim
	pats [][]elem
}

// elem is one pattern element: one word, or an optional group of words.
type elem struct {
	words    []string
	optional bool
}

var placeholders = map[string]bool{"{target}": true, "{n}": true, "{format}": true, "{time}": true}

func compileShim(s Shim) (*shim, error) {
	if !shimName.MatchString(s.Name) {
		return nil, errors.New("name must be a plain lower-case word")
	}
	if interpBuiltins[s.Name] || goBuiltins[s.Name] != nil {
		return nil, errors.New("name is a builtin")
	}
	if !filepath.IsAbs(s.Binary) {
		return nil, fmt.Errorf("binary %q is not an absolute path", s.Binary)
	}
	resolved, err := filepath.EvalSymlinks(s.Binary)
	if err != nil {
		return nil, fmt.Errorf("binary: %w", err)
	}
	for _, p := range []string{s.Name, s.Binary, resolved} {
		if neverGrantable[baseName(p)] {
			return nil, fmt.Errorf("%s is never grantable (package runner, fetcher, shell, interpreter, sudo or su)", baseName(p))
		}
	}
	tool := containerTools[baseName(s.Name)] || containerTools[baseName(s.Binary)] || containerTools[baseName(resolved)]
	c := &shim{Shim: s}
	for _, p := range s.Allow {
		var es []elem
		for _, w := range p {
			e := elem{words: []string{w}}
			if inner, ok := strings.CutPrefix(w, "["); ok {
				inner, ok = strings.CutSuffix(inner, "]")
				e = elem{words: strings.Fields(inner), optional: true}
				if !ok || len(e.words) == 0 {
					return nil, fmt.Errorf("pattern %q: bad optional group %q", p, w)
				}
			}
			for _, w := range e.words {
				if !placeholders[w] && (w == "" || strings.ContainsAny(w, "{}[]")) {
					return nil, fmt.Errorf("pattern %q: bad word %q", p, w)
				}
				if tool && destructiveVerbs[w] {
					return nil, fmt.Errorf("%s %s is never grantable", s.Name, w)
				}
			}
			es = append(es, e)
		}
		c.pats = append(c.pats, es)
	}
	return c, nil
}

// match reports whether argv (after the name) matches one allowed pattern.
func (s *shim) match(argv, targets []string) bool {
	for _, p := range s.pats {
		if matchElems(p, argv, targets) {
			return true
		}
	}
	return false
}

func matchElems(es []elem, argv, targets []string) bool {
	if len(es) == 0 {
		return len(argv) == 0
	}
	e, n := es[0], len(es[0].words)
	if n <= len(argv) && matchWords(e.words, argv[:n], targets) && matchElems(es[1:], argv[n:], targets) {
		return true
	}
	return e.optional && matchElems(es[1:], argv, targets)
}

func matchWords(words, argv, targets []string) bool {
	for i, w := range words {
		if !matchWord(w, argv[i], targets) {
			return false
		}
	}
	return true
}

const (
	digits      = "0123456789"
	letters     = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	formatChars = letters + digits + ` {}._:,|=-/%"()\`
	timeChars   = letters + digits + ":+-."
)

func matchWord(w, a string, targets []string) bool {
	switch w {
	case "{target}":
		return a != "" && slices.Contains(targets, a)
	case "{n}":
		n, err := strconv.Atoi(a)
		return len(a) <= 6 && only(a, digits) && err == nil && n <= 100000
	case "{format}":
		return len(a) <= 256 && only(a, formatChars)
	case "{time}":
		return len(a) <= 40 && only(a, timeChars)
	}
	return w == a
}

// only reports whether s is not empty and has only bytes from set.
func only(s, set string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(set, s[i]) < 0 {
			return false
		}
	}
	return true
}

// presets are the preset shims without their binaries.
var presets = map[string][]Shim{
	"podman-read": {{Name: "podman", Allow: []Pattern{
		{"ps", "[-a]", "[--format {format}]"},
		{"logs", "[--timestamps]", "--tail", "{n}", "{target}"},
		{"inspect", "[--format {format}]", "{target}"},
		{"stats", "--no-stream", "[--format {format}]", "[{target}]"},
		{"top", "{target}"},
		{"events", "--since", "{time}", "--until", "{time}", "[--format {format}]"},
	}}},
	"systemd-read": {
		{Name: "systemctl", Allow: []Pattern{
			{"status", "[--no-pager]", "{target}"},
			{"is-active", "{target}"},
			{"list-units", "--type=service", "[--no-pager]"},
		}},
		{Name: "journalctl", Allow: []Pattern{
			{"-u", "{target}", "-n", "{n}", "--no-pager"},
		}},
	},
}

// Presets are named shim sets. Each call resolves the binaries with
// exec.LookPath, and leaves a preset out when one of its binaries is missing.
func Presets() map[string][]Shim {
	out := map[string][]Shim{}
next:
	for name, shims := range presets {
		resolved := slices.Clone(shims)
		for i := range resolved {
			p, err := exec.LookPath(resolved[i].Name)
			if err == nil {
				p, err = filepath.Abs(p)
			}
			if err != nil {
				continue next
			}
			resolved[i].Binary = p
		}
		out[name] = resolved
	}
	return out
}
