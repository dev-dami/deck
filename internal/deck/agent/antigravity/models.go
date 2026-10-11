package antigravity

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// defaultBinary is the command looked up on PATH when no binary is configured.
const defaultBinary = "agy"

// effortSuffixes are the reasoning-effort suffixes agy bakes into model slugs
// (`gemini-3.8-flash-high`), in canonical order.
var effortSuffixes = []string{"low", "medium", "high"}

var effortDescriptions = map[string]string{
	"low":    "Quick answers, light reasoning",
	"medium": "Balanced speed and reasoning depth",
	"high":   "Deeper reasoning for complex work",
}

// Models implements agent.ModelLister. `agy models` prints one slug and
// display name per line; slugs that share a base with different effort
// suffixes are grouped under that base with the suffixes as efforts, so Deck
// shows one model with low/medium/high instead of three. Models() never
// prompts, so no account is used. When the list cannot be read a small static
// list is returned. The result is cached per adapter; concurrent callers share
// one load, and a caller that gives up gets the static list while the load
// goes on for the next one.
func (a *adapter) Models(ctx context.Context) ([]agent.ModelInfo, error) {
	list, err := a.models.Get(ctx, func(ctx context.Context) ([]agent.ModelInfo, error) {
		return a.loadModels(ctx), nil
	})
	if err != nil {
		return fallbackModels(), nil
	}
	return list, nil
}

// loadModels reads `agy models`; it never fails (see Models).
func (a *adapter) loadModels(ctx context.Context) []agent.ModelInfo {
	var list []agent.ModelInfo
	if out, err := a.modelsOutput(ctx); err == nil {
		list = modelsFromList(out)
	}
	if list == nil {
		list = fallbackModels()
	}
	return list
}

func (a *adapter) modelsOutput(ctx context.Context) (string, error) {
	if a.modelsFn != nil {
		return a.modelsFn(ctx)
	}
	bin := a.binary
	if bin == "" {
		bin = defaultBinary
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "models")
	cmd.Dir = os.TempDir() // no project's settings
	setProcAttr(cmd)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

type modelVariant struct {
	slug string
	name string
}

// modelsFromList groups `agy models` output into ModelInfos, or nil when
// nothing parses. A slug joins a family only when its base has at least two
// effort-suffixed variants; lone suffixed slugs (e.g. gpt-oss-120b-medium)
// stay standalone with no efforts, since --effort was never verified for them.
func modelsFromList(out string) []agent.ModelInfo {
	byBase := map[string][]modelVariant{}
	var bases []string
	seenSlug := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		slug, name := splitModelLine(line)
		if slug == "" || seenSlug[slug] {
			continue
		}
		seenSlug[slug] = true
		if name == "" {
			name = slug
		}
		base, _ := splitEffort(slug)
		if _, ok := byBase[base]; !ok {
			bases = append(bases, base)
		}
		byBase[base] = append(byBase[base], modelVariant{slug: slug, name: name})
	}
	if len(bases) == 0 {
		return nil
	}
	list := []agent.ModelInfo{defaultModel()}
	for _, base := range bases {
		vs := byBase[base]
		have := map[string]bool{}
		first := ""
		for _, v := range vs {
			if _, e := splitEffort(v.slug); e != "" {
				have[e] = true
				if first == "" {
					first = e
				}
			}
		}
		if len(have) < 2 {
			for _, v := range vs {
				list = append(list, agent.ModelInfo{ID: v.slug, Name: v.name})
			}
			continue
		}
		name := familyName(vs)
		mi := agent.ModelInfo{ID: base, Name: name, Description: "Antigravity " + name}
		for _, e := range effortSuffixes {
			if have[e] {
				mi.Efforts = append(mi.Efforts, agent.EffortInfo{Level: e, Description: effortDescriptions[e]})
			}
		}
		// Prefer medium, else defer to the CLI's own listing order.
		mi.DefaultEffort = first
		for _, e := range mi.Efforts {
			if e.Level == "medium" {
				mi.DefaultEffort = "medium"
			}
		}
		list = append(list, mi)
	}
	return list
}

// splitModelLine cuts an `agy models` line into slug and display name. The
// CLI separates them with a tab; two or more spaces are the fallback.
func splitModelLine(line string) (slug, name string) {
	if i := strings.IndexByte(line, '\t'); i >= 0 {
		return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:])
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return strings.TrimSpace(line), ""
	}
	return fields[0], strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
}

// splitEffort cuts a trailing -low/-medium/-high effort suffix off a slug.
func splitEffort(slug string) (base, effort string) {
	for _, e := range effortSuffixes {
		if strings.HasSuffix(slug, "-"+e) {
			return strings.TrimSuffix(slug, "-"+e), e
		}
	}
	return slug, ""
}

// familyName names a grouped family from its variants' display names, which
// carry the effort in parentheses ("Gemini 3.8 Flash (High)").
func familyName(vs []modelVariant) string {
	for _, v := range vs {
		for _, e := range effortSuffixes {
			if tail := " (" + strings.ToUpper(e[:1]) + e[1:] + ")"; strings.HasSuffix(v.name, tail) {
				return strings.TrimSuffix(v.name, tail)
			}
		}
	}
	return vs[0].name
}

func defaultModel() agent.ModelInfo {
	return agent.ModelInfo{ID: "", Name: "Default", Description: "agy's default model", Default: true}
}

func fallbackModels() []agent.ModelInfo {
	flash := agent.ModelInfo{ID: "gemini-3.8-flash", Name: "Gemini 3.8 Flash", Description: "Antigravity Gemini 3.8 Flash", DefaultEffort: "medium"}
	for _, e := range effortSuffixes {
		flash.Efforts = append(flash.Efforts, agent.EffortInfo{Level: e, Description: effortDescriptions[e]})
	}
	return []agent.ModelInfo{defaultModel(), flash}
}

// resolveSlug maps a base model id plus effort to the exact --model slug. It
// loads the model list on first use (no prompt, no account use); when the
// list is unavailable it falls back to syntax (base-effort), and the CLI
// fails loudly on slugs it does not know.
func (a *adapter) resolveSlug(ctx context.Context, base, effort string) string {
	if base == "" {
		return ""
	}
	models, _ := a.Models(ctx) // shared with the picker's load; never fails
	for _, m := range models {
		if m.ID != base {
			continue
		}
		if len(m.Efforts) == 0 {
			return base // standalone full slug
		}
		want := effort
		if want == "" {
			want = m.DefaultEffort
		}
		for _, e := range m.Efforts {
			if e.Level == want {
				return base + "-" + want
			}
		}
		return base + "-" + m.DefaultEffort
	}
	if effort != "" {
		return base + "-" + effort
	}
	return base
}
