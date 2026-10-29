// Package config loads application configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration.
type Config struct {
	Listen             string
	DBPath             string
	Timezone           string
	Location           *time.Location
	AdminUsername      string
	AdminPassword      string
	SessionSecret      string
	SessionIdleTimeout time.Duration
	TelegramToken      string
	TelegramChatID     int64
	SchedulerInterval  time.Duration
	ExportDir          string
	TrustProxy         bool
	LogLevel           string
}

// Load reads configuration from the environment.
func Load() (*Config, error) {
	return LoadFrom(os.Getenv)
}

// LoadFrom reads configuration using the given lookup function (useful for tests).
func LoadFrom(get func(string) string) (*Config, error) {
	c := &Config{
		Listen:        def(get("APP_LISTEN"), ":8080"),
		DBPath:        def(get("APP_DB_PATH"), "/data/app.db"),
		Timezone:      def(get("APP_TIMEZONE"), "Asia/Phnom_Penh"),
		AdminUsername: strings.TrimSpace(get("APP_ADMIN_USERNAME")),
		AdminPassword: get("APP_ADMIN_PASSWORD"),
		SessionSecret: get("APP_SESSION_SECRET"),
		TelegramToken: strings.TrimSpace(get("TELEGRAM_BOT_TOKEN")),
		ExportDir:     def(get("APP_EXPORT_DIR"), "/data/export"),
		LogLevel:      def(get("APP_LOG_LEVEL"), "info"),
	}
	var errs []error

	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		errs = append(errs, fmt.Errorf("APP_TIMEZONE: %w", err))
	}
	c.Location = loc

	if c.SessionIdleTimeout, err = time.ParseDuration(def(get("APP_SESSION_IDLE_TIMEOUT"), "12h")); err != nil || c.SessionIdleTimeout <= 0 {
		errs = append(errs, fmt.Errorf("APP_SESSION_IDLE_TIMEOUT: invalid duration"))
	}
	if c.SchedulerInterval, err = time.ParseDuration(def(get("APP_SCHEDULER_INTERVAL"), "30s")); err != nil || c.SchedulerInterval < time.Second {
		errs = append(errs, fmt.Errorf("APP_SCHEDULER_INTERVAL: must be a duration >= 1s"))
	}
	if v := strings.TrimSpace(get("TELEGRAM_CHAT_ID")); v != "" {
		if c.TelegramChatID, err = strconv.ParseInt(v, 10, 64); err != nil {
			errs = append(errs, fmt.Errorf("TELEGRAM_CHAT_ID: must be an integer"))
		}
	}
	c.TrustProxy, _ = strconv.ParseBool(get("APP_TRUST_PROXY"))
	return c, errors.Join(errs...)
}

// ValidateForServe checks settings only required when running the server.
func (c *Config) ValidateForServe() error {
	if len(c.SessionSecret) < 32 {
		return errors.New("APP_SESSION_SECRET is required and must be at least 32 characters")
	}
	return nil
}

func def(v, d string) string {
	if strings.TrimSpace(v) == "" {
		return d
	}
	return strings.TrimSpace(v)
}
