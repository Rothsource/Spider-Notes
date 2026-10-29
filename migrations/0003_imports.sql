-- Records items imported from other apps so re-running an import never duplicates them.
CREATE TABLE imports (
    source      TEXT NOT NULL,    -- e.g. 'memos'
    source_key  TEXT NOT NULL,    -- id/uid in the source app
    target_kind TEXT NOT NULL,    -- 'memo'
    target_id   INTEGER NOT NULL,
    imported_at TEXT NOT NULL,
    PRIMARY KEY (source, source_key)
);
