package peer

import (
	"context"
	"fmt"
	"time"

	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/version"
)

// ProbeResult is what one quick check of a peer found.
type ProbeResult struct {
	Latency time.Duration // MCP ping round trip
	Server  map[string]any
	Tools   []string
	Status  string // the peer.status text
}

// Probe connects to a pinned peer, runs initialize, tools/list, ping and
// peer.status, and disconnects. It is what `deck peer ping`, `deck peer
// list` and doctor use. Everything in the result and in an error may come
// from the peer: Sanitize it before printing.
func Probe(ctx context.Context, id *Identity, pin Pinned, self string, timeout time.Duration) (ProbeResult, error) {
	var res ProbeResult
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := Dial(ctx, DialOptions{
		Identity: id, Peer: pin, Name: self, Version: version.Version,
		DialTimeout: timeout, Limits: Limits{HandshakeTimeout: timeout},
	})
	if err != nil {
		return res, err
	}
	defer conn.Close()
	cl, err := conn.OpenMCP(ctx)
	if err != nil {
		return res, err
	}
	defer cl.Close()
	if res.Server, err = cl.Initialize(ctx); err != nil {
		return res, fmt.Errorf("initialize: %w", err)
	}
	tools, err := cl.ListTools(ctx)
	if err != nil {
		return res, fmt.Errorf("tools/list: %w", err)
	}
	for _, t := range tools {
		res.Tools = append(res.Tools, t.Name)
	}
	if res.Latency, err = cl.Ping(ctx); err != nil {
		return res, fmt.Errorf("ping: %w", err)
	}
	if r, err := cl.Execute(ctx, OpPeerStatus, nil); err == nil {
		res.Status = r.Text()
	}
	return res, nil
}

// AddRemote records a newly paired peer in the config file at path, unless
// one with that name is there already, so every pinned peer has an explicit
// row: dialed when addr is set, with role (observer when empty) and no
// allow_inbound, which means DefaultAllowInbound. It does not turn peers on,
// and touches nothing outside the peers section. added reports whether the
// file changed.
func AddRemote(path, name, addr, role string) (added bool, err error) {
	if role == "" {
		role = config.PeerRoleObserver
	}
	return config.UpdatePeers(path, func(p *config.Peers) bool {
		if _, ok := p.Find(name); ok {
			return false
		}
		p.Remote = append(p.Remote, config.PeerRemote{Name: name, Addr: addr, Dial: addr != "", Role: role})
		return true
	})
}

// SetRemote adds r to the config file's remotes, or updates the row of the
// same name (its addr, dial and role, and its via when r names one;
// allow_inbound, max_scope and relay are kept). An empty role keeps the
// row's, or is observer for a new row. changed reports whether the file
// changed.
func SetRemote(path string, r config.PeerRemote) (changed bool, err error) {
	return config.UpdatePeers(path, func(p *config.Peers) bool { return setRemote(p, r) })
}

// setRemote is SetRemote on the peers section p.
func setRemote(p *config.Peers, r config.PeerRemote) bool {
	for i := range p.Remote {
		cur := &p.Remote[i]
		if cur.Name != r.Name {
			continue
		}
		role, via := r.Role, r.Via
		if role == "" {
			role = cur.Role
		}
		if via == "" {
			via = cur.Via
		}
		if cur.Addr == r.Addr && cur.Dial == r.Dial && cur.Role == role && cur.Via == via {
			return false
		}
		cur.Addr, cur.Dial, cur.Role, cur.Via = r.Addr, r.Dial, role, via
		return true
	}
	if r.Role == "" {
		r.Role = config.PeerRoleObserver
	}
	p.Remote = append(p.Remote, config.PeerRemote{Name: r.Name, Addr: r.Addr, Dial: r.Dial, Role: r.Role, Via: r.Via})
	return true
}

// validRole reports whether role is a peer role, or empty for the default.
func validRole(role string) bool {
	return role == "" || role == config.PeerRoleObserver || role == config.PeerRoleContractor
}

// pinState is what pinning the key with fingerprint fp under name finds in
// store: the pin there now, if any, and whether fp would replace a
// different key.
func pinState(store *Store, name, fp string) (old Pinned, swap bool, err error) {
	old, ok, err := store.Get(name)
	if err != nil {
		return Pinned{}, false, err
	}
	return old, ok && old.Fingerprint != fp, nil
}

// savePin pins p, replacing the key pinned under its name when swap.
func savePin(store *Store, p Pinned, swap bool) error {
	if swap {
		return store.Replace(p)
	}
	return store.Pin(p)
}

// pingPinned probes the pinned peer name with this Deck's identity in dir.
// An error from the probe is sanitized: it can carry the peer's own text.
func pingPinned(ctx context.Context, dir, name, self string, timeout time.Duration) (ProbeResult, error) {
	pin, ok, err := OpenStore(dir).Get(name)
	if err != nil {
		return ProbeResult{}, err
	}
	if !ok {
		return ProbeResult{}, fmt.Errorf("no peer %q is pinned", name)
	}
	if pin.Addr == "" {
		return ProbeResult{}, fmt.Errorf("peer %q has no address (it dials in)", name)
	}
	id, err := LoadIdentity(dir)
	if err != nil {
		return ProbeResult{}, err
	}
	r, err := Probe(ctx, id, pin, self, timeout)
	if err != nil {
		return r, fmt.Errorf("ping %s: %s", name, Sanitize(err.Error()))
	}
	return r, nil
}

// removePeer unpins name in dir and drops it from the config file's remotes
// (when cfgPath is set). A config that could not be updated is cfgErr; the
// pin is gone either way.
func removePeer(dir, cfgPath, name string) (unpinned, unconfigured bool, cfgErr, err error) {
	unpinned, err = OpenStore(dir).Remove(name)
	if err != nil {
		return false, false, nil, err
	}
	if cfgPath != "" {
		unconfigured, cfgErr = RemoveRemote(cfgPath, name)
	}
	return unpinned, unconfigured, cfgErr, nil
}

// RemoveRemote drops name from the config file's remotes, and as the relay
// of any other remote: one with no address left is no longer dialed.
// removed reports whether the file changed.
func RemoveRemote(path, name string) (removed bool, err error) {
	return config.UpdatePeers(path, func(p *config.Peers) bool {
		out := p.Remote[:0]
		for _, r := range p.Remote {
			if r.Name == name {
				removed = true
				continue
			}
			if r.Via == name {
				r.Via, r.Dial = "", r.Dial && r.Addr != ""
			}
			out = append(out, r)
		}
		p.Remote = out
		return removed
	})
}
