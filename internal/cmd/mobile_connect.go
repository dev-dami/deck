package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/dev-dami/deck/internal/mobilecontrol"
	"github.com/dev-dami/deck/pkg/remote"
	"github.com/spf13/cobra"
)

func init() {
	var opt mobilecontrol.Options
	var jsonOutput bool
	command := &cobra.Command{Use: "connect", Short: "Start mobile access and show a pairing QR in one step", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		dir, err := mobileDir()
		if err != nil {
			return err
		}
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		opt.Dir = dir
		opt.Executable = exe
		result, err := mobilecontrol.Run(cmd.Context(), opt)
		if err != nil {
			return err
		}
		if jsonOutput {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Computer: %s\nRelay: %s\nOnline: %t | Phones: %d | Projects: %d\n", result.Name, result.RelayURL, result.Online, result.Devices, result.Projects)
		if result.Message != "" {
			fmt.Fprintln(cmd.OutOrStdout(), result.Message)
		}
		if result.PairURL != "" {
			qr, err := remote.RenderQR(result.PairURL)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "\nScan in the Deck phone app (expires %s):\n%s\n%s\n", result.ExpiresAt.Local().Format("15:04"), qr, result.PairURL)
		}
		return nil
	}}
	f := command.Flags()
	f.StringVar(&opt.Action, "action", "start", "Action: start, status, pair or stop")
	f.BoolVar(&opt.LAN, "lan", false, "Start a local relay on this PC for trusted Wi-Fi testing")
	f.StringVar(&opt.Project, "project", "", "Explicitly share this project directory (start only)")
	f.StringVar(&opt.RelayDir, "relay-dir", "", "Directory containing the local relay runtime")
	f.StringVar(&opt.LANIP, "lan-ip", "", "Private IPv4 address advertised to the phone")
	f.IntVar(&opt.Port, "port", 8787, "Local relay port")
	f.BoolVar(&jsonOutput, "json", false, "Print only a JSON result")
	mobileCmd.AddCommand(command)
	var role string
	managed := &cobra.Command{Use: "managed", Hidden: true, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		dir, err := mobileDir()
		if err != nil {
			return err
		}
		return mobilecontrol.ServeManaged(cmd.Context(), dir, role)
	}}
	managed.Flags().StringVar(&role, "role", "", "Managed component")
	mobileCmd.AddCommand(managed)
}
