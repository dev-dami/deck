package workspace

import (
	"context"
	"encoding/json"
	"github.com/dev-dami/deck/internal/permission"
	pb "github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// DesktopBrowser is installed only by the local desktop RPC server. The
// agent identity comes from the manager, never from op arguments.
type DesktopBrowser interface {
	InstallBrowser(func(context.Context, string, string, *pb.BrowserCommand) *pb.BrowserActionResponse)
	ApproveBrowserOrigin(context.Context, string, string, string) bool
	CheckBrowserSession(context.Context, string) error
}

func (w *DeckWorkspace) InstallBrowser(run func(context.Context, string, string, *pb.BrowserCommand) *pb.BrowserActionResponse) {
	w.mgr().SetBrowserAction(func(ctx context.Context, thread, owner string, command *pb.BrowserCommand) *pb.BrowserActionResponse {
		sid := w.sessionOf(thread)
		if sid == "" {
			return &pb.BrowserActionResponse{ErrorCode: "browser_unavailable", Result: &pb.OpResult{IsError: true, Text: "This thread has no desktop room."}}
		}
		return run(ctx, sid, owner, command)
	})
}
func (w *DeckWorkspace) CheckBrowserSession(ctx context.Context, sid string) error {
	return w.topSession(ctx, sid)
}
func (w *DeckWorkspace) ApproveBrowserOrigin(ctx context.Context, sid, owner, origin string) bool {
	if w.topSession(ctx, sid) != nil {
		return false
	}
	if owner == "" {
		owner = "user"
	}
	raw, _ := json.Marshal(map[string]string{"agent": owner, "origin": origin})
	ok, err := w.app.Permissions.Request(ctx, permission.CreatePermissionRequest{SessionID: sid, ToolName: "browser_origin", Description: owner + " wants to open " + origin + " in the agent browser", Action: "browse", Path: origin, Params: string(raw), MustAsk: true})
	return err == nil && ok
}
