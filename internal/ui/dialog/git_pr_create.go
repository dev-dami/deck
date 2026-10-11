package dialog

import (
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/util"
)

// GitPRCreateID is the identifier for the create pull request form.
const GitPRCreateID = "git_pr_create"

// The form's fields, in tab order.
const (
	prFieldTitle = iota
	prFieldBase
	prFieldBody
	prFieldDraft
	prFieldCount
)

// Body editor height bounds.
const (
	prBodyMinHeight = 4
	prBodyMaxHeight = 10
)

// GitPRCreate is the form that opens a pull request for the current branch.
type GitPRCreate struct {
	com     *common.Common
	help    help.Model
	spinner spinner.Model
	busy    string

	branch string
	notes  string // what happens on submit, e.g. "pushes 2 commits first"
	title  textinput.Model
	base   textinput.Model
	body   textarea.Model
	draft  bool
	focus  int

	keyMap struct {
		Next, Prev, Submit, SubmitLast, Toggle, Close key.Binding
	}
}

var (
	_ Dialog        = (*GitPRCreate)(nil)
	_ GitBusyDialog = (*GitPRCreate)(nil)
)

// NewGitPRCreate opens the form for branch, prefilled.
func NewGitPRCreate(com *common.Common, branch, title, base, body, notes string) *GitPRCreate {
	d := &GitPRCreate{com: com, branch: branch, notes: notes}
	d.help = help.New()
	d.help.Styles = com.Styles.DialogHelpStyles()
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = com.Styles.Dialog.Spinner
	d.spinner = s

	newInput := func(value, placeholder string) textinput.Model {
		in := textinput.New()
		in.SetVirtualCursor(false)
		in.SetStyles(com.Styles.TextInput)
		in.Prompt = "> "
		in.Placeholder = placeholder
		in.CharLimit = 256
		in.SetValue(value)
		in.CursorEnd()
		return in
	}
	d.title = newInput(title, "Title")
	d.base = newInput(base, "main")

	ta := textarea.New()
	taStyles := com.Styles.Editor.Textarea
	ta.SetStyles(taStyles)
	ta.Placeholder = "What does this change, and why?"
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetVirtualCursor(false)
	ta.DynamicHeight = false
	ta.SetPromptFunc(2, func(textarea.PromptInfo) string { return "  " })
	ta.SetHeight(prBodyMinHeight)
	ta.SetValue(body)
	ta.Blur()
	d.body = ta

	d.keyMap.Next = key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next field"))
	d.keyMap.Prev = key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "previous"))
	d.keyMap.Submit = key.NewBinding(key.WithKeys("ctrl+s"), key.WithHelp("ctrl+s", "open PR"))
	d.keyMap.SubmitLast = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "next"))
	d.keyMap.Toggle = key.NewBinding(key.WithKeys("space", " ", "x"), key.WithHelp("space", "toggle draft"))
	d.keyMap.Close = CloseKey

	d.setFocus(prFieldTitle)
	return d
}

// ID implements [Dialog].
func (*GitPRCreate) ID() string { return GitPRCreateID }

// InitialCmd starts the cursor.
func (d *GitPRCreate) InitialCmd() tea.Cmd { return d.title.Focus() }

// SetBusy implements [GitBusyDialog].
func (d *GitPRCreate) SetBusy(label string) tea.Cmd {
	was := d.busy != ""
	d.busy = label
	if label != "" && !was {
		return d.spinner.Tick
	}
	return nil
}

func (d *GitPRCreate) setFocus(f int) tea.Cmd {
	d.focus = (f + prFieldCount) % prFieldCount
	d.title.Blur()
	d.base.Blur()
	d.body.Blur()
	switch d.focus {
	case prFieldTitle:
		return d.title.Focus()
	case prFieldBase:
		return d.base.Focus()
	case prFieldBody:
		return d.body.Focus()
	}
	return nil
}

func (d *GitPRCreate) submit() Action {
	title := strings.TrimSpace(d.title.Value())
	if title == "" {
		d.setFocus(prFieldTitle)
		return ActionCmd{Cmd: util.ReportWarn("A pull request needs a title")}
	}
	return ActionGitSubmitPR{
		Title: title,
		Base:  strings.TrimSpace(d.base.Value()),
		Body:  strings.TrimSpace(d.body.Value()),
		Draft: d.draft,
	}
}

// HandleMsg implements [Dialog].
func (d *GitPRCreate) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		if d.busy != "" {
			var cmd tea.Cmd
			d.spinner, cmd = d.spinner.Update(msg)
			return ActionCmd{Cmd: cmd}
		}
	case tea.PasteMsg:
		return d.updateField(msg)
	case tea.KeyPressMsg:
		if d.busy != "" {
			// The pull request is on its way; only let the user look.
			return nil
		}
		switch {
		case key.Matches(msg, d.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, d.keyMap.Submit):
			return d.submit()
		case key.Matches(msg, d.keyMap.Next):
			return ActionCmd{Cmd: d.setFocus(d.focus + 1)}
		case key.Matches(msg, d.keyMap.Prev):
			return ActionCmd{Cmd: d.setFocus(d.focus - 1)}
		}
		switch d.focus {
		case prFieldDraft:
			switch {
			case key.Matches(msg, d.keyMap.Toggle):
				d.draft = !d.draft
			case key.Matches(msg, d.keyMap.SubmitLast):
				return d.submit()
			}
			return nil
		case prFieldTitle, prFieldBase:
			if key.Matches(msg, d.keyMap.SubmitLast) {
				return ActionCmd{Cmd: d.setFocus(d.focus + 1)}
			}
		}
		return d.updateField(msg)
	}
	return nil
}

// updateField sends msg to the focused input.
func (d *GitPRCreate) updateField(msg tea.Msg) Action {
	var cmd tea.Cmd
	switch d.focus {
	case prFieldTitle:
		d.title, cmd = d.title.Update(msg)
	case prFieldBase:
		d.base, cmd = d.base.Update(msg)
	case prFieldBody:
		d.body, cmd = d.body.Update(msg)
	}
	return ActionCmd{Cmd: cmd}
}

// Draw implements [Dialog].
func (d *GitPRCreate) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := d.com.Styles
	width := gitDialogWidth(t, area, gitDialogMaxWidth)
	inner := gitInner(t, width)
	muted := gitMuted(t)
	fieldWidth := max(1, inner-2*gitPad)

	label := func(text string, f int) string {
		if d.focus == f {
			return t.Dialog.Arguments.InputLabelFocused.Render(text)
		}
		return t.Dialog.Arguments.InputLabelBlurred.Render(text)
	}

	lines := []string{gitTitle(t, "Open a pull request", inner)}
	sub := t.Header.GitBranch.Render(d.branch)
	if d.notes != "" {
		sub += muted.Render(" · " + d.notes)
	}
	lines = append(lines, gitLine(sub, inner), "")

	var cur *tea.Cursor
	rowOf := func() int { return lipgloss.Height(strings.Join(lines, "\n")) }

	d.title.SetWidth(max(1, fieldWidth-lipgloss.Width(d.title.Prompt)-1))
	d.base.SetWidth(max(1, fieldWidth-lipgloss.Width(d.base.Prompt)-1))

	lines = append(lines, gitLine(label("Title", prFieldTitle), inner))
	if d.focus == prFieldTitle {
		cur = gitCursor(t, d.title.Cursor(), rowOf(), gitPad)
	}
	lines = append(lines, gitLine(d.title.View(), inner), "")

	lines = append(lines, gitLine(label("Base branch", prFieldBase), inner))
	if d.focus == prFieldBase {
		cur = gitCursor(t, d.base.Cursor(), rowOf(), gitPad)
	}
	lines = append(lines, gitLine(d.base.View(), inner), "")

	lines = append(lines, gitLine(label("Description", prFieldBody), inner))
	// title, sub, blank, 2×(label, input, blank), label, body, blank,
	// draft, blank, busy, help
	fixed := len(lines) + 6
	bodyHeight := max(1, min(prBodyMaxHeight, area.Dy()-t.Dialog.View.GetVerticalFrameSize()-fixed))
	bodyHeight = max(min(prBodyMinHeight, bodyHeight), min(bodyHeight, d.body.LineCount()+1))
	d.body.SetWidth(fieldWidth)
	d.body.SetHeight(bodyHeight)
	if d.focus == prFieldBody {
		cur = gitCursor(t, d.body.Cursor(), rowOf(), gitPad)
	}
	bodyView := lipgloss.NewStyle().Padding(0, gitPad).Render(d.body.View())
	lines = append(lines, bodyView, "")

	box := "[ ]"
	if d.draft {
		box = "[x]"
	}
	draft := box + " Draft"
	if d.focus == prFieldDraft {
		draft = t.Dialog.Arguments.InputLabelFocused.Render(draft) + muted.Render("  space toggles · enter opens the PR")
	} else {
		draft = t.Dialog.Arguments.InputLabelBlurred.Render(draft)
	}
	lines = append(lines, gitLine(draft, inner), "")

	if d.busy != "" {
		lines = append(lines, gitLine(d.spinner.View()+" "+muted.Render(d.busy), inner))
		cur = nil
	}
	lines = append(lines, renderDialogHelp(t, &d.help, d, inner))

	DrawCenterCursor(scr, area, gitView(t, width, lines), cur)
	return cur
}

// ShortHelp implements [help.KeyMap].
func (d *GitPRCreate) ShortHelp() []key.Binding {
	next := d.keyMap.SubmitLast
	if d.focus == prFieldDraft {
		next.SetHelp("enter", "open PR")
		return []key.Binding{d.keyMap.Toggle, next, d.keyMap.Prev, d.keyMap.Close}
	}
	if d.focus == prFieldBody {
		return []key.Binding{d.keyMap.Submit, d.keyMap.Next, d.keyMap.Prev, d.keyMap.Close}
	}
	return []key.Binding{d.keyMap.Submit, next, d.keyMap.Next, d.keyMap.Close}
}

// FullHelp implements [help.KeyMap].
func (d *GitPRCreate) FullHelp() [][]key.Binding {
	return [][]key.Binding{{d.keyMap.Submit, d.keyMap.SubmitLast, d.keyMap.Next, d.keyMap.Prev, d.keyMap.Toggle, d.keyMap.Close}}
}
