-- Expense categories the user picks from (expenses.category keeps the name as text).
CREATE TABLE expense_categories (
    id         INTEGER PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE COLLATE NOCASE,
    emoji      TEXT NOT NULL DEFAULT '',
    position   INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL
);

INSERT INTO expense_categories(name, emoji, position, created_at) VALUES
    ('Food & Drinks', '🍜', 10,  strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    ('Groceries',     '🛒', 20,  strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    ('Transport',     '🛺', 30,  strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    ('Shopping',      '🛍️', 40,  strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    ('Bills',         '💡', 50,  strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    ('Rent',          '🏠', 60,  strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    ('Phone & Internet', '📱', 70, strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    ('Health',        '💊', 80,  strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    ('Education',     '📚', 90,  strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    ('Entertainment', '🎬', 100, strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    ('Family',        '👪', 110, strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    ('Gifts',         '🎁', 120, strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    ('Travel',        '✈️', 130, strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    ('Other',         '📦', 1000, strftime('%Y-%m-%dT%H:%M:%SZ', 'now'));

-- Keep categories already typed on existing expenses.
INSERT OR IGNORE INTO expense_categories(name, emoji, position, created_at)
    SELECT DISTINCT category, '', 500, strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
    FROM expenses WHERE deleted_at IS NULL AND category <> '';
