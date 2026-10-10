package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/tui/motion"
	"github.com/dev-dami/deck/internal/tui/yolo"
)

// ---- helpers ----

func (r *rig) shiftTab() { r.feed(tea.KeyMsg{Type: tea.KeyShiftTab}) }

func (r *rig) click(x, y int) {
	r.feed(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
}

// clickChip clicks the first chip-line span of a kind (for hitModel, of one
// member; "" is the model a new agent starts with).
func (r *rig) clickChip(kind chipHitKind, agentID string) {
	r.t.Helper()
	_, hits := r.m.chipLayout(r.m.g.mainW)
	for _, h := range hits {
		if h.kind == kind && (kind == hitMode || h.agent == agentID) {
			r.click(r.m.g.mainX+h.x0+1, r.m.g.chipY)
			return
		}
	}
	r.t.Fatalf("no %v chip for %q in %v\n%s", kind, agentID, hits, r.view())
}

// upd feeds a message without running the commands it returns (the rig would
// chase the animation clock for as long as it runs).
func (r *rig) upd(msg tea.Msg) { r.m.Update(msg) }

// chipText is the chip line, colours stripped.
func (r *rig) chipText() string { return ansiStrip(r.m.chipLine(r.m.g.mainW)) }

// withMotion turns motion on for a test; the rest of the suite runs reduced.
func withMotion(t *testing.T) {
	t.Helper()
	motion.SetReduced(false)
	t.Cleanup(func() { motion.SetReduced(true) })
}

var t0 = time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)

// frozen gives the model a clock the test moves by hand.
func (r *rig) frozen(at time.Time) *time.Time {
	now := at
	r.m.nowFn = func() time.Time { return now }
	r.m.now = now
	return &now
}

// at moves the frozen clock and lets the model catch up.
func (r *rig) at(now *time.Time, to time.Time) {
	*now = to
	r.m.now = to
	r.m.settle()
}

// idleClaude is a one-agent thread that has said something and is idle.
func idleClaude(t *testing.T, w, h int) *rig {
	t.Helper()
	r := newRig(t, w, h)
	r.start("refactor the store")
	say(r.claude.last(), r, "I'll start with the store layer.")
	r.done(r.claude.last(), "claude")
	return r
}

// ---- mode chip ----

func TestShiftTabCyclesPlanAskYolo(t *testing.T) {
	r := idleClaude(t, 140, 40)
	run := r.claude.last()
	if got := r.mgr.Mode(r.m.sel); got != agent.ModeAsk {
		t.Fatalf("threads start in ask, got %s", got)
	}
	if !strings.HasSuffix(strings.TrimRight(r.chipText(), " "), "● ask") {
		t.Fatalf("chip line: %q", r.chipText())
	}

	r.shiftTab()
	if r.mgr.Mode(r.m.sel) != agent.ModeYolo || !strings.HasSuffix(strings.TrimRight(r.chipText(), " "), "▲ yolo") {
		t.Fatalf("ask -> yolo: mode %s, chip %q", r.mgr.Mode(r.m.sel), r.chipText())
	}
	r.shiftTab()
	if r.mgr.Mode(r.m.sel) != agent.ModePlan || !strings.HasSuffix(strings.TrimRight(r.chipText(), " "), "◇ plan") {
		t.Fatalf("yolo -> plan: mode %s, chip %q", r.mgr.Mode(r.m.sel), r.chipText())
	}
	r.shiftTab()
	if r.mgr.Mode(r.m.sel) != agent.ModeAsk {
		t.Fatalf("plan -> ask, got %s", r.mgr.Mode(r.m.sel))
	}
	want := []agent.Mode{agent.ModeYolo, agent.ModePlan, agent.ModeAsk}
	if got := run.modeCalls(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("the run was told %v, want %v", got, want)
	}
	for _, line := range []string{
		"▲ yolo on — Deck approves everything in this thread",
		"◇ plan — agents investigate and propose; edits and commands are declined",
		"● ask — agents ask before they edit or run anything",
	} {
		if !r.has(line) {
			t.Errorf("transcript is missing %q:\n%s", line, r.view())
		}
	}
}

func TestShiftTabWorksFromTheTranscriptAndWithADraft(t *testing.T) {
	r := idleClaude(t, 120, 30)
	r.typeText("half a thought")
	r.key("tab") // transcript focus
	r.shiftTab()
	if r.mgr.Mode(r.m.sel) != agent.ModeYolo {
		t.Fatal("shift+tab cycles from the transcript")
	}
	if r.m.comp.Value() != "half a thought" {
		t.Fatalf("the draft must survive: %q", r.m.comp.Value())
	}
}

func TestShiftTabDoesNothingInsideAMenuOrPicker(t *testing.T) {
	r := idleClaude(t, 120, 30)
	r.typeText("@")
	if !r.m.menu.open() {
		t.Fatal("the @ menu should be open")
	}
	r.shiftTab()
	if r.mgr.Mode(r.m.sel) != agent.ModeAsk {
		t.Fatal("shift+tab inside a menu must not switch the mode")
	}
	r.key("esc")
	r.m.comp.Reset()

	r.typeText("/model")
	r.key("enter", "enter")
	r.pump("picker", func() bool { return r.m.pick.open && !r.m.pick.loading })
	r.shiftTab()
	if r.mgr.Mode(r.m.sel) != agent.ModeAsk {
		t.Fatal("shift+tab inside the picker must not switch the mode")
	}
}

func TestClickOnModeChipCycles(t *testing.T) {
	r := idleClaude(t, 140, 40)
	r.clickChip(hitMode, "")
	if r.mgr.Mode(r.m.sel) != agent.ModeYolo {
		t.Fatalf("a click cycles ask -> yolo, got %s", r.mgr.Mode(r.m.sel))
	}
	r.clickChip(hitMode, "")
	if r.mgr.Mode(r.m.sel) != agent.ModePlan {
		t.Fatalf("yolo -> plan, got %s", r.mgr.Mode(r.m.sel))
	}
	// the chip is at the right edge of the main area
	_, hits := r.m.chipLayout(r.m.g.mainW)
	last := hits[len(hits)-1]
	if last.kind != hitMode || last.x1 != r.m.g.mainW {
		t.Fatalf("the mode chip belongs at the right edge: %+v (main width %d)", last, r.m.g.mainW)
	}
	for i := 1; i < len(hits); i++ {
		if hits[i].x0 < hits[i-1].x1 {
			t.Fatalf("chip spans overlap: %+v", hits)
		}
	}
}

func TestChipSpansMatchWhatIsDrawn(t *testing.T) {
	r := newRigWith(t, 140, 40, 0, func(c *config.Config) { c.Models = config.Models{Claude: "sonnet", Codex: "gpt-6-sol"} })
	r.duo()
	line, hits := r.m.chipLayout(r.m.g.mainW)
	plain := []rune(ansiStrip(line))
	text := func(h chipHit) string { return strings.TrimSpace(string(plain[h.x0:min(h.x1, len(plain))])) }
	got := []string{}
	for _, h := range hits {
		got = append(got, text(h))
	}
	spin := motion.NewSpinner().Frame(r.m.now) // both are at work: a spinner leads each chip
	want := []string{spin + " ✻ claude · sonnet", spin + " >_ codex · gpt-6-sol", "● ask"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("spans %q, want %q", got, want)
	}
}

func TestMouseOnTheChipLineOutsideChipsFocusesTheComposer(t *testing.T) {
	r := idleClaude(t, 140, 40)
	r.key("tab")
	r.click(r.m.g.mainX+60, r.m.g.chipY)
	if r.m.focus != focusComposer {
		t.Fatal("a click on empty chip line space falls through to the composer")
	}
}

// ---- yolo fight ----

func TestYoloFightPlaysThenFlipsTheChip(t *testing.T) {
	r := idleClaude(t, 140, 40)
	withMotion(t)
	now := r.frozen(t0)

	r.upd(tea.KeyMsg{Type: tea.KeyShiftTab})
	if r.mgr.Mode(r.m.sel) != agent.ModeYolo {
		t.Fatal("the session switches to yolo at once")
	}
	if !r.m.fight.active {
		t.Fatal("the rainbow should play")
	}
	if got := r.chipText(); !strings.HasSuffix(strings.TrimRight(got, " "), "● ask") {
		t.Fatalf("the chip keeps the old mode during the fight: %q", got)
	}
	if r.has("yolo on") {
		t.Fatal("nothing is announced before the fight ends")
	}
	checkShape(t, r.m.View(), 140, 40)

	r.at(now, t0.Add(1250*time.Millisecond))
	if !r.m.fight.active {
		t.Fatal("still fighting at 1.25s")
	}
	checkShape(t, r.m.View(), 140, 40)
	if !r.m.clock.Active() && !r.motionWanted() {
		t.Fatal("the clock must keep ticking during the fight")
	}

	r.at(now, t0.Add(yolo.Duration+50*time.Millisecond))
	if r.m.fight.active {
		t.Fatal("the fight is over")
	}
	if got := r.chipText(); !strings.HasSuffix(strings.TrimRight(got, " "), "▲ yolo") {
		t.Fatalf("the chip flips to yolo: %q", got)
	}
	if !r.has("▲ yolo on — Deck approves everything in this thread") {
		t.Fatalf("the notice follows the fight:\n%s", r.view())
	}
	if !r.m.modeFlash.Started() {
		t.Fatal("the chip flashes as it flips")
	}
}

func (r *rig) motionWanted() bool {
	fast, lg, amb := r.m.motionState()
	return fast || lg || amb
}

func TestAnyKeySkipsTheFightAndStillTypes(t *testing.T) {
	r := idleClaude(t, 140, 40)
	withMotion(t)
	r.frozen(t0)
	r.upd(tea.KeyMsg{Type: tea.KeyShiftTab})
	if !r.m.fight.active {
		t.Fatal("setup: the fight should be playing")
	}
	r.upd(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if r.m.fight.active {
		t.Fatal("a key skips the fight")
	}
	if r.m.comp.Value() != "x" {
		t.Fatalf("the key still does its job, composer = %q", r.m.comp.Value())
	}
	if got := r.chipText(); !strings.Contains(got, "▲ yolo") {
		t.Fatalf("the chip flips at once: %q", got)
	}
}

func TestShiftTabDuringTheFightLandsYoloFirst(t *testing.T) {
	r := idleClaude(t, 140, 40)
	withMotion(t)
	r.frozen(t0)
	r.upd(tea.KeyMsg{Type: tea.KeyShiftTab})
	r.upd(tea.KeyMsg{Type: tea.KeyShiftTab})
	if r.mgr.Mode(r.m.sel) != agent.ModePlan || r.m.fight.active {
		t.Fatalf("the second press skips the fight and moves on: mode %s", r.mgr.Mode(r.m.sel))
	}
	v := r.view()
	if !(strings.Index(v, "yolo on") >= 0 && strings.Index(v, "yolo on") < strings.Index(v, "◇ plan —")) {
		t.Fatalf("yolo is announced, then plan:\n%s", v)
	}
}

func TestReducedMotionFlipsAtOnce(t *testing.T) {
	r := idleClaude(t, 140, 40)
	if !motion.Reduced() {
		t.Fatal("the suite runs reduced")
	}
	r.shiftTab()
	if r.m.fight.active {
		t.Fatal("no fight in reduced motion")
	}
	if !strings.HasSuffix(strings.TrimRight(r.chipText(), " "), "▲ yolo") || !r.has("yolo on") {
		t.Fatalf("the chip and the notice appear at once:\n%s", r.view())
	}
}

func TestFailedSetModeDoesNotFight(t *testing.T) {
	r := idleClaude(t, 140, 40)
	withMotion(t)
	r.frozen(t0)
	cmds := r.m.setMode("no-such-thread", agent.ModeYolo)
	if r.m.fight.active {
		t.Fatal("no fight when the session refuses")
	}
	if len(cmds) == 0 || !r.m.toast.on || !r.m.toast.danger {
		t.Fatalf("a danger toast says why: %+v", r.m.toast)
	}
	if r.mgr.Mode(r.m.sel) != agent.ModeAsk {
		t.Fatal("the mode did not change")
	}
}

func TestThreadSwitchEndsTheFight(t *testing.T) {
	r := idleClaude(t, 140, 40)
	first := r.m.sel
	r.key("tab")
	r.key("n") // a new empty thread
	second := r.m.sel
	if second == first {
		t.Fatal("setup: a second thread")
	}
	r.m.selectThread(first)
	withMotion(t)
	r.frozen(t0)
	r.upd(tea.KeyMsg{Type: tea.KeyShiftTab})
	if !r.m.fight.active {
		t.Fatal("setup: fight")
	}
	r.m.selectThread(second)
	if r.m.fight.active {
		t.Fatal("leaving the thread ends its fight")
	}
	if r.mgr.Mode(first) != agent.ModeYolo || r.m.noted[first] != agent.ModeYolo {
		t.Fatal("the fight landed in the thread it was played for")
	}
}

// ---- yolo state ----

func TestYoloMarksTheRuleAndTheSidebar(t *testing.T) {
	withColour(t, true)
	r := idleClaude(t, 140, 40)
	rule := func() string { return strings.Split(r.m.View(), "\n")[r.m.g.ruleY] }
	before := rule()
	r.shiftTab()
	after := rule()
	if before == after {
		t.Fatal("the rule changes colour in yolo")
	}
	if ansiStrip(before) != ansiStrip(after) {
		t.Fatal("only the colour changes")
	}
	side := ansiStrip(strings.Join(r.m.sidebarLines(r.m.g, 6), "\n"))
	if !strings.Contains(side, "○▲") {
		t.Fatalf("the yolo thread carries a ▲ after its status glyph:\n%s", side)
	}
	r.shiftTab() // plan
	if side := ansiStrip(strings.Join(r.m.sidebarLines(r.m.g, 6), "\n")); strings.Contains(side, "▲") {
		t.Fatalf("no mark outside yolo:\n%s", side)
	}
	if !strings.Contains(r.m.modeChipFor(agent.ModeYolo), "38;2;240;113;105") { // Danger dark #F0716A
		t.Fatal("the yolo chip is Danger")
	}
}

func (m *model) modeChipFor(mode agent.Mode) string {
	was := m.fight
	m.fight = fightState{anim: was.anim, active: true, thread: m.sel, from: mode}
	defer func() { m.fight = was }()
	return m.modeChip()
}

func TestAutoApprovedAndDeclinedLinesSitInTheSpeakersBlock(t *testing.T) {
	r := idleClaude(t, 140, 40)
	r.feed(session.AutoApprovedMsg{ThreadID: r.m.sel, AgentID: "claude", Summary: "rm -rf dist"})
	r.feed(session.AutoDeniedMsg{ThreadID: r.m.sel, AgentID: "claude", Summary: "internal/store/store.go"})
	if !r.has("  ✓ auto-approved  rm -rf dist") {
		t.Fatalf("auto-approved line:\n%s", r.view())
	}
	if !r.has("  ⊘ declined (plan)  internal/store/store.go") {
		t.Fatalf("declined line:\n%s", r.view())
	}
	if n := r.countLines("✻ claude"); n != 1 {
		t.Fatalf("the lines join claude's block, found %d labels", n)
	}
	for i := 1; i < len(r.tv().lines); i++ {
		if r.tv().lines[i] == "" && r.tv().lines[i-1] == "" {
			t.Fatal("no double blank lines")
		}
	}
}

func TestModeChangedMsgFromOutsideIsAnnouncedOnce(t *testing.T) {
	r := idleClaude(t, 140, 40)
	if err := r.mgr.SetMode(r.m.sel, agent.ModePlan); err != nil {
		t.Fatal(err)
	}
	r.pump("plan notice", func() bool { return r.has("◇ plan —") })
	r.shiftTab() // ask
	r.shiftTab() // yolo
	r.pump("yolo", func() bool { return r.has("yolo on") })
	r.drain() // the stale ModeChangedMsgs arrive
	for _, want := range []string{"◇ plan —", "yolo on"} {
		if n := strings.Count(r.view(), want); n != 1 {
			t.Fatalf("%q announced %d times:\n%s", want, n, r.view())
		}
	}
}

func TestGlobalNoticeIsOneWarnToast(t *testing.T) {
	r := idleClaude(t, 140, 40)
	n := session.NoticeMsg{Text: "default_mode yolo ignored at startup; press shift+tab to switch"}
	r.feed(n)
	if !r.m.toast.on || !r.m.toast.warn || r.m.toast.danger || r.m.toast.text != n.Text {
		t.Fatalf("toast = %+v", r.m.toast)
	}
	if r.countLines("default_mode") != 0 {
		t.Fatal("a global notice is not a transcript line")
	}
	r.m.toast.on = false
	r.feed(n)
	if r.m.toast.on {
		t.Fatal("shown once")
	}
}

// ---- model picker ----

func TestModelChipClickOpensThePickerAndSelectingSwitchesTheModel(t *testing.T) {
	r := idleClaude(t, 140, 40)
	run := r.claude.last()
	r.clickChip(hitModel, "claude")
	r.pump("models", func() bool { return r.m.pick.open && !r.m.pick.loading })
	v := r.view()
	for _, want := range []string{"opus", "sonnet", "haiku", "Most capable, for complex work", "default"} {
		if !strings.Contains(v, want) {
			t.Errorf("picker is missing %q:\n%s", want, v)
		}
	}
	if !strings.Contains(r.chipText(), "model for ✻ claude") {
		t.Fatalf("chip line while picking: %q", r.chipText())
	}
	r.typeText("son") // fuzzy filter
	if len(r.m.pick.items) == 0 || r.m.pick.items[0].label != "sonnet" {
		t.Fatalf("filter: %+v", r.m.pick.items)
	}
	r.key("enter")
	if r.m.pick.stage != pickEffort || !r.has("Effort for Sonnet") || !r.has("Deeper reasoning for complex work") {
		t.Fatalf("a model with efforts asks which one:\n%s", r.view())
	}
	if it := r.m.pick.items[r.m.pick.sel]; it.label != "default" || !it.cur {
		t.Fatalf("the model's own effort is current: %+v", it)
	}
	r.typeText("high")
	r.key("enter")
	if r.m.pick.open {
		t.Fatal("enter picks and closes")
	}
	if got := run.modelCalls(); len(got) != 1 || got[0] != "sonnet" {
		t.Fatalf("SetModel calls = %v", got)
	}
	if got := run.effortCalls(); len(got) != 1 || got[0] != "high" {
		t.Fatalf("SetEffort calls = %v", got)
	}
	r.pump("model notice", func() bool { return r.has("✻ claude now on sonnet · high effort") })
	if r.m.view(r.m.sel).items[len(r.m.view(r.m.sel).items)-1].kind != kModel || strings.Count(r.view(), "now on sonnet") != 1 {
		t.Fatalf("one line for the model and effort:\n%s", r.view())
	}
	if !strings.Contains(r.chipText(), "✻ claude · sonnet · high") {
		t.Fatalf("the chip names the new model and effort: %q", r.chipText())
	}
}

func TestEffortCommandOpensTheCurrentModelsEfforts(t *testing.T) {
	r := newRigWith(t, 140, 40, 0, func(c *config.Config) { c.Models.Claude = "opus"; c.Efforts.Claude = "low" })
	r.start("hi")
	r.done(r.claude.last(), "claude")
	if spec := r.claude.last().spec; spec.Effort != "low" {
		t.Fatalf("the agent starts at the configured effort: %+v", spec)
	}
	r.typeText("/effort")
	r.key("enter", "enter")
	r.pump("efforts", func() bool { return r.m.pick.open && r.m.pick.stage == pickEffort })
	if it := r.m.pick.items[r.m.pick.sel]; it.label != "low" || !it.cur {
		t.Fatalf("low is current: %+v", it)
	}
	r.key("5") // default, low, medium, high, max
	if got := r.claude.last().effortCalls(); len(got) != 1 || got[0] != "max" {
		t.Fatalf("SetEffort calls = %v", got)
	}
	r.typeText("/effort medium")
	r.key("enter")
	if got := r.claude.last().effortCalls(); len(got) != 2 || got[1] != "medium" {
		t.Fatalf("/effort medium: %v", got)
	}
}

func TestCodexEffortsShowItsDefault(t *testing.T) {
	r := newRig(t, 140, 40)
	r.duo()
	r.clickChip(hitModel, "codex")
	r.pump("codex models", func() bool { return r.m.pick.open && r.m.pick.stage == pickModels && !r.m.pick.loading })
	r.key("enter") // gpt-5-codex, the current one
	if r.m.pick.stage != pickEffort {
		t.Fatalf("stage = %v", r.m.pick.stage)
	}
	for _, it := range r.m.pick.items {
		if it.label == "default" {
			t.Fatal("codex names its default level; no separate default row")
		}
	}
	if it := r.m.pick.items[r.m.pick.sel]; it.label != "medium" || it.tag != "default" {
		t.Fatalf("the model's default level is preselected: %+v", it)
	}
}

func TestPickerKeepsTheDraftAndTheCurrentModelIsMarked(t *testing.T) {
	r := newRigWith(t, 140, 40, 0, func(c *config.Config) { c.Models.Claude = "opus" })
	r.start("hi")
	r.done(r.claude.last(), "claude")
	r.typeText("my half-written message")
	r.openViaKey()
	r.pump("models", func() bool { return r.m.pick.open && !r.m.pick.loading })
	cur := -1
	for i, it := range r.m.pick.items {
		if it.cur {
			cur = i
		}
	}
	if cur < 0 || r.m.pick.items[cur].val != "opus" || r.m.pick.sel != cur {
		t.Fatalf("opus is current and pre-selected: sel=%d items=%+v", r.m.pick.sel, r.m.pick.items)
	}
	r.key("esc")
	if r.m.pick.open || r.m.comp.Value() != "my half-written message" {
		t.Fatalf("esc closes and gives the draft back: %q", r.m.comp.Value())
	}
}

// openViaKey opens the picker with alt+m, which the rig's key helper cannot spell.
func (r *rig) openViaKey() {
	if !r.m.pick.open {
		r.feed(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m"), Alt: true})
	}
}

func TestPickerFreeTextRow(t *testing.T) {
	r := idleClaude(t, 140, 40)
	run := r.claude.last()
	r.typeText("/model")
	r.key("enter", "enter") // complete the command name, then run it
	r.pump("picker", func() bool { return r.m.pick.open && !r.m.pick.loading })
	r.typeText("claude-x-9")
	items := r.m.pick.items
	if len(items) == 0 || !items[len(items)-1].free || items[len(items)-1].val != "claude-x-9" {
		t.Fatalf("the last row offers the typed id: %+v", items)
	}
	if !r.has("use claude-x-9") {
		t.Fatalf("the row reads use \"<text>\":\n%s", r.view())
	}
	r.key("enter")
	if got := run.modelCalls(); len(got) != 1 || got[0] != "claude-x-9" {
		t.Fatalf("SetModel calls = %v", got)
	}
}

func TestPickerListError(t *testing.T) {
	r := idleClaude(t, 140, 40)
	r.claude.modelsErr = fmt.Errorf("claude: boom")
	r.typeText("/model")
	r.key("enter", "enter")
	r.pump("error", func() bool { return r.m.pick.open && r.m.pick.loadErr != nil })
	if !r.has("couldn't list models: claude: boom") {
		t.Fatalf("the error is shown:\n%s", r.view())
	}
	r.typeText("my-model")
	if !r.has("use my-model") {
		t.Fatalf("free text still works after an error:\n%s", r.view())
	}
}

func TestPickerShowsLoadingRow(t *testing.T) {
	r := idleClaude(t, 140, 40)
	r.m.openPicker("")
	if !r.m.pick.loading || !r.has("loading models from claude…") {
		t.Fatalf("loading row missing:\n%s", r.view())
	}
}

func TestSetModelErrorToasts(t *testing.T) {
	r := newRig(t, 140, 40)
	r.claude.plain = true // a run that cannot switch anything
	r.start("hi")
	r.done(r.claude.last(), "claude")
	r.typeText("/model")
	r.key("enter", "enter")
	r.pump("picker", func() bool { return r.m.pick.open && !r.m.pick.loading })
	r.typeText("opus")
	r.key("enter", "enter") // the model, then its effort
	if !r.m.toast.on || !r.m.toast.danger || !strings.Contains(r.m.toast.text, "can't switch model mid-session") {
		t.Fatalf("toast = %+v", r.m.toast)
	}
}

func TestDuoPickerAsksWhichMemberThenListsItsModels(t *testing.T) {
	r := newRigWith(t, 140, 40, 0, func(c *config.Config) { c.Models = config.Models{Claude: "sonnet", Codex: "gpt-6-sol"} })
	r.duo()
	r.typeText("/model")
	r.key("enter", "enter")
	if r.m.pick.stage != pickMembers || len(r.m.pick.items) != 2 {
		t.Fatalf("a duo thread asks who first: %+v", r.m.pick)
	}
	if !r.has("✻ claude  sonnet") || !r.has(">_ codex  gpt-6-sol") {
		t.Fatalf("the member rows:\n%s", r.view())
	}
	r.key("down", "enter") // codex
	r.pump("codex models", func() bool { return r.m.pick.stage == pickModels && !r.m.pick.loading })
	if r.m.pick.kind != "codex" || !r.has("gpt-5-codex-mini") {
		t.Fatalf("codex's models, not claude's:\n%s", r.view())
	}
	r.typeText("mini")
	r.key("enter", "enter") // the model, then its effort
	if got := r.codex.last().modelCalls(); len(got) != 1 || got[0] != "gpt-5-codex-mini" {
		t.Fatalf("codex SetModel = %v", got)
	}
	if len(r.claude.last().modelCalls()) != 0 {
		t.Fatal("claude is untouched")
	}
}

func TestClickingAMemberChipSkipsTheChooser(t *testing.T) {
	r := newRig(t, 140, 40)
	r.duo()
	r.clickChip(hitModel, "codex")
	r.pump("codex models", func() bool { return r.m.pick.open && r.m.pick.stage == pickModels && !r.m.pick.loading })
	if r.m.pick.kind != "codex" || r.m.pick.agentID != "codex" {
		t.Fatalf("picker = %+v", r.m.pick)
	}
}

func TestPickerWithoutAMemberSetsTheNextAgentsModel(t *testing.T) {
	r := newRig(t, 120, 30)
	r.clickChip(hitModel, "")
	r.pump("models", func() bool { return r.m.pick.open && !r.m.pick.loading })
	r.typeText("opus")
	r.key("enter", "enter") // the model, then its effort
	if r.m.newModel["claude"] != "opus" {
		t.Fatalf("newModel = %v", r.m.newModel)
	}
	if !strings.Contains(r.chipText(), "claude · opus") {
		t.Fatalf("the chip shows it: %q", r.chipText())
	}
	r.typeText("go")
	r.key("enter")
	r.pump("claude", func() bool { return len(r.m.curThread().Members) == 1 })
	if spec := r.claude.last().spec; spec.Model != "opus" {
		t.Fatalf("the agent starts on the picked model: %+v", spec)
	}
}

func TestSlashModelWithAKindOpensThatKindsPicker(t *testing.T) {
	r := newRig(t, 120, 30)
	r.typeText("/model codex")
	r.key("enter")
	r.pump("codex models", func() bool { return r.m.pick.open && r.m.pick.kind == "codex" && !r.m.pick.loading })
	if !r.has("gpt-5-codex") {
		t.Fatalf("codex models:\n%s", r.view())
	}
}

func TestPickerClickOnARow(t *testing.T) {
	r := idleClaude(t, 140, 40)
	run := r.claude.last()
	r.typeText("/model")
	r.key("enter", "enter")
	r.pump("models", func() bool { return r.m.pick.open && !r.m.pick.loading })
	lines := r.lines()
	row := -1
	for i, l := range lines {
		if strings.Contains(l, "haiku") {
			row = i
		}
	}
	if row < 0 {
		t.Fatalf("no haiku row:\n%s", r.view())
	}
	r.click(r.m.g.mainX+5, row)
	if got := run.modelCalls(); len(got) != 1 || got[0] != "haiku" {
		t.Fatalf("SetModel = %v (row %d)\n%s", got, row, r.view())
	}
}

// ---- layout ----

func TestChipLineStaysOneLineAtEveryWidth(t *testing.T) {
	for _, w := range []int{30, 40, 60, 80, 100, 140} {
		r := newRig(t, w, 24)
		r.duo()
		for _, text := range []string{"", "@codex a long message to see the hint"} {
			r.m.comp.Reset()
			r.typeText(text)
			line, hits := r.m.chipLayout(r.m.g.mainW)
			if sw(line) > r.m.g.mainW {
				t.Errorf("w=%d chip %d wide, max %d", w, sw(line), r.m.g.mainW)
			}
			if !strings.Contains(ansiStrip(line), "ask") {
				t.Errorf("w=%d the mode chip is always there: %q", w, ansiStrip(line))
			}
			for _, h := range hits {
				if h.x0 < 0 || h.x1 > r.m.g.mainW || h.x0 >= h.x1 {
					t.Errorf("w=%d bad span %+v", w, h)
				}
			}
		}
		checkShape(t, r.m.View(), w, 24)
	}
}

func TestFightGeometryNeverOverflows(t *testing.T) {
	for _, sz := range []struct{ w, h int }{{80, 24}, {140, 40}, {40, 12}, {30, 8}} {
		motion.SetReduced(true) // build each rig still, as the rest of the suite does
		r := idleClaude(t, sz.w, sz.h)
		withMotion(t)
		now := r.frozen(t0)
		r.upd(tea.KeyMsg{Type: tea.KeyShiftTab})
		for _, ms := range []int{0, 300, 1000, 1250, 1500, 2000, 2100, 2150, 2190} {
			r.at(now, t0.Add(time.Duration(ms)*time.Millisecond))
			checkShape(t, r.m.View(), sz.w, sz.h)
			if r.m.g.trH < 1 {
				t.Fatalf("%dx%d at %dms: transcript squeezed out", sz.w, sz.h, ms)
			}
		}
	}
}

func TestFightOnTheEmptyScreenCoversInsteadOfGrowing(t *testing.T) {
	r := newRig(t, 120, 30)
	withMotion(t)
	now := r.frozen(t0)
	r.upd(tea.KeyMsg{Type: tea.KeyShiftTab})
	if !r.m.fight.active {
		t.Fatal("the fight plays on the empty screen too")
	}
	trH := r.m.g.trH
	r.at(now, t0.Add(1250*time.Millisecond))
	if r.m.g.trH != trH {
		t.Fatal("the empty screen does not reflow")
	}
	checkShape(t, r.m.View(), 120, 30)
}
