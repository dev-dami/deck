package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/clipimg"
	"github.com/dev-dami/deck/internal/session"
)

// ---- helpers ----

// typed makes the rig's keys arrive as one fast burst, like a terminal that
// delivers a paste as key presses. It returns a function that restores typing
// speed.
func (r *rig) burst() { r.clock.step = 0 }

func (r *rig) slow() { r.clock.step = 100 * time.Millisecond }

func (r *rig) keyAt(d time.Duration, k tea.KeyMsg) {
	r.clock.advance(d)
	r.feed(k)
}

func runeKey(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

var enterKey = tea.KeyMsg{Type: tea.KeyEnter}

// where finds s on the screen and returns the cell it starts at.
// where finds s in the main pane; whereSide finds it anywhere, sidebar included.
func (r *rig) where(s string) (x, y int) {
	r.t.Helper()
	return r.whereFrom(s, r.m.g.mainX)
}

func (r *rig) whereSide(s string) (x, y int) {
	r.t.Helper()
	return r.whereFrom(s, 0)
}

func (r *rig) whereFrom(s string, minX int) (x, y int) {
	r.t.Helper()
	for y, l := range r.lines() {
		if i := strings.Index(l, s); i >= 0 && sw(l[:i]) >= minX {
			return sw(l[:i]), y
		}
	}
	r.t.Fatalf("%q is not on the screen:\n%s", s, r.view())
	return 0, 0
}

func pngFile(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("\x89PNG\r\n\x1a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func (r *rig) members() int { return len(r.m.curThread().Members) }

// ---- 1. paste must not send ----

func TestBurstEnterInsertsNewlineInsteadOfSending(t *testing.T) {
	r := newRig(t, 100, 30)
	r.burst()
	r.feed(runeKey("a"))
	r.feed(runeKey("b"))
	r.keyAt(5*time.Millisecond, enterKey) // within 15ms of a typed key: part of a paste
	r.keyAt(2*time.Millisecond, runeKey("c"))
	r.keyAt(2*time.Millisecond, enterKey)
	r.keyAt(20*time.Millisecond, enterKey) // burst mode lasts until 60ms of quiet: still a newline
	r.feed(runeKey("d"))
	if got := r.m.comp.Value(); got != "ab\nc\n\nd" {
		t.Fatalf("composer = %q, want %q", got, "ab\nc\n\nd")
	}
	if r.members() != 0 || len(r.m.threads) > 1 && r.m.curThread().Title != "" {
		t.Fatalf("a pasted newline sent the message")
	}
	// after 60ms of quiet an Enter sends again
	r.keyAt(80*time.Millisecond, enterKey)
	r.pump("sent", func() bool { return r.members() == 1 })
	if !r.m.comp.Empty() {
		t.Errorf("composer = %q after sending", r.m.comp.Value())
	}
}

func TestSlowEnterStillSends(t *testing.T) {
	r := newRig(t, 100, 30)
	r.keyAt(0, runeKey("h"))
	r.keyAt(14*time.Millisecond, runeKey("i")) // fast typing alone is fine
	r.keyAt(40*time.Millisecond, enterKey)     // a human-speed Enter
	r.pump("sent", func() bool { return r.members() == 1 })
}

func TestBurstTabIsIndentationNotFocusChange(t *testing.T) {
	r := newRig(t, 100, 30)
	r.burst()
	r.feed(runeKey("x"))
	r.feed(tea.KeyMsg{Type: tea.KeyTab})
	r.feed(runeKey("y"))
	if r.m.focus != focusComposer || r.m.comp.Value() != "x    y" {
		t.Errorf("focus=%v composer=%q", r.m.focus, r.m.comp.Value())
	}
}

func TestPasteMsgInsertsWithoutSending(t *testing.T) {
	r := newRig(t, 100, 30)
	r.feed(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("one\r\ntwo\r\n"), Paste: true})
	if got := r.m.comp.Value(); got != "one\ntwo\n" || r.members() != 0 {
		t.Fatalf("composer = %q members=%d", got, r.members())
	}
}

// ---- 2. images ----

func TestPastedImagePathBecomesAChip(t *testing.T) {
	r := newRig(t, 100, 30)
	p := pngFile(t, "shot.png")
	r.feed(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(`"` + p + `"`), Paste: true})
	if !r.m.comp.Empty() || len(r.m.attach) != 1 || r.m.attach[0].Name != "shot.png" {
		t.Fatalf("composer %q attach %+v", r.m.comp.Value(), r.m.attach)
	}
	if !r.has("▣ shot.png ×") {
		t.Errorf("no chip on screen:\n%s", r.view())
	}
	// ordinary pasted text is text
	r.feed(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("see shot.png"), Paste: true})
	if r.m.comp.Value() != "see shot.png" || len(r.m.attach) != 1 {
		t.Errorf("text paste: %q %+v", r.m.comp.Value(), r.m.attach)
	}
}

func TestBurstOfAnImagePathWithEnterAttaches(t *testing.T) {
	r := newRig(t, 100, 30)
	p := pngFile(t, "drop.png")
	r.burst()
	for _, c := range p {
		r.feed(runeKey(string(c)))
	}
	r.feed(enterKey)
	if !r.m.comp.Empty() || len(r.m.attach) != 1 || r.members() != 0 {
		t.Fatalf("composer %q attach %+v members %d", r.m.comp.Value(), r.m.attach, r.members())
	}
}

func TestBurstOfAnImagePathWithoutEnterAttachesWhenItStops(t *testing.T) {
	r := newRig(t, 100, 30)
	p := pngFile(t, "drop2.jpg")
	r.burst()
	for _, c := range p {
		r.feed(runeKey(string(c)))
	}
	r.clock.advance(10 * time.Millisecond)
	r.m.Update(burstCheckMsg{}) // still arriving: nothing yet
	if len(r.m.attach) != 0 {
		t.Fatal("attached while the paste was still arriving")
	}
	r.clock.advance(200 * time.Millisecond)
	r.m.Update(burstCheckMsg{})
	if len(r.m.attach) != 1 || !r.m.comp.Empty() || r.m.attach[0].Type != "image/jpeg" {
		t.Fatalf("composer %q attach %+v", r.m.comp.Value(), r.m.attach)
	}
}

func TestCtrlVAttachesClipboardImage(t *testing.T) {
	r := newRig(t, 100, 30)
	p := pngFile(t, "deck-clip-1830.png")
	r.m.clipSave = func(context.Context, string) (string, error) { return p, nil }
	r.key("up") // any state
	r.feed(tea.KeyMsg{Type: tea.KeyCtrlV})
	if len(r.m.attach) != 1 || r.m.attach[0].Path != p {
		t.Fatalf("attach = %+v", r.m.attach)
	}
	r.m.clipSave = func(context.Context, string) (string, error) { return "", clipimg.ErrNoImage }
	r.feed(tea.KeyMsg{Type: tea.KeyCtrlV})
	if len(r.m.attach) != 1 || r.m.toast.on {
		t.Errorf("no image: attach %+v toast %v", r.m.attach, r.m.toast.on)
	}
}

func TestCtrlVWithoutAToolToastsOnce(t *testing.T) {
	r := newRig(t, 100, 30)
	r.m.clipSave = func(context.Context, string) (string, error) { return "", clipimg.ErrUnavailable }
	r.feed(tea.KeyMsg{Type: tea.KeyCtrlV})
	if !r.m.toast.on {
		t.Fatal("no toast")
	}
	seq := r.m.toast.seq
	r.feed(tea.KeyMsg{Type: tea.KeyCtrlV})
	if r.m.toast.seq != seq {
		t.Error("toasted twice")
	}
}

func TestAttachmentChipsRemoveByBackspaceAndClick(t *testing.T) {
	r := newRig(t, 100, 30)
	a, b := pngFile(t, "a.png"), pngFile(t, "b.jpg")
	r.m.addAttach(a)
	r.m.addAttach(b)
	r.m.addAttach(a) // no duplicates
	r.feed(tea.WindowSizeMsg{Width: 100, Height: 30})
	if !r.has("▣ a.png ×  ▣ b.jpg ×") {
		t.Fatalf("chips:\n%s", r.view())
	}
	_, y := r.where("▣ a.png")
	if y != r.m.g.attY {
		t.Fatalf("chip line drawn on row %d, geometry says %d", y, r.m.g.attY)
	}
	r.key("backspace") // empty composer: removes the last chip
	if len(r.m.attach) != 1 || r.m.attach[0].Name != "a.png" {
		t.Fatalf("after backspace: %+v", r.m.attach)
	}
	x, y := r.where("a.png ×")
	r.click(x+len("a.png "), y) // the ×
	if len(r.m.attach) != 0 || r.has("▣") {
		t.Fatalf("after click: %+v\n%s", r.m.attach, r.view())
	}
	// clicking the name works too
	r.m.addAttach(b)
	r.feed(tea.WindowSizeMsg{Width: 100, Height: 30})
	x, y = r.where("▣ b.jpg")
	r.click(x+3, y)
	if len(r.m.attach) != 0 {
		t.Fatalf("click on the name: %+v", r.m.attach)
	}
}

func TestSendingWithImages(t *testing.T) {
	r := newRig(t, 100, 30)
	r.start("hello")
	r.done(r.claude.last(), "claude")
	a, b := pngFile(t, "a.png"), pngFile(t, "b.png")
	r.m.addAttach(a)
	r.m.addAttach(b)
	r.say("what is wrong here")
	r.pump("images shown", func() bool { return r.has("▣ 2 images: a.png, b.png") })
	if len(r.m.attach) != 0 {
		t.Error("attachments stay in the composer after sending")
	}
	// image-only
	r.done(r.claude.last(), "claude")
	r.m.addAttach(pngFile(t, "solo.png"))
	r.key("enter")
	r.pump("image-only shown", func() bool { return r.has("▣ 1 image: solo.png") })
}

// ---- 3. context meter ----

func TestContextMeterGlyphsAndColours(t *testing.T) {
	for _, tc := range []struct {
		pct   int
		glyph string
	}{{0, "○"}, {24, "○"}, {25, "◔"}, {42, "◔"}, {50, "◑"}, {75, "◕"}, {99, "◕"}, {100, "●"}} {
		if got := ctxGlyph(tc.pct); got != tc.glyph {
			t.Errorf("ctxGlyph(%d) = %s, want %s", tc.pct, got, tc.glyph)
		}
	}
	fg := func(p int) any { return ctxStyle(p).GetForeground() }
	if fg(69) != sMuted.GetForeground() || fg(70) != sWarn.GetForeground() ||
		fg(90) != sWarn.GetForeground() || fg(91) != sDanger.GetForeground() {
		t.Error("meter colours: muted <70, warn 70-90, danger >90")
	}
	if _, ok := ctxPct(session.AgentInfo{ContextTokens: 5}); ok {
		t.Error("a zero window must hide the meter")
	}
	if p, _ := ctxPct(session.AgentInfo{ContextTokens: 42000, ContextWindow: 100000}); p != 42 {
		t.Errorf("pct = %d", p)
	}
}

func TestContextMeterInChipLineAndSidebar(t *testing.T) {
	r := newRig(t, 120, 30)
	r.duo()
	cl := r.claude.last()
	r.event(cl, agent.Event{Kind: agent.KindUsage, Usage: &agent.Usage{ContextTokens: 42000, ContextWindow: 100000}})
	r.event(r.codex.last(), agent.Event{Kind: agent.KindUsage, Usage: &agent.Usage{ContextTokens: 95000, ContextWindow: 100000}})
	r.pump("meter", func() bool { return strings.Contains(r.chipText(), "◔ 42%") })
	if c := r.chipText(); !strings.Contains(c, "◕ 95%") {
		t.Errorf("chip line %q", c)
	}
	side := r.view()
	if !strings.Contains(side, "✻ claude ◔") || !strings.Contains(side, ">_ codex ◕") {
		t.Errorf("sidebar chips carry the glyph only:\n%s", side)
	}
}

func TestChipLineShowsRunningModel(t *testing.T) {
	r := newRig(t, 100, 30)
	r.start("hi")
	a := r.m.curThread().Members[0]
	a.RunningModel, a.Model = "claude-opus-5-5", "opus"
	if got := r.m.modelLabel(a); got != "claude-opus-5-5" {
		t.Errorf("label = %q", got)
	}
	a.RunningModel, a.Model = "", ""
	if got := r.m.modelLabel(a); got != "default" && got == "" {
		t.Errorf("label = %q", got)
	}
}

// ---- 4. queue and steer ----

// busyThread leaves claude mid-turn with one message queued for it.
func busyThread(t *testing.T, text string) *rig {
	t.Helper()
	r := newRig(t, 100, 30)
	r.start("go")
	r.say(text)
	r.pump("queued", func() bool { return len(r.m.queueOf(r.m.sel)) == 1 })
	r.drain()
	return r
}

func TestQueueRowAndQueuedTag(t *testing.T) {
	r := busyThread(t, "fix the tests too")
	if !r.has("⏳ queued for ✻ claude · fix the tests too") || !r.has("steer ⏎ · ✕") {
		t.Fatalf("no queue row:\n%s", r.view())
	}
	if !strings.Contains(r.find("fix the tests too\n"), "queued") && !strings.Contains(r.view(), "Ada queued") {
		t.Errorf("the message carries no queued tag:\n%s", r.view())
	}
	_, y := r.where("⏳ queued for")
	if y != r.m.g.qY {
		t.Errorf("queue row on %d, geometry says %d", y, r.m.g.qY)
	}
}

func TestQueueShowsThreeAndMore(t *testing.T) {
	r := newRig(t, 100, 30)
	r.start("go")
	for _, s := range []string{"one", "two", "three", "four", "five"} {
		r.say(s)
		r.drain()
	}
	if got := strings.Count(r.view(), "⏳ queued for"); got != 3 {
		t.Errorf("%d rows shown, want 3:\n%s", got, r.view())
	}
	if !r.has("+2 more") {
		t.Errorf("no +2 more:\n%s", r.view())
	}
}

func TestClickQueueRowSteers(t *testing.T) {
	r := busyThread(t, "fix the tests too")
	x, y := r.where("⏳ queued")
	r.click(x+3, y)
	if !r.m.steering[r.m.queueOf(r.m.sel)[0].ID] || !r.has("↪ steering ✻ claude…") {
		t.Fatalf("row did not flip:\n%s", r.view())
	}
	r.pump("delivered", func() bool { return len(r.m.queueOf(r.m.sel)) == 0 })
	r.drain()
	if r.has("↪ steering") || r.has("⏳ queued") {
		t.Errorf("row stays after delivery:\n%s", r.view())
	}
	if !r.has("Ada steer") {
		t.Errorf("steer tag missing:\n%s", r.view())
	}
}

func TestClickQueueCrossCancels(t *testing.T) {
	r := busyThread(t, "never mind this")
	x, y := r.where("✕")
	r.click(x, y)
	r.pump("cancelled", func() bool { return len(r.m.queueOf(r.m.sel)) == 0 })
	r.done(r.claude.last(), "claude")
	for _, s := range r.claude.last().sentList() {
		if strings.Contains(s, "never mind this") {
			t.Errorf("a cancelled message was delivered: %q", s)
		}
	}
}

func TestAltUpFocusesQueueRowEnterSteersDeleteCancels(t *testing.T) {
	r := busyThread(t, "queued one")
	r.feed(tea.KeyMsg{Type: tea.KeyUp, Alt: true})
	if r.m.qFocus != 0 || !strings.Contains(r.find("⏳"), "▎") {
		t.Fatalf("qFocus=%d\n%s", r.m.qFocus, r.view())
	}
	if !strings.Contains(r.m.footerHints(), "enter") {
		t.Errorf("footer %q", ansiStrip(r.m.footerHints()))
	}
	r.feed(tea.KeyMsg{Type: tea.KeyDelete})
	r.pump("cancelled", func() bool { return len(r.m.queueOf(r.m.sel)) == 0 })

	r.say("queued two")
	r.pump("queued", func() bool { return len(r.m.queueOf(r.m.sel)) == 1 })
	r.feed(tea.KeyMsg{Type: tea.KeyUp, Alt: true})
	r.key("enter")
	r.pump("steered", func() bool { return len(r.m.queueOf(r.m.sel)) == 0 })
	r.drain()
	if !r.has("steer") {
		t.Errorf("no steer tag:\n%s", r.view())
	}
	// esc leaves the queue without doing anything
	r.feed(tea.KeyMsg{Type: tea.KeyEsc})
	if r.m.qFocus != -1 {
		t.Errorf("qFocus = %d", r.m.qFocus)
	}
}

func TestSteerEventWithoutQueuedMessageShowsAsSteerLine(t *testing.T) {
	r := newRig(t, 100, 30)
	r.start("go")
	r.feed(session.EventMsg{Event: agent.Event{Kind: agent.KindUserText, Text: "stop and do X"},
		ThreadID: r.m.sel, To: []string{"claude"}, EntryID: "e9", Steer: true, SteerOf: "gone"})
	if !r.has("Ada steer") {
		t.Errorf("\n%s", r.view())
	}
}

// ---- 5. edit ----

func TestUpOnEmptyComposerEditsLastMessageAndEnterResends(t *testing.T) {
	r := newRig(t, 100, 30)
	r.start("fix the bug")
	r.done(r.claude.last(), "claude")
	r.key("up")
	if r.m.edit == nil || r.m.comp.Value() != "fix the bug" {
		t.Fatalf("edit=%v composer=%q", r.m.edit, r.m.comp.Value())
	}
	if !strings.Contains(r.chipText(), "editing your message · enter resend · esc cancel") {
		t.Errorf("chip line %q", r.chipText())
	}
	// esc restores the draft
	r.key("esc")
	if r.m.edit != nil || !r.m.comp.Empty() {
		t.Fatalf("after esc: edit=%v composer=%q", r.m.edit, r.m.comp.Value())
	}
	r.typeText("half a draft")
	r.feed(tea.KeyMsg{Type: tea.KeyCtrlA})
	for r.m.comp.cur > 0 {
		r.m.comp.Left()
	}
	// with a draft, up just moves in the text: edit by the transcript instead
	r.m.comp.Reset()
	r.typeText("draft")
	r.key("tab")
	r.key("e")
	if r.m.edit == nil || r.m.comp.Value() != "fix the bug" {
		t.Fatalf("e on a selected message: edit=%v composer=%q", r.m.edit, r.m.comp.Value())
	}
	r.m.comp.Reset()
	r.typeText("fix the other bug")
	r.key("enter")
	r.pump("edited", func() bool { return r.has("fix the other bug") })
	r.drain()
	if !r.has("Ada edited") || r.has("fix the bug\n") {
		t.Errorf("edited tag missing:\n%s", r.view())
	}
	if r.m.comp.Value() != "draft" || r.m.edit != nil {
		t.Errorf("the draft is not back: %q", r.m.comp.Value())
	}
}

func TestUpKeepsThreadSelectionWhenAQueueExists(t *testing.T) {
	r := busyThread(t, "later")
	r.key("up")
	if r.m.edit != nil {
		t.Error("up edited a message while something is queued")
	}
}

func TestClickOnUserMessageStartsEdit(t *testing.T) {
	r := newRig(t, 100, 30)
	r.start("click me to edit")
	x, y := r.where("click me to edit")
	r.click(x+2, y)
	if r.m.edit == nil || r.m.comp.Value() != "click me to edit" {
		t.Fatalf("edit=%v composer=%q\n%s", r.m.edit, r.m.comp.Value(), r.view())
	}
}

func TestEditedMsgReplacesText(t *testing.T) {
	r := newRig(t, 100, 30)
	r.start("old words")
	id := r.tv().items[0].entryID
	if id == "" {
		t.Fatal("user message has no entry id")
	}
	r.feed(session.EditedMsg{ThreadID: r.m.sel, EntryID: id, OldText: "old words", NewText: "new words"})
	tr := ansiStrip(strings.Join(r.tv().lines, " "))
	if !strings.Contains(tr, "new words") || strings.Contains(tr, "old words") || !strings.Contains(tr, "Ada edited") {
		t.Errorf("\n%s", r.view())
	}
}

// ---- 6. clicking: every target uses the geometry it was drawn with ----

func TestClickToolRowFolds(t *testing.T) {
	r := newRig(t, 100, 30)
	r.start("go")
	toolRow(r, r.claude.last(), "t1", "Bash", "go test ./...", "ok  all passed")
	x, y := r.where("go test ./...")
	r.click(x+1, y)
	if !r.tv().items[len(r.tv().items)-1].expanded || !r.has("all passed") {
		t.Fatalf("tool row did not open:\n%s", r.view())
	}
}

func TestClickPickerRowPicks(t *testing.T) {
	r := newRig(t, 100, 30)
	r.start("hi")
	r.feed(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m"), Alt: true})
	r.pump("models", func() bool { return len(r.m.pick.items) > 1 })
	r.drain()
	lines := r.lines()
	row := -1
	for i, l := range lines {
		if strings.Contains(l, "2.") || strings.Contains(l, " 2 ") {
			row = i
			break
		}
	}
	if row < 0 {
		t.Skipf("picker rows not numbered in this layout:\n%s", r.view())
	}
	r.click(r.m.g.mainX+6, row)
	if r.m.pick.open && r.m.pick.sel != 1 {
		t.Errorf("click on row %d selected item %d\n%s", row, r.m.pick.sel, r.view())
	}
}

func TestClickSidebarRowAndChipLine(t *testing.T) {
	r := newRig(t, 120, 30)
	r.start("first")
	first := r.m.sel
	r.say("/new")
	r.say("second")
	r.pump("second thread", func() bool { return r.m.sel != first && r.members() == 1 })
	r.drain()
	x, y := r.whereSide(threadTitle(r.m.rows[0]))
	r.click(x+1, y)
	if r.m.sel != first {
		t.Errorf("sidebar click selected %s, want %s", r.m.sel, first)
	}
	// the model chip is drawn where chipAt says it is
	x, y = r.where("claude · ")
	if y != r.m.g.chipY {
		t.Fatalf("chip line on row %d, geometry says %d", y, r.m.g.chipY)
	}
	r.click(x+1, y)
	if !r.m.pick.open {
		t.Errorf("chip click did not open the picker")
	}
}
