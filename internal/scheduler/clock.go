package scheduler

import "time"

// Clock is the scheduler's only source of time, so a test can drive it. A Clock
// arms timers whose callbacks run on the clock's own goroutines (a real clock) or
// inside Advance (the Fake).
type Clock interface {
	Now() time.Time
	// After returns a channel that receives the time once d has passed.
	After(d time.Duration) <-chan time.Time
	// AfterFunc calls f once d has passed; Stop cancels it.
	AfterFunc(d time.Duration, f func()) Timer
}

// Timer is an armed AfterFunc.
type Timer interface {
	// Stop prevents the callback from running and reports whether it did.
	Stop() bool
}

// Real is the wall clock.
var Real Clock = realClock{}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
func (realClock) AfterFunc(d time.Duration, f func()) Timer {
	return time.AfterFunc(d, f)
}
