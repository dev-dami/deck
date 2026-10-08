package motion

import (
	"time"

	"github.com/dev-dami/deck/internal/tui/theme"
)

// SpinnerFPS is the spinner's frame rate.
const SpinnerFPS = 8

// Spinner picks a frame of theme.SpinnerFrames from the wall clock at ~8fps.
// It has no ticker of its own: the model just needs to re-render (the Clock
// at a low fps, or any other event) while a tool is running.
type Spinner struct {
	Frames []string
	FPS    int
}

// NewSpinner returns the standard tool spinner.
func NewSpinner() Spinner { return Spinner{Frames: theme.SpinnerFrames, FPS: SpinnerFPS} }

// Frame returns the glyph to show at now. In reduced motion it is static.
func (s Spinner) Frame(now time.Time) string {
	if len(s.Frames) == 0 {
		return ""
	}
	if Reduced() || s.FPS <= 0 {
		return s.Frames[0]
	}
	step := int64(time.Second) / int64(s.FPS)
	return s.Frames[int((now.UnixNano()/step)%int64(len(s.Frames)))]
}
