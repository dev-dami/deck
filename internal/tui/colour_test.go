package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/dev-dami/deck/internal/agent"
)

func withColour(t *testing.T, dark bool) {
	t.Helper()
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(dark)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(termenv.Ascii)
		lipgloss.SetHasDarkBackground(true)
	})
}

// diffRig returns a rig whose selected agent shows an expanded diff.
func diffRig(t *testing.T) *rig {
	r := newRig(t, 120, 40)
	r.start("go")
	run := r.claude.last()
	r.event(run, agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{ID: "e1", Name: "Edit", Title: "internal/store/store.go", Status: agent.ToolRunning}})
	r.event(run, agent.Event{Kind: agent.KindFileChange, Diff: demoDiff()})
	r.event(run, agent.Event{Kind: agent.KindToolResult, Tool: &agent.Tool{ID: "e1", Name: "Edit", Status: agent.ToolOK}})
	r.event(run, agent.Event{Kind: agent.KindTurnDone})
	r.pump("edit row", func() bool { return r.has("Edit internal/store/store.go +24 −9") })
	r.key("tab", "enter")
	return r
}

func TestDiffColoursDark(t *testing.T) {
	withColour(t, true)
	r := diffRig(t)
	v := r.m.View()
	// AddBg #1C2A1C and DelBg #2E1C1B, as 24-bit backgrounds
	if !strings.Contains(v, "48;2;28;42;28") {
		t.Error("added lines should sit on AddBg")
	}
	if !strings.Contains(v, "48;2;46;28;27") {
		t.Error("deleted lines should sit on DelBg")
	}
	checkShape(t, v, 120, 40)
}

func TestDiffColoursLight(t *testing.T) {
	withColour(t, false)
	r := diffRig(t)
	v := r.m.View()
	// light AddBg #EAF4E6, DelBg #FBEAE8 (termenv rounds 244 to 243)
	if !strings.Contains(v, "48;2;234;243;230") || !strings.Contains(v, "48;2;251;234;232") {
		t.Error("light theme should use the light diff backgrounds")
	}
	checkShape(t, v, 120, 40)
}

func TestApprovalCardUsesWarnColour(t *testing.T) {
	withColour(t, true)
	r := newRig(t, 120, 30)
	r.start("go")
	r.approval(r.claude.last(), "w1", "make deploy", agent.ApproveCommand)
	r.pump("card", func() bool { return len(r.m.pending) == 1 })
	v := r.m.View()
	if !strings.Contains(v, "38;2;232;176;75") { // Warn dark #E8B04B
		t.Error("the approval card should use the Warn colour")
	}
	checkShape(t, v, 120, 30)
}
