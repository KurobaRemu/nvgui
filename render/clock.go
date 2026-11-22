package render

import (
	"sync"
	"time"
)

type manualClockImpl struct {
	mu  sync.Mutex
	now time.Time
}

func newManualClock(start time.Time) *ManualClock {
	return &ManualClock{impl: &manualClockImpl{now: start}}
}

func manualClockNow(c *ManualClock) time.Time {
	if c == nil || c.impl == nil {
		return time.Time{}
	}
	c.impl.mu.Lock()
	defer c.impl.mu.Unlock()
	return c.impl.now
}

func manualClockAdvance(c *ManualClock, d time.Duration) time.Time {
	if c == nil || c.impl == nil {
		return time.Time{}
	}
	c.impl.mu.Lock()
	defer c.impl.mu.Unlock()
	if d >= 0 {
		c.impl.now = c.impl.now.Add(d)
	}
	return c.impl.now
}
