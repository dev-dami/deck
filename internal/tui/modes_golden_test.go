package tui

import (
	"testing"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/session"
)

// modesScene is a one-agent thread that has planned something and is idle, the
// backdrop of the mode goldens.
func modesScene(t *testing.T, w, h int) *rig {
	t.Helper()
	r := newRigWith(t, w, h, 0, func(c *config.Config) { c.Models.Claude = "sonnet" })
	r.start("refactor the store")
	run := r.claude.last()
	toolRow(r, run, "r1", "Read", "internal/store/store.go", "148 lines read")
	say(run, r, "I'll check the store layer before changing anything.\n\n- read `store.go` to see the current layout\n- run the package tests")
	r.pump("plan text", func() bool { return r.has("run the package tests") })
	r.drain()
	r.done(run, "claude")
	return r
}

func checkGolden(t *testing.T, r *rig, w, h int) {
	t.Helper()
	v := r.m.View()
	checkShape(t, v, w, h)
	requireGolden(t, v)
}

func TestGoldenModeChip(t *testing.T) {
	for _, mode := range []agent.Mode{agent.ModePlan, agent.ModeAsk, agent.ModeYolo} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			forSizes(t, func(t *testing.T, w, h int) {
				r := modesScene(t, w, h)
				for r.mgr.Mode(r.m.sel) != mode {
					r.shiftTab()
				}
				checkGolden(t, r, w, h)
			})
		})
	}
}

func TestGoldenModelPicker(t *testing.T) {
	forSizes(t, func(t *testing.T, w, h int) {
		r := modesScene(t, w, h)
		r.typeText("/model")
		r.key("enter", "enter")
		r.pump("models", func() bool { return r.m.pick.open && !r.m.pick.loading })
		checkGolden(t, r, w, h)
	})
}

func TestGoldenEffortPicker(t *testing.T) {
	forSizes(t, func(t *testing.T, w, h int) {
		r := modesScene(t, w, h)
		r.typeText("/model")
		r.key("enter", "enter")
		r.pump("models", func() bool { return r.m.pick.open && !r.m.pick.loading })
		r.typeText("opus")
		r.key("enter")
		checkGolden(t, r, w, h)
	})
}

func TestGoldenModelPickerDuo(t *testing.T) {
	forSizes(t, func(t *testing.T, w, h int) {
		r := newRigWith(t, w, h, 0, func(c *config.Config) { c.Models = config.Models{Claude: "sonnet", Codex: "gpt-6-sol"} })
		r.duo()
		r.done(r.claude.last(), "claude")
		r.done(r.codex.last(), "codex")
		r.typeText("/model")
		r.key("enter", "enter")
		checkGolden(t, r, w, h)
	})
}

func TestGoldenAutoApproved(t *testing.T) {
	forSizes(t, func(t *testing.T, w, h int) {
		r := modesScene(t, w, h)
		r.shiftTab() // yolo
		r.feed(session.AutoApprovedMsg{ThreadID: r.m.sel, AgentID: "claude", Summary: "rm -rf dist"})
		r.feed(session.AutoApprovedMsg{ThreadID: r.m.sel, AgentID: "claude", Summary: "go test ./internal/store/..."})
		checkGolden(t, r, w, h)
	})
}

func TestGoldenPlanDeclined(t *testing.T) {
	forSizes(t, func(t *testing.T, w, h int) {
		r := modesScene(t, w, h)
		r.shiftTab()
		r.shiftTab() // plan
		r.feed(session.AutoDeniedMsg{ThreadID: r.m.sel, AgentID: "claude", Summary: "edit internal/store/store.go"})
		checkGolden(t, r, w, h)
	})
}
