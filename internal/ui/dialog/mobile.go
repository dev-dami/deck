package dialog

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/dev-dami/deck/internal/clipboard"
	"github.com/dev-dami/deck/internal/ui/common"
)

const MobileID = "mobile"
const MobilePairID = "mobile:pair"
const MobileStartID = "mobile:start"
const MobileLANID = "mobile:lan"
const MobileStopID = "mobile:stop"

// MobileStatus is mobile connect's response. PairURL contains a short-lived
// secret and is retained only by this dialog, never sent to notices or history.
type MobileStatus struct {
	Name       string         `json:"name"`
	RelayURL   string         `json:"relay_url"`
	Online     bool           `json:"online"`
	Devices    int            `json:"devices"`
	DeviceList []MobileDevice `json:"device_list"`
	Projects   int            `json:"projects"`
	PairURL    string         `json:"pair_url"`
	ExpiresAt  time.Time      `json:"expires_at"`
	Message    string         `json:"message"`
}

type MobileDevice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type MobileResultMsg struct {
	Owner   *Mobile
	Request uint64
	Status  MobileStatus
	QR      string
	Err     error
}

func (MobileResultMsg) TargetDialog() string { return MobileID }

type mobileTickMsg struct {
	owner   *Mobile
	request uint64
}

func (mobileTickMsg) TargetDialog() string { return MobileID }

type mobileCopyMsg struct {
	owner   *Mobile
	request uint64
	err     error
}

func (mobileCopyMsg) TargetDialog() string { return MobileID }

// MobileRunner constructs commands; process execution belongs outside the dialog.
type MobileRunner func(owner *Mobile, request uint64, action string, lan bool) tea.Cmd

type Mobile struct {
	com               *common.Common
	run               MobileRunner
	project           string
	request           uint64
	busy              bool
	status            MobileStatus
	qr                string
	info              string
	choosingPhone     bool
	phoneIndex        int
	confirmDisconnect bool
}

func NewMobile(com *common.Common, project string, run MobileRunner) *Mobile {
	return &Mobile{com: com, project: project, run: run, info: "Start shares this project and opens a pairing QR."}
}
func (*Mobile) ID() string { return MobileID }
func (m *Mobile) Start(action string, lan bool) tea.Cmd {
	if m.busy {
		return nil
	}
	m.request++
	m.busy = true
	m.choosingPhone = false
	m.confirmDisconnect = false
	m.qr = ""
	m.status.PairURL = ""
	m.info = "Working…"
	return m.run(m, m.request, action, lan)
}
func (m *Mobile) tick() tea.Cmd {
	owner, request := m, m.request
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return mobileTickMsg{owner, request} })
}
func (m *Mobile) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case MobileResultMsg:
		if msg.Owner != m || msg.Request != m.request {
			return nil
		}
		m.busy = false
		if msg.Err != nil {
			m.info = msg.Err.Error()
			return nil
		}
		m.status = msg.Status
		m.qr = msg.QR
		m.info = "Ready. Open Deck on your phone and scan the QR."
		if m.status.PairURL == "" {
			m.info = "Status refreshed."
		}
		if m.status.Message != "" {
			m.info = m.status.Message
		}
		if m.status.PairURL != "" {
			return ActionCmd{Cmd: m.tick()}
		}
	case mobileTickMsg:
		if msg.owner != m || msg.request != m.request {
			return nil
		}
		if !time.Now().Before(m.status.ExpiresAt) {
			m.status.PairURL = ""
			m.qr = ""
			m.info = "Pairing expired. Press p for a new QR."
			return nil
		}
		return ActionCmd{Cmd: m.tick()}
	case mobileCopyMsg:
		if msg.owner == m && msg.request == m.request {
			m.info = "Pairing link copied. Keep it private."
			if msg.err != nil {
				m.info = "Clipboard unavailable. Use a larger terminal to scan the QR."
			}
		}
	case tea.KeyPressMsg:
		if m.choosingPhone {
			switch msg.String() {
			case "esc":
				m.choosingPhone, m.confirmDisconnect = false, false
			case "up", "down":
				count := len(m.status.DeviceList)
				if count > 0 {
					step := 1
					if msg.String() == "up" {
						step = -1
					}
					m.phoneIndex = (m.phoneIndex + step + count) % count
					m.confirmDisconnect = false
				}
			case "enter":
				if m.phoneIndex < len(m.status.DeviceList) {
					if !m.confirmDisconnect {
						m.confirmDisconnect = true
						return nil
					}
					return ActionCmd{Cmd: m.Start("revoke:"+m.status.DeviceList[m.phoneIndex].ID, false)}
				}
			}
			return nil
		}
		switch msg.String() {
		case "esc":
			return ActionClose{}
		case "s", "enter":
			return ActionCmd{Cmd: m.Start("start", false)}
		case "l":
			return ActionCmd{Cmd: m.Start("start", true)}
		case "p":
			return ActionCmd{Cmd: m.Start("link", false)}
		case "d":
			if m.busy {
				return nil
			}
			if len(m.status.DeviceList) == 0 {
				m.info = "No linked phones. Press p to link one."
				return nil
			}
			m.choosingPhone, m.confirmDisconnect, m.phoneIndex = true, false, 0
			return nil
		case "r":
			return ActionCmd{Cmd: m.Start("status", false)}
		case "x":
			return ActionCmd{Cmd: m.Start("stop", false)}
		case "c":
			if m.status.PairURL != "" && time.Now().Before(m.status.ExpiresAt) {
				url, owner, request := m.status.PairURL, m, m.request
				return ActionCmd{Cmd: func() tea.Msg { return mobileCopyMsg{owner, request, clipboard.WriteText(url)} }}
			}
		}
	}
	return nil
}

func (m *Mobile) body(width, height int) string {
	lines := []string{"p New QR / link phone   d Disconnect phone", "r Refresh status   x Stop remote access", "c Copy pairing link   esc Close", "Project: " + m.project}
	if m.status.RelayURL != "" {
		state := "offline"
		if m.status.Online {
			state = "online"
		}
		lines = append(lines, fmt.Sprintf("%s · %s · %d phones · %d projects", m.status.Name, state, m.status.Devices, m.status.Projects), m.status.RelayURL)
	}
	lines = append(lines, m.info)
	if m.choosingPhone {
		lines = append(lines, "Choose phone: up/down, enter to select, esc to cancel")
		visible := max(1, height-len(lines)-1)
		first := max(0, m.phoneIndex-visible+1)
		last := min(len(m.status.DeviceList), first+visible)
		for i := first; i < last; i++ {
			phone := m.status.DeviceList[i]
			prefix := "  "
			if i == m.phoneIndex {
				prefix = "> "
			}
			lines = append(lines, prefix+phone.Name)
		}
		if m.confirmDisconnect {
			lines = append(lines, "Disconnect selected phone? Enter confirms; esc cancels.")
		}
	} else if m.qr != "" && time.Now().Before(m.status.ExpiresAt) {
		if lipgloss.Width(m.qr) <= width && lipgloss.Height(m.qr)+len(lines) <= height {
			lines = append(lines, m.qr)
		} else {
			lines = append(lines, "Press c to copy; enlarge for QR.")
		}
	}
	// Control text and process errors must never inject terminal escapes.
	for i, line := range lines {
		if line != m.qr {
			plain := strings.Map(func(r rune) rune {
				if unicode.IsControl(r) {
					return ' '
				}
				return r
			}, ansi.Strip(line))
			lines[i] = ansi.Truncate(plain, max(0, width), "…")
		}
	}
	if len(lines) > height {
		lines = lines[:max(0, height)]
	}
	return strings.Join(lines, "\n")
}
func (m *Mobile) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := m.com.Styles
	width := max(0, min(100, area.Dx()))
	inner := max(0, width-t.Dialog.View.GetHorizontalFrameSize())
	height := max(0, area.Dy()-t.Dialog.View.GetVerticalFrameSize()-t.Dialog.Title.GetVerticalFrameSize()-1)
	rc := NewRenderContext(t, width)
	rc.Title = "Mobile remote control"
	rc.AddPart(m.body(inner, height))
	DrawCenter(scr, area, rc.Render())
	return nil
}
