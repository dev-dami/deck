package dialog

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/styles"
	"github.com/stretchr/testify/require"
)

func TestMobilePaletteActions(t *testing.T) {
	s := styles.DeckDark()
	com := &common.Common{Styles: &s, Workspace: &settingsWorkspace{cfg: &config.Config{Options: &config.Options{}}}}
	c, err := NewCommands(com, "", false, false, false, nil)
	require.NoError(t, err)
	want := map[string]string{"mobile": MobileID, "mobile_pair": MobilePairID, "mobile_start": MobileStartID, "mobile_lan": MobileLANID, "mobile_stop": MobileStopID, "remote": MobileLANID, "link": MobilePairID}
	for _, item := range c.defaultCommands() {
		if id, ok := want[item.id]; ok {
			require.Equal(t, ActionOpenDialog{DialogID: id}, item.action)
			delete(want, item.id)
		}
	}
	require.Empty(t, want)
}

func TestMobileCommandsAreDeferredAndLANExplicit(t *testing.T) {
	for _, tc := range []struct {
		key, action string
		lan         bool
	}{{"s", "start", false}, {"l", "start", true}, {"p", "link", false}, {"r", "status", false}, {"x", "stop", false}} {
		t.Run(tc.key, func(t *testing.T) {
			calls := 0
			m := NewMobile(nil, "project", func(owner *Mobile, request uint64, action string, lan bool) tea.Cmd {
				require.Equal(t, tc.action, action)
				require.Equal(t, tc.lan, lan)
				return func() tea.Msg { calls++; return MobileResultMsg{Owner: owner, Request: request} }
			})
			a := m.HandleMsg(tea.KeyPressMsg{Code: rune(tc.key[0]), Text: tc.key}).(ActionCmd)
			require.Zero(t, calls)
			require.True(t, m.busy)
			m.HandleMsg(a.Cmd())
			require.Equal(t, 1, calls)
			require.False(t, m.busy)
		})
	}
}

func TestMobileIgnoresClosedReopenedAndStaleResults(t *testing.T) {
	run := func(*Mobile, uint64, string, bool) tea.Cmd { return nil }
	old := NewMobile(nil, "project", run)
	old.Start("status", false)
	o := NewOverlay(old)
	o.CloseFrontDialog()
	fresh := NewMobile(nil, "project", run)
	fresh.Start("status", false)
	o.OpenDialog(fresh)
	o.UpdateDialog(MobileID, MobileResultMsg{Owner: old, Request: 1, Status: MobileStatus{PairURL: "secret"}})
	require.True(t, fresh.busy)
	require.Empty(t, fresh.status.PairURL)
	fresh.HandleMsg(MobileResultMsg{Owner: fresh, Request: 0})
	require.True(t, fresh.busy)
	fresh.HandleMsg(MobileResultMsg{Owner: fresh, Request: 1, Status: MobileStatus{PairURL: "secret", ExpiresAt: time.Now().Add(time.Hour)}, QR: "qr"})
	require.False(t, fresh.busy)
	require.Equal(t, "qr", fresh.qr)
	fresh.status.ExpiresAt = time.Now().Add(-time.Second)
	fresh.HandleMsg(mobileTickMsg{fresh, 1})
	require.Empty(t, fresh.qr)
	require.Empty(t, fresh.status.PairURL)
}

func TestMobileSmallLayoutKeepsSecretOffScreen(t *testing.T) {
	s := styles.DeckDark()
	m := NewMobile(&common.Common{Styles: &s}, strings.Repeat("path", 40), nil)
	m.status = MobileStatus{PairURL: "TOPSECRET", ExpiresAt: time.Now().Add(time.Hour)}
	m.qr = strings.Repeat("█", 70)
	body := m.body(30, 9)
	require.NotContains(t, body, "TOPSECRET")
	require.NotContains(t, body, m.qr)
	require.Contains(t, body, "Press c")
	for _, line := range strings.Split(body, "\n") {
		require.LessOrEqual(t, lipgloss.Width(line), 30)
	}
	require.LessOrEqual(t, lipgloss.Height(m.body(8, 3)), 3)
}

func TestDisconnectRequiresSelectingAndConfirmingExactPhone(t *testing.T) {
	var action string
	m := NewMobile(nil, "project", func(_ *Mobile, _ uint64, a string, _ bool) tea.Cmd {
		action = a
		return func() tea.Msg { return nil }
	})
	m.status.DeviceList = []MobileDevice{{ID: "one", Name: "First"}, {ID: "two", Name: "Second"}}
	key := func(k string) Action { return m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter, Text: k}) }
	m.HandleMsg(tea.KeyPressMsg{Code: 'd', Text: "d"})
	require.True(t, m.choosingPhone)
	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	key("enter")
	require.True(t, m.confirmDisconnect)
	require.Empty(t, action)
	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.False(t, m.choosingPhone)
	require.Empty(t, action)
	m.HandleMsg(tea.KeyPressMsg{Code: 'd', Text: "d"})
	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	key("enter")
	require.IsType(t, ActionCmd{}, key("enter"))
	require.Equal(t, "revoke:two", action)
}

func TestMobileShowsManagedStopExplanation(t *testing.T) {
	m := NewMobile(nil, "project", func(*Mobile, uint64, string, bool) tea.Cmd { return nil })
	m.Start("stop", false)
	m.HandleMsg(MobileResultMsg{Owner: m, Request: m.request, Status: MobileStatus{Message: "The separately started daemon is still running; stop it in its own terminal."}})
	require.Contains(t, m.body(100, 20), "separately started daemon")
}
