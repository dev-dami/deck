package dialog

import (
	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/sahilm/fuzzy"

	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/list"
	"github.com/dev-dami/deck/internal/ui/styles"
	"github.com/dev-dami/deck/internal/ui/tone"
	"github.com/dev-dami/deck/internal/workspace"
)

const (
	// AgentModelsID is the identifier for the agent model picker.
	AgentModelsID              = "agent_models"
	agentModelsDialogMaxWidth  = 72
	agentModelsDialogMinHeight = 8
	agentModelsDialogMaxHeight = 22
)

// ActionSelectAgentModel is the user's pick: agents of Kind run Model with
// Effort ("" = the model's default).
type ActionSelectAgentModel struct {
	Kind, Model, Effort string
}

// AgentModels picks the model, then the reasoning effort, that an agent CLI
// runs. Every installed CLI's models are listed together, each under its
// agent's own mark.
type AgentModels struct {
	com     *common.Common
	help    help.Model
	list    *list.FilterableList
	input   textinput.Model
	choices []workspace.ModelChoice

	// picked is the model whose effort is being chosen; nil while the
	// model is.
	picked *workspace.ModelChoice

	keyMap struct {
		Select   key.Binding
		Next     key.Binding
		Previous key.Binding
		UpDown   key.Binding
		Back     key.Binding
		Close    key.Binding
	}
}

var _ Dialog = (*AgentModels)(nil)

// NewAgentModels opens the picker on the given choices.
func NewAgentModels(com *common.Common, choices []workspace.ModelChoice) *AgentModels {
	d := &AgentModels{com: com, choices: choices}
	d.help = help.New()
	d.help.Styles = com.Styles.DialogHelpStyles()
	d.list = list.NewFilterableList()
	d.list.Focus()
	d.input = textinput.New()
	d.input.SetVirtualCursor(false)
	d.input.Placeholder = "Type to filter"
	d.input.SetStyles(com.Styles.TextInput)
	d.input.Focus()

	d.keyMap.Select = key.NewBinding(key.WithKeys("enter", "ctrl+y"), key.WithHelp("enter", "choose"))
	d.keyMap.Next = key.NewBinding(key.WithKeys("down", "ctrl+n"), key.WithHelp("↓", "next"))
	d.keyMap.Previous = key.NewBinding(key.WithKeys("up", "ctrl+p"), key.WithHelp("↑", "previous"))
	d.keyMap.UpDown = key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑/↓", "choose"))
	d.keyMap.Back = key.NewBinding(key.WithKeys("backspace"), key.WithHelp("⌫", "back to models"))
	d.keyMap.Close = CloseKey

	d.showModels()
	return d
}

// ID implements [Dialog].
func (d *AgentModels) ID() string { return AgentModelsID }

func (d *AgentModels) showModels() {
	d.picked = nil
	items := make([]list.FilterableItem, 0, len(d.choices))
	sel := 0
	for i, c := range d.choices {
		info := ""
		switch {
		case c.Current && c.CurrentEffort != "":
			info = "current · " + c.CurrentEffort
		case c.Current:
			info = "current"
		case c.Default:
			info = "default"
		}
		if c.Current {
			sel = i
		}
		items = append(items, &agentModelItem{
			Versioned: list.NewVersioned(),
			t:         d.com.Styles,
			key:       c.Kind + "/" + c.ID,
			kind:      c.Kind,
			title:     c.Name,
			filter:    c.Kind + " " + c.Name + " " + c.ID,
			info:      info,
			index:     i,
		})
	}
	d.list.SetItems(items...)
	d.list.SetSelected(sel)
	d.list.ScrollToSelected()
}

func (d *AgentModels) showEfforts(c *workspace.ModelChoice) {
	d.picked = c
	d.input.SetValue("")
	d.list.SetFilter("")
	cur := c.CurrentEffort
	if cur == "" {
		cur = c.DefaultEffort
	}
	items := []list.FilterableItem{&agentModelItem{
		Versioned: list.NewVersioned(),
		t:         d.com.Styles,
		key:       "",
		kind:      c.Kind,
		title:     "default",
		filter:    "default",
		info:      "what the model picks",
		index:     -1,
	}}
	sel := 0
	for i, e := range c.Efforts {
		info := e.Description
		if e.Level == cur {
			info = "current"
			sel = i + 1
		}
		items = append(items, &agentModelItem{
			Versioned: list.NewVersioned(),
			t:         d.com.Styles,
			key:       e.Level,
			kind:      c.Kind,
			title:     e.Level,
			filter:    e.Level,
			info:      info,
			index:     i,
		})
	}
	d.list.SetItems(items...)
	d.list.SetSelected(sel)
	d.list.ScrollToSelected()
}

// HandleMsg implements [Dialog].
func (d *AgentModels) HandleMsg(msg tea.Msg) Action {
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch {
	case key.Matches(km, d.keyMap.Close):
		if d.picked != nil {
			d.showModels()
			return nil
		}
		return ActionClose{}
	case d.picked != nil && key.Matches(km, d.keyMap.Back) && d.input.Value() == "":
		d.showModels()
		return nil
	case key.Matches(km, d.keyMap.Previous):
		if d.list.IsSelectedFirst() {
			d.list.SelectLast()
			d.list.ScrollToBottom()
			break
		}
		d.list.SelectPrev()
		d.list.ScrollToSelected()
	case key.Matches(km, d.keyMap.Next):
		if d.list.IsSelectedLast() {
			d.list.SelectFirst()
			d.list.ScrollToTop()
			break
		}
		d.list.SelectNext()
		d.list.ScrollToSelected()
	case key.Matches(km, d.keyMap.Select):
		it, ok := d.list.SelectedItem().(*agentModelItem)
		if !ok {
			break
		}
		if d.picked != nil {
			return ActionSelectAgentModel{Kind: d.picked.Kind, Model: d.picked.ID, Effort: it.key}
		}
		c := &d.choices[it.index]
		if len(c.Efforts) > 0 {
			d.showEfforts(c)
			return nil
		}
		return ActionSelectAgentModel{Kind: c.Kind, Model: c.ID}
	default:
		prev := d.input.Value()
		var cmd tea.Cmd
		d.input, cmd = d.input.Update(km)
		if v := d.input.Value(); v != prev {
			d.list.SetFilter(v)
			d.list.ScrollToTop()
			d.list.SetSelected(0)
		}
		return ActionCmd{cmd}
	}
	return nil
}

// Cursor returns the cursor position relative to the dialog.
func (d *AgentModels) Cursor() *tea.Cursor {
	return InputCursor(d.com.Styles, d.input.Cursor())
}

// Draw implements [Dialog].
func (d *AgentModels) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := d.com.Styles
	width := max(0, min(agentModelsDialogMaxWidth, area.Dx()-t.Dialog.View.GetHorizontalBorderSize()))
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()
	d.input.SetWidth(dialogInputTextWidth(t, d.input, innerWidth))

	heightOffset := t.Dialog.Title.GetVerticalFrameSize() + titleContentHeight +
		t.Dialog.InputPrompt.GetVerticalFrameSize() + inputContentHeight +
		t.Dialog.HelpView.GetVerticalFrameSize() +
		t.Dialog.View.GetVerticalFrameSize()
	maxAvailable := area.Dy() - t.Dialog.View.GetVerticalBorderSize()
	height := max(agentModelsDialogMinHeight, min(agentModelsDialogMaxHeight, heightOffset+d.list.TotalHeight(), maxAvailable))
	listHeight, listTotalHeight, _ := sizeDialogList(t, d.list, innerWidth, height)

	rc := NewRenderContext(t, width)
	rc.Title = "Choose a model"
	if d.picked != nil {
		rc.Title = tone.Mark(d.picked.Kind) + " " + d.picked.Name + " · effort"
	}
	rc.AddPart(t.Dialog.InputPrompt.Render(d.input.View()))
	if d.list.Height() >= len(d.list.FilteredItems()) {
		d.list.ScrollToTop()
	} else {
		d.list.ScrollToSelected()
	}
	listView := t.Dialog.List.Height(d.list.Height()).Render(d.list.Render())
	rc.AddPart(joinScrollbar(t, listView, listHeight, listTotalHeight, listHeight, d.list.Offset()))
	rc.Help = renderDialogHelp(t, &d.help, d, innerWidth)

	view := rc.Render()
	cur := d.Cursor()
	DrawCenterCursor(scr, area, view, cur)
	return cur
}

// ShortHelp implements [help.KeyMap].
func (d *AgentModels) ShortHelp() []key.Binding {
	if d.picked != nil {
		return []key.Binding{d.keyMap.UpDown, d.keyMap.Select, d.keyMap.Back, d.keyMap.Close}
	}
	return []key.Binding{d.keyMap.UpDown, d.keyMap.Select, d.keyMap.Close}
}

// FullHelp implements [help.KeyMap].
func (d *AgentModels) FullHelp() [][]key.Binding {
	return [][]key.Binding{{d.keyMap.Select, d.keyMap.Next, d.keyMap.Previous, d.keyMap.Back, d.keyMap.Close}}
}

// agentModelItem is one row: a model (under its agent's mark) or an effort.
type agentModelItem struct {
	*list.Versioned
	t       *styles.Styles
	key     string
	kind    string
	title   string
	filter  string
	info    string
	index   int
	m       fuzzy.Match
	cache   map[int]string
	focused bool
}

// Finished implements list.Item.
func (a *agentModelItem) Finished() bool { return true }

// Filter implements list.FilterableItem.
func (a *agentModelItem) Filter() string { return a.filter }

// ID implements list.Item.
func (a *agentModelItem) ID() string { return a.kind + ":" + a.key + ":" + a.title }

// SetFocused implements list.Focusable.
func (a *agentModelItem) SetFocused(focused bool) {
	if a.focused == focused {
		return
	}
	a.cache, a.focused = nil, focused
	a.Bump()
}

// SetMatch implements list.MatchSettable.
func (a *agentModelItem) SetMatch(m fuzzy.Match) {
	if sameFuzzyMatch(a.m, m) {
		return
	}
	a.cache, a.m = nil, m
	a.Bump()
}

// Render implements list.Item.
func (a *agentModelItem) Render(width int) string {
	mark := lipgloss.NewStyle().Foreground(tone.Tint(a.kind)).Render(tone.Mark(a.kind)) + " "
	st := ListItemStyles{
		ItemBlurred:     a.t.Dialog.NormalItem,
		ItemFocused:     a.t.Dialog.SelectedItem,
		InfoTextBlurred: a.t.Dialog.ListItem.InfoBlurred,
		InfoTextFocused: a.t.Dialog.ListItem.InfoFocused,
	}
	return mark + renderItem(st, a.title, a.info, a.focused, max(0, width-lipgloss.Width(mark)), a.cache, &a.m)
}
