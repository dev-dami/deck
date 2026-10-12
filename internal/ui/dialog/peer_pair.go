package dialog

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	deckconfig "github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/util"
	"github.com/dev-dami/deck/internal/workspace"
)

// The "Add a teammate" wizard of the Team settings page: pairing with
// another Deck without a terminal. Step 1 picks a side; step 2 either waits
// with a one-time code (`deck peer pair`) or proves the other Deck's code
// (`deck peer add`); step 3 shows both fingerprints and pins the other Deck
// only once the person says they match. No or Esc leaves nothing pinned.

const (
	// PeerPairID is the identifier of the Add a teammate wizard.
	PeerPairID = "settings:team-pair"

	peerPairMaxWidth = 70
	// pairDialTimeout bounds proving a code to the other Deck.
	pairDialTimeout = 40 * time.Second
	pairLabelWidth  = 18
)

// pairStep is where the wizard is.
type pairStep int

const (
	pairChoose  pairStep = iota // step 1: which side
	pairWaiting                 // step 2, wait mode: the code is shown
	pairForm                    // step 2, connect mode: name, address, code
	pairDialing                 // step 2, connect mode: proving the code
	pairConfirm                 // step 3: compare the fingerprints
	pairSaving                  // step 3: pinning
	pairDone
	pairFailed
)

// pairMode is the side this Deck takes.
type pairMode int

const (
	pairModeWait    pairMode = iota // this Deck waits for a code
	pairModeConnect                 // this Deck connects to one that waits
)

// The connect form's fields.
const (
	fieldName = iota
	fieldAddr
	fieldCode
	fieldDial
	fieldCount
)

// The wizard's messages, each tagged with the request it answers so an
// abandoned one is ignored.
type (
	pairStartedMsg struct {
		req  int
		wait *peer.PairWait
		err  error
	}
	pairOutcomeMsg struct {
		req int
		out peer.PairOutcome
	}
	pairDialMsg struct {
		req int
		res peer.PairResult
		err error
	}
	pairPinnedMsg struct {
		req  int
		name string
		err  error
	}
	pairTickMsg struct{ req int }
)

func (pairStartedMsg) TargetDialog() string { return PeerPairID }
func (pairOutcomeMsg) TargetDialog() string { return PeerPairID }
func (pairDialMsg) TargetDialog() string    { return PeerPairID }
func (pairPinnedMsg) TargetDialog() string  { return PeerPairID }
func (pairTickMsg) TargetDialog() string    { return PeerPairID }

// PeerPair is the Add a teammate wizard.
type PeerPair struct {
	com  *common.Common
	link workspace.PeerLink
	help help.Model

	step pairStep
	mode pairMode
	req  int // the request in flight; bumped to abandon one

	wait   *peer.PairWait     // wait mode: the listener
	cancel context.CancelFunc // connect mode: the dial in flight
	res    peer.PairResult    // what the other Deck proved

	inputs [fieldDial]textinput.Model // connect mode: name, address, code
	focus  int
	dial   bool
	name   textinput.Model // wait mode: the name to pin under

	err    string
	taken  string // a name paired with another key: Enter again replaces it
	paired string // the name pinned
	now    func() time.Time

	keyMap struct {
		Up, Down, Next, Prev, Select, Toggle key.Binding
		Copy, Yes, No, Back, Close           key.Binding
	}
}

var (
	_ Dialog      = (*PeerPair)(nil)
	_ help.KeyMap = (*PeerPair)(nil)
	_ TargetedMsg = pairTickMsg{}
	_ TargetedMsg = teamTickMsg{}
)

// NewPeerPair opens the wizard on its first step.
func NewPeerPair(com *common.Common) *PeerPair {
	p := &PeerPair{com: com, dial: true}
	if ws, ok := com.Workspace.(workspace.Peers); ok {
		p.link = ws.PeerLink()
	}
	p.help = help.New()
	p.help.Styles = com.Styles.DialogHelpStyles()
	placeholders := [fieldDial]string{"vps", "vps.example.com:7722", "ABCDE-23456"}
	for i := range p.inputs {
		p.inputs[i] = p.newInput(placeholders[i])
	}
	p.name = p.newInput("their name here")

	p.keyMap.Up = key.NewBinding(key.WithKeys("up", "ctrl+p"), key.WithHelp("↑", "previous"))
	p.keyMap.Down = key.NewBinding(key.WithKeys("down", "ctrl+n"), key.WithHelp("↓", "next"))
	p.keyMap.Next = key.NewBinding(key.WithKeys("tab", "down"), key.WithHelp("tab", "next field"))
	p.keyMap.Prev = key.NewBinding(key.WithKeys("shift+tab", "up"), key.WithHelp("shift+tab", "previous field"))
	p.keyMap.Select = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "continue"))
	p.keyMap.Toggle = key.NewBinding(key.WithKeys(" ", "space"), key.WithHelp("space", "toggle"))
	p.keyMap.Copy = key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "copy code"))
	p.keyMap.Yes = key.NewBinding(key.WithKeys("enter", "y"), key.WithHelp("enter", "yes, they match"))
	p.keyMap.No = key.NewBinding(key.WithKeys("esc", "alt+esc", "n"), key.WithHelp("esc", "no"))
	p.keyMap.Back = key.NewBinding(key.WithKeys("esc", "alt+esc"), key.WithHelp("esc", "back"))
	p.keyMap.Close = CloseKey
	return p
}

func (p *PeerPair) newInput(placeholder string) textinput.Model {
	in := textinput.New()
	in.SetStyles(p.com.Styles.TextInput)
	in.SetVirtualCursor(true) // fields sit mid-dialog: they draw their own cursor
	in.Placeholder = placeholder
	in.CharLimit = 128
	return in
}

// ID implements [Dialog].
func (*PeerPair) ID() string { return PeerPairID }

func (p *PeerPair) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

// abort abandons whatever is in flight: the listener closes, a dial stops,
// and late answers are ignored. Nothing is pinned before the person says
// the fingerprints match, so nothing needs unpinning.
func (p *PeerPair) abort() {
	p.req++
	if p.wait != nil {
		p.wait.Cancel()
		p.wait = nil
	}
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
}

// fail shows why pairing stopped.
func (p *PeerPair) fail(msg string) {
	p.abort()
	p.step, p.err = pairFailed, msg
}

// HandleMsg implements [Dialog].
func (p *PeerPair) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case pairStartedMsg:
		if msg.req != p.req {
			msg.wait.Cancel()
			return nil
		}
		if msg.err != nil {
			p.fail("Could not wait for a code: " + friendlyErr(msg.err))
			return nil
		}
		p.wait = msg.wait
		return ActionCmd{Cmd: tea.Batch(p.awaitOutcome(), p.tick())}
	case pairOutcomeMsg:
		if msg.req != p.req {
			return nil
		}
		p.wait = nil
		if msg.out.Err != nil {
			p.fail(pairErrText(msg.out.Err))
			return nil
		}
		p.res, p.step, p.err, p.taken = msg.out.Result, pairConfirm, "", ""
		name := peer.Sanitize(msg.out.Result.PeerName)
		if !deckconfig.ValidPeerName(name) {
			name = ""
		}
		p.name.SetValue(name)
		p.name.CursorEnd()
		_ = p.name.Focus()
	case pairDialMsg:
		if msg.req != p.req {
			return nil
		}
		p.cancel = nil
		if msg.err != nil {
			p.step, p.err = pairForm, pairErrText(msg.err)
			return nil
		}
		p.res, p.step, p.err, p.taken = msg.res, pairConfirm, "", ""
	case pairPinnedMsg:
		if msg.req != p.req {
			return nil
		}
		if msg.err != nil {
			p.step = pairConfirm
			if errors.Is(msg.err, peer.ErrPinTaken) {
				p.taken = msg.name
				p.err = "“" + msg.name + "” is already paired with a different key. Enter again replaces it: only if that Deck really has a new key."
			} else {
				p.err = friendlyErr(msg.err)
			}
			return nil
		}
		p.step, p.paired, p.err = pairDone, msg.name, ""
		return ActionCmd{Cmd: util.ReportInfo("Paired with " + msg.name)}
	case pairTickMsg:
		if msg.req == p.req && p.step == pairWaiting {
			return ActionCmd{Cmd: p.tick()}
		}
	case tea.KeyPressMsg:
		return p.handleKey(msg)
	}
	return nil
}

// tick redraws the expiry countdown in a second.
func (p *PeerPair) tick() tea.Cmd {
	req := p.req
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return pairTickMsg{req: req} })
}

// startWaiting opens the pairing listener, off the UI loop.
func (p *PeerPair) startWaiting() tea.Cmd {
	p.abort()
	p.mode, p.step, p.err = pairModeWait, pairWaiting, ""
	link, req := p.link, p.req
	if link == nil {
		p.fail(teamUnavailable)
		return nil
	}
	return func() tea.Msg {
		w, err := link.StartPairing("")
		return pairStartedMsg{req: req, wait: w, err: err}
	}
}

// awaitOutcome waits for the other Deck, off the UI loop.
func (p *PeerPair) awaitOutcome() tea.Cmd {
	w, req := p.wait, p.req
	return func() tea.Msg {
		out, ok := <-w.Done
		if !ok {
			out.Err = context.Canceled
		}
		return pairOutcomeMsg{req: req, out: out}
	}
}

// submitForm proves the typed code to the other Deck, off the UI loop.
func (p *PeerPair) submitForm() Action {
	name := strings.TrimSpace(p.inputs[fieldName].Value())
	addr := strings.TrimSpace(p.inputs[fieldAddr].Value())
	code := strings.TrimSpace(p.inputs[fieldCode].Value())
	switch {
	case !deckconfig.ValidPeerName(name):
		p.err = "Give it a name here: 1-32 of a-z, 0-9, '-', '_', '.'"
		p.setFocus(fieldName)
		return nil
	case addr == "":
		p.err = "Type the other Deck's address, like vps.example.com:7722."
		p.setFocus(fieldAddr)
		return nil
	case code == "":
		p.err = "Type the code the other Deck shows."
		p.setFocus(fieldCode)
		return nil
	}
	if p.link == nil {
		p.fail(teamUnavailable)
		return nil
	}
	p.abort()
	p.step, p.err, p.taken = pairDialing, "", ""
	ctx, cancel := context.WithTimeout(context.Background(), pairDialTimeout)
	p.cancel = cancel
	link, req := p.link, p.req
	return ActionCmd{Cmd: func() tea.Msg {
		defer cancel()
		res, err := link.PairDial(ctx, addr, code)
		return pairDialMsg{req: req, res: res, err: err}
	}}
}

// pinName is the name the other Deck gets pinned under.
func (p *PeerPair) pinName() string {
	if p.mode == pairModeWait {
		return strings.TrimSpace(p.name.Value())
	}
	return strings.TrimSpace(p.inputs[fieldName].Value())
}

// confirm pins the other Deck, off the UI loop: the person said the
// fingerprints match. A second Enter on a name taken by another key
// replaces that key.
func (p *PeerPair) confirm() Action {
	name := p.pinName()
	if !deckconfig.ValidPeerName(name) {
		p.err = "Give it a name here: 1-32 of a-z, 0-9, '-', '_', '.'"
		return nil
	}
	r := peer.PinRequest{Name: name, Replace: p.taken == name, Result: p.res}
	if p.mode == pairModeConnect {
		r.Addr = peer.WithDefaultPort(strings.TrimSpace(p.inputs[fieldAddr].Value()))
		r.Dial = p.dial
	}
	p.step, p.err = pairSaving, ""
	link, req := p.link, p.req
	return ActionCmd{Cmd: func() tea.Msg {
		return pairPinnedMsg{req: req, name: name, err: link.Pin(r)}
	}}
}

func (p *PeerPair) setFocus(i int) {
	p.focus = (i%fieldCount + fieldCount) % fieldCount
	for j := range p.inputs {
		if j == p.focus {
			_ = p.inputs[j].Focus()
		} else {
			p.inputs[j].Blur()
		}
	}
}

func (p *PeerPair) handleKey(msg tea.KeyPressMsg) Action {
	switch p.step {
	case pairChoose:
		switch {
		case key.Matches(msg, p.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, p.keyMap.Up, p.keyMap.Down):
			p.mode = 1 - p.mode
		case key.Matches(msg, p.keyMap.Select):
			if p.mode == pairModeWait {
				return ActionCmd{Cmd: p.startWaiting()}
			}
			p.step, p.err = pairForm, ""
			p.setFocus(fieldName)
		}
	case pairWaiting:
		switch {
		case key.Matches(msg, p.keyMap.Close):
			p.abort()
			return ActionClose{}
		case key.Matches(msg, p.keyMap.Copy):
			if p.wait != nil {
				return ActionCmd{Cmd: common.CopyToClipboard(p.wait.Code, "Pairing code copied")}
			}
		}
	case pairForm:
		switch {
		case key.Matches(msg, p.keyMap.Back):
			p.step, p.err = pairChoose, ""
		case key.Matches(msg, p.keyMap.Next):
			p.setFocus(p.focus + 1)
		case key.Matches(msg, p.keyMap.Prev):
			p.setFocus(p.focus - 1)
		case key.Matches(msg, p.keyMap.Select):
			return p.submitForm()
		case p.focus == fieldDial:
			if key.Matches(msg, p.keyMap.Toggle) {
				p.dial = !p.dial
			}
		default:
			var cmd tea.Cmd
			p.inputs[p.focus], cmd = p.inputs[p.focus].Update(msg)
			p.err = ""
			return ActionCmd{Cmd: cmd}
		}
	case pairDialing:
		if key.Matches(msg, p.keyMap.Back) {
			p.abort()
			p.step = pairForm
		}
	case pairConfirm:
		// In wait mode the name field takes letters, so only Enter and Esc
		// answer; in connect mode y and n do too.
		yes, no := key.Matches(msg, p.keyMap.Select), key.Matches(msg, p.keyMap.Back)
		if p.mode == pairModeConnect {
			yes, no = key.Matches(msg, p.keyMap.Yes), key.Matches(msg, p.keyMap.No)
		}
		switch {
		case yes:
			return p.confirm()
		case no:
			p.fail("Not paired: nothing was saved. If the fingerprints differ, someone else may have answered: start again and compare once more.")
		case p.mode == pairModeWait:
			var cmd tea.Cmd
			p.name, cmd = p.name.Update(msg)
			p.taken, p.err = "", ""
			return ActionCmd{Cmd: cmd}
		}
	case pairDone:
		if key.Matches(msg, p.keyMap.Select, p.keyMap.Close) {
			return ActionClose{}
		}
	case pairFailed:
		switch {
		case key.Matches(msg, p.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, p.keyMap.Select):
			p.step, p.err = pairChoose, ""
		}
	}
	return nil
}

// pairErrText says why pairing failed, in words a person can act on.
func pairErrText(err error) string {
	var dnsErr *net.DNSError
	var opErr *net.OpError
	switch {
	case errors.Is(err, peer.ErrPairWrong):
		return "Wrong code. Check it on the other Deck's screen and try again (3 tries per code)."
	case errors.Is(err, peer.ErrPairAttempts):
		return "Too many wrong codes: this code no longer works. Start again for a new one."
	case errors.Is(err, peer.ErrPairExpired):
		return "The code expired before the other Deck used it. Start again for a new one."
	case errors.Is(err, peer.ErrPairUnproven):
		return "The other Deck did not accept the code: it may have expired or been mistyped. Ask for a fresh one."
	case errors.Is(err, context.DeadlineExceeded):
		return "No answer in time. Is the other Deck still waiting for a code?"
	case errors.As(err, &dnsErr):
		return "No such host. Check the address."
	case errors.As(err, &opErr):
		return "Could not reach it. Check the address, and that the other Deck is waiting for a code (codes expire after 5 minutes)."
	}
	return friendlyErr(err)
}

// stepOf is the step number shown in the title; 0 for none.
func (p *PeerPair) stepOf() int {
	switch p.step {
	case pairChoose:
		return 1
	case pairWaiting, pairForm, pairDialing:
		return 2
	case pairConfirm, pairSaving:
		return 3
	}
	return 0
}

// Draw implements [Dialog].
func (p *PeerPair) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := p.com.Styles
	width := max(0, min(peerPairMaxWidth, area.Dx()-t.Dialog.View.GetHorizontalBorderSize()))
	innerWidth := max(0, width-t.Dialog.View.GetHorizontalFrameSize())

	rc := NewRenderContext(t, width)
	rc.Title = "Add a teammate"
	if n := p.stepOf(); n > 0 {
		rc.TitleInfo = t.Dialog.ListItem.InfoBlurred.Render(fmt.Sprintf(" step %d of 3", n))
	}
	rc.AddPart(strings.Join(p.lines(innerWidth), "\n"))
	rc.Help = renderDialogHelp(t, &p.help, p, innerWidth)
	DrawCenter(scr, area, rc.Render())
	return nil
}

// lines renders the current step.
func (p *PeerPair) lines(width int) []string {
	t := p.com.Styles
	text := max(0, width-2)
	muted := t.Dialog.SecondaryText
	line := func(str string) string { return muted.Render(ansi.Truncate(str, text, "…")) }
	wrap := func(str string) string { return muted.Width(width).Render(str) }
	strong := func(str string) string {
		return t.Dialog.PrimaryText.Render(ansi.Truncate(str, text, "…"))
	}
	field := func(label, value string) string {
		return t.Dialog.NormalItem.UnsetPaddingRight().Render(padRight(label, pairLabelWidth)) +
			t.Dialog.ListItem.InfoFocused.Render(ansi.Truncate(value, max(0, text-pairLabelWidth), "…"))
	}
	errLine := func() []string {
		if p.err == "" {
			return nil
		}
		return []string{"", t.Dialog.TitleError.PaddingLeft(1).Width(width).Render(p.err)}
	}

	switch p.step {
	case pairChoose:
		out := []string{strong("How do you want to pair?"), ""}
		opts := []struct{ title, desc string }{
			{"This Deck waits for a code", "Show a one-time code here; type it on the other Deck."},
			{"Connect to another Deck", "The other Deck shows a code; type it and its address here."},
		}
		for i, o := range opts {
			bullet := t.Radio.Off
			if pairMode(i) == p.mode {
				bullet = t.Radio.On
			}
			out = append(out, " "+bullet.Render()+t.Radio.Label.Padding(0, 1).Render(o.title), line("    "+o.desc))
		}
		return out

	case pairWaiting:
		if p.wait == nil {
			return []string{line("Opening a pairing listener…")}
		}
		w := p.wait
		left := max(0, w.Expires.Sub(p.clock()).Round(time.Second))
		big := t.Dialog.PrimaryText.Bold(true).
			Border(lipgloss.RoundedBorder()).BorderForeground(t.Dialog.PrimaryText.GetForeground()).
			Padding(0, 2).Render(spaced(w.Code))
		out := []string{strong("Type this code on the other Deck:"), ""}
		for _, l := range strings.Split(lipgloss.PlaceHorizontal(width, lipgloss.Center, big), "\n") {
			out = append(out, l)
		}
		return append(out,
			line("    expires in "+clockSpan(left)+" · single use · c copies it"),
			"",
			field("Listening on", w.Addr+" ("+reach(w.Addr)+")"),
			field("This Deck", w.Name),
			field("Fingerprint", w.Fingerprint),
			"",
			wrap("On the other Deck: Settings › Team › Add a teammate › Connect to another Deck, with this machine's address and port "+portOf(w.Addr)+". Esc stops waiting."),
		)

	case pairForm, pairDialing:
		labels := [fieldDial]string{"Its name here", "Address", "Pairing code"}
		out := []string{strong("Type what the other Deck shows:"), ""}
		for i := range p.inputs {
			in := p.inputs[i]
			in.SetWidth(max(1, text-pairLabelWidth-ansi.StringWidth(in.Prompt)-1))
			label := t.Dialog.NormalItem.UnsetPaddingRight().Render(padRight(labels[i], pairLabelWidth))
			if i == p.focus && p.step == pairForm {
				label = t.Dialog.PrimaryText.UnsetPaddingRight().Render(padRight(labels[i], pairLabelWidth))
			}
			out = append(out, label+ansi.Truncate(in.View(), max(0, text-pairLabelWidth), ""))
		}
		toggle := padRight("Also connect to it automatically: "+onOff(p.dial)+"  (space)", text)
		if p.focus == fieldDial && p.step == pairForm {
			out = append(out, t.Dialog.SelectedItem.Render(ansi.Truncate(toggle, text, "…")))
		} else {
			out = append(out, t.Dialog.NormalItem.Render(ansi.Truncate(toggle, text, "…")))
		}
		if p.step == pairDialing {
			out = append(out, "", line("Checking the code with "+strings.TrimSpace(p.inputs[fieldAddr].Value())+"… (esc cancels)"))
		}
		return append(out, errLine()...)

	case pairConfirm, pairSaving:
		r := p.res
		who := ansi.Truncate(peer.Sanitize(r.PeerName), 40, "…")
		out := []string{
			line("Code accepted by " + peer.Sanitize(r.RemoteAddr) + " (it calls itself “" + who + "”)."),
			"",
			field("Their fingerprint", r.PeerFingerprint),
			field("This Deck", r.OwnFingerprint),
			"",
			strong("Do these match on the other screen?"),
		}
		if p.mode == pairModeWait {
			p.name.SetWidth(max(1, text-pairLabelWidth-ansi.StringWidth(p.name.Prompt)-1))
			out = append(out, t.Dialog.NormalItem.UnsetPaddingRight().Render(padRight("Save it as", pairLabelWidth))+
				ansi.Truncate(p.name.View(), max(0, text-pairLabelWidth), ""))
		} else {
			out = append(out, field("Save it as", p.pinName()))
		}
		if p.step == pairSaving {
			out = append(out, "", line("Saving…"))
		}
		return append(out, errLine()...)

	case pairDone:
		out := []string{strong("Paired with " + p.paired + "."), ""}
		if p.mode == pairModeConnect && p.dial {
			out = append(out, wrap("This Deck connects to it while Team link is on."))
		} else {
			out = append(out, wrap("It can connect to this Deck while Team link and Accept connections are on."))
		}
		return out
	}
	// pairFailed
	return []string{t.Dialog.TitleError.PaddingLeft(1).Width(width).Render(p.err), "", line("enter starts again · esc closes")}
}

// spaced writes a code with room between its characters, to read it out.
func spaced(code string) string {
	return strings.Join(strings.Split(code, ""), " ")
}

// clockSpan writes a countdown as m:ss.
func clockSpan(d time.Duration) string {
	s := int(d / time.Second)
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// reach says who can reach a listening address.
func reach(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "?"
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		return "all interfaces"
	} else if host == "localhost" || (ip != nil && ip.IsLoopback()) {
		return "this machine only: the other Deck must run here too"
	}
	return "only " + peer.Sanitize(host)
}

func portOf(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return deckconfig.DefaultPeerPort
	}
	return port
}

// ShortHelp implements [help.KeyMap].
func (p *PeerPair) ShortHelp() []key.Binding {
	select_ := func(h string) key.Binding {
		return key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", h))
	}
	esc := func(h string) key.Binding {
		return key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", h))
	}
	switch p.step {
	case pairChoose:
		return []key.Binding{p.keyMap.Up, select_("continue"), esc("close")}
	case pairWaiting:
		return []key.Binding{p.keyMap.Copy, esc("stop waiting")}
	case pairForm:
		return []key.Binding{p.keyMap.Next, p.keyMap.Toggle, select_("connect"), esc("back")}
	case pairDialing:
		return []key.Binding{esc("cancel")}
	case pairConfirm:
		return []key.Binding{select_("yes, they match"), esc("no")}
	case pairDone:
		return []key.Binding{select_("done")}
	case pairFailed:
		return []key.Binding{select_("start again"), esc("close")}
	}
	return nil
}

// FullHelp implements [help.KeyMap].
func (p *PeerPair) FullHelp() [][]key.Binding { return [][]key.Binding{p.ShortHelp()} }
