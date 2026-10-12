package dialog

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/dev-dami/deck/internal/config"
	deckconfig "github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/styles"
	"github.com/dev-dami/deck/internal/workspace"
)

const (
	// SettingsID is the identifier for the settings dialog.
	SettingsID = "settings"
	// SettingsUsageID asks for the settings dialog open on its Usage tab.
	// It is only ever an open request: the dialog itself is SettingsID.
	SettingsUsageID = "settings:usage"
	// SettingsTeamID asks for the settings dialog open on its Team tab.
	SettingsTeamID = "settings:team"

	settingsDialogMaxWidth = 76
	// usageLoadTimeout bounds one usage report's queries.
	usageLoadTimeout = 10 * time.Second
)

// SettingsTab is one page of the settings dialog.
type SettingsTab int

const (
	SettingsTabGeneral SettingsTab = iota
	SettingsTabAgents
	SettingsTabUsage
	SettingsTabTeam
	settingsTabCount
)

// String returns the tab's label.
func (t SettingsTab) String() string {
	switch t {
	case SettingsTabAgents:
		return "Agents"
	case SettingsTabUsage:
		return "Usage"
	case SettingsTabTeam:
		return "Team"
	}
	return "General"
}

// ActionSetDeckSettings asks the UI to change Deck's own settings
// (~/.deck/config.json) with Edit and save them.
type ActionSetDeckSettings struct {
	Edit  func(*deckconfig.Config)
	Label string // what changed, for the status line
}

// ActionSetTUIOption asks the UI to save one TUI option of Deck's config.
type ActionSetTUIOption struct {
	Key   string // a config path, e.g. "options.tui.exit_banner"
	Value any
	Label string // what changed, for the status line
}

// usageRange is one span the Usage tab sums over.
type usageRange struct {
	label string
	days  int // 1 = today; 0 = all time
}

var usageRanges = []usageRange{
	{"Today", 1},
	{"7 days", 7},
	{"30 days", 30},
	{"All time", 0},
}

// defaultUsageRange is the range the Usage tab opens on (7 days).
const defaultUsageRange = 1

// binaryStatus is where an agent CLI's command was found, if it was.
type binaryStatus struct {
	Kind    string
	Command string // as configured
	Path    string // as found on PATH; "" when not found
}

// settingsBinariesMsg carries the agent CLIs looked up on PATH.
type settingsBinariesMsg struct {
	binaries []binaryStatus
}

// settingsUsageMsg carries a usage report; req tells a stale one apart.
type settingsUsageMsg struct {
	req    int
	report workspace.UsageReport
	err    error
}

// Settings is the settings dialog: General and Agents pages of options, a
// Usage page that sums what the agents used, from Deck's local record, and a
// Team page for the peer link between Deck instances.
type Settings struct {
	com     *common.Common
	help    help.Model
	spinner spinner.Model
	tab     SettingsTab
	sel     [settingsTabCount]int // the selected row of each page with rows
	team    teamState

	editor   workspace.SettingsEditor // nil when the workspace has no Deck settings
	reader   workspace.UsageReader    // nil when there is no usage record
	deck     deckconfig.Config        // as shown; changed here, saved by the UI
	kinds    []string                 // the agent CLIs Deck found
	binaries []binaryStatus           // nil until looked up

	// The TUI options as shown; the UI applies and saves each change.
	compact, transparent, mouse bool
	exitBanner                  string

	rng      int
	report   *workspace.UsageReport
	usageErr error
	req      int
	loading  bool

	keyMap struct {
		Up, Down, Left, Right, Activate  key.Binding
		NextTab, PrevTab, GoTab, Refresh key.Binding
		Close, FullView                  key.Binding
		// Team page
		AddPeer, CopyFingerprint, Edit, ApplyNow key.Binding
		Save, Back, Run, Yes, No                 key.Binding
		// help only
		UpDown, Change, Range key.Binding
	}
}

var (
	_ Dialog        = (*Settings)(nil)
	_ LoadingDialog = (*Settings)(nil)
)

// NewSettings opens the settings dialog on tab. Call [Settings.InitialCmd]
// for what it loads in the background.
func NewSettings(com *common.Common, tab SettingsTab) *Settings {
	s := &Settings{com: com, tab: tab, rng: defaultUsageRange, exitBanner: string(config.ExitBannerDefault), mouse: true}
	s.help = help.New()
	s.help.Styles = com.Styles.DialogHelpStyles()
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = com.Styles.Dialog.Spinner
	s.spinner = sp

	if com.Workspace != nil {
		s.editor, _ = com.Workspace.(workspace.SettingsEditor)
		s.reader, _ = com.Workspace.(workspace.UsageReader)
		if p, ok := com.Workspace.(workspace.Peers); ok {
			s.team.link = p.PeerLink()
		}
	}
	if s.editor != nil {
		s.deck = s.editor.DeckSettings()
		s.kinds = s.editor.AgentKinds()
	}
	if cfg := s.config(); cfg != nil && cfg.Options != nil && cfg.Options.TUI != nil {
		tui := cfg.Options.TUI
		s.compact = tui.CompactMode
		s.transparent = tui.IsTransparent()
		s.mouse = tui.Mouse == nil || *tui.Mouse
		if tui.ExitBanner != "" {
			s.exitBanner = string(tui.ExitBanner)
		}
	}

	s.keyMap.Up = key.NewBinding(key.WithKeys("up", "k", "ctrl+p"), key.WithHelp("↑", "previous"))
	s.keyMap.Down = key.NewBinding(key.WithKeys("down", "j", "ctrl+n"), key.WithHelp("↓", "next"))
	s.keyMap.Left = key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("←", "previous value"))
	s.keyMap.Right = key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("→", "next value"))
	s.keyMap.Activate = key.NewBinding(key.WithKeys("enter", " ", "space"), key.WithHelp("enter", "change"))
	s.keyMap.NextTab = key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next page"))
	s.keyMap.PrevTab = key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "previous page"))
	s.keyMap.GoTab = key.NewBinding(key.WithKeys("1", "2", "3", "4"), key.WithHelp("1-4", "page"))
	s.keyMap.Refresh = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh"))
	s.keyMap.Close = CloseKey
	s.keyMap.FullView = key.NewBinding(key.WithKeys(UsageKey), key.WithHelp(UsageKey, "full view"))
	s.keyMap.UpDown = key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑/↓", "choose"))
	s.keyMap.Change = key.NewBinding(key.WithKeys("enter", "left", "right"), key.WithHelp("enter/←/→", "change"))
	s.keyMap.Range = key.NewBinding(key.WithKeys("left", "right"), key.WithHelp("←/→", "range"))
	s.keyMap.AddPeer = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "add teammate"))
	s.keyMap.CopyFingerprint = key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "copy fingerprint"))
	s.keyMap.Edit = key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit"))
	s.keyMap.ApplyNow = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "apply now"))
	s.keyMap.Save = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "save"))
	s.keyMap.Back = key.NewBinding(key.WithKeys("esc", "alt+esc"), key.WithHelp("esc", "back"))
	s.keyMap.Run = key.NewBinding(key.WithKeys("enter", " ", "space"), key.WithHelp("enter", "run"))
	s.keyMap.Yes = key.NewBinding(key.WithKeys("y", "enter"), key.WithHelp("y", "remove"))
	s.keyMap.No = key.NewBinding(key.WithKeys("n", "esc", "alt+esc"), key.WithHelp("n", "keep"))
	return s
}

// ID implements [Dialog].
func (*Settings) ID() string { return SettingsID }

// Tab is the page shown.
func (s *Settings) Tab() SettingsTab { return s.tab }

// InitialCmd looks up the agent CLIs and, on the Usage page, loads usage.
func (s *Settings) InitialCmd() tea.Cmd {
	cmds := []tea.Cmd{s.lookBinaries()}
	switch s.tab {
	case SettingsTabUsage:
		cmds = append(cmds, s.loadUsage())
	case SettingsTabTeam:
		cmds = append(cmds, s.watchTeam())
	}
	return tea.Batch(cmds...)
}

// SetTab shows tab, loading what it needs.
func (s *Settings) SetTab(tab SettingsTab) tea.Cmd {
	s.tab = (tab%settingsTabCount + settingsTabCount) % settingsTabCount
	switch s.tab {
	case SettingsTabUsage:
		return s.loadUsage()
	case SettingsTabTeam:
		return tea.Batch(s.watchTeam(), s.tickTeam())
	case SettingsTabAgents:
		if s.binaries == nil {
			return s.lookBinaries()
		}
	}
	return nil
}

func (s *Settings) config() *config.Config {
	if s.com.Workspace == nil {
		return nil
	}
	return s.com.Config()
}

// HandleMsg implements [Dialog].
func (s *Settings) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case settingsBinariesMsg:
		s.binaries = msg.binaries
	case settingsUsageMsg:
		if msg.req != s.req {
			return nil // a range since left
		}
		s.StopLoading()
		s.usageErr = msg.err
		if msg.err == nil {
			r := msg.report
			s.report = &r
		}
	case spinner.TickMsg:
		if s.loading {
			var cmd tea.Cmd
			s.spinner, cmd = s.spinner.Update(msg)
			return ActionCmd{Cmd: cmd}
		}
	case teamSnapshotMsg, teamDoneMsg, teamPingMsg, teamTickMsg:
		return s.handleTeamMsg(msg)
	case tea.KeyPressMsg:
		return s.handleKey(msg)
	}
	return nil
}

func (s *Settings) handleKey(msg tea.KeyPressMsg) Action {
	if s.tab == SettingsTabTeam {
		if act, ok := s.handleTeamKey(msg); ok {
			return act
		}
	}
	switch {
	case key.Matches(msg, s.keyMap.Close):
		return ActionClose{}
	case key.Matches(msg, s.keyMap.FullView):
		return ActionOpenDialog{DialogID: UsageID}
	case key.Matches(msg, s.keyMap.NextTab):
		return ActionCmd{Cmd: s.SetTab(s.tab + 1)}
	case key.Matches(msg, s.keyMap.PrevTab):
		return ActionCmd{Cmd: s.SetTab(s.tab - 1)}
	case key.Matches(msg, s.keyMap.GoTab):
		return ActionCmd{Cmd: s.SetTab(SettingsTab(msg.String()[0] - '1'))}
	}

	if s.tab == SettingsTabUsage {
		switch {
		case key.Matches(msg, s.keyMap.Left):
			return ActionCmd{Cmd: s.setRange(s.rng - 1)}
		case key.Matches(msg, s.keyMap.Right):
			return ActionCmd{Cmd: s.setRange(s.rng + 1)}
		case key.Matches(msg, s.keyMap.Refresh):
			return ActionCmd{Cmd: s.loadUsage()}
		}
		return nil
	}

	rows := s.rows()
	if len(rows) == 0 {
		return nil
	}
	sel := &s.sel[s.tab]
	*sel = min(max(0, *sel), len(rows)-1)
	switch {
	case key.Matches(msg, s.keyMap.Up):
		*sel = (*sel - 1 + len(rows)) % len(rows)
	case key.Matches(msg, s.keyMap.Down):
		*sel = (*sel + 1) % len(rows)
	case key.Matches(msg, s.keyMap.Activate, s.keyMap.Right):
		if r := rows[*sel]; r.change != nil {
			return r.change(1)
		}
	case key.Matches(msg, s.keyMap.Left):
		if r := rows[*sel]; r.change != nil {
			return r.change(-1)
		}
	}
	return nil
}

// setRange switches the Usage page to another range and loads it.
func (s *Settings) setRange(i int) tea.Cmd {
	n := len(usageRanges)
	s.rng = (i%n + n) % n
	s.report = nil
	return s.loadUsage()
}

// loadUsage asks for the current range's report, off the UI loop.
func (s *Settings) loadUsage() tea.Cmd {
	if s.reader == nil {
		return nil
	}
	s.req++
	req, days, reader := s.req, usageRanges[s.rng].days, s.reader
	return tea.Batch(s.StartLoading(), func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), usageLoadTimeout)
		defer cancel()
		r, err := reader.Usage(ctx, days)
		return settingsUsageMsg{req: req, report: r, err: err}
	})
}

// lookBinaries finds each agent CLI's command on PATH, off the UI loop.
func (s *Settings) lookBinaries() tea.Cmd {
	if s.editor == nil {
		return nil
	}
	cfg := s.deck
	return func() tea.Msg {
		out := make([]binaryStatus, 0, len(deckconfig.Kinds))
		for _, kind := range deckconfig.Kinds {
			b := binaryStatus{Kind: kind, Command: cfg.Binary(kind)}
			if p, err := exec.LookPath(b.Command); err == nil {
				b.Path = p
			}
			out = append(out, b)
		}
		return settingsBinariesMsg{binaries: out}
	}
}

// StartLoading implements [LoadingDialog].
func (s *Settings) StartLoading() tea.Cmd {
	if s.loading {
		return nil
	}
	s.loading = true
	return s.spinner.Tick
}

// StopLoading implements [LoadingDialog].
func (s *Settings) StopLoading() {
	s.loading = false
}

// Draw implements [Dialog].
func (s *Settings) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := s.com.Styles
	width := max(0, min(settingsDialogMaxWidth, area.Dx()-t.Dialog.View.GetHorizontalBorderSize()))
	innerWidth := max(0, width-t.Dialog.View.GetHorizontalFrameSize())

	var body []string
	focus := -1 // a line that must stay on screen
	switch s.tab {
	case SettingsTabUsage:
		body = s.usageLines(innerWidth)
	case SettingsTabTeam:
		body, focus = s.teamLines(innerWidth)
	default:
		body = s.rowLines(innerWidth)
	}
	// Keep the frame on screen: drop what does not fit, at the bottom, or
	// around the focused line when it would fall off.
	maxBody := area.Dy() - t.Dialog.View.GetVerticalFrameSize() -
		t.Dialog.Title.GetVerticalFrameSize() - titleContentHeight -
		t.Dialog.HelpView.GetVerticalFrameSize() - 1
	if maxBody >= 0 && len(body) > maxBody {
		start := 0
		if focus >= maxBody {
			start = min(focus-maxBody/2, len(body)-maxBody)
		}
		body = body[start : start+maxBody]
	}

	rc := NewRenderContext(t, width)
	rc.Title = "Settings"
	rc.TitleInfo = s.tabsView(t)
	rc.AddPart(strings.Join(body, "\n"))
	rc.Help = renderDialogHelp(t, &s.help, s, innerWidth)
	DrawCenter(scr, area, rc.Render())
	return nil
}

// tabsView renders the page selector on the title line.
func (s *Settings) tabsView(t *styles.Styles) string {
	var b strings.Builder
	for tab := SettingsTabGeneral; tab < settingsTabCount; tab++ {
		bullet := t.Radio.Off
		if tab == s.tab {
			bullet = t.Radio.On
		}
		b.WriteString(" " + bullet.Render() + t.Radio.Label.Padding(0, 1).Render(tab.String()))
	}
	return b.String()
}

// ShortHelp implements [help.KeyMap].
func (s *Settings) ShortHelp() []key.Binding {
	switch s.tab {
	case SettingsTabUsage:
		return []key.Binding{s.keyMap.Range, s.keyMap.FullView, s.keyMap.Refresh, s.keyMap.NextTab, s.keyMap.Close}
	case SettingsTabTeam:
		return s.teamHelp()
	}
	return []key.Binding{s.keyMap.UpDown, s.keyMap.Change, s.keyMap.NextTab, s.keyMap.Close}
}

// FullHelp implements [help.KeyMap].
func (s *Settings) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{s.keyMap.Up, s.keyMap.Down, s.keyMap.Left, s.keyMap.Right, s.keyMap.Activate},
		{s.keyMap.NextTab, s.keyMap.PrevTab, s.keyMap.GoTab, s.keyMap.Refresh, s.keyMap.Close},
		{s.keyMap.AddPeer, s.keyMap.CopyFingerprint, s.keyMap.Edit, s.keyMap.ApplyNow},
	}
}
