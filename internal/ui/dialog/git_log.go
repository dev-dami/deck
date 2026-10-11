package dialog

import (
	"fmt"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/dev-dami/deck/internal/deck/gh"
	"github.com/dev-dami/deck/internal/ui/common"
)

// GitLogID is the identifier for the failed log viewer.
const GitLogID = "git_log"

// gitLogMaxWidth is the log viewer's widest; logs are wide.
const gitLogMaxWidth = 120

// GitLog shows the tail of a failed check's log.
type GitLog struct {
	com    *common.Common
	help   help.Model
	number int
	check  gh.Check
	log    string
	vp     viewport.Model
	width  int // the width the content was last cut to

	keyMap struct {
		Scroll, Top, Bottom, Handoff, Close key.Binding
	}
}

var _ Dialog = (*GitLog)(nil)

// NewGitLog shows log, the failed log of check on pull request number.
func NewGitLog(com *common.Common, number int, check gh.Check, log string) *GitLog {
	d := &GitLog{com: com, number: number, check: check, log: log, vp: viewport.New()}
	d.help = help.New()
	d.help.Styles = com.Styles.DialogHelpStyles()
	d.keyMap.Scroll = key.NewBinding(key.WithKeys("up", "down", "pgup", "pgdown"), key.WithHelp("↑↓/pgup/pgdn", "scroll"))
	d.keyMap.Top = key.NewBinding(key.WithKeys("home", "g"), key.WithHelp("g", "top"))
	d.keyMap.Bottom = key.NewBinding(key.WithKeys("end", "G"), key.WithHelp("G", "bottom"))
	d.keyMap.Handoff = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "hand to agent"))
	d.keyMap.Close = CloseKey
	return d
}

// ID implements [Dialog].
func (*GitLog) ID() string { return GitLogID }

// HandleMsg implements [Dialog].
func (d *GitLog) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, d.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, d.keyMap.Handoff):
			return ActionGitPRHandoff{Number: d.number, Check: d.check, Log: d.log}
		case key.Matches(msg, d.keyMap.Top):
			d.vp.GotoTop()
		case key.Matches(msg, d.keyMap.Bottom):
			d.vp.GotoBottom()
		default:
			d.vp, _ = d.vp.Update(msg)
		}
	case common.CoalescedWheelMsg:
		d.vp, _ = d.vp.Update(tea.MouseWheelMsg(msg.Mouse))
	}
	return nil
}

// Draw implements [Dialog].
func (d *GitLog) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := d.com.Styles
	width := gitDialogWidth(t, area, gitLogMaxWidth)
	inner := gitInner(t, width)
	textWidth := max(1, inner-2*gitPad-1) // a column for the scrollbar

	// title, a blank, the log, a blank, the help
	height := max(1, area.Dy()-t.Dialog.View.GetVerticalFrameSize()-4)
	first := d.width == 0
	if textWidth != d.width {
		d.width = textWidth
		lines := splitLines(d.log)
		for i, l := range lines {
			lines[i] = ansi.Truncate(l, textWidth, "…")
		}
		d.vp.SetContentLines(lines)
	}
	d.vp.SetWidth(textWidth)
	d.vp.SetHeight(min(height, max(1, d.vp.TotalLineCount())))
	if first {
		// The error is at the end.
		d.vp.GotoBottom()
	}

	body := lipgloss.NewStyle().Padding(0, gitPad).Render(
		joinScrollbar(t, d.vp.View(), d.vp.Height(), d.vp.TotalLineCount(), d.vp.Height(), d.vp.YOffset()))
	title := fmt.Sprintf("#%d · %s · failed log", d.number, d.check.Name)
	lines := []string{gitTitle(t, title, inner), "", body, "", renderDialogHelp(t, &d.help, d, inner)}
	DrawCenter(scr, area, gitView(t, width, lines))
	return nil
}

// splitLines splits s into lines; an empty s is one empty line.
func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// ShortHelp implements [help.KeyMap].
func (d *GitLog) ShortHelp() []key.Binding {
	return []key.Binding{d.keyMap.Scroll, d.keyMap.Handoff, d.keyMap.Top, d.keyMap.Bottom, d.keyMap.Close}
}

// FullHelp implements [help.KeyMap].
func (d *GitLog) FullHelp() [][]key.Binding {
	return [][]key.Binding{d.ShortHelp()}
}
