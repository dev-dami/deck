// Package attribution keeps Deck's credit on the commits its agents make.
//
// Agents are told to end commits with Deck's trailer, but a CLI may add its own
// ("Co-Authored-By: Claude ...", "Generated with Codex") anyway. The guard is a
// set of git hooks in ~/.deck/hooks that Deck points the agents' git at through
// GIT_CONFIG_PARAMETERS, the environment form of `git -c`. Every hook first runs
// the repository's own hook of that name, so husky and friends keep working;
// commit-msg then strips other AI attribution and adds Deck's trailer.
//
// It only touches git run by an agent Deck started: the user's own git, the
// repository's config and its hooks are left alone.
package attribution

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dev-dami/deck/internal/deck/brand"
)

// EnvKey is git's environment form of `git -c`, which outranks every config
// file, so a repository's own core.hooksPath (husky's .husky) still routes
// through the guard, which then runs that hook.
const EnvKey = "GIT_CONFIG_PARAMETERS"

// hookNames are git's client-side hooks. A guard hook exists for each, because
// core.hooksPath replaces the whole hooks folder: a missing one would silently
// skip the repository's own hook of that name.
var hookNames = []string{
	"applypatch-msg", "pre-applypatch", "post-applypatch",
	"pre-commit", "pre-merge-commit", "prepare-commit-msg", "commit-msg", "post-commit",
	"pre-rebase", "post-checkout", "post-merge", "pre-push", "post-rewrite",
	"reference-transaction", "push-to-checkout", "pre-auto-gc", "sendemail-validate",
	"fsmonitor-watchman", "post-index-change",
}

// forward runs the repository's own hook of the same name, the one git would
// have run without the guard. It reads core.hooksPath with the guard's override
// removed, and skips itself if the user pointed core.hooksPath at the guard.
const forward = `#!/bin/sh
# Deck attribution guard. Runs this repository's own hook of the same name.
name=$(basename "$0")
self=$(cd "$(dirname "$0")" && pwd -P)
hooks=$(GIT_CONFIG_PARAMETERS= git config --path core.hooksPath 2>/dev/null)
[ -n "$hooks" ] || hooks=$(GIT_CONFIG_PARAMETERS= git rev-parse --git-path hooks 2>/dev/null)
own=""
if [ -n "$hooks" ] && [ -d "$hooks" ] && [ "$(cd "$hooks" && pwd -P)" != "$self" ] && [ -x "$hooks/$name" ]; then
  own="$hooks/$name"
fi
`

// commitMsg strips other AI attribution from the message and adds Deck's
// trailer, after the repository's own commit-msg hook has had its say.
var commitMsg = forward + `if [ -n "$own" ]; then "$own" "$@" || exit $?; fi
msg="$1"
sed -E -i.deck \
  -e '/^[[:space:]]*[Cc]o-[Aa]uthored-[Bb]y:.*([Cc]laude|[Aa]nthropic|ANTHROPIC|[Cc]odex|[Oo]pen[Aa][Ii]|ChatGPT|[Oo]pen[Cc]ode|[Gg]emini|[Aa]ntigravity|[Cc]opilot|[Cc]ursor|[Aa]ider|[Dd]evin|[Ww]indsurf)/d' \
  -e '/[Gg]enerated (with|by) .*([Cc]laude|[Cc]odex|[Oo]pen[Aa][Ii]|ChatGPT|[Oo]pen[Cc]ode|[Gg]emini|[Aa]ntigravity|[Cc]opilot|[Cc]ursor|[Aa]ider)/d' \
  "$msg" && rm -f "$msg.deck"
# A commit Deck authors needs no Deck co-author line.
case "$(git var GIT_AUTHOR_IDENT 2>/dev/null)" in
  *` + shellQuote("<"+brand.AuthorEmail+">") + `*) exit 0 ;;
esac
exec git interpret-trailers --in-place --if-exists addIfDifferent --trailer ` + shellQuote(brand.CommitTrailer) + ` "$msg"
`

var (
	once    sync.Once
	dir     string
	initErr error

	homeDir = os.UserHomeDir // tests point it elsewhere
)

// Env returns the variables that route an agent's git through the guard,
// installing the hooks the first time. With author, the agent's commits are
// also authored by Deck (the user stays the committer), so Deck counts among
// the repository's contributors. On error the caller starts the agent without
// the guard; the instruction to use Deck's trailer still stands.
func Env(author bool) (map[string]string, error) {
	once.Do(func() { dir, initErr = install() })
	if initErr != nil {
		return nil, initErr
	}
	return envFor(dir, author), nil
}

func envFor(dir string, author bool) map[string]string {
	v := "'core.hooksPath'=" + shellQuote(filepath.ToSlash(dir))
	if prev := os.Getenv(EnvKey); prev != "" {
		v = prev + " " + v // the last setting of a key wins
	}
	env := map[string]string{EnvKey: v}
	if author {
		env["GIT_AUTHOR_NAME"] = brand.AuthorName
		env["GIT_AUTHOR_EMAIL"] = brand.AuthorEmail
	}
	return env
}

// install writes the hooks into ~/.deck/hooks, rewriting only what changed.
func install() (string, error) {
	home, err := homeDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(home, ".deck", "hooks")
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", err
	}
	for _, name := range hookNames {
		body := forward + `[ -n "$own" ] && exec "$own" "$@"
exit 0
`
		if name == "commit-msg" {
			body = commitMsg
		}
		if err := writeIfChanged(filepath.Join(d, name), []byte(body)); err != nil {
			return "", fmt.Errorf("attribution: %w", err)
		}
	}
	return d, nil
}

func writeIfChanged(path string, data []byte) error {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return nil
	}
	if err := os.WriteFile(path, data, 0o755); err != nil {
		return err
	}
	return os.Chmod(path, 0o755)
}

// shellQuote single-quotes s for sh (and for GIT_CONFIG_PARAMETERS, which uses
// the same quoting).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
