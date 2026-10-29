// Package store defines the persistence interfaces used by the service layer.
// The sqlite sub-package provides the default implementation; another backend
// (e.g. PostgreSQL) only needs to satisfy Store.
package store

import (
	"context"
	"time"

	"github.com/sovannroth/mynotes/internal/model"
)

// Store is the full persistence API.
type Store interface {
	Users
	Settings
	Sessions
	Tokens
	Tasks
	Reminders
	Occurrences
	Memos
	Tags
	Search

	// InTx runs fn inside a transaction. The Store passed to fn must be used
	// for all operations within the transaction.
	InTx(ctx context.Context, fn func(Store) error) error
	Backup(ctx context.Context, path string) error
	Ping(ctx context.Context) error
	Close() error
}

// Users persists the single account.
type Users interface {
	CountUsers(ctx context.Context) (int, error)
	CreateUser(ctx context.Context, u *model.User) error
	GetUserByUsername(ctx context.Context, username string) (*model.User, error)
	GetUser(ctx context.Context, id int64) (*model.User, error)
	UpdatePassword(ctx context.Context, id int64, hash string) error
}

// Settings is a key/value store.
type Settings interface {
	GetSettings(ctx context.Context) (map[string]string, error)
	SetSetting(ctx context.Context, key, value string) error
	DeleteSetting(ctx context.Context, key string) error
}

// Sessions persists login sessions.
type Sessions interface {
	CreateSession(ctx context.Context, s *model.Session) error
	GetSession(ctx context.Context, idHash string) (*model.Session, error)
	TouchSession(ctx context.Context, idHash string, at time.Time) error
	DeleteSession(ctx context.Context, idHash string) error
	DeleteSessionsExcept(ctx context.Context, keepIDHash string) error
	DeleteSessionsIdleSince(ctx context.Context, before time.Time) error
}

// Tokens persists personal API tokens.
type Tokens interface {
	CreateToken(ctx context.Context, t *model.APIToken) error
	ListTokens(ctx context.Context) ([]model.APIToken, error)
	GetTokenByHash(ctx context.Context, hash string) (*model.APIToken, error)
	TouchToken(ctx context.Context, id int64, at time.Time) error
	RevokeToken(ctx context.Context, id int64, at time.Time) error
}

// Tasks persists tasks.
type Tasks interface {
	CreateTask(ctx context.Context, t *model.Task) error
	UpdateTask(ctx context.Context, t *model.Task) error
	GetTask(ctx context.Context, id int64) (*model.Task, error)
	ListTasks(ctx context.Context, f model.TaskFilter) ([]model.Task, error)
	SoftDeleteTask(ctx context.Context, id int64, at time.Time) error
	// ListTasksDue returns pending, non-deleted tasks with due_at <= before.
	ListTasksDue(ctx context.Context, before time.Time) ([]model.Task, error)
}

// Reminders persists reminders.
type Reminders interface {
	CreateReminder(ctx context.Context, r *model.Reminder) error
	UpdateReminder(ctx context.Context, r *model.Reminder) error
	GetReminder(ctx context.Context, id int64) (*model.Reminder, error)
	// ListReminders lists non-deleted reminders; taskID > 0 restricts to one task.
	ListReminders(ctx context.Context, taskID int64) ([]model.Reminder, error)
	SoftDeleteReminder(ctx context.Context, id int64, at time.Time) error
	// DueReminders returns enabled, non-deleted reminders with next_fire_at <= now.
	DueReminders(ctx context.Context, now time.Time) ([]model.Reminder, error)
}

// Occurrences persists occurrence history.
type Occurrences interface {
	// InsertOccurrence inserts a row unless one already exists for the same
	// reminder/task and scheduled time. created reports whether it was inserted.
	InsertOccurrence(ctx context.Context, o *model.Occurrence) (created bool, err error)
	GetOccurrence(ctx context.Context, id int64) (*model.Occurrence, error)
	UpdateOccurrence(ctx context.Context, o *model.Occurrence) error
	ListOccurrences(ctx context.Context, f model.OccurrenceFilter) ([]model.Occurrence, error)
	// DueDeliveries returns open reminder occurrences whose next_send_at <= now
	// and whose reminder is enabled and not deleted.
	DueDeliveries(ctx context.Context, now time.Time) ([]model.Occurrence, error)
	// FindOccurrence returns the occurrence for a reminder (or reminder-less task) at a time.
	FindOccurrence(ctx context.Context, reminderID, taskID int64, at time.Time) (*model.Occurrence, error)
}

// Memos persists memos.
type Memos interface {
	CreateMemo(ctx context.Context, m *model.Memo) error
	UpdateMemo(ctx context.Context, m *model.Memo) error
	GetMemo(ctx context.Context, id int64) (*model.Memo, error)
	ListMemos(ctx context.Context, f model.MemoFilter, pinnedOnly bool) ([]model.Memo, error)
	SoftDeleteMemo(ctx context.Context, id int64, at time.Time) error
}

// Tags manages tag links.
type Tags interface {
	SetTaskTags(ctx context.Context, id int64, tags []string) error
	SetReminderTags(ctx context.Context, id int64, tags []string) error
	SetMemoTags(ctx context.Context, id int64, tags []string) error
	// TagCounts returns tag usage; kind is "memo", "task", "reminder" or "" for all.
	TagCounts(ctx context.Context, kind string) ([]model.TagCount, error)
}

// Search runs full-text search.
type Search interface {
	Search(ctx context.Context, ftsQuery string, limit int) ([]model.SearchHit, error)
}
