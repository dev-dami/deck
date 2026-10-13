package dialog

import (
	"image/color"
	"slices"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dev-dami/deck/internal/config"
	deckconfig "github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/ui/tone"
)

// settingsRow is one option on the General, Agents or Team page.
type settingsRow struct {
	label string
	value string
	kind  string // an agent kind whose mark leads the value, if any
	// mark leads the value when kind is unset (a status dot), in markColor.
	mark      string
	markColor color.Color
	section   string // the heading the row sits under; "" for none
	peer      string // the peer a Team page row is about
	hint      string // shown under the rows while this one is selected
	// change steps the option (dir is 1 for the next value, -1 for the
	// previous; a switch flips either way) and returns what the UI must
	// do. nil for a read-only row.
	change func(dir int) Action
}

const (
	settingsLabelWidth = 24
	// settingsMarkWidth is the widest agent mark (">_").
	settingsMarkWidth = 2
)

var (
	motionValues     = []string{"on", "off"}
	modeValues       = []string{"plan", "ask", "yolo"}
	exitBannerValues = []string{string(config.ExitBannerDefault), string(config.ExitBannerCompact), string(config.ExitBannerNone)}
)

// cycle steps from cur through values by dir, wrapping; an unknown cur
// starts at the first value.
func cycle(values []string, cur string, dir int) string {
	i := slices.Index(values, cur)
	if i < 0 {
		return values[0]
	}
	n := len(values)
	return values[((i+dir)%n+n)%n]
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// rows are the options of the page shown.
func (s *Settings) rows() []settingsRow {
	switch s.tab {
	case SettingsTabGeneral:
		return s.generalRows()
	case SettingsTabAgents:
		return s.agentRows()
	case SettingsTabTeam:
		return s.teamRows()
	}
	return nil
}

// deckEdit shows a change to Deck's settings at once and asks the UI to save
// it. set must assign a value, not flip one, so it can be replayed.
func (s *Settings) deckEdit(label string, set func(*deckconfig.Config)) Action {
	set(&s.deck)
	return ActionSetDeckSettings{Edit: set, Label: label}
}

func (s *Settings) generalRows() []settingsRow {
	theme := "deck"
	if cfg := s.config(); cfg != nil && cfg.Options != nil && cfg.Options.TUI != nil && cfg.Options.TUI.ActiveTheme != "" {
		theme = cfg.Options.TUI.ActiveTheme
	}
	rows := []settingsRow{{
		label: "Theme",
		value: theme,
		hint:  "Enter opens the theme picker.",
		change: func(int) Action {
			return ActionOpenDialog{DialogID: ThemeID}
		},
	}}
	if s.editor != nil {
		mode := s.deck.DefaultMode
		if mode == "" {
			mode = "ask"
		}
		rows = append(rows,
			settingsRow{
				label: "Motion",
				value: s.deck.Motion,
				hint:  "Deck's animations. DECK_REDUCED_MOTION=1 turns them off whatever this says.",
				change: func(dir int) Action {
					v := cycle(motionValues, s.deck.Motion, dir)
					return s.deckEdit("Motion "+v, func(c *deckconfig.Config) { c.Motion = v })
				},
			},
			settingsRow{
				label: "Bell",
				value: onOff(s.deck.Bell),
				hint:  "Ring the terminal bell when a turn needs you.",
				change: func(int) Action {
					v := !s.deck.Bell
					return s.deckEdit("Bell "+onOff(v), func(c *deckconfig.Config) { c.Bell = v })
				},
			},
			settingsRow{
				label: "Default mode",
				value: mode,
				hint:  "The mode new threads start in. yolo is ignored at startup: threads start in ask.",
				change: func(dir int) Action {
					v := cycle(modeValues, mode, dir)
					return s.deckEdit("Default mode "+v, func(c *deckconfig.Config) { c.DefaultMode = v })
				},
			},
		)
	}
	rows = append(rows,
		settingsRow{
			label: "Compact mode",
			value: onOff(s.compact),
			hint:  "Hide the sidebar. ctrl+b toggles it too.",
			change: func(int) Action {
				s.compact = !s.compact
				return ActionToggleCompactMode{}
			},
		},
		settingsRow{
			label: "Transparent background",
			value: onOff(s.transparent),
			hint:  "Let the terminal's own background show through.",
			change: func(int) Action {
				s.transparent = !s.transparent
				return ActionToggleTransparentBackground{}
			},
		},
		settingsRow{
			label: "Mouse",
			value: onOff(s.mouse),
			hint:  "Off leaves selection and copy to the terminal.",
			change: func(int) Action {
				s.mouse = !s.mouse
				return ActionToggleMouseSupport{}
			},
		},
		settingsRow{
			label: "Exit banner",
			value: s.exitBanner,
			hint:  "What Deck prints when it quits: the logo, just the resume line, or nothing.",
			change: func(dir int) Action {
				s.exitBanner = cycle(exitBannerValues, s.exitBanner, dir)
				return ActionSetTUIOption{Key: "options.tui.exit_banner", Value: s.exitBanner, Label: "Exit banner: " + s.exitBanner}
			},
		},
	)
	return rows
}

func (s *Settings) agentRows() []settingsRow {
	if s.editor == nil {
		return nil
	}
	kinds := s.kinds
	if len(kinds) == 0 {
		kinds = deckconfig.Kinds
	}
	name := s.deck.UserName
	if name == "" {
		name = "your name"
	}
	rows := []settingsRow{
		{
			label: "Default agent",
			value: s.deck.DefaultAgent,
			kind:  s.deck.DefaultAgent,
			hint:  "Who answers a message that names no agent. Cycles through the CLIs Deck found.",
			change: func(dir int) Action {
				v := cycle(kinds, s.deck.DefaultAgent, dir)
				return s.deckEdit("Default agent "+v, func(c *deckconfig.Config) { c.DefaultAgent = v })
			},
		},
		{
			label: "Track changes",
			value: onOff(s.deck.TrackChanges),
			hint:  "Snapshot the work tree with each message, for the changes panel and its undo.",
			change: func(int) Action {
				v := !s.deck.TrackChanges
				return s.deckEdit("Track changes "+onOff(v), func(c *deckconfig.Config) { c.TrackChanges = v })
			},
		},
		{
			label: "Automatic titles",
			value: onOff(s.deck.AutoTitle),
			hint:  "The default agent's model writes a short title for a thread after its first turn; a title you type is never replaced.",
			change: func(int) Action {
				v := !s.deck.AutoTitle
				return s.deckEdit("Automatic titles "+onOff(v), func(c *deckconfig.Config) { c.AutoTitle = v })
			},
		},
		{
			label: "Parallel tasks",
			value: strconv.Itoa(max(1, s.deck.MaxParallel)),
			hint:  "How many tasks a thread with orchestration on runs at once (press o in the Tasks pane).",
			change: func(dir int) Action {
				v := max(1, min(8, max(1, s.deck.MaxParallel)+dir))
				return s.deckEdit("Parallel tasks "+strconv.Itoa(v), func(c *deckconfig.Config) { c.MaxParallel = v })
			},
		},
		{
			label: "Tell agents your name",
			value: onOff(s.deck.TellAgentsName),
			hint:  "Tell the agents they are working with " + name + ".",
			change: func(int) Action {
				v := !s.deck.TellAgentsName
				return s.deckEdit("Tell agents your name "+onOff(v), func(c *deckconfig.Config) { c.TellAgentsName = v })
			},
		},
		{
			label: "Deck attribution",
			value: onOff(s.deck.DeckAttribution),
			hint:  "Commits agents make credit Deck: other AI attribution is stripped and replaced. Applies to agents started after the change.",
			change: func(int) Action {
				v := !s.deck.DeckAttribution
				return s.deckEdit("Deck attribution "+onOff(v), func(c *deckconfig.Config) { c.DeckAttribution = v })
			},
		},
		{
			label: "Author commits as Deck",
			value: onOff(s.deck.DeckAuthor),
			hint:  "Agents' commits are authored by Deck, with you as committer, so Deck shows among the repository's contributors. Needs Deck attribution on.",
			change: func(int) Action {
				v := !s.deck.DeckAuthor
				return s.deckEdit("Author commits as Deck "+onOff(v), func(c *deckconfig.Config) { c.DeckAuthor = v })
			},
		},
	}
	for _, kind := range deckconfig.Kinds {
		row := settingsRow{
			label: kind + " CLI",
			kind:  kind,
			value: "checking…",
			hint:  "Set " + kind + "_binary in ~/.deck/config.json to use another command.",
		}
		for _, b := range s.binaries {
			if b.Kind != kind {
				continue
			}
			if b.Path != "" {
				row.value = b.Path
			} else {
				row.value = b.Command + " not found"
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// rowLines renders the page's rows and the selected row's hint.
func (s *Settings) rowLines(width int) []string {
	t := s.com.Styles
	rows := s.rows()
	if len(rows) == 0 {
		return []string{t.Dialog.SecondaryText.Width(width).Render("Agent settings are only here when Deck runs its agents.")}
	}
	sel := min(max(0, s.sel[s.tab]), len(rows)-1)
	lines := make([]string, 0, len(rows)+2)
	for i, r := range rows {
		lines = append(lines, s.renderRow(r, i == sel, width))
	}
	if hint := rows[sel].hint; hint != "" {
		lines = append(lines, "", t.Dialog.SecondaryText.Width(width).Render(ansi.Truncate(hint, max(0, width-2), "…")))
	}
	return lines
}

// renderRow draws one row: its label, then its value (under the agent's
// mark when it names one). The item styles pad a cell on each side.
func (s *Settings) renderRow(r settingsRow, selected bool, width int) string {
	t := s.com.Styles
	text := max(0, width-2)
	labelW := min(settingsLabelWidth, text/2)
	label := padRight(ansi.Truncate(r.label, labelW, "…"), labelW)

	mark, markColor := "", r.markColor
	switch {
	case r.kind != "":
		mark, markColor = padRight(tone.Mark(r.kind), settingsMarkWidth)+" ", tone.Tint(r.kind)
	case r.mark != "":
		mark = padRight(r.mark, settingsMarkWidth) + " "
	}
	valueW := max(0, text-labelW-ansi.StringWidth(mark))
	value := padRight(ansi.Truncate(r.value, valueW, "…"), valueW)

	if selected {
		return t.Dialog.SelectedItem.Render(label + mark + value)
	}
	if mark != "" && markColor != nil {
		mark = lipgloss.NewStyle().Foreground(markColor).Render(mark)
	}
	valueStyle := t.Dialog.ListItem.InfoFocused
	if r.change == nil {
		valueStyle = t.Dialog.ListItem.InfoBlurred
	}
	return t.Dialog.NormalItem.UnsetPaddingRight().Render(label) + mark + valueStyle.Render(value) + " "
}

// padRight pads s with spaces to width cells.
func padRight(s string, width int) string {
	return s + strings.Repeat(" ", max(0, width-ansi.StringWidth(s)))
}
