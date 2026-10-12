package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
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
	require.NotNil(t, peerAddCmd.Flags().Lookup("yes"))
	require.NotNil(t, peerPairCmd.Flags().Lookup("addr"))
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
