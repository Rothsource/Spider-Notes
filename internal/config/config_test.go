package config

import (
	"testing"
	"time"
)

func TestDefaultsAndValidation(t *testing.T) {
	c, err := LoadFrom(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":8080" || c.DBPath != "/data/app.db" || c.Location.String() != "Asia/Phnom_Penh" ||
		c.SessionIdleTimeout != 12*time.Hour || c.SchedulerInterval != 30*time.Second || c.ExportDir != "/data/export" {
		t.Fatalf("unexpected defaults %+v", c)
	}
	if c.ValidateForServe() == nil {
		t.Fatal("missing session secret must fail")
	}
	env := map[string]string{"APP_TIMEZONE": "Mars/Base", "TELEGRAM_CHAT_ID": "abc", "APP_SCHEDULER_INTERVAL": "1ms"}
	if _, err := LoadFrom(func(k string) string { return env[k] }); err == nil {
		t.Fatal("invalid values must fail")
	}
}
