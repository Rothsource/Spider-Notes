-- Expense tracking. Amounts are stored as integers in the currency's minor unit
-- (USD cents, KHR riel) so sums are exact.
CREATE TABLE expenses (
    id           INTEGER PRIMARY KEY,
    item         TEXT NOT NULL,
    amount_minor INTEGER NOT NULL CHECK (amount_minor > 0),
    currency     TEXT NOT NULL,
    category     TEXT NOT NULL DEFAULT '',
    payment      TEXT NOT NULL DEFAULT '',
    note         TEXT NOT NULL DEFAULT '',
    spent_at     TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    deleted_at   TEXT
);

CREATE INDEX idx_expenses_spent ON expenses(spent_at) WHERE deleted_at IS NULL;
CREATE INDEX idx_expenses_category ON expenses(category) WHERE deleted_at IS NULL;
