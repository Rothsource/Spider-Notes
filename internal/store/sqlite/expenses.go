package sqlite

import (
	"context"
	"strings"
	"time"

	"github.com/sovannroth/mynotes/internal/model"
)

const expenseCols = `e.id, e.item, e.amount_minor, e.currency, e.category, e.payment, e.note, e.spent_at, e.created_at, e.updated_at, e.deleted_at`

func scanExpense(sc interface{ Scan(...any) error }) (model.Expense, error) {
	var e model.Expense
	err := sc.Scan(&e.ID, &e.Item, &e.AmountMinor, &e.Currency, &e.Category, &e.Payment, &e.Note,
		ts(&e.SpentAt), ts(&e.CreatedAt), ts(&e.UpdatedAt), tsp(&e.DeletedAt))
	e.Amount = model.FormatMinor(e.AmountMinor, e.Currency)
	return e, err
}

// CreateExpense implements store.Expenses.
func (d *DB) CreateExpense(ctx context.Context, e *model.Expense) error {
	res, err := d.q.ExecContext(ctx, `INSERT INTO expenses(item, amount_minor, currency, category, payment, note, spent_at, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		e.Item, e.AmountMinor, e.Currency, e.Category, e.Payment, e.Note, ft(e.SpentAt), ft(e.CreatedAt), ft(e.UpdatedAt))
	if err != nil {
		return err
	}
	e.ID, err = res.LastInsertId()
	return err
}

// UpdateExpense implements store.Expenses.
func (d *DB) UpdateExpense(ctx context.Context, e *model.Expense) error {
	return checkAffected(d.q.ExecContext(ctx, `UPDATE expenses SET item=?, amount_minor=?, currency=?, category=?, payment=?, note=?, spent_at=?, updated_at=?
		WHERE id=? AND deleted_at IS NULL`,
		e.Item, e.AmountMinor, e.Currency, e.Category, e.Payment, e.Note, ft(e.SpentAt), ft(e.UpdatedAt), e.ID))
}

// GetExpense implements store.Expenses.
func (d *DB) GetExpense(ctx context.Context, id int64) (*model.Expense, error) {
	e, err := scanExpense(d.q.QueryRowContext(ctx, `SELECT `+expenseCols+` FROM expenses e WHERE e.id = ? AND e.deleted_at IS NULL`, id))
	if err != nil {
		return nil, notFound(err)
	}
	return &e, nil
}

// ListExpenses implements store.Expenses. Newest first.
func (d *DB) ListExpenses(ctx context.Context, f model.ExpenseFilter) ([]model.Expense, error) {
	where := []string{"e.deleted_at IS NULL"}
	var args []any
	if f.From != nil {
		where = append(where, "e.spent_at >= ?")
		args = append(args, ft(*f.From))
	}
	if f.To != nil {
		where = append(where, "e.spent_at < ?")
		args = append(args, ft(*f.To))
	}
	if f.Category != "" {
		where = append(where, "e.category = ?")
		args = append(args, f.Category)
	}
	if f.Currency != "" {
		where = append(where, "e.currency = ?")
		args = append(args, f.Currency)
	}
	if f.Query != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(f.Query) + "%"
		where = append(where, `(e.item LIKE ? ESCAPE '\' OR e.category LIKE ? ESCAPE '\' OR e.payment LIKE ? ESCAPE '\' OR e.note LIKE ? ESCAPE '\')`)
		args = append(args, like, like, like, like)
	}
	q := `SELECT ` + expenseCols + ` FROM expenses e WHERE ` + strings.Join(where, " AND ") + ` ORDER BY e.spent_at DESC, e.id DESC`
	if f.Limit > 0 {
		q += ` LIMIT ?`
		args = append(args, f.Limit)
	}
	rows, err := d.q.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Expense
	for rows.Next() {
		e, err := scanExpense(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ExpenseValues implements store.Expenses: distinct non-empty values of
// category or payment, most used first.
func (d *DB) ExpenseValues(ctx context.Context, column string) ([]string, error) {
	if column != "category" && column != "payment" {
		return nil, nil
	}
	rows, err := d.q.QueryContext(ctx, `SELECT `+column+` FROM expenses WHERE deleted_at IS NULL AND `+column+` <> '' GROUP BY `+column+` ORDER BY COUNT(*) DESC, `+column+` LIMIT 100`) // #nosec G202 -- column is one of two fixed names
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// SoftDeleteExpense implements store.Expenses.
func (d *DB) SoftDeleteExpense(ctx context.Context, id int64, at time.Time) error {
	return checkAffected(d.q.ExecContext(ctx, `UPDATE expenses SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`, ft(at), ft(at), id))
}

// ---- expense categories ----

const expCatCols = `id, name, emoji, position, created_at`

func scanExpCat(sc interface{ Scan(...any) error }) (model.ExpenseCategory, error) {
	var c model.ExpenseCategory
	err := sc.Scan(&c.ID, &c.Name, &c.Emoji, &c.Position, ts(&c.CreatedAt))
	return c, err
}

// ListExpenseCategories implements store.Expenses.
func (d *DB) ListExpenseCategories(ctx context.Context) ([]model.ExpenseCategory, error) {
	rows, err := d.q.QueryContext(ctx, `SELECT `+expCatCols+` FROM expense_categories ORDER BY position, name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.ExpenseCategory
	for rows.Next() {
		c, err := scanExpCat(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetExpenseCategory implements store.Expenses.
func (d *DB) GetExpenseCategory(ctx context.Context, id int64) (*model.ExpenseCategory, error) {
	c, err := scanExpCat(d.q.QueryRowContext(ctx, `SELECT `+expCatCols+` FROM expense_categories WHERE id = ?`, id))
	if err != nil {
		return nil, notFound(err)
	}
	return &c, nil
}

// FindExpenseCategory implements store.Expenses (case-insensitive).
func (d *DB) FindExpenseCategory(ctx context.Context, name string) (*model.ExpenseCategory, error) {
	c, err := scanExpCat(d.q.QueryRowContext(ctx, `SELECT `+expCatCols+` FROM expense_categories WHERE name = ? COLLATE NOCASE`, name))
	if err != nil {
		return nil, notFound(err)
	}
	return &c, nil
}

// CreateExpenseCategory implements store.Expenses.
func (d *DB) CreateExpenseCategory(ctx context.Context, c *model.ExpenseCategory) error {
	res, err := d.q.ExecContext(ctx, `INSERT INTO expense_categories(name, emoji, position, created_at) VALUES (?,?,?,?)`,
		c.Name, c.Emoji, c.Position, ft(c.CreatedAt))
	if err != nil {
		return err
	}
	c.ID, err = res.LastInsertId()
	return err
}

// UpdateExpenseCategory implements store.Expenses. A rename also renames the
// category on existing expenses.
func (d *DB) UpdateExpenseCategory(ctx context.Context, c *model.ExpenseCategory, oldName string) error {
	if err := checkAffected(d.q.ExecContext(ctx, `UPDATE expense_categories SET name=?, emoji=?, position=? WHERE id=?`, c.Name, c.Emoji, c.Position, c.ID)); err != nil {
		return err
	}
	if oldName != c.Name {
		_, err := d.q.ExecContext(ctx, `UPDATE expenses SET category = ? WHERE category = ?`, c.Name, oldName)
		return err
	}
	return nil
}

// DeleteExpenseCategory implements store.Expenses. Existing expenses keep the name.
func (d *DB) DeleteExpenseCategory(ctx context.Context, id int64) error {
	return checkAffected(d.q.ExecContext(ctx, `DELETE FROM expense_categories WHERE id = ?`, id))
}
