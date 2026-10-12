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
// list` and doctor use.
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

// AddRemote records a newly paired peer in the config file at path as a
// remote to dial, unless one with that name is there already. It does not
// turn peers on. added reports whether the file changed.
func AddRemote(path, name, addr string) (added bool, err error) {
	cfg, err := config.LoadFrom(path)
	if err != nil {
		return false, err
	}
	if cfg.Peers == nil {
		cfg.Peers = &config.Peers{}
	}
	if _, ok := cfg.Peers.Find(name); ok {
		return false, nil
	}
	cfg.Peers.Remote = append(cfg.Peers.Remote, config.PeerRemote{Name: name, Addr: addr, Dial: addr != ""})
	return true, config.SaveTo(path, cfg)
}

// RemoveRemote drops name from the config file's remotes. removed reports
// whether the file changed.
func RemoveRemote(path, name string) (removed bool, err error) {
	cfg, err := config.LoadFrom(path)
	if err != nil {
		return false, err
	}
	if cfg.Peers == nil {
		return false, nil
	}
	out := cfg.Peers.Remote[:0]
	for _, r := range cfg.Peers.Remote {
		if r.Name == name {
			removed = true
			continue
		}
		out = append(out, r)
	}
	if !removed {
		return false, nil
	}
	cfg.Peers.Remote = out
	return true, config.SaveTo(path, cfg)
}
