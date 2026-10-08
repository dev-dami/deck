package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/tui/motion"
	"github.com/dev-dami/deck/internal/tui/theme"
)

const (
	toastLife   = 3 * time.Second
	toastFadeAt = 2600 * time.Millisecond
)

type toastFadeMsg struct{ seq int }
type toastExpireMsg struct{ seq int }

type toast struct {
	text   string
	danger bool
	on     bool
	seq    int
	fadeAt time.Time
	fade   motion.Fade
}

// show replaces the toast and returns the commands that fade and clear it.
func (t *toast) show(text string, danger bool, now time.Time) tea.Cmd {
	t.seq++
	t.text, t.danger, t.on = text, danger, true
	t.fade = motion.NewFade(toastLife-toastFadeAt, motion.Linear)
	t.fadeAt = now.Add(toastFadeAt)
	t.fade.Start(t.fadeAt)
	seq := t.seq
	return tea.Batch(
		tea.Tick(toastFadeAt, func(time.Time) tea.Msg { return toastFadeMsg{seq} }),
		tea.Tick(toastLife, func(time.Time) tea.Msg { return toastExpireMsg{seq} }),
	)
}

func (t *toast) moving(now time.Time) bool {
	return t.on && !motion.Reduced() && now.After(t.fadeAt) && !t.fade.Done(now)
}

// render returns the styled toast or "".
func (m *model) renderToast() string {
	t := &m.toast
	if !t.on {
		return ""
	}
	p := 0.0
	if !motion.Reduced() {
		p = t.fade.Progress(m.now)
	}
	from := theme.Muted
	if t.danger {
		from = theme.Danger
	}
	return m.ramp(from, theme.Faint, p).Render(t.text)
}
