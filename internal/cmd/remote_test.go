package cmd

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/pkg/remote/auth"
	"github.com/dev-dami/deck/pkg/remote/server"
)

func TestRemoteCommandAndFlags(t *testing.T) {
	t.Parallel()
	names := map[string]*cobra.Command{}
	for _, c := range rootCmd.Commands() {
		names[c.Name()] = c
	}
	require.Contains(t, names, "remote")
	require.NotContains(t, names, "serve", "deck serve is reserved for the headless engine (R5)")

	subs := map[string]bool{}
	for _, c := range names["remote"].Commands() {
		subs[c.Name()] = true
	}
	for _, want := range []string{"pair", "devices", "revoke"} {
		require.True(t, subs[want], "missing 'deck remote %s'", want)
	}
	for _, flag := range []string{"lan", "port"} {
		require.NotNil(t, names["remote"].Flags().Lookup(flag), "deck remote --%s", flag)
		require.NotNil(t, rootCmd.Flags().Lookup(flag), "deck --%s", flag)
	}
	require.NotNil(t, rootCmd.Flags().Lookup("remote"))
	require.Nil(t, names["remote"].Flags().Lookup("allow-yolo"), "yolo is per device, not a server switch (R15)")
	require.Nil(t, names["remote"].Flags().Lookup("tunnel"), "tunnels are not in v1")
	require.Nil(t, names["remote"].Flags().Lookup("insecure"), "no plain http on the LAN (R9)")
	require.Equal(t, "7878", names["remote"].Flags().Lookup("port").DefValue)
	require.Equal(t, "false", names["remote"].Flags().Lookup("lan").DefValue)

	var devices *cobra.Command
	for _, c := range names["remote"].Commands() {
		if c.Name() == "devices" {
			devices = c
		}
	}
	require.NotNil(t, devices.Flags().Lookup("allow-yolo"))
}

func TestPrintDevicesAndPairingOutput(t *testing.T) {
	t.Parallel()
	store, err := auth.Open(filepath.Join(t.TempDir(), "remote"))
	require.NoError(t, err)
	var out bytes.Buffer
	require.NoError(t, printDevices(&out, store))
	require.Equal(t, "No paired phones.\n", out.String())

	code, err := store.Arm()
	require.NoError(t, err)
	_, dev, err := store.Pair(code.Code, "Pixel")
	require.NoError(t, err)
	require.NoError(t, store.SetAllowYolo(dev.ID, true))
	out.Reset()
	require.NoError(t, printDevices(&out, store))
	require.Contains(t, out.String(), dev.ID)
	require.Contains(t, out.String(), "Pixel")
	require.Contains(t, out.String(), "yolo")
	require.Contains(t, out.String(), "last seen never")

	code, err = store.Arm()
	require.NoError(t, err)
	ad := auth.Advert{URL: "https://192.168.1.5:7878", Hosts: []string{"192.168.1.5"}, Port: 7878, TLS: true, Fingerprint: "SHA256:" + strings.Repeat("A", 43)}
	out.Reset()
	require.NoError(t, printPairing(&out, code, server.NewPairing(ad, code, "deck"), auth.Certificate{Hex: "AB:CD"}))
	require.Contains(t, out.String(), "https://192.168.1.5:7878/#pair="+code.Code)
	require.Contains(t, out.String(), "deck://pair?v=1&host=192.168.1.5&port=7878&code="+code.Code)
	require.Contains(t, out.String(), "AB:CD")
	require.Contains(t, out.String(), "\x1b[30;47m", "the QR is drawn")
}

func TestWaitPairedSeesANewDevice(t *testing.T) {
	store, err := auth.Open(filepath.Join(t.TempDir(), "remote"))
	require.NoError(t, err)
	code, err := store.Arm()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan auth.Device, 1)
	go func() {
		d, _ := waitPaired(ctx, store, code.ExpiresAt)
		done <- d
	}()
	time.Sleep(100 * time.Millisecond)
	other, err := auth.Open(store.Dir())
	require.NoError(t, err)
	_, dev, err := other.Pair(code.Code, "Pixel")
	require.NoError(t, err)
	select {
	case got := <-done:
		require.Equal(t, dev.ID, got.ID)
	case <-ctx.Done():
		t.Fatal("the pairing was not announced")
	}
}
