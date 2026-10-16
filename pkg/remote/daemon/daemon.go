// Package daemon is the always-on side of Deck Mobile: it holds the
// computer's identity, accepts phones over the Noise channel, routes their
// requests to the provider that owns each project, and aggregates the inbox
// (docs/mobile-contract.md). It never imports internal/workspace; providers
// reach it through pkg/remote/ipc.
package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/devstore"
	"github.com/dev-dami/deck/pkg/remote/identity"
	"github.com/dev-dami/deck/pkg/remote/projects"
	"github.com/dev-dami/deck/pkg/remote/queue"
	"github.com/dev-dami/deck/pkg/remote/server"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// Gate decides what needs more than the Noise session: approvals the phone may
// not give on its own, and yolo (contract §7). The daemon fails closed until a
// real gate (policy classifier and WebAuthn verification) is configured.
type Gate interface {
	// Tier classifies an approval as wire.TierNormal or wire.TierDestructive.
	Tier(pj string, a wire.Approval) string
	// AllowAnswer returns nil when dev may answer with req, or the error the
	// phone should see (assertion_required, assertion_invalid, forbidden).
	AllowAnswer(ctx context.Context, dev devstore.Device, a wire.ApprovalV3, req wire.ApprovalsAnswer3Params) *wire.Error
	// AllowYolo is the same for mode.set yolo.
	AllowYolo(ctx context.Context, dev devstore.Device, pj string, req wire.ModeSet3Params) *wire.Error
}

// Config is what a Daemon needs.
type Config struct {
	Identity *identity.Identity
	Devices  *devstore.Store
	Projects *projects.Registry
	// Name is the computer's name as the phone shows it.
	Name    string
	Version string
	// Gate defaults to a gate that lets the phone deny and read but not allow.
	Gate Gate
	// Now defaults to time.Now.
	Now func() time.Time
	// Relay lets pairing open its window and refresh the device list on the
	// relay; nil for a daemon with no relay.
	Relay RelayControl
	// Queue is the durable offline queue; nil disables it.
	Queue *queue.Store
	// QueueSettle is how long the mailbox must be quiet before queued actions
	// run; default 2 s.
	QueueSettle time.Duration
	// Veto runs before a batch of queued actions and may cancel some. The
	// default tells a terminal UI that is open for the project and waits
	// VetoWindow, during which 'deck mobile queue cancel' works.
	Veto Vetoer
	// VetoWindow overrides wire.QueueVetoWindow.
	VetoWindow time.Duration
	// Snapshots receives encrypted snapshots for the relay; nil disables them.
	Snapshots SnapshotSink
	// SnapshotDebounce is how long changes settle before a snapshot is
	// written; default 10 s.
	SnapshotDebounce time.Duration
	// Pusher delivers encrypted web pushes through the relay; nil disables push.
	Pusher Pusher
	// Spawn starts a headless Deck for a project that has no provider, so
	// queued work and phone requests have something to run on. It returns at
	// once; the provider attaches when it is up. Nil disables it.
	Spawn func(pj string) error
	// OnPaired runs after a phone is paired, to announce it where the code
	// was shown (remote-contract R3).
	OnPaired func(devstore.Device)
}

// Daemon is the router. Create it with New.
type Daemon struct {
	cfg Config
	hub *hub

	mu        sync.Mutex
	providers map[string]*provider
	acks      map[string]time.Time // device|pj|session -> last ack
	refresh   *time.Timer
	slot      *slot
	dirty     chan struct{}
	spawned   map[string]time.Time
	live      map[string]int // device id -> open sessions
}

type provider struct {
	be     remote.Backend
	call   *server.ProjectCaller
	cancel context.CancelFunc
}

// New returns a daemon with no providers attached.
func New(cfg Config) *Daemon {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Gate == nil {
		cfg.Gate = failClosedGate{}
	}
	d := &Daemon{cfg: cfg, hub: newHub(), providers: map[string]*provider{}, acks: map[string]time.Time{}, dirty: make(chan struct{}, 1), spawned: map[string]time.Time{}, live: map[string]int{}}
	if d.cfg.Veto == nil {
		d.cfg.Veto = d.reviewWindow
	}
	return d
}

// Attach makes be the provider of project pj and starts relaying its events.
// A second Attach for the same project replaces the first.
func (d *Daemon) Attach(pj string, be remote.Backend) {
	ctx, cancel := context.WithCancel(context.Background())
	p := &provider{be: be, call: server.NewProjectCaller(be), cancel: cancel}
	d.mu.Lock()
	if old := d.providers[pj]; old != nil {
		old.cancel()
	}
	d.providers[pj] = p
	d.mu.Unlock()

	d.hub.publish(event{name: wire.EvInboxChanged, payload: d.inboxFor(context.Background(), devstore.Device{})})
	go func() {
		for ev := range be.Events(ctx) {
			d.hub.publish(event{pj: pj, name: ev.Name, sessionID: ev.SessionID, scoped: ev.Scoped, payload: ev.Payload})
			d.onProjectEvent(ctx, pj, ev.Name, ev.Payload)
			if affectsInbox(ev.Name) {
				d.scheduleInbox()
			}
		}
	}()
}

// Detach removes the provider of pj if it is still be.
func (d *Daemon) Detach(pj string, be remote.Backend) {
	d.mu.Lock()
	if p := d.providers[pj]; p != nil && p.be == be {
		p.cancel()
		delete(d.providers, pj)
	}
	d.mu.Unlock()
	d.scheduleInbox()
}

func affectsInbox(name string) bool {
	switch name {
	case wire.EvSessionCreated, wire.EvSessionUpdated, wire.EvSessionDeleted,
		wire.EvApprovalRequested, wire.EvApprovalResolved, wire.EvBusy:
		return true
	}
	return false
}

// scheduleInbox publishes inbox.changed once the burst of events that caused
// it has settled.
func (d *Daemon) scheduleInbox() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.refresh != nil {
		return
	}
	d.refresh = time.AfterFunc(150*time.Millisecond, func() {
		d.mu.Lock()
		d.refresh = nil
		d.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// Unread is per device, so the broadcast carries none; a phone asks
		// inbox.get for it.
		d.hub.publish(event{name: wire.EvInboxChanged, payload: d.inboxFor(ctx, devstore.Device{})})
		d.snapshotDirty()
	})
}

func (d *Daemon) provider(pj string) *provider {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.providers[pj]
}

func (d *Daemon) online(pj string) bool { return d.provider(pj) != nil }

// Online reports whether a project has a running provider.
func (d *Daemon) Online(pj string) bool { return d.online(pj) }

// projectRefs lists the enabled projects with their online state.
func (d *Daemon) projectRefs() []wire.ProjectRef { return d.cfg.Projects.Refs(d.online) }

func (d *Daemon) kinds() []string {
	var out []string
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, p := range d.providers {
		for _, k := range p.be.Info().Kinds {
			if !slices.Contains(out, k) {
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	if out == nil {
		out = []string{}
	}
	return out
}

func (d *Daemon) theme() wire.Theme {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, p := range d.providers {
		return p.be.Info().Theme
	}
	return wire.Theme{Palette: map[string]string{}, Kinds: map[string]wire.KindTone{}}
}

// Inbox is the aggregate for one device: Needs you, then Running, then Done,
// newest first inside each.
func (d *Daemon) Inbox(ctx context.Context, dev devstore.Device) []wire.InboxItem {
	return d.inboxFor(ctx, dev)
}

func (d *Daemon) inboxFor(ctx context.Context, dev devstore.Device) []wire.InboxItem {
	out := []wire.InboxItem{}
	for _, ref := range d.projectRefs() {
		p := d.provider(ref.ID)
		if p == nil {
			continue
		}
		sessions, err := p.be.Sessions(ctx)
		if err != nil {
			slog.Warn("remote: inbox: sessions", "project", ref.ID, "error", err)
			continue
		}
		pending := map[string]int{}
		if apprs, err := p.be.Approvals(""); err == nil {
			for _, a := range apprs {
				pending[a.SessionID]++
			}
		}
		for _, s := range sessions {
			n := max(pending[s.ID], s.Pending)
			item := wire.InboxItem{
				PJ: ref.ID, SessionID: s.ID, Title: s.Title, State: wire.InboxDone,
				Approvals: n, LastActivity: s.UpdatedAt, Summary: summarize(s.LastText),
			}
			switch {
			case n > 0:
				item.State = wire.InboxNeedsYou
			case s.Busy:
				item.State = wire.InboxRunning
			}
			if !dev.CreatedAt.IsZero() {
				seen := d.ackTime(dev, ref.ID, s.ID)
				item.Unread = s.UpdatedAt.After(seen)
			}
			out = append(out, item)
		}
	}
	rank := map[string]int{wire.InboxNeedsYou: 0, wire.InboxRunning: 1, wire.InboxDone: 2}
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].State] != rank[out[j].State] {
			return rank[out[i].State] < rank[out[j].State]
		}
		return out[i].LastActivity.After(out[j].LastActivity)
	})
	return out
}

func summarize(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= wire.MaxInboxSummaryRunes {
		return s
	}
	return string([]rune(s)[:wire.MaxInboxSummaryRunes-1]) + "…"
}

func ackKey(dev devstore.Device, pj, session string) string {
	return dev.ID + "|" + pj + "|" + session
}

// ackTime is when dev last acknowledged the session; before any ack,
// whatever happened before the phone paired counts as seen.
func (d *Daemon) ackTime(dev devstore.Device, pj, session string) time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	if t, ok := d.acks[ackKey(dev, pj, session)]; ok {
		return t
	}
	return dev.CreatedAt
}

func (d *Daemon) ack(dev devstore.Device, pj, session string) {
	d.mu.Lock()
	d.acks[ackKey(dev, pj, session)] = d.cfg.Now()
	d.mu.Unlock()
}

// Digest is the hex sha256 of what a person approves: kind, command, path
// and diff. A queued approval binds to it (contract §6).
func Digest(a wire.Approval) string {
	h := sha256.New()
	for _, part := range []string{a.Kind, a.Command, a.Path, a.Diff} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (d *Daemon) v3Approval(pj string, a wire.Approval) wire.ApprovalV3 {
	return wire.ApprovalV3{Approval: a, Tier: d.cfg.Gate.Tier(pj, a), Digest: Digest(a)}
}

// failClosedGate is the default: everything counts as destructive and nothing
// can be verified, so the phone may deny but never allow, and never set yolo.
type failClosedGate struct{}

func (failClosedGate) Tier(string, wire.Approval) string { return wire.TierDestructive }

func (failClosedGate) AllowAnswer(_ context.Context, _ devstore.Device, _ wire.ApprovalV3, req wire.ApprovalsAnswer3Params) *wire.Error {
	if req.Decision == wire.DecisionDeny {
		return nil
	}
	return &wire.Error{Code: wire.CodeAssertionRequired, Msg: "Allowing from the phone is not set up on this computer yet."}
}

func (failClosedGate) AllowYolo(_ context.Context, dev devstore.Device, _ string, _ wire.ModeSet3Params) *wire.Error {
	if !dev.AllowYolo {
		return &wire.Error{Code: wire.CodeYoloDisabled, Msg: "Yolo is not allowed for this phone."}
	}
	return &wire.Error{Code: wire.CodeAssertionRequired, Msg: "Yolo needs your fingerprint or face, which is not set up yet."}
}

// SetRelay connects the daemon to its relay client after both exist (the
// client needs the daemon's handlers, the daemon needs the client).
func (d *Daemon) SetRelay(r RelayControl, s SnapshotSink) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cfg.Relay, d.cfg.Snapshots = r, s
	if p, ok := r.(Pusher); ok {
		d.cfg.Pusher = p
	}
}

// spawnInterval is how long to leave a project alone after trying to start
// it, so a Deck that fails to start is not relaunched in a loop.
const spawnInterval = 30 * time.Second

// ensureProvider asks for a headless Deck for pj if none is running. It
// reports whether a provider is already there.
func (d *Daemon) ensureProvider(pj string) bool {
	if d.provider(pj) != nil {
		return true
	}
	if d.cfg.Spawn == nil {
		return false
	}
	if _, err := d.cfg.Projects.Get(pj); err != nil {
		return false
	}
	d.mu.Lock()
	last := d.spawned[pj]
	if d.cfg.Now().Sub(last) < spawnInterval {
		d.mu.Unlock()
		return false
	}
	d.spawned[pj] = d.cfg.Now()
	d.mu.Unlock()
	if err := d.cfg.Spawn(pj); err != nil {
		slog.Warn("remote: could not start a Deck for the project", "project", pj, "error", err)
	}
	return false
}
