// Package model contains the domain types shared by the store, services and handlers.
package model

import (
	"errors"
	"strings"
	"time"
)

// Common errors.
var (
	ErrNotFound   = errors.New("not found")
	ErrConflict   = errors.New("conflict")
	ErrValidation = errors.New("validation error")
)

// ValidationError carries a user-facing message.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

// Unwrap lets errors.Is(err, ErrValidation) work.
func (e *ValidationError) Unwrap() error { return ErrValidation }

// Invalid returns a ValidationError.
func Invalid(msg string) error { return &ValidationError{Msg: msg} }

// Priority values.
const (
	PriorityLow    = "low"
	PriorityNormal = "normal"
	PriorityHigh   = "high"
)

// Task statuses.
const (
	TaskPending = "pending"
	TaskDone    = "done"
)

// User is the single account.
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	CreatedAt    time.Time
}

// Task is a to-do item.
type Task struct {
	ID            int64      `json:"id"`
	Title         string     `json:"title"`
	DescriptionMD string     `json:"description_md"`
	DueAt         *time.Time `json:"due_at"`
	Priority      string     `json:"priority"`
	Status        string     `json:"status"`
	RepeatRule    string     `json:"repeat_rule,omitempty"`
	RepeatStartAt *time.Time `json:"repeat_start_at,omitempty"`
	CompletedAt   *time.Time `json:"completed_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	DeletedAt     *time.Time `json:"deleted_at,omitempty"`
	Tags          []string   `json:"tags"`
}

// Schedule types for reminders.
const (
	OneTime   = "one_time"
	Repeating = "repeating"
)

// Reminder is a scheduled message, standalone or attached to a task.
type Reminder struct {
	ID                 int64      `json:"id"`
	TaskID             *int64     `json:"task_id"`
	TaskTitle          string     `json:"task_title,omitempty"`
	MessageMD          string     `json:"message_md"`
	ScheduleType       string     `json:"schedule_type"`
	FireAt             time.Time  `json:"fire_at"` // one-time time, or series start for repeating
	RepeatRule         string     `json:"repeat_rule,omitempty"`
	ActiveWindow       string     `json:"active_window,omitempty"` // "HH:MM-HH:MM" for hourly/minute rules
	Timezone           string     `json:"timezone"`
	NagEnabled         bool       `json:"nag_enabled"`
	NagIntervalMin     int        `json:"nag_interval_min"`
	QuietHoursOverride string     `json:"quiet_hours_override"` // "", "off", or "HH:MM-HH:MM"
	IgnoreQuietHours   bool       `json:"ignore_quiet_hours"`
	Enabled            bool       `json:"enabled"`
	NextFireAt         *time.Time `json:"next_fire_at"`
	LastSentAt         *time.Time `json:"last_sent_at"`
	FireCount          int        `json:"fire_count"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	DeletedAt          *time.Time `json:"deleted_at,omitempty"`
	Tags               []string   `json:"tags"`
}

// Occurrence statuses.
const (
	OccPending = "pending"
	OccSent    = "sent"
	OccDone    = "done"
	OccSnoozed = "snoozed"
	OccSkipped = "skipped"
	OccMissed  = "missed"
)

// Action sources.
const (
	SourceWeb      = "web"
	SourceTelegram = "telegram"
	SourceAPI      = "api"
	SourceSystem   = "system"
)

// Occurrence is one firing of a reminder or one due instance of a task.
type Occurrence struct {
	ID                int64      `json:"id"`
	ReminderID        *int64     `json:"reminder_id"`
	TaskID            *int64     `json:"task_id"`
	ScheduledFor      time.Time  `json:"scheduled_for"`
	Status            string     `json:"status"`
	SentCount         int        `json:"sent_count"`
	SnoozeCount       int        `json:"snooze_count"`
	SnoozedUntil      *time.Time `json:"snoozed_until"`
	NextSendAt        *time.Time `json:"next_send_at"`
	LastSentAt        *time.Time `json:"last_sent_at"`
	DoneAt            *time.Time `json:"done_at"`
	ActionSource      string     `json:"action_source,omitempty"`
	TelegramChatID    int64      `json:"-"`
	TelegramMessageID int        `json:"-"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`

	// Denormalised for display.
	Title     string   `json:"title"`
	Kind      string   `json:"kind"` // "reminder" | "task"
	Repeating bool     `json:"repeating"`
	Tags      []string `json:"tags,omitempty"`
}

// IsOpen reports whether the occurrence still awaits an action.
func (o *Occurrence) IsOpen() bool {
	return o.Status == OccPending || o.Status == OccSent || o.Status == OccSnoozed
}

// Memo is a quick note.
type Memo struct {
	ID        int64      `json:"id"`
	ContentMD string     `json:"content_md"`
	Pinned    bool       `json:"pinned"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
	Tags      []string   `json:"tags"`
}

// TagCount is a tag with usage count.
type TagCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// APIToken is a personal API token (hash only).
type APIToken struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	TokenHash  string     `json:"-"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

// Session is a server-side login session.
type Session struct {
	IDHash     string
	UserID     int64
	CSRFToken  string
	CreatedAt  time.Time
	LastSeenAt time.Time
}

// SearchHit is a full-text search result.
type SearchHit struct {
	Kind    string    `json:"kind"` // memo | task | reminder
	ID      int64     `json:"id"`
	Title   string    `json:"title"`
	Snippet string    `json:"snippet"`
	When    time.Time `json:"when"`
}

// TaskFilter filters task listings.
type TaskFilter struct {
	Status   string // pending|done|all
	Range    string // today|week|overdue|nodate|""
	Tag      string
	Priority string
	Query    string
	From, To *time.Time // computed due range
	Before   *time.Time // overdue cutoff
	NoDate   bool
	Limit    int
}

// OccurrenceFilter filters occurrence listings.
type OccurrenceFilter struct {
	From, To   *time.Time
	Statuses   []string
	Kind       string // reminder|task|""
	Tag        string
	ReminderID int64
	TaskID     int64
	Limit      int
	// ByOutcome filters and orders by when the outcome happened (done_at,
	// falling back to scheduled_for) instead of by scheduled time.
	ByOutcome bool
}

// MemoFilter filters memo listings.
type MemoFilter struct {
	Tag      string
	Query    string
	Before   *time.Time // cursor: created_at < Before
	BeforeID int64
	From, To *time.Time
	Limit    int
}

// FirstLine returns the first non-empty line of s, trimmed of Markdown markers and capped in length.
func FirstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "#>-*"))
		if l != "" {
			if r := []rune(l); len(r) > 120 {
				return string(r[:117]) + "..."
			}
			return l
		}
	}
	return ""
}
