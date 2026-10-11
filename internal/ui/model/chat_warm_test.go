package model

import (
	"strconv"
	"testing"

	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/ui/chat"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/stretchr/testify/require"
)

// countingMessageItem counts its renders.
type countingMessageItem struct {
	id      string
	renders *int
}

func (m countingMessageItem) ID() string { return m.id }
func (m countingMessageItem) Render(int) string {
	*m.renders++
	return "line\nline"
}
func (m countingMessageItem) RawRender(w int) string { return m.Render(w) }
func (m countingMessageItem) Version() uint64        { return 0 }
func (m countingMessageItem) Finished() bool         { return true }

// TestChatWarm_FirstScrollRendersNothing asserts that once a warm started
// after loading messages has run, the scrollbar's total-height scan on the
// first scroll renders no message: the whole transcript is in the cache.
func TestChatWarm_FirstScrollRendersNothing(t *testing.T) {
	t.Parallel()

	c := NewChat(common.DefaultCommon(nil), config.ScrollbarDefault)
	c.SetSize(80, 20)
	renders := 0
	items := make([]chat.MessageItem, 300)
	for i := range items {
		items[i] = countingMessageItem{id: strconv.Itoa(i), renders: &renders}
	}
	c.SetMessages(items...)

	cmd := c.Warm()
	require.NotNil(t, cmd, "a non-empty chat warms")
	steps := 0
	for cmd != nil {
		msg, ok := cmd().(chatWarmMsg)
		require.True(t, ok)
		var done bool
		cmd, done = c.WarmStep(msg.seq)
		steps++
		require.Equal(t, cmd == nil, done, "a live warm continues until it is done")
	}
	require.Greater(t, steps, 1, "warming is spread over several steps")

	renders = 0
	_ = c.list.TotalHeight()
	require.Zero(t, renders, "a warmed transcript renders nothing for the total height")
}

// TestChatWarm_SupersededByResize asserts a resize retires a warm in
// flight, as a later warm does.
func TestChatWarm_SupersededByResize(t *testing.T) {
	t.Parallel()

	c := NewChat(common.DefaultCommon(nil), config.ScrollbarDefault)
	c.SetSize(80, 20)
	renders := 0
	c.SetMessages(countingMessageItem{id: "a", renders: &renders}, countingMessageItem{id: "b", renders: &renders})

	msg := c.Warm()().(chatWarmMsg)
	_ = c.BeginResize()
	cmd, done := c.WarmStep(msg.seq)
	require.Nil(t, cmd)
	require.False(t, done, "a superseded warm is a no-op")
}
