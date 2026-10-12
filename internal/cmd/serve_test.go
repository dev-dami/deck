package cmd

import (
	"bytes"
	"context"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/remote"
	"github.com/dev-dami/deck/internal/remote/auth"
)

func flagCmd(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	c := &cobra.Command{Use: "x", Run: func(*cobra.Command, []string) {}}
	addRemoteFlags(c.Flags())
	require.NoError(t, c.ParseFlags(args))
	return c
}

func TestRemoteRunOptionsDefaultsAreLocalOnly(t *testing.T) {
	ro, err := remoteRunOptions(flagCmd(t), true)
	require.NoError(t, err)
	require.Equal(t, remote.RunOptions{Port: remote.DefaultPort, Headless: true, Version: ro.Version}, ro)
	require.False(t, ro.LAN)
	require.False(t, ro.AllowYolo, "yolo from a phone is opt-in")
	require.Empty(t, ro.Tunnel)
}

func TestBareTunnelFlagMeansCloudflare(t *testing.T) {
	ro, err := remoteRunOptions(flagCmd(t, "--tunnel"), false)
	require.NoError(t, err)
	require.Equal(t, "cloudflare", ro.Tunnel)
	require.False(t, ro.Headless)

	ro, err = remoteRunOptions(flagCmd(t, "--tunnel=tailscale", "--lan", "--port", "9000", "--allow-yolo"), true)
	require.NoError(t, err)
	require.Equal(t, "tailscale", ro.Tunnel)
	require.True(t, ro.LAN)
	require.True(t, ro.AllowYolo)
	require.Equal(t, 9000, ro.Port)
}

func TestRemoteRunOptionsRejectBadInput(t *testing.T) {
	_, err := remoteRunOptions(flagCmd(t, "--tunnel=ngrok"), true)
	require.ErrorContains(t, err, "ngrok")
	_, err = remoteRunOptions(flagCmd(t, "--port", "70000"), true)
	require.ErrorContains(t, err, "port")
	_, err = remoteRunOptions(flagCmd(t, "--port", "-1"), true)
	require.ErrorContains(t, err, "port")
}

func TestServeSubcommands(t *testing.T) {
	names := map[string]bool{}
	for _, c := range serveCmd.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["devices"])
	require.True(t, names["revoke"])
}

func newStore(t *testing.T) *auth.Store {
	t.Helper()
	s, err := auth.New(t.TempDir())
	require.NoError(t, err)
	return s
}

func pairOne(t *testing.T, s *auth.Store, name string) auth.Device {
	t.Helper()
	code, err := s.NewCode()
	require.NoError(t, err)
	_, dev, err := s.Pair(code, name, "")
	require.NoError(t, err)
	return dev
}

func TestPrintDevices(t *testing.T) {
	s := newStore(t)
	var out bytes.Buffer
	require.NoError(t, printDevices(&out, s))
	require.Contains(t, out.String(), "No paired phones")

	dev := pairOne(t, s, "Pixel 9")
	out.Reset()
	require.NoError(t, printDevices(&out, s))
	require.Contains(t, out.String(), dev.ID)
	require.Contains(t, out.String(), "Pixel 9")
}

func TestRevokeDevices(t *testing.T) {
	s := newStore(t)
	a, b := pairOne(t, s, "A"), pairOne(t, s, "B")
	var out bytes.Buffer

	require.NoError(t, revokeDevices(&out, s, []string{a.ID}, false))
	devs, _ := s.List()
	require.Len(t, devs, 1)
	require.Equal(t, b.ID, devs[0].ID)

	require.ErrorContains(t, revokeDevices(&out, s, []string{"nope"}, false), "no paired phone")
	require.ErrorContains(t, revokeDevices(&out, s, nil, false), "device id")
	require.ErrorContains(t, revokeDevices(&out, s, []string{b.ID}, true), "not both")

	out.Reset()
	require.NoError(t, revokeDevices(&out, s, nil, true))
	require.Contains(t, out.String(), "1 phone")
	devs, _ = s.List()
	require.Empty(t, devs)
}

type nilBackend struct{ remote.Backend }

func (nilBackend) Events(context.Context) <-chan remote.Event { return nil }

func TestBannerWarnsAboutPlainHTTPAndYolo(t *testing.T) {
	r := &remote.Running{LocalURL: "http://127.0.0.1:7878", LANURLs: []string{"http://192.168.1.5:7878"}}
	// Pairing needs a server; borrow a real one over a temp store.
	started, err := remote.Start(t.Context(), nilBackend{}, remote.RunOptions{Dir: t.TempDir(), LAN: true})
	require.NoError(t, err)
	defer started.Stop()
	started.LANURLs = r.LANURLs
	started.PublicURL = ""

	var out bytes.Buffer
	require.NoError(t, printBanner(&out, started, remote.RunOptions{AllowYolo: true}))
	text := out.String()
	require.Contains(t, text, "same Wi-Fi")
	require.Contains(t, text, "Plain http")
	require.Contains(t, text, "WARNING: --allow-yolo")
	require.Contains(t, text, "#pair=")
	require.Contains(t, text, "█")
}

func TestBannerForCloudflareTunnel(t *testing.T) {
	started, err := remote.Start(t.Context(), nilBackend{}, remote.RunOptions{Dir: t.TempDir()})
	require.NoError(t, err)
	defer started.Stop()
	started.PublicURL, started.Tunnel = "https://x.trycloudflare.com", "cloudflare"

	var out bytes.Buffer
	require.NoError(t, printBanner(&out, started, remote.RunOptions{}))
	require.Contains(t, out.String(), "new address every run")
	require.Contains(t, out.String(), "https://x.trycloudflare.com/#pair=")
	require.NotContains(t, out.String(), "WARNING")
}

func TestBannerLocalOnlyExplainsHowToReachIt(t *testing.T) {
	started, err := remote.Start(t.Context(), nilBackend{}, remote.RunOptions{Dir: t.TempDir()})
	require.NoError(t, err)
	defer started.Stop()
	var out bytes.Buffer
	require.NoError(t, printBanner(&out, started, remote.RunOptions{}))
	require.Contains(t, out.String(), "Only this machine")
}
