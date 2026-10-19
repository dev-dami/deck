package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/pkg/remote/devstore"
	"github.com/dev-dami/deck/pkg/remote/mobile"
	"github.com/dev-dami/deck/pkg/remote/projects"
)

func run(t *testing.T, c *cobra.Command, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	err := c.RunE(c, args)
	return out.String(), err
}

func TestMobileCommandTree(t *testing.T) {
	t.Parallel()
	var mobileC *cobra.Command
	for _, c := range rootCmd.Commands() {
		if c.Name() == "mobile" {
			mobileC = c
		}
	}
	require.NotNil(t, mobileC, "deck mobile")
	subs := map[string]*cobra.Command{}
	for _, c := range mobileC.Commands() {
		subs[c.Name()] = c
	}
	for _, want := range []string{"setup", "daemon", "pair", "status", "devices", "revoke", "projects", "host", "install", "uninstall"} {
		require.Contains(t, subs, want, "deck mobile %s", want)
	}
	require.True(t, subs["host"].Hidden, "the headless host is the daemon's business, not the user's")
	require.NotNil(t, subs["devices"].Flags().Lookup("allow-yolo"))
	require.NotNil(t, subs["setup"].Flags().Lookup("relay"))
}

func TestMobileSetupValidatesTheRelay(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DECK_MOBILE_DIR", dir)

	require.NoError(t, mobileSetupCmd.Flags().Set("relay", "https://app.example.dev/"))
	require.NoError(t, mobileSetupCmd.Flags().Set("name", "work-pc"))
	out, err := run(t, mobileSetupCmd)
	require.NoError(t, err)
	require.Contains(t, out, "https://app.example.dev")
	cfg, err := mobile.LoadConfig(dir)
	require.NoError(t, err)
	require.Equal(t, mobile.Config{RelayURL: "https://app.example.dev", Name: "work-pc"}, cfg, "trailing slash trimmed")

	for _, bad := range []string{"http://app.example.dev", "ftp://x.example", "not a url", "https://"} {
		require.NoError(t, mobileSetupCmd.Flags().Set("relay", bad))
		_, err := run(t, mobileSetupCmd)
		require.Error(t, err, "relay %q must be refused: a plain-http relay would carry the phone's page in the clear", bad)
	}
	require.NoError(t, mobileSetupCmd.Flags().Set("relay", "http://localhost:8787"))
	_, err = run(t, mobileSetupCmd)
	require.NoError(t, err, "http is fine for a local relay")
}

func TestMobileProjectsFlow(t *testing.T) {
	t.Setenv("DECK_MOBILE_DIR", t.TempDir())
	folder := t.TempDir()

	out, err := run(t, mobileProjectsCmd)
	require.NoError(t, err)
	require.Contains(t, out, "No folders shared")

	out, err = run(t, mobileProjectsAddCmd, folder)
	require.NoError(t, err)
	id := projects.IDOf(folder)
	require.Contains(t, out, id)

	out, err = run(t, mobileProjectsCmd)
	require.NoError(t, err)
	require.Contains(t, out, id)
	require.Contains(t, out, "on")

	_, err = run(t, mobileProjectsRmCmd, "prj_nope")
	require.ErrorContains(t, err, "no shared folder")
	_, err = run(t, mobileProjectsRmCmd, id)
	require.NoError(t, err)
	out, _ = run(t, mobileProjectsCmd)
	require.Contains(t, out, "No folders shared")

	_, err = run(t, mobileProjectsAddCmd, folder+"-missing")
	require.Error(t, err)
}

func TestMobileDevicesAndRevoke(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DECK_MOBILE_DIR", dir)

	out, err := run(t, mobileDevicesCmd)
	require.NoError(t, err)
	require.Equal(t, "No paired phones.\n", out)

	store, err := devstore.Open(dir)
	require.NoError(t, err)
	dev, _, err := store.Add("Pixel 9", bytes.Repeat([]byte{1}, 32))
	require.NoError(t, err)

	out, err = run(t, mobileDevicesCmd)
	require.NoError(t, err)
	require.Contains(t, out, dev.ID)
	require.Contains(t, out, "Pixel 9")
	require.NotContains(t, out, "yolo")

	require.NoError(t, mobileDevicesCmd.Flags().Set("allow-yolo", dev.ID))
	out, err = run(t, mobileDevicesCmd)
	require.NoError(t, err)
	require.Contains(t, out, "fingerprint or face", "the grant is not the whole gate and the message must not suggest it is")
	require.True(t, strings.Contains(out, dev.ID+"  Pixel 9") && strings.Contains(out, "yolo"))
	require.NoError(t, mobileDevicesCmd.Flags().Set("allow-yolo", ""))
	require.NoError(t, mobileDevicesCmd.Flags().Set("deny-yolo", ""))

	_, err = run(t, mobileRevokeCmd, "dev_nope")
	require.ErrorContains(t, err, "no paired phone")
	_, err = run(t, mobileRevokeCmd, dev.ID)
	require.NoError(t, err)
	out, _ = run(t, mobileDevicesCmd)
	require.Equal(t, "No paired phones.\n", out)
}

func TestMobileStatusWhenNotRunning(t *testing.T) {
	t.Setenv("DECK_MOBILE_DIR", t.TempDir())
	out, err := run(t, mobileStatusCmd)
	require.NoError(t, err)
	require.Contains(t, out, "Not running")
}
