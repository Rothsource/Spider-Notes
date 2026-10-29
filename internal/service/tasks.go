package service

import (
	"context"
	"errors"
	"time"

	"github.com/sovannroth/mynotes/internal/markdown"
	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/schedule"
	"github.com/sovannroth/mynotes/internal/store"
)

// TaskInput is used to create or update a task.
type TaskInput struct {
	Title       string       `json:"title"`
	Description string       `json:"description_md"`
	StartAt     *time.Time   `json:"start_at"`
	DueAt       *time.Time   `json:"due_at"`
	Alert       string       `json:"alert"` // see TaskAlerts
	Priority    string       `json:"priority"`
	Tags        []string     `json:"tags"`
	Repeat      *RepeatInput `json:"repeat"`
}

// Task alert values: when the built-in Telegram notification fires.
const (
	AlertNone  = ""
	AlertStart = "start"
	AlertDue   = "due"
)

// TaskAlertOption is one choice of the task alert picker.
type TaskAlertOption struct {
	Value, Label string
}

// TaskAlerts lists the alert choices in display order.
var TaskAlerts = []TaskAlertOption{
	{AlertNone, "None"},
	{AlertStart, "At start time"},
	{AlertDue, "At due time"},
	{"5m", "5 minutes before"},
	{"15m", "15 minutes before"},
	{"30m", "30 minutes before"},
	{"1h", "1 hour before"},
	{"2h", "2 hours before"},
	{"1d", "1 day before"},
}

// alertOffsets maps "before due" alerts to their lead time.
var alertOffsets = map[string]time.Duration{
	AlertDue: 0, "5m": 5 * time.Minute, "15m": 15 * time.Minute, "30m": 30 * time.Minute,
	"1h": time.Hour, "2h": 2 * time.Hour, "1d": 24 * time.Hour,
}

// staleAlert is how late an alert may still be sent after its instance was due (e.g. after downtime).
const staleAlert = time.Hour

func (s *Service) buildTask(in TaskInput, t *model.Task) error {
	var err error
	if t.Title, err = trimLen(in.Title, MaxTitleLen); err != nil {
		return model.Invalid("Title is too long")
	}
	if t.Title == "" {
		return model.Invalid("Title is required")
	}
	if t.DescriptionMD, err = trimLen(in.Description, MaxTextLen); err != nil {
		return model.Invalid("Description is too long")
	}
	switch in.Priority {
	case "":
		t.Priority = model.PriorityNormal
	case model.PriorityLow, model.PriorityNormal, model.PriorityHigh:
		t.Priority = in.Priority
	default:
		return model.Invalid("Priority must be low, normal or high")
	}
	t.Tags = markdown.NormalizeTags(in.Tags)
	if in.StartAt != nil && in.DueAt != nil && in.StartAt.After(*in.DueAt) {
		return model.Invalid("Start must be before the due time")
	}
	if in.Alert != AlertNone {
		if _, ok := alertOffsets[in.Alert]; !ok && in.Alert != AlertStart {
			return model.Invalid("Unknown alert option")
		}
		if in.DueAt == nil {
			return model.Invalid("An alert needs a due date and time")
		}
		if in.Alert == AlertStart && in.StartAt == nil {
			return model.Invalid("“At start time” needs a start time")
		}
	}
	oldRule, oldStart := t.RepeatRule, t.RepeatStartAt
	alertChanged := t.ID == 0 || t.Alert != in.Alert || !sameTime(t.StartAt, in.StartAt) || !sameTime(t.DueAt, in.DueAt)
	t.StartAt, t.Alert = in.StartAt, in.Alert
	t.DueAt = in.DueAt
	t.RepeatRule = ""
	t.RepeatStartAt = nil
	if in.Repeat != nil && in.Repeat.Freq != "" {
		if in.DueAt == nil {
			return model.Invalid("A repeating task needs a due date and time")
		}
		rep := *in.Repeat
		due := in.DueAt.In(s.loc)
		rep.Time = due.Format("15:04")
		if rep.MonthDay == 0 {
			rep.MonthDay = due.Day()
		}
		if len(rep.Weekdays) == 0 {
			rep.Weekdays = []int{int(due.Weekday())}
		}
		rule, _, err := rep.toRule(s.loc)
		if err != nil {
			return err
		}
		if rule.IsSubDaily() {
			return model.Invalid("Tasks can repeat daily, weekly or monthly")
		}
		t.RepeatRule = rule.String()
		if t.RepeatRule == oldRule && oldStart != nil {
			t.RepeatStartAt = oldStart // keep phase/count anchor
		} else {
			t.RepeatStartAt = ptr(s.StartOfDay(due))
		}
	}
	if alertChanged || t.RepeatRule != oldRule {
		t.NextAlertAt = s.nextAlert(t, s.now().UTC())
	}
	return nil
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// alertLead is how long before an instance's due time the alert fires.
func alertLead(t *model.Task) (time.Duration, bool) {
	if t.Alert == AlertNone || t.DueAt == nil {
		return 0, false
	}
	if t.Alert == AlertStart {
		if t.StartAt == nil {
			return 0, false
		}
		return t.DueAt.Sub(*t.StartAt), true
	}
	d, ok := alertOffsets[t.Alert]
	return d, ok
}

// nextAlert returns the alert time for the first instance due after `after`
// (the current due time, then later repeats), or nil when there is none.
// The result may lie in the past while the instance is still ahead: the alert then fires at once.
func (s *Service) nextAlert(t *model.Task, after time.Time) *time.Time {
	lead, ok := alertLead(t)
	if !ok || t.Status != model.TaskPending {
		return nil
	}
	due := *t.DueAt
	if !due.After(after) {
		if t.RepeatRule == "" || t.RepeatStartAt == nil {
			return nil
		}
		rule, err := schedule.Parse(t.RepeatRule)
		if err != nil {
			return nil
		}
		for i := 0; i < 100_000 && !due.IsZero() && !due.After(after); i++ {
			due = rule.Next(*t.RepeatStartAt, due, s.loc)
		}
		if due.IsZero() || !due.After(after) {
			return nil
		}
	}
	return ptr(due.Add(-lead).UTC())
}

// moveDue sets a new due time; the start time keeps its distance to the due time.
func moveDue(t *model.Task, next time.Time) {
	if t.StartAt != nil && t.DueAt != nil {
		t.StartAt = ptr(t.StartAt.Add(next.Sub(*t.DueAt)).UTC())
	}
	t.DueAt = ptr(next.UTC())
}

// processTaskAlerts queues the Telegram alert of tasks whose alert time has come.
// The alert is the occurrence row of the instance's due time with next_send_at set,
// so it is delivered, snoozed and acted on like a reminder.
func (s *Service) processTaskAlerts(ctx context.Context, now time.Time) error {
	tasks, err := s.st.ListTaskAlertsDue(ctx, now)
	if err != nil {
		return err
	}
	for i := range tasks {
		id := tasks[i].ID
		err := s.st.InTx(ctx, func(tx store.Store) error {
			t, err := liveTask(ctx, tx, id)
			if err != nil {
				return err
			}
			if t.Status != model.TaskPending || t.NextAlertAt == nil || t.NextAlertAt.After(now) {
				return nil // changed concurrently
			}
			lead, ok := alertLead(t)
			if !ok {
				t.NextAlertAt = nil
				return tx.UpdateTask(ctx, t)
			}
			due := t.NextAlertAt.Add(lead) // the instance this alert belongs to
			if now.Sub(due) <= staleAlert {
				o, err := tx.FindOccurrence(ctx, 0, t.ID, due)
				switch {
				case errors.Is(err, model.ErrNotFound):
					_, err = tx.InsertOccurrence(ctx, &model.Occurrence{TaskID: &t.ID, ScheduledFor: due, Status: model.OccPending,
						NextSendAt: &now, CreatedAt: now, UpdatedAt: now})
				case err == nil && o.IsOpen() && o.SentCount == 0 && o.NextSendAt == nil:
					o.NextSendAt, o.UpdatedAt = &now, now
					err = tx.UpdateOccurrence(ctx, o)
				}
				if err != nil {
					return err
				}
			}
			t.NextAlertAt = s.nextAlert(t, due)
			return tx.UpdateTask(ctx, t)
		})
		logErr("task alert", err, "task", id)
	}
	return nil
}

// CreateTask creates a task.
func (s *Service) CreateTask(ctx context.Context, in TaskInput) (*model.Task, error) {
	now := s.now().UTC()
	t := &model.Task{Status: model.TaskPending, CreatedAt: now, UpdatedAt: now}
	if err := s.buildTask(in, t); err != nil {
		return nil, err
	}
	err := s.st.InTx(ctx, func(tx store.Store) error {
		if err := tx.CreateTask(ctx, t); err != nil {
			return err
		}
		return tx.SetTaskTags(ctx, t.ID, t.Tags)
	})
	return t, err
}

// UpdateTask edits a task.
func (s *Service) UpdateTask(ctx context.Context, id int64, in TaskInput) (*model.Task, error) {
	var t *model.Task
	err := s.st.InTx(ctx, func(tx store.Store) error {
		var err error
		if t, err = liveTask(ctx, tx, id); err != nil {
			return err
		}
		if err := s.buildTask(in, t); err != nil {
			return err
		}
		t.UpdatedAt = s.now().UTC()
		if err := tx.UpdateTask(ctx, t); err != nil {
			return err
		}
		return tx.SetTaskTags(ctx, t.ID, t.Tags)
	})
	return t, err
}

func liveTask(ctx context.Context, st store.Store, id int64) (*model.Task, error) {
	t, err := st.GetTask(ctx, id)
	if err != nil {
		return nil, err
	}
	if t.DeletedAt != nil {
		return nil, model.ErrNotFound
	}
	return t, nil
}

// GetTask returns a non-deleted task.
func (s *Service) GetTask(ctx context.Context, id int64) (*model.Task, error) {
	return liveTask(ctx, s.st, id)
}

// DeleteTask soft-deletes a task and pauses its reminders (history is kept).
func (s *Service) DeleteTask(ctx context.Context, id int64) error {
	now := s.now().UTC()
	return s.st.InTx(ctx, func(tx store.Store) error {
		if err := tx.SoftDeleteTask(ctx, id, now); err != nil {
			return err
		}
		rems, err := tx.ListReminders(ctx, id)
		if err != nil {
			return err
		}
		for i := range rems {
			if err := s.deleteReminderTx(ctx, tx, rems[i].ID, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListTasks lists tasks. Range values: today, week, overdue, nodate.
func (s *Service) ListTasks(ctx context.Context, f model.TaskFilter) ([]model.Task, error) {
	now := s.Now()
	sod := s.StartOfDay(now)
	switch f.Range {
	case "today":
		f.From, f.To = &sod, ptr(sod.AddDate(0, 0, 1))
	case "week":
		wd := (int(sod.Weekday()) + 6) % 7
		start := sod.AddDate(0, 0, -wd)
		f.From, f.To = &start, ptr(start.AddDate(0, 0, 7))
	case "overdue":
		f.Before = &now
	case "nodate":
		f.NoDate = true
	}
	if f.Query != "" {
		f.Query = FTSQuery(f.Query)
		if f.Query == "" {
			return nil, nil
		}
	}
	return s.st.ListTasks(ctx, f)
}

// CompleteTask marks a task (or the current occurrence of a repeating task) done.
func (s *Service) CompleteTask(ctx context.Context, id int64, source string) (*model.Task, error) {
	var t *model.Task
	err := s.st.InTx(ctx, func(tx store.Store) error {
		var err error
		t, err = s.finishTaskTx(ctx, tx, id, model.OccDone, source)
		return err
	})
	return t, err
}

// SkipTask skips the current occurrence of a repeating task.
func (s *Service) SkipTask(ctx context.Context, id int64, source string) (*model.Task, error) {
	var t *model.Task
	err := s.st.InTx(ctx, func(tx store.Store) error {
		var err error
		t, err = s.finishTaskTx(ctx, tx, id, model.OccSkipped, source)
		return err
	})
	return t, err
}

func (s *Service) finishTaskTx(ctx context.Context, tx store.Store, id int64, status, source string) (*model.Task, error) {
	now := s.now().UTC()
	t, err := liveTask(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if t.Status == model.TaskDone {
		return t, model.ErrConflict
	}
	if status == model.OccSkipped && t.RepeatRule == "" {
		return nil, model.Invalid("Only repeating tasks can be skipped")
	}
	scheduled := now
	if t.DueAt != nil {
		scheduled = *t.DueAt
	}
	// Record the outcome on the occurrence row for this due instance.
	o, err := tx.FindOccurrence(ctx, 0, t.ID, scheduled)
	if errors.Is(err, model.ErrNotFound) {
		o = &model.Occurrence{TaskID: &t.ID, ScheduledFor: scheduled, CreatedAt: now}
		err = nil
	}
	if err != nil {
		return nil, err
	}
	o.Status, o.DoneAt, o.ActionSource, o.NextSendAt, o.UpdatedAt = status, &now, source, nil, now
	if o.ID == 0 {
		if _, err := tx.InsertOccurrence(ctx, o); err != nil {
			return nil, err
		}
	} else if err := tx.UpdateOccurrence(ctx, o); err != nil {
		return nil, err
	}
	if o.TelegramMessageID != 0 {
		s.notify().Finalize(o.TelegramChatID, o.TelegramMessageID, s.finalText(ctx, tx, o))
	}

	if t.RepeatRule != "" && t.DueAt != nil && t.RepeatStartAt != nil {
		rule, err := schedule.Parse(t.RepeatRule)
		if err != nil {
			return nil, err
		}
		next := rule.Next(*t.RepeatStartAt, *t.DueAt, s.loc)
		if next.IsZero() {
			t.Status = model.TaskDone // series finished
		} else {
			moveDue(t, next)
		}
		if status == model.OccDone {
			t.CompletedAt = &now
		}
	} else {
		t.Status = model.TaskDone
		t.CompletedAt = &now
	}
	t.NextAlertAt = s.nextAlert(t, now)
	t.UpdatedAt = now
	if err := tx.UpdateTask(ctx, t); err != nil {
		return nil, err
	}
	// Stop nags of reminders attached to this task.
	if err := s.closeTaskReminderOccs(ctx, tx, t.ID, status, source, now); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Service) closeTaskReminderOccs(ctx context.Context, tx store.Store, taskID int64, status, source string, now time.Time) error {
	rems, err := tx.ListReminders(ctx, taskID)
	if err != nil {
		return err
	}
	for _, r := range rems {
		occs, err := tx.ListOccurrences(ctx, model.OccurrenceFilter{ReminderID: r.ID, Statuses: openStatuses})
		if err != nil {
			return err
		}
		for i := range occs {
			if err := s.closeOcc(ctx, tx, &occs[i], status, source, now); err != nil {
				return err
			}
		}
	}
	return nil
}

// UncompleteTask reopens a completed non-repeating task (correcting a mistaken tick).
func (s *Service) UncompleteTask(ctx context.Context, id int64) (*model.Task, error) {
	var t *model.Task
	err := s.st.InTx(ctx, func(tx store.Store) error {
		var err error
		if t, err = liveTask(ctx, tx, id); err != nil {
			return err
		}
		if t.Status != model.TaskDone {
			return model.ErrConflict
		}
		now := s.now().UTC()
		occs, err := tx.ListOccurrences(ctx, model.OccurrenceFilter{TaskID: id, Kind: "task", Statuses: []string{model.OccDone}, Limit: 1})
		if err != nil {
			return err
		}
		if len(occs) > 0 {
			o := occs[0]
			o.Status, o.DoneAt, o.ActionSource, o.UpdatedAt = model.OccPending, nil, "", now
			if err := tx.UpdateOccurrence(ctx, &o); err != nil {
				return err
			}
		}
		t.Status, t.CompletedAt, t.UpdatedAt = model.TaskPending, nil, now
		t.NextAlertAt = s.nextAlert(t, now)
		return tx.UpdateTask(ctx, t)
	})
	return t, err
}

// processTasks creates occurrence rows for due tasks and advances repeating
// tasks whose next occurrence has already arrived (marking the old one missed).
func (s *Service) processTasks(ctx context.Context, now time.Time) error {
	tasks, err := s.st.ListTasksDue(ctx, now)
	if err != nil {
		return err
	}
	for i := range tasks {
		t := tasks[i]
		err := s.st.InTx(ctx, func(tx store.Store) error {
			if _, err := tx.InsertOccurrence(ctx, &model.Occurrence{TaskID: &t.ID, ScheduledFor: *t.DueAt, Status: model.OccPending, CreatedAt: now, UpdatedAt: now}); err != nil {
				return err
			}
			if t.RepeatRule == "" || t.RepeatStartAt == nil {
				return nil
			}
			rule, err := schedule.Parse(t.RepeatRule)
			if err != nil {
				return err
			}
			latest := *t.DueAt
			var passed []time.Time
			for n := rule.Next(*t.RepeatStartAt, latest, s.loc); !n.IsZero() && !n.After(now) && len(passed) < 1000; n = rule.Next(*t.RepeatStartAt, n, s.loc) {
				passed = append(passed, latest)
				latest = n
			}
			if len(passed) == 0 {
				return nil
			}
			for j, p := range passed {
				if j < len(passed)-50 { // keep history bounded after long downtime
					continue
				}
				o, err := tx.FindOccurrence(ctx, 0, t.ID, p)
				if errors.Is(err, model.ErrNotFound) {
					o = &model.Occurrence{TaskID: &t.ID, ScheduledFor: p, CreatedAt: now}
					err = nil
				}
				if err != nil {
					return err
				}
				if o.ID != 0 {
					// closeOcc also removes the buttons of an alert sent for this instance.
					if err := s.closeOcc(ctx, tx, o, model.OccMissed, model.SourceSystem, now); err != nil {
						return err
					}
					continue
				}
				o.Status, o.ActionSource, o.UpdatedAt = model.OccMissed, model.SourceSystem, now
				if _, err := tx.InsertOccurrence(ctx, o); err != nil {
					return err
				}
			}
			moveDue(&t, latest)
			if t.NextAlertAt == nil || !t.NextAlertAt.After(now) {
				t.NextAlertAt = s.nextAlert(&t, now)
			}
			t.UpdatedAt = now
			if err := tx.UpdateTask(ctx, &t); err != nil {
				return err
			}
			_, err = tx.InsertOccurrence(ctx, &model.Occurrence{TaskID: &t.ID, ScheduledFor: latest, Status: model.OccPending, CreatedAt: now, UpdatedAt: now})
			return err
		})
		logErr("process task", err, "task", t.ID)
	}
	return nil
}
