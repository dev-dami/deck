package workspace

import (
	appconfig "github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/deck/profile"
)

// Profile is a named agent (deck.json "profiles"): see package profile.
type Profile = profile.Profile

// Profiles reads and edits the named agents of the project: the global
// deck.json's with the project's laid over them by name. The UI and the
// RPC type-assert their Workspace to it. Every call reads or writes a
// file: call them off the UI loop.
type Profiles interface {
	// ListProfiles is the merged list, each with its Scope. Invalid entries
	// are left out (deck doctor says why).
	ListProfiles() []Profile
	// SaveProfile validates p (profile.Validate, with the agent kinds and
	// the configured teammates) and writes it to its Scope's deck.json,
	// replacing the one of the same name there.
	SaveProfile(p Profile) error
	// DeleteProfile removes the profile called name from scope's deck.json;
	// an error wrapping profile.ErrNotFound when that scope has none.
	DeleteProfile(name, scope string) error
}

var _ Profiles = (*DeckWorkspace)(nil)

// ListProfiles implements [Profiles].
func (w *DeckWorkspace) ListProfiles() []Profile {
	ps, _ := appconfig.LoadProfiles(w.WorkingDir(), w.DeckSettings().Teammates())
	return ps
}

// SaveProfile implements [Profiles].
func (w *DeckWorkspace) SaveProfile(p Profile) error {
	return appconfig.SaveProfile(w.WorkingDir(), p, w.DeckSettings().Teammates())
}

// DeleteProfile implements [Profiles].
func (w *DeckWorkspace) DeleteProfile(name, scope string) error {
	return appconfig.DeleteProfile(w.WorkingDir(), name, scope)
}
