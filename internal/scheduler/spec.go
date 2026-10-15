package scheduler

import (
	"fmt"
	"strings"
	"time"
	// The zone database is embedded, so CRON_TZ and scheduler.timezone work in a
	// minimal container that ships none (it adds about 450 KB to a binary).
	_ "time/tzdata"

	"github.com/robfig/cron/v3"
)

// MinEvery is the shortest @every interval. A spec has no seconds field in v1, and the
// parser would otherwise turn 0s, a negative value and anything under a second into one
// second without a word.
const MinEvery = time.Minute

var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// Spec is a validated schedule: a five-field cron expression, a descriptor
// (@hourly @daily @weekly @monthly @yearly) or "@every <duration>", with an optional
// "CRON_TZ=<zone>" prefix.
type Spec struct {
	Expr string

	sched    cron.Schedule
	loc      *time.Location
	explicit bool          // the spec names its own zone (CRON_TZ=)
	every    time.Duration // > 0 for @every
	// fixed is true for a spec that names its hours. When the clocks go back and an hour
	// repeats, such a spec runs once per reading of the clock ("30 3 * * *" runs once on the
	// day 03:30 happens twice; "*/15 3 * * *" four times, not eight), where a spec that allows
	// every hour ("@hourly") follows real hours, which is what "hourly" means.
	fixed bool
}

// Parse validates expr and fixes its time zone: the zone the spec names, else def (UTC
// when nil). It never panics and it names what is wrong; from is the time it must be
// able to fire after (a spec like "0 0 31 2 *" parses but never would, and is rejected).
func Parse(expr string, def *time.Location, from time.Time) (s *Spec, err error) {
	if def == nil {
		def = time.UTC
	}
	defer func() {
		// The parser panics on a bare "CRON_TZ=Zone", among other inputs it does not expect.
		if r := recover(); r != nil {
			s, err = nil, fmt.Errorf("invalid schedule %q: %v", expr, r)
		}
	}()
	trimmed := strings.TrimSpace(expr)
	if trimmed == "" {
		return nil, fmt.Errorf("invalid schedule: it is empty")
	}
	if f := strings.Fields(trimmed); len(f) == 1 && (strings.HasPrefix(f[0], "CRON_TZ=") || strings.HasPrefix(f[0], "TZ=")) {
		return nil, fmt.Errorf("invalid schedule %q: a time zone prefix must be followed by the schedule", expr)
	}
	if err := checkZoneAndSteps(trimmed, expr); err != nil {
		return nil, err
	}
	sched, perr := parser.Parse(trimmed)
	if perr != nil {
		return nil, fmt.Errorf("invalid schedule %q: %w", expr, perr)
	}
	s = &Spec{Expr: trimmed, sched: sched, loc: def}
	switch v := sched.(type) {
	case cron.ConstantDelaySchedule:
		if v.Delay < MinEvery {
			return nil, fmt.Errorf("invalid schedule %q: @every must be at least %s (there is no seconds field)", expr, MinEvery)
		}
		s.every = v.Delay
	case *cron.SpecSchedule:
		if v.Location != time.Local { // the parser leaves time.Local when no zone was named
			s.loc, s.explicit = v.Location, true
		}
	}
	if v, ok := sched.(*cron.SpecSchedule); ok {
		// It names its hours unless every hour of the day is allowed (a "*", "*/1", "0-23" or
		// "@hourly" all read the same here): see the doc of Spec.fixed.
		const allHours = uint64(1)<<24 - 1
		s.fixed = v.Hour&allHours != allHours
	}
	if s.Next(from).IsZero() {
		return nil, fmt.Errorf("invalid schedule %q: it never fires (no such day exists)", expr)
	}
	return s, nil
}

// Next is the first time after t that the spec fires, in the spec's zone; the zero
// time when there is none.
func (s *Spec) Next(t time.Time) time.Time {
	n := s.sched.Next(t.In(s.loc))
	if n.IsZero() {
		return n
	}
	return n.In(s.loc)
}

// Upcoming is the next n due times after from, computed from the spec alone. A spec that
// names its hours leaves out the second reading of an hour the clocks repeat, as the runner
// does, so a listing shows what will run and not two lines for one run.
func (s *Spec) Upcoming(from time.Time, n int) []time.Time {
	seen := map[string]time.Time{}
	if s.fixed {
		// What ran just before from counts too: from may fall between the two readings.
		for t := s.Next(from.Add(-firedWindow)); !t.IsZero() && !t.After(from); t = s.Next(t) {
			seen[wallOf(t, s.loc)] = t
		}
	}
	var out []time.Time
	for t := from; len(out) < n; {
		if t = s.Next(t); t.IsZero() {
			break
		}
		if s.fixed {
			w := wallOf(t, s.loc)
			if _, repeat := seen[w]; repeat {
				continue
			}
			seen[w] = t
		}
		out = append(out, t)
	}
	return out
}

// Location is the zone the spec is evaluated in: the one it names, else the default.
func (s *Spec) Location() *time.Location { return s.loc }

// ExplicitZone reports whether the spec names its own zone (CRON_TZ=), which then wins
// over the configured default.
func (s *Spec) ExplicitZone() bool { return s.explicit }

// Every is the interval of an "@every" spec; ok is false for any other.
func (s *Spec) Every() (d time.Duration, ok bool) { return s.every, s.every > 0 }

// ShortDuration is a duration without its zero tails: 6h, 10m, 1h30m, 1m30s. What it returns
// parses back to the same duration, so it can be written into a spec ("@every 6h").
func ShortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = s[:len(s)-2]
	}
	if strings.HasSuffix(s, "h0m") {
		s = s[:len(s)-2]
	}
	return s
}

// fieldSizes are the number of values each of the five fields takes: minute, hour, day of
// month, month, day of week.
var fieldSizes = [5]int{60, 24, 31, 12, 7}

// checkZoneAndSteps rejects what the parser accepts and means something else: a zone prefix
// that names no zone or "Local" (which would be the scheduler's zone, not the machine's),
// and a step wider than its field ("*/90" in the minute field runs once an hour).
func checkZoneAndSteps(spec, quoted string) error {
	f := strings.Fields(spec)
	if len(f) > 0 {
		for _, p := range []string{"CRON_TZ=", "TZ="} {
			if name, ok := strings.CutPrefix(f[0], p); ok {
				if name == "" || strings.EqualFold(name, "local") {
					return fmt.Errorf("invalid schedule %q: %s names no IANA time zone (use Europe/Sofia, UTC, ...; \"Local\" depends on the machine)", quoted, strings.TrimSuffix(p, "="))
				}
				f = f[1:]
				break
			}
		}
	}
	if len(f) != 5 {
		return nil
	}
	for i, field := range f {
		for _, part := range strings.Split(field, ",") {
			_, step, ok := strings.Cut(part, "/")
			if !ok {
				continue
			}
			var n int
			if _, err := fmt.Sscanf(step, "%d", &n); err == nil && n > fieldSizes[i] {
				return fmt.Errorf("invalid schedule %q: a step of %d is wider than its field (%d values), so it would run only at the start of the range", quoted, n, fieldSizes[i])
			}
		}
	}
	return nil
}
