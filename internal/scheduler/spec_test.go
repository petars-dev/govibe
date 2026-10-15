package scheduler

import (
	"strings"
	"testing"
	"time"
)

func utc(y int, m time.Month, d, h, mi, s int) time.Time {
	return time.Date(y, m, d, h, mi, s, 0, time.UTC)
}

func TestParseAcceptsTheDocumentedSyntax(t *testing.T) {
	from := utc(2026, 10, 11, 18, 17, 43)
	for _, tc := range []struct {
		spec string
		next string // in UTC, "" to skip the check
	}{
		{"*/5 * * * *", "2026-10-11 18:20:00"},
		{"0 2 * * *", "2026-10-12 02:00:00"},
		{"@hourly", "2026-10-11 19:00:00"},
		{"@daily", "2026-10-12 00:00:00"},
		{"@weekly", "2026-10-18 00:00:00"}, // the coming Sunday
		{"@monthly", "2026-11-01 00:00:00"},
		{"@yearly", "2027-01-01 00:00:00"},
		{"@every 10m", "2026-10-11 18:27:43"},
		{"@every 90m", ""},
		{"CRON_TZ=Europe/Sofia 0 9 * * MON-FRI", "2026-10-12 06:00:00"}, // 09:00 EEST (UTC+3) on Monday
		{"  0 2 * * *  ", "2026-10-12 02:00:00"},
		{"0 0 29 2 *", ""}, // a leap day: rare, but it fires
	} {
		s, err := Parse(tc.spec, nil, from)
		if err != nil {
			t.Errorf("%q: %v", tc.spec, err)
			continue
		}
		if tc.next != "" {
			if got := s.Next(from).In(time.UTC).Format(time.DateTime); got != tc.next {
				t.Errorf("%q: next after %s = %s, want %s", tc.spec, from.Format(time.DateTime), got, tc.next)
			}
		}
	}
}

func TestParseRejectsWhatItShould(t *testing.T) {
	from := utc(2026, 10, 11, 12, 0, 0)
	for _, tc := range []struct{ spec, why string }{
		{"", "empty"},
		{"   ", "empty"},
		{"* * *", "expected exactly 5 fields"},
		{"61 * * * *", "end of range"},
		{"@every", "descriptor"},
		{"@weekly2", "descriptor"},
		{"CRON_TZ=Nowhere/Land * * * * *", "unknown time zone"},
		{"CRON_TZ=Europe/Sofia", "must be followed"}, // the parser itself panics on this one
		{"TZ=Europe/Sofia", "must be followed"},
		{"0 0 9 * * *", "expected exactly 5 fields"}, // six fields: no seconds in v1
		{"@every 0s", "at least"},
		{"@every -5m", "at least"},
		{"@every 30s", "at least"},
		{"@every 59s", "at least"},
		{"@every soon", "duration"},
		{"0 0 31 2 *", "never fires"},
	} {
		_, err := Parse(tc.spec, nil, from)
		if err == nil {
			t.Errorf("%q should be rejected", tc.spec)
			continue
		}
		if !strings.Contains(err.Error(), tc.why) {
			t.Errorf("%q: error %q does not mention %q", tc.spec, err, tc.why)
		}
		if tc.spec != "" && strings.TrimSpace(tc.spec) != "" && !strings.Contains(err.Error(), tc.spec) {
			t.Errorf("%q: error %q does not quote the spec", tc.spec, err)
		}
	}
}

func TestExplicitZoneBeatsTheDefault(t *testing.T) {
	sofia, _ := time.LoadLocation("Europe/Sofia")
	s, err := Parse("CRON_TZ=Europe/Sofia 0 9 * * *", time.UTC, utc(2026, 10, 11, 12, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !s.ExplicitZone() || s.Location().String() != "Europe/Sofia" {
		t.Errorf("zone = %v explicit=%v", s.Location(), s.ExplicitZone())
	}
	if got := s.Next(utc(2026, 10, 11, 12, 0, 0)); got.Hour() != 9 || got.Location().String() != sofia.String() {
		t.Errorf("next = %v", got)
	}
	d, err := Parse("0 9 * * *", sofia, utc(2026, 10, 11, 12, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if d.ExplicitZone() || d.Location().String() != "Europe/Sofia" {
		t.Errorf("the default zone must apply: %v explicit=%v", d.Location(), d.ExplicitZone())
	}
	if got := d.Next(utc(2026, 10, 11, 12, 0, 0)); got.Hour() != 9 {
		t.Errorf("next = %v, want 09:00 Sofia", got)
	}
}

func TestEveryReportsItsInterval(t *testing.T) {
	s, _ := Parse("@every 6h", nil, time.Now())
	if d, ok := s.Every(); !ok || d != 6*time.Hour {
		t.Errorf("Every = %v %v", d, ok)
	}
	s, _ = Parse("0 2 * * *", nil, time.Now())
	if _, ok := s.Every(); ok {
		t.Error("a cron spec has no interval")
	}
}

// A spec that names its hours is de-duplicated when the clocks repeat an hour; one that
// allows every hour follows real hours. The rule is read from the parsed spec, so every way of
// writing "every hour" agrees.
func TestWhichSpecsNameTheirHours(t *testing.T) {
	for spec, want := range map[string]bool{
		"30 3 * * *":                   true, // names its hour
		"0,30 3 * * *":                 true,
		"*/15 3 * * *":                 true,
		"0 9-17 * * MON-FRI":           true,
		"0 */3 * * *":                  true,
		"@daily":                       true,
		"@weekly":                      true,
		"CRON_TZ=Asia/Tokyo 0 4 * * *": true,
		"30 * * * *":                   false, // every hour
		"*/5 * * * *":                  false,
		"0 */1 * * *":                  false,
		"0 0-23 * * *":                 false, // every hour, written out
		"0 0-23/1 * * *":               false,
		"@hourly":                      false,
		"@every 10m":                   false,
	} {
		s, err := Parse(spec, time.UTC, utc(2026, 10, 11, 12, 0, 0))
		if err != nil {
			t.Errorf("%q: %v", spec, err)
			continue
		}
		if s.fixed != want {
			t.Errorf("%q names its hours = %v, want %v", spec, s.fixed, want)
		}
	}
}

func TestParseRejectsWhatItWouldReadAsSomethingElse(t *testing.T) {
	from := utc(2026, 10, 11, 12, 0, 0)
	for spec, why := range map[string]string{
		"CRON_TZ=Local 0 9 * * *": "Local",
		"TZ=local 0 9 * * *":      "Local",
		"CRON_TZ= 0 9 * * *":      "names no IANA time zone",
		"*/90 * * * *":            "wider than its field",
		"0 */25 * * *":            "wider than its field",
		"0 0 */40 * *":            "wider than its field",
		"0-59/61 * * * *":         "wider than its field",
	} {
		_, err := Parse(spec, nil, from)
		if err == nil || !strings.Contains(err.Error(), why) || !strings.Contains(err.Error(), spec) {
			t.Errorf("%q: %v, want an error naming the spec and %q", spec, err, why)
		}
	}
	for _, ok := range []string{"*/59 * * * *", "*/60 * * * *", "0 */24 * * *", "0 0 */31 * *", "0 0 * */12 *", "0 0 * * */7"} {
		if _, err := Parse(ok, nil, from); err != nil {
			t.Errorf("%q must be accepted: %v", ok, err)
		}
	}
}

// A listing shows what will run: the second reading of a repeated hour is not a line of its own,
// and a "now" between the two readings does not point at the one that will be skipped.
func TestUpcomingLeavesOutTheRepeatedReading(t *testing.T) {
	sof, _ := time.LoadLocation("Europe/Sofia")
	s, err := Parse("30 3 * * *", sof, utc(2026, 10, 20, 0, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	show := func(ts []time.Time) string {
		var out []string
		for _, x := range ts {
			out = append(out, x.In(sof).Format("01-02 15:04 MST"))
		}
		return strings.Join(out, " | ")
	}
	from := time.Date(2026, 10, 24, 12, 0, 0, 0, sof)
	if got := show(s.Upcoming(from, 3)); got != "10-25 03:30 EEST | 10-26 03:30 EET | 10-27 03:30 EET" {
		t.Errorf("upcoming = %s", got)
	}
	// From between the two readings (03:45 EEST): the repeat at 03:30 EET is not next.
	between := time.Date(2026, 10, 25, 3, 45, 0, 0, sof)
	if got := show(s.Upcoming(between, 2)); got != "10-26 03:30 EET | 10-27 03:30 EET" {
		t.Errorf("between the readings: %s", got)
	}
	// Every hour: the repeated hour is real and is listed.
	h, _ := Parse("30 * * * *", sof, utc(2026, 10, 20, 0, 0, 0))
	if got := show(h.Upcoming(time.Date(2026, 10, 25, 2, 45, 0, 0, sof), 3)); got != "10-25 03:30 EEST | 10-25 03:30 EET | 10-25 04:30 EET" {
		t.Errorf("an hourly spec follows real hours: %s", got)
	}
}

func TestShortDurationDropsZeroTailsAndParsesBack(t *testing.T) {
	for d, want := range map[time.Duration]string{
		6 * time.Hour: "6h", 10 * time.Minute: "10m", 90 * time.Minute: "1h30m", 90 * time.Second: "1m30s", 30 * time.Second: "30s",
		time.Minute: "1m", 100 * time.Hour: "100h", 6*time.Hour + 30*time.Second: "6h0m30s", time.Hour + 10*time.Second: "1h0m10s",
	} {
		got := ShortDuration(d)
		if got != want {
			t.Errorf("ShortDuration(%s) = %q, want %q", d, got, want)
		}
		if back, err := time.ParseDuration(got); err != nil || back != d {
			t.Errorf("%q does not parse back to %s: %v %v", got, d, back, err)
		}
	}
}
