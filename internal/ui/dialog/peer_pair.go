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

	"github.com/dev-dami/deck/internal/clipboard"
	deckconfig "github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/util"
	"github.com/dev-dami/deck/internal/workspace"
)

// The "Add a teammate" wizard of the Team settings page: pairing with
// another Deck without a terminal. Step 1 picks a side; step 2 either waits
// with a one-time code, served on this Deck's own listener when it runs, or
// proves the other Deck's code; step 3 shows both fingerprints and pins the
// other Deck only once the person says they match. No or Esc leaves nothing
// pinned. The waiting side copies an invite (address, code, fingerprint) in
// one key; pasting it on the other side fills the whole form.

const (
	// PeerPairID is the identifier of the Add a teammate wizard.
	PeerPairID = "settings:team-pair"

	peerPairMaxWidth = 72
	pairLabelWidth   = 14
)

// pairStep is where the wizard is.
type pairStep int

const (
	pairChoose  pairStep = iota // step 1: which side
	pairWaiting                 // step 2, wait mode: the code is shown
	pairExpired                 // step 2, wait mode: the code ran out
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

// The wizard's messages. Each answer to a request carries the request it
// answers (or, for a wait's outcome, the wait's channel), so an abandoned
// one is ignored.
type (
	pairStartedMsg struct {
		req  int
		wait *peer.PairWait
		err  error
	}
	pairOutcomeMsg struct {
		done <-chan peer.PairOutcome
		out  peer.PairOutcome
	}
	pairMovedMsg struct {
		req  int
		wait *peer.PairWait
		err  error
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
	pairTickMsg     struct{ req int }
	pairClipReadMsg struct {
		req  int
		text string
		err  error
	}
)

// PairClipboardMsg is the terminal's answer to an OSC 52 clipboard read the
// wizard asked for (v, where no system clipboard can be read); the UI hands
// tea.ClipboardMsg on as this.
type PairClipboardMsg struct{ Content string }

func (pairStartedMsg) TargetDialog() string   { return PeerPairID }
func (pairOutcomeMsg) TargetDialog() string   { return PeerPairID }
func (pairMovedMsg) TargetDialog() string     { return PeerPairID }
func (pairDialMsg) TargetDialog() string      { return PeerPairID }
func (pairPinnedMsg) TargetDialog() string    { return PeerPairID }
func (pairTickMsg) TargetDialog() string      { return PeerPairID }
func (pairClipReadMsg) TargetDialog() string  { return PeerPairID }
func (PairClipboardMsg) TargetDialog() string { return PeerPairID }

// PeerPair is the Add a teammate wizard.
type PeerPair struct {
	com  *common.Common
	link workspace.PeerLink
	help help.Model

	step pairStep
	mode pairMode
	req  int // the request in flight; bumped to abandon one

	wait    *peer.PairWait     // wait mode: the armed code
	moving  bool               // wait mode: moving to every interface
	copied  bool               // wait mode: the invite was copied; it shows in full
	cancel  context.CancelFunc // connect mode: the dial in flight
	dialAt  time.Time          // connect mode: when the dial started
	res     peer.PairResult    // what the other Deck proved
	expect  string             // connect mode: the fingerprint an invite named
	pasted  bool               // connect mode: the form came from an invite
	askClip bool               // an OSC 52 clipboard read is out

	inputs [fieldDial]textinput.Model // connect mode: name, address, code
	focus  int
	dial   bool
	name   textinput.Model // wait mode: the name to pin under

	err      string
	taken    string // a name paired with another key: Enter again replaces it
	paired   string // the name pinned
	moreHelp bool
	now      func() time.Time

	keyMap struct {
		Up, Down, Next, Prev, Select, Toggle key.Binding
		Copy, CopyCode, ListenAll, NewCode   key.Binding
		Paste, PasteForm                     key.Binding
		Yes, No, Back, Close, More           key.Binding
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
	p.inputs[fieldAddr].CharLimit = 260
	p.name = p.newInput("their name here")

	p.keyMap.Up = key.NewBinding(key.WithKeys("up", "ctrl+p"), key.WithHelp("↑/↓", "choose"))
	p.keyMap.Down = key.NewBinding(key.WithKeys("down", "ctrl+n"), key.WithHelp("↓", "next"))
	p.keyMap.Next = key.NewBinding(key.WithKeys("tab", "down"), key.WithHelp("tab", "next field"))
	p.keyMap.Prev = key.NewBinding(key.WithKeys("shift+tab", "up"), key.WithHelp("shift+tab", "previous field"))
	p.keyMap.Select = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "continue"))
	p.keyMap.Toggle = key.NewBinding(key.WithKeys(" ", "space"), key.WithHelp("space", "toggle"))
	p.keyMap.Copy = key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "copy invite"))
	p.keyMap.CopyCode = key.NewBinding(key.WithKeys("C", "shift+c"), key.WithHelp("C", "copy code only"))
	p.keyMap.ListenAll = key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "listen on all interfaces"))
	p.keyMap.NewCode = key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new code"))
	p.keyMap.Paste = key.NewBinding(key.WithKeys("v", "ctrl+v"), key.WithHelp("v", "paste invite"))
	p.keyMap.PasteForm = key.NewBinding(key.WithKeys("ctrl+v"), key.WithHelp("ctrl+v", "paste invite"))
	p.keyMap.Yes = key.NewBinding(key.WithKeys("enter", "y"), key.WithHelp("enter", "yes, they match"))
	p.keyMap.No = key.NewBinding(key.WithKeys("esc", "alt+esc", "n"), key.WithHelp("esc", "no"))
	p.keyMap.Back = key.NewBinding(key.WithKeys("esc", "alt+esc"), key.WithHelp("esc", "back"))
	p.keyMap.Close = CloseKey
	p.keyMap.More = MoreKey
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

// abort abandons whatever is in flight: the code is disarmed, a dial stops,
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
	p.moving, p.copied, p.askClip = false, false, false
}

// fail shows why pairing stopped.
func (p *PeerPair) fail(msg string) {
	p.abort()
	p.step, p.err = pairFailed, msg
}

// left is how long the code shown has left.
func (p *PeerPair) left() time.Duration {
	if p.wait == nil {
		return 0
	}
	return max(0, p.wait.Expires.Sub(p.clock()).Round(time.Second))
}

// expire shows that the code ran out, with a new one a key away.
func (p *PeerPair) expire() {
	p.abort()
	p.step, p.err = pairExpired, ""
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
	case pairMovedMsg:
		if msg.req != p.req {
			msg.wait.Cancel()
			return nil
		}
		p.moving = false
		if msg.err != nil {
			p.fail("Could not listen on all interfaces: " + friendlyErr(msg.err))
			return nil
		}
		same := p.wait != nil && msg.wait.Done == p.wait.Done
		p.wait, p.err = msg.wait, ""
		if same {
			return ActionCmd{Cmd: util.ReportInfo("Listening on all interfaces; the code still works")}
		}
		// A fresh code: its outcome is waited for anew.
		return ActionCmd{Cmd: tea.Batch(p.awaitOutcome(), util.ReportInfo("Listening on all interfaces, with a new code"))}
	case pairOutcomeMsg:
		if p.wait == nil || msg.done != p.wait.Done || p.step != pairWaiting {
			return nil
		}
		if p.moving && errors.Is(msg.out.Err, context.Canceled) {
			return nil // the move replaced it; pairMovedMsg says with what
		}
		p.wait = nil
		switch {
		case errors.Is(msg.out.Err, peer.ErrPairExpired):
			p.expire()
			return nil
		case msg.out.Err != nil:
			p.fail(peer.PairErrText(msg.out.Err, ""))
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
			addr := peer.WithDefaultPort(strings.TrimSpace(p.inputs[fieldAddr].Value()))
			p.step, p.err = pairForm, peer.PairErrText(msg.err, addr)
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
		if msg.req != p.req {
			return nil
		}
		switch p.step {
		case pairWaiting:
			if p.wait != nil && p.left() <= 0 {
				p.expire()
				return nil
			}
			return ActionCmd{Cmd: p.tick()}
		case pairDialing:
			return ActionCmd{Cmd: p.tick()}
		}
	case pairClipReadMsg:
		if msg.req != p.req {
			return nil
		}
		if msg.err != nil {
			// No system clipboard here (an SSH session, say): ask the
			// terminal over OSC 52; its answer comes as PairClipboardMsg.
			p.askClip = true
			return ActionCmd{Cmd: func() tea.Msg { return tea.ReadClipboard() }}
		}
		return p.paste(msg.text, true)
	case PairClipboardMsg:
		if !p.askClip {
			return nil
		}
		p.askClip = false
		return p.paste(msg.Content, true)
	case tea.PasteMsg:
		return p.paste(msg.Content, false)
	case tea.KeyPressMsg:
		return p.handleKey(msg)
	}
	return nil
}

// tick redraws a countdown or the dial's progress in a second.
func (p *PeerPair) tick() tea.Cmd {
	req := p.req
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return pairTickMsg{req: req} })
}

// startWaiting arms a fresh code, off the UI loop.
func (p *PeerPair) startWaiting() tea.Cmd {
	p.abort()
	p.mode, p.step, p.err = pairModeWait, pairWaiting, ""
	link, req := p.link, p.req
	if link == nil {
		p.fail(teamUnavailable)
		return nil
	}
	return func() tea.Msg {
		w, err := link.StartPairListen(context.Background())
		return pairStartedMsg{req: req, wait: w, err: err}
	}
}

// listenAll moves the wait to every interface, off the UI loop: only when
// the person pressed the key for it.
func (p *PeerPair) listenAll() Action {
	if p.wait == nil || p.wait.Reachable || p.moving || p.link == nil {
		return nil
	}
	p.moving, p.err = true, ""
	link, w, req := p.link, p.wait, p.req
	return ActionCmd{Cmd: func() tea.Msg {
		nw, err := link.PairListenAll(w)
		return pairMovedMsg{req: req, wait: nw, err: err}
	}}
}

// awaitOutcome waits for the other Deck, off the UI loop.
func (p *PeerPair) awaitOutcome() tea.Cmd {
	done := p.wait.Done
	return func() tea.Msg {
		out, ok := <-done
		if !ok {
			out.Err = context.Canceled
		}
		return pairOutcomeMsg{done: done, out: out}
	}
}

// copyInvite copies the invite: the one line the other Deck needs. It
// holds the code, so it is copied, shown here, and nowhere else.
func (p *PeerPair) copyInvite() Action {
	if p.wait == nil {
		return nil
	}
	inv := p.wait.Invite()
	if inv.Host == "" {
		return ActionCmd{Cmd: common.CopyToClipboard(p.wait.Code, "Copied the code")}
	}
	p.copied = true
	return ActionCmd{Cmd: common.CopyToClipboard(inv.Encode(), "Copied the invite")}
}

// readClipboard reads the system clipboard for v, off the UI loop.
func (p *PeerPair) readClipboard() Action {
	req := p.req
	return ActionCmd{Cmd: func() tea.Msg {
		b, err := clipboard.Read(clipboard.FormatText)
		if err == nil && len(b) == 0 {
			err = clipboard.ErrEmpty
		}
		return pairClipReadMsg{req: req, text: string(b), err: err}
	}}
}

// paste takes pasted text. An invite fills the whole connect form, from
// step 1 or any field; other text goes to the focused field, so pasting a
// plain code keeps working. explicit is set for v, where anything but an
// invite is an error.
func (p *PeerPair) paste(text string, explicit bool) Action {
	text = strings.TrimSpace(text)
	switch p.step {
	case pairChoose, pairForm, pairFailed:
	default:
		return nil
	}
	if peer.LooksLikeInvite(text) || explicit {
		inv, err := peer.ParseInvite(text)
		if err != nil {
			p.err = "That is not a Deck invite. Copy it again with c on the waiting Deck."
			if p.step == pairFailed {
				p.step = pairChoose
			}
			return nil
		}
		p.fillFromInvite(inv)
		return nil
	}
	if p.step != pairForm || p.focus == fieldDial {
		return nil
	}
	var cmd tea.Cmd
	p.inputs[p.focus], cmd = p.inputs[p.focus].Update(tea.PasteMsg{Content: text})
	p.err = ""
	return ActionCmd{Cmd: cmd}
}

// fillFromInvite fills the connect form from an invite and remembers the
// fingerprint it names; the person checks the name and presses Enter.
func (p *PeerPair) fillFromInvite(inv peer.Invite) {
	p.abort()
	name := inv.Name
	if cur := strings.TrimSpace(p.inputs[fieldName].Value()); cur != "" && p.step == pairForm {
		name = cur // a name typed here wins
	}
	p.mode, p.step, p.err, p.taken = pairModeConnect, pairForm, "", ""
	p.inputs[fieldName].SetValue(name)
	p.inputs[fieldAddr].SetValue(inv.Addr())
	p.inputs[fieldCode].SetValue(inv.Code)
	for i := range p.inputs {
		p.inputs[i].CursorEnd()
	}
	p.expect, p.pasted = inv.Fingerprint, true
	if name == "" {
		p.setFocus(fieldName)
	} else {
		p.setFocus(fieldDial)
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
	p.step, p.err, p.taken, p.dialAt = pairDialing, "", "", p.clock()
	// The direct try and the relays after it have their own timeouts; this
	// one only adds a margin so a stuck call can never hold the wizard.
	ctx, cancel := context.WithTimeout(context.Background(), 2*peer.PairDialTimeout+2*time.Second)
	p.cancel = cancel
	link, req, expect := p.link, p.req, p.expect
	return ActionCmd{Cmd: tea.Batch(func() tea.Msg {
		defer cancel()
		res, err := link.PairDial(ctx, addr, code, expect)
		return pairDialMsg{req: req, res: res, err: err}
	}, p.tick())}
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

// resetForm empties the connect form and forgets an invite.
func (p *PeerPair) resetForm() {
	for i := range p.inputs {
		p.inputs[i].SetValue("")
	}
	p.expect, p.pasted, p.dial = "", false, true
}

func (p *PeerPair) handleKey(msg tea.KeyPressMsg) Action {
	if p.step != pairForm && !(p.step == pairConfirm && p.mode == pairModeWait) && key.Matches(msg, p.keyMap.More) {
		p.moreHelp = !p.moreHelp
		return nil
	}
	switch p.step {
	case pairChoose:
		switch {
		case key.Matches(msg, p.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, p.keyMap.Up, p.keyMap.Down):
			p.mode = 1 - p.mode
		case key.Matches(msg, p.keyMap.Paste):
			return p.readClipboard()
		case key.Matches(msg, p.keyMap.Select):
			if p.mode == pairModeWait {
				return ActionCmd{Cmd: p.startWaiting()}
			}
			p.resetForm()
			p.step, p.err = pairForm, ""
			p.setFocus(fieldName)
		}
	case pairWaiting:
		switch {
		case key.Matches(msg, p.keyMap.Close):
			p.abort()
			return ActionClose{}
		case key.Matches(msg, p.keyMap.CopyCode):
			if p.wait != nil {
				return ActionCmd{Cmd: common.CopyToClipboard(p.wait.Code, "Copied the code")}
			}
		case key.Matches(msg, p.keyMap.Copy):
			return p.copyInvite()
		case key.Matches(msg, p.keyMap.ListenAll):
			return p.listenAll()
		case key.Matches(msg, p.keyMap.NewCode):
			return ActionCmd{Cmd: p.startWaiting()}
		}
	case pairExpired:
		switch {
		case key.Matches(msg, p.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, p.keyMap.NewCode, p.keyMap.Select):
			return ActionCmd{Cmd: p.startWaiting()}
		}
	case pairForm:
		switch {
		case key.Matches(msg, p.keyMap.Back):
			p.step, p.err = pairChoose, ""
		case key.Matches(msg, p.keyMap.PasteForm):
			return p.readClipboard()
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
			if p.focus == fieldAddr || p.focus == fieldCode {
				// The invite's fingerprint belongs to the invite's
				// address and code: edited by hand, it no longer applies.
				p.expect, p.pasted = "", false
			}
			return ActionCmd{Cmd: cmd}
		}
	case pairDialing:
		if key.Matches(msg, p.keyMap.Back) {
			p.abort()
			p.step, p.err = pairForm, "Cancelled."
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
func pairErrText(err error) string { return peer.PairErrText(err, "the other Deck") }

// stepOf is the step number shown in the title; 0 for none.
func (p *PeerPair) stepOf() int {
	switch p.step {
	case pairChoose:
		return 1
	case pairWaiting, pairExpired, pairForm, pairDialing:
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
	rc.Help = p.renderHelp(innerWidth)
	body := p.fit(innerWidth, p.maxBody(area, rc.Help))
	rc.AddPart(strings.Join(body, "\n"))
	DrawCenter(scr, area, rc.Render())
	return nil
}

// maxBody is how many body lines fit in area under the title and help.
func (p *PeerPair) maxBody(area uv.Rectangle, helpView string) int {
	t := p.com.Styles
	return area.Dy() - t.Dialog.View.GetVerticalFrameSize() -
		t.Dialog.Title.GetVerticalFrameSize() - titleContentHeight - lipgloss.Height(helpView) - 1
}

// fit renders the step in at most maxLines lines: in full when it fits,
// else compact (no spacing, no box around the code), else the top of the
// compact one, which has what to do next first.
func (p *PeerPair) fit(width, maxLines int) []string {
	body := p.lines(width, false)
	if maxLines <= 0 || len(body) <= maxLines {
		return body
	}
	body = p.lines(width, true)
	if len(body) > maxLines {
		body = body[:maxLines]
	}
	return body
}

// renderHelp is the key help for the step, never cut off.
func (p *PeerPair) renderHelp(width int) string {
	more := &p.keyMap.More
	if p.step == pairForm || (p.step == pairConfirm && p.mode == pairModeWait) {
		more = nil // ? would be typed into a field: show every hint instead
	}
	return renderFittedHelp(p.com.Styles, &p.help, p.ShortHelp(), more, p.moreHelp, width)
}

// lines renders the current step at width; compact drops spacing and the
// box around the code for short terminals.
func (p *PeerPair) lines(width int, compact bool) []string {
	t := p.com.Styles
	muted := t.Dialog.SecondaryText
	wrap := func(str string) []string { return wrapText(muted, str, width) }
	strong := func(str string) []string { return wrapText(t.Dialog.PrimaryText, str, width) }
	gap := func() []string {
		if compact {
			return nil
		}
		return []string{""}
	}
	label := func(l string) string {
		return t.Dialog.NormalItem.UnsetPadding().Render(padRight(l, pairLabelWidth))
	}
	// field shows a value after its label, wrapped under the value column.
	field := func(l, value string) []string {
		var out []string
		vw := max(1, width-pairLabelWidth)
		for i, v := range strings.Split(ansi.Wrap(value, vw, ""), "\n") {
			pre := label(l)
			if i > 0 {
				pre = strings.Repeat(" ", pairLabelWidth)
			}
			out = append(out, pre+t.Dialog.ListItem.InfoFocused.Render(v))
		}
		return out
	}
	// fpRows shows a fingerprint in full: after its label when it fits,
	// else on the lines under it, split but never shortened.
	fpRows := func(l, fp, note string) []string {
		value := fp
		if note != "" {
			value += "  " + note
		}
		if pairLabelWidth+ansi.StringWidth(value) <= width {
			return []string{label(l) + t.Dialog.ListItem.InfoFocused.Render(fp) + t.Dialog.ListItem.InfoBlurred.Render(strings.TrimPrefix(value, fp))}
		}
		out := []string{label(l)}
		for _, part := range hardLines(fp, max(1, width-2)) {
			out = append(out, "  "+t.Dialog.ListItem.InfoFocused.Render(part))
		}
		if note != "" {
			out = append(out, "  "+t.Dialog.ListItem.InfoBlurred.Render(note))
		}
		return out
	}
	errLines := func() []string {
		if p.err == "" {
			return nil
		}
		return append(gap(), wrapText(t.Dialog.TitleError.PaddingLeft(1), p.err, width)...)
	}
	var out []string
	add := func(ls ...[]string) {
		for _, l := range ls {
			out = append(out, l...)
		}
	}

	switch p.step {
	case pairChoose:
		add(strong("How do you want to pair?"), gap())
		opts := []struct{ title, desc string }{
			{"This Deck waits for a code", "Show a one-time code and an invite here."},
			{"Connect to another Deck", "Paste its invite, or type its address and code."},
		}
		for i, o := range opts {
			bullet := t.Radio.Off
			if pairMode(i) == p.mode {
				bullet = t.Radio.On
			}
			out = append(out, " "+bullet.Render()+t.Radio.Label.Padding(0, 1).Render(o.title))
			add(wrapText(muted.PaddingLeft(4), o.desc, width))
		}
		add(gap(), wrap("Got an invite? Paste it here, or press v."), errLines())
		return out

	case pairWaiting:
		if p.wait == nil {
			return wrap("Arming a one-time code…")
		}
		w := p.wait
		inv := w.Invite()
		addr := w.Addr
		if inv.Host != "" {
			addr = inv.Addr()
		}
		add(strong("Waiting for the other Deck · expires in "+clockSpan(p.left())),
			wrap("On it: Settings › Team › a › Connect, and paste the invite (c copies it). Or type the address and code:"),
			gap(), field("Address", addr))
		if len(inv.Alt) > 0 {
			add(field("", "or "+strings.Join(inv.Alt, ", ")))
		}
		if compact {
			add(field("Code", spaced(w.Code)))
		} else {
			big := t.Dialog.PrimaryText.Bold(true).
				Border(lipgloss.RoundedBorder()).BorderForeground(t.Dialog.PrimaryText.GetForeground()).
				Padding(0, 2).Render(spaced(w.Code))
			for i, l := range strings.Split(big, "\n") {
				pre := strings.Repeat(" ", pairLabelWidth)
				if i == 1 {
					pre = label("Code")
				}
				out = append(out, pre+l)
			}
		}
		add(fpRows("Fingerprint", w.Fingerprint, ""), field("This Deck", w.Name),
			field("Listening on", w.Addr+" ("+reach(w.Addr)+")"))
		if !w.Reachable {
			host, _, _ := net.SplitHostPort(w.Addr)
			add(gap(), wrapText(t.Dialog.TitleError.PaddingLeft(1),
				"The other Deck cannot reach "+host+" — it only listens on this machine. Press l to listen on all interfaces.", width))
		}
		if p.moving {
			add(wrap("Moving to all interfaces…"))
		}
		add(wrap("Open port "+portOf(w.Addr)+" in your firewall if the other Deck is on another network."),
			wrap("Both behind NAT? No port needed: if a Deck you are both paired with relays, the invite reaches this one through it."),
			wrap("5-minute, single use · c copies the invite · C just the code"))
		if p.copied && inv.Host != "" {
			add(gap(), wrap("Copied. If it did not arrive, select it here:"))
			for _, l := range hardLines(inv.Encode(), max(1, width-2)) {
				out = append(out, "  "+t.Dialog.ListItem.InfoFocused.Render(l))
			}
		}
		add(errLines())
		return out

	case pairExpired:
		add(strong("The code expired."), wrap("Nothing was paired. n gets a new code; esc closes."))
		return out

	case pairForm, pairDialing:
		labels := [fieldDial]string{"Its name here", "Address", "Pairing code"}
		add(strong("Type what the other Deck shows, or paste its invite:"), gap())
		for i := range p.inputs {
			in := p.inputs[i]
			in.SetWidth(max(1, width-pairLabelWidth-ansi.StringWidth(in.Prompt)-1))
			l := label(labels[i])
			if i == p.focus && p.step == pairForm {
				l = t.Dialog.PrimaryText.UnsetPadding().Render(padRight(labels[i], pairLabelWidth))
			}
			out = append(out, l+in.View())
		}
		toggle := "Also connect to it automatically: " + onOff(p.dial) + "  (space)"
		style := t.Dialog.NormalItem
		if p.focus == fieldDial && p.step == pairForm {
			style = t.Dialog.SelectedItem
		}
		add(wrapText(style, toggle, width))
		if p.expect != "" {
			add(fpRows("Expect", p.expect, "(from the invite; checked)"))
		}
		if p.pasted && p.step == pairForm {
			add(wrap("Invite pasted: check the name, then enter connects."))
		}
		if p.step == pairDialing {
			addr := peer.WithDefaultPort(strings.TrimSpace(p.inputs[fieldAddr].Value()))
			el := p.clock().Sub(p.dialAt).Round(time.Second)
			add(gap(), wrap(fmt.Sprintf("Checking the code with %s… %s · esc cancels", addr, clockSpan(el))))
			if p.expect != "" {
				add(wrap("If it does not answer, Deck tries through the teammates you are connected to."))
			}
		}
		add(errLines())
		return out

	case pairConfirm, pairSaving:
		r := p.res
		who := peer.Sanitize(r.PeerName)
		if ansi.StringWidth(who) > 32 {
			who = ansi.Cut(who, 0, 32)
		}
		note := ""
		if p.mode == pairModeConnect && p.expect != "" && p.expect == r.PeerFingerprint {
			note = "✓ matches the invite"
		}
		by := "Code accepted by " + peer.Sanitize(r.RemoteAddr)
		if r.Via != "" {
			by = "Code accepted through " + r.Via + ", which relayed it and saw only ciphertext"
		}
		add(wrap(by+" (it calls itself “"+who+"”)."), gap(),
			fpRows("Theirs", r.PeerFingerprint, note), fpRows("This Deck", r.OwnFingerprint, ""), gap(),
			strong("Do these match on the other screen?"))
		if p.mode == pairModeWait {
			p.name.SetWidth(max(1, width-pairLabelWidth-ansi.StringWidth(p.name.Prompt)-1))
			out = append(out, label("Save it as")+p.name.View())
		} else {
			add(field("Save it as", p.pinName()))
		}
		if p.step == pairSaving {
			add(gap(), wrap("Saving…"))
		}
		add(errLines())
		return out

	case pairDone:
		add(strong("Paired with "+p.paired+"."), gap())
		switch {
		case p.res.Via != "" && p.mode == pairModeConnect:
			add(wrap("This Deck connects to it through " + p.res.Via + " while Team link is on."))
		case p.res.Via != "":
			add(wrap("It connects to this Deck through " + p.res.Via + ", which may now relay links to this Deck."))
		case p.mode == pairModeConnect && p.dial:
			add(wrap("This Deck connects to it while Team link is on."))
		default:
			add(wrap("It can connect to this Deck while Team link and Accept connections are on."))
		}
		return out
	}
	// pairFailed
	add(wrapText(t.Dialog.TitleError.PaddingLeft(1), p.err, width), gap(), wrap("enter starts again · esc closes"))
	return out
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
		return "this machine only"
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

// ShortHelp implements [help.KeyMap]: the hints for the step, most
// important first.
func (p *PeerPair) ShortHelp() []key.Binding {
	select_ := func(h string) key.Binding {
		return key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", h))
	}
	esc := func(h string) key.Binding {
		return key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", h))
	}
	switch p.step {
	case pairChoose:
		return []key.Binding{select_("continue"), p.keyMap.Up, p.keyMap.Paste, esc("close")}
	case pairWaiting:
		b := []key.Binding{p.keyMap.Copy}
		if p.wait != nil && !p.wait.Reachable {
			b = append(b, p.keyMap.ListenAll)
		}
		return append(b, esc("stop"), p.keyMap.CopyCode, p.keyMap.NewCode)
	case pairExpired:
		return []key.Binding{p.keyMap.NewCode, esc("close")}
	case pairForm:
		return []key.Binding{select_("connect"), p.keyMap.Next, p.keyMap.PasteForm, esc("back")}
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
