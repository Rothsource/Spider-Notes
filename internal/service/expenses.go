package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/store"
	"github.com/sovannroth/mynotes/internal/xlsx"
)

// Limits for expenses.
const (
	MaxExpenseItemLen  = 200
	MaxExpenseLabelLen = 60
	maxAmountMinor     = 1_000_000_000_000_000 // keeps sums far from int64 overflow
)

// ExpenseInput is used to create or update an expense.
type ExpenseInput struct {
	Item     string      `json:"item"`
	Amount   json.Number `json:"amount"` // decimal, as number or string: 12.5, "12.50", "1,000"
	Currency string      `json:"currency"`
	Category string      `json:"category"`
	Payment  string      `json:"payment"`
	Note     string      `json:"note"`
	SpentAt  *time.Time  `json:"spent_at"` // nil = now
}

// ParseAmount converts a decimal string to minor units of the currency without
// floating point. Commas, spaces and currency symbols are ignored.
func ParseAmount(s, currency string) (int64, error) {
	c, ok := model.LookupCurrency(currency)
	if !ok {
		return 0, model.Invalid("Unknown currency")
	}
	s = strings.Map(func(r rune) rune {
		if (r >= '0' && r <= '9') || r == '.' || r == '-' {
			return r
		}
		if r == ',' || r == ' ' || r == '$' || r == '៛' || r == '฿' || r == '€' {
			return -1
		}
		return 'x'
	}, strings.TrimSpace(s))
	if s == "" {
		return 0, model.Invalid("Amount is required")
	}
	whole, frac, hasDot := strings.Cut(s, ".")
	if strings.ContainsAny(whole, "-x") || strings.ContainsAny(frac, ".-x") || (whole == "" && frac == "") {
		if strings.HasPrefix(s, "-") {
			return 0, model.Invalid("Amount must be more than zero")
		}
		return 0, model.Invalid("Amount must be a number, e.g. 12.50")
	}
	if hasDot && len(frac) > c.Decimals {
		if c.Decimals == 0 {
			return 0, model.Invalid(c.Code + " amounts have no decimals")
		}
		return 0, model.Invalid("Use at most 2 decimals")
	}
	for len(frac) < c.Decimals {
		frac += "0"
	}
	var n int64
	for _, r := range whole + frac {
		n = n*10 + int64(r-'0')
		if n > maxAmountMinor {
			return 0, model.Invalid("Amount is too large")
		}
	}
	if n == 0 {
		return 0, model.Invalid("Amount must be more than zero")
	}
	return n, nil
}

func cleanLabel(s, what string) (string, error) {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > MaxExpenseLabelLen {
		return "", model.Invalid(what + " is too long")
	}
	return s, nil
}

func (s *Service) buildExpense(in ExpenseInput, e *model.Expense, now time.Time) error {
	var err error
	if e.Item, err = trimLen(in.Item, MaxExpenseItemLen); err != nil {
		return model.Invalid("Item is too long")
	}
	if e.Item == "" {
		return model.Invalid("What did you spend on? Item is required")
	}
	e.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if e.Currency == "" {
		e.Currency = model.Currencies[0].Code
	}
	if e.AmountMinor, err = ParseAmount(in.Amount.String(), e.Currency); err != nil {
		return err
	}
	e.Amount = model.FormatMinor(e.AmountMinor, e.Currency)
	if e.Category, err = cleanLabel(in.Category, "Category"); err != nil {
		return err
	}
	if e.Payment, err = cleanLabel(in.Payment, "Payment method"); err != nil {
		return err
	}
	if e.Note, err = trimLen(in.Note, MaxTextLen); err != nil {
		return model.Invalid("Description is too long")
	}
	switch {
	case in.SpentAt != nil:
		e.SpentAt = in.SpentAt.UTC().Truncate(time.Second)
	case e.ID == 0:
		e.SpentAt = now
	}
	return nil
}

// CreateExpense records an expense; the time defaults to now.
func (s *Service) CreateExpense(ctx context.Context, in ExpenseInput) (*model.Expense, error) {
	now := s.now().UTC().Truncate(time.Second)
	e := &model.Expense{CreatedAt: now, UpdatedAt: now}
	if err := s.buildExpense(in, e, now); err != nil {
		return nil, err
	}
	err := s.st.InTx(ctx, func(tx store.Store) error {
		var err error
		if e.Category, err = s.ensureCategory(ctx, tx, e.Category, now); err != nil {
			return err
		}
		return tx.CreateExpense(ctx, e)
	})
	return e, err
}

// UpdateExpense edits an expense; a nil SpentAt keeps the recorded time.
func (s *Service) UpdateExpense(ctx context.Context, id int64, in ExpenseInput) (*model.Expense, error) {
	e, err := s.st.GetExpense(ctx, id)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC().Truncate(time.Second)
	if err := s.buildExpense(in, e, now); err != nil {
		return nil, err
	}
	e.UpdatedAt = now
	err = s.st.InTx(ctx, func(tx store.Store) error {
		var err error
		if e.Category, err = s.ensureCategory(ctx, tx, e.Category, now); err != nil {
			return err
		}
		return tx.UpdateExpense(ctx, e)
	})
	return e, err
}

// GetExpense returns one expense.
func (s *Service) GetExpense(ctx context.Context, id int64) (*model.Expense, error) {
	return s.st.GetExpense(ctx, id)
}

// DeleteExpense soft-deletes an expense.
func (s *Service) DeleteExpense(ctx context.Context, id int64) error {
	return s.st.SoftDeleteExpense(ctx, id, s.now().UTC())
}

// ListExpenses lists expenses, newest first.
func (s *Service) ListExpenses(ctx context.Context, f model.ExpenseFilter) ([]model.Expense, error) {
	f.Query = strings.TrimSpace(f.Query)
	return s.st.ListExpenses(ctx, f)
}

// ExpensePayments returns payment methods used before (most used first) for autocomplete.
func (s *Service) ExpensePayments(ctx context.Context) ([]string, error) {
	return s.st.ExpenseValues(ctx, "payment")
}

// customCategoryPosition sorts user-added categories after the presets but before "Other".
const customCategoryPosition = 900

// CategoryInput creates or edits an expense category.
type CategoryInput struct {
	Name  string `json:"name"`
	Emoji string `json:"emoji"`
}

func validCategory(in CategoryInput) (name, emoji string, err error) {
	if name, err = cleanLabel(in.Name, "Category name"); err != nil {
		return "", "", err
	}
	if name == "" {
		return "", "", model.Invalid("Category name is required")
	}
	emoji = strings.TrimSpace(in.Emoji)
	if len([]rune(emoji)) > 12 || strings.ContainsAny(emoji, " <>&\"'") {
		return "", "", model.Invalid("Use a single emoji (or leave it empty)")
	}
	return name, emoji, nil
}

// ensureCategory returns the stored spelling of a category, creating it when new.
func (s *Service) ensureCategory(ctx context.Context, tx store.Store, name string, now time.Time) (string, error) {
	if name == "" {
		return "", nil
	}
	c, err := tx.FindExpenseCategory(ctx, name)
	if err == nil {
		return c.Name, nil
	}
	if !errors.Is(err, model.ErrNotFound) {
		return "", err
	}
	return name, tx.CreateExpenseCategory(ctx, &model.ExpenseCategory{Name: name, Position: customCategoryPosition, CreatedAt: now})
}

// ListExpenseCategories returns the categories in display order.
func (s *Service) ListExpenseCategories(ctx context.Context) ([]model.ExpenseCategory, error) {
	return s.st.ListExpenseCategories(ctx)
}

// CreateExpenseCategory adds a category.
func (s *Service) CreateExpenseCategory(ctx context.Context, in CategoryInput) (*model.ExpenseCategory, error) {
	name, emoji, err := validCategory(in)
	if err != nil {
		return nil, err
	}
	c := &model.ExpenseCategory{Name: name, Emoji: emoji, Position: customCategoryPosition, CreatedAt: s.now().UTC()}
	err = s.st.InTx(ctx, func(tx store.Store) error {
		if _, err := tx.FindExpenseCategory(ctx, name); err == nil {
			return model.Invalid("Category \u201c" + name + "\u201d already exists")
		}
		return tx.CreateExpenseCategory(ctx, c)
	})
	return c, err
}

// UpdateExpenseCategory renames a category (existing expenses follow) or changes its emoji.
func (s *Service) UpdateExpenseCategory(ctx context.Context, id int64, in CategoryInput) (*model.ExpenseCategory, error) {
	name, emoji, err := validCategory(in)
	if err != nil {
		return nil, err
	}
	var c *model.ExpenseCategory
	err = s.st.InTx(ctx, func(tx store.Store) error {
		if c, err = tx.GetExpenseCategory(ctx, id); err != nil {
			return err
		}
		if other, err := tx.FindExpenseCategory(ctx, name); err == nil && other.ID != id {
			return model.Invalid("Category \u201c" + name + "\u201d already exists")
		}
		old := c.Name
		c.Name, c.Emoji = name, emoji
		return tx.UpdateExpenseCategory(ctx, c, old)
	})
	return c, err
}

// DeleteExpenseCategory removes a category from the picker; expenses keep their category text.
func (s *Service) DeleteExpenseCategory(ctx context.Context, id int64) error {
	return s.st.DeleteExpenseCategory(ctx, id)
}

// LastCurrency is the currency of the most recent expense (default for new ones).
func (s *Service) LastCurrency(ctx context.Context) string {
	if list, err := s.st.ListExpenses(ctx, model.ExpenseFilter{Limit: 1}); err == nil && len(list) > 0 {
		return list[0].Currency
	}
	return model.Currencies[0].Code
}

// MoneyTotal is a sum in one currency.
type MoneyTotal struct {
	Currency string `json:"currency"`
	Minor    int64  `json:"amount_minor"`
	Amount   string `json:"amount"`
	Display  string `json:"display"`
	Count    int    `json:"count"`
}

// CategoryTotal is a sum per category and currency.
type CategoryTotal struct {
	Category string `json:"category"` // "" = uncategorised
	MoneyTotal
	Share float64 `json:"share"` // of the currency's total, 0..1
}

// ExpenseSummary totals a list of expenses.
type ExpenseSummary struct {
	Count      int             `json:"count"`
	Totals     []MoneyTotal    `json:"totals"`      // per currency, largest count first
	ByCategory []CategoryTotal `json:"by_category"` // largest first within each currency
}

func money(cur string, minor int64, count int) MoneyTotal {
	return MoneyTotal{Currency: cur, Minor: minor, Amount: model.FormatMinor(minor, cur), Display: model.FormatMoney(minor, cur), Count: count}
}

// SummarizeExpenses computes totals per currency and per category.
func SummarizeExpenses(list []model.Expense) ExpenseSummary {
	type key struct{ cat, cur string }
	tot := map[string]*MoneyTotal{}
	cats := map[key]*CategoryTotal{}
	for _, e := range list {
		t := tot[e.Currency]
		if t == nil {
			t = &MoneyTotal{Currency: e.Currency}
			tot[e.Currency] = t
		}
		t.Minor += e.AmountMinor
		t.Count++
		k := key{e.Category, e.Currency}
		c := cats[k]
		if c == nil {
			c = &CategoryTotal{Category: e.Category, MoneyTotal: MoneyTotal{Currency: e.Currency}}
			cats[k] = c
		}
		c.Minor += e.AmountMinor
		c.Count++
	}
	sum := ExpenseSummary{Count: len(list)}
	for cur, t := range tot {
		sum.Totals = append(sum.Totals, money(cur, t.Minor, t.Count))
	}
	sort.Slice(sum.Totals, func(i, j int) bool {
		a, b := sum.Totals[i], sum.Totals[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Currency < b.Currency
	})
	order := map[string]int{}
	for i, t := range sum.Totals {
		order[t.Currency] = i
	}
	for _, c := range cats {
		ct := CategoryTotal{Category: c.Category, MoneyTotal: money(c.Currency, c.Minor, c.Count)}
		if total := tot[c.Currency].Minor; total > 0 {
			ct.Share = float64(c.Minor) / float64(total)
		}
		sum.ByCategory = append(sum.ByCategory, ct)
	}
	sort.Slice(sum.ByCategory, func(i, j int) bool {
		a, b := sum.ByCategory[i], sum.ByCategory[j]
		if a.Currency != b.Currency {
			return order[a.Currency] < order[b.Currency]
		}
		if a.Minor != b.Minor {
			return a.Minor > b.Minor
		}
		return a.Category < b.Category
	})
	return sum
}

// XLSXContentType is the media type of Excel workbooks.
const XLSXContentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

// ExpenseExportName returns the download file name and the summary title for an export.
func (s *Service) ExpenseExportName(f model.ExpenseFilter) (filename, title string) {
	name, title := "expenses", "Expenses"
	if f.From != nil && f.To != nil {
		from, last := f.From.In(s.loc).Format("2006-01-02"), f.To.In(s.loc).Add(-time.Second).Format("2006-01-02")
		name += "_" + from + "_to_" + last
		title += " " + from + " to " + last
	}
	if f.Category != "" {
		title += " · " + f.Category
	}
	return name + ".xlsx", title
}

// ExportExpensesXLSX writes the filtered expenses as an Excel workbook with an
// "Expenses" sheet (one row per expense) and a "Summary" sheet.
func (s *Service) ExportExpensesXLSX(ctx context.Context, w io.Writer, f model.ExpenseFilter, title string) error {
	list, err := s.ListExpenses(ctx, f)
	if err != nil {
		return err
	}
	moneyStyle := func(cur string, bold bool) int {
		c, _ := model.LookupCurrency(cur)
		switch {
		case c.Decimals == 0 && bold:
			return xlsx.BoldM0
		case c.Decimals == 0:
			return xlsx.Money0
		case bold:
			return xlsx.BoldM2
		}
		return xlsx.Money2
	}
	value := func(minor int64, cur string) float64 {
		c, _ := model.LookupCurrency(cur)
		v := float64(minor)
		for i := 0; i < c.Decimals; i++ {
			v /= 10
		}
		return v
	}
	h := func(s string) xlsx.Cell { return xlsx.Cell{V: s, Style: xlsx.Bold} }
	rows := [][]xlsx.Cell{{h("Date & time"), h("Item"), h("Category"), h("Amount"), h("Currency"), h("Payment"), h("Description")}}
	// Oldest first reads naturally in a spreadsheet.
	for i := len(list) - 1; i >= 0; i-- {
		e := list[i]
		rows = append(rows, []xlsx.Cell{
			{V: e.SpentAt.In(s.loc), Style: xlsx.DateTime},
			{V: e.Item}, {V: e.Category},
			{V: value(e.AmountMinor, e.Currency), Style: moneyStyle(e.Currency, false)},
			{V: e.Currency}, {V: e.Payment}, {V: e.Note},
		})
	}
	sum := SummarizeExpenses(list)
	srows := [][]xlsx.Cell{{h(title)}, {}, {h("Currency"), h("Total"), h("Expenses")}}
	for _, t := range sum.Totals {
		srows = append(srows, []xlsx.Cell{{V: t.Currency}, {V: value(t.Minor, t.Currency), Style: moneyStyle(t.Currency, false)}, {V: t.Count}})
	}
	srows = append(srows, []xlsx.Cell{}, []xlsx.Cell{h("Category"), h("Currency"), h("Total"), h("Expenses"), h("Share")})
	for _, c := range sum.ByCategory {
		name := c.Category
		if name == "" {
			name = "(none)"
		}
		srows = append(srows, []xlsx.Cell{{V: name}, {V: c.Currency}, {V: value(c.Minor, c.Currency), Style: moneyStyle(c.Currency, false)},
			{V: c.Count}, {V: strconv.FormatFloat(math.Round(c.Share*1000)/10, 'f', -1, 64) + "%"}})
	}
	srows = append(srows, []xlsx.Cell{}, []xlsx.Cell{{V: "Exported " + s.Now().Format("2006-01-02 15:04") + " (" + s.loc.String() + ")"}})
	return xlsx.Write(w, []xlsx.Sheet{
		{Name: "Expenses", Rows: rows, Widths: []float64{18, 32, 16, 14, 10, 14, 48}, FreezeHeader: true, AutoFilter: true},
		{Name: "Summary", Rows: srows, Widths: []float64{24, 12, 14, 10, 10}},
	})
}
