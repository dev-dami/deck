package server

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

func TestR10_EveryIdIsCheckedAgainstTheSession(t *testing.T) {
	r := newRig(t)
	s1 := r.be.Get("s1")
	s1.Approvals = []wire.Approval{{ID: "a1", SessionID: "s1", Agent: "claude1", Kind: wire.ApprovalCommand, Command: "rm -rf"}}
	s1.Queue = []wire.QueuedItem{{ID: "q1", Agent: "claude1", Text: "later"}}
	s1.Messages = []wire.Message{{ID: "m1", SessionID: "s1", Role: wire.RoleUser, Editable: true, Parts: []wire.Part{}}}
	s1.Roster = []wire.AgentLine{{ID: "claude1", Name: "claude", Kind: "claude"}}
	r.be.AddSession("s2", "Two")
	tok, _ := r.token("Pixel")
	c, _ := r.welcome(tok, nil)

	foreign := []struct {
		method string
		params any
	}{
		{wire.MApprovalsAnswer, wire.ApprovalsAnswerParams{SessionID: "s2", ID: "a1", Decision: wire.DecisionAllow}},
		{wire.MQueueSteer, wire.QueueItemParams{SessionID: "s2", ItemID: "q1"}},
		{wire.MQueueCancel, wire.QueueItemParams{SessionID: "s2", ItemID: "q1"}},
		{wire.MMessageEdit, wire.MessageEditParams{SessionID: "s2", MessageID: "m1", Text: "x"}},
		{wire.MAgentInterrupt, wire.AgentInterruptParams{SessionID: "s2", Agent: "claude1"}},
		{wire.MModeGet, wire.SessionParams{SessionID: "ghost"}},
		{wire.MMessagesList, wire.MessagesListParams{SessionID: "ghost"}},
		{wire.MChatSend, wire.ChatSendParams{SessionID: "ghost", Text: "hi"}},
		{wire.MSessionsDelete, wire.IDParams{ID: "ghost"}},
	}
	for _, f := range foreign {
		res := c.call(f.method, f.params)
		require.Equal(t, wire.CodeNotFound, errCode(t, res), f.method)
	}
	require.Empty(t, r.be.Answered())
	require.Empty(t, r.be.Calls())
	require.Len(t, s1.Queue, 1)

	// The same ids in their own session work.
	res := c.call(wire.MApprovalsAnswer, wire.ApprovalsAnswerParams{SessionID: "s1", ID: "a1", Decision: wire.DecisionAllow})
	require.True(t, unmarshal[wire.ApprovalsAnswerResult](t, okRes(t, res)).Resolved)
	okRes(t, c.call(wire.MQueueSteer, wire.QueueItemParams{SessionID: "s1", ItemID: "q1"}))
	okRes(t, c.call(wire.MMessageEdit, wire.MessageEditParams{SessionID: "s1", MessageID: "m1", Text: "x"}))
	okRes(t, c.call(wire.MAgentInterrupt, wire.AgentInterruptParams{SessionID: "s1", Agent: "claude1"}))
	require.Equal(t, map[string]string{"a1": wire.DecisionAllow}, r.be.Answered())
	require.Equal(t, []string{"steer:q1", "interrupt:claude1"}, r.be.Calls())

	res = c.call(wire.MModeGet, nil)
	require.Equal(t, wire.CodeInvalid, errCode(t, res), "mode.get needs a session")
}

func TestR11_SeqFocusPagingAndNoGoErrorText(t *testing.T) {
	r := newRig(t)
	s1 := r.be.Get("s1")
	for i := 0; i < 7; i++ {
		s1.Messages = append(s1.Messages, wire.Message{ID: "m" + string(rune('0'+i)), SessionID: "s1", Role: wire.RoleUser, Parts: []wire.Part{}})
	}
	tok, _ := r.token("Pixel")
	c, welcome := r.welcome(tok, nil)

	// messages.list pages by cursor and says whether more exist.
	res := c.call(wire.MMessagesList, wire.MessagesListParams{SessionID: "s1", Limit: 3})
	page := unmarshal[wire.MessagesListResult](t, okRes(t, res))
	require.Len(t, page.Messages, 3)
	require.Equal(t, "m4", page.Messages[0].ID, "the newest page, oldest first")
	require.True(t, page.More)
	require.NotEmpty(t, page.Cursor)
	res = c.call(wire.MMessagesList, wire.MessagesListParams{SessionID: "s1", Limit: 3, Before: page.Cursor})
	page = unmarshal[wire.MessagesListResult](t, okRes(t, res))
	require.Equal(t, "m1", page.Messages[0].ID)
	require.True(t, page.More)
	res = c.call(wire.MMessagesList, wire.MessagesListParams{SessionID: "s1", Limit: 3, Before: page.Cursor})
	page = unmarshal[wire.MessagesListResult](t, okRes(t, res))
	require.Len(t, page.Messages, 1)
	require.False(t, page.More)
	require.Empty(t, page.Cursor)
	res = c.call(wire.MMessagesList, wire.MessagesListParams{SessionID: "s1", Limit: wire.MaxPageLimit + 1})
	require.Equal(t, wire.CodeInvalid, errCode(t, res))
	raw := c.call(wire.MMessagesList, wire.MessagesListParams{SessionID: "s1"})
	require.Contains(t, string(raw.R), `"more":false`, "more is always written")

	// A failing backend never leaks its error text.
	r.be.SendErr = errors.New("boom: C:\\Users\\secret\\deck")
	res = c.call(wire.MChatSend, wire.ChatSendParams{SessionID: "s1", Text: "hi", IdempotencyKey: "k1"})
	require.Equal(t, wire.CodeInternal, errCode(t, res))
	require.NotContains(t, res.Err.Msg, "boom")
	require.NotContains(t, res.Err.Msg, "secret")
	require.LessOrEqual(t, len([]rune(res.Err.Msg)), wire.MaxErrorMsgRunes)

	// A failed send is not remembered as sent: the retry with the same
	// key goes through, and a third call with it is answered from memory.
	r.be.SendErr = nil
	res = c.call(wire.MChatSend, wire.ChatSendParams{SessionID: "s1", Text: "hi", IdempotencyKey: "k1"})
	first := unmarshal[wire.ChatSendResult](t, okRes(t, res))
	res = c.call(wire.MChatSend, wire.ChatSendParams{SessionID: "s1", Text: "hi", IdempotencyKey: "k1"})
	require.Equal(t, first.MessageID, unmarshal[wire.ChatSendResult](t, okRes(t, res)).MessageID)
	require.Len(t, r.be.Sent(), 1)

	// Every event has a seq, in order; device.revoked decodes (R11).
	r.be.Inject(remote.Event{Name: wire.EvBusy, SessionID: "s1", Payload: wire.BusyChanged{SessionID: "s1", Busy: true}})
	ev := c.event(wire.EvBusy)
	require.Equal(t, uint64(1), ev.Ev.Seq)
	require.Equal(t, welcome.Welcome.Epoch, r.srv.Hub().Epoch())

	// Focus is restored from hello.resume (R11): the scoped row arrives
	// without a sessions.focus after the reconnect.
	okRes(t, c.call(wire.MSessionsFocus, wire.IDParams{ID: "s1"}))
	c.ws.Close()
	c2, welcome2 := r.welcome(tok, &wire.Resume{Epoch: welcome.Welcome.Epoch, Seq: 1, Focus: "s1"})
	require.False(t, welcome2.Welcome.Replay)
	r.be.Inject(remote.Event{Name: wire.EvMessageCreated, SessionID: "s1", Scoped: true, Payload: wire.Message{ID: "x", SessionID: "s1", Parts: []wire.Part{}}})
	ev = c2.event(wire.EvMessageCreated)
	require.Equal(t, uint64(2), ev.Ev.Seq)

	// Another epoch is a resync right after the welcome.
	c3, _ := r.welcome(tok, &wire.Resume{Epoch: "stale", Seq: 1})
	require.Equal(t, wire.FrameResync, c3.read().T)
}

func TestR12_CapsAndReplayThroughTheBoundedQueue(t *testing.T) {
	r := newRig(t)
	tok, _ := r.token("Pixel")
	c, welcome := r.welcome(tok, nil)

	long := strings.Repeat("é", wire.MaxTextBytes) // 2 bytes a rune: well over the cap
	msg := wire.Message{ID: "big", SessionID: "s1", Role: wire.RoleAssistant, Parts: []wire.Part{
		{Type: wire.PartText, Text: long},
		{Type: wire.PartReasoning, Text: long},
		{Type: wire.PartToolResult, ToolCallID: "t", Output: strings.Repeat("o", wire.MaxOutputBytes+1), Diff: strings.Repeat("d", wire.MaxDiffBytes+1)},
	}}
	okRes(t, c.call(wire.MSessionsFocus, wire.IDParams{ID: "s1"}))
	r.be.Inject(remote.Event{Name: wire.EvMessageCreated, SessionID: "s1", Scoped: true, Payload: msg})
	got := unmarshal[wire.Message](t, c.event(wire.EvMessageCreated).P)
	require.True(t, got.Parts[0].TextTruncated)
	require.LessOrEqual(t, len(got.Parts[0].Text), wire.MaxTextBytes)
	require.True(t, strings.HasSuffix(got.Parts[0].Text, "é"), "cut on a rune boundary")
	require.True(t, got.Parts[1].TextTruncated)
	require.LessOrEqual(t, len(got.Parts[1].Text), wire.MaxReasoningBytes)
	require.True(t, got.Parts[2].OutputTruncated)
	require.Len(t, got.Parts[2].Output, wire.MaxOutputBytes)
	require.True(t, got.Parts[2].DiffTruncated)
	require.Len(t, got.Parts[2].Diff, wire.MaxDiffBytes)

	r.be.Get("s1").Messages = []wire.Message{msg}
	page := unmarshal[wire.MessagesListResult](t, okRes(t, c.call(wire.MMessagesList, wire.MessagesListParams{SessionID: "s1"})))
	require.True(t, page.Messages[0].Parts[0].TextTruncated, "results are capped like events")

	r.be.Get("s1").Approvals = []wire.Approval{{ID: "a", SessionID: "s1", Command: strings.Repeat("c", wire.MaxCommandBytes+1), Raw: strings.Repeat("r", wire.MaxCommandBytes+1)}}
	apps := unmarshal[[]wire.Approval](t, okRes(t, c.call(wire.MApprovalsList, nil)))
	require.True(t, apps[0].CommandTruncated)
	require.Len(t, apps[0].Command, wire.MaxCommandBytes)
	require.True(t, apps[0].RawTruncated)
	r.be.Get("s1").Queue = []wire.QueuedItem{{ID: "q", Text: long}}
	queue := unmarshal[[]wire.QueuedItem](t, okRes(t, c.call(wire.MQueueList, wire.SessionParams{SessionID: "s1"})))
	require.True(t, queue[0].TextTruncated)

	// Replay goes through the same bounded queue as live events: a gap
	// larger than the queue is a resync, never a 4408; a smaller one is
	// replayed in order.
	last := r.srv.Hub().Seq()
	c.ws.Close()
	for i := 0; i < wire.OutboundQueue+10; i++ {
		r.be.Inject(remote.Event{Name: wire.EvBusy, Payload: wire.BusyChanged{SessionID: "s1"}})
	}
	require.Eventually(t, func() bool { return r.srv.Hub().Seq() == last+uint64(wire.OutboundQueue+10) }, 3*time.Second, 10*time.Millisecond)
	c2, welcome2 := r.welcome(tok, &wire.Resume{Epoch: welcome.Welcome.Epoch, Seq: last})
	require.False(t, welcome2.Welcome.Replay)
	require.Equal(t, wire.FrameResync, c2.read().T)
	okRes(t, c2.call(wire.MSessionsList, nil)) // the connection is alive after the resync

	c3, welcome3 := r.welcome(tok, &wire.Resume{Epoch: welcome.Welcome.Epoch, Seq: r.srv.Hub().Seq() - 5})
	require.True(t, welcome3.Welcome.Replay)
	for i := 4; i >= 0; i-- {
		ev := c3.read()
		require.Equal(t, wire.FrameEv, ev.T)
		require.Equal(t, r.srv.Hub().Seq()-uint64(i), ev.Ev.Seq)
	}
}

func TestChatSendImages(t *testing.T) {
	r := newRig(t)
	tok, _ := r.token("Pixel")
	c, _ := r.welcome(tok, nil)
	data := base64.StdEncoding.EncodeToString([]byte("png-bytes"))
	res := c.call(wire.MChatSend, wire.ChatSendParams{SessionID: "s1", Images: []wire.Image{{Name: "../../etc/shot.png", Mime: "image/png", Data: data}}})
	okRes(t, res)
	sent := r.be.Sent()
	require.Len(t, sent, 1)
	require.Equal(t, "shot.png", sent[0].Images[0].Name, "a display name, never a path")
	require.Equal(t, []byte("png-bytes"), sent[0].Images[0].Data)

	res = c.call(wire.MChatSend, wire.ChatSendParams{SessionID: "s1", Images: []wire.Image{{Name: "x", Mime: "text/plain", Data: data}}})
	require.Equal(t, wire.CodeInvalid, errCode(t, res))
	res = c.call(wire.MChatSend, wire.ChatSendParams{SessionID: "s1", Text: "  "})
	require.Equal(t, wire.CodeInvalid, errCode(t, res))
}

func TestR11_IdempotencyMapIsBounded(t *testing.T) {
	clk := newClock()
	m := newIdem(clk.now)
	for i := 0; i < wire.IdempotencyKeys+10; i++ {
		m.record("k"+string(rune(i)), "m")
	}
	require.Equal(t, wire.IdempotencyKeys, m.size())
	_, ok := m.lookup("k" + string(rune(0)))
	require.False(t, ok, "the oldest key went")
	_, ok = m.lookup("k" + string(rune(wire.IdempotencyKeys+9)))
	require.True(t, ok)
	clk.advance(wire.IdempotencyTTL + time.Second)
	_, ok = m.lookup("k" + string(rune(wire.IdempotencyKeys+9)))
	require.False(t, ok, "keys expire")
	require.Zero(t, m.size())
}
