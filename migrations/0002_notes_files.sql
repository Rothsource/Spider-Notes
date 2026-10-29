-- Folders of notes and uploaded files (attachments).

CREATE TABLE folders (
    id         INTEGER PRIMARY KEY,
    parent_id  INTEGER REFERENCES folders(id),
    name       TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    deleted_at TEXT
);
CREATE INDEX idx_folders_parent ON folders(parent_id);

CREATE TABLE notes (
    id         INTEGER PRIMARY KEY,
    folder_id  INTEGER REFERENCES folders(id),
    title      TEXT NOT NULL,
    content_md TEXT NOT NULL DEFAULT '',
    pinned     INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    deleted_at TEXT
);
CREATE INDEX idx_notes_folder ON notes(folder_id);

CREATE TABLE note_tags (
    note_id INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    tag_id  INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (note_id, tag_id)
);

-- A file either lives in a folder (folder_id, or root when all owner ids are NULL)
-- or is attached to a memo or note. The bytes are stored on disk under storage_key.
CREATE TABLE attachments (
    id          INTEGER PRIMARY KEY,
    folder_id   INTEGER REFERENCES folders(id),
    memo_id     INTEGER REFERENCES memos(id),
    note_id     INTEGER REFERENCES notes(id),
    filename    TEXT NOT NULL,
    mime        TEXT NOT NULL,
    size        INTEGER NOT NULL,
    sha256      TEXT NOT NULL,
    storage_key TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    deleted_at  TEXT
);
CREATE INDEX idx_attachments_folder ON attachments(folder_id);
CREATE INDEX idx_attachments_memo ON attachments(memo_id);
CREATE INDEX idx_attachments_note ON attachments(note_id);

CREATE VIRTUAL TABLE notes_fts USING fts5(title, content_md, content='notes', content_rowid='id', tokenize='unicode61');
CREATE TRIGGER notes_ai AFTER INSERT ON notes BEGIN
    INSERT INTO notes_fts(rowid, title, content_md) VALUES (new.id, new.title, new.content_md);
END;
CREATE TRIGGER notes_ad AFTER DELETE ON notes BEGIN
    INSERT INTO notes_fts(notes_fts, rowid, title, content_md) VALUES ('delete', old.id, old.title, old.content_md);
END;
CREATE TRIGGER notes_au AFTER UPDATE OF title, content_md ON notes BEGIN
    INSERT INTO notes_fts(notes_fts, rowid, title, content_md) VALUES ('delete', old.id, old.title, old.content_md);
    INSERT INTO notes_fts(rowid, title, content_md) VALUES (new.id, new.title, new.content_md);
END;
