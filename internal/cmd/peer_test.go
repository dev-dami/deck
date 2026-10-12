package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/deck/peer"
)

func TestPeerCommands(t *testing.T) {
	var peerFound bool
	for _, c := range rootCmd.Commands() {
		if c.Name() == "peer" {
			peerFound = true
		}
	}
	require.True(t, peerFound, "missing peer command")
	names := map[string]bool{}
	for _, c := range peerCmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"pair", "add", "list", "ping", "remove", "fingerprint"} {
		require.True(t, names[want], "missing peer %q", want)
	}
	require.NotNil(t, peerAddCmd.Flags().Lookup("code"))
	require.True(t, peerAddCmd.Flags().Lookup("code").Hidden, "--code must stay out of help")
	require.NotEmpty(t, peerAddCmd.Flags().Lookup("code").Deprecated)
	require.NotNil(t, peerAddCmd.Flags().Lookup("yes"))
	require.NotNil(t, peerAddCmd.Flags().Lookup("replace"))
	require.NotNil(t, peerPairCmd.Flags().Lookup("replace"))
	require.NotNil(t, peerPairCmd.Flags().Lookup("addr"))
	require.NotContains(t, peerCmd.Example, "--code")
}

func TestPairingCodeSources(t *testing.T) {
	newCmd := func(in string) *cobra.Command {
		c := &cobra.Command{}
		c.Flags().String("code", "", "")
		c.SetIn(strings.NewReader(in))
		c.SetErr(&bytes.Buffer{})
		return c
	}
	// The environment wins over the prompt.
	t.Setenv(peer.EnvPairCode, " ABCDE-23456 ")
	code, err := pairingCode(newCmd("ignored\n"))
	require.NoError(t, err)
	require.Equal(t, "ABCDE-23456", code)

	// Not a terminal: one line is read, and only that line.
	t.Setenv(peer.EnvPairCode, "")
	in := strings.NewReader("FGHJK-23456\nyes\n")
	c := newCmd("")
	c.SetIn(in)
	code, err = pairingCode(c)
	require.NoError(t, err)
	require.Equal(t, "FGHJK-23456", code)
	rest := make([]byte, 16)
	n, _ := in.Read(rest)
	require.Equal(t, "yes\n", string(rest[:n]), "the confirmation answer was read away")

	// The hidden flag still works for scripts.
	c = newCmd("")
	require.NoError(t, c.Flags().Set("code", "MNPQR-23456"))
	code, err = pairingCode(c)
	require.NoError(t, err)
	require.Equal(t, "MNPQR-23456", code)
}

func TestPeerFingerprintCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var out bytes.Buffer
	peerFingerprintCmd.SetOut(&out)
	t.Cleanup(func() { peerFingerprintCmd.SetOut(nil) })
	require.NoError(t, peerFingerprintCmd.RunE(peerFingerprintCmd, nil))
	require.Contains(t, out.String(), "SHA256:")
	first := out.String()
	out.Reset()
	require.NoError(t, peerFingerprintCmd.RunE(peerFingerprintCmd, nil))
	require.Equal(t, first, out.String(), "fingerprint changed between runs")
	require.True(t, strings.Count(first, "\n") == 1)
}
