package schedule

import (
	"testing"
	"time"
)

var pp = mustLoc("Asia/Phnom_Penh")

func mustLoc(n string) *time.Location {
	l, err := time.LoadLocation(n)
	if err != nil {
		panic(err)
	}
	return l
}

func lt(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, pp)
	if err != nil {
		panic(err)
	}
	return t
}

func check(t *testing.T, got time.Time, want string) {
	t.Helper()
	if want == "" {
		if !got.IsZero() {
			t.Fatalf("want zero, got %s", got.In(pp))
		}
		return
	}
	if !got.Equal(lt(want)) {
		t.Fatalf("want %s, got %s", want, got.In(pp).Format("2006-01-02 15:04 Mon"))
	}
}

func TestDaily(t *testing.T) {
	r := Rule{Freq: Daily, Interval: 1, Hour: 8, Minute: 0, WindowStart: -1, WindowEnd: -1}
	anchor := lt("2026-01-10 00:00")
	check(t, r.Next(anchor, lt("2026-01-10 07:59"), pp), "2026-01-10 08:00")
	check(t, r.Next(anchor, lt("2026-01-10 08:00"), pp), "2026-01-11 08:00")
	check(t, r.Next(anchor, lt("2025-12-01 00:00"), pp), "2026-01-10 08:00")
	// anchor after the time of day on the first day
	check(t, r.Next(lt("2026-01-10 09:00"), lt("2026-01-10 09:00").Add(-time.Second), pp), "2026-01-11 08:00")
}

func TestEveryNDays(t *testing.T) {
	r := Rule{Freq: Daily, Interval: 3, Hour: 6, Minute: 30, WindowStart: -1, WindowEnd: -1}
	anchor := lt("2026-01-01 00:00")
	check(t, r.Next(anchor, lt("2026-01-01 07:00"), pp), "2026-01-04 06:30")
	check(t, r.Next(anchor, lt("2026-01-05 00:00"), pp), "2026-01-07 06:30")
	check(t, r.Next(anchor, lt("2026-02-28 07:00"), pp), "2026-03-02 06:30") // Jan1 + 60d = Mar2
}

func TestWeekdays(t *testing.T) {
	r := Rule{Freq: Weekly, Interval: 1, Weekdays: []time.Weekday{time.Monday, time.Wednesday, time.Friday}, Hour: 9, WindowStart: -1, WindowEnd: -1}
	anchor := lt("2026-10-26 00:00") // Monday
	check(t, r.Next(anchor, lt("2026-10-26 09:00"), pp), "2026-10-28 09:00")
	check(t, r.Next(anchor, lt("2026-10-30 10:00"), pp), "2026-11-02 09:00")
	r.Interval = 2
	check(t, r.Next(anchor, lt("2026-10-30 10:00"), pp), "2026-11-09 09:00")
	// Sunday-only rule, anchor mid-week
	r2 := Rule{Freq: Weekly, Interval: 1, Weekdays: []time.Weekday{time.Sunday}, Hour: 20, WindowStart: -1, WindowEnd: -1}
	check(t, r2.Next(lt("2026-10-28 12:00"), lt("2026-10-28 12:00"), pp), "2026-11-01 20:00")
}

func TestMonthly31st(t *testing.T) {
	r := Rule{Freq: Monthly, Interval: 1, MonthDay: 31, Hour: 10, WindowStart: -1, WindowEnd: -1}
	anchor := lt("2026-01-01 00:00")
	check(t, r.Next(anchor, lt("2026-01-31 10:00"), pp), "2026-02-28 10:00")
	check(t, r.Next(anchor, lt("2026-02-28 10:00"), pp), "2026-03-31 10:00")
	check(t, r.Next(anchor, lt("2026-04-01 00:00"), pp), "2026-04-30 10:00")
	check(t, r.Next(anchor, lt("2027-12-31 10:00"), pp), "2028-01-31 10:00")
	check(t, r.Next(anchor, lt("2028-02-01 00:00"), pp), "2028-02-29 10:00") // leap year
	r.MonthDay = 15
	r.Interval = 2
	check(t, r.Next(anchor, lt("2026-01-15 10:00"), pp), "2026-03-15 10:00")
}

func TestEveryNHoursWithWindow(t *testing.T) {
	r := Rule{Freq: Hourly, Interval: 2, WindowStart: -1, WindowEnd: -1}
	anchor := lt("2026-05-01 08:00")
	check(t, r.Next(anchor, lt("2026-05-01 08:00"), pp), "2026-05-01 10:00")
	check(t, r.Next(anchor, lt("2026-05-01 07:00"), pp), "2026-05-01 08:00")
	r.WindowStart, r.WindowEnd = 8*60, 20*60
	check(t, r.Next(anchor, lt("2026-05-01 19:00"), pp), "2026-05-01 20:00") // window end inclusive
	check(t, r.Next(anchor, lt("2026-05-01 20:00"), pp), "2026-05-02 08:00")
	// Every 90 minutes in a window that wraps midnight
	r3 := Rule{Freq: Minutely, Interval: 90, WindowStart: 22 * 60, WindowEnd: 2 * 60}
	check(t, r3.Next(lt("2026-05-01 00:00"), lt("2026-05-01 02:00"), pp), "2026-05-01 22:30")
}

func TestUntilAndCount(t *testing.T) {
	r := Rule{Freq: Daily, Interval: 1, Hour: 8, WindowStart: -1, WindowEnd: -1, Until: lt("2026-01-03 23:59")}
	anchor := lt("2026-01-01 00:00")
	check(t, r.Next(anchor, lt("2026-01-02 09:00"), pp), "2026-01-03 08:00")
	check(t, r.Next(anchor, lt("2026-01-03 09:00"), pp), "")
	r = Rule{Freq: Daily, Interval: 1, Hour: 8, WindowStart: -1, WindowEnd: -1, Count: 2}
	check(t, r.Next(anchor, lt("2026-01-01 08:00"), pp), "2026-01-02 08:00")
	check(t, r.Next(anchor, lt("2026-01-02 08:00"), pp), "")
	got := r.Between(anchor, lt("2025-01-01 00:00"), lt("2027-01-01 00:00"), pp, 100)
	if len(got) != 2 {
		t.Fatalf("want 2 occurrences, got %d", len(got))
	}
}

func TestRoundTrip(t *testing.T) {
	cases := []string{
		"FREQ=DAILY;BYHOUR=8;BYMINUTE=0",
		"FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,WE,SU;BYHOUR=9;BYMINUTE=15",
		"FREQ=MONTHLY;BYMONTHDAY=31;BYHOUR=10;BYMINUTE=0;COUNT=12",
		"FREQ=HOURLY;INTERVAL=3;UNTIL=20261231T165959Z",
		"FREQ=MINUTELY;INTERVAL=45",
	}
	for _, c := range cases {
		r, err := Parse(c)
		if err != nil {
			t.Fatalf("%s: %v", c, err)
		}
		if r.String() != c {
			t.Fatalf("round trip: %s != %s", r.String(), c)
		}
	}
	for _, bad := range []string{"", "FREQ=YEARLY", "FREQ=WEEKLY;BYHOUR=1;BYMINUTE=0", "FREQ=MONTHLY;BYMONTHDAY=32", "FREQ=MINUTELY;INTERVAL=1", "FREQ=DAILY;FOO=1"} {
		if _, err := Parse(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

func TestQuietHours(t *testing.T) {
	q := ResolveQuiet("22:00-06:30", "", false)
	check(t, q.HoldUntil(lt("2026-01-01 23:15"), pp), "2026-01-02 06:30")
	check(t, q.HoldUntil(lt("2026-01-02 03:00"), pp), "2026-01-02 06:30")
	check(t, q.HoldUntil(lt("2026-01-02 06:30"), pp), "2026-01-02 06:30")
	check(t, q.HoldUntil(lt("2026-01-02 21:59"), pp), "2026-01-02 21:59")
	if ResolveQuiet("22:00-06:30", "off", false).Enabled {
		t.Fatal("override off should disable")
	}
	if ResolveQuiet("22:00-06:30", "", true).Enabled {
		t.Fatal("ignore should disable")
	}
	q = ResolveQuiet("22:00-06:30", "12:00-13:00", false)
	check(t, q.HoldUntil(lt("2026-01-02 12:10"), pp), "2026-01-02 13:00")
	check(t, q.HoldUntil(lt("2026-01-02 23:10"), pp), "2026-01-02 23:10")
}
