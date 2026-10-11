package dialog

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/dev-dami/deck/internal/deck/gh"
	"github.com/dev-dami/deck/internal/ui/common"
)

// GitMergeID is the identifier for the merge confirmation.
const GitMergeID = "git_merge"

// gitMergeHints says what each merge method does.
var gitMergeHints = map[string]string{
	"squash": "one commit on the base branch",
	"merge":  "a merge commit, history kept",
	"rebase": "the commits replayed onto the base branch",
}

// GitMerge asks how to merge a pull request. Nothing is merged until the
// user confirms.
type GitMerge struct {
	com    *common.Common
	help   help.Model
	number int
	title  string
	method int

	keyMap struct {
		Switch, Confirm, Close key.Binding
	}
}

var _ Dialog = (*GitMerge)(nil)

// NewGitMerge asks how to merge pull request number.
func NewGitMerge(com *common.Common, number int, title string) *GitMerge {
	d := &GitMerge{com: com, number: number, title: title}
	d.help = help.New()
	d.help.Styles = com.Styles.DialogHelpStyles()
	d.keyMap.Switch = key.NewBinding(key.WithKeys("left", "right", "tab", "shift+tab", "h", "l"), key.WithHelp("←→", "method"))
	d.keyMap.Confirm = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "merge"))
	d.keyMap.Close = key.NewBinding(key.WithKeys("esc", "alt+esc", "n", "N"), key.WithHelp("esc", "don't merge"))
	return d
}

// ID implements [Dialog].
func (*GitMerge) ID() string { return GitMergeID }

// HandleMsg implements [Dialog].
func (d *GitMerge) HandleMsg(msg tea.Msg) Action {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch {
	case key.Matches(k, d.keyMap.Close):
		return ActionClose{}
	case key.Matches(k, d.keyMap.Confirm):
		return ActionGitPRMerge{Number: d.number, Method: gh.MergeMethods[d.method]}
	case key.Matches(k, d.keyMap.Switch):
		n := len(gh.MergeMethods)
		switch k.String() {
		case "left", "shift+tab", "h":
			d.method = (d.method + n - 1) % n
		default:
			d.method = (d.method + 1) % n
		}
	}
	return nil
}

// Draw implements [Dialog].
func (d *GitMerge) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := d.com.Styles
	width := gitDialogWidth(t, area, 60)
	inner := gitInner(t, width)
	muted := gitMuted(t)

	buttons := make([]string, len(gh.MergeMethods))
	for i, m := range gh.MergeMethods {
		buttons[i] = common.Button(t, common.ButtonOpts{Text: m, Selected: i == d.method, Padding: 2})
	}
	method := gh.MergeMethods[d.method]
	lines := []string{
		gitTitle(t, fmt.Sprintf("Merge #%d?", d.number), inner),
		gitLine(gitPrimary(t).Render(d.title), inner),
		"",
		gitLine(strings.Join(buttons, " "), inner),
		gitLine(muted.Render(gitMergeHints[method]), inner),
		"",
		renderDialogHelp(t, &d.help, d, inner),
	}
	DrawCenter(scr, area, gitView(t, width, lines))
	return nil
}

// ShortHelp implements [help.KeyMap].
func (d *GitMerge) ShortHelp() []key.Binding {
	return []key.Binding{d.keyMap.Switch, d.keyMap.Confirm, d.keyMap.Close}
}

// FullHelp implements [help.KeyMap].
func (d *GitMerge) FullHelp() [][]key.Binding {
	return [][]key.Binding{d.ShortHelp()}
}
