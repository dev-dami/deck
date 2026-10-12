package peer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dev-dami/deck/internal/deck/config"
)

// CLI implements the `deck peer` subcommands; the cobra layer only parses
// flags.
type CLI struct {
	Dir        string // peers directory
	ConfigPath string // Deck's config.json, for peers.remote and peers.name
	Out        io.Writer
	In         io.Reader // answers to confirmation prompts
}

func (c CLI) printf(format string, a ...any) { fmt.Fprintf(c.Out, format, a...) }

// selfName is this Deck's name from the config, else the host name.
func (c CLI) selfName() string {
	if c.ConfigPath != "" {
		if cfg, err := config.LoadFrom(c.ConfigPath); err == nil {
			return SelfName(cfg.Peers)
		}
	}
	return SelfName(nil)
}

// confirm asks a yes/no question; anything but y or yes is no.
func (c CLI) confirm(question string) bool {
	c.printf("%s [y/N] ", question)
	if c.In == nil {
		c.printf("\n")
		return false
	}
	line, _ := bufio.NewReader(c.In).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

// Fingerprint prints this Deck's fingerprint, creating the identity if needed.
func (c CLI) Fingerprint() error {
	id, err := LoadOrCreateIdentity(c.Dir)
	if err != nil {
		return err
	}
	c.printf("%s  %s\n", c.selfName(), id.Fingerprint())
	return nil
}

// PairArgs are the options of `deck peer pair`.
type PairArgs struct {
	Addr   string // where to listen
	Name   string // name to pin the other Deck under; empty means the name it sends
	Role   string
	Yes    bool
	Expiry time.Duration
	// Ready, when set, gets the listening address once the code is shown.
	Ready func(addr, code string)
}

// Pair listens with a fresh one-time code and pins the Deck that proves it.
func (c CLI) Pair(ctx context.Context, a PairArgs) error {
	if a.Role != "" && a.Role != config.PeerRoleObserver && a.Role != config.PeerRoleContractor {
		return fmt.Errorf("--role must be observer or contractor")
	}
	if a.Name != "" && !config.ValidPeerName(a.Name) {
		return fmt.Errorf("bad name %q: use 1-32 of a-z, 0-9, '-', '_', '.'", a.Name)
	}
	id, err := LoadOrCreateIdentity(c.Dir)
	if err != nil {
		return err
	}
	code, err := NewPairCode()
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", a.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w (is Deck already listening there? stop it or pass --addr)", a.Addr, err)
	}
	self := c.selfName()
	expiry := a.Expiry
	if expiry <= 0 {
		expiry = DefaultPairExpiry
	}
	c.printf("Pairing code:  %s   (single use, valid %s)\n", code, expiry)
	c.printf("This Deck:     %s  %s\n", self, id.Fingerprint())
	c.printf("Listening on:  %s\n\n", ln.Addr())
	c.printf("On the other Deck run:\n  deck peer add %s <this-host>:%s --code %s\n\n", self, portOf(ln.Addr()), code)
	if a.Ready != nil {
		a.Ready(ln.Addr().String(), code)
	}
	res, err := ServePair(ctx, ln, PairOptions{Identity: id, Name: self, Code: code, Expiry: expiry})
	if err != nil {
		return err
	}
	name := a.Name
	if name == "" {
		name = res.PeerName
	}
	if !config.ValidPeerName(name) || name == self {
		return fmt.Errorf("the other Deck calls itself %q, which cannot be used here; run pair again with --name", truncate(name, 40))
	}
	return c.pin(name, "", a.Role, res, a.Yes)
}

// AddArgs are the options of `deck peer add`.
type AddArgs struct {
	Name, Addr, Code, Role string
	Yes                    bool
}

// Add dials a Deck running `deck peer pair`, proves the code and pins it.
func (c CLI) Add(ctx context.Context, a AddArgs) error {
	if !config.ValidPeerName(a.Name) {
		return fmt.Errorf("bad name %q: use 1-32 of a-z, 0-9, '-', '_', '.'", a.Name)
	}
	if err := config.ValidPeerAddr(a.Addr); err != nil {
		return fmt.Errorf("bad address %q: %v (want host:port)", a.Addr, err)
	}
	if a.Role != "" && a.Role != config.PeerRoleObserver && a.Role != config.PeerRoleContractor {
		return fmt.Errorf("--role must be observer or contractor")
	}
	id, err := LoadOrCreateIdentity(c.Dir)
	if err != nil {
		return err
	}
	res, err := DialPair(ctx, a.Addr, PairOptions{Identity: id, Name: c.selfName(), Code: a.Code})
	if err != nil {
		return err
	}
	if err := c.pin(a.Name, a.Addr, a.Role, res, a.Yes); err != nil {
		return err
	}
	if c.ConfigPath != "" {
		added, err := AddRemote(c.ConfigPath, a.Name, a.Addr)
		switch {
		case err != nil:
			c.printf("Could not add %q to %s: %v\n", a.Name, c.ConfigPath, err)
		case added:
			c.printf("Added %q to peers.remote in %s (set peers.enabled to true to keep it connected).\n", a.Name, c.ConfigPath)
		}
	}
	return nil
}

// pin shows both fingerprints and pins the peer once confirmed.
func (c CLI) pin(name, addr, role string, res PairResult, yes bool) error {
	store := OpenStore(c.Dir)
	c.printf("Code accepted by %s (calls itself %q).\n", res.RemoteAddr, truncate(res.PeerName, 40))
	c.printf("  their fingerprint:  %s\n", res.PeerFingerprint)
	c.printf("  this Deck:          %s\n", res.OwnFingerprint)
	c.printf("Check that the other Deck shows the same two fingerprints.\n")
	if old, ok, err := store.Get(name); err != nil {
		return err
	} else if ok && old.Fingerprint != res.PeerFingerprint {
		c.printf("Warning: this replaces the key pinned for %q (%s).\n", name, old.Fingerprint)
	}
	if !yes && !c.confirm(fmt.Sprintf("Pin %q?", name)) {
		return errors.New("not pinned")
	}
	if err := store.Pin(NewPinned(name, addr, role, res.PeerKey)); err != nil {
		return err
	}
	c.printf("Pinned %q.\n", name)
	return nil
}

// List prints the pinned peers; with probe it also tries each dialable one.
func (c CLI) List(ctx context.Context, probe bool) error {
	peers, err := OpenStore(c.Dir).List()
	if err != nil {
		return err
	}
	if len(peers) == 0 {
		c.printf("No peers pinned. Pair one with deck peer pair / deck peer add.\n")
		return nil
	}
	var id *Identity
	if probe {
		if id, err = LoadIdentity(c.Dir); err != nil {
			probe = false
		}
	}
	tw := tabwriter.NewWriter(c.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tADDR\tROLE\tSTATE\tFINGERPRINT")
	self := c.selfName()
	for _, p := range peers {
		addr, state := p.Addr, "inbound only"
		if addr == "" {
			addr = "-"
		} else if probe {
			if r, err := Probe(ctx, id, p, self, 3*time.Second); err != nil {
				state = "unreachable"
			} else {
				state = "reachable " + r.Latency.Round(time.Millisecond).String()
			}
		} else {
			state = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", p.Name, addr, p.Role, state, p.Fingerprint)
	}
	return tw.Flush()
}

// Ping connects to a pinned peer and reports its latency, tools and status.
func (c CLI) Ping(ctx context.Context, name string) error {
	pin, ok, err := OpenStore(c.Dir).Get(name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no peer %q is pinned", name)
	}
	if pin.Addr == "" {
		return fmt.Errorf("peer %q has no address (it dials in)", name)
	}
	id, err := LoadIdentity(c.Dir)
	if err != nil {
		return err
	}
	r, err := Probe(ctx, id, pin, c.selfName(), 10*time.Second)
	if err != nil {
		return fmt.Errorf("ping %s: %w", name, err)
	}
	c.printf("pong from %s in %s\n", name, r.Latency.Round(time.Microsecond))
	c.printf("  tools:  %s\n", strings.Join(r.Tools, ", "))
	if r.Status != "" {
		c.printf("  status: %s\n", truncate(r.Status, 400))
	}
	return nil
}

// Remove unpins a peer and drops it from the config's remotes.
func (c CLI) Remove(name string) error {
	ok, err := OpenStore(c.Dir).Remove(name)
	if err != nil {
		return err
	}
	removedCfg := false
	if c.ConfigPath != "" {
		if removedCfg, err = RemoveRemote(c.ConfigPath, name); err != nil {
			c.printf("Could not update %s: %v\n", c.ConfigPath, err)
		}
	}
	if !ok && !removedCfg {
		return fmt.Errorf("no peer %q", name)
	}
	c.printf("Removed %q; it can no longer connect.\n", name)
	return nil
}

func portOf(a net.Addr) string {
	_, port, err := net.SplitHostPort(a.String())
	if err != nil {
		return config.DefaultPeerPort
	}
	return port
}
