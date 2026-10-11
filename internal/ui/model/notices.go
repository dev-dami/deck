package model

import (
	"fmt"
	"image"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/dev-dami/deck/internal/notify"
	"github.com/dev-dami/deck/internal/permission"
	"github.com/dev-dami/deck/internal/ui/dialog"
	"github.com/dev-dami/deck/internal/ui/motion"
	"github.com/dev-dami/deck/internal/ui/notice"
	"github.com/dev-dami/deck/internal/ui/util"
)

// Notifications: every report (util.ReportInfo/Warn/Error), agent turns
// that end while the user looks elsewhere, approvals and CI results go into
// one history, and the newest show as toasts in the top-right corner over
// the chat. The history is the Notifications pane and the alt+n popup.
//
// Code in Update calls m.Notify; a tea.Cmd returns notice.Msg.

// noticeState is the history and the toasts. Its zero value is ready.
type noticeState struct {
	store  notice.Store
	toasts notice.Toasts
	// hover is the toast under the pointer (0 for none); it holds still.
	hover int
	// ticking is true while a toastTickMsg of generation gen is pending.
	ticking bool
	gen     int
}

// toastTickMsg expires toasts and moves their slide-in along.
type toastTickMsg struct {
	at  time.Time
	gen int
}

func toastTick(d time.Duration, gen int) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return toastTickMsg{at: t, gen: gen} })
}

// Notify records a notice and, unless it is quiet, shows it as a toast.
// Call it from Update; from a tea.Cmd return notice.Msg instead.
func (m *UI) Notify(n notice.Notice) notice.Notice {
	n = m.notices.store.Push(n)
	if !n.Quiet {
		m.notices.toasts.Add(n, time.Now())
		m.notices.ticking = false // reschedule: the new toast slides in now
	}
	return n
}

// handleToastTick runs one toast timer tick and schedules the next.
func (m *UI) handleToastTick(msg toastTickMsg) tea.Cmd {
	if msg.gen != m.notices.gen {
		return nil // superseded by a newer timer
	}
	m.notices.ticking = false
	m.notices.toasts.Expire(msg.at)
	return m.scheduleToastTick(msg.at)
}

// scheduleToastTick starts the toast timer if a toast needs it. One timer
// runs at a time, and none while every toast is held under the pointer.
func (m *UI) scheduleToastTick(now time.Time) tea.Cmd {
	if m.notices.ticking || m.notices.toasts.Len() == 0 {
		return nil
	}
	d := m.notices.toasts.Next(now, !motion.Off())
	if d <= 0 {
		return nil
	}
	m.notices.gen++
	m.notices.ticking = true
	return toastTick(d, m.notices.gen)
}

// noticeFromInfo files a status bar report as a notice. Plain info stays
// in the history (the status bar shows it already); successes, warnings,
// errors, updates and CI results toast too. Mode banners are not notices.
func (m *UI) noticeFromInfo(msg util.InfoMsg) {
	text := strings.TrimSpace(msg.Msg)
	if text == "" {
		return
	}
	n := notice.Notice{Source: notice.SourceDeck, Body: text}
	switch msg.Type {
	case util.InfoTypeInfo:
		n.Level, n.Quiet = notice.Info, true
	case util.InfoTypeSuccess:
		n.Level = notice.Success
	case util.InfoTypeWarn:
		n.Level = notice.Warn
	case util.InfoTypeError:
		n.Level = notice.Error
	case util.InfoTypeUpdate:
		n.Level = notice.Info
	default: // plan and yolo banners
		return
	}
	if strings.HasPrefix(text, "CI ") {
		n.Source, n.Quiet = notice.SourceCI, false
		if strings.HasPrefix(text, "CI passed") {
			n.Level = notice.Success
		}
		if num := prNumber(text); num > 0 {
			n.Action = &notice.Action{ID: notice.ActionGitPR + strconv.Itoa(num), Label: fmt.Sprintf("Open PR #%d", num)}
		}
	}
	m.Notify(n)
}

var prNumberRE = regexp.MustCompile(`#(\d+)`)

// prNumber is the first "#123" in s, 0 when there is none.
func prNumber(s string) int {
	if mm := prNumberRE.FindStringSubmatch(s); mm != nil {
		n, _ := strconv.Atoi(mm[1])
		return n
	}
	return 0
}

// lookingElsewhere reports whether the user is not watching sessionID's
// chat: another session is open, the terminal is in the background, a
// dialog is up or the chat is scrolled away from its end.
func (m *UI) lookingElsewhere(sessionID string) bool {
	switch {
	case m.session == nil || m.session.ID != sessionID:
		return true
	case !m.notifyWindowFocused, m.dialog.HasDialogs():
		return true
	case m.state == uiChat && m.chat != nil && !m.chat.Follow():
		return true
	}
	return false
}

// noticeFromAgent files an agent's busy-to-idle edge as a notice: always
// for an error, for a finished turn only when the user looks elsewhere.
func (m *UI) noticeFromAgent(n notify.Notification) {
	title := n.SessionTitle
	if title == "" {
		title = "a session"
	}
	switch n.Type {
	case notify.TypeAgentFinished:
		if !m.lookingElsewhere(n.SessionID) {
			return
		}
		m.Notify(notice.Notice{Level: notice.Success, Source: notice.SourceAgent, Title: "Agents done", Body: "in “" + title + "”"})
	case notify.TypeAgentError:
		m.Notify(notice.Notice{Level: notice.Error, Source: notice.SourceAgent, Title: "Agent stopped on an error", Body: "in “" + title + "”"})
	}
}

// noticeFromPermission files an approval request as a notice.
func (m *UI) noticeFromPermission(p permission.PermissionRequest) {
	body := p.Description
	if body == "" {
		body = p.ToolName
	}
	m.Notify(notice.Notice{
		Level:  notice.Warn,
		Source: notice.SourceApprovals,
		Title:  "Approval needed",
		Body:   body,
		Action: &notice.Action{ID: notice.ActionApproval, Label: "Review approval"},
	})
}

// runNoticeAction runs what a notice offers.
func (m *UI) runNoticeAction(id int) tea.Cmd {
	n, ok := m.notices.store.Get(id)
	if !ok || n.Action == nil {
		return nil
	}
	m.dialog.CloseDialog(dialog.NoticeHistoryID)
	m.notices.toasts.Dismiss(id)
	a := n.Action
	if a.Msg != nil {
		msg := a.Msg
		return func() tea.Msg { return msg }
	}
	switch {
	case a.ID == notice.ActionGit:
		return m.focusPaneActivity(paneGit)
	case strings.HasPrefix(a.ID, notice.ActionGitPR):
		num, err := strconv.Atoi(strings.TrimPrefix(a.ID, notice.ActionGitPR))
		if err != nil || num <= 0 {
			return m.focusPaneActivity(paneGit)
		}
		return tea.Batch(m.focusPaneActivity(paneGit), m.openGitPR(num))
	case a.ID == notice.ActionApproval:
		if m.dialog.ContainsDialog(dialog.PermissionsID) {
			m.dialog.BringToFront(dialog.PermissionsID)
			return nil
		}
		return util.ReportInfo("No approval is waiting")
	case a.ID == notice.ActionNotices:
		return m.openNoticeHistory()
	}
	return nil
}

// openNoticeHistory opens the notification history popup.
func (m *UI) openNoticeHistory() tea.Cmd {
	m.dialog.CloseDialog(dialog.CommandsID)
	if m.dialog.ContainsDialog(dialog.NoticeHistoryID) {
		m.dialog.BringToFront(dialog.NoticeHistoryID)
	} else {
		m.dialog.OpenDialog(dialog.NewNoticeHistory(m.com, &m.notices.store))
	}
	m.notices.store.MarkRead()
	m.notices.toasts.Clear()
	return nil
}

// clearNotices empties the history and the toasts.
func (m *UI) clearNotices() {
	m.notices.store.Clear()
	m.notices.toasts.Clear()
	m.notices.hover = 0
}

// toastBox is a toast laid out on screen.
type toastBox struct {
	id   int
	rect image.Rectangle
	view string
}

// toastArea is where toasts stack: the chat's top-right corner, below the
// header.
func (m *UI) toastArea() image.Rectangle {
	if a := m.layout.main; a.Dx() > 0 && a.Dy() > 0 {
		return a
	}
	return image.Rect(0, 0, m.width, m.height)
}

// toastBoxes lays the toasts out, newest on top, at now.
func (m *UI) toastBoxes(now time.Time) []toastBox {
	toasts := m.notices.toasts.List()
	if len(toasts) == 0 || m.state == uiOnboarding {
		return nil
	}
	area := m.toastArea()
	moving := !motion.Off()
	var out []toastBox
	y := area.Min.Y
	for _, t := range toasts {
		view := notice.Card(m.com.Styles, t.Notice, area.Dx()-2, t.Notice.ID == m.notices.hover)
		if view == "" {
			break
		}
		w, h := lipgloss.Size(view)
		if y+h > area.Max.Y {
			break
		}
		x := area.Max.X - w - 1
		if p := notice.SlideProgress(t, now, moving); p < 1 {
			x += int((1 - p) * float64(w+1))
		}
		out = append(out, toastBox{id: t.Notice.ID, rect: image.Rect(x, y, x+w, y+h), view: view})
		y += h
	}
	return out
}

// drawToasts paints the toasts over the main view.
func (m *UI) drawToasts(scr uv.Screen, area uv.Rectangle) {
	for _, b := range m.toastBoxes(time.Now()) {
		r := b.rect.Intersect(area)
		if r.Empty() {
			continue
		}
		view := b.view
		if r.Dx() < b.rect.Dx() { // sliding in past the edge
			lines := strings.Split(view, "\n")
			for i, l := range lines {
				lines[i] = ansi.Truncate(l, r.Dx(), "")
			}
			view = strings.Join(lines, "\n")
		}
		uv.NewStyledString(view).Draw(scr, r)
	}
}

// toastAt is the toast under x, y, 0 for none.
func (m *UI) toastAt(x, y int) int {
	p := image.Pt(x, y)
	for _, b := range m.toastBoxes(time.Now()) {
		if p.In(b.rect) {
			return b.id
		}
	}
	return 0
}

// clickToast dismisses the toast under a click and reports whether there
// was one.
func (m *UI) clickToast(x, y int) bool {
	id := m.toastAt(x, y)
	if id == 0 {
		return false
	}
	m.notices.toasts.Dismiss(id)
	if m.notices.hover == id {
		m.notices.hover = 0
	}
	m.notices.ticking = false
	return true
}

// hoverToast holds the toast under the pointer.
func (m *UI) hoverToast(x, y int) {
	if m.notices.toasts.Len() == 0 {
		return
	}
	id := m.toastAt(x, y)
	if id == m.notices.hover {
		return
	}
	m.notices.hover = id
	if m.notices.toasts.Hover(id, time.Now()) {
		m.notices.ticking = false
	}
}

// drawNoticeDot marks unread notifications at the right of the status
// bar: "● 3 alt+n".
func (m *UI) drawNoticeDot(scr uv.Screen, area uv.Rectangle) {
	n := m.notices.store.Unread()
	if n == 0 || area.Dx() < 12 || area.Dy() < 1 {
		return
	}
	t := m.com.Styles
	lvl := notice.Info
	for _, it := range m.notices.store.List()[:n] {
		lvl = max(lvl, it.Level)
	}
	dot := lipgloss.NewStyle().Foreground(notice.Color(t, lvl)).Render(fmt.Sprintf("● %d", n))
	hint := lipgloss.NewStyle().Foreground(t.Dialog.SecondaryText.GetForeground()).Render(" " + notice.HistoryKey)
	s := dot + hint
	w := lipgloss.Width(s)
	r := image.Rect(area.Max.X-w-1, area.Min.Y, area.Max.X-1, area.Min.Y+1)
	uv.NewStyledString(s).Draw(scr, r)
}
