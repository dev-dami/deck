package cmd

import (
	"net"

	"github.com/spf13/cobra"

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
# On the Deck that listens (e.g. a VPS)
deck peer pair

# On the other Deck, with the code it printed
deck peer add vps vps.example.com:7722 --code ABCDE-23456

# Check the link
deck peer ping vps
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
			addr = ":" + deckconfig.DefaultPeerPort
			if cfg, err := deckconfig.Load(); err == nil && cfg.Peers != nil && cfg.Peers.Listen.Addr != "" {
				addr = cfg.Peers.Listen.Addr
			}
		}
		name, _ := cmd.Flags().GetString("name")
		role, _ := cmd.Flags().GetString("role")
		yes, _ := cmd.Flags().GetBool("yes")
		return c.Pair(cmd.Context(), peer.PairArgs{Addr: addr, Name: name, Role: role, Yes: yes})
	},
}

var peerAddCmd = &cobra.Command{
	Use:   "add <name> <host:port>",
	Short: "Pair with a Deck running deck peer pair",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := peerCLI(cmd)
		if err != nil {
			return err
		}
		addr := args[1]
		if _, _, err := net.SplitHostPort(addr); err != nil {
			addr = net.JoinHostPort(addr, deckconfig.DefaultPeerPort)
		}
		code, _ := cmd.Flags().GetString("code")
		role, _ := cmd.Flags().GetString("role")
		yes, _ := cmd.Flags().GetBool("yes")
		return c.Add(cmd.Context(), peer.AddArgs{Name: args[0], Addr: addr, Code: code, Role: role, Yes: yes})
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
	peerAddCmd.Flags().String("code", "", "the code deck peer pair printed")
	peerAddCmd.Flags().String("role", "", "the other Deck's role here: observer (default) or contractor")
	peerAddCmd.Flags().Bool("yes", false, "pin without asking (after checking the fingerprints)")
	_ = peerAddCmd.MarkFlagRequired("code")
	peerListCmd.Flags().Bool("offline", false, "do not try to connect")
	peerCmd.AddCommand(peerPairCmd, peerAddCmd, peerListCmd, peerPingCmd, peerRemoveCmd, peerFingerprintCmd)
}
