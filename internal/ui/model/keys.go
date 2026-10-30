package model

import (
	"charm.land/bubbles/v2/key"

	"github.com/dev-dami/deck/internal/ui/dialog"
	"github.com/dev-dami/deck/internal/ui/notice"
)

type KeyMap struct {
	Editor struct {
		SendMessage  key.Binding
		OpenEditor   key.Binding
		Newline      key.Binding
		AddImage     key.Binding
		PasteImage   key.Binding
		MentionFile  key.Binding
		MentionAgent key.Binding
		Commands     key.Binding

		// Attachments key maps
		AttachmentDeleteMode key.Binding
		Escape               key.Binding
		DeleteAllAttachments key.Binding

		// History navigation
		HistoryPrev key.Binding
		HistoryNext key.Binding

		// CopySelection copies the current textarea selection to the
		// clipboard.
		CopySelection key.Binding

		// CutSelection copies the current textarea selection to the
		// clipboard and deletes it from the textarea.
		CutSelection key.Binding

		// SelectAll selects all text in the textarea.
		SelectAll key.Binding

		// PasteText pastes clipboard text into the textarea, as an
		// alternative to bracketed paste.
		PasteText key.Binding
	}

	Chat struct {
		NewSession     key.Binding
		AddAttachment  key.Binding
		Cancel         key.Binding
		Tab            key.Binding
		Details        key.Binding
		TogglePills    key.Binding
		ToggleSidebar  key.Binding
		PillLeft       key.Binding
		PillRight      key.Binding
		Down           key.Binding
		Up             key.Binding
		UpDown         key.Binding
		DownOneItem    key.Binding
		UpOneItem      key.Binding
		UpDownOneItem  key.Binding
		PageDown       key.Binding
		PageUp         key.Binding
		HalfPageDown   key.Binding
		HalfPageUp     key.Binding
		Home           key.Binding
		End            key.Binding
		EndFollow      key.Binding
		Copy           key.Binding
		ClearHighlight key.Binding
		Expand         key.Binding
		ScrollLeft     key.Binding
		ScrollRight    key.Binding
		FocusSidebar   key.Binding
		FocusChat      key.Binding
	}

	// Sidebar keys work while the sidebar (the strip of panes) has focus,
	// after the focused pane has had its turn (strip_keys.go).
	Sidebar struct {
		Left        key.Binding // previous column; from the first, the chat
		Right       key.Binding // next column
		Up          key.Binding // scroll up, then previous pane
		Down        key.Binding // scroll down, then next pane
		PageUp      key.Binding
		PageDown    key.Binding
		Top         key.Binding
		Bottom      key.Binding
		Close       key.Binding
		PaneLeft    key.Binding // consume into / expel to the left
		PaneRight   key.Binding // consume into / expel to the right
		ColumnLeft  key.Binding
		ColumnRight key.Binding
		Narrow      key.Binding
		Widen       key.Binding
		Reopen      key.Binding
		Select      key.Binding // in the reopen list
		Back        key.Binding // back to the editor

		// Help-only summaries of the pairs above.
		Move       key.Binding
		MovePane   key.Binding
		MoveColumn key.Binding
		Resize     key.Binding
	}

	// SidebarColumnLeft and SidebarColumnRight move the sidebar's focused
	// column from anywhere, without leaving the editor or the chat.
	SidebarColumnLeft  key.Binding
	SidebarColumnRight key.Binding

	Initialize struct {
		Yes,
		No,
		Enter,
		Switch key.Binding
	}

	// Global key maps
	Quit       key.Binding
	Help       key.Binding
	Commands   key.Binding
	Models     key.Binding
	Suspend    key.Binding
	Sessions   key.Binding
	Settings   key.Binding
	Tab        key.Binding
	ToggleYolo key.Binding
	ShiftTab   key.Binding
	Git        key.Binding
	Notices    key.Binding
	Usage      key.Binding
}

func DefaultKeyMap() KeyMap {
	km := KeyMap{
		Quit: key.NewBinding(
			key.WithKeys("ctrl+c"),
			key.WithHelp("ctrl+c", "quit"),
		),
		Help: key.NewBinding(
			key.WithKeys("ctrl+g"),
			key.WithHelp("ctrl+g", "more"),
		),
		Commands: key.NewBinding(
			key.WithKeys("ctrl+p"),
			key.WithHelp("ctrl+p", "commands"),
		),
		// The Agent picker: the thread's lead, its model and effort.
		Models: key.NewBinding(
			key.WithKeys("ctrl+m", "ctrl+l"),
			key.WithHelp("ctrl+l", "agent"),
		),
		Suspend: key.NewBinding(
			key.WithKeys("ctrl+z"),
			key.WithHelp("ctrl+z", "suspend"),
		),
		Sessions: key.NewBinding(
			key.WithKeys("ctrl+s"),
			key.WithHelp("ctrl+s", "sessions"),
		),
		// Only terminals that report modified keys (the kitty keyboard
		// protocol) can send ctrl+,; elsewhere Settings is in the palette.
		Settings: key.NewBinding(
			key.WithKeys("ctrl+,"),
			key.WithHelp("ctrl+,", "settings"),
		),
		Tab: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "change focus"),
		),
		ToggleYolo: key.NewBinding(
			key.WithKeys("ctrl+y"),
			key.WithHelp("ctrl+y", "toggle yolo"),
		),
		ShiftTab: key.NewBinding(
			key.WithKeys("shift+tab"),
			key.WithHelp("shift+tab", "mode"),
		),
		Git: key.NewBinding(
			key.WithKeys("ctrl+x"),
			key.WithHelp("ctrl+x", "git & PRs"),
		),
		// alt+n: ctrl+u deletes to the line start in the editor. Inline
		// question forms take alt+n first, for their notes.
		Notices: key.NewBinding(
			key.WithKeys(notice.HistoryKey),
			key.WithHelp(notice.HistoryKey, "notifications"),
		),
		// alt+u: ctrl+u deletes to the line start in the editor.
		Usage: key.NewBinding(
			key.WithKeys(dialog.UsageKey),
			key.WithHelp(dialog.UsageKey, "usage"),
		),
	}

	km.Editor.SendMessage = key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "send"),
	)
	km.Editor.OpenEditor = key.NewBinding(
		key.WithKeys("ctrl+o"),
		key.WithHelp("ctrl+o", "open editor"),
	)
	km.Editor.Newline = key.NewBinding(
		key.WithKeys("shift+enter", "ctrl+j"),
		// "ctrl+j" is a common keybinding for newline in many editors. If
		// the terminal supports "shift+enter", we substitute the help tex
		// to reflect that.
		key.WithHelp("ctrl+j", "newline"),
	)
	km.Editor.AddImage = key.NewBinding(
		key.WithKeys("ctrl+f"),
		key.WithHelp("ctrl+f", "add image"),
	)
	km.Editor.PasteImage = key.NewBinding(
		key.WithKeys("ctrl+v"),
		key.WithHelp("ctrl+v", "paste image from clipboard"),
	)
	km.Editor.PasteText = key.NewBinding(
		key.WithKeys("ctrl+shift+v"),
		key.WithHelp("ctrl+shift+v", "paste text"),
	)
	km.Editor.MentionFile = key.NewBinding(
		key.WithKeys("#"),
		key.WithHelp("#", "mention file"),
	)
	km.Editor.MentionAgent = key.NewBinding(
		key.WithKeys("@", "$"),
		key.WithHelp("@", "agents & people ($ too)"),
	)
	km.Editor.Commands = key.NewBinding(
		key.WithKeys("/"),
		key.WithHelp("/", "commands"),
	)
	km.Editor.AttachmentDeleteMode = key.NewBinding(
		key.WithKeys("ctrl+r"),
		key.WithHelp("ctrl+r+{i}", "delete attachment at index i"),
	)
	km.Editor.Escape = key.NewBinding(
		key.WithKeys("esc", "alt+esc"),
		key.WithHelp("esc", "cancel delete mode"),
	)
	km.Editor.DeleteAllAttachments = key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("ctrl+r+r", "delete all attachments"),
	)
	km.Editor.HistoryPrev = key.NewBinding(
		key.WithKeys("up"),
	)
	km.Editor.HistoryNext = key.NewBinding(
		key.WithKeys("down"),
	)
	km.Editor.CopySelection = key.NewBinding(
		key.WithKeys("ctrl+shift+c"),
		key.WithHelp("ctrl+shift+c", "copy selection"),
	)
	km.Editor.CutSelection = key.NewBinding(
		key.WithKeys("ctrl+shift+x"),
		key.WithHelp("ctrl+shift+x", "cut selection"),
	)
	km.Editor.SelectAll = key.NewBinding(
		key.WithKeys("ctrl+shift+a"),
		key.WithHelp("ctrl+shift+a", "select all"),
	)

	km.Chat.NewSession = key.NewBinding(
		key.WithKeys("ctrl+n"),
		key.WithHelp("ctrl+n", "new session"),
	)
	km.Chat.AddAttachment = key.NewBinding(
		key.WithKeys("ctrl+f"),
		key.WithHelp("ctrl+f", "add attachment"),
	)
	km.Chat.Cancel = key.NewBinding(
		key.WithKeys("esc", "alt+esc"),
		key.WithHelp("esc", "cancel"),
	)
	km.Chat.Tab = key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "change focus"),
	)
	km.Chat.Details = key.NewBinding(
		key.WithKeys("ctrl+d"),
		key.WithHelp("ctrl+d", "toggle details"),
	)
	km.Chat.TogglePills = key.NewBinding(
		key.WithKeys("ctrl+t", "ctrl+space"),
		key.WithHelp("ctrl+t", "toggle tasks"),
	)
	km.Chat.ToggleSidebar = key.NewBinding(
		key.WithKeys("ctrl+b"),
		key.WithHelp("ctrl+b", "toggle sidebar"),
	)
	km.Chat.PillLeft = key.NewBinding(
		key.WithKeys("left"),
		key.WithHelp("←/→", "switch section"),
	)
	km.Chat.PillRight = key.NewBinding(
		key.WithKeys("right"),
		key.WithHelp("←/→", "switch section"),
	)

	km.Chat.Down = key.NewBinding(
		key.WithKeys("down", "ctrl+j", "j"),
		key.WithHelp("↓", "down"),
	)
	km.Chat.Up = key.NewBinding(
		key.WithKeys("up", "ctrl+k", "k"),
		key.WithHelp("↑", "up"),
	)
	km.Chat.UpDown = key.NewBinding(
		key.WithKeys("up", "down"),
		key.WithHelp("↑↓", "scroll"),
	)
	km.Chat.UpOneItem = key.NewBinding(
		key.WithKeys("shift+up", "K"),
		key.WithHelp("shift+↑", "up one item"),
	)
	km.Chat.DownOneItem = key.NewBinding(
		key.WithKeys("shift+down", "J"),
		key.WithHelp("shift+↓", "down one item"),
	)
	km.Chat.UpDownOneItem = key.NewBinding(
		key.WithKeys("shift+up", "shift+down"),
		key.WithHelp("shift+↑↓", "scroll one item"),
	)
	km.Chat.HalfPageDown = key.NewBinding(
		key.WithKeys("d"),
		key.WithHelp("d", "half page down"),
	)
	km.Chat.PageDown = key.NewBinding(
		key.WithKeys("pgdown", " ", "f"),
		key.WithHelp("f/pgdn", "page down"),
	)
	km.Chat.PageUp = key.NewBinding(
		key.WithKeys("pgup", "b"),
		key.WithHelp("b/pgup", "page up"),
	)
	km.Chat.HalfPageUp = key.NewBinding(
		key.WithKeys("u"),
		key.WithHelp("u", "half page up"),
	)
	km.Chat.Home = key.NewBinding(
		key.WithKeys("g", "home"),
		key.WithHelp("g", "home"),
	)
	km.Chat.End = key.NewBinding(
		key.WithKeys("G", "end"),
		key.WithHelp("G", "end"),
	)
	km.Chat.EndFollow = key.NewBinding(
		key.WithKeys("ctrl+end"),
	)
	km.Chat.Copy = key.NewBinding(
		key.WithKeys("c", "y", "C", "Y"),
		key.WithHelp("c/y", "copy"),
	)
	km.Chat.ClearHighlight = key.NewBinding(
		key.WithKeys("esc", "alt+esc"),
		key.WithHelp("esc", "clear selection"),
	)
	km.Chat.Expand = key.NewBinding(
		key.WithKeys("space"),
		key.WithHelp("space", "expand/collapse"),
	)
	km.Chat.ScrollLeft = key.NewBinding(
		key.WithKeys("shift+left", "H"),
		key.WithHelp("shift+←/H", "scroll left"),
	)
	km.Chat.ScrollRight = key.NewBinding(
		key.WithKeys("shift+right", "L"),
		key.WithHelp("shift+→/L", "scroll right"),
	)
	km.Chat.FocusSidebar = key.NewBinding(
		key.WithKeys("l", "right"),
		key.WithHelp("l/→", "focus sidebar"),
	)
	km.Chat.FocusChat = key.NewBinding(
		key.WithKeys("h", "left"),
		key.WithHelp("h/←", "focus chat"),
	)
	km.Sidebar.Left = key.NewBinding(key.WithKeys("h", "left"), key.WithHelp("h/←", "column left"))
	km.Sidebar.Right = key.NewBinding(key.WithKeys("l", "right"), key.WithHelp("l/→", "column right"))
	km.Sidebar.Up = key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("k/↑", "up"))
	km.Sidebar.Down = key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("j/↓", "down"))
	km.Sidebar.PageUp = key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "page up"))
	km.Sidebar.PageDown = key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdn", "page down"))
	km.Sidebar.Top = key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g", "top"))
	km.Sidebar.Bottom = key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G", "bottom"))
	km.Sidebar.Close = key.NewBinding(key.WithKeys("x", "ctrl+w"), key.WithHelp("x", "close pane"))
	km.Sidebar.PaneLeft = key.NewBinding(key.WithKeys("["), key.WithHelp("[", "pane left"))
	km.Sidebar.PaneRight = key.NewBinding(key.WithKeys("]"), key.WithHelp("]", "pane right"))
	km.Sidebar.ColumnLeft = key.NewBinding(key.WithKeys(",", "H", "shift+h"), key.WithHelp(",", "move column left"))
	km.Sidebar.ColumnRight = key.NewBinding(key.WithKeys(".", "L", "shift+l"), key.WithHelp(".", "move column right"))
	km.Sidebar.Narrow = key.NewBinding(key.WithKeys("-"), key.WithHelp("-", "narrower"))
	km.Sidebar.Widen = key.NewBinding(key.WithKeys("=", "+"), key.WithHelp("=", "wider"))
	km.Sidebar.Reopen = key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "reopen pane"))
	km.Sidebar.Select = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open"))
	km.Sidebar.Back = key.NewBinding(key.WithKeys("esc", "alt+esc"), key.WithHelp("esc", "back to editor"))
	km.Sidebar.Move = key.NewBinding(key.WithKeys("h", "j", "k", "l"), key.WithHelp("hjkl", "move"))
	km.Sidebar.MovePane = key.NewBinding(key.WithKeys("[", "]"), key.WithHelp("[/]", "move pane"))
	km.Sidebar.MoveColumn = key.NewBinding(key.WithKeys(",", "."), key.WithHelp(",/.", "move column"))
	km.Sidebar.Resize = key.NewBinding(key.WithKeys("-", "="), key.WithHelp("-/=", "column width"))
	km.SidebarColumnLeft = key.NewBinding(key.WithKeys("alt+h"), key.WithHelp("alt+h/l", "sidebar column"))
	km.SidebarColumnRight = key.NewBinding(key.WithKeys("alt+l"), key.WithHelp("alt+h/l", "sidebar column"))

	km.Initialize.Yes = key.NewBinding(
		key.WithKeys("y", "Y"),
		key.WithHelp("y", "yes"),
	)
	km.Initialize.No = key.NewBinding(
		key.WithKeys("n", "N", "esc", "alt+esc"),
		key.WithHelp("n", "no"),
	)
	km.Initialize.Switch = key.NewBinding(
		key.WithKeys("left", "right", "tab"),
		key.WithHelp("tab", "switch"),
	)
	km.Initialize.Enter = key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "select"),
	)

	return km
}
