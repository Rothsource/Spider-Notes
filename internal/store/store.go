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
	Notes
	Attachments
	Imports
	Expenses

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
	// ListTaskAlertsDue returns pending, non-deleted tasks with next_alert_at <= now.
	ListTaskAlertsDue(ctx context.Context, now time.Time) ([]model.Task, error)
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

// Notes persists folders and notes.
type Notes interface {
	CreateFolder(ctx context.Context, f *model.Folder) error
	UpdateFolder(ctx context.Context, f *model.Folder) error
	GetFolder(ctx context.Context, id int64) (*model.Folder, error)
	// ListFolders returns all non-deleted folders ordered by name.
	ListFolders(ctx context.Context) ([]model.Folder, error)
	SoftDeleteFolder(ctx context.Context, id int64, at time.Time) error

	CreateNote(ctx context.Context, n *model.Note) error
	UpdateNote(ctx context.Context, n *model.Note) error
	GetNote(ctx context.Context, id int64) (*model.Note, error)
	// ListNotes lists notes in a folder (folderID 0 = top level); all=true ignores the folder.
	ListNotes(ctx context.Context, folderID int64, all bool) ([]model.Note, error)
	SoftDeleteNote(ctx context.Context, id int64, at time.Time) error
	SetNoteTags(ctx context.Context, id int64, tags []string) error
}

// Attachments persists uploaded file metadata.
type Attachments interface {
	CreateAttachment(ctx context.Context, a *model.Attachment) error
	UpdateAttachment(ctx context.Context, a *model.Attachment) error
	GetAttachment(ctx context.Context, id int64) (*model.Attachment, error)
	ListAttachments(ctx context.Context, f model.AttachmentFilter) ([]model.Attachment, error)
	SoftDeleteAttachment(ctx context.Context, id int64, at time.Time) error
}

// Imports tracks items imported from other apps.
type Imports interface {
	// ImportedID returns the target id for a source item, or 0 if not imported yet.
	ImportedID(ctx context.Context, source, key string) (int64, error)
	RecordImport(ctx context.Context, source, key, kind string, id int64, at time.Time) error
}

// Search runs full-text search.
type Search interface {
	Search(ctx context.Context, ftsQuery string, limit int) ([]model.SearchHit, error)
}

// Expenses persists spending records.
type Expenses interface {
	CreateExpense(ctx context.Context, e *model.Expense) error
	UpdateExpense(ctx context.Context, e *model.Expense) error
	GetExpense(ctx context.Context, id int64) (*model.Expense, error)
	ListExpenses(ctx context.Context, f model.ExpenseFilter) ([]model.Expense, error)
	// ExpenseValues returns the distinct categories or payment methods ("category"/"payment"), most used first.
	ExpenseValues(ctx context.Context, column string) ([]string, error)
	SoftDeleteExpense(ctx context.Context, id int64, at time.Time) error

	ListExpenseCategories(ctx context.Context) ([]model.ExpenseCategory, error)
	GetExpenseCategory(ctx context.Context, id int64) (*model.ExpenseCategory, error)
	// FindExpenseCategory looks a category up by name, ignoring case.
	FindExpenseCategory(ctx context.Context, name string) (*model.ExpenseCategory, error)
	CreateExpenseCategory(ctx context.Context, c *model.ExpenseCategory) error
	// UpdateExpenseCategory saves c; when the name changed, expenses using oldName are renamed too.
	UpdateExpenseCategory(ctx context.Context, c *model.ExpenseCategory, oldName string) error
	DeleteExpenseCategory(ctx context.Context, id int64) error
}
