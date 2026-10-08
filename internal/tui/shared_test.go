package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/tui/motion"
)

func say(run *stubRun, r *rig, text string) {
	r.event(run, agent.Event{Kind: agent.KindAssistantText, Text: text})
}

// count counts the lines of the transcript that start with s (colours stripped).
func (r *rig) countLines(prefix string) int {
	n := 0
	for _, l := range r.tv().lines {
		if strings.HasPrefix(ansiStrip(l), prefix) {
			n++
		}
	}
	return n
}

func TestRelayLineFormatAndHangingIndent(t *testing.T) {
	r := newRig(t, 140, 40)
	r.duo()
	r.relay("claude", "codex", "can you review this plan?")
	if !r.has(`  ↪ claude → codex  "can you review this plan?"`) {
		t.Fatalf("relay line:\n%s", r.view())
	}

	long := "Please review this plan for the store change and tell me what you would change before I start, in particular the lock order between the per-thread lock and the index lock."
	r.relay("codex", "claude", long)
	lines := make([]string, len(r.tv().lines))
	for i, l := range r.tv().lines {
		lines[i] = ansiStrip(l)
	}
	found := false
	for i, l := range lines {
		if !strings.Contains(l, "↪ codex → claude") {
			continue
		}
		found = true
		quoteAt := len([]rune(l[:strings.Index(l, `"`)]))
		next := []rune(lines[i+1])
		if len(next) <= quoteAt || strings.TrimSpace(string(next[:quoteAt])) != "" || next[quoteAt] == ' ' {
			t.Fatalf("the quote must wrap under itself (col %d):\n%s\n%s", quoteAt, l, lines[i+1])
		}
	}
	if !found {
		t.Fatalf("second relay missing:\n%s", r.view())
	}
}

func TestRelayFoldsLongTextAndExpands(t *testing.T) {
	r := newRig(t, 120, 40)
	r.duo()
	text := "Plan:\n- drop streaming deltas before disk\n- one lock per thread\n- test 1000 deltas\n- document the store\n- bench it\n- ship it"
	r.relay("claude", "codex", text)
	if r.has("ship it") || !r.has("… +") {
		t.Fatalf("a long relay folds to %d lines:\n%s", relayCap, r.view())
	}
	var relay *item
	for _, it := range r.tv().items {
		if it.kind == kRelay {
			relay = it
		}
	}
	if !relay.foldable() {
		t.Fatal("a folded relay can be unfolded")
	}
	r.key("tab") // the transcript owns the keys; the cursor starts on the last foldable row
	r.tv().cursor = relay
	r.key("enter")
	if !r.has("ship it") {
		t.Fatalf("enter unfolds the relay:\n%s", r.view())
	}
}

func TestHeldRelayIsDimmedAndTagged(t *testing.T) {
	r := newRig(t, 140, 40)
	r.duo()
	r.feed(session.RelayMsg{ThreadID: r.m.sel, From: "codex", To: []string{"claude"}, Text: "over the limit", Held: true, Via: "tool"})
	line := r.find("↪ codex → claude")
	if !strings.Contains(line, "held") || !strings.Contains(line, `"over the limit"`) {
		t.Fatalf("held relay: %q", line)
	}
	r.feed(session.ContinuedMsg{ThreadID: r.m.sel})
	if strings.Contains(r.find("↪ codex → claude"), "held") {
		t.Fatal("a continued relay is no longer held")
	}
}

func TestMessageAndReadToolsAreCompacted(t *testing.T) {
	r := newRig(t, 140, 40)
	r.duo()
	run := r.claude.last()
	r.event(run, agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{ID: "m1", Name: "message", Title: "to codex: look at this", Status: agent.ToolRunning}})
	r.event(run, agent.Event{Kind: agent.KindToolResult, Tool: &agent.Tool{ID: "m1", Name: "message", Title: "to codex: look at this", Output: "delivered to codex", Status: agent.ToolOK}})
	r.event(run, agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{ID: "m2", Name: "send_message", Title: "to codex: again", Status: agent.ToolRunning}})
	r.event(run, agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{ID: "r1", Name: "read", Title: "shared thread", Status: agent.ToolRunning}})
	r.event(run, agent.Event{Kind: agent.KindToolResult, Tool: &agent.Tool{ID: "r1", Name: "read", Title: "shared thread", Output: "the whole thread", Status: agent.ToolOK}})
	r.event(run, agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{ID: "b1", Name: "Bash", Title: "go vet", Status: agent.ToolRunning}})
	r.pump("rows", func() bool { return r.has("Bash go vet") })
	if r.has("to codex") || r.has("send_message") || r.has("the whole thread") {
		t.Fatalf("the relay line says what the message tool did; no tool row:\n%s", r.view())
	}
	if !r.has("∴ read the thread") {
		t.Fatalf("reading the thread is a one-liner:\n%s", r.view())
	}
	for _, it := range r.tv().items {
		if it.kind == kTool && it.tool.Name != "Bash" {
			t.Fatalf("unexpected tool row %+v", it.tool)
		}
	}

	r.event(run, agent.Event{Kind: agent.KindToolResult, Tool: &agent.Tool{ID: "m2", Name: "send_message", Output: "unknown recipient", Status: agent.ToolError}})
	r.pump("failure", func() bool { return r.has("message not delivered: unknown recipient") })
}

func TestTintedSpeakersAndMergedBlocks(t *testing.T) {
	withColour(t, true)
	r := newRig(t, 140, 40)
	r.duo()
	cl, cx := r.claude.last(), r.codex.last()
	say(cl, r, "I will plan this.")
	r.event(cl, agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{ID: "t1", Name: "Read", Title: "store.go", Status: agent.ToolRunning}})
	r.event(cl, agent.Event{Kind: agent.KindToolResult, Tool: &agent.Tool{ID: "t1", Name: "Read", Output: "ok", Status: agent.ToolOK}})
	// Each agent's events reach the UI on their own goroutine, so order across
	// agents is only what the UI has rendered: let claude's block land first.
	r.pump("claude's block", func() bool { return r.has("I will plan this.") && r.has("Read store.go") })
	say(cx, r, "I will review it.")
	r.pump("both spoke", func() bool { return r.has("I will plan this.") && r.has("I will review it.") })
	r.done(cl, "claude")
	r.done(cx, "codex")

	if n := r.countLines("◆ claude"); n != 1 {
		t.Fatalf("claude's text and tool row are one block, got %d labels", n)
	}
	if n := r.countLines("◆ codex"); n != 1 {
		t.Fatalf("codex labels = %d", n)
	}
	for name, esc := range map[string]string{"claude": "38;2;227;154;111", "codex": "38;2;141;169;240"} { // TintClaude, TintCodex (dark)
		label := ""
		for _, l := range r.tv().lines {
			if ansiStrip(l) == "◆ "+name {
				label = l
			}
		}
		if label == "" {
			t.Fatalf("no %s label", name)
		}
		if !strings.Contains(label, esc+"m◆") {
			t.Errorf("the %s ◆ should be tinted %s: %q", name, esc, label)
		}
		if !strings.Contains(label, esc+"m"+name) || !strings.Contains(label, "1;"+esc) {
			t.Errorf("the %s name should be tinted %s and bold: %q", name, esc, label)
		}
	}
	checkShape(t, r.m.View(), 140, 40)
}

func TestUserMessageNamesItsAddressees(t *testing.T) {
	r := newRig(t, 140, 40)
	r.start("hello")
	if r.has("Ada →") {
		t.Fatalf("a one-member thread shows no addressee:\n%s", r.view())
	}
	r.done(r.claude.last(), "claude")
	r.say("@codex look at this")
	r.pump("codex", func() bool { return len(r.m.curThread().Members) == 2 })
	r.drain()
	if !r.has("Ada → codex") {
		t.Fatalf("the user's message names its addressee:\n%s", r.view())
	}
	r.done(r.codex.last(), "codex")
	r.say("@all status?")
	r.pump("all", func() bool { return r.has("Ada → all") })
}

func TestJoinLine(t *testing.T) {
	r := newRig(t, 140, 40)
	r.duo()
	if !r.has("◆codex joined") {
		t.Fatalf("the second member announces itself:\n%s", r.view())
	}
	if r.has("claude joined") {
		t.Fatal("the first member needs no announcement")
	}
	r.feed(session.NoticeMsg{ThreadID: r.m.sel, Text: "dropped a repeated message from claude to codex"})
	if !r.has("dropped a repeated message from claude to codex") {
		t.Fatalf("notice missing:\n%s", r.view())
	}
}

func pauseRig(t *testing.T, w, h int) *rig {
	t.Helper()
	r := newRigHops(t, w, h, 1)
	r.duo()
	r.done(r.claude.last(), "claude")
	r.relay("claude", "codex", "first")
	r.relay("codex", "claude", "second")
	r.pump("paused", func() bool { return r.m.curThread().Paused && r.has("paused after") })
	return r
}

func TestPauseMarkerAndEnterContinues(t *testing.T) {
	r := pauseRig(t, 140, 40)
	if !r.has("‖ paused after 1 agent message · enter continue · or type to steer") {
		t.Fatalf("pause marker:\n%s", r.view())
	}
	if !strings.Contains(r.find(`"second"`), "held") {
		t.Fatalf("the relay that hit the limit is held:\n%s", r.view())
	}
	if !strings.Contains(r.m.footerHints(), "enter") || !strings.Contains(ansiStrip(r.m.footerHints()), "continue") {
		t.Fatalf("footer: %q", ansiStrip(r.m.footerHints()))
	}
	glyph, _ := r.m.threadGlyph(r.m.curThread())
	if glyph != glyphPause {
		t.Fatalf("a paused thread shows %s in the sidebar, got %s", glyphPause, glyph)
	}

	r.key("enter") // empty composer: continue
	r.pump("continued", func() bool { return r.has("continued") && !r.m.curThread().Paused })
	if r.has("paused after") || strings.Contains(r.find(`"second"`), "held") {
		t.Fatalf("continued replaces the marker and releases the relay:\n%s", r.view())
	}
	r.pump("delivered", func() bool {
		for _, s := range r.claude.last().sentList() {
			if strings.Contains(s, "second") {
				return true
			}
		}
		return false
	})
}

func TestTypingWhilePausedSteersInstead(t *testing.T) {
	r := pauseRig(t, 140, 40)
	r.say("@claude do it this way")
	r.pump("steered", func() bool { return r.has("continued") })
	if r.m.curThread().Paused {
		t.Fatal("a new message ends the pause")
	}
	if !r.has("do it this way") {
		t.Fatalf("the steering message is in the transcript:\n%s", r.view())
	}
}

func TestEnterWithTextDoesNotContinue(t *testing.T) {
	r := pauseRig(t, 140, 40)
	r.typeText("hold on")
	if strings.Contains(ansiStrip(r.m.footerHints()), "enter continue") {
		t.Fatalf("with text typed, enter sends it: %q", ansiStrip(r.m.footerHints()))
	}
}

func TestAddressHintAppearsWhileTyping(t *testing.T) {
	r := newRig(t, 140, 40)
	r.duo()
	r.done(r.claude.last(), "claude")
	say(r.codex.last(), r, "my review")
	r.done(r.codex.last(), "codex")
	r.pump("codex spoke", func() bool { return r.m.curThread().LastSpeaker == "codex" })

	chip := func() string { return ansiStrip(r.m.chipLine(r.m.g.mainW)) }
	if strings.Contains(chip(), "→") {
		t.Fatalf("no hint without text: %q", chip())
	}
	for text, want := range map[string]string{
		"keep going":      "→ codex (last spoke)",
		"@claude look":    "→ claude",
		"@all everybody":  "→ all",
		"@nobody there":   "no agent @nobody",
		"@claude, @codex": "→ claude, codex",
	} {
		r.m.comp.Reset()
		r.typeText(text)
		if got := strings.TrimRight(strings.TrimSuffix(strings.TrimRight(chip(), " "), "● ask"), " "); !strings.HasSuffix(got, want) {
			t.Errorf("typing %q: chip line %q should end with %q (before the mode chip)", text, got, want)
		}
	}
	r.m.comp.Reset()
	if got := chip(); !strings.Contains(got, "◆claude · default  ◆codex · default") || !strings.Contains(got, "~/repo") || !strings.HasSuffix(strings.TrimRight(got, " "), "● ask") {
		t.Fatalf("the chip line lists the members and the directory: %q", got)
	}
}

func TestAtMenuMembersThenAllThenAdd(t *testing.T) {
	labels := func(r *rig) (out []string) {
		for _, it := range r.m.menu.items {
			out = append(out, it.label+"/"+it.desc)
		}
		return out
	}

	// one claude: itself, then codex to add (no @all with a single member)
	r := newRig(t, 140, 40)
	r.start("hello")
	r.typeText("@")
	if got := strings.Join(labels(r), ","); got != "claude/idle,codex/add codex" && got != "claude/working,codex/add codex" {
		t.Fatalf("items = %s", got)
	}
	if !r.m.menu.items[1].isNew {
		t.Fatal("kinds not in the thread are marked as additions")
	}

	// claude and codex: both members, then @all, and nothing left to add
	r = newRig(t, 140, 40)
	r.duo()
	r.typeText("@")
	var names []string
	for _, it := range r.m.menu.items {
		names = append(names, it.label)
	}
	if strings.Join(names, ",") != "claude,codex,all" {
		t.Fatalf("items = %v", names)
	}
	r.key("down", "down", "tab")
	if r.m.comp.Value() != "@all " {
		t.Fatalf("completion = %q", r.m.comp.Value())
	}

	// two claudes: members, @all, and codex to add
	r = newRig(t, 140, 40)
	r.start("hello")
	if _, err := r.mgr.AddMember(r.m.sel, "claude", ""); err != nil {
		t.Fatal(err)
	}
	r.pump("claude2", func() bool { return len(r.m.curThread().Members) == 2 })
	r.typeText("@")
	names = names[:0]
	for _, it := range r.m.menu.items {
		names = append(names, it.label)
	}
	if strings.Join(names, ",") != "claude,claude2,all,codex" {
		t.Fatalf("items = %v", names)
	}
	if last := r.m.menu.items[3]; !last.isNew || last.desc != "add codex" {
		t.Fatalf("last = %+v", last)
	}
}

func TestApprovalCardSitsInItsSpeakersBlock(t *testing.T) {
	r := newRig(t, 140, 40)
	r.duo()
	say(r.claude.last(), r, "Planning first.")
	r.approval(r.codex.last(), "x1", "go test ./...", agent.ApproveCommand)
	r.pump("card", func() bool { return len(r.m.pending) == 1 && r.has("Planning first.") })

	ls := r.tv().lines
	card := -1
	for i, l := range ls {
		if strings.Contains(ansiStrip(l), "codex wants to run") {
			card = i
		}
	}
	if card < 0 {
		t.Fatalf("card missing:\n%s", r.view())
	}
	for i := card - 1; i >= 0; i-- {
		if l := ansiStrip(ls[i]); strings.HasPrefix(l, "◆ ") {
			if l != "◆ codex" {
				t.Fatalf("the card belongs to codex's block, found %q above it", l)
			}
			return
		}
	}
	t.Fatal("no speaker label above the card")
}

func TestHeaderCountsWorkingAgents(t *testing.T) {
	r := newRig(t, 140, 40)
	r.duo()
	r.pump("working", func() bool {
		return status(r, "claude") == session.StatusWorking && status(r, "codex") == session.StatusWorking
	})
	if h := ansiStrip(strings.Split(r.view(), "\n")[0]); !strings.Contains(h, "2 agents working") {
		t.Fatalf("header: %q", h)
	}
	r.done(r.claude.last(), "claude")
	if h := ansiStrip(strings.Split(r.view(), "\n")[0]); !strings.Contains(h, "1 agent working") {
		t.Fatalf("header: %q", h)
	}
}

func TestSidebarThreadRowsAndMemberChips(t *testing.T) {
	r := newRig(t, 140, 40)
	r.duo()
	side := ansiStrip(strings.Join(r.m.sidebarLines(r.m.g, 6), "\n"))
	lines := strings.Split(side, "\n")
	if !strings.Contains(lines[0], "plan it") || !strings.Contains(lines[0], "●") {
		t.Fatalf("row 1 is the thread: %q", lines[0])
	}
	if !strings.Contains(lines[1], "◆claude ◆codex") {
		t.Fatalf("row 2 lists the members: %q", lines[1])
	}
	// a pending approval turns the thread's glyph into ⚠
	r.approval(r.claude.last(), "w1", "ls", agent.ApproveCommand)
	r.pump("card", func() bool { return len(r.m.pending) == 1 })
	if lines = strings.Split(ansiStrip(strings.Join(r.m.sidebarLines(r.m.g, 4), "\n")), "\n"); !strings.Contains(lines[0], "⚠") {
		t.Fatalf("approval glyph missing: %q", lines[0])
	}
}

// ---- the logo ----

func TestEmptyStateLayout(t *testing.T) {
	for _, sz := range []struct{ w, h int }{{80, 24}, {140, 40}} {
		r := newRig(t, sz.w, sz.h)
		p := r.m.plan
		if p.logoH < fullLogoRows || !p.tag || !p.hints {
			t.Fatalf("%dx%d: plan = %+v", sz.w, sz.h, p)
		}
		v := r.view()
		for _, want := range []string{"one deck for all your agents", "type to start claude in ~/repo", "@codex to start with codex instead", "/ for commands"} {
			if !strings.Contains(v, want) {
				t.Errorf("%dx%d: missing %q", sz.w, sz.h, want)
			}
		}
		checkShape(t, r.m.View(), sz.w, sz.h)
	}
	// small terminals get a smaller logo, never a broken one
	for _, sz := range []struct{ w, h int }{{30, 8}, {40, 12}, {60, 16}} {
		r := newRig(t, sz.w, sz.h)
		if r.m.plan.logoH == 0 || brailleCells(r.view())+strings.Count(r.view(), "deck") == 0 {
			t.Errorf("%dx%d draws nothing:\n%s", sz.w, sz.h, r.view())
		}
		checkShape(t, r.m.View(), sz.w, sz.h)
	}
}

func TestLogoFallsBackToPlainWordmark(t *testing.T) {
	r := newRig(t, 120, 30)
	r.m.logo.SetSize(0, 0) // the logo has nothing to draw
	r.m.plan = emptyPlan{}
	lines := r.m.emptyLines(4, 3)
	if !strings.Contains(ansiStrip(strings.Join(lines, "\n")), "◆ deck") {
		t.Fatalf("plain wordmark expected: %q", lines)
	}
}

func TestReducedMotionLogoIsStaticAndNeverTicks(t *testing.T) {
	r := newRig(t, 100, 30) // reduced for the whole test binary
	before := r.view()
	if r.m.wake() != nil {
		t.Fatal("reduced motion must never tick")
	}
	x, y := r.m.logoX+3, r.m.logoY+2
	_, cmd := r.m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionMotion})
	if cmd != nil || r.m.logoMoving && !motion.Reduced() {
		t.Fatal("the pointer must not wake a reduced-motion logo")
	}
	if r.view() != before {
		t.Fatal("the static logo does not change")
	}
}

func logoRig(t *testing.T) *rig {
	t.Helper()
	motion.SetReduced(true) // build the rig quietly; motion goes on below
	r := newRig(t, 100, 30)
	r.m.introTag = motion.NewFade(0, nil) // no tagline fade to confuse the clock
	r.m.logoMoving = false
	motion.SetReduced(false)
	t.Cleanup(func() { motion.SetReduced(true) })
	return r
}

func TestMouseOverLogoWakesClockAndElsewhereDoesNot(t *testing.T) {
	r := logoRig(t)
	if c := r.m.wake(); c != nil {
		t.Fatal("a settled logo must not tick")
	}
	bw, bh := r.m.logo.Bounds()
	if bw == 0 || bh == 0 {
		t.Fatal("no logo")
	}

	// off the logo: nothing
	if _, cmd := r.m.Update(tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionMotion}); cmd != nil || r.m.logoMoving {
		t.Fatal("a pointer away from the logo must not wake anything")
	}
	// over it: the clock wakes
	_, cmd := r.m.Update(tea.MouseMsg{X: r.m.logoX + bw/2, Y: r.m.logoY + bh/2, Action: tea.MouseActionMotion})
	if cmd == nil || !r.m.logoMoving {
		t.Fatalf("a pointer over the logo wakes the clock (cmd=%v moving=%v)", cmd != nil, r.m.logoMoving)
	}
	// a click does too, even after the logo settled
	r.m.logoMoving = false
	r.m.clock.Stop()
	_, cmd = r.m.Update(tea.MouseMsg{X: r.m.logoX + 2, Y: r.m.logoY + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if cmd == nil || !r.m.logoMoving {
		t.Fatal("a click on the logo wakes the clock")
	}
}

func TestStartingTheIntroWakesTheClock(t *testing.T) {
	r := logoRig(t)
	r.m.clock.Stop()
	r.m.startIntro(time.Now())
	if r.m.wake() == nil {
		t.Fatal("the intro needs frames")
	}
}

func TestKeypressCalmsTheLogo(t *testing.T) {
	settleAfter := func(press bool) (moving bool) {
		r := logoRig(t)
		bw, bh := r.m.logo.Bounds()
		r.m.Update(tea.MouseMsg{X: r.m.logoX + bw/2, Y: r.m.logoY + bh/2, Action: tea.MouseActionMotion})
		base := r.m.now
		if press {
			r.m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
		}
		at := base
		for at.Before(base.Add(3500 * time.Millisecond)) {
			at = at.Add(16 * time.Millisecond)
			moving = r.m.logo.Advance(at)
		}
		return moving
	}
	if !settleAfter(false) {
		t.Fatal("without a keypress the shimmer is still awake after 3.5s")
	}
	if settleAfter(true) {
		t.Fatal("a keypress should have calmed the logo by 3.5s")
	}
}

func TestFrameAdvancesLogoOnlyInEmptyState(t *testing.T) {
	r := logoRig(t)
	r.m.startIntro(time.Now())
	r.m.introTag = motion.NewFade(0, nil)
	tick := r.m.wake()
	if tick == nil {
		t.Fatal("no tick")
	}
	_, next := r.m.Update(tick())
	if !r.m.logoMoving || next == nil {
		t.Fatalf("the intro keeps ticking (moving=%v)", r.m.logoMoving)
	}
	if r.m.fps != 30 {
		t.Fatalf("logo-only frames run at 30fps, got %d", r.m.fps)
	}
	motion.SetReduced(true) // (the rig would chase every frame otherwise)
	r.start("hello")        // leaving the empty state stops the logo
	if r.m.logoMoving || r.m.logoStarted {
		t.Fatal("the logo is for the empty state only")
	}
}

func TestMentionRelayReplacesItsLineInTheReply(t *testing.T) {
	r := newRig(t, 140, 40)
	r.duo()
	say(r.codex.last(), r, "Looks fine.\n\n@claude agree on the lock order\nand add the test.\n\n@nobody this one is not routed\n\n```\n@claude in code stays\n```")
	r.pump("text", func() bool { return r.has("Looks fine.") })
	r.feed(session.RelayMsg{ThreadID: r.m.sel, From: "codex", To: []string{"claude"}, Via: "mention", Text: "agree on the lock order\nand add the test."})
	if r.has("@claude agree") {
		t.Fatalf("the relay line stands for the mention line:\n%s", r.view())
	}
	for _, want := range []string{"Looks fine.", "@nobody this one is not routed", "@claude in code stays", `↪ codex → claude  "agree on the lock order`} {
		if !r.has(want) {
			t.Fatalf("missing %q:\n%s", want, r.view())
		}
	}

	// tool-routed relays leave the reply alone
	say(r.claude.last(), r, "@codex see above")
	r.pump("claude text", func() bool { return r.has("@codex see above") })
	r.feed(session.RelayMsg{ThreadID: r.m.sel, From: "claude", To: []string{"codex"}, Via: "tool", Text: "see above"})
	if !r.has("@codex see above") {
		t.Fatalf("tool relays do not touch the reply:\n%s", r.view())
	}

	// a reply that was only the mention disappears, the relay line stays
	say(r.codex.last(), r, "@claude ship it")
	r.pump("codex text", func() bool { return r.has("@claude ship it") })
	r.feed(session.RelayMsg{ThreadID: r.m.sel, From: "codex", To: []string{"claude"}, Via: "mention", Text: "ship it"})
	if r.has("@claude ship it") || !r.has(`"ship it"`) {
		t.Fatalf("only-mention reply:\n%s", r.view())
	}
}

func TestBareMentionKeepsTheTextAndShowsTheHint(t *testing.T) {
	r := newRig(t, 140, 40)
	r.duo()
	r.typeText("@claude")
	r.key("esc") // close the menu
	r.key("enter")
	if r.m.comp.Value() != "@claude " || r.m.focus != focusComposer {
		t.Fatalf("composer = %q focus = %v", r.m.comp.Value(), r.m.focus)
	}
	if got := strings.TrimSuffix(strings.TrimRight(ansiStrip(r.m.chipLine(r.m.g.mainW)), " "), "● ask"); !strings.HasSuffix(strings.TrimRight(got, " "), "→ claude") {
		t.Fatalf("the hint says who it is for: %q", got)
	}
	if !r.m.toast.on || r.m.toast.danger {
		t.Fatalf("toast = %+v", r.m.toast)
	}
	if len(r.claude.last().sentList()) != 0 {
		t.Fatal("nothing is sent")
	}
}
