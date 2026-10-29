// Package schedule implements recurrence rules (a subset of RFC 5545 RRULE),
// next-fire-time calculation and quiet hours. All wall-clock calculations are
// done in a given *time.Location.
package schedule

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Freq is the recurrence frequency.
type Freq string

// Supported frequencies.
const (
	Daily    Freq = "DAILY"
	Weekly   Freq = "WEEKLY"
	Monthly  Freq = "MONTHLY"
	Hourly   Freq = "HOURLY"
	Minutely Freq = "MINUTELY"
)

// Rule is a structured recurrence rule. It serialises to an RRULE string.
//
// For DAILY/WEEKLY/MONTHLY the occurrence time of day comes from Hour/Minute.
// For HOURLY/MINUTELY occurrences are anchor + k*Interval and may be limited to
// an active window (WindowStart..WindowEnd, minutes from midnight; may wrap midnight).
// MONTHLY with MonthDay greater than the number of days in a month fires on the
// last day of that month (so "31" means "last day" in short months).
type Rule struct {
	Freq        Freq
	Interval    int
	Weekdays    []time.Weekday
	MonthDay    int
	Hour        int
	Minute      int
	WindowStart int // minutes from midnight, -1 = no window
	WindowEnd   int
	Until       time.Time // inclusive, zero = none
	Count       int       // 0 = unlimited
}

// HasWindow reports whether an active window is configured.
func (r Rule) HasWindow() bool {
	return r.WindowStart >= 0 && r.WindowEnd >= 0 && r.WindowStart != r.WindowEnd
}

var dayCodes = []string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}

// String encodes the rule as an RRULE value (without DTSTART). The active
// window is not part of RFC 5545 and is stored separately by callers.
func (r Rule) String() string {
	parts := []string{"FREQ=" + string(r.Freq)}
	if r.Interval > 1 {
		parts = append(parts, "INTERVAL="+strconv.Itoa(r.Interval))
	}
	switch r.Freq {
	case Weekly:
		var ds []string
		for _, d := range sortedDays(r.Weekdays) {
			ds = append(ds, dayCodes[d])
		}
		parts = append(parts, "BYDAY="+strings.Join(ds, ","))
	case Monthly:
		parts = append(parts, "BYMONTHDAY="+strconv.Itoa(r.MonthDay))
	}
	if r.Freq == Daily || r.Freq == Weekly || r.Freq == Monthly {
		parts = append(parts, "BYHOUR="+strconv.Itoa(r.Hour), "BYMINUTE="+strconv.Itoa(r.Minute))
	}
	if !r.Until.IsZero() {
		parts = append(parts, "UNTIL="+r.Until.UTC().Format("20060102T150405Z"))
	}
	if r.Count > 0 {
		parts = append(parts, "COUNT="+strconv.Itoa(r.Count))
	}
	return strings.Join(parts, ";")
}

// Parse decodes an RRULE value produced by String (a pragmatic RFC 5545 subset).
func Parse(s string) (Rule, error) {
	r := Rule{Interval: 1, WindowStart: -1, WindowEnd: -1}
	s = strings.TrimPrefix(strings.TrimSpace(s), "RRULE:")
	if s == "" {
		return r, errors.New("empty rule")
	}
	for _, p := range strings.Split(s, ";") {
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			return r, fmt.Errorf("invalid rule part %q", p)
		}
		var err error
		switch strings.ToUpper(k) {
		case "FREQ":
			r.Freq = Freq(strings.ToUpper(v))
		case "INTERVAL":
			r.Interval, err = strconv.Atoi(v)
		case "BYDAY":
			for _, d := range strings.Split(v, ",") {
				idx := indexOf(dayCodes, strings.ToUpper(strings.TrimSpace(d)))
				if idx < 0 {
					return r, fmt.Errorf("invalid BYDAY %q", d)
				}
				r.Weekdays = append(r.Weekdays, time.Weekday(idx))
			}
		case "BYMONTHDAY":
			r.MonthDay, err = strconv.Atoi(v)
		case "BYHOUR":
			r.Hour, err = strconv.Atoi(v)
		case "BYMINUTE":
			r.Minute, err = strconv.Atoi(v)
		case "UNTIL":
			r.Until, err = parseUntil(v)
		case "COUNT":
			r.Count, err = strconv.Atoi(v)
		default:
			return r, fmt.Errorf("unsupported rule part %q", k)
		}
		if err != nil {
			return r, fmt.Errorf("invalid %s: %w", k, err)
		}
	}
	return r, r.Validate()
}

func parseUntil(v string) (time.Time, error) {
	for _, layout := range []string{"20060102T150405Z", "20060102"} {
		if t, err := time.Parse(layout, v); err == nil {
			if layout == "20060102" {
				t = t.Add(24*time.Hour - time.Second)
			}
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("bad UNTIL %q", v)
}

// Validate checks rule consistency.
func (r Rule) Validate() error {
	switch r.Freq {
	case Daily, Weekly, Monthly, Hourly, Minutely:
	default:
		return fmt.Errorf("unsupported frequency %q", r.Freq)
	}
	if r.Interval < 1 || r.Interval > 1000 {
		return errors.New("interval must be between 1 and 1000")
	}
	if r.Freq == Minutely && r.Interval < 5 {
		return errors.New("minute interval must be at least 5")
	}
	if r.Freq == Weekly && len(r.Weekdays) == 0 {
		return errors.New("weekly rule needs at least one weekday")
	}
	if r.Freq == Monthly && (r.MonthDay < 1 || r.MonthDay > 31) {
		return errors.New("day of month must be 1..31")
	}
	if r.Hour < 0 || r.Hour > 23 || r.Minute < 0 || r.Minute > 59 {
		return errors.New("invalid time of day")
	}
	if r.Count < 0 {
		return errors.New("count must be >= 0")
	}
	if r.HasWindow() && (r.WindowStart >= 1440 || r.WindowEnd >= 1440) {
		return errors.New("invalid active window")
	}
	return nil
}

// IsSubDaily reports whether the rule fires more than once per day potentially.
func (r Rule) IsSubDaily() bool { return r.Freq == Hourly || r.Freq == Minutely }

// Next returns the first occurrence strictly after `after`, for a series starting
// at anchor (the first occurrence is the first matching time >= anchor).
// It returns the zero time if the series has ended.
func (r Rule) Next(anchor, after time.Time, loc *time.Location) time.Time {
	anchor = anchor.In(loc)
	after = after.In(loc)
	if r.Count > 0 {
		// Walk from the start so COUNT is honoured; bounded by Count.
		t := anchor.Add(-time.Nanosecond)
		for i := 0; i < r.Count; i++ {
			t = r.nextRaw(anchor, t, loc)
			if t.IsZero() || r.pastUntil(t) {
				return time.Time{}
			}
			if t.After(after) {
				return t
			}
		}
		return time.Time{}
	}
	t := r.nextRaw(anchor, after, loc)
	if t.IsZero() || r.pastUntil(t) {
		return time.Time{}
	}
	return t
}

// Between returns occurrences in [from, to), at most limit items.
func (r Rule) Between(anchor, from, to time.Time, loc *time.Location, limit int) []time.Time {
	var out []time.Time
	t := r.Next(anchor, from.Add(-time.Nanosecond), loc)
	for !t.IsZero() && t.Before(to) && len(out) < limit {
		out = append(out, t)
		t = r.Next(anchor, t, loc)
	}
	return out
}

func (r Rule) pastUntil(t time.Time) bool {
	return !r.Until.IsZero() && t.After(r.Until)
}

// nextRaw ignores COUNT/UNTIL.
func (r Rule) nextRaw(anchor, after time.Time, loc *time.Location) time.Time {
	if after.Before(anchor) {
		after = anchor.Add(-time.Nanosecond)
	}
	switch r.Freq {
	case Daily:
		return r.nextDaily(anchor, after, loc)
	case Weekly:
		return r.nextWeekly(anchor, after, loc)
	case Monthly:
		return r.nextMonthly(anchor, after, loc)
	case Hourly:
		return r.nextStep(anchor, after, time.Duration(r.Interval)*time.Hour, loc)
	case Minutely:
		return r.nextStep(anchor, after, time.Duration(r.Interval)*time.Minute, loc)
	}
	return time.Time{}
}

func dayNumber(t time.Time) int {
	// Days since epoch of the civil date (ignores the time of day and zone offset).
	y, m, d := t.Date()
	return int(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400)
}

func (r Rule) at(y int, m time.Month, d int, loc *time.Location) time.Time {
	return time.Date(y, m, d, r.Hour, r.Minute, 0, 0, loc)
}

func (r Rule) nextDaily(anchor, after time.Time, loc *time.Location) time.Time {
	a0 := dayNumber(anchor)
	day := max(dayNumber(after), a0)
	// Align to the interval phase.
	if off := (day - a0) % r.Interval; off != 0 {
		day += r.Interval - off
	}
	for i := 0; i < 3; i++ {
		d := time.Unix(int64(day)*86400, 0).UTC()
		t := r.at(d.Year(), d.Month(), d.Day(), loc)
		if t.After(after) && !t.Before(anchor) {
			return t
		}
		day += r.Interval
	}
	return time.Time{}
}

func (r Rule) nextWeekly(anchor, after time.Time, loc *time.Location) time.Time {
	weekStart := func(t time.Time) int { // Monday-based week start day number
		wd := (int(t.Weekday()) + 6) % 7
		return dayNumber(t) - wd
	}
	w0 := weekStart(anchor)
	ws := max(weekStart(after), w0)
	if off := ((ws - w0) / 7) % r.Interval; off != 0 {
		ws += (r.Interval - off) * 7
	}
	days := sortedDays(r.Weekdays)
	for i := 0; i < 3; i++ {
		for _, wd := range days {
			off := (int(wd) + 6) % 7
			d := time.Unix(int64(ws+off)*86400, 0).UTC()
			t := r.at(d.Year(), d.Month(), d.Day(), loc)
			if t.After(after) && !t.Before(anchor) {
				return t
			}
		}
		ws += 7 * r.Interval
	}
	return time.Time{}
}

func daysIn(y int, m time.Month) int {
	return time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func (r Rule) nextMonthly(anchor, after time.Time, loc *time.Location) time.Time {
	mi := func(t time.Time) int { return t.Year()*12 + int(t.Month()) - 1 }
	m0 := mi(anchor)
	m := max(mi(after), m0)
	if off := (m - m0) % r.Interval; off != 0 {
		m += r.Interval - off
	}
	for i := 0; i < 3; i++ {
		y, mon := m/12, time.Month(m%12+1)
		d := r.MonthDay
		if dim := daysIn(y, mon); d > dim {
			d = dim
		}
		t := r.at(y, mon, d, loc)
		if t.After(after) && !t.Before(anchor) {
			return t
		}
		m += r.Interval
	}
	return time.Time{}
}

func (r Rule) nextStep(anchor, after time.Time, step time.Duration, loc *time.Location) time.Time {
	k := int64(after.Sub(anchor)/step) + 1
	if after.Before(anchor) {
		k = 0
	}
	t := anchor.Add(time.Duration(k) * step)
	if !r.HasWindow() {
		return t
	}
	// The active window end is inclusive ("08:00-20:00" includes 20:00).
	w := Window{Start: r.WindowStart, End: r.WindowEnd + 1}
	for i := 0; i < 1000; i++ {
		if w.Contains(t, loc) {
			return t
		}
		ws := w.NextStart(t, loc)
		k = int64((ws.Sub(anchor) + step - 1) / step)
		t = anchor.Add(time.Duration(k) * step)
	}
	return time.Time{}
}

func sortedDays(ds []time.Weekday) []time.Weekday {
	seen := map[time.Weekday]bool{}
	var out []time.Weekday
	for _, d := range ds {
		if d >= 0 && d <= 6 && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	// Order Monday..Sunday.
	sort.Slice(out, func(i, j int) bool { return (int(out[i])+6)%7 < (int(out[j])+6)%7 })
	return out
}

func indexOf(xs []string, s string) int {
	for i, x := range xs {
		if x == s {
			return i
		}
	}
	return -1
}

// Describe returns a short human description of the rule.
func (r Rule) Describe() string {
	hm := fmt.Sprintf("%02d:%02d", r.Hour, r.Minute)
	var s string
	switch r.Freq {
	case Daily:
		if r.Interval == 1 {
			s = "Daily at " + hm
		} else {
			s = fmt.Sprintf("Every %d days at %s", r.Interval, hm)
		}
	case Weekly:
		var names []string
		for _, d := range sortedDays(r.Weekdays) {
			names = append(names, d.String()[:3])
		}
		s = "Weekly on " + strings.Join(names, ", ") + " at " + hm
		if r.Interval > 1 {
			s = fmt.Sprintf("Every %d weeks on %s at %s", r.Interval, strings.Join(names, ", "), hm)
		}
	case Monthly:
		s = fmt.Sprintf("Monthly on day %d at %s", r.MonthDay, hm)
		if r.MonthDay >= 29 {
			s += " (or last day)"
		}
	case Hourly:
		s = fmt.Sprintf("Every %d hour(s)", r.Interval)
	case Minutely:
		s = fmt.Sprintf("Every %d minutes", r.Interval)
	}
	if r.HasWindow() {
		s += fmt.Sprintf(" between %s and %s", FormatHM(r.WindowStart), FormatHM(r.WindowEnd))
	}
	if !r.Until.IsZero() {
		s += ", until " + r.Until.Format("2006-01-02")
	}
	if r.Count > 0 {
		s += fmt.Sprintf(", %d times", r.Count)
	}
	return s
}
