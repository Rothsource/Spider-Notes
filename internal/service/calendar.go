package service

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/schedule"
)

// CalItem is one entry on the calendar (expanded occurrence).
type CalItem struct {
	Kind         string    `json:"kind"` // task | reminder
	ID           int64     `json:"id"`   // task or reminder id
	OccurrenceID int64     `json:"occurrence_id,omitempty"`
	Title        string    `json:"title"`
	At           time.Time `json:"at"`
	Status       string    `json:"status"` // pending|overdue|done|skipped|missed|sent|snoozed|paused|upcoming
	Repeating    bool      `json:"repeating"`
	Priority     string    `json:"priority,omitempty"`
	Count        int       `json:"count,omitempty"` // >1 when several sub-daily firings are collapsed
}

// Calendar is the expanded content of a date range.
type Calendar struct {
	From     time.Time      `json:"from"`
	To       time.Time      `json:"to"`
	Items    []CalItem      `json:"items"`
	MemoDays map[string]int `json:"memo_days"` // YYYY-MM-DD -> memo count
}

const maxPerSeries = 400

// Calendar expands tasks, reminders and memo markers in [from, to).
func (s *Service) Calendar(ctx context.Context, from, to time.Time) (*Calendar, error) {
	if !to.After(from) || to.Sub(from) > 100*24*time.Hour {
		return nil, model.Invalid("Calendar range must be between 1 and 100 days")
	}
	now := s.now().UTC()
	cal := &Calendar{From: from.In(s.loc), To: to.In(s.loc), MemoDays: map[string]int{}}
	add := func(it CalItem) { cal.Items = append(cal.Items, it) }

	// Recorded occurrences (history + open ones) in range.
	occs, err := s.st.ListOccurrences(ctx, model.OccurrenceFilter{From: &from, To: &to, Limit: 5000})
	if err != nil {
		return nil, err
	}
	recorded := map[string]bool{}
	for _, o := range occs {
		key := ""
		it := CalItem{OccurrenceID: o.ID, Title: o.Title, At: o.ScheduledFor.In(s.loc), Status: o.Status, Repeating: o.Repeating}
		if o.ReminderID != nil {
			it.Kind, it.ID = "reminder", *o.ReminderID
			key = fmt.Sprintf("r%d@%d", it.ID, o.ScheduledFor.Unix())
		} else {
			it.Kind, it.ID = "task", *o.TaskID
			key = fmt.Sprintf("t%d@%d", it.ID, o.ScheduledFor.Unix())
		}
		recorded[key] = true
		if it.Kind == "task" && o.IsOpen() {
			continue // shown from the task row below (with overdue state)
		}
		add(it)
	}

	// Tasks.
	tasks, err := s.st.ListTasks(ctx, model.TaskFilter{Status: "all", Limit: 1000})
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		if t.DueAt == nil {
			continue
		}
		base := CalItem{Kind: "task", ID: t.ID, Title: t.Title, Repeating: t.RepeatRule != "", Priority: t.Priority}
		if !t.DueAt.Before(from) && t.DueAt.Before(to) {
			it := base
			it.At = t.DueAt.In(s.loc)
			switch {
			case t.Status == model.TaskDone:
				it.Status = model.OccDone
			case t.DueAt.Before(now):
				it.Status = "overdue"
			default:
				it.Status = model.OccPending
			}
			// A completed task is already shown via its occurrence row.
			if !(t.Status == model.TaskDone && recorded[fmt.Sprintf("t%d@%d", t.ID, t.DueAt.Unix())]) {
				add(it)
			}
		}
		if t.RepeatRule != "" && t.Status == model.TaskPending && t.RepeatStartAt != nil {
			rule, err := schedule.Parse(t.RepeatRule)
			if err != nil {
				continue
			}
			start := from
			if t.DueAt.After(start) || t.DueAt.Equal(start) {
				start = t.DueAt.Add(time.Second)
			}
			for _, at := range rule.Between(*t.RepeatStartAt, start, to, s.loc, maxPerSeries) {
				it := base
				it.At, it.Status = at, "upcoming"
				add(it)
			}
		}
	}

	// Reminders: projected future firings.
	rems, err := s.st.ListReminders(ctx, 0)
	if err != nil {
		return nil, err
	}
	for i := range rems {
		r := &rems[i]
		status := "upcoming"
		if !r.Enabled {
			status = "paused"
		}
		title := firstLineOr(r.MessageMD, r.TaskTitle)
		base := CalItem{Kind: "reminder", ID: r.ID, Title: title, Repeating: r.ScheduleType == model.Repeating, Status: status}
		var times []time.Time
		if r.ScheduleType == model.OneTime {
			next := r.NextFireAt
			if !r.Enabled && r.FireCount == 0 {
				next = &r.FireAt
			}
			if next != nil && !next.Before(from) && next.Before(to) {
				times = append(times, *next)
			}
		} else {
			rule, err := ParseRule(r.RepeatRule, r.ActiveWindow)
			if err != nil {
				continue
			}
			start := from
			if r.Enabled && r.NextFireAt != nil && r.NextFireAt.After(start) {
				start = *r.NextFireAt
			} else if r.Enabled && r.NextFireAt == nil {
				continue // series finished
			}
			if start.Before(now) {
				start = now
			}
			times = rule.Between(r.FireAt, start, to, s.loc, maxPerSeries)
		}
		for _, at := range times {
			if recorded[fmt.Sprintf("r%d@%d", r.ID, at.Unix())] {
				continue
			}
			it := base
			it.At = at.In(s.loc)
			add(it)
		}
	}

	cal.Items = collapseSubDaily(cal.Items, s.loc)
	sort.SliceStable(cal.Items, func(i, j int) bool { return cal.Items[i].At.Before(cal.Items[j].At) })

	memos, err := s.st.ListMemos(ctx, model.MemoFilter{From: &from, To: &to, Limit: 5000}, false)
	if err != nil {
		return nil, err
	}
	for _, m := range memos {
		cal.MemoDays[m.CreatedAt.In(s.loc).Format("2006-01-02")]++
	}
	return cal, nil
}

// collapseSubDaily merges many firings of the same reminder on one day with the
// same status (e.g. "every 15 minutes") into a single item with a count.
func collapseSubDaily(items []CalItem, loc *time.Location) []CalItem {
	type key struct {
		kind, day, status string
		id                int64
	}
	counts := map[key]int{}
	for _, it := range items {
		counts[key{it.Kind, it.At.In(loc).Format("2006-01-02"), it.Status, it.ID}]++
	}
	seen := map[key]bool{}
	var out []CalItem
	for _, it := range items {
		k := key{it.Kind, it.At.In(loc).Format("2006-01-02"), it.Status, it.ID}
		if n := counts[k]; n > 6 {
			if seen[k] {
				continue
			}
			seen[k] = true
			it.Count = n
		}
		out = append(out, it)
	}
	return out
}

func firstLineOr(msg, alt string) string {
	if l := firstLine(msg); l != "" {
		return l
	}
	return alt
}

// DayDetail lists everything on one local day (calendar side panel).
type DayDetail struct {
	Day   time.Time
	Items []CalItem
	Memos []model.Memo
}

// Day returns calendar items and memos for one local day.
func (s *Service) Day(ctx context.Context, day time.Time) (*DayDetail, error) {
	from := s.StartOfDay(day)
	to := from.AddDate(0, 0, 1)
	cal, err := s.Calendar(ctx, from, to)
	if err != nil {
		return nil, err
	}
	memos, err := s.st.ListMemos(ctx, model.MemoFilter{From: &from, To: &to, Limit: 500}, false)
	if err != nil {
		return nil, err
	}
	return &DayDetail{Day: from, Items: cal.Items, Memos: memos}, nil
}
