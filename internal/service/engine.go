package service

import (
	"context"
	"time"

	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/schedule"
	"github.com/sovannroth/mynotes/internal/store"
)

// missedAfter is how long a non-nagging occurrence stays open before it is marked missed.
const missedAfter = 24 * time.Hour

// maxCatchupRows bounds how many "missed" rows are written for one reminder after downtime.
const maxCatchupRows = 50

// ProcessDue runs one scheduler tick. It is idempotent and restart-safe: all
// progress is stored in the DB (next_fire_at, occurrence rows, next_send_at),
// and every state transition happens in a transaction before any message is sent.
func (s *Service) ProcessDue(ctx context.Context) error {
	now := s.now().UTC().Truncate(time.Second)
	set, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	due, err := s.st.DueReminders(ctx, now)
	if err != nil {
		return err
	}
	for i := range due {
		logErr("materialize reminder", s.materialize(ctx, due[i].ID, now), "reminder", due[i].ID)
	}
	logErr("task alerts", s.processTaskAlerts(ctx, now))
	logErr("process tasks", s.processTasks(ctx, now))
	logErr("expire occurrences", s.expireStale(ctx, now))

	deliveries, err := s.st.DueDeliveries(ctx, now)
	if err != nil {
		return err
	}
	for i := range deliveries {
		logErr("deliver occurrence", s.deliver(ctx, deliveries[i].ID, set, now), "occurrence", deliveries[i].ID)
	}
	return nil
}

// materialize turns a due reminder into an occurrence row and advances next_fire_at.
// After downtime only the most recent due time becomes a deliverable occurrence;
// older ones are recorded as missed.
func (s *Service) materialize(ctx context.Context, id int64, now time.Time) error {
	return s.st.InTx(ctx, func(tx store.Store) error {
		r, err := liveReminder(ctx, tx, id)
		if err != nil {
			return err
		}
		if !r.Enabled || r.NextFireAt == nil || r.NextFireAt.After(now) {
			return nil // changed concurrently
		}
		var times []time.Time // ring of the most recent due times
		total := 0
		var next *time.Time
		if r.ScheduleType == model.OneTime {
			times, total = []time.Time{*r.NextFireAt}, 1
		} else {
			rule, err := ParseRule(r.RepeatRule, r.ActiveWindow)
			if err != nil {
				r.NextFireAt = nil
				r.UpdatedAt = now
				return tx.UpdateReminder(ctx, r)
			}
			t := *r.NextFireAt
			for i := 0; i < 1_000_000 && !t.IsZero() && !t.After(now); i++ {
				times = append(times, t)
				if len(times) > maxCatchupRows+1 {
					times = times[1:]
				}
				total++
				t = rule.Next(r.FireAt, t, s.loc)
			}
			if !t.IsZero() {
				next = ptr(t.UTC())
			}
		}
		latest := times[len(times)-1]
		r.NextFireAt = next
		r.FireCount += total
		r.UpdatedAt = now

		// Skip firing if the linked task is already done or deleted.
		if r.TaskID != nil {
			if t, err := tx.GetTask(ctx, *r.TaskID); err == nil && (t.DeletedAt != nil || t.Status == model.TaskDone) {
				return tx.UpdateReminder(ctx, r)
			}
		}

		// Earlier occurrences still open when a new one arrives become missed.
		open, err := tx.ListOccurrences(ctx, model.OccurrenceFilter{ReminderID: r.ID, Statuses: openStatuses, To: &latest})
		if err != nil {
			return err
		}
		for i := range open {
			if err := s.closeOcc(ctx, tx, &open[i], model.OccMissed, model.SourceSystem, now); err != nil {
				return err
			}
		}
		for _, t := range times[:len(times)-1] {
			if _, err := tx.InsertOccurrence(ctx, &model.Occurrence{ReminderID: &r.ID, TaskID: r.TaskID, ScheduledFor: t,
				Status: model.OccMissed, ActionSource: model.SourceSystem, CreatedAt: now, UpdatedAt: now}); err != nil {
				return err
			}
		}
		if _, err := tx.InsertOccurrence(ctx, &model.Occurrence{ReminderID: &r.ID, TaskID: r.TaskID, ScheduledFor: latest,
			Status: model.OccPending, NextSendAt: &latest, CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		return tx.UpdateReminder(ctx, r)
	})
}

// deliver sends one due occurrence. The DB is updated (claim + next nag time)
// before the message is queued, so a crash can never cause a double send.
func (s *Service) deliver(ctx context.Context, id int64, set Settings, now time.Time) error {
	var msg *Message
	err := s.st.InTx(ctx, func(tx store.Store) error {
		o, err := tx.GetOccurrence(ctx, id)
		if err != nil {
			return err
		}
		if !o.IsOpen() || o.NextSendAt == nil || o.NextSendAt.After(now) {
			return nil
		}
		// Task alerts have no reminder: global quiet hours, no nagging.
		r := &model.Reminder{}
		if o.ReminderID != nil {
			if r, err = liveReminder(ctx, tx, *o.ReminderID); err != nil || !r.Enabled {
				return nil //nolint:nilerr // reminder gone or paused: hold
			}
		} else if t, err := liveTask(ctx, tx, *o.TaskID); err != nil || t.Status != model.TaskPending {
			return nil //nolint:nilerr // task gone or done: nothing to send
		}
		if hold := s.quietFor(set, r).HoldUntil(now, s.loc); hold.After(now) {
			o.NextSendAt, o.UpdatedAt = ptr(hold.UTC()), now
			return tx.UpdateOccurrence(ctx, o)
		}
		o.SentCount++
		o.LastSentAt = &now
		o.Status = model.OccSent
		o.SnoozedUntil = nil
		o.NextSendAt = nil
		if r.NagEnabled && r.NagIntervalMin > 0 {
			o.NextSendAt = ptr(now.Add(time.Duration(r.NagIntervalMin) * time.Minute))
		}
		o.UpdatedAt = now
		if err := tx.UpdateOccurrence(ctx, o); err != nil {
			return err
		}
		if r.ID != 0 {
			r.LastSentAt = &now
			if err := tx.UpdateReminder(ctx, r); err != nil {
				return err
			}
		}
		msg = &Message{OccurrenceID: o.ID, Text: s.MessageText(ctx, tx, o), AllowSkip: o.Repeating}
		return nil
	})
	if err == nil && msg != nil {
		s.notify().Deliver(*msg)
	}
	return err
}

// expireStale marks open, non-nagging reminder occurrences older than 24h as missed.
func (s *Service) expireStale(ctx context.Context, now time.Time) error {
	cutoff := now.Add(-missedAfter)
	occs, err := s.st.ListOccurrences(ctx, model.OccurrenceFilter{Kind: "reminder", Statuses: []string{model.OccPending, model.OccSent}, To: &cutoff})
	if err != nil {
		return err
	}
	for i := range occs {
		o := occs[i]
		err := s.st.InTx(ctx, func(tx store.Store) error {
			r, err := tx.GetReminder(ctx, *o.ReminderID)
			if err != nil {
				return err
			}
			if r.NagEnabled && r.Enabled && r.DeletedAt == nil {
				return nil // nagging continues until done/snoozed
			}
			return s.closeOcc(ctx, tx, &o, model.OccMissed, model.SourceSystem, now)
		})
		logErr("expire occurrence", err, "occurrence", o.ID)
	}
	return nil
}

// OpenOccurrences returns reminder/task occurrences awaiting action, newest first.
func (s *Service) OpenOccurrences(ctx context.Context) ([]model.Occurrence, error) {
	now := s.now().UTC()
	return s.st.ListOccurrences(ctx, model.OccurrenceFilter{Statuses: openStatuses, To: ptr(now.Add(time.Second)), Limit: 200})
}

// ListOccurrences exposes occurrence listing for the API.
func (s *Service) ListOccurrences(ctx context.Context, f model.OccurrenceFilter) ([]model.Occurrence, error) {
	return s.st.ListOccurrences(ctx, f)
}

// GetOccurrence returns one occurrence.
func (s *Service) GetOccurrence(ctx context.Context, id int64) (*model.Occurrence, error) {
	return s.st.GetOccurrence(ctx, id)
}

// QuietNow reports whether global quiet hours are active (for UI hints).
func (s *Service) QuietNow(ctx context.Context) bool {
	set, err := s.Settings(ctx)
	if err != nil {
		return false
	}
	q := schedule.ResolveQuiet(set.QuietHours, "", false)
	return q.HoldUntil(s.now(), s.loc).After(s.now())
}
