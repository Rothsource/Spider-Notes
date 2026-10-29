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
	DueAt       *time.Time   `json:"due_at"`
	Priority    string       `json:"priority"`
	Tags        []string     `json:"tags"`
	Repeat      *RepeatInput `json:"repeat"`
}

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
	oldRule, oldStart := t.RepeatRule, t.RepeatStartAt
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

	if t.RepeatRule != "" && t.DueAt != nil && t.RepeatStartAt != nil {
		rule, err := schedule.Parse(t.RepeatRule)
		if err != nil {
			return nil, err
		}
		next := rule.Next(*t.RepeatStartAt, *t.DueAt, s.loc)
		if next.IsZero() {
			t.Status = model.TaskDone // series finished
		} else {
			t.DueAt = ptr(next.UTC())
		}
		if status == model.OccDone {
			t.CompletedAt = &now
		}
	} else {
		t.Status = model.TaskDone
		t.CompletedAt = &now
	}
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
				if o.ID != 0 && !o.IsOpen() {
					continue
				}
				o.Status, o.ActionSource, o.UpdatedAt = model.OccMissed, model.SourceSystem, now
				if o.ID == 0 {
					_, err = tx.InsertOccurrence(ctx, o)
				} else {
					err = tx.UpdateOccurrence(ctx, o)
				}
				if err != nil {
					return err
				}
			}
			t.DueAt = ptr(latest.UTC())
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
