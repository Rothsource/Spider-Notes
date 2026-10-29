package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/sovannroth/mynotes/internal/model"
)

// ---- occurrences ----

const occCols = `o.id, o.reminder_id, o.task_id, o.scheduled_for, o.status, o.sent_count, o.snooze_count, o.snoozed_until, o.next_send_at,
	o.last_sent_at, o.done_at, o.action_source, o.telegram_chat_id, o.telegram_message_id, o.created_at, o.updated_at,
	COALESCE(r.message_md, ''), COALESCE(t.title, ''), COALESCE(r.schedule_type = 'repeating', t.repeat_rule <> '', 0)`

const occFrom = ` FROM occurrences o LEFT JOIN reminders r ON r.id = o.reminder_id LEFT JOIN tasks t ON t.id = o.task_id `

func scanOcc(sc interface{ Scan(...any) error }) (model.Occurrence, error) {
	var o model.Occurrence
	var msg, title string
	err := sc.Scan(&o.ID, ip(&o.ReminderID), ip(&o.TaskID), ts(&o.ScheduledFor), &o.Status, &o.SentCount, &o.SnoozeCount, tsp(&o.SnoozedUntil), tsp(&o.NextSendAt),
		tsp(&o.LastSentAt), tsp(&o.DoneAt), &o.ActionSource, &o.TelegramChatID, &o.TelegramMessageID, ts(&o.CreatedAt), ts(&o.UpdatedAt),
		&msg, &title, &o.Repeating)
	if o.ReminderID != nil {
		o.Kind = "reminder"
		o.Title = model.FirstLine(msg)
	} else {
		o.Kind = "task"
		o.Title = title
	}
	return o, err
}

// InsertOccurrence implements store.Occurrences.
func (d *DB) InsertOccurrence(ctx context.Context, o *model.Occurrence) (bool, error) {
	res, err := d.q.ExecContext(ctx, `INSERT OR IGNORE INTO occurrences(reminder_id, task_id, scheduled_for, status, sent_count, snooze_count, snoozed_until, next_send_at,
		last_sent_at, done_at, action_source, telegram_chat_id, telegram_message_id, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		nullInt(o.ReminderID), nullInt(o.TaskID), ft(o.ScheduledFor), o.Status, o.SentCount, o.SnoozeCount, fpt(o.SnoozedUntil), fpt(o.NextSendAt),
		fpt(o.LastSentAt), fpt(o.DoneAt), o.ActionSource, o.TelegramChatID, o.TelegramMessageID, ft(o.CreatedAt), ft(o.UpdatedAt))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	o.ID, err = res.LastInsertId()
	return true, err
}

// GetOccurrence implements store.Occurrences.
func (d *DB) GetOccurrence(ctx context.Context, id int64) (*model.Occurrence, error) {
	o, err := scanOcc(d.q.QueryRowContext(ctx, `SELECT `+occCols+occFrom+`WHERE o.id = ?`, id))
	if err != nil {
		return nil, notFound(err)
	}
	return &o, nil
}

// FindOccurrence implements store.Occurrences.
func (d *DB) FindOccurrence(ctx context.Context, reminderID, taskID int64, at time.Time) (*model.Occurrence, error) {
	var row *sql.Row
	if reminderID > 0 {
		row = d.q.QueryRowContext(ctx, `SELECT `+occCols+occFrom+`WHERE o.reminder_id = ? AND o.scheduled_for = ?`, reminderID, ft(at))
	} else {
		row = d.q.QueryRowContext(ctx, `SELECT `+occCols+occFrom+`WHERE o.reminder_id IS NULL AND o.task_id = ? AND o.scheduled_for = ?`, taskID, ft(at))
	}
	o, err := scanOcc(row)
	if err != nil {
		return nil, notFound(err)
	}
	return &o, nil
}

// UpdateOccurrence implements store.Occurrences.
func (d *DB) UpdateOccurrence(ctx context.Context, o *model.Occurrence) error {
	return checkAffected(d.q.ExecContext(ctx, `UPDATE occurrences SET status=?, sent_count=?, snooze_count=?, snoozed_until=?, next_send_at=?, last_sent_at=?,
		done_at=?, action_source=?, telegram_chat_id=?, telegram_message_id=?, updated_at=? WHERE id=?`,
		o.Status, o.SentCount, o.SnoozeCount, fpt(o.SnoozedUntil), fpt(o.NextSendAt), fpt(o.LastSentAt),
		fpt(o.DoneAt), o.ActionSource, o.TelegramChatID, o.TelegramMessageID, ft(o.UpdatedAt), o.ID))
}

func (d *DB) queryOccs(ctx context.Context, q string, args ...any) ([]model.Occurrence, error) {
	rows, err := d.q.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Occurrence
	for rows.Next() {
		o, err := scanOcc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ListOccurrences implements store.Occurrences.
func (d *DB) ListOccurrences(ctx context.Context, f model.OccurrenceFilter) ([]model.Occurrence, error) {
	where := []string{"1=1"}
	var args []any
	timeCol := "o.scheduled_for"
	if f.ByOutcome {
		timeCol = "COALESCE(o.done_at, o.scheduled_for)"
	}
	if f.From != nil {
		where = append(where, timeCol+" >= ?")
		args = append(args, ft(*f.From))
	}
	if f.To != nil {
		where = append(where, timeCol+" < ?")
		args = append(args, ft(*f.To))
	}
	if len(f.Statuses) > 0 {
		where = append(where, "o.status IN ("+placeholders(len(f.Statuses))+")")
		for _, s := range f.Statuses {
			args = append(args, s)
		}
	}
	switch f.Kind {
	case "reminder":
		where = append(where, "o.reminder_id IS NOT NULL")
	case "task":
		where = append(where, "o.reminder_id IS NULL")
	}
	if f.ReminderID > 0 {
		where = append(where, "o.reminder_id = ?")
		args = append(args, f.ReminderID)
	}
	if f.TaskID > 0 {
		where = append(where, "o.task_id = ?")
		args = append(args, f.TaskID)
	}
	if f.Tag != "" {
		where = append(where, `(EXISTS (SELECT 1 FROM reminder_tags x JOIN tags g ON g.id = x.tag_id WHERE x.reminder_id = o.reminder_id AND g.name = ?)
			OR EXISTS (SELECT 1 FROM task_tags x JOIN tags g ON g.id = x.tag_id WHERE x.task_id = o.task_id AND g.name = ?))`)
		args = append(args, f.Tag, f.Tag)
	}
	limit := f.Limit
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	args = append(args, limit)
	return d.queryOccs(ctx, `SELECT `+occCols+occFrom+`WHERE `+strings.Join(where, " AND ")+` ORDER BY `+timeCol+` DESC, o.id DESC LIMIT ?`, args...)
}

// DueDeliveries implements store.Occurrences.
func (d *DB) DueDeliveries(ctx context.Context, now time.Time) ([]model.Occurrence, error) {
	return d.queryOccs(ctx, `SELECT `+occCols+occFrom+`WHERE o.reminder_id IS NOT NULL AND o.status IN ('pending','sent','snoozed')
		AND o.next_send_at IS NOT NULL AND o.next_send_at <= ? AND r.enabled = 1 AND r.deleted_at IS NULL ORDER BY o.next_send_at`, ft(now))
}

// ---- memos ----

const memoCols = `m.id, m.content_md, m.pinned, m.created_at, m.updated_at, m.deleted_at`

func scanMemo(sc interface{ Scan(...any) error }) (model.Memo, error) {
	var m model.Memo
	err := sc.Scan(&m.ID, &m.ContentMD, &m.Pinned, ts(&m.CreatedAt), ts(&m.UpdatedAt), tsp(&m.DeletedAt))
	return m, err
}

// CreateMemo implements store.Memos.
func (d *DB) CreateMemo(ctx context.Context, m *model.Memo) error {
	res, err := d.q.ExecContext(ctx, `INSERT INTO memos(content_md, pinned, created_at, updated_at) VALUES (?,?,?,?)`,
		m.ContentMD, b2i(m.Pinned), ft(m.CreatedAt), ft(m.UpdatedAt))
	if err != nil {
		return err
	}
	m.ID, err = res.LastInsertId()
	return err
}

// UpdateMemo implements store.Memos.
func (d *DB) UpdateMemo(ctx context.Context, m *model.Memo) error {
	return checkAffected(d.q.ExecContext(ctx, `UPDATE memos SET content_md=?, pinned=?, updated_at=? WHERE id=? AND deleted_at IS NULL`,
		m.ContentMD, b2i(m.Pinned), ft(m.UpdatedAt), m.ID))
}

// GetMemo implements store.Memos.
func (d *DB) GetMemo(ctx context.Context, id int64) (*model.Memo, error) {
	m, err := scanMemo(d.q.QueryRowContext(ctx, `SELECT `+memoCols+` FROM memos m WHERE m.id = ? AND m.deleted_at IS NULL`, id))
	if err != nil {
		return nil, notFound(err)
	}
	tags, err := d.loadTags(ctx, memoTagLink, []int64{id})
	if err != nil {
		return nil, err
	}
	m.Tags = tags[id]
	return &m, nil
}

// ListMemos implements store.Memos.
func (d *DB) ListMemos(ctx context.Context, f model.MemoFilter, pinnedOnly bool) ([]model.Memo, error) {
	where := []string{"m.deleted_at IS NULL"}
	var args []any
	if pinnedOnly {
		where = append(where, "m.pinned = 1")
	}
	if f.Tag != "" {
		where = append(where, "EXISTS (SELECT 1 FROM memo_tags x JOIN tags g ON g.id = x.tag_id WHERE x.memo_id = m.id AND g.name = ?)")
		args = append(args, f.Tag)
	}
	if f.Query != "" {
		where = append(where, "m.id IN (SELECT rowid FROM memos_fts WHERE memos_fts MATCH ?)")
		args = append(args, f.Query)
	}
	if f.From != nil {
		where = append(where, "m.created_at >= ?")
		args = append(args, ft(*f.From))
	}
	if f.To != nil {
		where = append(where, "m.created_at < ?")
		args = append(args, ft(*f.To))
	}
	if f.Before != nil {
		where = append(where, "(m.created_at < ? OR (m.created_at = ? AND m.id < ?))")
		args = append(args, ft(*f.Before), ft(*f.Before), f.BeforeID)
	}
	limit := f.Limit
	if limit <= 0 || limit > 5000 {
		limit = 50
	}
	args = append(args, limit)
	rows, err := d.q.QueryContext(ctx, `SELECT `+memoCols+` FROM memos m WHERE `+strings.Join(where, " AND ")+` ORDER BY m.created_at DESC, m.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	var out []model.Memo
	var ids []int64
	for rows.Next() {
		m, err := scanMemo(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, m)
		ids = append(ids, m.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	tags, err := d.loadTags(ctx, memoTagLink, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Tags = tags[out[i].ID]
	}
	return out, nil
}

// SoftDeleteMemo implements store.Memos.
func (d *DB) SoftDeleteMemo(ctx context.Context, id int64, at time.Time) error {
	return checkAffected(d.q.ExecContext(ctx, `UPDATE memos SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL`, ft(at), id))
}

// ---- search ----

// Snippet highlight markers (replaced with <mark> by the UI after escaping).
const (
	MarkStart = "\x02"
	MarkEnd   = "\x03"
)

// Search implements store.Search.
func (d *DB) Search(ctx context.Context, q string, limit int) ([]model.SearchHit, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := d.q.QueryContext(ctx, `
		SELECT 'memo', m.id, '', snippet(memos_fts, 0, ?, ?, '…', 16), m.created_at
		  FROM memos_fts JOIN memos m ON m.id = memos_fts.rowid WHERE memos_fts MATCH ? AND m.deleted_at IS NULL
		UNION ALL
		SELECT 'task', t.id, t.title, snippet(tasks_fts, -1, ?, ?, '…', 16), t.updated_at
		  FROM tasks_fts JOIN tasks t ON t.id = tasks_fts.rowid WHERE tasks_fts MATCH ? AND t.deleted_at IS NULL
		UNION ALL
		SELECT 'reminder', r.id, '', snippet(reminders_fts, 0, ?, ?, '…', 16), r.updated_at
		  FROM reminders_fts JOIN reminders r ON r.id = reminders_fts.rowid WHERE reminders_fts MATCH ? AND r.deleted_at IS NULL
		UNION ALL
		SELECT 'note', n.id, n.title, snippet(notes_fts, -1, ?, ?, '…', 16), n.updated_at
		  FROM notes_fts JOIN notes n ON n.id = notes_fts.rowid WHERE notes_fts MATCH ? AND n.deleted_at IS NULL
		ORDER BY 5 DESC LIMIT ?`,
		MarkStart, MarkEnd, q, MarkStart, MarkEnd, q, MarkStart, MarkEnd, q, MarkStart, MarkEnd, q, limit)
	if err != nil {
		if strings.Contains(err.Error(), "fts5") || strings.Contains(err.Error(), "syntax error") {
			return nil, errors.Join(model.Invalid("invalid search query"), err)
		}
		return nil, err
	}
	defer rows.Close()
	var out []model.SearchHit
	for rows.Next() {
		var h model.SearchHit
		if err := rows.Scan(&h.Kind, &h.ID, &h.Title, &h.Snippet, ts(&h.When)); err != nil {
			return nil, err
		}
		if h.Title == "" {
			h.Title = model.FirstLine(strings.NewReplacer(MarkStart, "", MarkEnd, "").Replace(h.Snippet))
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
