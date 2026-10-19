package model

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/internal/ui/notice"
	"github.com/dev-dami/deck/internal/ui/util"
)

// The Shared pane: threads teammates share with this Deck
// (docs/multiplayer-design.md §8.5), listed from their presence. enter
// follows one: a mirror that a command keeps current with a read that waits
// for news. As the role allows, p posts to it, and y or n answers the
// approval under the cursor. esc goes back to the list.

const paneShared paneID = "shared"

const (
	// sharedKeep caps the mirror lines kept, and sharedBacklog how many log
	// entries before the end the first read starts.
	sharedKeep    = 400
	sharedBacklog = 400
	// sharedWait is how long one read waits for news, sharedRetry the pause
	// after a failed read.
	sharedWait  = 20 * time.Second
	sharedRetry = 5 * time.Second
	// sharedShown caps the mirror lines the pane asks room for.
	sharedShown = 20
)

func init() { registerPane(paneShared, func() pane { return &sharedPane{} }) }

// sharedView is the shared thread the person follows.
type sharedView struct {
	gen          int
	peer, thread string
	title, role  string
	mode         string
	lines        []string
	next         int
	pending      []peer.PendingApproval
	err          string
}

// sharedMirrorMsg carries one read of the followed thread; sharedRetryMsg
// reads it again after a failure.
type sharedMirrorMsg struct {
	gen int
	mr  peer.Mirror
	err error
}

type sharedRetryMsg struct{ gen int }

// openShared starts following thread, which teammate name shares.
func (m *UI) openShared(name string, th peer.SharedThread) tea.Cmd {
	m.sharedGen++
	m.shared = &sharedView{gen: m.sharedGen, peer: name, thread: th.ID, title: th.Title, role: th.Role, mode: th.Mode,
		next: max(0, th.Entries-sharedBacklog)}
	return m.watchShared(0)
}

// watchShared reads the followed thread from where the last read ended,
// waiting up to wait for news.
func (m *UI) watchShared(wait time.Duration) tea.Cmd {
	link, v := m.peerLink(), m.shared
	if link == nil || v == nil {
		return nil
	}
	gen, name, thread, next := v.gen, v.peer, v.thread, v.next
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), wait+30*time.Second)
		defer cancel()
		mr, err := link.ReadShared(ctx, name, thread, next, wait)
		return sharedMirrorMsg{gen: gen, mr: mr, err: err}
	}
}

func (m *UI) applySharedMirror(msg sharedMirrorMsg) tea.Cmd {
	v := m.shared
	if v == nil || msg.gen != v.gen {
		return nil // the person moved on
	}
	if msg.err != nil {
		v.err = msg.err.Error()
		gen := v.gen
		return tea.Tick(sharedRetry, func(time.Time) tea.Msg { return sharedRetryMsg{gen: gen} })
	}
	v.err = ""
	th := msg.mr.Thread
	v.title, v.role, v.mode = th.Title, th.Role, th.Mode
	v.pending, v.next = msg.mr.Pending, msg.mr.Next
	for _, l := range msg.mr.Lines {
		v.lines = append(v.lines, strings.Split(l, "\n")...)
	}
	if len(v.lines) > sharedKeep {
		v.lines = v.lines[len(v.lines)-sharedKeep:]
	}
	return m.watchShared(sharedWait)
}

func (m *UI) applySharedRetry(msg sharedRetryMsg) tea.Cmd {
	if m.shared == nil || msg.gen != m.shared.gen {
		return nil
	}
	return m.watchShared(0)
}

// sharedNotices tells the person of each thread a teammate started to
// share with this Deck since prev.
func (m *UI) sharedNotices(prev, snap peer.Snapshot) {
	known := map[string]bool{}
	for _, ps := range prev.Peers {
		if ps.Presence != nil {
			for _, th := range ps.Presence.Threads {
				known[ps.Name+"\x00"+th.ID] = true
			}
		}
	}
	for _, ps := range snap.Peers {
		if ps.Presence == nil {
			continue
		}
		for _, th := range ps.Presence.Threads {
			if known[ps.Name+"\x00"+th.ID] {
				continue
			}
			m.Notify(notice.Notice{
				Level: notice.Info, Source: notice.SourceTeam,
				Title: ps.Name + " shared a thread with you", Body: th.Title + " (" + th.Role + ")",
				Action: &notice.Action{Label: "Open the Shared pane", Msg: focusPaneMsg{id: paneShared}},
			})
		}
	}
}

// sharedKeyPrefix starts the row key of a shared thread in the list:
// "shared:<peer>\x00<thread>"; approvalKeyPrefix that of an approval.
const (
	sharedKeyPrefix   = "shared:"
	approvalKeyPrefix = "approval:"
)

type sharedPane struct {
	list paneList
	// posting is set while the person writes draft to the followed thread.
	posting bool
	draft   string
}

func (p *sharedPane) id() paneID       { return paneShared }
func (p *sharedPane) title(*UI) string { return "Shared" }
func (p *sharedPane) width() int       { return 0 }

func (p *sharedPane) badge(m *UI) string {
	if m.shared != nil && len(m.shared.pending) > 0 {
		return "approval"
	}
	return ""
}

func (p *sharedPane) height(m *UI, width int) int {
	return rowsHeight(p.rows(m, width-1, sharedShown))
}

func (p *sharedPane) render(m *UI, width, height int, focused bool) string {
	return p.list.view(p.rows(m, width-1, height), width, height, focused, m.com.Styles.Header.Mark.GetForeground())
}

// sharedRef is a thread a teammate shares with this Deck.
type sharedRef struct {
	peer string
	th   peer.SharedThread
}

// sharedThreads lists the threads teammates share with this Deck: their row
// keys in order, and each by key.
func (m *UI) sharedThreads() ([]string, map[string]sharedRef) {
	threads := map[string]sharedRef{}
	if m.teamSnap == nil {
		return nil, threads
	}
	var keys []string
	for _, ps := range m.teamSnap.Peers {
		if ps.Presence == nil {
			continue
		}
		for _, th := range ps.Presence.Threads {
			k := sharedKeyPrefix + ps.Name + "\x00" + th.ID
			keys = append(keys, k)
			threads[k] = sharedRef{ps.Name, th}
		}
	}
	return keys, threads
}

// rows are the list of shared threads, or the followed one with its last
// lines that fit in height.
func (p *sharedPane) rows(m *UI, width, height int) []paneRow {
	t := m.com.Styles
	muted := t.ModelInfo.Provider
	cut := func(s string) string { return ansi.Truncate(s, width, "…") }
	v := m.shared
	if v == nil {
		keys, threads := m.sharedThreads()
		if len(keys) == 0 {
			return []paneRow{{lines: []string{cut(muted.Render("No teammate shares a thread with you."))}}}
		}
		var rows []paneRow
		for _, k := range keys {
			e := threads[k]
			rows = append(rows, paneRow{key: k, selectable: true, lines: []string{
				cut(t.ModelInfo.Name.Bold(true).Render(e.peer) + " · " + e.th.Title),
				cut("  " + muted.Render(e.th.Role+" · "+e.th.Mode+" mode · "+strings.Join(e.th.Members, ", "))),
			}})
		}
		return append(rows, paneRow{lines: []string{cut(muted.Render("enter follows a thread"))}})
	}
	help := "esc back"
	if shareRank(v.role) >= 2 {
		help += " · p posts"
	}
	rows := []paneRow{{lines: []string{
		cut(t.ModelInfo.Name.Bold(true).Render(v.peer) + " · " + v.title),
		cut(muted.Render("you are " + v.role + " · " + v.mode + " mode · " + help)),
	}}}
	if v.err != "" {
		rows = append(rows, paneRow{lines: []string{cut(muted.Render("cannot reach it: " + v.err))}})
	}
	for _, a := range v.pending {
		rows = append(rows, paneRow{key: approvalKeyPrefix + a.ID, selectable: true, lines: []string{
			cut("! " + a.Agent + " asks: " + a.Summary),
			cut("  " + muted.Render("y allow · n decline")),
		}})
	}
	room := height - rowsHeight(rows)
	if p.posting {
		room -= 2
	}
	lines := v.lines
	if room > 0 && len(lines) > room {
		lines = lines[len(lines)-room:]
	}
	for _, l := range lines {
		rows = append(rows, paneRow{lines: []string{cut(l)}})
	}
	if p.posting {
		rows = append(rows, paneRow{lines: []string{
			cut("> " + p.draft + "▏"),
			cut(muted.Render("enter posts · esc drops")),
		}})
	}
	return rows
}

// shareRank orders the roles: each may do what the one below does.
func shareRank(role string) int {
	return map[string]int{"observer": 1, "participant": 2, "editor": 3}[role]
}

func (p *sharedPane) key(m *UI, msg tea.KeyPressMsg) (bool, tea.Cmd) {
	v, link := m.shared, m.peerLink()
	if v != nil && p.posting {
		return p.postKey(m, msg)
	}
	rows := p.rows(m, 80, sharedShown)
	if p.list.navKey(rows, msg) {
		return true, nil
	}
	sel := p.list.selected(rows)
	if v == nil {
		_, threads := m.sharedThreads()
		if e, ok := threads[sel]; ok && msg.String() == "enter" {
			p.list.cursor = 0
			return true, m.openShared(e.peer, e.th)
		}
		return false, nil
	}
	switch msg.String() {
	case "esc":
		m.shared, p.draft = nil, ""
		m.sharedGen++
		return true, nil
	case "p":
		if shareRank(v.role) < 2 {
			return true, util.ReportInfo("You are " + v.role + " on this thread: you can read it, not post")
		}
		p.posting, p.draft = true, ""
		return true, nil
	case "y", "n":
		id, ok := strings.CutPrefix(sel, approvalKeyPrefix)
		if !ok || link == nil {
			return false, nil
		}
		allow, name, thread := msg.String() == "y", v.peer, v.thread
		return true, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := link.AnswerShared(ctx, name, thread, id, allow); err != nil {
				return util.NewErrorMsg(err)
			}
			return util.NewInfoMsg("Answered " + name + "'s approval")
		}
	}
	return false, nil
}

// postKey types a post to the followed thread: enter sends it, esc drops
// it.
func (p *sharedPane) postKey(m *UI, msg tea.KeyPressMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "esc":
		p.posting, p.draft = false, ""
		return true, nil
	case "enter":
		text, link, v := strings.TrimSpace(p.draft), m.peerLink(), m.shared
		p.posting, p.draft = false, ""
		if text == "" || link == nil {
			return true, nil
		}
		name, thread := v.peer, v.thread
		return true, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := link.PostShared(ctx, name, thread, text); err != nil {
				return util.NewErrorMsg(err)
			}
			return nil
		}
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
	if msg.Text != "" && msg.Mod&(tea.ModCtrl|tea.ModAlt) == 0 && len(p.draft) < 4000 {
		p.draft += msg.Text
	}
	return true, nil
}

func (p *sharedPane) click(m *UI, _, y int) tea.Cmd {
	if i := p.list.rowAt(y); i >= 0 {
		p.list.cursor = i
	}
	return nil
}
