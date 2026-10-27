package adapter

import (
	"github.com/dev-dami/deck/internal/permission"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestBrowserOriginApprovalKeepsOriginAndAgent(t *testing.T) {
	req := permission.PermissionRequest{ID: "origin", SessionID: "room", ToolName: "browser_origin", Params: `{"agent":"codex","origin":"https://example.test"}`}
	got := toApproval(req, time.Now(), "/project")
	require.Equal(t, "browser_origin", got.Kind)
	require.Equal(t, "https://example.test", got.Origin)
	require.Equal(t, "codex", got.Agent)
	req.ToolName = "unrelated"
	require.Equal(t, "other", toApproval(req, time.Now(), "/project").Kind)
}
