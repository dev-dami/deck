package model

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/dev-dami/deck/internal/ui/dialog"
	"github.com/stretchr/testify/require"
)

func TestMobileCommandErrorPreservesWorkflowInstructions(t *testing.T) {
	err := mobileCommandError(&exec.ExitError{Stderr: []byte("\x1b[31mError: Local runtime missing; set --relay-dir PATH\x1b[0m")})
	require.Contains(t, err.Error(), "--relay-dir PATH")
	require.NotContains(t, err.Error(), "\x1b")
	err = mobileCommandError(&exec.ExitError{Stderr: []byte("Error connecting to https://example/#p=SECRET")})
	require.NotContains(t, err.Error(), "SECRET")
}

func TestMobileCommandExecutionDeferredAndSecretErrorsSanitized(t *testing.T) {
	called := false
	run := func(ctx context.Context, project string, args []string) ([]byte, error) {
		called = true
		require.Equal(t, "project", project)
		require.Equal(t, mobileConnectArgs("project", "pair", false), args)
		_, deadline := ctx.Deadline()
		require.True(t, deadline)
		return nil, errors.New("private pairing URL https://example/#psk=SECRET")
	}
	cmd := mobileConnectCmdUsing(nil, 4, "project", "pair", false, run)
	require.False(t, called)
	result := cmd().(dialog.MobileResultMsg)
	require.True(t, called)
	require.Equal(t, uint64(4), result.Request)
	require.Error(t, result.Err)
	require.NotContains(t, result.Err.Error(), "SECRET")
	require.NotContains(t, result.Err.Error(), "https://")
}

func TestMobileCommandParsesJSONAndRendersQR(t *testing.T) {
	cmd := mobileConnectCmdUsing(nil, 1, "project", "pair", false, func(context.Context, string, []string) ([]byte, error) {
		return []byte(`{"name":"PC","online":true,"devices":2,"projects":1,"pair_url":"https://example/#pair=secret","expires_at":"2099-01-01T00:00:00Z"}`), nil
	})
	result := cmd().(dialog.MobileResultMsg)
	require.NoError(t, result.Err)
	require.NotEmpty(t, result.QR)
	require.Equal(t, 2, result.Status.Devices)
	cmd = mobileConnectCmdUsing(nil, 1, "project", "pair", false, func(context.Context, string, []string) ([]byte, error) { return []byte("SECRET malformed"), nil })
	result = cmd().(dialog.MobileResultMsg)
	require.Error(t, result.Err)
	require.NotContains(t, result.Err.Error(), "SECRET")
}

func TestMobileConnectArgs(t *testing.T) {
	args := mobileConnectArgs("C:/project with spaces", "start", false)
	require.Equal(t, []string{"mobile", "connect", "--json", "--project", "C:/project with spaces", "--action", "start"}, args)
	require.NotContains(t, args, "--lan")
	require.Contains(t, mobileConnectArgs("project", "start", true), "--lan")
	require.Equal(t, []string{"mobile", "connect", "--json", "--project", "project", "--action", "revoke", "--device", "phone_2"}, mobileConnectArgs("project", "revoke:phone_2", false))
}

func TestMobileSlashActions(t *testing.T) {
	for _, tc := range []struct {
		input, action string
		lan           bool
	}{{"/mobile lan", "start", true}, {"/mobile start", "start", false}, {"/mobile pair", "pair", false}, {"/mobile stop", "stop", false}, {"/remote", "start", true}, {"/link", "link", false}} {
		action, lan, ok := mobileSlashAction(tc.input)
		require.True(t, ok)
		require.Equal(t, tc.action, action)
		require.Equal(t, tc.lan, lan)
	}
}

func TestMobileSlashOpensPanelWithoutSendingPrompt(t *testing.T) {
	for _, input := range []string{"/mobile", "/remote", "/link", " /mobile status ", "/mobile pair", "/mobile lan", "/mobile start", "/mobile stop"} {
		m := newTestUIWithConfig(t, nil)
		m.dialog = dialog.NewOverlay()
		cmd := m.sendMessage(input)
		require.NotNil(t, cmd)
		require.True(t, m.dialog.ContainsDialog(dialog.MobileID))
		require.Empty(t, m.com.Workspace.(*testWorkspace).runPrompts)
	}
	for _, input := range []string{"/mobile arbitrary", "please /remote", "/remotely"} {
		require.False(t, isMobileSlash(input))
	}
}

func TestMobileTargetedResultDroppedAfterClose(t *testing.T) {
	m := newTestUIWithConfig(t, nil)
	m.dialog = dialog.NewOverlay()
	m.openMobileDialog()
	owner := m.dialog.Dialog(dialog.MobileID).(*dialog.Mobile)
	m.dialog.CloseDialog(dialog.MobileID)
	_, cmd := m.Update(dialog.MobileResultMsg{Owner: owner, Request: 1, Status: dialog.MobileStatus{PairURL: "private"}})
	// Routing to a closed dialog must produce no notice or reopen it.
	require.False(t, m.dialog.HasDialogs())
	if cmd != nil {
		require.IsType(t, tea.BatchMsg{}, cmd())
	}
}
