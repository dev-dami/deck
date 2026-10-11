package model

import (
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dev-dami/deck/internal/ui/notice"
)

// The Notifications pane: the notification history, newest first. enter
// runs a notice's action, c clears the history. Notices are unread until
// the pane (or the popup) has been looked at.

func init() { registerPane(paneNotices, func() pane { return &noticesPane{} }) }

type noticesPane struct {
	list paneList
}

func (p *noticesPane) id() paneID       { return paneNotices }
func (p *noticesPane) title(*UI) string { return "Notifications" }
func (p *noticesPane) width() int       { return 0 }

func (p *noticesPane) badge(m *UI) string {
	if n := m.notices.store.Unread(); n > 0 {
		return strconv.Itoa(n) + " new"
	}
	return ""
}

func (p *noticesPane) height(m *UI, width int) int {
	return rowsHeight(p.rows(m, width-1, time.Now()))
}

func (p *noticesPane) render(m *UI, width, height int, focused bool) string {
	if focused {
		m.notices.store.MarkRead()
	}
	rows := p.rows(m, width-1, time.Now())
	return p.list.view(rows, width, height, focused, m.com.Styles.Header.Mark.GetForeground())
}

func (p *noticesPane) key(m *UI, msg tea.KeyPressMsg) (bool, tea.Cmd) {
	rows := p.rows(m, 80, time.Now())
	if p.list.navKey(rows, msg) {
		return true, nil
	}
	switch msg.String() {
	case "enter":
		return true, p.run(m, p.list.selected(rows))
	case "c":
		m.clearNotices()
		p.list = paneList{}
		return true, nil
	}
	return false, nil
}

func (p *noticesPane) click(m *UI, _, y int) tea.Cmd {
	i := p.list.rowAt(y)
	rows := p.rows(m, 80, time.Now())
	if i < 0 || i >= len(rows) || !rows[i].selectable {
		return nil
	}
	again := i == p.list.cursor
	p.list.cursor = i
	if again {
		return p.run(m, rows[i].key)
	}
	return nil
}

func (p *noticesPane) run(m *UI, key string) tea.Cmd {
	id, err := strconv.Atoi(key)
	if err != nil {
		return nil
	}
	return m.runNoticeAction(id)
}

// rows are the notices at width (the gutter left out).
func (p *noticesPane) rows(m *UI, width int, now time.Time) []paneRow {
	list := m.notices.store.List()
	if len(list) == 0 {
		return []paneRow{{lines: []string{m.com.Styles.ModelInfo.Provider.Render("No notifications")}}}
	}
	rows := make([]paneRow, 0, len(list))
	for i, n := range list {
		rows = append(rows, paneRow{
			key:        strconv.Itoa(n.ID),
			selectable: true,
			lines:      notice.Lines(m.com.Styles, n, width, now, i == p.list.cursor),
		})
	}
	return rows
}
