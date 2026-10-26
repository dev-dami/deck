package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	deckconfig "github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/peer"
)

var peerCmd = &cobra.Command{
	Use:   "peer",
	Short: "Pair and check other Deck instances",
	Long: `Link this Deck with other Deck instances over Deck's own SSH transport.
Only paired (pinned) Decks can connect, and they can only call Deck's two
tools, search and execute.`,
	Example: `
# On the Deck that listens (e.g. a VPS). With deck serve or Deck running
# there, the running Deck waits for the code: no need to stop it.
deck peer pair

# On the other Deck; type the code it printed when asked
# (or set DECK_PEER_CODE)
deck peer add vps vps.example.com:7722

# Or paste the invite it printed (it holds the address, code and fingerprint)
deck peer add --invite -

# Check the link
deck peer ping vps

# Two Decks behind NAT, both paired with vps: vps relays, and they pair
# through it (Settings › Team › a, paste the invite) with no port open
deck peer relay on        # on vps
  `,
}

// peerCLI builds the CLI with the default directories.
func peerCLI(cmd *cobra.Command) (peer.CLI, error) {
	dir, err := peer.DefaultDir()
	if err != nil {
		return peer.CLI{}, err
	}
	cfgPath, _ := deckconfig.Path()
	return peer.CLI{Dir: dir, ConfigPath: cfgPath, Out: cmd.OutOrStdout(), In: cmd.InOrStdin()}, nil
}

var peerPairCmd = &cobra.Command{
	Use:   "pair",
	Short: "Listen and show a one-time code for another Deck to pair with",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := peerCLI(cmd)
		if err != nil {
			return err
		}
		addr, _ := cmd.Flags().GetString("addr")
		if addr == "" {
			var p *deckconfig.Peers
			if cfg, err := deckconfig.Load(); err == nil {
				p = cfg.Peers
			}
			addr = peer.PairAddr(p)
		}
		name, _ := cmd.Flags().GetString("name")
		role, _ := cmd.Flags().GetString("role")
		yes, _ := cmd.Flags().GetBool("yes")
		replace, _ := cmd.Flags().GetBool("replace")
		return c.Pair(cmd.Context(), peer.PairArgs{Addr: addr, Name: name, Role: role, Yes: yes, Replace: replace})
	},
}

// pairingCode returns the code for deck peer add: the hidden --code flag
// (scripts and tests), then DECK_PEER_CODE, then a prompt that does not echo
// when stdin is a terminal. A code on the command line would show in the
// process list and the shell history.
func pairingCode(cmd *cobra.Command) (string, error) {
	if code, _ := cmd.Flags().GetString("code"); code != "" {
		return code, nil
	}
	if code := strings.TrimSpace(os.Getenv(peer.EnvPairCode)); code != "" {
		return code, nil
	}
	return promptSecret(cmd, "Pairing code (or invite): ")
}

// promptSecret asks for a code or an invite, without echoing it when stdin
// is a terminal.
func promptSecret(cmd *cobra.Command, prompt string) (string, error) {
	errOut := cmd.ErrOrStderr()
	fmt.Fprint(errOut, prompt)
	in := cmd.InOrStdin()
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(errOut)
		return strings.TrimSpace(string(b)), err
	}
	return readLine(in)
}

// readLine reads one line a byte at a time, so nothing after it is buffered
// away from the confirmation prompt that reads the same input next.
func readLine(r io.Reader) (string, error) {
	var b []byte
	one := make([]byte, 1)
	for len(b) < 2048 { // room for an invite
		n, err := r.Read(one)
		if n == 1 {
			if one[0] == '\n' {
				break
			}
			b = append(b, one[0])
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return strings.TrimSpace(string(b)), nil
}

var peerAddCmd = &cobra.Command{
	Use:   "add [name] [host:port]",
	Short: "Pair with a Deck that waits for a code",
	Long: `Pair with a Deck that waits for a code (Settings › Team › a there, or
deck peer pair). Give its name and address and type the code when asked, or
paste the invite it shows: --invite - (or no address) asks for it without
echoing it. The prompt also takes an invite in place of the code.

With Deck running on this machine (deck serve or a terminal Deck), the
running Deck does the pairing and pins the other Deck: when the invite's
address does not answer, it pairs through the teammates it is connected to,
if one of them relays.`,
	Args: cobra.RangeArgs(0, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := peerCLI(cmd)
		if err != nil {
			return err
		}
		a := peer.AddArgs{}
		a.Role, _ = cmd.Flags().GetString("role")
		a.Yes, _ = cmd.Flags().GetBool("yes")
		a.Replace, _ = cmd.Flags().GetBool("replace")
		a.Invite, _ = cmd.Flags().GetString("invite")
		if len(args) > 0 {
			a.Name = args[0]
		}
		if len(args) > 1 {
			a.Addr = peer.WithDefaultPort(args[1])
		}
		switch {
		case a.Invite == "-" || (a.Invite == "" && a.Addr == ""):
			// The invite holds the code: read it like one, off the
			// command line.
			if a.Invite, err = promptSecret(cmd, "Invite: "); err != nil {
				return err
			}
		case a.Invite == "":
			if a.Code, err = pairingCode(cmd); err != nil {
				return err
			}
		}
		return c.Add(cmd.Context(), a)
	},
}

var peerListCmd = &cobra.Command{
	Use:   "list",
	Short: "List pinned peers and whether they answer",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := peerCLI(cmd)
		if err != nil {
			return err
		}
		offline, _ := cmd.Flags().GetBool("offline")
		return c.List(cmd.Context(), !offline)
	},
}

var peerPingCmd = &cobra.Command{
	Use:   "ping <name>",
	Short: "Connect to a pinned peer and check its tools and latency",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := peerCLI(cmd)
		if err != nil {
			return err
		}
		return c.Ping(cmd.Context(), args[0])
	},
}

var peerRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Unpin a peer so it can no longer connect",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := peerCLI(cmd)
		if err != nil {
			return err
		}
		return c.Remove(args[0])
	},
}

var peerRelayCmd = &cobra.Command{
	Use:   "relay [on|off | via <name> <relay|direct> | allow <relay> | deny <relay>]",
	Short: "Link teammates that cannot reach each other through a Deck both are paired with",
	Long: `Two Decks that cannot reach each other (both behind NAT, say) can link
through a third Deck they are both paired with: the relay. The two ends run
SSH inside the relayed channel with their own pinned keys, so the relay sees
only ciphertext.

Most of it sets itself: turn relaying on on the relay (deck peer relay on),
then pair the two Decks as usual from Settings › Team › a. When the invite's
address does not answer, the connecting Deck pairs through the relay, and both
Decks save the route. Use the rest to set routes by hand:

  on | off               this Deck relays for its teammates, or stops
  via <name> <relay>     reach <name> through <relay>; "direct" undoes it
  allow | deny <relay>   let <relay> bring links to this Deck, or stop it

With no arguments it shows the relay settings. The Deck running on this
machine (deck serve or a terminal Deck) applies a change at once.`,
	Example: `
# On the server both laptops are paired with
deck peer relay on

# By hand, on laptop a: reach b through vps; on b: let vps relay to it
deck peer relay via b vps
deck peer relay allow vps
  `,
	Args: cobra.MaximumNArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := peerCLI(cmd)
		if err != nil {
			return err
		}
		return c.Relay(args)
	},
}

var peerFingerprintCmd = &cobra.Command{
	Use:   "fingerprint",
	Short: "Print this Deck's peer fingerprint (creating its key if needed)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := peerCLI(cmd)
		if err != nil {
			return err
		}
		return c.Fingerprint()
	},
}

func init() {
	peerPairCmd.Flags().String("addr", "", "address to listen on (default peers.listen.addr or :7722)")
	peerPairCmd.Flags().String("name", "", "name to pin the other Deck under (default: the name it sends)")
	peerPairCmd.Flags().String("role", "", "the other Deck's role here: observer (default) or contractor")
	peerPairCmd.Flags().Bool("yes", false, "pin without asking (after checking the fingerprints)")
	peerPairCmd.Flags().Bool("replace", false, "allow replacing a different key already pinned under that name")
	// --code is for tests and scripts only: hidden, and deprecated so using
	// it prints why. The prompt or DECK_PEER_CODE keep the code out of the
	// process list and the shell history.
	peerAddCmd.Flags().String("code", "", "the code deck peer pair printed (deprecated: use the prompt or DECK_PEER_CODE)")
	_ = peerAddCmd.Flags().MarkHidden("code")
	_ = peerAddCmd.Flags().MarkDeprecated("code", "it shows in the process list and shell history; type the code at the prompt or set DECK_PEER_CODE")
	peerAddCmd.Flags().String("invite", "", "the invite the waiting Deck shows; - asks for it without echoing (it holds the code)")
	peerAddCmd.Flags().String("role", "", "the other Deck's role here: observer (default) or contractor")
	peerAddCmd.Flags().Bool("yes", false, "pin without asking (after checking the fingerprints)")
	peerAddCmd.Flags().Bool("replace", false, "allow replacing a different key already pinned under that name")
	peerListCmd.Flags().Bool("offline", false, "do not try to connect")
	peerCmd.AddCommand(peerPairCmd, peerAddCmd, peerListCmd, peerPingCmd, peerRemoveCmd, peerRelayCmd, peerFingerprintCmd)
}
