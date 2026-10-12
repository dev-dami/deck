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
	"unicode"

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

// EnvPairCode lets `deck peer add` take the code from the environment
// instead of its prompt; a code on the command line shows in the process
// list and the shell history.
const EnvPairCode = "DECK_PEER_CODE"

// Sanitize makes text that came from a peer safe to print on a terminal:
// control characters (ESC and the rest of C0 and C1, which drive terminal
// escape sequences) and Unicode format characters (bidi overrides,
// zero-width) become '?', newlines and tabs become spaces, and invalid UTF-8
// becomes U+FFFD.
func Sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return ' '
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			return '?'
		}
		return r
	}, s)
}

// PairArgs are the options of `deck peer pair`.
type PairArgs struct {
	Addr string // where to listen
	Name string // name to pin the other Deck under; empty means the name it sends
	Role string
	Yes  bool
	// Replace allows pinning over a different key already pinned under the
	// name; without it that is refused, --yes or not.
	Replace bool
	Expiry  time.Duration
	// Ready, when set, gets the listening address once the code is shown.
	Ready func(addr, code string)
}

// Pair listens with a fresh one-time code and pins the Deck that proves it.
func (c CLI) Pair(ctx context.Context, a PairArgs) error {
	if !validRole(a.Role) {
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
	c.printf("On the other Deck run the line below and type the code when it asks\n(or set %s):\n  deck peer add %s <this-host>:%s\n\n", EnvPairCode, self, portOf(ln.Addr()))
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
	if err := c.pin(name, "", a.Role, res, a.Yes, a.Replace); err != nil {
		return err
	}
	// Every pinned peer gets an explicit config row: inbound only, the
	// role chosen here, and the default allow_inbound.
	c.addRemote(name, "", a.Role)
	return nil
}

// AddArgs are the options of `deck peer add`.
type AddArgs struct {
	Name, Addr, Code, Role string
	Yes                    bool
	Replace                bool // see PairArgs.Replace
}

// Add dials a Deck running `deck peer pair`, proves the code and pins it.
func (c CLI) Add(ctx context.Context, a AddArgs) error {
	if !config.ValidPeerName(a.Name) {
		return fmt.Errorf("bad name %q: use 1-32 of a-z, 0-9, '-', '_', '.'", a.Name)
	}
	if err := config.ValidPeerAddr(a.Addr); err != nil {
		return fmt.Errorf("bad address %q: %v (want host:port)", a.Addr, err)
	}
	if !validRole(a.Role) {
		return fmt.Errorf("--role must be observer or contractor")
	}
	if a.Code == "" {
		return fmt.Errorf("no pairing code: type it at the prompt or set %s", EnvPairCode)
	}
	id, err := LoadOrCreateIdentity(c.Dir)
	if err != nil {
		return err
	}
	res, err := DialPair(ctx, a.Addr, PairOptions{Identity: id, Name: c.selfName(), Code: a.Code})
	if err != nil {
		return err
	}
	if err := c.pin(a.Name, a.Addr, a.Role, res, a.Yes, a.Replace); err != nil {
		return err
	}
	c.addRemote(a.Name, a.Addr, a.Role)
	return nil
}

// addRemote records a newly pinned peer in the config, saying what it did.
func (c CLI) addRemote(name, addr, role string) {
	if c.ConfigPath == "" {
		return
	}
	added, err := AddRemote(c.ConfigPath, name, addr, role)
	switch {
	case err != nil:
		c.printf("Could not add %q to %s: %v\n", name, c.ConfigPath, err)
	case added && addr != "":
		c.printf("Added %q to peers.remote in %s (set peers.enabled to true to keep it connected).\n", name, c.ConfigPath)
	case added:
		c.printf("Added %q to peers.remote in %s (inbound only; it may run: %s).\n", name, c.ConfigPath, strings.Join(DefaultAllowInbound, ", "))
	}
}

// checkReplace refuses to pin key fp over a different key already pinned
// under name unless replace is set (--yes alone is not enough). The same
// key again, e.g. to update the address, is fine.
func (c CLI) checkReplace(name, fp string, replace bool) error {
	old, swap, err := pinState(OpenStore(c.Dir), name, fp)
	if err != nil || !swap || replace {
		return err
	}
	return fmt.Errorf("%q is already pinned with another key (%s); if that Deck really has a new key, run again with --replace", name, old.Fingerprint)
}

// pin shows both fingerprints and pins the peer once confirmed.
func (c CLI) pin(name, addr, role string, res PairResult, yes, replace bool) error {
	store := OpenStore(c.Dir)
	c.printf("Code accepted by %s (calls itself %q).\n", Sanitize(res.RemoteAddr), Sanitize(truncate(res.PeerName, 40)))
	c.printf("  their fingerprint:  %s\n", res.PeerFingerprint)
	c.printf("  this Deck:          %s\n", res.OwnFingerprint)
	c.printf("Check that the other Deck shows the same two fingerprints.\n")
	if err := c.checkReplace(name, res.PeerFingerprint, replace); err != nil {
		return err
	}
	old, swap, err := pinState(store, name, res.PeerFingerprint)
	if err != nil {
		return err
	}
	if swap {
		c.printf("Warning: this replaces the key pinned for %q (%s).\n", name, old.Fingerprint)
	}
	if !yes && !c.confirm(fmt.Sprintf("Pin %q?", name)) {
		return errors.New("not pinned")
	}
	if err := savePin(store, NewPinned(name, addr, role, res.PeerKey), swap); err != nil {
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
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", p.Name, Sanitize(addr), Sanitize(p.Role), state, p.Fingerprint)
	}
	return tw.Flush()
}

// Ping connects to a pinned peer and reports its latency, tools and status.
func (c CLI) Ping(ctx context.Context, name string) error {
	r, err := pingPinned(ctx, c.Dir, name, c.selfName(), 10*time.Second)
	if err != nil {
		return err
	}
	c.printf("pong from %s in %s\n", name, r.Latency.Round(time.Microsecond))
	tools := make([]string, len(r.Tools))
	for i, t := range r.Tools {
		tools[i] = Sanitize(truncate(t, 64))
	}
	c.printf("  tools:  %s\n", strings.Join(tools, ", "))
	if r.Status != "" {
		c.printf("  status: %s\n", Sanitize(truncate(r.Status, 400)))
	}
	return nil
}

// Remove unpins a peer and drops it from the config's remotes.
func (c CLI) Remove(name string) error {
	ok, removedCfg, cfgErr, err := removePeer(c.Dir, c.ConfigPath, name)
	if err != nil {
		return err
	}
	if cfgErr != nil {
		c.printf("Could not update %s: %v\n", c.ConfigPath, cfgErr)
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
