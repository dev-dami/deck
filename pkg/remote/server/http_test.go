package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/pkg/remote/auth"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

func TestHealthAndHeaders(t *testing.T) {
	r := newRig(t)
	resp, err := http.Get(r.ts.URL + "/api/v1/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	require.JSONEq(t, `{"ok":true,"protocol":2}`, string(body))
	require.Equal(t, csp, resp.Header.Get("Content-Security-Policy"))
	require.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	require.Equal(t, "no-referrer", resp.Header.Get("Referrer-Policy"))
	require.Equal(t, "no-store", resp.Header.Get("Cache-Control"))

	page, err := http.Get(r.ts.URL + "/sessions/abc")
	require.NoError(t, err)
	defer page.Body.Close()
	body, _ = io.ReadAll(page.Body)
	require.Equal(t, "<html>app</html>", string(body), "client routes serve the app shell")
	require.Equal(t, "no-cache", page.Header.Get("Cache-Control"), "the shell revalidates; no-store is for /api")

	req, _ := http.NewRequest(http.MethodGet, r.ts.URL+"/api/v1/health", nil)
	req.Host = "evil.example"
	bad, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer bad.Body.Close()
	require.Equal(t, http.StatusMisdirectedRequest, bad.StatusCode, "DNS rebinding")
}

func TestR7_PairRequiresSameOriginAndJSON(t *testing.T) {
	r := newRig(t)
	// A phone already paired hears the new device (R3).
	tok, _ := r.token("Old")
	old, _ := r.welcome(tok, nil)

	code, err := r.store.Arm()
	require.NoError(t, err)
	body, _ := json.Marshal(wire.PairRequest{Code: code.Code, DeviceName: "Pixel"})
	js := "application/json"

	resp := r.post("/api/v1/pair", body, map[string]string{"Content-Type": js})
	require.Equal(t, http.StatusForbidden, resp.StatusCode, "no Origin")
	require.Equal(t, wire.CodeForbidden, pairError(t, resp))

	resp = r.post("/api/v1/pair", body, map[string]string{"Content-Type": js, "Origin": "http://evil.example"})
	require.Equal(t, http.StatusForbidden, resp.StatusCode, "another Origin")

	resp = r.post("/api/v1/pair", body, map[string]string{"Content-Type": js, "Origin": r.ts.URL, "Sec-Fetch-Site": "cross-site"})
	require.Equal(t, http.StatusForbidden, resp.StatusCode, "Sec-Fetch-Site says otherwise")

	resp = r.post("/api/v1/pair", body, map[string]string{"Content-Type": "text/plain", "Origin": r.ts.URL})
	require.Equal(t, http.StatusUnsupportedMediaType, resp.StatusCode, "not JSON")
	require.Equal(t, wire.CodeInvalid, pairError(t, resp))

	big := []byte(`{"code":"` + strings.Repeat("A", wire.MaxPairBodyBytes) + `","device_name":"x"}`)
	resp = r.post("/api/v1/pair", big, map[string]string{"Content-Type": js, "Origin": r.ts.URL})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "over 4 KiB")

	resp = r.pair("ZZZZZ-ZZZZZ", "Pixel")
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "wrong code")
	require.Equal(t, wire.CodeUnauthorized, pairError(t, resp))

	resp = r.pair(code.Code, "Pixel")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var pr wire.PairResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&pr))
	require.NotEmpty(t, pr.Token)
	require.NotEmpty(t, pr.DeviceID)
	require.Equal(t, r.store.ServerID(), pr.ServerID)
	require.Equal(t, r.clk.now().Add(wire.DeviceKeyExpiry).Truncate(time.Second), pr.ExpiresAt)

	var paired wire.Device
	for paired.ID != pr.DeviceID { // its own pairing may be announced first
		paired = unmarshal[wire.Device](t, old.event(wire.EvDevicePaired).P)
	}
	require.Equal(t, "Pixel", paired.Name)
	require.False(t, paired.Self)

	_, welcome := r.welcome(pr.Token, nil)
	require.Equal(t, "Pixel", welcome.Welcome.Device.Name)
	require.Equal(t, pr.DeviceID, welcome.Welcome.Device.ID)
}

func TestR7_PairLockoutIsPerSource(t *testing.T) {
	r := newRig(t)
	const bad, good = "10.0.0.1:4000", "10.0.0.2:4000"
	for i := 0; i < auth.SourceFailures; i++ {
		resp := r.pairFrom("ZZZZZ-ZZZZZ", "x", bad)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	}
	resp := r.pairFrom("ZZZZZ-ZZZZZ", "x", bad)
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	require.Equal(t, wire.CodeRateLimited, pairError(t, resp))
	require.NotEmpty(t, resp.Header.Get("Retry-After"))

	// Another source pairs while the first is locked: the lockout is the
	// source's, never a global switch.
	code, err := r.store.Arm()
	require.NoError(t, err)
	resp = r.pairFrom(code.Code, "Pixel", good)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp = r.pairFrom(code.Code, "Pixel", bad)
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode, "still locked")

	// A valid token from the locked source still connects (R1).
	tok, _ := r.token("Pixel")
	ws, _, err := r.connectFrom(r.ts.URL, bad)
	require.NoError(t, err)
	require.NoError(t, ws.WriteJSON(wire.Frame{T: wire.FrameHello, Hello: &wire.Hello{V: wire.Protocol, Token: tok}}))
	c := &client{t: t, ws: ws}
	require.Equal(t, wire.FrameWelcome, c.read().T)

	r.clk.advance(auth.SourceLockout + time.Second)
	code, err = r.store.Arm()
	require.NoError(t, err)
	resp = r.pairFrom(code.Code, "Pixel", bad)
	require.Equal(t, http.StatusOK, resp.StatusCode, "the lockout ends")
}
