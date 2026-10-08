package motion

import (
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// FrameMsg is delivered by the Clock for each animation frame.
type FrameMsg struct {
	ID int
	At time.Time
}

// Clock schedules animation frames. It keeps at most one tick outstanding and
// ticks only while something is moving. Typical use in a model:
//
//	case motion.FrameMsg:
//	    if !m.clock.Accept(msg) { return m, nil }
//	    moving := m.advance(msg.At)   // update springs, report if any moved
//	    m.clock.SetMoving(moving)
//	    return m, m.clock.Tick()
//
// and, whenever something starts moving, `cmds = append(cmds, m.clock.Wake())`.
type Clock struct {
	mu          sync.Mutex
	interval    time.Duration
	id          int
	outstanding bool
	moving      bool
}

// NewClock returns a Clock ticking at fps frames per second (clamped to 1..60).
func NewClock(fps int) *Clock {
	c := &Clock{}
	c.SetFPS(fps)
	return c
}

// SetFPS changes the frame rate (clamped to 1..60). Ambient animations such as
// the spinner do not need 60fps.
func (c *Clock) SetFPS(fps int) {
	if fps < 1 {
		fps = 1
	}
	if fps > 60 {
		fps = 60
	}
	c.mu.Lock()
	c.interval = time.Second / time.Duration(fps)
	c.mu.Unlock()
}

// SetMoving records whether anything is still animating. Call it with the
// result of the frame's updates before Tick.
func (c *Clock) SetMoving(moving bool) {
	c.mu.Lock()
	c.moving = moving
	c.mu.Unlock()
}

// Active reports whether the clock should keep ticking: something is moving
// and motion is not reduced.
func (c *Clock) Active() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.activeLocked()
}

func (c *Clock) activeLocked() bool { return c.moving && !Reduced() }

// Tick returns a command for the next frame, or nil if the clock is inactive
// or a tick is already outstanding.
func (c *Clock) Tick() tea.Cmd {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tickLocked()
}

func (c *Clock) tickLocked() tea.Cmd {
	if !c.activeLocked() || c.outstanding {
		return nil
	}
	c.id++
	c.outstanding = true
	id := c.id
	return tea.Tick(c.interval, func(t time.Time) tea.Msg {
		return FrameMsg{ID: id, At: t}
	})
}

// Wake marks the clock as moving and starts ticking if it is not already.
func (c *Clock) Wake() tea.Cmd {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.moving = true
	return c.tickLocked()
}

// Stop marks the clock idle and invalidates any in-flight tick, so a later
// Wake starts a fresh one and the old FrameMsg is rejected by Accept.
func (c *Clock) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.moving = false
	c.outstanding = false
	c.id++
}

// Accept reports whether msg is the current outstanding tick. Stale or
// duplicate frames return false and must be ignored by the caller.
func (c *Clock) Accept(msg FrameMsg) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.outstanding || msg.ID != c.id {
		return false
	}
	c.outstanding = false
	return true
}
