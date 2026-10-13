package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/auth"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

func TestR17_SocketRequiresThePageOrigin(t *testing.T) {
	r := newRig(t)
	_, resp, err := r.connect("")
	require.Error(t, err, "no Origin")
	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	_, resp, err = r.connect("http://evil.example")
	require.Error(t, err, "another Origin")
	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	tok, _ := r.token("Pixel")
	_, welcome := r.welcome(tok, nil)
	require.Equal(t, wire.Protocol, welcome.Welcome.Server.Protocol)
	require.Equal(t, "proj", welcome.Welcome.Server.Project)
	require.Equal(t, r.store.ServerID(), welcome.Welcome.Server.ServerID)
	require.Equal(t, r.srv.Hub().Epoch(), welcome.Welcome.Epoch)
	require.Equal(t, wire.TunnelNone, welcome.Welcome.Server.Tunnel)
	require.NotEmpty(t, welcome.Welcome.Server.Features)
	require.Equal(t, []string{"claude", "codex"}, welcome.Welcome.Server.Kinds)
	require.Equal(t, "#5fd7af", welcome.Welcome.Server.Theme.Palette["primary"], "the theme comes from the backend")
	require.Equal(t, "✻", welcome.Welcome.Server.Theme.Kinds["claude"].Mark)
	require.False(t, welcome.Welcome.Server.AllowYolo)
	require.False(t, welcome.Welcome.Replay)
}

func TestR1_BadHellosNeverBlockAValidToken(t *testing.T) {
	r := newRig(t)
	tok, _ := r.token("Pixel")

	// hello.v must be 2 (R11).
	ws, _, err := r.connect(r.ts.URL)
	require.NoError(t, err)
	require.NoError(t, ws.WriteJSON(wire.Frame{T: wire.FrameHello, Hello: &wire.Hello{V: 1, Token: tok}}))
	code, _ := closeCode(t, ws)
	require.Equal(t, wire.CloseBadHello, code)

	ws, _, err = r.connect(r.ts.URL)
	require.NoError(t, err)
	require.NoError(t, ws.WriteJSON(wire.Frame{T: wire.FrameReq, ID: "1", M: wire.MSessionsList}))
	code, _ = closeCode(t, ws)
	require.Equal(t, wire.CloseBadHello, code, "the first frame must be a hello")

	// Wrong tokens: token_invalid until the source is locked, then
	// rate_limited with a retry-after in the reason.
	for i := 0; i < auth.SourceFailures; i++ {
		c, _ := r.dialRaw("nobody.secret")
		code, reason := closeCode(t, c)
		require.Equal(t, wire.CloseTokenInvalid, code)
		require.Equal(t, wire.CodeTokenInvalid, reason)
	}
	c, _ := r.dialRaw("nobody.secret")
	code, reason := closeCode(t, c)
	require.Equal(t, wire.CloseRateLimited, code)
	secs, err := strconv.Atoi(reason)
	require.NoError(t, err, "the reason is the seconds to wait")
	require.Greater(t, secs, 0)

	// The valid token from the same locked source is checked first and
	// gets its welcome: no global lockout, no collateral logout.
	_, welcome := r.welcome(tok, nil)
	require.Equal(t, "Pixel", welcome.Welcome.Device.Name)

	// A revoked device's token is token_invalid, not rate_limited.
	r.clk.advance(auth.SourceLockout + time.Second)
	tok2, dev2 := r.token("Gone")
	require.NoError(t, r.store.Revoke(dev2.ID))
	c, _ = r.dialRaw(tok2)
	code, reason = closeCode(t, c)
	require.Equal(t, wire.CloseTokenInvalid, code)
	require.Equal(t, wire.CodeTokenInvalid, reason)

	// The device lockout: wrong secrets for one real device id from many
	// sources lock that id, not the sources and not unknown ids.
	_, dev := r.token("Target")
	for i := 0; i < auth.DeviceFailures; i++ {
		c, _ := r.dialRawFrom(dev.ID+".wrong", "10.1.0."+strconv.Itoa(i+1)+":1")
		code, _ := closeCode(t, c)
		require.Equal(t, wire.CloseTokenInvalid, code)
	}
	const fresh = "10.2.0.1:1"
	c, _ = r.dialRawFrom(dev.ID+".wrong", fresh)
	code, _ = closeCode(t, c)
	require.Equal(t, wire.CloseRateLimited, code, "the device is locked from a fresh source")
	c, _ = r.dialRawFrom("nobody.wrong", fresh)
	code, _ = closeCode(t, c)
	require.Equal(t, wire.CloseTokenInvalid, code, "the fresh source itself is not locked")
	c, _ = r.dialRawFrom(tok, fresh)
	require.Equal(t, wire.FrameWelcome, (&client{t: t, ws: c}).read().T, "another device's valid token is unaffected")
}

// dialRaw connects and sends a hello without reading the reply.
func (r *rig) dialRaw(token string) (*websocket.Conn, error) { return r.dialRawFrom(token, "") }

// dialRawFrom is dialRaw as seen from source.
func (r *rig) dialRawFrom(token, source string) (*websocket.Conn, error) {
	r.t.Helper()
	ws, _, err := r.connectFrom(r.ts.URL, source)
	require.NoError(r.t, err)
	return ws, ws.WriteJSON(wire.Frame{T: wire.FrameHello, Hello: &wire.Hello{V: wire.Protocol, Token: token}})
}

func TestR1_StoreErrorsAreNotTokenInvalid(t *testing.T) {
	r := newRig(t)
	tok, _ := r.token("Pixel")
	path := filepath.Join(r.store.Dir(), auth.DevicesFile)
	good, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))

	c, _ := r.dialRaw(tok)
	code, _ := closeCode(t, c)
	require.Equal(t, websocket.CloseInternalServerErr, code, "an unreadable store is 1011, never 4401")
	require.Zero(t, r.srv.limits.Tracked(), "and counts against nobody")

	require.NoError(t, os.WriteFile(path, good, 0o600))
	r.welcome(tok, nil)
}

func TestRevokedBetweenVerifyAndTrack(t *testing.T) {
	r := newRig(t)
	tok, dev := r.token("Pixel")
	r.welcome(tok, nil) // the snapshot knows the device
	r.srv.beforeTrack = func() { require.NoError(t, r.store.Revoke(dev.ID)) }
	c, _ := r.dialRaw(tok)
	code, _ := closeCode(t, c)
	require.Equal(t, wire.CloseRevoked, code, "closed before it was ever tracked")
	// The first connection goes when the watcher sees the revoke.
	require.Eventually(t, func() bool { return r.srv.Connections(dev.ID) == 0 }, 3*time.Second, 20*time.Millisecond)
}

func TestR4_OversizedPreAuthFrameIsRefused(t *testing.T) {
	r := newRig(t)
	tok, _ := r.token("Pixel")
	ws, _, err := r.connect(r.ts.URL)
	require.NoError(t, err)
	pad := strings.Repeat("x", wire.MaxHelloBytes)
	err = ws.WriteMessage(websocket.TextMessage, []byte(`{"t":"hello","v":2,"token":"`+tok+`","pad":"`+pad+`"}`))
	require.NoError(t, err)
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	var f wire.Frame
	err = ws.ReadJSON(&f)
	require.Error(t, err, "no welcome for a 4 KiB hello")
	var ce *websocket.CloseError
	require.ErrorAs(t, err, &ce)
	require.Contains(t, []int{wire.CloseBadHello, websocket.CloseMessageTooBig}, ce.Code)

	// After the hello the limit is 12 MiB: a 1 MiB request goes through.
	c, _ := r.welcome(tok, nil)
	big := strings.Repeat("y", 1<<20)
	res := c.call(wire.MChatSend, wire.ChatSendParams{SessionID: "s1", Text: big})
	okRes(t, res)
	require.Len(t, r.be.Sent(), 1)
}

func TestR4_UnauthenticatedConnectionsAreCapped(t *testing.T) {
	r := newRig(t)
	var idle []*websocket.Conn
	for i := 0; i < wire.MaxUnauthPerSource; i++ {
		ws, _, err := r.connect(r.ts.URL)
		require.NoError(t, err)
		idle = append(idle, ws)
	}
	_, resp, err := r.connect(r.ts.URL)
	require.Error(t, err, "a fifth silent connection from the same source is refused at upgrade")
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)

	// A slot frees when a connection authenticates or ends.
	tok, _ := r.token("Pixel")
	idle[0].Close()
	require.Eventually(t, func() bool {
		ws, _, err := r.connect(r.ts.URL)
		if err != nil {
			return false
		}
		ws.Close()
		return true
	}, 2*time.Second, 20*time.Millisecond)
	for _, ws := range idle[1:] {
		require.NoError(t, ws.WriteJSON(wire.Frame{T: wire.FrameHello, Hello: &wire.Hello{V: wire.Protocol, Token: tok}}))
		var f wire.Frame
		_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
		require.NoError(t, ws.ReadJSON(&f))
		require.Equal(t, wire.FrameWelcome, f.T)
	}
	require.Eventually(t, func() bool {
		r.srv.mu.Lock()
		defer r.srv.mu.Unlock()
		return r.srv.unauthTotal == 0
	}, 2*time.Second, 20*time.Millisecond, "authenticated connections hold no pre-auth slot")

	// The total cap holds across sources: 32 silent sockets from eight
	// sources, and a ninth source is refused.
	var silent []*websocket.Conn
	for src := 0; src < wire.MaxUnauthTotal/wire.MaxUnauthPerSource; src++ {
		for i := 0; i < wire.MaxUnauthPerSource; i++ {
			ws, _, err := r.connectFrom(r.ts.URL, "10.9."+strconv.Itoa(src)+".1:1")
			require.NoError(t, err)
			silent = append(silent, ws)
		}
	}
	_, resp, err = r.connectFrom(r.ts.URL, "10.9.99.1:1")
	require.Error(t, err)
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	for _, ws := range silent {
		ws.Close()
	}
	require.Eventually(t, func() bool {
		r.srv.mu.Lock()
		defer r.srv.mu.Unlock()
		return r.srv.unauthTotal == 0
	}, 3*time.Second, 20*time.Millisecond)

	// The hello deadline closes a silent socket with 4400.
	helloTimeout = 200 * time.Millisecond
	t.Cleanup(func() { helloTimeout = wire.HelloTimeout })
	ws, _, err := r.connect(r.ts.URL)
	require.NoError(t, err)
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _, err = ws.ReadMessage()
	var ce *websocket.CloseError
	require.ErrorAs(t, err, &ce)
	require.Equal(t, wire.CloseBadHello, ce.Code)
}

func TestR3_R6_RevocationThemeAndClose(t *testing.T) {
	r := newRig(t)
	tok, dev := r.token("Pixel")
	tok2, _ := r.token("Other")
	c, _ := r.welcome(tok, nil)
	other, _ := r.welcome(tok2, nil)

	theme := wire.Theme{Palette: map[string]string{"primary": "#ffffff"}, Kinds: map[string]wire.KindTone{}}
	r.be.Inject(remote.Event{Name: wire.EvThemeChanged, Payload: theme})
	ev := c.event(wire.EvThemeChanged)
	require.Equal(t, "#ffffff", unmarshal[wire.Theme](t, ev.P).Palette["primary"])

	// `deck remote revoke` in another process: the watcher sees the file
	// change, every phone hears device.revoked and the device is closed
	// with 4403.
	cli, err := auth.Open(r.store.Dir(), auth.WithClock(r.clk.now))
	require.NoError(t, err)
	require.NoError(t, cli.Revoke(dev.ID))
	ev = other.event(wire.EvDeviceRevoked)
	require.Equal(t, dev.ID, unmarshal[wire.DeviceRevoked](t, ev.P).ID)
	require.NotZero(t, ev.Ev.Seq)
	code, _ := c.closeCode()
	require.Equal(t, wire.CloseRevoked, code)
	require.Eventually(t, func() bool { return r.srv.Connections(dev.ID) == 0 }, time.Second, 10*time.Millisecond)

	// A phone may revoke itself: the response comes first, then 4403.
	res := other.call(wire.MDevicesRevoke, wire.IDParams{ID: dev.ID})
	require.Equal(t, wire.CodeForbidden, errCode(t, res), "but not another device (R10)")
	list := unmarshal[[]wire.Device](t, okRes(t, other.call(wire.MDevicesList, nil)))
	require.Len(t, list, 1)
	require.True(t, list[0].Self)
	res = other.call(wire.MDevicesRevoke, wire.IDParams{ID: list[0].ID})
	okRes(t, res)
	code, _ = other.closeCode()
	require.Equal(t, wire.CloseRevoked, code)
}

func TestR15_YoloIsAPerDeviceGrant(t *testing.T) {
	r := newRig(t)
	tok, dev := r.token("Pixel")
	c, welcome := r.welcome(tok, nil)
	require.False(t, welcome.Welcome.Server.AllowYolo)
	res := c.call(wire.MModeSet, wire.ModeSetParams{SessionID: "s1", Mode: wire.ModeYolo})
	require.Equal(t, wire.CodeYoloDisabled, errCode(t, res))
	require.Empty(t, r.be.Modes())

	require.NoError(t, r.store.SetAllowYolo(dev.ID, true))
	res = c.call(wire.MModeSet, wire.ModeSetParams{SessionID: "s1", Mode: wire.ModeYolo})
	require.Equal(t, wire.ModeYolo, unmarshal[wire.ModeResult](t, okRes(t, res)).Mode)
	require.Equal(t, []string{"s1=yolo"}, r.be.Modes())
	res = c.call(wire.MModeGet, wire.SessionParams{SessionID: "s1"})
	require.Equal(t, wire.ModeYolo, unmarshal[wire.ModeResult](t, okRes(t, res)).Mode)

	_, welcome = r.welcome(tok, nil)
	require.True(t, welcome.Welcome.Server.AllowYolo)
	list := unmarshal[[]wire.Device](t, okRes(t, c.call(wire.MDevicesList, nil)))
	require.True(t, list[0].AllowYolo)

	res = c.call(wire.MModeSet, wire.ModeSetParams{SessionID: "s1", Mode: "chaos"})
	require.Equal(t, wire.CodeInvalid, errCode(t, res))
}

func TestInFlightLimitAndUnknownFrames(t *testing.T) {
	r := newRig(t)
	tok, _ := r.token("Pixel")
	c, _ := r.welcome(tok, nil)
	require.NoError(t, c.ws.WriteJSON(map[string]any{"t": "bogus", "id": "z"}))
	f := c.read()
	for f.T == wire.FrameEv {
		f = c.read()
	}
	require.Equal(t, wire.FrameRes, f.T)
	require.Equal(t, wire.CodeInvalid, errCode(t, f), "an unknown frame type is an error, never a crash")
	res := c.call("sessions.explode", nil)
	require.Equal(t, wire.CodeInvalid, errCode(t, res), "unknown method")
	res = c.call(wire.MPushStatus, nil)
	require.JSONEq(t, `{"available":false,"enabled":false,"approvals":false,"done":false}`, string(okRes(t, res)))
	res = c.call(wire.MPushSubscribe, wire.PushSubscribeParams{})
	require.Equal(t, wire.CodeInvalid, errCode(t, res))
}
