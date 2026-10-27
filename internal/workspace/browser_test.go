package workspace

import (
	"context"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/permission"
	"github.com/stretchr/testify/require"
)

func TestBrowserOriginAlwaysAsksEvenWhenPermissionsAreSkipped(t *testing.T) {
	w, a, sid := newAskWorkspace(t)
	a.Permissions = permission.NewPermissionService(t.TempDir(), true, []string{"browser_origin"})
	a.Permissions.AutoApproveSession(sid)
	sub := a.Permissions.Subscribe(t.Context())
	out := make(chan bool, 1)
	go func() { out <- w.ApproveBrowserOrigin(t.Context(), sid, "codex", "https://example.test") }()
	select {
	case event := <-sub:
		req := event.Payload
		require.Equal(t, "browser_origin", req.ToolName)
		require.Equal(t, "https://example.test", req.Path)
		select {
		case <-out:
			t.Fatal("origin prompt bypassed")
		default:
		}
		require.True(t, a.Permissions.Grant(req))
		require.True(t, <-out)
	case <-time.After(time.Second):
		t.Fatal("origin approval not shown")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.False(t, w.ApproveBrowserOrigin(ctx, sid, "codex", "https://another.test"))
	require.False(t, w.ApproveBrowserOrigin(t.Context(), "missing", "codex", "https://example.test"))
}
