package model

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/internal/ui/util"
)

// The Team chat pane: a chat room with your actual teammates, the people
// on the Decks paired with this one. Messages come with the peer link's
// snapshot; typing while the pane has focus writes a message and enter
// sends it to every paired teammate (console.post on their Deck).

const paneChat paneID = "chat"

// chatSendTimeout bounds sending one message to every teammate.
const chatSendTimeout = 30 * time.Second

func init() { registerPane(paneChat, func() pane { return &chatPane{} }) }

// teamChatSentMsg reports a sent message.
type teamChatSentMsg struct {
	reached []string
	err     error
}

// focusPaneMsg focuses a pane, opening it when it is closed (a notice's
// action).
type focusPaneMsg struct{ id paneID }

func (m *UI) focusOrOpenPane(id paneID) tea.Cmd {
	if m.strip().live[id] == nil {
		if cmd := m.reopenPane(id); cmd != nil {
			return tea.Batch(cmd, m.focusPane(id))
		}
	}
	return m.focusPane(id)
}

func (m *UI) sendTeamChat(text string) tea.Cmd {
	d := m.teamDesk()
	if d == nil {
		return util.ReportWarn("Team chat needs Deck's peer link")
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), chatSendTimeout)
		defer cancel()
		reached, err := d.SendTeamChat(ctx, text)
		return teamChatSentMsg{reached: reached, err: err}
	}
}

func (m *UI) applyTeamChatSent(msg teamChatSentMsg) tea.Cmd {
	var queued peer.QueuedError
	if errors.As(msg.err, &queued) {
		if len(msg.reached) > 0 {
			return util.ReportInfo("Sent to " + strings.Join(msg.reached, ", ") + "; " + queued.Error())
		}
		return util.ReportInfo("Sent: " + queued.Error())
	}
	if msg.err != nil {
		if len(msg.reached) > 0 {
			return util.ReportWarn("Sent to " + strings.Join(msg.reached, ", ") + "; " + msg.err.Error())
		}
		return util.ReportError(msg.err)
	}
	return nil
}

type chatPane struct {
	draft string
}

func (p *chatPane) id() paneID       { return paneChat }
func (p *chatPane) title(*UI) string { return "Team chat" }
func (p *chatPane) width() int       { return 0 }

func (p *chatPane) badge(m *UI) string {
	if m.teamSnap == nil {
		return ""
	}
	on := 0
	for _, ps := range m.teamSnap.Peers {
		if ps.Link.Inbound > 0 || (ps.Link.Supervised && ps.Link.Status.State == peer.StateConnected) {
			on++
		}
	}
	if len(m.teamSnap.Peers) == 0 {
		return ""
	}
	return strconv.Itoa(on) + " online"
}

func (p *chatPane) height(m *UI, width int) int {
	// The room grows with the conversation, up to a screenful.
	n := len(p.lines(m, width-1)) + 2
	return min(max(n, 4), 24)
}

func (p *chatPane) render(m *UI, width, height int, focused bool) string {
	t := m.com.Styles
	w := max(1, width-1)
	msgs := p.lines(m, w)
	room := max(0, height-2)
	if len(msgs) > room {
		msgs = msgs[len(msgs)-room:]
	}
	for len(msgs) < room {
		msgs = append([]string{""}, msgs...)
	}
	rule := t.ModelInfo.Provider.Render(strings.Repeat("─", w))
	prompt := "› "
	var input string
	switch {
	case p.draft != "":
		input = prompt + p.draft
		if focused {
			input += "█"
		}
		// Keep the end of a long draft in view.
		if ansi.StringWidth(input) > w {
			input = "…" + ansi.TruncateLeft(input, ansi.StringWidth(input)-w+1, "")
		}
	case focused:
		input = prompt + "█" + t.ModelInfo.Provider.Render(" message your team, enter sends")
	default:
		input = t.ModelInfo.Provider.Render(prompt + "focus here to chat with your team")
	}
	out := append(msgs, rule, ansi.Truncate(input, w, ""))
	return lipgloss.JoinVertical(lipgloss.Left, out...)
}

// lines are the chat's messages wrapped to width.
func (p *chatPane) lines(m *UI, width int) []string {
	t := m.com.Styles
	muted := t.ModelInfo.Provider
	snap := m.teamSnap
	if snap == nil {
		if m.peerLink() == nil {
			return []string{muted.Render("No peer link in this Deck")}
		}
		return []string{muted.Render("Reading the team…")}
	}
	if len(snap.Chat) == 0 {
		if len(snap.Peers) == 0 {
			return []string{muted.Render("Pair a teammate in Settings › Team to chat.")}
		}
		return []string{muted.Render("No messages yet. Say hi.")}
	}
	var out []string
	for _, c := range snap.Chat {
		name := c.Author()
		style := t.ModelInfo.Name.Bold(true)
		if c.Mine {
			name = "you"
			if c.Agent != "" {
				name = "your " + c.Agent
			}
			style = lipgloss.NewStyle().Foreground(t.Header.Mark.GetForeground()).Bold(true)
		}
		out = append(out, ansi.Truncate(style.Render(name)+" "+muted.Render(c.At.Local().Format("15:04")), width, "…"))
		for _, l := range strings.Split(ansi.Wordwrap(c.Text, max(1, width-2), " -"), "\n") {
			out = append(out, "  "+ansi.Truncate(l, max(1, width-2), "…"))
		}
	}
	return out
}

func (p *chatPane) key(m *UI, msg tea.KeyPressMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "enter":
		text := strings.TrimSpace(p.draft)
		if text == "" {
			return true, nil
		}
		p.draft = ""
		return true, m.sendTeamChat(text)
	case "backspace":
		if p.draft != "" {
			_, n := utf8.DecodeLastRuneInString(p.draft)
			p.draft = p.draft[:len(p.draft)-n]
		}
		return true, nil
	case "ctrl+u":
		p.draft = ""
		return true, nil
	}
	// Plain typing goes to the draft; keys with ctrl or alt, and the
	// arrows, stay with the sidebar.
	if msg.Text != "" && msg.Mod&(tea.ModCtrl|tea.ModAlt) == 0 {
		if len(p.draft) < 4000 {
			p.draft += msg.Text
		}
		return true, nil
	}
	return false, nil
}

func (p *chatPane) click(*UI, int, int) tea.Cmd { return nil }
