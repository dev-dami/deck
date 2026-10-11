package dialog

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/notice"
)

// NoticeHistoryID is the notification history popup. (NotificationsID is
// the notification style picker.)
const NoticeHistoryID = "notice-history"

const (
	noticeHistoryMaxWidth  = 72
	noticeHistoryMaxHeight = 26
)

// ActionRunNotice asks the UI to run a notice's action.
type ActionRunNotice struct{ ID int }

// ActionClearNotices asks the UI to clear the notification history.
type ActionClearNotices struct{}

// NoticeHistory lists the notifications, newest first: enter runs a
// notice's action, c clears the history.
type NoticeHistory struct {
	com    *common.Common
	store  *notice.Store
	help   help.Model
	cursor int // index into the newest-first list
	offset int // first line shown

	keyMap struct {
		Up, Down, Home, End, Run, Clear, UpDown, Close key.Binding
	}
}

var _ Dialog = (*NoticeHistory)(nil)

// NewNoticeHistory opens the history over store, which the UI owns.
func NewNoticeHistory(com *common.Common, store *notice.Store) *NoticeHistory {
	d := &NoticeHistory{com: com, store: store}
	h := help.New()
	h.Styles = com.Styles.DialogHelpStyles()
	d.help = h
	d.keyMap.Up = key.NewBinding(key.WithKeys("up", "k", "ctrl+p"), key.WithHelp("↑", "up"))
	d.keyMap.Down = key.NewBinding(key.WithKeys("down", "j", "ctrl+n"), key.WithHelp("↓", "down"))
	d.keyMap.UpDown = key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑/↓", "choose"))
	d.keyMap.Home = key.NewBinding(key.WithKeys("home", "g"), key.WithHelp("g", "newest"))
	d.keyMap.End = key.NewBinding(key.WithKeys("end", "G"), key.WithHelp("G", "oldest"))
	d.keyMap.Run = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "run action"))
	d.keyMap.Clear = key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "clear"))
	d.keyMap.Close = CloseKey
	return d
}

// ID implements [Dialog].
func (d *NoticeHistory) ID() string { return NoticeHistoryID }

// HandleMsg implements [Dialog].
func (d *NoticeHistory) HandleMsg(msg tea.Msg) Action {
	n := d.store.Len()
	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		if msg.Button == tea.MouseWheelUp {
			d.cursor = max(0, d.cursor-1)
		} else {
			d.cursor = min(max(0, n-1), d.cursor+1)
		}
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, d.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, d.keyMap.Up):
			d.cursor = max(0, d.cursor-1)
		case key.Matches(msg, d.keyMap.Down):
			d.cursor = min(max(0, n-1), d.cursor+1)
		case key.Matches(msg, d.keyMap.Home):
			d.cursor = 0
		case key.Matches(msg, d.keyMap.End):
			d.cursor = max(0, n-1)
		case key.Matches(msg, d.keyMap.Clear):
			d.cursor, d.offset = 0, 0
			return ActionClearNotices{}
		case key.Matches(msg, d.keyMap.Run):
			list := d.store.List()
			if d.cursor < len(list) && list[d.cursor].Action != nil {
				return ActionRunNotice{ID: list[d.cursor].ID}
			}
		}
	}
	return nil
}

// Draw implements [Dialog].
func (d *NoticeHistory) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := d.com.Styles
	width := max(0, min(noticeHistoryMaxWidth, area.Dx()-t.Dialog.View.GetHorizontalBorderSize()))
	height := max(0, min(noticeHistoryMaxHeight, area.Dy()-t.Dialog.View.GetVerticalBorderSize()))
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()
	listHeight := max(1, height-
		(t.Dialog.Title.GetVerticalFrameSize()+titleContentHeight+
			t.Dialog.HelpView.GetVerticalFrameSize()+1+
			t.Dialog.View.GetVerticalFrameSize()))

	list := d.store.List()
	d.cursor = min(d.cursor, max(0, len(list)-1))
	pad := t.Dialog.List.GetHorizontalFrameSize()
	rowWidth := max(1, innerWidth-pad-3) // the cursor mark and the scrollbar

	var lines []string
	start, end := 0, 0
	now := time.Now()
	for i, n := range list {
		sel := i == d.cursor
		rl := notice.Lines(t, n, rowWidth, now, sel)
		if sel {
			start, end = len(lines), len(lines)+len(rl)
		}
		mark := "  "
		if sel {
			mark = lipgloss.NewStyle().Foreground(notice.Color(t, n.Level)).Render("▌ ")
		}
		for _, l := range rl {
			lines = append(lines, mark+l)
		}
	}
	if len(lines) == 0 {
		muted := lipgloss.NewStyle().Foreground(t.Dialog.SecondaryText.GetForeground())
		lines = []string{muted.Render("No notifications")}
	}
	// Keep the selected notice in view.
	if start < d.offset {
		d.offset = start
	}
	if end > d.offset+listHeight {
		d.offset = min(start, end-listHeight)
	}
	d.offset = max(0, min(d.offset, max(0, len(lines)-listHeight)))
	shown := lines[d.offset:min(len(lines), d.offset+listHeight)]
	for i, l := range shown {
		shown[i] = ansi.Truncate(l, max(0, innerWidth-pad-1), "…")
	}
	view := strings.Join(shown, "\n")
	view = t.Dialog.List.Height(listHeight).Render(view)
	view = joinScrollbar(t, view, listHeight, len(lines), listHeight, d.offset)

	rc := NewRenderContext(t, width)
	rc.Title = "Notifications"
	if n := len(list); n > 0 {
		rc.TitleInfo = lipgloss.NewStyle().Foreground(t.Dialog.SecondaryText.GetForeground()).Render(fmt.Sprintf(" %d", n))
	}
	rc.AddPart(view)
	rc.Help = renderDialogHelp(t, &d.help, d, innerWidth)
	DrawCenter(scr, area, rc.Render())
	return nil
}

// ShortHelp implements [help.KeyMap].
func (d *NoticeHistory) ShortHelp() []key.Binding {
	return []key.Binding{d.keyMap.UpDown, d.keyMap.Run, d.keyMap.Clear, d.keyMap.Close}
}

// FullHelp implements [help.KeyMap].
func (d *NoticeHistory) FullHelp() [][]key.Binding {
	return [][]key.Binding{{d.keyMap.Up, d.keyMap.Down, d.keyMap.Home, d.keyMap.End}, {d.keyMap.Run, d.keyMap.Clear, d.keyMap.Close}}
}
