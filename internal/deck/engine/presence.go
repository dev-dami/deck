package engine

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/dev-dami/deck/internal/deck/changes"
	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/deck/store"
)

// localWork is this Deck's own work as peer.status tells teammates of it
// (peer.Local).
type localWork struct {
	mgr *session.Manager

	mu    sync.Mutex
	roots map[string]string // a thread's directory -> its repository's root commit; "" outside git
}

// root is the root commit of the repository dir is in, "" when there is none.
// It is read once per directory.
func (w *localWork) root(dir string) string {
	w.mu.Lock()
	r, ok := w.roots[dir]
	w.mu.Unlock()
	if ok {
		return r
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if repo, err := changes.Open(ctx, dir); err == nil {
		r, _ = repo.RootCommit(ctx)
	}
	w.mu.Lock()
	if w.roots == nil {
		w.roots = map[string]string{}
	}
	w.roots[dir] = r
	w.mu.Unlock()
	return r
}

// Repos implements peer.Local.
func (w *localWork) Repos() []string {
	seen := map[string]bool{}
	var out []string
	for _, th := range w.mgr.Threads() {
		if r := w.root(th.Dir); r != "" && !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

// Presence implements peer.Local.
func (w *localWork) Presence(asker string, repos []string, limits bool) peer.Presence {
	want := map[string]bool{}
	for _, r := range repos {
		want[r] = true
	}
	var p peer.Presence
	for _, st := range w.mgr.SharedWith(asker) {
		p.Threads = append(p.Threads, sharedThread(st))
	}
	for _, th := range w.mgr.Threads() {
		running := map[string]string{} // owner -> the title of the task it runs
		for _, tk := range w.mgr.Tasks(th.ID) {
			if tk.Status == store.TaskRunning && tk.Owner != "" {
				running[tk.Owner] = tk.Title
			}
		}
		names := map[string]string{}
		for _, a := range th.Members {
			names[a.ID] = a.Name
			if a.Status == session.StatusExited {
				continue
			}
			ap := peer.AgentPresence{Name: a.Name, Kind: a.Kind, Status: string(a.Status), Task: running[a.ID]}
			if a.ContextWindow > 0 {
				ap.ContextPct = a.ContextTokens * 100 / a.ContextWindow
			}
			p.Agents = append(p.Agents, ap)
		}
		if len(want) == 0 {
			continue
		}
		if root := w.root(th.Dir); want[root] {
			for _, l := range w.mgr.Leases(th.ID) {
				p.Leases = append(p.Leases, peer.LeasePresence{Repo: root, Agent: names[l.Member], Paths: l.Paths, Exclusive: l.Exclusive, Until: l.Until})
			}
		}
	}
	if limits {
		now := time.Now()
		for kind, l := range w.mgr.PlanLimits() {
			for _, win := range l.Windows {
				if win.ResetsAt.IsZero() || win.ResetsAt.After(now) {
					p.Limits = append(p.Limits, peer.LimitPresence{Kind: kind, Window: win.Label, UsedPct: win.UsedPct})
				}
			}
		}
		sort.Slice(p.Limits, func(i, j int) bool {
			if p.Limits[i].Kind != p.Limits[j].Kind {
				return p.Limits[i].Kind < p.Limits[j].Kind
			}
			return p.Limits[i].Window < p.Limits[j].Window
		})
	}
	return p
}
