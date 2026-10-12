package runner

import (
	"sort"
	"sync"
	"time"
)

// FakeClock is a manual clock: timers fire only when Advance passes them.
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
	ws  []*waiter
}
type waiter struct {
	at time.Time
	ch chan time.Time
}

func NewFake(t time.Time) *FakeClock { return &FakeClock{now: t} }
func (c *FakeClock) Now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *FakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	w := &waiter{at: c.now.Add(d), ch: make(chan time.Time, 1)}
	if d <= 0 {
		w.ch <- c.now
		return w.ch
	}
	c.ws = append(c.ws, w)
	return w.ch
}
func (c *FakeClock) Waiters() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.ws) }

// BlockUntil waits until n timers are armed (the runner is idle and waiting).
func (c *FakeClock) BlockUntil(n int) {
	for i := 0; i < 20000 && c.Waiters() < n; i++ {
		time.Sleep(50 * time.Microsecond)
	}
}

// Advance moves time forward timer by timer, letting the goroutines settle after each one. idle is how many timers
// the system holds when it is waiting (one per task loop).
func (c *FakeClock) Advance(d time.Duration, idle int) {
	c.mu.Lock()
	target := c.now.Add(d)
	c.mu.Unlock()
	for {
		c.mu.Lock()
		sort.Slice(c.ws, func(i, j int) bool { return c.ws[i].at.Before(c.ws[j].at) })
		if len(c.ws) == 0 || c.ws[0].at.After(target) {
			c.now = target
			c.mu.Unlock()
			return
		}
		w := c.ws[0]
		c.ws = c.ws[1:]
		if w.at.After(c.now) {
			c.now = w.at
		}
		c.mu.Unlock()
		w.ch <- w.at
		time.Sleep(300 * time.Microsecond) // let the woken goroutine run and re-arm
		c.BlockUntil(idle)
	}
}
