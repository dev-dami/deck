package remote

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStartServesAndStops(t *testing.T) {
	r, err := Start(t.Context(), newFake(), RunOptions{Dir: t.TempDir(), Version: "t"})
	require.NoError(t, err)

	resp, err := http.Get(r.LocalURL + "/api/v1/health")
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, r.LocalURL, r.Origin(), "no LAN, no tunnel: this machine")

	link, qr, err := r.Pairing()
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(link, r.LocalURL+"/#pair="), link)
	require.Contains(t, qr, "█")

	stopped := make(chan struct{})
	go func() { r.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return")
	}
	_, err = http.Get(r.LocalURL + "/api/v1/health")
	require.Error(t, err, "the port is closed after Stop")
}

func TestStartLoopbackOnlyByDefault(t *testing.T) {
	r, err := Start(t.Context(), newFake(), RunOptions{Dir: t.TempDir()})
	require.NoError(t, err)
	defer r.Stop()
	require.Empty(t, r.LANURLs)
	require.True(t, strings.HasPrefix(r.LocalURL, "http://127.0.0.1:"))
}

func TestStartLANListsPrivateAddresses(t *testing.T) {
	r, err := Start(t.Context(), newFake(), RunOptions{Dir: t.TempDir(), LAN: true})
	require.NoError(t, err)
	defer r.Stop()
	for _, u := range r.LANURLs {
		require.True(t, strings.HasPrefix(u, "http://"), u)
	}
	if len(r.LANURLs) > 0 {
		require.Equal(t, r.LANURLs[0], r.Origin(), "a phone is sent to the LAN address first")
		// the LAN address is an accepted Host, so a phone is not refused.
		req, _ := http.NewRequest("GET", r.LANURLs[0]+"/api/v1/health", nil)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
	}
}

func TestStartPortInUse(t *testing.T) {
	a, err := Start(t.Context(), newFake(), RunOptions{Dir: t.TempDir()})
	require.NoError(t, err)
	defer a.Stop()
	port := a.LocalURL[strings.LastIndex(a.LocalURL, ":")+1:]
	var n int
	for _, c := range port {
		n = n*10 + int(c-'0')
	}
	_, err = Start(t.Context(), newFake(), RunOptions{Dir: t.TempDir(), Port: n})
	require.ErrorContains(t, err, "listen")
}

func TestStartUnknownTunnel(t *testing.T) {
	_, err := Start(t.Context(), newFake(), RunOptions{Dir: t.TempDir(), Tunnel: "ngrok"})
	require.ErrorContains(t, err, "unknown tunnel")
}

func TestHostOf(t *testing.T) {
	require.Equal(t, "a.trycloudflare.com", hostOf("https://a.trycloudflare.com"))
	require.Equal(t, "box.ts.net", hostOf("https://box.ts.net/path"))
}
