// Package conin reads the Windows console for Bubble Tea, so that a paste
// arrives as one paste.
//
// Bubble Tea v1 reads the Windows console as key records and switches its VT
// input mode off, so the terminal's bracketed paste never reaches the program:
// a pasted block arrives as hundreds of separate key presses, the Enter keys in
// it among them. Here the console is put in VT input mode instead and its
// bytes are handed to Bubble Tea's ANSI reader, which knows bracketed paste,
// mouse and focus reports. Where the terminal does not bracket a paste, a block
// of text with a line break in it that arrives in one go is bracketed here:
// typing never delivers a line break and other characters at the same time.
//
// On other systems [New] reports [ErrUnsupported] and Bubble Tea reads the
// terminal itself.
package conin

import (
	"bytes"
	"errors"
	"os"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

// ErrUnsupported means the console is read the usual way.
var ErrUnsupported = errors.New("conin: not a Windows console")

// Run runs a Bubble Tea program, reading a Windows console through a [Reader]
// when it can. DECK_INPUT=legacy leaves the reading to Bubble Tea.
func Run(m tea.Model, opts ...tea.ProgramOption) (tea.Model, error) {
	if os.Getenv("DECK_INPUT") == "legacy" {
		return tea.NewProgram(m, opts...).Run()
	}
	r, err := New()
	if err != nil {
		return tea.NewProgram(m, opts...).Run()
	}
	defer r.Close()
	// Ending the input on quit lets Bubble Tea's reader stop at once.
	quit := tea.WithFilter(func(_ tea.Model, msg tea.Msg) tea.Msg {
		if _, ok := msg.(tea.QuitMsg); ok {
			go r.Close()
		}
		return msg
	})
	p := tea.NewProgram(m, append(opts, tea.WithInput(r.Input()), quit)...)
	r.Attach(p)
	return p.Run()
}

const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// frame decides how a block of input that arrived in one go is passed on: as
// is, or bracketed as a paste.
func frame(b []byte) []byte {
	if !looksPasted(b) {
		return b
	}
	out := make([]byte, 0, len(b)+len(pasteStart)+len(pasteEnd))
	out = append(out, pasteStart...)
	out = append(out, b...)
	return append(out, pasteEnd...)
}

// looksPasted reports whether b is text that holds a line break and something
// else, with no escape sequence in it (keys, mouse, an already bracketed
// paste).
func looksPasted(b []byte) bool {
	if bytes.IndexByte(b, 0x1b) >= 0 || !utf8.Valid(b) {
		return false
	}
	brk, other := false, false
	for _, c := range b {
		switch {
		case c == '\r' || c == '\n':
			brk = true
		case c >= 0x20 && c != 0x7f, c == '\t':
			other = true
		}
	}
	return brk && other
}
