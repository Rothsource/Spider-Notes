// Package service holds the business logic shared by the web UI, JSON API,
// scheduler and Telegram bot.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/schedule"
	"github.com/sovannroth/mynotes/internal/store"
)

// Limits for user input.
const (
	MaxMemoLen    = 20000
	MaxTitleLen   = 300
	MaxTextLen    = 20000
	MaxMessageLen = 4000 // Telegram message limit is 4096
)

// Notifier delivers reminder messages (Telegram). Implementations must not block.
type Notifier interface {
	Enabled() bool
	// Deliver sends a new message with action buttons for the occurrence.
	Deliver(m Message)
	// Finalize edits an earlier message to show text and removes its buttons.
	Finalize(chatID int64, messageID int, text string)
}

// Message is an outgoing reminder notification.
type Message struct {
	OccurrenceID int64
	Text         string
	AllowSkip    bool
}

type nopNotifier struct{}

func (nopNotifier) Enabled() bool               { return false }
func (nopNotifier) Deliver(Message)             {}
func (nopNotifier) Finalize(int64, int, string) {}

// Settings are user-configurable values stored in the DB.
type Settings struct {
	QuietHours         string // "HH:MM-HH:MM" or ""
	SnoozeMorning      string // "HH:MM"
	DefaultNagInterval int
	ExportDaily        bool
	ExportTime         string // "HH:MM"
	TelegramToken      string
	TelegramChatID     int64
	LastAutoExport     string // YYYY-MM-DD
}

// Setting keys.
const (
	KeyQuietHours     = "quiet_hours"
	KeySnoozeMorning  = "snooze_morning"
	KeyNagDefault     = "nag_default_interval"
	KeyExportDaily    = "export_daily"
	KeyExportTime     = "export_time"
	KeyTelegramToken  = "telegram_token"
	KeyTelegramChatID = "telegram_chat_id"
	KeyLastAutoExport = "last_auto_export"
)

// Options configure a Service.
type Options struct {
	Store          store.Store
	Location       *time.Location
	ExportDir      string
	EnvTelegramTok string
	EnvTelegramID  int64
	Now            func() time.Time
	AttachDir      string // where uploaded files are stored
	MaxUpload      int64  // bytes; 0 = 50 MB
}

// Service implements all use cases.
type Service struct {
	st        store.Store
	loc       *time.Location
	now       func() time.Time
	exportDir string
	envTgTok  string
	envTgID   int64
	attachDir string
	maxUpload int64

	mu         sync.RWMutex
	notifier   Notifier
	onTelegram func(token string, chatID int64)
}

// New creates a Service.
func New(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Location == nil {
		o.Location = time.UTC
	}
	if o.MaxUpload <= 0 {
		o.MaxUpload = 50 << 20
	}
	return &Service{st: o.Store, loc: o.Location, now: o.Now, exportDir: o.ExportDir,
		envTgTok: o.EnvTelegramTok, envTgID: o.EnvTelegramID, notifier: nopNotifier{},
		attachDir: o.AttachDir, maxUpload: o.MaxUpload}
}

// SetNotifier installs the notifier (nil disables notifications).
func (s *Service) SetNotifier(n Notifier) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n == nil {
		n = nopNotifier{}
	}
	s.notifier = n
}

// OnTelegramChange registers a callback invoked when Telegram settings change.
func (s *Service) OnTelegramChange(fn func(token string, chatID int64)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onTelegram = fn
}

func (s *Service) notify() Notifier {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.notifier
}

// Location returns the app time zone.
func (s *Service) Location() *time.Location { return s.loc }

// Now returns the current time in the app zone.
func (s *Service) Now() time.Time { return s.now().In(s.loc) }

// Store exposes the underlying store (used by auth).
func (s *Service) Store() store.Store { return s.st }

// Settings loads settings, applying defaults and env fallbacks.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	m, err := s.st.GetSettings(ctx)
	if err != nil {
		return Settings{}, err
	}
	set := Settings{
		QuietHours:         m[KeyQuietHours],
		SnoozeMorning:      valueOr(m[KeySnoozeMorning], "07:00"),
		DefaultNagInterval: 30,
		ExportTime:         valueOr(m[KeyExportTime], "23:55"),
		ExportDaily:        m[KeyExportDaily] == "1",
		LastAutoExport:     m[KeyLastAutoExport],
		TelegramToken:      s.envTgTok,
		TelegramChatID:     s.envTgID,
	}
	if n, err := strconv.Atoi(m[KeyNagDefault]); err == nil && n > 0 {
		set.DefaultNagInterval = n
	}
	// DB values override env once they have been saved from the Settings page.
	if v, ok := m[KeyTelegramToken]; ok {
		set.TelegramToken = v
	}
	if v, ok := m[KeyTelegramChatID]; ok {
		set.TelegramChatID, _ = strconv.ParseInt(v, 10, 64)
	}
	return set, nil
}

func valueOr(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// SettingsInput updates general settings.
type SettingsInput struct {
	QuietHours         string
	SnoozeMorning      string
	DefaultNagInterval int
	ExportDaily        bool
	ExportTime         string
}

// UpdateSettings validates and stores general settings.
func (s *Service) UpdateSettings(ctx context.Context, in SettingsInput) error {
	in.QuietHours = strings.TrimSpace(in.QuietHours)
	if _, _, err := schedule.ParseWindow(in.QuietHours); err != nil {
		return model.Invalid("Quiet hours: " + err.Error())
	}
	if _, err := schedule.ParseHM(in.SnoozeMorning); err != nil {
		return model.Invalid("Snooze morning time: " + err.Error())
	}
	if _, err := schedule.ParseHM(in.ExportTime); err != nil {
		return model.Invalid("Export time: " + err.Error())
	}
	if in.DefaultNagInterval < 1 || in.DefaultNagInterval > 1440 {
		return model.Invalid("Default nag interval must be 1..1440 minutes")
	}
	return s.st.InTx(ctx, func(tx store.Store) error {
		kv := map[string]string{
			KeyQuietHours:    in.QuietHours,
			KeySnoozeMorning: in.SnoozeMorning,
			KeyNagDefault:    strconv.Itoa(in.DefaultNagInterval),
			KeyExportTime:    in.ExportTime,
			KeyExportDaily:   map[bool]string{true: "1", false: "0"}[in.ExportDaily],
		}
		for k, v := range kv {
			if err := tx.SetSetting(ctx, k, v); err != nil {
				return err
			}
		}
		return nil
	})
}

// UpdateTelegram stores Telegram settings and notifies the bot manager.
// An empty token disables Telegram. If token is "-" the stored token is kept.
func (s *Service) UpdateTelegram(ctx context.Context, token, chatID string) error {
	cur, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	token = strings.TrimSpace(token)
	if token == "-" {
		token = cur.TelegramToken
	}
	var id int64
	if strings.TrimSpace(chatID) != "" {
		if id, err = strconv.ParseInt(strings.TrimSpace(chatID), 10, 64); err != nil {
			return model.Invalid("Chat ID must be a number (negative for groups)")
		}
	}
	if token != "" && (!strings.Contains(token, ":") || len(token) > 100) {
		return model.Invalid("Bot token looks invalid (expected 123456:ABC...)")
	}
	err = s.st.InTx(ctx, func(tx store.Store) error {
		if err := tx.SetSetting(ctx, KeyTelegramToken, token); err != nil {
			return err
		}
		return tx.SetSetting(ctx, KeyTelegramChatID, strconv.FormatInt(id, 10))
	})
	if err != nil {
		return err
	}
	s.mu.RLock()
	fn := s.onTelegram
	s.mu.RUnlock()
	if fn != nil {
		fn(token, id)
	}
	return nil
}

func (s *Service) quietFor(set Settings, r *model.Reminder) schedule.Quiet {
	return schedule.ResolveQuiet(set.QuietHours, r.QuietHoursOverride, r.IgnoreQuietHours)
}

func (s *Service) fmtTime(t time.Time) string { return t.In(s.loc).Format("Mon 02 Jan 15:04") }

func (s *Service) hm(t time.Time) string { return t.In(s.loc).Format("15:04") }

// StartOfDay returns local midnight of t's day.
func (s *Service) StartOfDay(t time.Time) time.Time {
	t = t.In(s.loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, s.loc)
}

func logErr(msg string, err error, args ...any) {
	if err != nil {
		slog.Error(msg, append(args, "err", err)...)
	}
}

func ptr[T any](v T) *T { return &v }

func trimLen(s string, n int) (string, error) {
	s = strings.TrimSpace(s)
	if len([]rune(s)) > n {
		return s, model.Invalid(fmt.Sprintf("text is too long (max %d characters)", n))
	}
	return s, nil
}
