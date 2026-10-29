-- Tasks get an optional start time and a built-in alert (Telegram notification).
ALTER TABLE tasks ADD COLUMN start_at TEXT;               -- NULL = no start
ALTER TABLE tasks ADD COLUMN alert TEXT NOT NULL DEFAULT ''; -- '' | 'start' | 'due' | '5m' | ... | '1d' (before due)
ALTER TABLE tasks ADD COLUMN next_alert_at TEXT;          -- when the scheduler fires the alert; NULL = nothing pending

CREATE INDEX idx_tasks_next_alert ON tasks(next_alert_at) WHERE next_alert_at IS NOT NULL;
