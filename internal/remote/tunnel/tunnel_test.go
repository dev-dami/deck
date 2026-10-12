package tunnel

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseQuickTunnelURL(t *testing.T) {
	for line, want := range map[string]string{
		"2026-10-12T09:00:00Z INF |  https://happy-river-1234.trycloudflare.com                |": "https://happy-river-1234.trycloudflare.com",
		"INF Requesting new quick Tunnel on trycloudflare.com...":                                 "",
		"https://api.cloudflare.com/tunnel":                                                       "",
		"":                                                                                        "",
	} {
		require.Equal(t, want, parseQuickTunnelURL(line), line)
	}
}

func TestTailnetHost(t *testing.T) {
	h, err := tailnetHost([]byte(`{"BackendState":"Running","Self":{"DNSName":"laptop.tail1234.ts.net."}}`))
	require.NoError(t, err)
	require.Equal(t, "laptop.tail1234.ts.net", h)

	_, err = tailnetHost([]byte(`{"BackendState":"Stopped","Self":{"DNSName":"x."}}`))
	require.ErrorContains(t, err, "tailscale up")
	_, err = tailnetHost([]byte(`{"BackendState":"Running","Self":{}}`))
	require.ErrorContains(t, err, "MagicDNS")
	_, err = tailnetHost([]byte(`not json`))
	require.Error(t, err)
}

func TestNewUnknownKind(t *testing.T) {
	_, err := New("ngrok")
	require.ErrorContains(t, err, "unknown tunnel")
	for _, k := range []string{Cloudflare, Tailscale} {
		tn, err := New(k)
		require.NoError(t, err)
		require.Equal(t, k, tn.Kind())
	}
}

func TestMissingBinaryGivesInstallHint(t *testing.T) {
	missing := func(string) (string, error) { return "", exec.ErrNotFound }
	for _, tn := range []Tunnel{
		&cloudflared{command: exec.CommandContext, lookPath: missing},
		&tailscale{command: exec.CommandContext, lookPath: missing},
	} {
		_, err := tn.Start(context.Background(), "127.0.0.1:1")
		var ni *ErrNotInstalled
		require.ErrorAs(t, err, &ni, tn.Kind())
		require.NotEmpty(t, ni.Hint)
	}
}

// helper re-runs the test binary as a fake tunnel program, driven by HELPER_MODE.
func helper(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=TestHelperProcess", "--", name}, args...)...)
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER=1")
	return cmd
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER") != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	mode := os.Getenv("HELPER_MODE")
	switch {
	case mode == "url":
		os.Stderr.WriteString("INF starting\nINF |  https://fake-name-9.trycloudflare.com  |\n")
		time.Sleep(time.Minute)
	case mode == "silent":
		os.Stderr.WriteString("INF connecting...\n")
		time.Sleep(time.Minute)
	case mode == "exit":
		os.Stderr.WriteString("ERR could not reach the edge\n")
		os.Exit(1)
	case len(args) > 2 && args[1] == "status":
		os.Stdout.WriteString(`{"BackendState":"Running","Self":{"DNSName":"box.tail9.ts.net."}}`)
	case len(args) > 2 && args[1] == "serve":
		if mode == "serve-fail" {
			os.Stderr.WriteString("HTTPS is not enabled")
			os.Exit(1)
		}
		if f := os.Getenv("HELPER_LOG"); f != "" {
			fh, _ := os.OpenFile(f, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			fh.WriteString(strings.Join(args[1:], " ") + "\n")
			fh.Close()
		}
	}
	os.Exit(0)
}

func withMode(t *testing.T, mode string) {
	t.Helper()
	t.Setenv("HELPER_MODE", mode)
}

func found(string) (string, error) { return "fake", nil }

func TestCloudflaredReportsAddress(t *testing.T) {
	withMode(t, "url")
	c := &cloudflared{command: helper, lookPath: found}
	u, err := c.Start(context.Background(), "127.0.0.1:7878")
	require.NoError(t, err)
	require.Equal(t, "https://fake-name-9.trycloudflare.com", u)
	require.NoError(t, c.Close())
	require.NoError(t, c.Close(), "closing twice is fine")
}

func TestCloudflaredTimesOut(t *testing.T) {
	withMode(t, "silent")
	old := startTimeout
	startTimeout = 300 * time.Millisecond
	defer func() { startTimeout = old }()
	c := &cloudflared{command: helper, lookPath: found}
	_, err := c.Start(context.Background(), "127.0.0.1:7878")
	require.ErrorContains(t, err, "no tunnel address")
	require.ErrorContains(t, err, "connecting", "the program's last output is in the error")
}

func TestCloudflaredExitsEarly(t *testing.T) {
	withMode(t, "exit")
	c := &cloudflared{command: helper, lookPath: found}
	_, err := c.Start(context.Background(), "127.0.0.1:7878")
	require.ErrorContains(t, err, "exited before")
	require.ErrorContains(t, err, "could not reach the edge")
}

func TestCloudflaredHonoursContext(t *testing.T) {
	withMode(t, "silent")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	c := &cloudflared{command: helper, lookPath: found}
	_, err := c.Start(ctx, "127.0.0.1:7878")
	require.Error(t, err)
	require.True(t, errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "exited"), err.Error())
}

func TestTailscaleServesAndUndoes(t *testing.T) {
	withMode(t, "")
	log := t.TempDir() + "/serve.log"
	t.Setenv("HELPER_LOG", log)
	ts := &tailscale{command: helper, lookPath: found}

	u, err := ts.Start(context.Background(), "127.0.0.1:7878")
	require.NoError(t, err)
	require.Equal(t, "https://box.tail9.ts.net", u)

	require.NoError(t, ts.Close())
	require.NoError(t, ts.Close(), "undo runs once")
	b, err := os.ReadFile(log)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	require.Equal(t, []string{
		"serve --bg --https=443 http://127.0.0.1:7878",
		"serve --https=443 off",
	}, lines)
}

func TestTailscaleServeFailureLeavesNothingToUndo(t *testing.T) {
	withMode(t, "serve-fail")
	ts := &tailscale{command: helper, lookPath: found}
	_, err := ts.Start(context.Background(), "127.0.0.1:7878")
	require.ErrorContains(t, err, "HTTPS is not enabled")
	require.NoError(t, ts.Close())
}
