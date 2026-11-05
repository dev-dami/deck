package session

import (
	"cmp"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// The model catalog: what each agent CLI offers, kept per kind. Listing
// models spawns the CLI, which can take seconds or fail, so the catalog
// answers from what it holds at once and loads in the background: every
// kind in parallel, each bounded by its own timeout. A kind that failed is
// not asked again until its back-off ends, which doubles with each failure.
// A list that is older than catalogTTL is served still while it reloads.

const (
	defaultCatalogTimeout = 20 * time.Second
	defaultCatalogBackoff = 30 * time.Second
	catalogBackoffMax     = 10 * time.Minute
	catalogTTL            = 24 * time.Hour
)

// CatalogState is where a kind's model list stands.
type CatalogState string

const (
	CatalogNone    CatalogState = ""        // never asked
	CatalogLoading CatalogState = "loading" // a load runs; Models is the old list, if any
	CatalogFresh   CatalogState = "fresh"   // the CLI answered
	CatalogFailed  CatalogState = "failed"  // the last load failed; Models is what was kept, if any
)

// KindCatalog is one kind's entry in the catalog.
type KindCatalog struct {
	Kind    string
	Models  []agent.ModelInfo
	Fetched time.Time // when the CLI last answered; zero if never
	State   CatalogState
	Err     string // why the last load failed
}

// kindCatalog is a kind's entry as the Manager keeps it; guarded by catMu.
type kindCatalog struct {
	models   []agent.ModelInfo
	fetched  time.Time
	err      error
	failedAt time.Time
	fails    int
	// loading is closed when the load under way ends; nil when none runs.
	loading chan struct{}
}

// catalog is the Manager's model catalog.
type catalog struct {
	mu    sync.Mutex
	kinds map[string]*kindCatalog
	// timeout bounds one CLI's answer and backoff is the first wait before
	// a failed CLI is asked again (tests shorten both).
	timeout, backoff time.Duration
}

// Catalog answers at once with the model lists of kinds (every kind when
// none is given), as far as they are loaded. It asks no CLI.
func (m *Manager) Catalog(kinds ...string) []KindCatalog {
	kinds = m.catalogKinds(kinds)
	out := make([]KindCatalog, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, m.kindCatalog(k))
	}
	return out
}

// LoadCatalog starts a background load of each of kinds (every kind when
// none is given) that is not loaded yet, stale, or past its back-off. It
// does not wait.
func (m *Manager) LoadCatalog(kinds ...string) {
	for _, k := range m.catalogKinds(kinds) {
		m.loadKind(k, false)
	}
}

// RefreshCatalog reloads the model lists of kinds (every kind when none is
// given) in the background, whatever their state and back-off. Their old
// lists are served until the new ones arrive.
func (m *Manager) RefreshCatalog(kinds ...string) {
	for _, k := range m.catalogKinds(kinds) {
		m.loadKind(k, true)
	}
}

// WaitCatalog starts loads as LoadCatalog does and waits until each has ended or
// ctx is done. It never waits on a kind in back-off: that one answers with
// what it kept.
func (m *Manager) WaitCatalog(ctx context.Context, kinds ...string) []KindCatalog {
	kinds = m.catalogKinds(kinds)
	var waits []chan struct{}
	for _, k := range kinds {
		if ch := m.loadKind(k, false); ch != nil {
			waits = append(waits, ch)
		}
	}
	for _, ch := range waits {
		select {
		case <-ch:
		case <-ctx.Done():
		}
	}
	out := make([]KindCatalog, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, m.kindCatalog(k))
	}
	return out
}

// Models lists the models the kind's CLI offers, through its adapter when it
// implements agent.ModelLister. It waits for a load when nothing is kept yet
// (bounded by ctx and the catalog's timeout); a kind that failed answers its
// error until its back-off ends. An adapter that cannot list models yields
// nil, nil.
func (m *Manager) Models(ctx context.Context, kind string) ([]agent.ModelInfo, error) {
	if _, ok := m.adapters[kind]; !ok {
		return nil, fmt.Errorf("session: unknown agent kind %q", kind)
	}
	if ch := m.loadKind(kind, false); ch != nil && m.kindCatalog(kind).Models == nil {
		select {
		case <-ch:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	m.cat.mu.Lock()
	e := m.cat.kinds[kind]
	var list []agent.ModelInfo
	var err error
	if e != nil {
		list, err = e.models, e.err
	}
	m.cat.mu.Unlock()
	if list == nil && err != nil {
		return nil, err
	}
	if list == nil {
		return nil, nil
	}
	return m.withSlugs(kind, list), nil
}

// cachedModels is the kind's list as kept, without loading; nil when there
// is none. The caller must not change it.
func (m *Manager) cachedModels(kind string) []agent.ModelInfo {
	m.cat.mu.Lock()
	defer m.cat.mu.Unlock()
	if e := m.cat.kinds[kind]; e != nil {
		return e.models
	}
	return nil
}

// listedModel is model's entry in kind's kept list ("" is the CLI's
// default); false when the list is not loaded or does not name it.
func (m *Manager) listedModel(kind, model string) (agent.ModelInfo, bool) {
	for _, mi := range m.cachedModels(kind) {
		if mi.ID == model || (model == "" && mi.Default) {
			return mi, true
		}
	}
	return agent.ModelInfo{}, false
}

// ForgetModels drops every kind's model list and remembered failure, here
// and in each adapter that keeps one (agent.ModelForgetter), so the next
// Models asks the CLIs again. A load under way finishes, but its result is
// not kept.
func (m *Manager) ForgetModels() {
	m.cat.mu.Lock()
	m.cat.kinds = nil
	m.cat.mu.Unlock()
	for _, ad := range m.adapters {
		if f, ok := ad.(agent.ModelForgetter); ok {
			f.ForgetModels()
		}
	}
}

func (m *Manager) catalogKinds(kinds []string) []string {
	if len(kinds) == 0 {
		return m.Kinds()
	}
	var out []string
	for _, k := range kinds {
		if _, ok := m.adapters[k]; ok {
			out = append(out, k)
		}
	}
	return out
}

// kindCatalog is a copy of kind's entry.
func (m *Manager) kindCatalog(kind string) KindCatalog {
	m.cat.mu.Lock()
	defer m.cat.mu.Unlock()
	out := KindCatalog{Kind: kind}
	e := m.cat.kinds[kind]
	if e == nil {
		return out
	}
	out.Models = m.withSlugs(kind, e.models)
	out.Fetched = e.fetched
	switch {
	case e.loading != nil:
		out.State = CatalogLoading
	case e.err != nil:
		out.State = CatalogFailed
	case !e.fetched.IsZero():
		out.State = CatalogFresh
	}
	if e.err != nil {
		out.Err = e.err.Error()
	}
	return out
}

// loadKind starts a load of kind's models unless one runs already, the list
// is fresh, or a failure is in back-off; force skips the last two checks. It
// returns the channel closed when the load under way ends, nil when none
// runs.
func (m *Manager) loadKind(kind string, force bool) chan struct{} {
	ad := m.adapters[kind]
	ml, ok := ad.(agent.ModelLister)
	now := time.Now()
	m.cat.mu.Lock()
	defer m.cat.mu.Unlock()
	if m.cat.kinds == nil {
		m.cat.kinds = map[string]*kindCatalog{}
	}
	e := m.cat.kinds[kind]
	if e == nil {
		e = &kindCatalog{}
		m.cat.kinds[kind] = e
	}
	switch {
	case e.loading != nil:
		return e.loading
	case !ok:
		// Nothing to ask: an empty list, known at once.
		if e.fetched.IsZero() {
			e.fetched = now
		}
		return nil
	case force:
	case e.err != nil && now.Sub(e.failedAt) < m.backoff(e.fails):
		return nil
	case e.err == nil && !e.fetched.IsZero() && now.Sub(e.fetched) < catalogTTL:
		return nil
	}
	ch := make(chan struct{})
	e.loading = ch
	timeout := cmp.Or(m.cat.timeout, defaultCatalogTimeout)
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, timeout)
		list, err := ml.Models(ctx)
		cancel()
		m.cat.mu.Lock()
		// ForgetModels during the load disowned it.
		if cur := m.cat.kinds[kind]; cur == e {
			if len(list) > 0 {
				e.models = append([]agent.ModelInfo(nil), list...)
			}
			if err != nil {
				e.err, e.failedAt = err, time.Now()
				e.fails++
			} else {
				e.err, e.fails, e.fetched = nil, 0, time.Now()
				if list == nil {
					e.models = nil
				}
			}
		}
		e.loading = nil
		m.cat.mu.Unlock()
		close(ch)
	}()
	return ch
}

// backoff is how long a kind that failed fails times in a row waits before
// it is asked again.
func (m *Manager) backoff(fails int) time.Duration {
	d := m.cat.backoff
	if d == 0 {
		d = defaultCatalogBackoff
	}
	if d < 0 {
		return 0
	}
	for i := 1; i < fails && d < catalogBackoffMax; i++ {
		d *= 2
	}
	return min(d, catalogBackoffMax)
}

// withSlugs is a copy of list with each model's Slug, what it ran as; ""
// when no run has said (its ID is then the best name there is).
func (m *Manager) withSlugs(kind string, list []agent.ModelInfo) []agent.ModelInfo {
	if list == nil {
		return nil
	}
	out := append([]agent.ModelInfo(nil), list...)
	for i := range out {
		out[i].Slug = cmp.Or(m.ModelSlug(kind, out[i].ID), out[i].Slug)
	}
	return out
}
