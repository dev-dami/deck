package peer

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dev-dami/deck/internal/deck/config"
)

// opsWords map the words of a request to the presets they ask for.
var opsWords = map[string]string{
	"podman containers": "podman-read",
	"podman":            "podman-read",
	"containers":        "podman-read",
	"services":          "systemd-read",
	"systemd":           "systemd-read",
	"systemd services":  "systemd-read",
	"systemd units":     "systemd-read",
	"units":             "systemd-read",
}

// ParseOps reads an ops request the person typed:
//
//	ask <peer> to report <what>: <field>, <field>, ... [for <target>, ...]
//
// where what names presets ("podman containers", "services", joined with
// "and"). It returns the teammate and the contract, with every field a
// string of at most defaultFieldLength characters. It is a fixed template,
// not a guess: text it does not know is an error.
func ParseOps(text string) (string, Contract, error) {
	s := strings.Join(strings.Fields(text), " ")
	const usage = "write: ask <teammate> to report <what>: <field>, <field> [for <target>, <target>]"
	rest, ok := cutPrefixFold(s, "ask ")
	if !ok {
		return "", Contract{}, errors.New(usage)
	}
	name, rest, _ := strings.Cut(rest, " ")
	name = strings.ToLower(name)
	if !config.ValidPeerName(name) {
		return "", Contract{}, fmt.Errorf("%q is not a teammate's name", truncate(name, 40))
	}
	if rest, ok = cutPrefixFold(rest, "to report "); !ok {
		return "", Contract{}, errors.New(usage)
	}
	what, rest, ok := strings.Cut(rest, ":")
	if !ok {
		return "", Contract{}, errors.New("put a colon before the fields: " + usage)
	}
	what = strings.ToLower(strings.TrimSpace(what))
	c := Contract{Kind: KindOps, Title: "report " + what}
	for _, w := range strings.FieldsFunc(strings.ReplaceAll(what, " and ", ","), func(r rune) bool { return r == ',' }) {
		p, ok := opsWords[strings.TrimSpace(w)]
		if !ok {
			return "", Contract{}, fmt.Errorf("%q is not something Deck can report (use %s)", strings.TrimSpace(w), opsWordList())
		}
		if !slices.Contains(c.Scope, p) {
			c.Scope = append(c.Scope, p)
		}
	}
	if len(c.Scope) == 0 {
		return "", Contract{}, fmt.Errorf("say what to report (use %s)", opsWordList())
	}
	fields, targets, _ := cutFold(rest, " for ")
	for _, f := range strings.Split(fields, ",") {
		f = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(f)), " ", "_")
		if f == "" {
			continue
		}
		c.Schema = append(c.Schema, Field{Name: f, Type: FieldString, MaxLength: defaultFieldLength})
	}
	for _, t := range strings.Split(targets, ",") {
		if t = strings.TrimSpace(t); t != "" {
			c.Targets = append(c.Targets, t)
		}
	}
	if err := checkOps(c); err != nil {
		return "", Contract{}, err
	}
	return name, c, nil
}

// opsWordList is the words ParseOps knows, for an error.
func opsWordList() string {
	var ws []string
	for w := range opsWords {
		ws = append(ws, `"`+w+`"`)
	}
	slices.Sort(ws)
	return strings.Join(ws, ", ")
}

// cutPrefixFold is strings.CutPrefix with the ASCII prefix matched without
// regard to case.
func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) < len(prefix) || !strings.EqualFold(s[:len(prefix)], prefix) {
		return s, false
	}
	return s[len(prefix):], true
}

// cutFold is strings.Cut with the ASCII sep matched without regard to case.
func cutFold(s, sep string) (before, after string, found bool) {
	for i := 0; i+len(sep) <= len(s); i++ {
		if strings.EqualFold(s[i:i+len(sep)], sep) {
			return s[:i], s[i+len(sep):], true
		}
	}
	return s, "", false
}
