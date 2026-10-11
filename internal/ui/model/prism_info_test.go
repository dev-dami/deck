package model

import (
	"testing"

	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/message"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/ui/chat"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/dialog"
	"github.com/dev-dami/deck/internal/workspace"
	"github.com/stretchr/testify/require"
)

// prismWorkspace is a minimal [workspace.Workspace] stub providing the
// methods the assistant-info rendering path needs.
type prismWorkspace struct {
	workspace.Workspace
}

func (w *prismWorkspace) Config() *config.Config { return &config.Config{} }
func (w *prismWorkspace) WorkingDir() string     { return "/" }

func newPrismTestUI() *UI {
	com := common.DefaultCommon(&prismWorkspace{})
	return &UI{
		com:     com,
		status:  NewStatus(com, nil),
		chat:    NewChat(com, config.ScrollbarDefault),
		state:   uiChat,
		focus:   uiFocusEditor,
		width:   140,
		height:  45,
		session: &session.Session{ID: "s1"},
		keyMap:  DefaultKeyMap(),
		dialog:  dialog.NewOverlay(),
	}
}

func prismToolTurn(finished bool, prismName string) message.Message {
	parts := []message.ContentPart{
		message.ToolCall{ID: "tc1", Name: "bash", Input: "{}", Finished: true},
	}
	if finished {
		parts = append(parts, message.Finish{Reason: message.FinishReasonToolUse, Time: 1735689600})
	}
	return message.Message{
		ID:             "a1",
		SessionID:      "s1",
		Role:           message.Assistant,
		Model:          "prism-model",
		Provider:       "test",
		Parts:          parts,
		PrismModelName: prismName,
	}
}

// TestUpdateSessionMessage_NoPrismInfoWithoutName ensures intermediate turns
// without a Prism-routed model name do not get an info item.
func TestUpdateSessionMessage_NoPrismInfoWithoutName(t *testing.T) {
	m := newPrismTestUI()

	_ = m.appendSessionMessage(prismToolTurn(false, ""))
	_ = m.updateSessionMessage(prismToolTurn(true, ""))
	require.Nil(t, m.chat.MessageItem(chat.AssistantInfoID("a1")))
}
