package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Window is a daily time-of-day range [Start, End) in minutes from midnight.
// If End < Start the window wraps midnight (e.g. 22:00-06:30).
type Window struct {
	Start int
	End   int
}

// ParseHM parses "HH:MM" into minutes from midnight.
func ParseHM(s string) (int, error) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok {
		return 0, fmt.Errorf("invalid time %q, want HH:MM", s)
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, fmt.Errorf("invalid time %q, want HH:MM", s)
	}
	return hh*60 + mm, nil
}

// FormatHM formats minutes from midnight as HH:MM.
func FormatHM(m int) string { return fmt.Sprintf("%02d:%02d", (m/60)%24, m%60) }

// ParseWindow parses "HH:MM-HH:MM". An empty string returns ok=false.
func ParseWindow(s string) (w Window, ok bool, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return w, false, nil
	}
	a, b, found := strings.Cut(s, "-")
	if !found {
		return w, false, fmt.Errorf("invalid range %q, want HH:MM-HH:MM", s)
	}
	if w.Start, err = ParseHM(a); err != nil {
		return w, false, err
	}
	if w.End, err = ParseHM(b); err != nil {
		return w, false, err
	}
	if w.Start == w.End {
		return w, false, fmt.Errorf("range %q is empty", s)
	}
	return w, true, nil
}

func (w Window) String() string { return FormatHM(w.Start) + "-" + FormatHM(w.End) }

func minuteOfDay(t time.Time, loc *time.Location) int {
	t = t.In(loc)
	return t.Hour()*60 + t.Minute()
}

// Contains reports whether t (in loc) falls inside the window.
func (w Window) Contains(t time.Time, loc *time.Location) bool {
	m := minuteOfDay(t, loc)
	if w.Start < w.End {
		return m >= w.Start && m < w.End
	}
	return m >= w.Start || m < w.End
}

// NextStart returns the first window start strictly after t.
func (w Window) NextStart(t time.Time, loc *time.Location) time.Time {
	return nextTimeOfDay(t, w.Start, loc)
}

// EndAfter returns the end of the window instance containing t (first End strictly after t).
func (w Window) EndAfter(t time.Time, loc *time.Location) time.Time {
	return nextTimeOfDay(t, w.End%1440, loc)
}

func nextTimeOfDay(t time.Time, minutes int, loc *time.Location) time.Time {
	lt := t.In(loc)
	c := time.Date(lt.Year(), lt.Month(), lt.Day(), minutes/60, minutes%60, 0, 0, loc)
	if !c.After(t) {
		c = time.Date(lt.Year(), lt.Month(), lt.Day()+1, minutes/60, minutes%60, 0, 0, loc)
	}
	return c
}

// Quiet describes effective quiet hours for a reminder.
type Quiet struct {
	Enabled bool
	Window  Window
}

// HoldUntil returns when a message due at t may be delivered: t itself when
// outside quiet hours, otherwise the end of the quiet period.
func (q Quiet) HoldUntil(t time.Time, loc *time.Location) time.Time {
	if !q.Enabled || !q.Window.Contains(t, loc) {
		return t
	}
	return q.Window.EndAfter(t, loc)
}

// ResolveQuiet computes effective quiet hours. override: "" = use global,
// "off" = none, "HH:MM-HH:MM" = custom.
func ResolveQuiet(global, override string, ignore bool) Quiet {
	if ignore {
		return Quiet{}
	}
	src := global
	switch strings.TrimSpace(strings.ToLower(override)) {
	case "":
	case "off", "none":
		return Quiet{}
	default:
		src = override
	}
	w, ok, err := ParseWindow(src)
	if err != nil || !ok {
		return Quiet{}
	}
	return Quiet{Enabled: true, Window: w}
}
