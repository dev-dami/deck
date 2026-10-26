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
	if cc := dialControl(c.Dir); cc != nil {
		return c.pairRunning(ctx, cc, a)
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
		return fmt.Errorf("%w\nIf another program holds the port, free it or pick another one with --addr", err)
	}
	self := c.selfName()
	expiry := a.Expiry
	if expiry <= 0 {
		expiry = DefaultPairExpiry
	}
	addr := ln.Addr().String()
	w := PairWait{
		Code: code, Addr: addr, Fingerprint: id.Fingerprint(), Name: self,
		Hosts: InviteHosts(addr), Expires: time.Now().Add(expiry), Reachable: Reachable(addr),
	}
	c.printWait(w, expiry, "Run again with --addr 0.0.0.0:"+portOf(ln.Addr())+" to listen on all interfaces.")
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

// printWait shows a code armed for the other Deck: the code, the invite and
// how to use them. fix says how to listen on all interfaces.
func (c CLI) printWait(w PairWait, expiry time.Duration, fix string) {
	inv := w.Invite()
	port := portOfAddr(w.Addr)
	c.printf("Pairing code:  %s   (single use, valid %s)\n", w.Code, expiry.Round(time.Second))
	c.printf("This Deck:     %s\n", w.Name)
	c.printf("Fingerprint:   %s\n", w.Fingerprint)
	c.printf("Listening on:  %s\n", w.Addr)
	if inv.Host != "" {
		c.printf("Address:       %s\n", inv.Addr())
	}
	c.printf("\n")
	if !w.Reachable {
		c.printf("Warning: the other Deck cannot reach %s: it only listens on this machine.\n%s\n\n", w.Addr, fix)
	}
	if inv.Host != "" {
		c.printf("Invite (holds the code: single use, valid %s; paste it on the other Deck):\n  %s\n\n", expiry.Round(time.Second), inv.Encode())
		c.printf("On the other Deck: Settings › Team › a › Connect, and paste the invite.\nOr run:  deck peer add --invite -   and paste it at the prompt\n")
	}
	c.printf("Or run:  deck peer add %s <this-host>:%s   and type the code when it asks (or set %s)\n", w.Name, port, EnvPairCode)
	c.printf("Open port %s in your firewall if the other Deck is on another network,\nor pair through a Deck you are both paired with that relays (deck peer relay on there).\n\n", port)
}

// pairRunning is Pair done by the Deck that runs on this machine (deck
// serve or a terminal Deck): the code is armed on its listener and served
// to pairings its teammates relay, and it pins the other Deck.
func (c CLI) pairRunning(ctx context.Context, cc *controlClient, a PairArgs) error {
	defer cc.Close()
	stop := context.AfterFunc(ctx, func() { cc.Close() })
	defer stop()
	if err := cc.send(controlMsg{Op: "pair"}); err != nil {
		return err
	}
	m, err := cc.recv()
	if err != nil {
		return err
	}
	if m.Wait == nil {
		return errors.New("the running Deck did not arm a code")
	}
	wt := m.Wait
	c.printf("The Deck running here waits for the code, on its own listener.\n\n")
	w := PairWait{
		Code: wt.Code, Addr: wt.Addr, Fingerprint: wt.Fingerprint, Name: wt.Name,
		Hosts: wt.Hosts, Reachable: wt.Reachable, Expires: wt.Expires,
	}
	c.printWait(w, time.Until(wt.Expires), "Turn on Accept connections on all interfaces there (Settings › Team › Listen on, or peers.listen.addr 0.0.0.0:"+portOfAddr(wt.Addr)+").")
	if a.Ready != nil {
		a.Ready(wt.Addr, wt.Code)
	}
	if m, err = cc.recv(); err != nil {
		return err
	}
	if m.Result == nil {
		return errors.New("the running Deck sent no result")
	}
	name := a.Name
	if name == "" {
		name = m.Result.PeerName
	}
	if !config.ValidPeerName(name) || name == wt.Name {
		return fmt.Errorf("the other Deck calls itself %q, which cannot be used here; run pair again with --name", truncate(name, 40))
	}
	return c.pinRunning(cc, *m.Result, controlMsg{Op: "pin", Name: name, Role: a.Role, Replace: a.Replace}, a.Yes, true)
}

// pinRunning shows what the other Deck proved and has the running Deck pin
// it once confirmed. Closing without pin pins nothing.
func (c CLI) pinRunning(cc *controlClient, r controlResult, pin controlMsg, yes, waiting bool) error {
	c.printAccepted(r.RemoteAddr, r.PeerName, r.PeerFingerprint, r.OwnFingerprint, r.Via, waiting)
	if err := c.checkReplace(pin.Name, r.PeerFingerprint, pin.Replace); err != nil {
		return err
	}
	if !yes && !c.confirm(fmt.Sprintf("Pin %q?", pin.Name)) {
		return errors.New("not pinned")
	}
	if err := cc.send(pin); err != nil {
		return err
	}
	if _, err := cc.recv(); err != nil {
		if errors.Is(err, ErrPinTaken) {
			return fmt.Errorf("%w; if that Deck really has a new key, run again with --replace", err)
		}
		return err
	}
	c.printf("Pinned %q; the running Deck has it in effect.\n", pin.Name)
	if r.Via != "" {
		c.printf("It goes through %s, which relays it and sees only ciphertext.\n", r.Via)
	}
	return nil
}

// printAccepted shows who proved the code and both fingerprints.
// waiting is set on the side that armed the code.
func (c CLI) printAccepted(remote, peerName, theirs, own, via string, waiting bool) {
	if via != "" {
		c.printf("Code accepted through %s, which relayed it (it calls itself %q).\n", via, Sanitize(truncate(peerName, 40)))
	} else {
		c.printf("Code accepted by %s (calls itself %q).\n", Sanitize(remote), Sanitize(truncate(peerName, 40)))
	}
	c.printf("  their fingerprint:  %s\n", theirs)
	c.printf("  this Deck:          %s\n", own)
	c.printf("Check that the other Deck shows the same two fingerprints.\n")
	switch {
	case via != "" && waiting:
		c.printf("Pinning it also lets %s relay links to this Deck.\n", via)
	case via != "":
		c.printf("This Deck will reach it through %s.\n", via)
	}
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
	if cc := dialControl(c.Dir); cc != nil {
		return c.addRunning(ctx, cc, a, expect)
	}
	id, err := LoadOrCreateIdentity(c.Dir)
	if err != nil {
		return err
	}
	c.printf("Checking the code with %s… (up to %s)\n", a.Addr, PairDialTimeout)
	res, err := DialPair(ctx, a.Addr, PairOptions{Identity: id, Name: c.selfName(), Code: a.Code, ExpectFingerprint: expect})
	if err != nil {
		msg := PairErrText(err, a.Addr)
		if errors.Is(err, ErrPairNoAnswer) && expect != "" {
			msg += "\nBoth behind NAT? Start Deck (or deck serve) with a teammate that relays (deck peer relay on there), then run this again: the running Deck pairs through it."
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

// addRunning is Add done by the Deck that runs on this machine: when the
// address does not answer, it pairs through the teammates it is connected
// to, and it pins the other Deck.
func (c CLI) addRunning(ctx context.Context, cc *controlClient, a AddArgs, expect string) error {
	defer cc.Close()
	stop := context.AfterFunc(ctx, func() { cc.Close() })
	defer stop()
	c.printf("Checking the code with %s through the Deck running here…\n", a.Addr)
	if expect != "" {
		c.printf("If it does not answer, the Deck tries through the teammates it is connected to.\n")
	}
	if err := cc.send(controlMsg{Op: "add", Addr: a.Addr, Code: a.Code, Expect: expect, Dial: true}); err != nil {
		return err
	}
	m, err := cc.recv()
	if err != nil {
		return explained{msg: err.Error(), err: err}
	}
	if m.Result == nil {
		return errors.New("the running Deck sent no result")
	}
	if expect != "" {
		c.printf("✓ The fingerprint matches the invite.\n")
	}
	return c.pinRunning(cc, *m.Result, controlMsg{Op: "pin", Name: a.Name, Role: a.Role, Replace: a.Replace}, a.Yes, false)
}

// applied says whether the Deck running here applied a config change made
// from the shell, or when it will be.
func (c CLI) applied() {
	ran, err := ReloadRunning(c.Dir)
	switch {
	case !ran:
		c.printf("In effect when Deck next starts.\n")
	case err != nil:
		c.printf("The Deck running here could not apply it: %v\n", err)
	default:
		c.printf("The Deck running here applied it.\n")
	}
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
	c.printAccepted(res.RemoteAddr, res.PeerName, res.PeerFingerprint, res.OwnFingerprint, "", false)
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
		c.printf("%s\n", done)
		c.applied()
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
	if ran, err := ReloadRunning(c.Dir); ran && err == nil {
		c.printf("The Deck running here closed its links to it.\n")
	}
	return nil
}

func portOf(a net.Addr) string {
	_, port, err := net.SplitHostPort(a.String())
	if err != nil {
		return config.DefaultPeerPort
	}
	return port
}
