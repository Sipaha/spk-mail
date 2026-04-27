// Package clock provides a swappable wall clock. By default it returns time.Now;
// browser-mode test code swaps in a fixed value via POST /api/_test/clock so
// "relative time" UI text is deterministic in screenshots.
package clock

import (
	"sync"
	"time"
)

type Clock struct {
	mu    sync.RWMutex
	fixed *time.Time
}

func New() *Clock { return &Clock{} }

func (c *Clock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.fixed != nil {
		return *c.fixed
	}
	return time.Now()
}

func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fixed = &t
}

func (c *Clock) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fixed = nil
}

// Default is a process-wide instance used by simple callers. Production code
// that wants to be testable should accept a *Clock as a dependency rather than
// reaching into Default.
var Default = New()
