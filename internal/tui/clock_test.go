package tui

import "time"

// manualClock is the key clock of the tests. Every reading moves it on by step,
// so keys fed one after another look like typing, not like a paste; a test that
// wants a burst sets step to 0 and moves the clock itself.
type manualClock struct {
	t    time.Time
	step time.Duration
}

func (c *manualClock) Now() time.Time {
	c.t = c.t.Add(c.step)
	return c.t
}

func (c *manualClock) advance(d time.Duration) { c.t = c.t.Add(d) }
