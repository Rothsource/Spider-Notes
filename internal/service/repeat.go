package service

import (
	"strings"
	"time"

	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/schedule"
)

// RepeatInput is the user-facing structured form of a repeat rule.
type RepeatInput struct {
	Freq     string `json:"freq"`     // daily | weekly | monthly | hourly | minutely
	Interval int    `json:"interval"` // every N days/weeks/months/hours/minutes
	Weekdays []int  `json:"weekdays"` // 0=Sunday .. 6=Saturday (weekly)
	MonthDay int    `json:"month_day"`
	Time     string `json:"time"`   // HH:MM for daily/weekly/monthly
	Window   string `json:"window"` // HH:MM-HH:MM active window for hourly/minutely
	Until    string `json:"until"`  // YYYY-MM-DD (inclusive), optional
	Count    int    `json:"count"`  // max occurrences, optional
}

var freqMap = map[string]schedule.Freq{
	"daily": schedule.Daily, "weekly": schedule.Weekly, "monthly": schedule.Monthly,
	"hourly": schedule.Hourly, "minutely": schedule.Minutely,
}

// toRule converts the input to a schedule.Rule and the active-window string.
func (in RepeatInput) toRule(loc *time.Location) (schedule.Rule, string, error) {
	r := schedule.Rule{WindowStart: -1, WindowEnd: -1, Interval: in.Interval, MonthDay: in.MonthDay, Count: in.Count}
	f, ok := freqMap[strings.ToLower(strings.TrimSpace(in.Freq))]
	if !ok {
		return r, "", model.Invalid("Repeat: choose daily, weekly, monthly, hourly or minutely")
	}
	r.Freq = f
	if r.Interval == 0 {
		r.Interval = 1
	}
	for _, d := range in.Weekdays {
		if d < 0 || d > 6 {
			return r, "", model.Invalid("Repeat: invalid weekday")
		}
		r.Weekdays = append(r.Weekdays, time.Weekday(d))
	}
	if !r.IsSubDaily() {
		m, err := schedule.ParseHM(in.Time)
		if err != nil {
			return r, "", model.Invalid("Repeat: time of day must be HH:MM")
		}
		r.Hour, r.Minute = m/60, m%60
	}
	window := ""
	if r.IsSubDaily() && strings.TrimSpace(in.Window) != "" {
		w, ok, err := schedule.ParseWindow(in.Window)
		if err != nil {
			return r, "", model.Invalid("Repeat: active window " + err.Error())
		}
		if ok {
			r.WindowStart, r.WindowEnd = w.Start, w.End
			window = w.String()
		}
	}
	if u := strings.TrimSpace(in.Until); u != "" {
		d, err := time.ParseInLocation("2006-01-02", u, loc)
		if err != nil {
			return r, "", model.Invalid("Repeat: end date must be YYYY-MM-DD")
		}
		r.Until = d.AddDate(0, 0, 1).Add(-time.Second).UTC()
	}
	if err := r.Validate(); err != nil {
		return r, "", model.Invalid("Repeat: " + err.Error())
	}
	return r, window, nil
}

// RepeatFromRule converts a stored rule back to the structured form (for edit forms/API).
func RepeatFromRule(rule, window string, loc *time.Location) *RepeatInput {
	if rule == "" {
		return nil
	}
	r, err := schedule.Parse(rule)
	if err != nil {
		return nil
	}
	in := &RepeatInput{Interval: r.Interval, MonthDay: r.MonthDay, Count: r.Count, Window: window,
		Time: schedule.FormatHM(r.Hour*60 + r.Minute)}
	for k, v := range freqMap {
		if v == r.Freq {
			in.Freq = k
		}
	}
	for _, d := range r.Weekdays {
		in.Weekdays = append(in.Weekdays, int(d))
	}
	if !r.Until.IsZero() {
		in.Until = r.Until.In(loc).Format("2006-01-02")
	}
	return in
}

// ParseRule parses a stored rule, attaching the active window.
func ParseRule(rule, window string) (schedule.Rule, error) {
	r, err := schedule.Parse(rule)
	if err != nil {
		return r, err
	}
	if w, ok, _ := schedule.ParseWindow(window); ok {
		r.WindowStart, r.WindowEnd = w.Start, w.End
	}
	return r, nil
}

// DescribeRule returns a human description of a stored rule.
func DescribeRule(rule, window string) string {
	r, err := ParseRule(rule, window)
	if err != nil {
		return ""
	}
	return r.Describe()
}
