package brand

import (
	"os"
	"path/filepath"
	"strings"
)

var homeDir = os.UserHomeDir // tests point it elsewhere

// deckDir is a folder under ~/.deck, where agents in Deck keep what each CLI
// would otherwise keep in its own private folder.
func deckDir(elem ...string) string {
	home, err := homeDir()
	if err != nil {
		home = ""
	}
	return filepath.Join(append([]string{home, ".deck"}, elem...)...)
}

// PlansDir is where agents in Deck keep plan files: ~/.deck/plans, beside the
// threads, rather than each CLI's own folder (~/.claude/plans).
func PlansDir() string {
	return deckDir("plans")
}

// MemoryDir is where agents in Deck keep memory for a project:
// ~/.deck/memory/<slug>, rather than ~/.claude/projects/<slug>/memory.
func MemoryDir(project string) string {
	return deckDir("memory", ProjectSlug(project))
}

// ProjectSlug names a project's folder the way Claude Code does: every
// character but an ASCII letter or digit becomes '-', so C:\Users\me\app is
// C--Users-me-app.
func ProjectSlug(project string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, project)
}

// cliPlace is a private folder an agent CLI keeps under the home directory,
// and the Deck name files in it are shown under. Each element of under
// matches one path element, ignoring case; "*" matches any one.
type cliPlace struct {
	under []string
	label string
}

// cliPlaces is every folder ShowPath renames: the ones the CLIs write that
// Deck cannot always redirect, and Deck's own, so a file reads the same
// either way. A new CLI or folder goes here.
var cliPlaces = []cliPlace{
	{[]string{".deck", "memory", "*"}, "memory"},
	{[]string{".deck", "plans"}, "plans"},

	// Claude Code. Deck redirects both (see agent/claude writeSettings),
	// but sessions started outside Deck and resumed in it still use these.
	{[]string{".claude", "projects", "*", "memory"}, "memory"},
	{[]string{".claude", "plans"}, "plans"},
}

// ShowPath names a file in a CLI's private folder the way Deck does, for
// display only: ~/.claude/projects/<slug>/memory/MEMORY.md shows as
// "memory · MEMORY.md". It takes Windows or POSIX separators and a path that
// starts at the home directory, "~", $HOME or %USERPROFILE%. ok is false, and
// p comes back as it is, for any path in no such folder.
func ShowPath(p string) (string, bool) {
	rest, ok := underHome(p)
	if !ok {
		return p, false
	}
	elems := strings.FieldsFunc(rest, func(r rune) bool { return r == '/' || r == '\\' })
	for _, pl := range cliPlaces {
		if len(elems) < len(pl.under) || !matchElems(pl.under, elems) {
			continue
		}
		if tail := elems[len(pl.under):]; len(tail) > 0 {
			return pl.label + " · " + strings.Join(tail, "/"), true
		}
		return pl.label, true
	}
	return p, false
}

func matchElems(pattern, elems []string) bool {
	for i, s := range pattern {
		if s != "*" && !strings.EqualFold(s, elems[i]) {
			return false
		}
	}
	return true
}

// underHome returns the part of p below the home directory, with any mix of
// separators, or false when p is not below it.
func underHome(p string) (string, bool) {
	p = strings.ReplaceAll(p, `\`, "/")
	homes := []string{"~", "$HOME", "${HOME}", "%USERPROFILE%"}
	if h, err := homeDir(); err == nil {
		if h = strings.TrimRight(strings.ReplaceAll(h, `\`, "/"), "/"); h != "" {
			homes = append(homes, h)
		}
	}
	for _, h := range homes {
		// Windows paths ignore case, and a drive letter may come either way.
		if len(p) > len(h) && p[len(h)] == '/' && strings.EqualFold(p[:len(h)], h) {
			return p[len(h)+1:], true
		}
	}
	return "", false
}
