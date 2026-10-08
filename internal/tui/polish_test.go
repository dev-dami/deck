package tui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/agent"
)

func TestEmptyStateHeaderHasNoDuplicateWordmark(t *testing.T) {
	r := newRig(t, 100, 30)
	lines := strings.Split(r.view(), "\n")
	if strings.Contains(lines[0], "deck") {
		t.Fatalf("header must not repeat the wordmark in the empty state: %q", lines[0])
	}
	if strings.Count(r.view(), "◆ deck") != 1 {
		t.Fatalf("exactly one wordmark expected:\n%s", r.view())
	}
	r.start("hi")
	if !strings.Contains(strings.Split(r.view(), "\n")[0], "◆ deck") {
		t.Fatal("header wordmark returns once an agent exists")
	}
}

func TestTranscriptIsBottomAnchored(t *testing.T) {
	r := newRig(t, 100, 30)
	r.start("hello")
	r.event(r.claude.last(), agent.Event{Kind: agent.KindAssistantText, Text: "Short answer."})
	r.pump("text", func() bool { return r.has("Short answer.") })
	lines := strings.Split(r.view(), "\n")
	rule := r.m.g.ruleY
	if !strings.Contains(lines[rule-1], "Short answer.") {
		t.Fatalf("latest message should sit right above the rule, got %q above %q", lines[rule-1], lines[rule])
	}
	// the first rows of the pane are blank padding
	if strings.TrimSpace(lines[r.m.g.topY+1][r.m.g.mainX:]) != "" {
		t.Fatalf("padding expected above the transcript: %q", lines[r.m.g.topY+1])
	}
}

func TestNoDoubleBlankLinesInTranscript(t *testing.T) {
	r := newRig(t, 100, 40)
	r.start("go")
	run := r.claude.last()
	r.event(run, agent.Event{Kind: agent.KindAssistantText, Text: "One.\n\nTwo.\n\n\n\nThree."})
	r.event(run, agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{ID: "a", Name: "Read", Title: "a.go", Status: agent.ToolRunning}})
	r.event(run, agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{ID: "b", Name: "Read", Title: "b.go", Status: agent.ToolRunning}})
	r.event(run, agent.Event{Kind: agent.KindAssistantText, Text: "After tools."})
	r.approval(run, "x", "ls", agent.ApproveCommand)
	r.pump("all", func() bool { return len(r.m.pending) == 1 && r.has("After tools.") })
	v := r.m.views["claude"]
	for i := 1; i < len(v.lines); i++ {
		if v.lines[i] == "" && v.lines[i-1] == "" {
			t.Fatalf("two blank lines at %d:\n%s", i, strings.Join(v.lines, "\n"))
		}
	}
	got := ansiStrip(strings.Join(v.lines, "\n"))
	if !strings.Contains(got, "▸ Read a.go\n  ◐") && !regexp.MustCompile(`Read a\.go\n\s+. Read b\.go`).MatchString(got) {
		t.Fatalf("consecutive tool rows must touch:\n%s", got)
	}
	if !regexp.MustCompile(`Three\.\n\s+. Read a\.go`).MatchString(got) {
		t.Fatalf("tool rows sit directly under their message:\n%s", got)
	}
}

func TestMarkdownStyle(t *testing.T) {
	md := newMD(true)
	ls := md.lines("# Plan\n\nUse `Append` here.\n\n- one\n- two\n\n```go\nfunc main() {}\n```\n\nDone.", 60)
	got := ansiStrip(strings.Join(ls, "\n"))
	for _, want := range []string{"Plan\n", "Use Append here.", "• one", "│ func main() {}", "Done."} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "#") || strings.Contains(got, "Use  Append") || strings.Contains(got, "Append  here") {
		t.Errorf("no heading marks or code padding expected:\n%s", got)
	}
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "  ") {
			t.Errorf("document margin must be 0, got %q", l)
		}
	}
}

func TestMarkdownColoursInlineCodeAccent(t *testing.T) {
	withColour(t, true)
	md := newMD(true)
	out := strings.Join(md.lines("Use `Append` now.", 60), "\n")
	if !strings.Contains(out, "38;2;95;215;175mAppend") { // Accent dark #5FD7AF
		t.Errorf("inline code should be Accent with nothing around it: %q", out)
	}
	if strings.Contains(out, "48;") {
		t.Errorf("inline code must have no background: %q", out)
	}
}

func TestSplitFences(t *testing.T) {
	segs := splitFences("a\n```go\nx\n```\nb\n```\nunclosed")
	if len(segs) != 4 || !segs[1].code || segs[1].lang != "go" || segs[1].text != "x" || segs[2].text != "b" || !segs[3].code || segs[3].text != "unclosed" {
		t.Fatalf("%+v", segs)
	}
}

func TestNarrowSidebarDropsTitleAndOnlySelectedShowsCost(t *testing.T) {
	for _, tc := range []struct {
		w         int
		wantTitle bool
	}{{99, false}, {100, true}} {
		r := newRig(t, tc.w, 30)
		r.start("dtitle")
		r.typeText("@codex second")
		r.key("enter")
		r.pump("codex", func() bool { return r.m.sel == "codex" })
		r.event(r.claude.last(), agent.Event{Kind: agent.KindUsage, Usage: &agent.Usage{CostUSD: 1.5}})
		r.event(r.codex.last(), agent.Event{Kind: agent.KindUsage, Usage: &agent.Usage{CostUSD: 2.5}})
		r.event(r.codex.last(), agent.Event{Kind: agent.KindTurnDone})
		r.event(r.claude.last(), agent.Event{Kind: agent.KindTurnDone})
		r.pump("costs", func() bool {
			return r.mgr.Agents()[0].CostUSD > 1 && r.mgr.Agents()[1].CostUSD > 2 && !r.m.toast.on == false
		})
		r.m.settle()
		side := ansiStrip(strings.Join(r.m.sidebarLines(r.m.g, 4), "\n"))
		if strings.Contains(side, "dtitle") != tc.wantTitle {
			t.Errorf("w=%d title in sidebar = %v, want %v:\n%s", tc.w, !tc.wantTitle, tc.wantTitle, side)
		}
		hasClaudeCost, hasCodexCost := strings.Contains(side, "$1.50"), strings.Contains(side, "$2.50")
		if !hasCodexCost {
			t.Errorf("w=%d selected row must show its cost:\n%s", tc.w, side)
		}
		if hasClaudeCost != tc.wantTitle {
			t.Errorf("w=%d unselected cost shown = %v:\n%s", tc.w, hasClaudeCost, side)
		}
	}
}

func TestDiffGutterHasOldAndNewColumns(t *testing.T) {
	ls := renderDiff(demoDiff(), 70, true, 0)
	var ctx, del, add string
	for _, l := range ls {
		p := ansiStrip(l)
		switch {
		case strings.Contains(p, "│-") && del == "":
			del = p
		case strings.Contains(p, "│+") && add == "":
			add = p
		case strings.Contains(p, "│ ") && strings.Contains(p, "type Store") && ctx == "":
			ctx = p
		}
	}
	re := regexp.MustCompile(`^\s*(\d+)\s+(\d+)\s+│`)
	if m := re.FindStringSubmatch(ctx); m == nil {
		t.Errorf("context line needs both numbers: %q", ctx)
	}
	if !regexp.MustCompile(`^\s*\d+\s+│-`).MatchString(del) {
		t.Errorf("deleted line shows only the old number: %q", del)
	}
	if !regexp.MustCompile(`^\s{4,}\d+\s+│\+`).MatchString(add) || regexp.MustCompile(`^\s*\d+\s+\d+`).MatchString(add) {
		t.Errorf("added line shows only the new number: %q", add)
	}
}
