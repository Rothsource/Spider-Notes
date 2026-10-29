package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/sovannroth/mynotes/internal/markdown"
	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/schedule"
	"github.com/sovannroth/mynotes/internal/store"
)

// ReminderInput is used to create or update a reminder.
type ReminderInput struct {
	TaskID             *int64       `json:"task_id"`
	Message            string       `json:"message_md"`
	FireAt             time.Time    `json:"fire_at"` // one-time: when; repeating: series start
	Repeat             *RepeatInput `json:"repeat"`  // nil = one-time
	NagEnabled         bool         `json:"nag_enabled"`
	NagIntervalMin     int          `json:"nag_interval_min"`
	QuietHoursOverride string       `json:"quiet_hours_override"`
	IgnoreQuietHours   bool         `json:"ignore_quiet_hours"`
	Enabled            *bool        `json:"enabled"`
	Tags               []string     `json:"tags"`
}

func (s *Service) buildReminder(ctx context.Context, tx store.Store, in ReminderInput, r *model.Reminder, now time.Time) error {
	var err error
	if r.MessageMD, err = trimLen(in.Message, MaxMessageLen); err != nil {
		return model.Invalid("Message is too long (max 4000 characters)")
	}
	if r.MessageMD == "" && in.TaskID == nil {
		return model.Invalid("Message is required")
	}
	if in.TaskID != nil && *in.TaskID > 0 {
		if _, err := liveTask(ctx, tx, *in.TaskID); err != nil {
			return model.Invalid("Linked task not found")
		}
		r.TaskID = in.TaskID
	} else {
		r.TaskID = nil
	}
	if in.FireAt.IsZero() {
		return model.Invalid("Date and time are required")
	}
	if in.NagEnabled && (in.NagIntervalMin < 1 || in.NagIntervalMin > 1440) {
		return model.Invalid("Nag interval must be between 1 and 1440 minutes")
	}
	if in.NagIntervalMin <= 0 {
		in.NagIntervalMin = 30
	}
	q := strings.TrimSpace(in.QuietHoursOverride)
	if q != "" && !strings.EqualFold(q, "off") {
		w, ok, err := schedule.ParseWindow(q)
		if err != nil || !ok {
			return model.Invalid("Quiet hours override must be empty, 'off' or HH:MM-HH:MM")
		}
		q = w.String()
	}
	r.NagEnabled, r.NagIntervalMin, r.QuietHoursOverride, r.IgnoreQuietHours = in.NagEnabled, in.NagIntervalMin, strings.ToLower(q), in.IgnoreQuietHours
	if in.Enabled != nil {
		r.Enabled = *in.Enabled
	}
	r.Tags = markdown.NormalizeTags(in.Tags)
	r.Timezone = s.loc.String()

	oldType, oldFire, oldRule, oldWindow := r.ScheduleType, r.FireAt, r.RepeatRule, r.ActiveWindow
	r.FireAt = in.FireAt.UTC().Truncate(time.Second)
	if in.Repeat == nil || in.Repeat.Freq == "" {
		r.ScheduleType, r.RepeatRule, r.ActiveWindow = model.OneTime, "", ""
	} else {
		rule, window, err := in.Repeat.toRule(s.loc)
		if err != nil {
			return err
		}
		r.ScheduleType, r.RepeatRule, r.ActiveWindow = model.Repeating, rule.String(), window
	}
	changed := r.ID == 0 || oldType != r.ScheduleType || !oldFire.Equal(r.FireAt) || oldRule != r.RepeatRule || oldWindow != r.ActiveWindow
	if changed {
		if r.ScheduleType == model.OneTime && r.FireAt.Before(now.Add(-time.Minute)) {
			return model.Invalid("The reminder time is in the past")
		}
		r.FireCount = 0
		r.NextFireAt = s.computeNext(r, now)
		if r.ScheduleType == model.Repeating && r.NextFireAt == nil {
			return model.Invalid("This schedule has no future occurrences")
		}
	}
	return nil
}

// computeNext returns the next fire time from now (nil when finished).
func (s *Service) computeNext(r *model.Reminder, now time.Time) *time.Time {
	if r.ScheduleType == model.OneTime {
		if r.FireCount > 0 {
			return nil
		}
		return ptr(r.FireAt)
	}
	rule, err := ParseRule(r.RepeatRule, r.ActiveWindow)
	if err != nil {
		return nil
	}
	n := rule.Next(r.FireAt, now.Add(-time.Second), s.loc)
	if n.IsZero() {
		return nil
	}
	return ptr(n.UTC())
}

// CreateReminder creates a reminder.
func (s *Service) CreateReminder(ctx context.Context, in ReminderInput) (*model.Reminder, error) {
	now := s.now().UTC()
	r := &model.Reminder{Enabled: true, CreatedAt: now, UpdatedAt: now}
	err := s.st.InTx(ctx, func(tx store.Store) error {
		if err := s.buildReminder(ctx, tx, in, r, now); err != nil {
			return err
		}
		if err := tx.CreateReminder(ctx, r); err != nil {
			return err
		}
		return tx.SetReminderTags(ctx, r.ID, r.Tags)
	})
	if err != nil {
		return nil, err
	}
	return s.st.GetReminder(ctx, r.ID)
}

// UpdateReminder edits a reminder; schedule changes recompute next_fire_at immediately.
func (s *Service) UpdateReminder(ctx context.Context, id int64, in ReminderInput) (*model.Reminder, error) {
	now := s.now().UTC()
	err := s.st.InTx(ctx, func(tx store.Store) error {
		r, err := liveReminder(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := s.buildReminder(ctx, tx, in, r, now); err != nil {
			return err
		}
		r.UpdatedAt = now
		if err := tx.UpdateReminder(ctx, r); err != nil {
			return err
		}
		return tx.SetReminderTags(ctx, r.ID, r.Tags)
	})
	if err != nil {
		return nil, err
	}
	return s.st.GetReminder(ctx, id)
}

func liveReminder(ctx context.Context, st store.Store, id int64) (*model.Reminder, error) {
	r, err := st.GetReminder(ctx, id)
	if err != nil {
		return nil, err
	}
	if r.DeletedAt != nil {
		return nil, model.ErrNotFound
	}
	return r, nil
}

// GetReminder returns a non-deleted reminder.
func (s *Service) GetReminder(ctx context.Context, id int64) (*model.Reminder, error) {
	return liveReminder(ctx, s.st, id)
}

// ListReminders lists reminders (taskID > 0 filters to one task).
func (s *Service) ListReminders(ctx context.Context, taskID int64) ([]model.Reminder, error) {
	return s.st.ListReminders(ctx, taskID)
}

// SetReminderEnabled pauses or resumes a reminder. Resuming schedules from now
// (occurrences missed while paused are not sent).
func (s *Service) SetReminderEnabled(ctx context.Context, id int64, enabled bool) (*model.Reminder, error) {
	now := s.now().UTC()
	err := s.st.InTx(ctx, func(tx store.Store) error {
		r, err := liveReminder(ctx, tx, id)
		if err != nil {
			return err
		}
		if r.Enabled == enabled {
			return nil
		}
		r.Enabled, r.UpdatedAt = enabled, now
		if enabled {
			r.NextFireAt = s.computeNext(r, now)
			if r.ScheduleType == model.OneTime && r.NextFireAt != nil && r.NextFireAt.Before(now) {
				// A one-time reminder whose time passed while paused fires once, now.
				r.NextFireAt = &now
			}
		}
		return tx.UpdateReminder(ctx, r)
	})
	if err != nil {
		return nil, err
	}
	return s.st.GetReminder(ctx, id)
}

// DeleteReminder soft-deletes a reminder; open occurrences are closed as skipped.
func (s *Service) DeleteReminder(ctx context.Context, id int64) error {
	now := s.now().UTC()
	return s.st.InTx(ctx, func(tx store.Store) error { return s.deleteReminderTx(ctx, tx, id, now) })
}

func (s *Service) deleteReminderTx(ctx context.Context, tx store.Store, id int64, now time.Time) error {
	if err := tx.SoftDeleteReminder(ctx, id, now); err != nil {
		return err
	}
	occs, err := tx.ListOccurrences(ctx, model.OccurrenceFilter{ReminderID: id, Statuses: openStatuses})
	if err != nil {
		return err
	}
	for i := range occs {
		if err := s.closeOcc(ctx, tx, &occs[i], model.OccSkipped, model.SourceSystem, now); err != nil {
			return err
		}
	}
	return nil
}

var openStatuses = []string{model.OccPending, model.OccSent, model.OccSnoozed}

// closeOcc moves an open occurrence to a final status and finalizes its Telegram message.
func (s *Service) closeOcc(ctx context.Context, tx store.Store, o *model.Occurrence, status, source string, now time.Time) error {
	if !o.IsOpen() {
		return nil
	}
	o.Status, o.ActionSource, o.NextSendAt, o.UpdatedAt = status, source, nil, now
	if status == model.OccDone || status == model.OccSkipped {
		o.DoneAt = &now
	}
	if err := tx.UpdateOccurrence(ctx, o); err != nil {
		return err
	}
	if o.TelegramMessageID != 0 {
		s.notify().Finalize(o.TelegramChatID, o.TelegramMessageID, s.finalText(ctx, tx, o))
	}
	return nil
}

// ---- occurrence actions ----

// Snooze durations accepted by OccurrenceAction.
const (
	ActionDone     = "done"
	ActionSkip     = "skip"
	ActionSnooze10 = "snooze10"
	ActionSnooze30 = "snooze30"
	ActionSnooze60 = "snooze60"
	ActionSnoozeAM = "snooze_morning"
)

// ErrAlreadyHandled is returned when acting on a closed occurrence.
var ErrAlreadyHandled = errors.Join(model.ErrConflict, errors.New("this reminder was already handled"))

// OccurrenceAction applies done/snooze/skip to an occurrence.
func (s *Service) OccurrenceAction(ctx context.Context, id int64, action, source string) (*model.Occurrence, error) {
	now := s.now().UTC()
	set, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	var res *model.Occurrence
	err = s.st.InTx(ctx, func(tx store.Store) error {
		o, err := tx.GetOccurrence(ctx, id)
		if err != nil {
			return err
		}
		res = o
		if !o.IsOpen() {
			return ErrAlreadyHandled
		}
		switch action {
		case ActionSnooze10, ActionSnooze30, ActionSnooze60, ActionSnoozeAM:
			until := s.snoozeUntil(action, set, now)
			o.Status, o.SnoozeCount, o.SnoozedUntil, o.NextSendAt, o.ActionSource, o.UpdatedAt = model.OccSnoozed, o.SnoozeCount+1, &until, &until, source, now
			if err := tx.UpdateOccurrence(ctx, o); err != nil {
				return err
			}
			if o.TelegramMessageID != 0 {
				s.notify().Finalize(o.TelegramChatID, o.TelegramMessageID, s.finalText(ctx, tx, o))
			}
			return nil
		}
		if o.ReminderID == nil { // task occurrence (due instance or task alert)
			switch action {
			case ActionDone:
				_, err = s.finishTaskTx(ctx, tx, *o.TaskID, model.OccDone, source)
			case ActionSkip:
				_, err = s.finishTaskTx(ctx, tx, *o.TaskID, model.OccSkipped, source)
			default:
				return model.Invalid("Unknown action")
			}
			if err != nil {
				return err
			}
			res, err = tx.GetOccurrence(ctx, id)
			return err
		}
		switch action {
		case ActionDone:
			if err := s.closeOcc(ctx, tx, o, model.OccDone, source, now); err != nil {
				return err
			}
			return s.completeLinkedTask(ctx, tx, o, source)
		case ActionSkip:
			if !o.Repeating {
				return model.Invalid("Only repeating reminders can be skipped")
			}
			return s.closeOcc(ctx, tx, o, model.OccSkipped, source, now)
		default:
			return model.Invalid("Unknown action")
		}
	})
	return res, err
}

func (s *Service) snoozeUntil(action string, set Settings, now time.Time) time.Time {
	switch action {
	case ActionSnooze10:
		return now.Add(10 * time.Minute)
	case ActionSnooze30:
		return now.Add(30 * time.Minute)
	case ActionSnooze60:
		return now.Add(time.Hour)
	}
	m, err := schedule.ParseHM(set.SnoozeMorning)
	if err != nil {
		m = 7 * 60
	}
	l := now.In(s.loc)
	return time.Date(l.Year(), l.Month(), l.Day()+1, m/60, m%60, 0, 0, s.loc).UTC()
}

// completeLinkedTask completes the task attached to a reminder when the user marks
// the reminder done, if the task's current instance is due on or before that day.
func (s *Service) completeLinkedTask(ctx context.Context, tx store.Store, o *model.Occurrence, source string) error {
	if o.TaskID == nil {
		return nil
	}
	t, err := liveTask(ctx, tx, *o.TaskID)
	if err != nil || t.Status != model.TaskPending {
		return nil //nolint:nilerr // task gone or done: nothing to do
	}
	if t.DueAt != nil && !t.DueAt.Before(s.StartOfDay(o.ScheduledFor).AddDate(0, 0, 1)) {
		return nil
	}
	_, err = s.finishTaskTx(ctx, tx, t.ID, model.OccDone, source)
	return err
}

// RecordTelegramMessage stores the Telegram message carrying an occurrence's buttons.
func (s *Service) RecordTelegramMessage(ctx context.Context, occID, chatID int64, messageID int) {
	err := s.st.InTx(ctx, func(tx store.Store) error {
		o, err := tx.GetOccurrence(ctx, occID)
		if err != nil {
			return err
		}
		prevChat, prevMsg := o.TelegramChatID, o.TelegramMessageID
		o.TelegramChatID, o.TelegramMessageID = chatID, messageID
		if err := tx.UpdateOccurrence(ctx, o); err != nil {
			return err
		}
		if !o.IsOpen() {
			// Acted on before the send completed: remove the buttons right away.
			s.notify().Finalize(chatID, messageID, s.finalText(ctx, tx, o))
		} else if prevMsg != 0 && prevMsg != messageID {
			// A nag superseded the previous message: drop its buttons.
			s.notify().Finalize(prevChat, prevMsg, s.baseText(ctx, tx, o)+"\n\n🔁 Sent again below")
		}
		return nil
	})
	logErr("record telegram message", err, "occurrence", occID)
}

// ---- message text ----

func (s *Service) baseText(ctx context.Context, st store.Store, o *model.Occurrence) string {
	var b strings.Builder
	b.WriteString("🔔 ")
	if o.ReminderID != nil {
		if r, err := st.GetReminder(ctx, *o.ReminderID); err == nil {
			b.WriteString(r.MessageMD)
			if r.TaskID != nil && r.TaskTitle != "" {
				if r.MessageMD != "" {
					b.WriteString("\n")
				}
				b.WriteString("📋 Task: " + r.TaskTitle)
			}
		}
		b.WriteString("\n🕒 " + s.fmtTime(o.ScheduledFor))
		return b.String()
	}
	b.WriteString("📋 " + o.Title)
	if o.TaskID != nil {
		if t, err := st.GetTask(ctx, *o.TaskID); err == nil && t.StartAt != nil && t.DueAt != nil {
			// Start of this instance: the start keeps its distance to the due time.
			b.WriteString("\n▶️ Start " + s.fmtTime(o.ScheduledFor.Add(-t.DueAt.Sub(*t.StartAt))))
		}
	}
	b.WriteString("\n🕒 Due " + s.fmtTime(o.ScheduledFor))
	return b.String()
}

// MessageText is the text of a reminder notification (with nag counter).
func (s *Service) MessageText(ctx context.Context, st store.Store, o *model.Occurrence) string {
	t := s.baseText(ctx, st, o)
	if o.SentCount > 1 {
		t += "\n(reminder #" + strconv.Itoa(o.SentCount) + ")"
	}
	return t
}

func (s *Service) finalText(ctx context.Context, st store.Store, o *model.Occurrence) string {
	t := s.baseText(ctx, st, o)
	now := s.now()
	switch o.Status {
	case model.OccDone:
		return t + "\n\n✅ Done at " + s.hm(now)
	case model.OccSkipped:
		return t + "\n\n⏭ Skipped"
	case model.OccMissed:
		return t + "\n\n⚠️ Missed"
	case model.OccSnoozed:
		if o.SnoozedUntil != nil {
			return t + "\n\n⏰ Snoozed until " + s.fmtTime(*o.SnoozedUntil)
		}
	}
	return t
}

// ResultText returns the final message text for an occurrence (used by Telegram).
func (s *Service) ResultText(ctx context.Context, o *model.Occurrence) string {
	return s.finalText(ctx, s.st, o)
}
