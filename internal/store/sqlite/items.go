package sqlite

import (
	"context"
	"strings"
	"time"

	"github.com/sovannroth/mynotes/internal/model"
)

// ---- tags ----

type tagLink struct{ table, col string }

var (
	taskTagLink     = tagLink{"task_tags", "task_id"}
	reminderTagLink = tagLink{"reminder_tags", "reminder_id"}
	memoTagLink     = tagLink{"memo_tags", "memo_id"}
)

func (d *DB) setTags(ctx context.Context, l tagLink, id int64, tags []string) error {
	if _, err := d.q.ExecContext(ctx, `DELETE FROM `+l.table+` WHERE `+l.col+` = ?`, id); err != nil {
		return err
	}
	for _, t := range tags {
		if _, err := d.q.ExecContext(ctx, `INSERT INTO tags(name) VALUES (?) ON CONFLICT(name) DO NOTHING`, t); err != nil {
			return err
		}
		if _, err := d.q.ExecContext(ctx, `INSERT OR IGNORE INTO `+l.table+`(`+l.col+`, tag_id) SELECT ?, id FROM tags WHERE name = ?`, id, t); err != nil {
			return err
		}
	}
	return nil
}

// loadTags returns tags for the given ids. Must be called with no open rows.
func (d *DB) loadTags(ctx context.Context, l tagLink, ids []int64) (map[int64][]string, error) {
	out := map[int64][]string{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := d.q.QueryContext(ctx, `SELECT x.`+l.col+`, g.name FROM `+l.table+` x JOIN tags g ON g.id = x.tag_id WHERE x.`+l.col+` IN (`+placeholders(len(ids))+`) ORDER BY g.name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = append(out[id], name)
	}
	return out, rows.Err()
}

// SetTaskTags implements store.Tags.
func (d *DB) SetTaskTags(ctx context.Context, id int64, tags []string) error {
	return d.setTags(ctx, taskTagLink, id, tags)
}

// SetReminderTags implements store.Tags.
func (d *DB) SetReminderTags(ctx context.Context, id int64, tags []string) error {
	return d.setTags(ctx, reminderTagLink, id, tags)
}

// SetMemoTags implements store.Tags.
func (d *DB) SetMemoTags(ctx context.Context, id int64, tags []string) error {
	return d.setTags(ctx, memoTagLink, id, tags)
}

// TagCounts implements store.Tags.
func (d *DB) TagCounts(ctx context.Context, kind string) ([]model.TagCount, error) {
	parts := map[string]string{
		"memo":     `SELECT tag_id FROM memo_tags x JOIN memos m ON m.id = x.memo_id WHERE m.deleted_at IS NULL`,
		"task":     `SELECT tag_id FROM task_tags x JOIN tasks t ON t.id = x.task_id WHERE t.deleted_at IS NULL`,
		"reminder": `SELECT tag_id FROM reminder_tags x JOIN reminders r ON r.id = x.reminder_id WHERE r.deleted_at IS NULL`,
		"note":     `SELECT tag_id FROM note_tags x JOIN notes n ON n.id = x.note_id WHERE n.deleted_at IS NULL`,
	}
	var sub string
	if p, ok := parts[kind]; ok {
		sub = p
	} else {
		sub = parts["memo"] + " UNION ALL " + parts["task"] + " UNION ALL " + parts["reminder"] + " UNION ALL " + parts["note"]
	}
	rows, err := d.q.QueryContext(ctx, `SELECT g.name, COUNT(*) FROM (`+sub+`) u JOIN tags g ON g.id = u.tag_id GROUP BY g.name ORDER BY COUNT(*) DESC, g.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.TagCount
	for rows.Next() {
		var tc model.TagCount
		if err := rows.Scan(&tc.Name, &tc.Count); err != nil {
			return nil, err
		}
		out = append(out, tc)
	}
	return out, rows.Err()
}

// ---- tasks ----

const taskCols = `t.id, t.title, t.description_md, t.start_at, t.due_at, t.alert, t.next_alert_at, t.priority, t.status, t.repeat_rule, t.repeat_start_at, t.completed_at, t.created_at, t.updated_at, t.deleted_at`

func scanTask(sc interface{ Scan(...any) error }) (model.Task, error) {
	var t model.Task
	err := sc.Scan(&t.ID, &t.Title, &t.DescriptionMD, tsp(&t.StartAt), tsp(&t.DueAt), &t.Alert, tsp(&t.NextAlertAt), &t.Priority, &t.Status, &t.RepeatRule,
		tsp(&t.RepeatStartAt), tsp(&t.CompletedAt), ts(&t.CreatedAt), ts(&t.UpdatedAt), tsp(&t.DeletedAt))
	return t, err
}

// CreateTask implements store.Tasks.
func (d *DB) CreateTask(ctx context.Context, t *model.Task) error {
	res, err := d.q.ExecContext(ctx, `INSERT INTO tasks(title, description_md, start_at, due_at, alert, next_alert_at, priority, status, repeat_rule, repeat_start_at, completed_at, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.Title, t.DescriptionMD, fpt(t.StartAt), fpt(t.DueAt), t.Alert, fpt(t.NextAlertAt), t.Priority, t.Status, t.RepeatRule, fpt(t.RepeatStartAt), fpt(t.CompletedAt), ft(t.CreatedAt), ft(t.UpdatedAt))
	if err != nil {
		return err
	}
	t.ID, err = res.LastInsertId()
	return err
}

// UpdateTask implements store.Tasks.
func (d *DB) UpdateTask(ctx context.Context, t *model.Task) error {
	return checkAffected(d.q.ExecContext(ctx, `UPDATE tasks SET title=?, description_md=?, start_at=?, due_at=?, alert=?, next_alert_at=?, priority=?, status=?, repeat_rule=?, repeat_start_at=?, completed_at=?, updated_at=?
		WHERE id=? AND deleted_at IS NULL`,
		t.Title, t.DescriptionMD, fpt(t.StartAt), fpt(t.DueAt), t.Alert, fpt(t.NextAlertAt), t.Priority, t.Status, t.RepeatRule, fpt(t.RepeatStartAt), fpt(t.CompletedAt), ft(t.UpdatedAt), t.ID))
}

// GetTask implements store.Tasks. Soft-deleted tasks are returned too (for history).
func (d *DB) GetTask(ctx context.Context, id int64) (*model.Task, error) {
	t, err := scanTask(d.q.QueryRowContext(ctx, `SELECT `+taskCols+` FROM tasks t WHERE t.id = ?`, id))
	if err != nil {
		return nil, notFound(err)
	}
	tags, err := d.loadTags(ctx, taskTagLink, []int64{id})
	if err != nil {
		return nil, err
	}
	t.Tags = tags[id]
	return &t, nil
}

func (d *DB) queryTasks(ctx context.Context, q string, args ...any) ([]model.Task, error) {
	rows, err := d.q.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	var out []model.Task
	var ids []int64
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, t)
		ids = append(ids, t.ID)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	tags, err := d.loadTags(ctx, taskTagLink, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Tags = tags[out[i].ID]
	}
	return out, nil
}

// ListTasks implements store.Tasks.
func (d *DB) ListTasks(ctx context.Context, f model.TaskFilter) ([]model.Task, error) {
	where := []string{"t.deleted_at IS NULL"}
	var args []any
	switch f.Status {
	case model.TaskPending, model.TaskDone:
		where = append(where, "t.status = ?")
		args = append(args, f.Status)
	}
	if f.From != nil {
		where = append(where, "t.due_at >= ?")
		args = append(args, ft(*f.From))
	}
	if f.To != nil {
		where = append(where, "t.due_at < ?")
		args = append(args, ft(*f.To))
	}
	if f.Before != nil {
		where = append(where, "t.due_at < ? AND t.status = 'pending'")
		args = append(args, ft(*f.Before))
	}
	if f.NoDate {
		where = append(where, "t.due_at IS NULL")
	}
	if f.Priority != "" {
		where = append(where, "t.priority = ?")
		args = append(args, f.Priority)
	}
	if f.Tag != "" {
		where = append(where, "EXISTS (SELECT 1 FROM task_tags x JOIN tags g ON g.id = x.tag_id WHERE x.task_id = t.id AND g.name = ?)")
		args = append(args, f.Tag)
	}
	if f.Query != "" {
		where = append(where, "t.id IN (SELECT rowid FROM tasks_fts WHERE tasks_fts MATCH ?)")
		args = append(args, f.Query)
	}
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	args = append(args, limit)
	q := `SELECT ` + taskCols + ` FROM tasks t WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY t.status = 'done', t.due_at IS NULL, t.due_at, CASE t.priority WHEN 'high' THEN 0 WHEN 'normal' THEN 1 ELSE 2 END, t.id DESC LIMIT ?`
	return d.queryTasks(ctx, q, args...)
}

// ListTasksDue implements store.Tasks.
func (d *DB) ListTasksDue(ctx context.Context, before time.Time) ([]model.Task, error) {
	return d.queryTasks(ctx, `SELECT `+taskCols+` FROM tasks t WHERE t.deleted_at IS NULL AND t.status = 'pending' AND t.due_at IS NOT NULL AND t.due_at <= ? ORDER BY t.due_at`, ft(before))
}

// ListTaskAlertsDue implements store.Tasks.
func (d *DB) ListTaskAlertsDue(ctx context.Context, now time.Time) ([]model.Task, error) {
	return d.queryTasks(ctx, `SELECT `+taskCols+` FROM tasks t WHERE t.deleted_at IS NULL AND t.status = 'pending' AND t.next_alert_at IS NOT NULL AND t.next_alert_at <= ? ORDER BY t.next_alert_at`, ft(now))
}

// SoftDeleteTask implements store.Tasks.
func (d *DB) SoftDeleteTask(ctx context.Context, id int64, at time.Time) error {
	return checkAffected(d.q.ExecContext(ctx, `UPDATE tasks SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`, ft(at), ft(at), id))
}

// ---- reminders ----

const reminderCols = `r.id, r.task_id, COALESCE(t.title, ''), r.message_md, r.schedule_type, r.fire_at, r.repeat_rule, r.active_window, r.timezone,
	r.nag_enabled, r.nag_interval_min, r.quiet_hours_override, r.ignore_quiet_hours, r.enabled, r.next_fire_at, r.last_sent_at, r.fire_count,
	r.created_at, r.updated_at, r.deleted_at`

const reminderFrom = ` FROM reminders r LEFT JOIN tasks t ON t.id = r.task_id `

func scanReminder(sc interface{ Scan(...any) error }) (model.Reminder, error) {
	var r model.Reminder
	err := sc.Scan(&r.ID, ip(&r.TaskID), &r.TaskTitle, &r.MessageMD, &r.ScheduleType, ts(&r.FireAt), &r.RepeatRule, &r.ActiveWindow, &r.Timezone,
		&r.NagEnabled, &r.NagIntervalMin, &r.QuietHoursOverride, &r.IgnoreQuietHours, &r.Enabled, tsp(&r.NextFireAt), tsp(&r.LastSentAt), &r.FireCount,
		ts(&r.CreatedAt), ts(&r.UpdatedAt), tsp(&r.DeletedAt))
	return r, err
}

// CreateReminder implements store.Reminders.
func (d *DB) CreateReminder(ctx context.Context, r *model.Reminder) error {
	res, err := d.q.ExecContext(ctx, `INSERT INTO reminders(task_id, message_md, schedule_type, fire_at, repeat_rule, active_window, timezone, nag_enabled, nag_interval_min,
		quiet_hours_override, ignore_quiet_hours, enabled, next_fire_at, last_sent_at, fire_count, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		nullInt(r.TaskID), r.MessageMD, r.ScheduleType, ft(r.FireAt), r.RepeatRule, r.ActiveWindow, r.Timezone, b2i(r.NagEnabled), r.NagIntervalMin,
		r.QuietHoursOverride, b2i(r.IgnoreQuietHours), b2i(r.Enabled), fpt(r.NextFireAt), fpt(r.LastSentAt), r.FireCount, ft(r.CreatedAt), ft(r.UpdatedAt))
	if err != nil {
		return err
	}
	r.ID, err = res.LastInsertId()
	return err
}

// UpdateReminder implements store.Reminders.
func (d *DB) UpdateReminder(ctx context.Context, r *model.Reminder) error {
	return checkAffected(d.q.ExecContext(ctx, `UPDATE reminders SET task_id=?, message_md=?, schedule_type=?, fire_at=?, repeat_rule=?, active_window=?, timezone=?,
		nag_enabled=?, nag_interval_min=?, quiet_hours_override=?, ignore_quiet_hours=?, enabled=?, next_fire_at=?, last_sent_at=?, fire_count=?, updated_at=?
		WHERE id=? AND deleted_at IS NULL`,
		nullInt(r.TaskID), r.MessageMD, r.ScheduleType, ft(r.FireAt), r.RepeatRule, r.ActiveWindow, r.Timezone,
		b2i(r.NagEnabled), r.NagIntervalMin, r.QuietHoursOverride, b2i(r.IgnoreQuietHours), b2i(r.Enabled), fpt(r.NextFireAt), fpt(r.LastSentAt), r.FireCount, ft(r.UpdatedAt), r.ID))
}

// GetReminder implements store.Reminders (includes soft-deleted rows).
func (d *DB) GetReminder(ctx context.Context, id int64) (*model.Reminder, error) {
	r, err := scanReminder(d.q.QueryRowContext(ctx, `SELECT `+reminderCols+reminderFrom+`WHERE r.id = ?`, id))
	if err != nil {
		return nil, notFound(err)
	}
	tags, err := d.loadTags(ctx, reminderTagLink, []int64{id})
	if err != nil {
		return nil, err
	}
	r.Tags = tags[id]
	return &r, nil
}

func (d *DB) queryReminders(ctx context.Context, q string, args ...any) ([]model.Reminder, error) {
	rows, err := d.q.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	var out []model.Reminder
	var ids []int64
	for rows.Next() {
		r, err := scanReminder(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, r)
		ids = append(ids, r.ID)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	tags, err := d.loadTags(ctx, reminderTagLink, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Tags = tags[out[i].ID]
	}
	return out, nil
}

// ListReminders implements store.Reminders.
func (d *DB) ListReminders(ctx context.Context, taskID int64) ([]model.Reminder, error) {
	q := `SELECT ` + reminderCols + reminderFrom + `WHERE r.deleted_at IS NULL`
	var args []any
	if taskID > 0 {
		q += ` AND r.task_id = ?`
		args = append(args, taskID)
	}
	q += ` ORDER BY r.enabled DESC, r.next_fire_at IS NULL, r.next_fire_at, r.id DESC`
	return d.queryReminders(ctx, q, args...)
}

// DueReminders implements store.Reminders.
func (d *DB) DueReminders(ctx context.Context, now time.Time) ([]model.Reminder, error) {
	return d.queryReminders(ctx, `SELECT `+reminderCols+reminderFrom+`WHERE r.deleted_at IS NULL AND r.enabled = 1 AND r.next_fire_at IS NOT NULL AND r.next_fire_at <= ? ORDER BY r.next_fire_at`, ft(now))
}

// SoftDeleteReminder implements store.Reminders.
func (d *DB) SoftDeleteReminder(ctx context.Context, id int64, at time.Time) error {
	return checkAffected(d.q.ExecContext(ctx, `UPDATE reminders SET deleted_at = ?, updated_at = ?, next_fire_at = NULL WHERE id = ? AND deleted_at IS NULL`, ft(at), ft(at), id))
}
