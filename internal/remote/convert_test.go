package remote

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/message"
	"github.com/dev-dami/deck/internal/permission"
	"github.com/dev-dami/deck/internal/remote/wire"
	tools "github.com/dev-dami/deck/internal/toolspec"
)

func TestToMessageParts(t *testing.T) {
	m := message.Message{
		ID: "m1", SessionID: "s1", Role: message.Assistant, Model: "claude", Provider: "claude",
		CreatedAt: 1, UpdatedAt: 2,
		Parts: []message.ContentPart{
			message.TextContent{Text: "hello"},
			message.TextContent{Text: "secret continuation", Hidden: true},
			message.ReasoningContent{Thinking: "hmm"},
			message.ToolCall{ID: "t1", Name: "bash", Input: `{"command":"go test ./..."}`},
			message.ToolResult{ToolCallID: "t1", Name: "bash", Content: "ok", IsError: false},
			message.BinaryContent{Path: "a.png", MIMEType: "image/png", Data: []byte{1, 2, 3}},
			message.Finish{Reason: message.FinishReasonEndTurn},
		},
	}
	got := toMessage(m)
	require.Equal(t, "claude", got.Agent)
	require.Equal(t, wire.RoleAssistant, got.Role)
	require.True(t, got.Finished)
	require.Equal(t, []wire.Part{
		{Type: wire.PartText, Text: "hello"},
		{Type: wire.PartReasoning, Text: "hmm"},
		{Type: wire.PartToolCall, ID: "t1", Name: "bash", Summary: "go test ./..."},
		{Type: wire.PartToolResult, ToolCallID: "t1", Name: "bash", Output: "ok"},
		{Type: wire.PartImage, Name: "a.png", Mime: "image/png"},
	}, got.Parts)
}

func TestToMessageNeverShipsImageBytes(t *testing.T) {
	m := message.Message{Role: message.User, Parts: []message.ContentPart{
		message.BinaryContent{Path: "a.png", MIMEType: "image/png", Data: []byte("PIXELS")},
	}}
	require.NotContains(t, strings.Join([]string{toMessage(m).Parts[0].Output, toMessage(m).Parts[0].Text}, ""), "PIXELS")
}

func TestToMessageDeckRowsHaveNoAgent(t *testing.T) {
	got := toMessage(message.Message{Role: message.Assistant, Model: "deck", Provider: "deck",
		Parts: []message.ContentPart{message.TextContent{Text: "claude joined"}}})
	require.Empty(t, got.Agent)
}

func TestToMessageUnfinishedAssistantStreams(t *testing.T) {
	got := toMessage(message.Message{Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "par"}}})
	require.False(t, got.Finished)
	require.True(t, toMessage(message.Message{Role: message.User}).Finished, "user rows are never streaming")
}

func TestToolResultTruncates(t *testing.T) {
	big := strings.Repeat("é", wire.MaxOutputBytes) // 2 bytes per rune: cut must not split one
	got := toMessage(message.Message{Role: message.Assistant, Parts: []message.ContentPart{
		message.ToolResult{ToolCallID: "t", Name: "bash", Content: big},
	}}).Parts[0]
	require.True(t, got.Truncated)
	require.LessOrEqual(t, len(got.Output), wire.MaxOutputBytes)
	require.True(t, strings.HasSuffix(got.Output, "é"), "cut on a rune boundary")
}

func TestToolResultCarriesEditDiff(t *testing.T) {
	got := toMessage(message.Message{Role: message.Assistant, Parts: []message.ContentPart{
		message.ToolResult{ToolCallID: "t", Name: "a.go", Content: "edited",
			Metadata: `{"additions":1,"removals":1,"old_content":"a\n","new_content":"b\n"}`},
	}}).Parts[0]
	require.Contains(t, got.Diff, "-a")
	require.Contains(t, got.Diff, "+b")
}

func TestToolSummary(t *testing.T) {
	require.Equal(t, "ls -la", toolSummary(`{"command":"ls -la","description":"list"}`))
	require.Equal(t, "a.go", toolSummary(`{"file_path":"a.go"}`))
	require.Equal(t, "not json", toolSummary("not json"))
	require.Len(t, toolSummary(strings.Repeat("x", 500)), 120)
}

func TestApprovalCommand(t *testing.T) {
	a := toApproval(permission.PermissionRequest{
		ID: "p1", SessionID: "s1", ToolCallID: "c1", Description: "claude wants to run",
		Params: tools.BashPermissionsParams{Command: "rm -rf build", WorkingDir: "/w", Description: "claude"},
	}, 9)
	require.Equal(t, wire.Approval{
		ID: "p1", SessionID: "s1", ToolCallID: "c1", Agent: "claude", Kind: wire.ApprovalCommand,
		Description: "claude wants to run", Command: "rm -rf build", Cwd: "/w", CreatedAt: 9,
	}, a)
}

func TestApprovalEditShowsFullDiff(t *testing.T) {
	a := toApproval(permission.PermissionRequest{
		ID: "p2", Params: tools.EditPermissionsParams{FilePath: "a.go", OldContent: "one\n", NewContent: "two\n"},
	}, 1)
	require.Equal(t, wire.ApprovalEdit, a.Kind)
	require.Equal(t, "a.go", a.Path)
	require.Contains(t, a.Diff, "-one")
	require.Contains(t, a.Diff, "+two")
}

func TestApprovalHugeDiffIsCapped(t *testing.T) {
	big := strings.Repeat("line\n", wire.MaxDiffBytes)
	a := toApproval(permission.PermissionRequest{
		Params: tools.EditPermissionsParams{FilePath: "a.go", OldContent: "", NewContent: big},
	}, 1)
	require.LessOrEqual(t, len(a.Diff), wire.MaxDiffBytes)
}

func TestApprovalOtherKinds(t *testing.T) {
	s := toApproval(permission.PermissionRequest{Params: `{"agent":"codex","request":"fetch"}`}, 1)
	require.Equal(t, wire.ApprovalOther, s.Kind)
	require.Contains(t, s.Raw, "fetch")

	odd := toApproval(permission.PermissionRequest{Params: struct{ X int }{3}}, 1)
	require.Equal(t, wire.ApprovalOther, odd.Kind)
	require.Contains(t, odd.Raw, `"X":3`)

	nilParams := toApproval(permission.PermissionRequest{}, 1)
	require.Equal(t, wire.ApprovalOther, nilParams.Kind)
}
