package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/agent"
)

// Models implements agent.ModelLister. The list is the one Claude Code's own
// /model picker shows: a short-lived `claude -p` in stream-json mode is sent
// the SDK's initialize request, and the models (with their effort levels) are
// read from its answer. Nothing is prompted, so no model API is called. When
// that fails the aliases are read from the `--model` entry of `claude --help`,
// and failing that a static list is returned. The result is cached per adapter.
func (a *adapter) Models(ctx context.Context) ([]agent.ModelInfo, error) {
	a.modelsMu.Lock()
	defer a.modelsMu.Unlock()
	if a.models != nil {
		return append([]agent.ModelInfo(nil), a.models...), nil
	}
	var list []agent.ModelInfo
	if out, err := a.initAnswer(ctx); err == nil {
		list = modelsFromInit(out)
	}
	if list == nil {
		if txt, err := a.helpText(ctx); err == nil {
			list = modelsFromHelp(txt)
		}
	}
	if list == nil {
		list = fallbackModels()
	}
	a.models = list
	return append([]agent.ModelInfo(nil), list...), nil
}

const initRequestID = "deck-models"

// initAnswer runs claude just long enough to answer the initialize request and
// returns that one answer line.
func (a *adapter) initAnswer(ctx context.Context) ([]byte, error) {
	if a.initFn != nil {
		return a.initFn(ctx)
	}
	bin := a.binary
	if bin == "" {
		bin = "claude"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.Command(path, "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose")
	cmd.Dir = os.TempDir() // no project's settings or hooks
	setProcAttr(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer func() {
		_ = stdin.Close()
		killTree(cmd)
		_ = cmd.Wait()
	}()
	if _, err := stdin.Write([]byte(`{"type":"control_request","request_id":"` + initRequestID + `","request":{"subtype":"initialize"}}` + "\n")); err != nil {
		return nil, err
	}
	found := make(chan []byte, 1)
	go func() {
		defer close(found)
		r := bufio.NewReaderSize(stdout, 1<<20)
		for {
			line, err := r.ReadBytes('\n')
			if bytes.Contains(line, []byte(`"control_response"`)) && bytes.Contains(line, []byte(initRequestID)) {
				found <- line
				return
			}
			if err != nil {
				return
			}
		}
	}()
	select {
	case line, ok := <-found:
		if !ok {
			return nil, errors.New("claude: no answer to initialize")
		}
		return line, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// initAnswerJSON is the part of the initialize answer Deck reads. The answer
// also describes the account; that is never decoded.
type initAnswerJSON struct {
	Response struct {
		Subtype   string `json:"subtype"`
		RequestID string `json:"request_id"`
		Response  struct {
			Models []struct {
				Value          string   `json:"value"`
				DisplayName    string   `json:"displayName"`
				Description    string   `json:"description"`
				SupportsEffort bool     `json:"supportsEffort"`
				Efforts        []string `json:"supportedEffortLevels"`
			} `json:"models"`
		} `json:"response"`
	} `json:"response"`
}

// effortDescriptions describe Claude Code's effort levels, which it lists
// without descriptions.
var effortDescriptions = map[string]string{
	"low":    "Quick answers, light reasoning",
	"medium": "Balanced speed and reasoning depth",
	"high":   "Deeper reasoning for complex work",
	"xhigh":  "Extra reasoning depth for hard problems",
	"max":    "The most reasoning, slowest",
}

// modelsFromInit reads the models out of an initialize answer, or nil.
func modelsFromInit(line []byte) []agent.ModelInfo {
	var ans initAnswerJSON
	if json.Unmarshal(line, &ans) != nil || ans.Response.Subtype != "success" || ans.Response.RequestID != initRequestID {
		return nil
	}
	var out []agent.ModelInfo
	for _, m := range ans.Response.Response.Models {
		if m.Value == "" {
			continue
		}
		mi := agent.ModelInfo{ID: m.Value, Name: m.DisplayName, Description: m.Description}
		if m.Value == "default" {
			mi.ID, mi.Default = "", true
		}
		if m.SupportsEffort {
			for _, e := range m.Efforts {
				mi.Efforts = append(mi.Efforts, agent.EffortInfo{Level: e, Description: effortDescriptions[e]})
			}
		}
		out = append(out, mi)
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
