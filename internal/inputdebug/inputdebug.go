// Package inputdebug runs a small full-screen Bubble Tea program that shows
// the raw input messages a terminal delivers: keys, pastes, mouse, window
// size and focus. It is for checking how a terminal behaves with Deck.
//
// Nothing is written to disk. The last few messages are kept in memory only.
package inputdebug

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// maxEntries is how many received messages are kept (and can be shown).
const maxEntries = 30

const footerText = "click, scroll, paste multi-line text, press shift+tab — q twice to quit"

// defaultWidth and defaultHeight are used until the first WindowSizeMsg.
const (
	defaultWidth  = 80
	defaultHeight = 24
)

// getenv is replaceable in tests.
var getenv = os.Getenv

// Run starts the input debug program on the alternate screen with all-motion
// mouse reporting and focus reporting enabled. It returns when the user quits.
func Run() error {
	p := tea.NewProgram(
		newModel(time.Now, getenv),
		tea.WithAltScreen(),
		tea.WithMouseAllMotion(),
		tea.WithReportFocus(),
	)
	_, err := p.Run()
	return err
}

// entry is one received message as shown in the log.
type entry struct {
	at    time.Time
	delta time.Duration // since the previous message; zero for the first
	kind  string
	text  string
}

type model struct {
	now    func() time.Time
	header []string

	width, height int

	last     time.Time
	haveLast bool
	entries  []entry

	armed bool // q has been pressed once and not followed by another key
}

func newModel(now func() time.Time, env func(string) string) model {
	return model{
		now:    now,
		header: headerLines(env, runtime.GOOS),
		width:  defaultWidth,
		height: defaultHeight,
	}
}

// Init implements tea.Model.
func (m model) Init() tea.Cmd {
	return nil
}

// Update implements tea.Model. Every message is logged. Two q presses in a
// row quit, and Ctrl+C quits at once so the user is never stuck in the program.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	kind, text := describe(msg)
	m.record(m.now(), kind, text)

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
		if isQ(msg) {
			if m.armed {
				return m, tea.Quit
			}
			m.armed = true
		} else {
			m.armed = false
		}
	}
	return m, nil
}

// View implements tea.Model.
func (m model) View() string {
	width := m.width
	if width <= 0 {
		width = defaultWidth
	}
	height := m.height
	if height <= 0 {
		height = defaultHeight
	}

	var lines []string
	lines = append(lines, m.header...)
	rule := strings.Repeat("─", width)
	if m.armed {
		rule = "── press q again to quit " + strings.Repeat("─", width)
	}
	lines = append(lines, rule)

	footer := footerText
	logRows := height - len(lines) - 1 // one row for the footer
	if logRows < 0 {
		logRows = 0
	}

	if len(m.entries) == 0 {
		if logRows > 0 {
			lines = append(lines, "waiting for input...")
		}
	} else {
		start := len(m.entries) - logRows
		if start < 0 {
			start = 0
		}
		for _, e := range m.entries[start:] {
			lines = append(lines, formatEntry(e))
		}
	}

	// Keep the footer on the last row of the screen.
	for len(lines) < height-1 {
		lines = append(lines, "")
	}
	lines = append(lines, footer)

	for i, l := range lines {
		lines[i] = ansi.Truncate(l, width, "…")
	}
	return strings.Join(lines, "\n")
}

// record adds a message to the log, computing the delta from the previous one.
func (m *model) record(at time.Time, kind, text string) {
	var delta time.Duration
	if m.haveLast {
		delta = at.Sub(m.last)
	}
	m.last = at
	m.haveLast = true
	m.entries = append(m.entries, entry{at: at, delta: delta, kind: kind, text: text})
	if len(m.entries) > maxEntries {
		m.entries = m.entries[len(m.entries)-maxEntries:]
	}
}

// formatEntry renders one log line: wall clock, delta in ms, kind, details.
func formatEntry(e entry) string {
	return fmt.Sprintf("%s %6s  %-6s %s",
		e.at.Format("15:04:05.000"),
		fmt.Sprintf("+%dms", e.delta.Milliseconds()),
		e.kind,
		e.text)
}

// isQ reports whether msg is a typed, unmodified q (not part of a paste).
func isQ(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyRunes && !msg.Alt && !msg.Paste &&
		len(msg.Runes) == 1 && msg.Runes[0] == 'q'
}

// describe returns a short kind label and the details for one message.
func describe(msg tea.Msg) (kind, text string) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return "key", describeKey(msg)
	case tea.MouseMsg:
		return "mouse", describeMouse(msg)
	case tea.WindowSizeMsg:
		return "window", fmt.Sprintf("%dx%d", msg.Width, msg.Height)
	case tea.FocusMsg:
		return "focus", "terminal gained focus"
	case tea.BlurMsg:
		return "blur", "terminal lost focus"
	default:
		return "other", fmt.Sprintf("%T %v", msg, msg)
	}
}

func describeKey(k tea.KeyMsg) string {
	runes := string(k.Runes)
	const maxRunes = 60
	if r := []rune(runes); len(r) > maxRunes {
		runes = string(r[:maxRunes]) + "…"
	}
	// Paste and alt come first so they stay visible when the line is cut.
	return fmt.Sprintf("paste=%t alt=%t type=%s string=%q runes=%s",
		k.Paste, k.Alt, k.Type.String(), k.String(), strconv.Quote(runes))
}

func describeMouse(m tea.MouseMsg) string {
	return fmt.Sprintf("x=%d y=%d action=%s button=%s shift=%t alt=%t ctrl=%t",
		m.X, m.Y, actionName(m.Action), buttonName(m.Button), m.Shift, m.Alt, m.Ctrl)
}

func actionName(a tea.MouseAction) string {
	switch a {
	case tea.MouseActionPress:
		return "press"
	case tea.MouseActionRelease:
		return "release"
	case tea.MouseActionMotion:
		return "motion"
	default:
		return fmt.Sprintf("action(%d)", int(a))
	}
}

func buttonName(b tea.MouseButton) string {
	switch b {
	case tea.MouseButtonNone:
		return "none"
	case tea.MouseButtonLeft:
		return "left"
	case tea.MouseButtonMiddle:
		return "middle"
	case tea.MouseButtonRight:
		return "right"
	case tea.MouseButtonWheelUp:
		return "wheel-up"
	case tea.MouseButtonWheelDown:
		return "wheel-down"
	case tea.MouseButtonWheelLeft:
		return "wheel-left"
	case tea.MouseButtonWheelRight:
		return "wheel-right"
	case tea.MouseButtonBackward:
		return "backward"
	case tea.MouseButtonForward:
		return "forward"
	default:
		return fmt.Sprintf("button(%d)", int(b))
	}
}

// headerLines describes the platform and the terminal, from environment
// variables. Only names and the values of TERM and TERM_PROGRAM are shown.
func headerLines(env func(string) string, goos string) []string {
	val := func(name string) string {
		if v := env(name); v != "" {
			return v
		}
		return "-"
	}
	set := func(name string) string {
		if env(name) != "" {
			return "set"
		}
		return "-"
	}

	termProgram := env("TERM_PROGRAM")
	vscode := env("VSCODE_INJECTION") != "" || termProgram == "vscode"

	var hints []string
	add := func(ok bool, name string) {
		if ok {
			hints = append(hints, name)
		}
	}
	add(vscode, "VS Code terminal")
	add(env("WT_SESSION") != "", "Windows Terminal")
	add(env("ConEmuANSI") != "", "ConEmu")
	add(termProgram == "WezTerm" || env("WEZTERM_EXECUTABLE") != "", "WezTerm")
	add(env("KITTY_WINDOW_ID") != "", "kitty")
	add(env("TMUX") != "", "tmux")
	add(env("SSH_CONNECTION") != "" || env("SSH_TTY") != "", "ssh")
	add(env("CLAUDECODE") != "" || env("CLAUDE_CODE_ENTRYPOINT") != "", "Claude Code")
	hintText := "none"
	if len(hints) > 0 {
		hintText = strings.Join(hints, ", ")
	}

	return []string{
		fmt.Sprintf("os=%s  TERM=%s  TERM_PROGRAM=%s", goos, val("TERM"), val("TERM_PROGRAM")),
		fmt.Sprintf("WT_SESSION=%s  ConEmuANSI=%s  VSCODE_INJECTION=%s  CLAUDECODE=%s  CLAUDE_CODE_ENTRYPOINT=%s  TMUX=%s",
			set("WT_SESSION"), set("ConEmuANSI"), set("VSCODE_INJECTION"),
			set("CLAUDECODE"), set("CLAUDE_CODE_ENTRYPOINT"), set("TMUX")),
		"host hints: " + hintText,
	}
}
