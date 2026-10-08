// Package yolo plays the short animation shown above the composer when the
// permission mode is switched to YOLO: two stick figures fight, the cautious
// one ("ask", holding a ? sign) is kicked off the left edge, and the wild one
// ("yolo") celebrates and stamps its name. Then the strip collapses.
//
// Like the logo, the package is pure. It owns no tea.Cmd and no timers: the
// host calls Start, drives Advance from motion.Clock frames, asks Height how
// many rows to reserve above the composer and View for the block. Every
// position is computed from elapsed time, so the result does not depend on
// the frame rate.
//
//	a := yolo.New(dark)
//	a.SetWidth(composerWidth)          // on layout, so Height is right
//	a.Start(now)                       // on switching to yolo
//	moving := a.Advance(msg.At)        // each motion.FrameMsg
//	rows := a.Height(msg.At)           // strip height, 4 then 4->0 at the end
//	block := a.View(width, msg.At)     // exactly rows lines, width cells each
//	a.Skip()                           // any keypress
//	if a.Done(now) { /* flip the mode chip */ }
//
// With motion.Reduced the animation is done as soon as it starts, with
// height 0: the host just flips the chip.
//
// Timeline (ms): 0-300 face off; 300-1000 scuffle; 1000-1400 wind-up, kick,
// ask tumbles off in an arc leaving dust; 1400-1900 victory and the stamp;
// 1900-2200 everything fades to Faint, and over the last 200 the strip
// collapses. Below MinWidth columns a one-row version plays instead.
package yolo

import (
	"math"
	"time"

	"github.com/dev-dami/deck/internal/tui/motion"
)

const (
	// Duration is the total run time, including the collapse.
	Duration = 2200 * time.Millisecond
	// MinWidth is the narrowest width that gets the four-row strip; below it
	// the one-row version plays.
	MinWidth = 40
	// FullHeight is the strip height in rows.
	FullHeight = 4

	fadeAt     = 1900 * time.Millisecond // fade to Faint starts
	fadeSpan   = 250 * time.Millisecond
	collapseAt = Duration - 200*time.Millisecond
)

// Anim is the animation state. Use New. It is not safe for concurrent use.
type Anim struct {
	dark    bool
	width   int
	start   time.Time
	started bool
	skipped bool
}

// New returns an Anim in its finished state: Done, height 0. Call Start to
// play it.
func New(dark bool) *Anim { return &Anim{dark: dark} }

// SetDark switches between the dark and light palette.
func (a *Anim) SetDark(dark bool) { a.dark = dark }

// SetWidth tells the animation how wide the strip will be, so Height knows
// whether the one-row version applies before the first View. View records the
// width it is given, so this only matters for the first Height call.
func (a *Anim) SetWidth(w int) { a.width = w }

// Start begins the animation at now (restarting it if it was playing).
func (a *Anim) Start(now time.Time) {
	a.start, a.started, a.skipped = now, true, false
}

// Skip jumps to the end. The host calls it on any keypress.
func (a *Anim) Skip() {
	if a.started {
		a.skipped = true
	}
}

// Done reports whether the animation has finished, was skipped, never
// started, or motion is reduced.
func (a *Anim) Done(now time.Time) bool {
	return !a.started || a.skipped || motion.Reduced() || a.elapsed(now) >= Duration
}

// Advance steps the animation to now and reports whether it is still moving.
// When it returns false the host stops ticking and flips the mode chip.
func (a *Anim) Advance(now time.Time) (moving bool) { return !a.Done(now) }

// Height is the number of rows the strip needs at now: 4 (or 1 in the compact
// version) while it plays, shrinking to 0 over the last 200ms, and 0 once
// Done. The host animates the composer with it.
func (a *Anim) Height(now time.Time) int {
	if a.Done(now) {
		return 0
	}
	full := a.fullRows()
	el := a.elapsed(now)
	if el < collapseAt {
		return full
	}
	u := float64(el-collapseAt) / float64(Duration-collapseAt)
	h := int(math.Ceil(float64(full)*(1-u) - 1e-9))
	return min(max(h, 0), full)
}

// View renders the strip at now: exactly Height(now) lines of width display
// cells each, joined by newlines; empty when Height is 0 or width <= 0. While
// collapsing it shows the bottom rows, so the figures sink into the ground.
func (a *Anim) View(width int, now time.Time) string {
	if width <= 0 {
		return ""
	}
	a.width = width
	h := a.Height(now)
	if h == 0 {
		return ""
	}
	el := a.elapsed(now)
	t := float64(el) / float64(time.Millisecond)
	cv := newCanvas(width, a.fullRows())
	if a.fullRows() == 1 {
		sceneCompact(cv, t)
	} else {
		sceneFull(cv, t)
	}
	return cv.render(h, clamp01(float64(el-fadeAt)/float64(fadeSpan)), a.dark)
}

func (a *Anim) fullRows() int {
	if a.width > 0 && a.width < MinWidth {
		return 1
	}
	return FullHeight
}

func (a *Anim) elapsed(now time.Time) time.Duration {
	if d := now.Sub(a.start); d > 0 {
		return d
	}
	return 0
}
