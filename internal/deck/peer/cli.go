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
	ln, err := ListenPair(a.Addr)
	if err != nil {
		// A Deck running here serves pairing on its own listener: pairing
		// from it needs no second port.
		return fmt.Errorf("%w\nIf it is this machine's running Deck, pair from it instead: Settings › Team › a › This Deck waits for a code (it uses the same port)", err)
	}
	self := c.selfName()
	expiry := a.Expiry
	if expiry <= 0 {
		expiry = DefaultPairExpiry
	}
	addr := ln.Addr().String()
	port := portOf(ln.Addr())
	w := PairWait{Code: code, Addr: addr, Fingerprint: id.Fingerprint(), Name: self,
		Hosts: InviteHosts(addr), Expires: time.Now().Add(expiry)}
	inv := w.Invite()
	c.printf("Pairing code:  %s   (single use, valid %s)\n", code, expiry)
	c.printf("This Deck:     %s\n", self)
	c.printf("Fingerprint:   %s\n", id.Fingerprint())
	c.printf("Listening on:  %s\n", addr)
	if inv.Host != "" {
		c.printf("Address:       %s\n", inv.Addr())
	}
	c.printf("\n")
	if !Reachable(addr) {
		c.printf("Warning: the other Deck cannot reach %s: it only listens on this machine.\nRun again with --addr 0.0.0.0:%s to listen on all interfaces.\n\n", addr, port)
	}
	if inv.Host != "" {
		c.printf("Invite (holds the code: single use, valid %s; paste it on the other Deck):\n  %s\n\n", expiry, inv.Encode())
		c.printf("On the other Deck: Settings › Team › a › Connect, and paste the invite.\nOr run:  deck peer add --invite -   and paste it at the prompt\n")
	}
	c.printf("Or run:  deck peer add %s <this-host>:%s   and type the code when it asks (or set %s)\n", self, port, EnvPairCode)
	c.printf("Open port %s in your firewall if the other Deck is on another network.\n\n", port)
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
	// Invite, when set, is the line the waiting Deck shows: it gives the
	// address, the code, the fingerprint to expect and, when Name is empty,
	// the name. A Code that is an invite counts as one.
	Invite  string
	Yes     bool
	Replace bool // see PairArgs.Replace
}

// explained is an error worded for a person, keeping the cause for errors.Is.
type explained struct {
	msg string
	err error
}

func (e explained) Error() string { return e.msg }
func (e explained) Unwrap() error { return e.err }

// Add dials a waiting Deck, proves the code and pins it.
func (c CLI) Add(ctx context.Context, a AddArgs) error {
	if a.Invite == "" && LooksLikeInvite(a.Code) {
		a.Invite, a.Code = a.Code, ""
	}
	var expect string
	if a.Invite != "" {
		inv, err := ParseInvite(a.Invite)
		if err != nil {
			return err
		}
		if a.Addr == "" {
			a.Addr = inv.Addr()
		}
		if a.Name == "" {
			a.Name = inv.Name
		}
		a.Code, expect = inv.Code, inv.Fingerprint
	}
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
	c.printf("Checking the code with %s… (up to %s)\n", a.Addr, PairDialTimeout)
	res, err := DialPair(ctx, a.Addr, PairOptions{Identity: id, Name: c.selfName(), Code: a.Code, ExpectFingerprint: expect})
	if err != nil {
		msg := PairErrText(err, a.Addr)
		if errors.Is(err, ErrPairNoAnswer) {
			msg += "\nBoth behind NAT? Pair from a running Deck instead (Settings › Team › a › Connect, paste the invite): it tries through the teammates it is connected to, when one relays (deck peer relay on there)."
		}
		return explained{msg: msg, err: err}
	}
	if expect != "" {
		c.printf("✓ The fingerprint matches the invite.\n")
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
	cfg := c.peersConfig()
	tw := tabwriter.NewWriter(c.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tADDR\tROUTE\tROLE\tSTATE\tFINGERPRINT")
	self := c.selfName()
	for _, p := range peers {
		route := "direct"
		if r, ok := cfg.Find(p.Name); ok {
			if r.Via != "" {
				route = "via " + r.Via
			}
			if r.Relay {
				route += ", relays here"
			}
		}
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
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", p.Name, Sanitize(addr), route, Sanitize(p.Role), state, p.Fingerprint)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if cfg.Relay {
		c.printf("This Deck relays for its teammates.\n")
	}
	return nil
}

// peersConfig is the peers section of the config, empty when it cannot be
// read.
func (c CLI) peersConfig() *config.Peers {
	if c.ConfigPath != "" {
		if p, err := config.LoadPeers(c.ConfigPath); err == nil && p != nil {
			return p
		}
	}
	return &config.Peers{}
}

// relayApplies says when a relay change made here is in effect.
const relayApplies = "A running Deck applies it with r in Settings › Team (deck serve: on SIGHUP), or at its next start.\n"

// Relay is `deck peer relay`: with no args it shows the relay settings;
// on/off sets whether this Deck relays for its teammates; via <name>
// <relay|direct> sets the teammate that relays the link to name; allow and
// deny <relay> set whether a teammate may relay links to this Deck.
func (c CLI) Relay(args []string) error {
	if c.ConfigPath == "" {
		return errors.New("no config file to change")
	}
	store := OpenStore(c.Dir)
	pinned := func(name string) (Pinned, error) {
		p, ok, err := store.Get(name)
		if err == nil && !ok {
			err = fmt.Errorf("no teammate %q is paired here (deck peer list)", name)
		}
		return p, err
	}
	update := func(fn func(p *config.Peers) bool, done string) error {
		changed, err := config.UpdatePeers(c.ConfigPath, fn)
		if err != nil {
			return err
		}
		if !changed {
			c.printf("Already so: %s\n", done)
			return nil
		}
		c.printf("%s\n%s", done, relayApplies)
		return nil
	}
	switch {
	case len(args) == 0:
		return c.relayStatus()
	case len(args) == 1 && (args[0] == "on" || args[0] == "off"):
		on := args[0] == "on"
		done := "This Deck relays for its teammates; it sees only ciphertext."
		if !on {
			done = "This Deck no longer relays."
		}
		return update(func(p *config.Peers) bool {
			if p.Relay == on {
				return false
			}
			p.Relay = on
			return true
		}, done)
	case len(args) == 3 && args[0] == "via":
		name, via := args[1], args[2]
		pin, err := pinned(name)
		if err != nil {
			return err
		}
		var vpin Pinned
		if via == "direct" || via == "-" {
			via = ""
		} else if via == name {
			return fmt.Errorf("%q cannot relay its own link", name)
		} else if vpin, err = pinned(via); err != nil {
			return err
		}
		done := fmt.Sprintf("%s is reached through %s. On %s: deck peer relay on. On %s: deck peer relay allow %s (with the names they use).", name, via, via, name, via)
		if via == "" {
			done = name + " is reached directly."
		}
		return update(func(p *config.Peers) bool { return setVia(p, pin, vpin, via) }, done)
	case len(args) == 2 && (args[0] == "allow" || args[0] == "deny"):
		on := args[0] == "allow"
		pin, err := pinned(args[1])
		if err != nil {
			return err
		}
		done := args[1] + " may relay links and pairings to this Deck."
		if !on {
			done = args[1] + " may no longer relay links to this Deck."
		}
		return update(func(p *config.Peers) bool { return setMayRelay(p, pin, on) }, done)
	}
	return errors.New("use: deck peer relay [on|off | via <name> <relay|direct> | allow <relay> | deny <relay>]")
}

// relayStatus prints the relay settings: whether this Deck relays, the
// teammates reached through a relay, and the ones that may relay here.
func (c CLI) relayStatus() error {
	p := c.peersConfig()
	if p.Relay {
		c.printf("This Deck relays for its teammates (deck peer relay off stops it).\n")
	} else {
		c.printf("This Deck does not relay (deck peer relay on starts it).\n")
	}
	var via, allow []string
	for _, r := range p.Remote {
		if r.Via != "" {
			via = append(via, r.Name+" through "+r.Via)
		}
		if r.Relay {
			allow = append(allow, r.Name)
		}
	}
	if len(via) > 0 {
		c.printf("Reached through a relay: %s\n", strings.Join(via, ", "))
	}
	if len(allow) > 0 {
		c.printf("May relay links to this Deck: %s\n", strings.Join(allow, ", "))
	}
	return nil
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
