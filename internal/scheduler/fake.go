package scheduler

import (
	"sync"
	"time"
)

// DefaultSettle is how long a Fake waits, in real time, for the runs a timer started
// to finish before it moves on to the next timer.
const DefaultSettle = 5 * time.Second

// Fake is a manual clock: time moves only when Advance or AdvanceTo says so, and every
// timer that falls due on the way fires, in order, inside that call. A callback runs
// with Now at its own due time, so a task armed from inside a callback is armed
// exactly, and nothing depends on goroutine timing.
//
// A Runner on a Fake also waits, between timers, for the runs the last one started
// (see SetSettle), so a command that takes no virtual time has finished before the
// next due time is looked at. That makes "advance an hour, then count the runs"
// deterministic.
type Fake struct {
	mu      sync.Mutex
	now     time.Time
	seq     uint64
	timers  []*fakeTimer
	settles []func(budget time.Duration)
	budget  time.Duration
}

type fakeTimer struct {
	at   time.Time
	seq  uint64
	f    func()
	dead bool
	c    *Fake
}

// NewFake returns a clock that reads start until it is advanced.
func NewFake(start time.Time) *Fake {
	return &Fake{now: start, budget: DefaultSettle}
}

// Now is the clock's current time.
func (c *Fake) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// After returns a channel that receives the time when the clock reaches now+d.
func (c *Fake) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	c.AfterFunc(d, func() { ch <- c.Now() })
	return ch
}

// AfterFunc arms f for now+d. A timer that is already due fires on the next Advance.
func (c *Fake) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	t := &fakeTimer{at: c.now.Add(d), seq: c.seq, f: f, c: c}
	c.timers = append(c.timers, t)
	return t
}

// Stop cancels the timer.
func (t *fakeTimer) Stop() bool {
	t.c.mu.Lock()
	defer t.c.mu.Unlock()
	if t.dead {
		return false
	}
	t.dead = true
	return true
}

// Pending is the number of timers still armed.
func (c *Fake) Pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, t := range c.timers {
		if !t.dead {
			n++
		}
	}
	return n
}

// SetSettle sets how long, in real time, the clock waits for started runs after each
// timer fires. Zero or less restores DefaultSettle. A test that starts a command which
// blocks on purpose (an overlap test) lowers it so the wait is short.
func (c *Fake) SetSettle(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d <= 0 {
		d = DefaultSettle
	}
	c.budget = d
}

// addSettle registers a runner: after each timer the clock calls it, so it can wait
// for the runs that timer started.
func (c *Fake) addSettle(f func(budget time.Duration)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.settles = append(c.settles, f)
}

// Advance moves the clock forward by d, firing what falls due.
func (c *Fake) Advance(d time.Duration) { c.AdvanceTo(c.Now().Add(d)) }

// AdvanceTo moves the clock to t (never backwards), firing every timer due on the
// way in time order, then lets the runs they started finish.
func (c *Fake) AdvanceTo(t time.Time) {
	for {
		c.mu.Lock()
		if t.Before(c.now) {
			t = c.now
		}
		// One pass drops the timers that were stopped and picks the earliest that is due;
		// a fired timer leaves the list at once, so the list never outgrows the live timers
		// and advancing over a year of a task that fires every minute stays linear.
		live := c.timers[:0]
		pick := -1
		for _, tm := range c.timers {
			if tm.dead {
				continue
			}
			live = append(live, tm)
			if tm.at.After(t) {
				continue
			}
			if pick < 0 || tm.at.Before(live[pick].at) || (tm.at.Equal(live[pick].at) && tm.seq < live[pick].seq) {
				pick = len(live) - 1
			}
		}
		for i := len(live); i < len(c.timers); i++ {
			c.timers[i] = nil
		}
		c.timers = live
		settles, budget := c.settleSnapshot()
		if pick < 0 {
			c.now = t
			c.mu.Unlock()
			for _, s := range settles {
				s(budget)
			}
			return
		}
		next := live[pick]
		c.timers = append(live[:pick], live[pick+1:]...)
		next.dead = true
		if next.at.After(c.now) {
			c.now = next.at
		}
		c.mu.Unlock()
		next.f()
		for _, s := range settles {
			s(budget)
		}
	}
}

// settleSnapshot copies the registered runners and the budget. The caller holds c.mu.
func (c *Fake) settleSnapshot() ([]func(time.Duration), time.Duration) {
	out := make([]func(time.Duration), len(c.settles))
	copy(out, c.settles)
	return out, c.budget
}
