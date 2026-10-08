package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dev-dami/deck/internal/tui/motion"
)

// The yolo blast reaches the screen: switching with shift+tab on the empty
// screen and in a thread draws the big YOLO a moment later, on the real clock.
func TestYoloBlastIsDrawn(t *testing.T) {
	for _, name := range []string{"empty", "idle"} {
		motion.SetReduced(true) // build the rig still, as the rest of the suite does
		var r *rig
		if name == "empty" {
			r = newRig(t, 100, 30)
		} else {
			r = idleClaude(t, 100, 30)
		}
		withMotion(t)
		for i := 0; i < 3 && r.m.mgr.Mode(r.m.sel) != "yolo"; i++ {
			r.m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
		}
		time.Sleep(300 * time.Millisecond)
		r.m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
		if n := strings.Count(ansi.Strip(r.m.View()), "█"); n < 40 {
			t.Fatalf("%s: the yolo blast is not on screen (%d letter cells):\n%s", name, n, ansi.Strip(r.m.View()))
		}
	}
}
