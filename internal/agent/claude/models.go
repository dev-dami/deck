package claude

import (
	"bytes"
	"context"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/agent"
)

// Models implements agent.ModelLister. Claude Code has no model-list command,
// so the aliases are read from the `--model` entry of the installed
// `claude --help` text; if that cannot be run or parsed a static list is
// returned. No API is ever called. The result is cached per adapter.
func (a *adapter) Models(ctx context.Context) ([]agent.ModelInfo, error) {
	a.modelsMu.Lock()
	defer a.modelsMu.Unlock()
	if a.models != nil {
		return append([]agent.ModelInfo(nil), a.models...), nil
	}
	var list []agent.ModelInfo
	if txt, err := a.helpText(ctx); err == nil {
		list = modelsFromHelp(txt)
	}
	if list == nil {
		list = fallbackModels()
	}
	a.models = list
	return append([]agent.ModelInfo(nil), list...), nil
}

func (a *adapter) helpText(ctx context.Context) (string, error) {
	if a.helpFn != nil {
		return a.helpFn(ctx)
	}
	bin := a.binary
	if bin == "" {
		bin = "claude"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--help")
	var out bytes.Buffer
	cmd.Stdout = &out
	setProcAttr(cmd)
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out.String(), nil
}

var quotedAlias = regexp.MustCompile(`['"]([A-Za-z][A-Za-z0-9._-]*)['"]`)

// parseModelAliases extracts the quoted alias examples (e.g. 'opus') from the
// `--model` entry of a `claude --help` text. Returns nil when none are found.
func parseModelAliases(help string) []string {
	lines := strings.Split(strings.ReplaceAll(help, "\r\n", "\n"), "\n")
	indent := func(s string) int { return len(s) - len(strings.TrimLeft(s, " \t")) }
	var entry []string
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if !(strings.HasPrefix(t, "--model ") || t == "--model" || strings.Contains(t, ", --model ")) {
			continue
		}
		base := indent(l)
		entry = append(entry, t)
		for _, c := range lines[i+1:] {
			if strings.TrimSpace(c) == "" || indent(c) <= base {
				break
			}
			entry = append(entry, strings.TrimSpace(c))
		}
		break
	}
	if len(entry) == 0 {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range quotedAlias.FindAllStringSubmatch(strings.Join(entry, " "), -1) {
		alias := strings.ToLower(m[1])
		if alias == "default" || seen[alias] {
			continue
		}
		seen[alias] = true
		out = append(out, alias)
	}
	return out
}

var aliasDescriptions = map[string]string{
	"opus":   "Most capable model for complex work",
	"sonnet": "Balanced speed and capability",
	"haiku":  "Fastest, lightweight model",
	"fable":  "Latest Fable model",
}

func modelInfoFor(alias string) agent.ModelInfo {
	desc := aliasDescriptions[alias]
	if desc == "" {
		desc = "Latest " + alias + " model"
	}
	return agent.ModelInfo{ID: alias, Name: strings.ToUpper(alias[:1]) + alias[1:], Description: desc}
}

func defaultModel() agent.ModelInfo {
	return agent.ModelInfo{ID: "", Name: "Default", Description: "Claude Code's default model", Default: true}
}

func modelsFromHelp(help string) []agent.ModelInfo {
	aliases := parseModelAliases(help)
	if len(aliases) == 0 {
		return nil
	}
	out := []agent.ModelInfo{defaultModel()}
	for _, a := range aliases {
		out = append(out, modelInfoFor(a))
	}
	return out
}

func fallbackModels() []agent.ModelInfo {
	out := []agent.ModelInfo{defaultModel()}
	for _, a := range []string{"opus", "sonnet", "haiku", "fable"} {
		out = append(out, modelInfoFor(a))
	}
	return out
}
