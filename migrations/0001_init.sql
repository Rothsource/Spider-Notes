-- Timestamps are stored as UTC text in the fixed format YYYY-MM-DDTHH:MM:SSZ
-- so that lexical comparison equals chronological comparison.

CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    TEXT NOT NULL
);

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE sessions (
    id_hash      TEXT PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_token   TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    last_seen_at TEXT NOT NULL
);

CREATE TABLE api_tokens (
    id           INTEGER PRIMARY KEY,
    name         TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    last_used_at TEXT,
    created_at   TEXT NOT NULL,
    revoked_at   TEXT
);

CREATE TABLE tasks (
    id              INTEGER PRIMARY KEY,
    title           TEXT NOT NULL,
    description_md  TEXT NOT NULL DEFAULT '',
    due_at          TEXT,
    priority        TEXT NOT NULL DEFAULT 'normal' CHECK (priority IN ('low','normal','high')),
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','done')),
    repeat_rule     TEXT NOT NULL DEFAULT '',
    repeat_start_at TEXT,
    completed_at    TEXT,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    deleted_at      TEXT
);
CREATE INDEX idx_tasks_due_status ON tasks(due_at, status);

CREATE TABLE reminders (
    id                   INTEGER PRIMARY KEY,
    task_id              INTEGER REFERENCES tasks(id),
    message_md           TEXT NOT NULL,
    schedule_type        TEXT NOT NULL CHECK (schedule_type IN ('one_time','repeating')),
    fire_at              TEXT NOT NULL,
    repeat_rule          TEXT NOT NULL DEFAULT '',
    active_window        TEXT NOT NULL DEFAULT '',
    timezone             TEXT NOT NULL,
    nag_enabled          INTEGER NOT NULL DEFAULT 0,
    nag_interval_min     INTEGER NOT NULL DEFAULT 30,
    quiet_hours_override TEXT NOT NULL DEFAULT '',
    ignore_quiet_hours   INTEGER NOT NULL DEFAULT 0,
    enabled              INTEGER NOT NULL DEFAULT 1,
    next_fire_at         TEXT,
    last_sent_at         TEXT,
    fire_count           INTEGER NOT NULL DEFAULT 0,
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL,
    deleted_at           TEXT
);
CREATE INDEX idx_reminders_next_fire ON reminders(next_fire_at);
CREATE INDEX idx_reminders_task ON reminders(task_id);

CREATE TABLE occurrences (
    id                  INTEGER PRIMARY KEY,
    reminder_id         INTEGER REFERENCES reminders(id),
    task_id             INTEGER REFERENCES tasks(id),
    scheduled_for       TEXT NOT NULL,
    status              TEXT NOT NULL CHECK (status IN ('pending','sent','done','snoozed','skipped','missed')),
    sent_count          INTEGER NOT NULL DEFAULT 0,
    snooze_count        INTEGER NOT NULL DEFAULT 0,
    snoozed_until       TEXT,
    next_send_at        TEXT,
    last_sent_at        TEXT,
    done_at             TEXT,
    action_source       TEXT NOT NULL DEFAULT '',
    telegram_chat_id    INTEGER NOT NULL DEFAULT 0,
    telegram_message_id INTEGER NOT NULL DEFAULT 0,
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL,
    CHECK (reminder_id IS NOT NULL OR task_id IS NOT NULL)
);
-- One occurrence row per reminder (or reminder-less task) per scheduled time: makes the scheduler idempotent.
CREATE UNIQUE INDEX uq_occ_reminder_time ON occurrences(reminder_id, scheduled_for) WHERE reminder_id IS NOT NULL;
CREATE UNIQUE INDEX uq_occ_task_time ON occurrences(task_id, scheduled_for) WHERE reminder_id IS NULL AND task_id IS NOT NULL;
CREATE INDEX idx_occ_status_sched ON occurrences(status, scheduled_for);
CREATE INDEX idx_occ_next_send ON occurrences(next_send_at);

CREATE TABLE memos (
    id         INTEGER PRIMARY KEY,
    content_md TEXT NOT NULL,
    pinned     INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    deleted_at TEXT
);
CREATE INDEX idx_memos_created ON memos(created_at);

CREATE TABLE tags (
    id   INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE
);
CREATE TABLE task_tags (
    task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    tag_id  INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (task_id, tag_id)
);
CREATE TABLE memo_tags (
    memo_id INTEGER NOT NULL REFERENCES memos(id) ON DELETE CASCADE,
    tag_id  INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (memo_id, tag_id)
);
CREATE TABLE reminder_tags (
    reminder_id INTEGER NOT NULL REFERENCES reminders(id) ON DELETE CASCADE,
    tag_id      INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (reminder_id, tag_id)
);

-- Full-text search (external content tables kept in sync by triggers).
CREATE VIRTUAL TABLE memos_fts USING fts5(content_md, content='memos', content_rowid='id', tokenize='unicode61');
CREATE TRIGGER memos_ai AFTER INSERT ON memos BEGIN
    INSERT INTO memos_fts(rowid, content_md) VALUES (new.id, new.content_md);
END;
CREATE TRIGGER memos_ad AFTER DELETE ON memos BEGIN
    INSERT INTO memos_fts(memos_fts, rowid, content_md) VALUES ('delete', old.id, old.content_md);
END;
CREATE TRIGGER memos_au AFTER UPDATE OF content_md ON memos BEGIN
    INSERT INTO memos_fts(memos_fts, rowid, content_md) VALUES ('delete', old.id, old.content_md);
    INSERT INTO memos_fts(rowid, content_md) VALUES (new.id, new.content_md);
END;

CREATE VIRTUAL TABLE tasks_fts USING fts5(title, description_md, content='tasks', content_rowid='id', tokenize='unicode61');
CREATE TRIGGER tasks_ai AFTER INSERT ON tasks BEGIN
    INSERT INTO tasks_fts(rowid, title, description_md) VALUES (new.id, new.title, new.description_md);
END;
CREATE TRIGGER tasks_ad AFTER DELETE ON tasks BEGIN
    INSERT INTO tasks_fts(tasks_fts, rowid, title, description_md) VALUES ('delete', old.id, old.title, old.description_md);
END;
CREATE TRIGGER tasks_au AFTER UPDATE OF title, description_md ON tasks BEGIN
    INSERT INTO tasks_fts(tasks_fts, rowid, title, description_md) VALUES ('delete', old.id, old.title, old.description_md);
    INSERT INTO tasks_fts(rowid, title, description_md) VALUES (new.id, new.title, new.description_md);
END;

CREATE VIRTUAL TABLE reminders_fts USING fts5(message_md, content='reminders', content_rowid='id', tokenize='unicode61');
CREATE TRIGGER reminders_ai AFTER INSERT ON reminders BEGIN
    INSERT INTO reminders_fts(rowid, message_md) VALUES (new.id, new.message_md);
END;
CREATE TRIGGER reminders_ad AFTER DELETE ON reminders BEGIN
    INSERT INTO reminders_fts(reminders_fts, rowid, message_md) VALUES ('delete', old.id, old.message_md);
END;
CREATE TRIGGER reminders_au AFTER UPDATE OF message_md ON reminders BEGIN
    INSERT INTO reminders_fts(reminders_fts, rowid, message_md) VALUES ('delete', old.id, old.message_md);
    INSERT INTO reminders_fts(rowid, message_md) VALUES (new.id, new.message_md);
END;
