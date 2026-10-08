package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/clipimg"
)

// Paste handling, image attachments and the burst detector.
//
// On Windows a multi-line paste often reaches the program as individual key
// presses, Enter included, because bracketed paste is not delivered. Typing is
// never that fast, so an Enter that follows another key within burstGap is part
// of a paste and becomes a newline; burst mode lasts until burstQuiet passes
// with no key.

const (
	burstGap   = 15 * time.Millisecond
	burstQuiet = 60 * time.Millisecond
	// burstCheck is how long after the last pasted key a lone pasted image path
	// (no Enter after it) is turned into an attachment.
	burstCheck = 80 * time.Millisecond
)

// attachment is an image waiting in the composer to be sent.
type attachment struct {
	Path string
	Name string
	Type string // media type
}

// burstCheckMsg asks whether a burst of pasted keys has ended on an image path.
type burstCheckMsg struct{}

// clipImgMsg is the result of reading an image off the clipboard.
type clipImgMsg struct {
	path string
	err  error
}

func mediaType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	return "image/png"
}

// addAttach attaches the image at path (once).
func (m *model) addAttach(path string) {
	for _, a := range m.attach {
		if a.Path == path {
			return
		}
	}
	m.attach = append(m.attach, attachment{Path: path, Name: filepath.Base(path), Type: mediaType(path)})
}

func (m *model) removeAttach(i int) {
	if i < 0 || i >= len(m.attach) {
		return
	}
	m.attach = append(append([]attachment(nil), m.attach[:i]...), m.attach[i+1:]...)
}

// takeAttach empties the attachments and returns them as agent images.
func (m *model) takeAttach() []agent.Image {
	var out []agent.Image
	for _, a := range m.attach {
		out = append(out, agent.Image{Path: a.Path, MediaType: a.Type})
	}
	m.attach = nil
	return out
}

// pasted handles text that arrived as one paste: an image path is attached,
// anything else is inserted.
func (m *model) pasted(text string) {
	if m.edit == nil {
		if p, ok := clipimg.DetectImagePath(text); ok {
			m.addAttach(p)
			return
		}
	}
	m.comp.Insert(text)
}

// burstKey decides whether a key belongs to a paste that arrived as key
// presses. It returns true when it consumed the key (as a newline, or as
// indentation). at is the time the key arrived.
func (m *model) burstKey(msg tea.KeyMsg, at time.Time) (consumed bool, cmds []tea.Cmd) {
	have := !m.lastKeyAt.IsZero()
	gap := at.Sub(m.lastKeyAt)
	if !have || gap >= burstQuiet {
		m.inBurst = false
	}
	fast := have && gap < burstGap
	typed := msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace
	enter := msg.Type == tea.KeyEnter && !msg.Alt
	tab := msg.Type == tea.KeyTab && !msg.Alt
	switch {
	case enter || tab:
		if m.inBurst || (fast && (m.prevTyped || m.prevPasteKey)) {
			m.inBurst = true
			m.prevTyped, m.prevPasteKey = false, true
			if enter {
				if !m.attachPastedPath() {
					m.comp.Insert("\n")
				}
			} else {
				m.comp.Insert("    ")
			}
			return true, nil
		}
	case typed && fast:
		m.burstText = true
		if !m.burstArmed && !m.noBurstTick {
			m.burstArmed = true
			cmds = append(cmds, tea.Tick(burstCheck, func(time.Time) tea.Msg { return burstCheckMsg{} }))
		}
	}
	m.prevTyped, m.prevPasteKey = typed, false
	return false, cmds
}

// attachPastedPath turns a composer that holds nothing but a pasted image path
// into an attachment.
func (m *model) attachPastedPath() bool {
	if m.edit != nil || !m.burstText {
		return false
	}
	if p, ok := clipimg.DetectImagePath(m.comp.Value()); ok {
		m.addAttach(p)
		m.comp.Reset()
		m.burstText = false
		return true
	}
	return false
}

// onBurstCheck fires after pasted keys: once they stop, a lone image path in
// the composer becomes an attachment.
func (m *model) onBurstCheck() []tea.Cmd {
	m.burstArmed = false
	if m.keyNow().Sub(m.lastKeyAt) < burstQuiet { // still arriving
		if m.noBurstTick {
			return nil
		}
		m.burstArmed = true
		return []tea.Cmd{tea.Tick(burstCheck, func(time.Time) tea.Msg { return burstCheckMsg{} })}
	}
	m.attachPastedPath()
	m.burstText = false
	return nil
}

// clipCmd reads an image off the clipboard without blocking the UI.
func (m *model) clipCmd() tea.Cmd {
	save := m.clipSave
	return func() tea.Msg {
		p, err := save(context.Background(), "")
		return clipImgMsg{path: p, err: err}
	}
}

func (m *model) onClipImg(msg clipImgMsg) []tea.Cmd {
	switch {
	case msg.err == nil:
		m.addAttach(msg.path)
	case errors.Is(msg.err, clipimg.ErrNoImage):
		// the terminal pastes text itself
	case errors.Is(msg.err, clipimg.ErrUnavailable):
		if !m.clipWarned {
			m.clipWarned = true
			return []tea.Cmd{m.toast.show("no clipboard image tool found; paste an image file path instead", true, m.now)}
		}
	default:
		return []tea.Cmd{m.toast.show("clipboard: "+msg.err.Error(), true, m.now)}
	}
	return nil
}

// ---- the attachment line ----

type attHit struct{ x0, x1, idx int }

// attachLayout draws the chips (▣ name ×) and the spans a click can hit, in
// cells from the left edge of the main area.
func (m *model) attachLayout(w int) (string, []attHit) {
	line := "  "
	var hits []attHit
	for i, a := range m.attach {
		if i > 0 {
			line += "  "
		}
		x0 := sw(line)
		chip := "▣ " + trunc(a.Name, 24) + " ×"
		if x0+sw(chip) > w-1 {
			if rest := len(m.attach) - i; rest > 0 && x0+4 <= w {
				line += sFaint.Render("+" + itoa(rest))
			}
			break
		}
		line += sMuted.Render(chip)
		hits = append(hits, attHit{x0: x0, x1: sw(line), idx: i})
	}
	return trunc(line, w), hits
}

func itoa(n int) string { return strconv.Itoa(n) }

func (m *model) attachMouse(x int) {
	_, hits := m.attachLayout(m.g.mainW)
	for _, h := range hits {
		if x >= h.x0 && x < h.x1 {
			m.removeAttach(h.idx)
			return
		}
	}
}
