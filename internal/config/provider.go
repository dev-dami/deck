package config

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/charmbracelet/x/etag"
)

type syncer[T any] interface {
	Get(context.Context) (T, error)
}

var (
	providerOnce sync.Once
	providerList []catwalk.Provider
	providerErr  error
)

// file to cache provider data. It resolves through GlobalConfigData so the
// catalog follows DECK_GLOBAL_DATA like the rest of the data directory.
func cachePathFor(name string) string {
	return filepath.Join(filepath.Dir(GlobalConfigData()), name+".json")
}

var (
	catwalkSyncer = &catwalkSync{}
)

// CatwalkUpdated reports whether the Catwalk provider catalog was
// refreshed with new data during this run's catalog fetch. The fetch
// runs once per process, before the first call to Providers returns, so
// the result is stable afterwards. Callers use it to renew catalogs that
// shadow Catwalk's, such as the ChatGPT model catalog.
func CatwalkUpdated() bool {
	return catwalkSyncer.Updated()
}

// Providers returns the list of providers, taking into account cached results
// and whether or not auto update is enabled.
//
// It will:
// 1. if auto update is disabled, it'll return the embedded providers at the
// time of release.
// 2. load the cached providers
// 3. try to get the fresh list of providers, and return either this new list,
// the cached list, or the embedded list if all others fail.
//
// A returned error is advisory: it reports that the catalog could not be
// cached, or that an upstream returned nothing usable. It never means that no
// providers are available, so callers should surface it as a warning and keep
// using the returned list. A refresh that simply could not reach the network
// is not an error at all: the cached or embedded catalog is a sound answer, so
// those are logged and the fallback is returned.
func Providers(*Config, ...func(context.Context) error) ([]catwalk.Provider, error) {
	// Deck's agents are the user's own CLIs, logged in on their own: there is
	// no model provider catalogue to fetch, and nothing is downloaded.
	return nil, nil
}

// UpdateProviderInList replaces a provider in the memoized provider list
// returned by Providers(). This is used after re-fetching a single
// provider (e.g. Hyper after OAuth) so that all callers of Providers()
// see the updated entry without needing to reset sync.Once.
func UpdateProviderInList(provider catwalk.Provider) {
	for i, p := range providerList {
		if p.ID == provider.ID {
			providerList[i] = provider
			return
		}
	}
	// Provider not found in list; prepend it.
	providerList = append([]catwalk.Provider{provider}, providerList...)
}

type cache[T any] struct {
	path string
}

func newCache[T any](path string) cache[T] {
	return cache[T]{path: path}
}

func (c cache[T]) Get() (T, string, error) {
	var v T
	data, err := readFile(c.path)
	if err != nil {
		return v, "", fmt.Errorf("failed to read provider cache file: %w", err)
	}

	if err := json.Unmarshal(data, &v); err != nil {
		return v, "", fmt.Errorf("failed to unmarshal provider data from cache: %w", err)
	}

	return v, etag.Of(data), nil
}

func (c cache[T]) Store(v T) error {
	slog.Info("Saving provider data to disk", "path", c.path)
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return fmt.Errorf("failed to create directory for provider cache: %w", err)
	}

	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("failed to marshal provider data: %w", err)
	}

	// Written through a temporary file and renamed into place. Several Deck
	// instances start independently and race to refresh this cache, and a
	// truncating write would let one of them read a half-written catalog and
	// silently fall back to the bundled copy.
	if err := atomicWriteFile(c.path, data, 0o644); err != nil {
		return fmt.Errorf("failed to write provider data to cache: %w", err)
	}
	return nil
}
