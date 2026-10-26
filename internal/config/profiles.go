package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	deckconfig "github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/profile"
	"github.com/dev-dami/deck/internal/fsext"
)

// profilesKey is the deck.json key that holds the named agents.
const profilesKey = "profiles"

// profileSources are the deck.json files profiles are read from for a
// project at cwd: the global one, and the project's JSON configs from the
// lowest priority to the highest, as lookupConfigs finds them, then the
// workspace's .deck/deck.json. Shell configs (deckrc) are not run for
// profiles.
func profileSources(cwd string) (global string, project []string) {
	global = GlobalConfig()
	seen := map[string]bool{absOr(global): true}
	add := func(p string) {
		if a := absOr(p); !seen[a] {
			seen[a] = true
			project = append(project, p)
		}
	}
	boundary := projectBoundary(cwd)
	if found, err := fsext.LookupBounded(cwd, boundary, "."+appName+".json", appName+".json"); err == nil {
		slices.Reverse(found)
		for _, p := range found {
			add(p)
		}
	}
	add(projectProfilesPath(cwd))
	return global, project
}

// projectProfilesPath is where a new project profile is written: the
// workspace config, deck.json in the project's .deck folder (the closest
// one, else one in cwd).
func projectProfilesPath(cwd string) string {
	dir := filepath.Join(cwd, defaultDataDirectory)
	if p, ok := fsext.LookupClosestBounded(cwd, projectBoundary(cwd), defaultDataDirectory); ok {
		dir = p
	}
	return filepath.Join(dir, appName+".json")
}

func absOr(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// LoadProfiles reads the named agents for the project at cwd: the global
// deck.json's profiles with the project's laid over them by name. Each
// carries its scope. A profile that breaks the rules (profile.Validate, with
// the agent kinds and the teammates given) is left out, and so is a file
// that cannot be read; warnings say why, for deck doctor.
func LoadProfiles(cwd string, teammates []string) (ps []profile.Profile, warnings []string) {
	global, project := profileSources(cwd)
	g, warn := readProfiles(global, profile.ScopeGlobal, teammates)
	warnings = append(warnings, warn...)
	var proj []profile.Profile
	for _, path := range project {
		p, warn := readProfiles(path, profile.ScopeProject, teammates)
		warnings = append(warnings, warn...)
		proj = profile.Merge(proj, p)
	}
	return profile.Merge(g, proj), warnings
}

// readProfiles reads the valid profiles of one deck.json, tagged with
// scope. A missing file has none.
func readProfiles(path, scope string, teammates []string) (out []profile.Profile, warnings []string) {
	data, err := readFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			warnings = append(warnings, fmt.Sprintf("%s: %v", path, err))
		}
		return nil, warnings
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	if !json.Valid(data) {
		return nil, []string{path + ": invalid JSON; its profiles are skipped"}
	}
	list := gjson.GetBytes(data, profilesKey)
	if !list.Exists() {
		return nil, nil
	}
	if !list.IsArray() {
		return nil, []string{path + ": \"profiles\" is not a list"}
	}
	for i, raw := range list.Array() {
		var p profile.Profile
		if err := json.Unmarshal([]byte(raw.Raw), &p); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: profile %d is skipped: %v", path, i+1, err))
			continue
		}
		p.Name, p.Kind = strings.TrimSpace(p.Name), strings.TrimSpace(p.Kind)
		if err := profile.Validate(p, deckconfig.Kinds, teammates); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: profile %d is skipped: %v", path, i+1, err))
			continue
		}
		if _, dup := profile.Find(out, p.Name); dup {
			warnings = append(warnings, fmt.Sprintf("%s: profile %q is listed twice; the later one is used", path, p.Name))
		}
		p.Scope = scope
		out = profile.Merge(out, []profile.Profile{p})
	}
	return out, warnings
}

// SaveProfile validates p and writes it to the deck.json of its scope,
// replacing the profile of the same name there. A project profile goes to
// the project file that already holds it, else to the workspace's
// .deck/deck.json. The file's other keys are left as they are.
func SaveProfile(cwd string, p profile.Profile, teammates []string) error {
	p.Name, p.Kind = strings.TrimSpace(p.Name), strings.TrimSpace(p.Kind)
	if p.Scope == "" {
		return fmt.Errorf("%w: %q needs a scope (global or project)", profile.ErrInvalid, p.Name)
	}
	if err := profile.Validate(p, deckconfig.Kinds, teammates); err != nil {
		return err
	}
	global, project := profileSources(cwd)
	path := global
	if p.Scope == profile.ScopeProject {
		path = projectProfilesPath(cwd)
		for _, f := range project {
			if holds(f, p.Name) {
				path = f
			}
		}
	}
	entry, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return editProfiles(path, func(list []json.RawMessage) []json.RawMessage {
		for i, raw := range list {
			if strings.EqualFold(gjson.GetBytes(raw, "name").String(), p.Name) {
				list[i] = entry
				return list
			}
		}
		return append(list, entry)
	})
}

// DeleteProfile removes the profile called name from every deck.json of
// scope that holds it; profile.ErrNotFound when none does.
func DeleteProfile(cwd, name, scope string) error {
	global, project := profileSources(cwd)
	var paths []string
	switch scope {
	case profile.ScopeGlobal:
		paths = []string{global}
	case profile.ScopeProject:
		paths = project
	default:
		return fmt.Errorf("no scope %q (global or project)", scope)
	}
	found := false
	for _, path := range paths {
		if !holds(path, name) {
			continue
		}
		found = true
		err := editProfiles(path, func(list []json.RawMessage) []json.RawMessage {
			return slices.DeleteFunc(list, func(raw json.RawMessage) bool {
				return strings.EqualFold(gjson.GetBytes(raw, "name").String(), name)
			})
		})
		if err != nil {
			return err
		}
	}
	if !found {
		return fmt.Errorf("%w %q in the %s deck.json", profile.ErrNotFound, name, scope)
	}
	return nil
}

// holds reports whether the deck.json at path lists a profile called name.
func holds(path, name string) bool {
	data, err := readFile(path)
	if err != nil || !json.Valid(data) {
		return false
	}
	for _, raw := range gjson.GetBytes(data, profilesKey).Array() {
		if strings.EqualFold(raw.Get("name").String(), name) {
			return true
		}
	}
	return false
}

// editProfiles rewrites the profiles list of the deck.json at path with
// edit, creating the file (and its folder) when it is missing.
func editProfiles(path string, edit func([]json.RawMessage) []json.RawMessage) error {
	data, err := readFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		data = []byte("{}")
	case err != nil:
		return err
	case len(strings.TrimSpace(string(data))) == 0:
		data = []byte("{}")
	case !json.Valid(data):
		return fmt.Errorf("%s is not valid JSON; fix it before saving profiles there", path)
	}
	var list []json.RawMessage
	if v := gjson.GetBytes(data, profilesKey); v.IsArray() {
		for _, raw := range v.Array() {
			list = append(list, json.RawMessage(raw.Raw))
		}
	}
	list = edit(list)
	if list == nil {
		list = []json.RawMessage{}
	}
	raw, err := json.MarshalIndent(list, "  ", "  ")
	if err != nil {
		return err
	}
	out, err := sjson.SetRawBytes(data, profilesKey, raw)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return atomicWriteFile(path, out, 0o600)
}
