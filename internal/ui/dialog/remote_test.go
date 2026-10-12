package dialog

import (
	"context"
	"image"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/remote"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/styles"
)

type nullBackend struct{ remote.Backend }

func (nullBackend) Events(context.Context) <-chan remote.Event { return nil }

// The dialog reads the process-wide active server, so these tests do not run
// in parallel.
func startActive(t *testing.T) *remote.Running {
	t.Helper()
	r, err := remote.Start(t.Context(), nullBackend{}, remote.RunOptions{Dir: t.TempDir()})
	require.NoError(t, err)
	remote.SetActive(r)
	t.Cleanup(func() {
		remote.SetActive(nil)
		r.Stop()
	})
	return r
}

func pairDevice(t *testing.T, r *remote.Running, name string) string {
	t.Helper()
	code, err := r.Store.NewCode()
	require.NoError(t, err)
	_, dev, err := r.Store.Pair(code, name, "")
	require.NoError(t, err)
	return dev.ID
}

func newRemoteDialog() *Remote {
	sty := styles.DeckDark()
	return NewRemote(&common.Common{Styles: &sty})
}

func remoteView(d *Remote, w, h int) string {
	scr := uv.NewScreenBuffer(w, h)
	d.Draw(scr, image.Rect(0, 0, w, h))
	return ansi.Strip(scr.Render())
}

func TestRemoteDialogWhenNotRunning(t *testing.T) {
	remote.SetActive(nil)
	d := newRemoteDialog()
	require.Equal(t, RemoteID, d.ID())
	require.Contains(t, remoteView(d, 100, 40), "deck --remote")
	require.IsType(t, ActionClose{}, d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape}))
}

func TestRemoteDialogShowsAddressQRAndDevices(t *testing.T) {
	r := startActive(t)
	pairDevice(t, r, "Pixel 9")
	d := newRemoteDialog()

	view := remoteView(d, 100, 60)
	require.Contains(t, view, r.Origin())
	require.Contains(t, view, "#pair=")
	require.Contains(t, view, "█", "the QR is drawn when there is room")
	require.Contains(t, view, "Pixel 9")
	require.Contains(t, view, "This machine only", "a loopback-only server says how to reach it from a phone")
}

func TestRemoteDialogDropsTheQRInASmallTerminal(t *testing.T) {
	r := startActive(t)
	d := newRemoteDialog()
	view := remoteView(d, 100, 24)
	require.NotContains(t, view, "█")
	require.Contains(t, view, "too small for the QR")
	require.Contains(t, view, r.Origin()+"/#pair=", "the link is still there")
}

func TestRemoteDialogNewCodeChangesTheLink(t *testing.T) {
	startActive(t)
	d := newRemoteDialog()
	before := d.link
	require.NotEmpty(t, before)
	d.HandleMsg(tea.KeyPressMsg{Code: 'n', Text: "n"})
	require.NotEqual(t, before, d.link)
	require.True(t, strings.Contains(d.link, "#pair="))
}

func TestRemoteDialogRevokesTheSelectedDevice(t *testing.T) {
	r := startActive(t)
	pairDevice(t, r, "First")
	second := pairDevice(t, r, "Second")
	d := newRemoteDialog()
	require.Len(t, d.devices, 2)

	d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Equal(t, 1, d.sel)
	d.HandleMsg(tea.KeyPressMsg{Code: 'x', Text: "x"})

	devs, err := r.Store.List()
	require.NoError(t, err)
	require.Len(t, devs, 1)
	require.NotEqual(t, second, devs[0].ID)
	require.Len(t, d.devices, 1)
	require.Equal(t, 0, d.sel, "the selection stays on a device that exists")
}

func TestRemoteDialogRevokeWithNoDevicesIsHarmless(t *testing.T) {
	startActive(t)
	d := newRemoteDialog()
	require.NotPanics(t, func() { d.HandleMsg(tea.KeyPressMsg{Code: 'x', Text: "x"}) })
	require.Contains(t, remoteView(d, 100, 60), "none yet")
}

func TestSeen(t *testing.T) {
	require.Equal(t, "never", seen(zeroTime))
	require.Equal(t, "just now", seen(nowMinus(10)))
	require.Equal(t, "5m ago", seen(nowMinus(5*60+5)))
	require.Equal(t, "3h ago", seen(nowMinus(3*3600+5)))
}

var zeroTime time.Time

// nowMinus is a moment the given number of seconds ago.
func nowMinus(seconds int) time.Time { return time.Now().Add(-time.Duration(seconds) * time.Second) }
