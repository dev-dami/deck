package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/skills"
)

// skillInfo is one skill some agent can see.
type skillInfo struct {
	Name        string
	Description string
	Agent       string // "claude", "codex", "opencode", "antigravity", "gemini", "agents" (shared), "deck"
	Scope       string // "project", "user" or "builtin"
	Dir         string // the skill's folder (deck://skills/<name> for builtins)
	File        string // its SKILL.md
	Builtin     bool
}

// Source names where the skill comes from: "claude-user", "deck-builtin".
func (s skillInfo) Source() string { return s.Agent + "-" + s.Scope }

// skillDir is one folder of skills and the agent that reads it.
type skillDir struct{ rel, agent string }

// projectSkillDirs are relative to the project; userSkillDirs to home.
var (
	projectSkillDirs = []skillDir{
		{".claude/skills", "claude"},
		{".agents/skills", "agents"},
		{".codex/skills", "codex"},
		{".opencode/skill", "opencode"},
		{".opencode/skills", "opencode"},
		{".agent/skills", "antigravity"},
		{".gemini/skills", "gemini"},
	}
	userSkillDirs = []skillDir{
		{".claude/skills", "claude"},
		{".agents/skills", "agents"},
		{".codex/skills", "codex"},
		{".config/opencode/skill", "opencode"},
		{".config/opencode/skills", "opencode"},
		{".gemini/antigravity/skills", "antigravity"},
		{".gemini/skills", "gemini"},
	}
)

const (
	maxSkills     = 500
	maxSkillDepth = 3 // base/<group>/<name>/SKILL.md at most
)

// discoverSkills lists every skill visible to any agent: the project's, the
// user's, then Deck's builtins. A file reached twice is listed once.
func discoverSkills(env *Env) []skillInfo {
	var out []skillInfo
	seen := map[string]bool{}
	add := func(base, ag, scope string) {
		for _, f := range findSkillFiles(base) {
			key := strings.ToLower(filepath.Clean(f))
			if seen[key] || len(out) >= maxSkills {
				continue
			}
			seen[key] = true
			out = append(out, readSkillInfo(f, ag, scope))
		}
	}
	var projects []string
	for _, d := range append([]string{env.Dir}, env.Roots...) {
		dup := d == ""
		for _, p := range projects {
			dup = dup || samePath(p, d)
		}
		if !dup {
			projects = append(projects, d)
		}
	}
	for _, p := range projects {
		for _, sd := range projectSkillDirs {
			add(filepath.Join(p, filepath.FromSlash(sd.rel)), sd.agent, "project")
		}
	}
	if home := env.home(); home != "" {
		for _, sd := range userSkillDirs {
			add(filepath.Join(home, filepath.FromSlash(sd.rel)), sd.agent, "user")
		}
	}
	for _, s := range skills.DiscoverBuiltin() {
		out = append(out, skillInfo{
			Name: s.Name, Description: s.Description, Agent: "deck", Scope: "builtin",
			Dir: s.Path, File: s.SkillFilePath, Builtin: true,
		})
	}
	return out
}

// findSkillFiles finds SKILL.md files under base, at most maxSkillDepth
// folders down. A missing base is no skills.
func findSkillFiles(base string) []string {
	var out []string
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		ents, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range ents {
			name := e.Name()
			p := filepath.Join(dir, name)
			isDir := e.IsDir()
			if e.Type()&fs.ModeSymlink != 0 {
				st, err := os.Stat(p)
				if err != nil {
					continue
				}
				isDir = st.IsDir()
			}
			if !isDir {
				if depth > 0 && name == skills.SkillFileName {
					out = append(out, p)
				}
				continue
			}
			if depth >= maxSkillDepth || name == ".git" || name == "node_modules" {
				continue
			}
			walk(p, depth+1)
		}
	}
	walk(base, 0)
	sort.Strings(out)
	return out
}

// readSkillInfo reads a SKILL.md leniently: a skill whose front matter does
// not parse, or whose name differs from its folder, still shows (named after
// its folder), since another agent may well load it.
func readSkillInfo(file, ag, scope string) skillInfo {
	dir := filepath.Dir(file)
	si := skillInfo{Name: filepath.Base(dir), Agent: ag, Scope: scope, Dir: dir, File: file}
	f, err := os.Open(file)
	if err != nil {
		return si
	}
	defer f.Close()
	buf := make([]byte, 16<<10)
	n, _ := io.ReadFull(f, buf)
	if s, err := skills.ParseContent(buf[:n]); err == nil {
		if s.Name != "" {
			si.Name = s.Name
		}
		si.Description = s.Description
	}
	return si
}

func opSkillsList(_ context.Context, env *Env, _ args) (string, error) {
	sks := discoverSkills(env)
	if len(sks) == 0 {
		return "No skills found.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d skills (execute skills.read {name, source?}):\n", len(sks))
	for _, s := range sks {
		fmt.Fprintf(&b, "  %s [%s] - %s\n", s.Name, s.Source(), clip(s.Description, 140))
	}
	return b.String(), nil
}

// findSkill picks the skill called name (from source, when given).
func findSkill(env *Env, name, source string) (skillInfo, []skillInfo, error) {
	var match []skillInfo
	for _, s := range discoverSkills(env) {
		if !strings.EqualFold(s.Name, name) {
			continue
		}
		if source != "" && !strings.EqualFold(s.Source(), source) && !strings.EqualFold(s.Agent, source) {
			continue
		}
		match = append(match, s)
	}
	if len(match) == 0 {
		return skillInfo{}, nil, fmt.Errorf("no skill %q (execute skills.list)", name)
	}
	return match[0], match[1:], nil
}

func opSkillsRead(_ context.Context, env *Env, a args) (string, error) {
	s, others, err := findSkill(env, strings.TrimSpace(a.str("name")), strings.TrimSpace(a.str("source")))
	if err != nil {
		return "", err
	}
	file := strings.TrimSpace(a.str("file"))
	var b strings.Builder
	fmt.Fprintf(&b, "Skill %s [%s]", s.Name, s.Source())
	if !s.Builtin {
		fmt.Fprintf(&b, " in %s", s.Dir)
	}
	if len(others) > 0 {
		var srcs []string
		for _, o := range others {
			srcs = append(srcs, o.Source())
		}
		fmt.Fprintf(&b, " (also from %s; pass source to pick)", strings.Join(srcs, ", "))
	}
	b.WriteString("\n\n")
	if file == "" || strings.EqualFold(file, skills.SkillFileName) {
		body, err := readSkillFile(s, "")
		if err != nil {
			return "", err
		}
		b.WriteString(body)
		if files := listSkillFiles(s); len(files) > 0 {
			b.WriteString("\n\nOther files in this skill (skills.read {name, file}):\n")
			for _, f := range files {
				b.WriteString("  " + f + "\n")
			}
		}
		return b.String(), nil
	}
	body, err := readSkillFile(s, file)
	if err != nil {
		return "", err
	}
	b.WriteString(body)
	return b.String(), nil
}

// errEscape refuses a path that would leave the skill's folder.
var errEscape = errors.New("file must be a relative path inside the skill's folder")

// cleanRel validates a path inside a skill folder and returns it cleaned,
// with forward slashes.
func cleanRel(file string) (string, error) {
	f := strings.ReplaceAll(strings.TrimSpace(file), `\`, "/")
	if f == "" || strings.HasPrefix(f, "/") || filepath.IsAbs(file) || filepath.VolumeName(file) != "" || strings.Contains(f, ":") {
		return "", errEscape
	}
	c := path.Clean(f)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", errEscape
	}
	return c, nil
}

// readSkillFile reads SKILL.md (file "") or one file inside the skill's
// folder. Paths that leave the folder, directly or through a link, are
// refused.
func readSkillFile(s skillInfo, file string) (string, error) {
	rel := skills.SkillFileName
	if file != "" {
		c, err := cleanRel(file)
		if err != nil {
			return "", err
		}
		rel = c
	}
	if s.Builtin {
		p := "builtin/" + strings.TrimPrefix(s.Dir, skills.BuiltinPrefix) + "/" + rel
		if !fs.ValidPath(p) {
			return "", errEscape
		}
		data, err := fs.ReadFile(skills.BuiltinFS(), p)
		if err != nil {
			return "", fmt.Errorf("no file %q in skill %s", rel, s.Name)
		}
		return string(data), nil
	}
	full := filepath.Join(s.Dir, filepath.FromSlash(rel))
	realDir, err := filepath.EvalSymlinks(s.Dir)
	if err != nil {
		return "", err
	}
	realFull, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", fmt.Errorf("no file %q in skill %s", rel, s.Name)
	}
	if r, err := filepath.Rel(realDir, realFull); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) || filepath.IsAbs(r) {
		return "", errEscape
	}
	st, err := os.Stat(realFull)
	if err != nil {
		return "", err
	}
	if st.IsDir() {
		return "", fmt.Errorf("%q is a folder; skills.read {name} lists the skill's files", rel)
	}
	f, err := os.Open(realFull)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, MaxOutput+1)
	n, _ := io.ReadFull(f, buf)
	return string(buf[:n]), nil
}

// listSkillFiles names the files of a skill other than SKILL.md.
func listSkillFiles(s skillInfo) []string {
	var out []string
	if s.Builtin {
		root := "builtin/" + strings.TrimPrefix(s.Dir, skills.BuiltinPrefix)
		_ = fs.WalkDir(skills.BuiltinFS(), root, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && path.Base(p) != skills.SkillFileName && len(out) < 100 {
				out = append(out, strings.TrimPrefix(p, root+"/"))
			}
			return nil
		})
		return out
	}
	_ = filepath.WalkDir(s.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || len(out) >= 100 {
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(s.Dir, p)
		if d.IsDir() {
			if rel != "." && (d.Name() == ".git" || d.Name() == "node_modules" || strings.Count(rel, string(filepath.Separator)) >= 3) {
				return filepath.SkipDir
			}
			return nil
		}
		if rel != skills.SkillFileName {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out
}

// skillPath finds ".../<name>/SKILL.md" in a command line or path.
var skillPath = regexp.MustCompile("(?i)([^\\s\"'`/\\\\]+)[/\\\\]SKILL\\.md")

// used is one skill seen in use in the thread.
type used struct {
	name   string
	agents []string
	how    []string
}

// skillsUsed finds the skills the thread's agents used: Claude's Skill tool,
// Deck's own skills.read, and any tool call that names a SKILL.md (a Read,
// or cat in a shell).
func skillsUsed(tools []ToolUse) []used {
	var out []used
	idx := map[string]int{}
	note := func(name, ag, how string) {
		name = strings.TrimSpace(strings.TrimPrefix(name, "/"))
		if name == "" {
			return
		}
		k := strings.ToLower(name)
		i, ok := idx[k]
		if !ok {
			i = len(out)
			idx[k] = i
			out = append(out, used{name: name})
		}
		u := &out[i]
		if !contains(u.agents, ag) {
			u.agents = append(u.agents, ag)
		}
		if !contains(u.how, how) {
			u.how = append(u.how, how)
		}
	}
	for _, t := range tools {
		switch {
		case t.Name == "Skill":
			var in struct {
				Skill   string `json:"skill"`
				Command string `json:"command"`
				Name    string `json:"name"`
			}
			_ = json.Unmarshal(t.Input, &in)
			n := in.Skill
			if n == "" {
				n = in.Command
			}
			if n == "" {
				n = in.Name
			}
			if n == "" {
				n = strings.Fields(t.Title + " ")[0]
			}
			note(n, t.Agent, "Skill tool")
			continue
		case t.Name == "deck":
			if in := agent.ParseExecInput(t.Input); in.Op == "skills.read" {
				var a struct {
					Name string `json:"name"`
				}
				_ = json.Unmarshal(in.Args, &a)
				note(a.Name, t.Agent, "skills.read")
				continue
			}
		}
		for _, m := range skillPath.FindAllStringSubmatch(t.Title+" "+string(t.Input), -1) {
			note(m[1], t.Agent, "read SKILL.md")
		}
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}

func opSkillsUsed(_ context.Context, env *Env, _ args) (string, error) {
	us := skillsUsed(env.Tools)
	if len(us) == 0 {
		return "No skill use seen in this thread yet. execute skills.list shows every skill the agents can see.", nil
	}
	sks := discoverSkills(env)
	var b strings.Builder
	b.WriteString("Skills used in this thread:\n")
	for _, u := range us {
		fmt.Fprintf(&b, "  %s - used by %s (%s)", u.name, strings.Join(u.agents, ", "), strings.Join(u.how, ", "))
		var srcs []string
		for _, s := range sks {
			if strings.EqualFold(s.Name, u.name) {
				srcs = append(srcs, s.Source())
			}
		}
		if len(srcs) > 0 {
			fmt.Fprintf(&b, "; available from %s: execute skills.read {\"name\":%q}", strings.Join(srcs, ", "), u.name)
		} else {
			b.WriteString("; not found in the skill folders Deck knows")
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}
