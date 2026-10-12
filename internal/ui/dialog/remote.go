package dialog

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/dev-dami/deck/internal/remote"
	"github.com/dev-dami/deck/internal/remote/auth"
	"github.com/dev-dami/deck/internal/ui/common"
)

// RemoteID is the identifier for the remote control dialog.
const RemoteID = "remote"

// qrRows is the height of a rendered pairing QR plus the text around it; below
// this the QR is left out rather than clipped into something unscannable.
const qrRows = 36

// Remote shows how to reach this Deck from a phone: the address, a pairing QR
// and the paired devices.
type Remote struct {
	com     *common.Common
	link    string
	qr      string
	err     string
	devices []auth.Device
	sel     int

	keyMap struct {
		NewCode, Revoke, UpDown, Close key.Binding
	}
}

var _ Dialog = (*Remote)(nil)

// NewRemote creates the remote control dialog.
func NewRemote(com *common.Common) *Remote {
	r := &Remote{com: com}
	r.keyMap.NewCode = key.NewBinding(key.WithKeys("n", "enter"), key.WithHelp("n", "new pairing code"))
	r.keyMap.Revoke = key.NewBinding(key.WithKeys("x", "delete"), key.WithHelp("x", "revoke device"))
	r.keyMap.UpDown = key.NewBinding(key.WithKeys("up", "down", "k", "j"), key.WithHelp("↑/↓", "choose device"))
	r.keyMap.Close = CloseKey
	r.refresh(true)
	return r
}

// ID implements [Dialog].
func (*Remote) ID() string { return RemoteID }

func (r *Remote) refresh(newCode bool) {
	srv := remote.Active()
	if srv == nil {
		return
	}
	r.err = ""
	if newCode {
		link, qr, err := srv.Pairing()
		if err != nil {
			r.err = err.Error()
		}
		r.link, r.qr = link, qr
	}
	devs, err := srv.Devices()
	if err != nil {
		r.err = err.Error()
	}
	r.devices = devs
	r.sel = min(r.sel, max(len(devs)-1, 0))
}

// HandleMsg implements [Dialog].
func (r *Remote) HandleMsg(msg tea.Msg) Action {
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch {
	case key.Matches(kp, r.keyMap.Close):
		return ActionClose{}
	case key.Matches(kp, r.keyMap.NewCode):
		r.refresh(true)
	case key.Matches(kp, r.keyMap.UpDown):
		switch kp.String() {
		case "up", "k":
			r.sel = max(r.sel-1, 0)
		default:
			r.sel = min(r.sel+1, max(len(r.devices)-1, 0))
		}
	case key.Matches(kp, r.keyMap.Revoke):
		srv := remote.Active()
		if srv != nil && r.sel < len(r.devices) {
			if err := srv.Revoke(r.devices[r.sel].ID); err != nil {
				r.err = err.Error()
				break
			}
			r.refresh(false)
		}
	}
	return nil
}

func seen(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return t.Format("2006-01-02")
}

// Draw implements [Dialog].
func (r *Remote) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := r.com.Styles
	var b strings.Builder
	b.WriteString("Remote control\n\n")

	srv := remote.Active()
	if srv == nil {
		b.WriteString("Not running. Start Deck with:\n\n  deck --remote [--lan] [--tunnel]\n\n")
		b.WriteString("--lan   same Wi-Fi only (plain http)\n--tunnel   from anywhere (Cloudflare; or --tunnel=tailscale)\n")
		DrawCenter(scr, area, t.Dialog.Quit.Frame.Render(b.String()))
		return nil
	}

	b.WriteString("Scan with your phone's camera:\n")
	if area.Dy() >= qrRows && r.qr != "" {
		b.WriteString(r.qr)
	} else {
		b.WriteString("(terminal too small for the QR; open this link on your phone)\n")
	}
	if r.link != "" {
		b.WriteString(r.link + "\n")
	}
	b.WriteString("\nServing on " + srv.Origin())
	if srv.PublicURL == "" && len(srv.LANURLs) == 0 {
		b.WriteString("\nThis machine only: restart with --lan or --tunnel to reach it from a phone.")
	}
	if srv.PublicURL == "" && len(srv.LANURLs) > 0 {
		b.WriteString("\nSame Wi-Fi only, over plain http.")
	}
	b.WriteString("\n\nPaired phones:\n")
	if len(r.devices) == 0 {
		b.WriteString("  none yet\n")
	}
	for i, d := range r.devices {
		mark := "  "
		if i == r.sel {
			mark = "> "
		}
		fmt.Fprintf(&b, "%s%s  (seen %s)\n", mark, d.Name, seen(d.LastSeen))
	}
	if r.err != "" {
		b.WriteString("\n" + r.err + "\n")
	}
	b.WriteString("\nn new code · x revoke · esc close")
	DrawCenter(scr, area, t.Dialog.Quit.Frame.Render(b.String()))
	return nil
}

// ShortHelp implements [help.KeyMap].
func (r *Remote) ShortHelp() []key.Binding {
	return []key.Binding{r.keyMap.NewCode, r.keyMap.Revoke, r.keyMap.Close}
}

// FullHelp implements [help.KeyMap].
func (r *Remote) FullHelp() [][]key.Binding {
	return [][]key.Binding{{r.keyMap.NewCode, r.keyMap.Revoke, r.keyMap.UpDown, r.keyMap.Close}}
}
