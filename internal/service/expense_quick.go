package service

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/store"
)

// Quick expense text, as typed in a chat: "lunch 4.5", "tuk tuk 8000r #transport @cash".
var (
	quickAmountRe = regexp.MustCompile(`(?i)^([$€฿៛])?(\d{1,3}(?:,\d{3})+|\d+)(?:\.(\d+))?([$€฿៛]|r|riels?|usd|khr|thb|eur|baht)?$`)
	quickCurRe    = regexp.MustCompile(`(?i)^([$€฿៛]|r|riels?|usd|khr|thb|eur|baht|dollars?)$`)
)

func quickCurrency(s string) string {
	switch strings.ToLower(s) {
	case "$", "usd", "dollar", "dollars":
		return "USD"
	case "៛", "r", "riel", "riels", "khr":
		return "KHR"
	case "฿", "thb", "baht":
		return "THB"
	case "€", "eur":
		return "EUR"
	}
	return ""
}

// QuickExpense is a parsed chat message.
type QuickExpense struct {
	Item, Amount, Currency string // Currency "" = not given
	Category, Payment      string // as typed after # and @ ("" = none)
}

// ParseQuickExpense reads an amount, an item, an optional #category and @payment
// from free text. The amount is the token with a currency mark, else the last number.
func ParseQuickExpense(text string) (QuickExpense, error) {
	var q QuickExpense
	tokens := strings.Fields(text)
	amountAt, marked := -1, false
	for i, t := range tokens {
		m := quickAmountRe.FindStringSubmatch(t)
		if m == nil {
			continue
		}
		hasMark := m[1] != "" || m[4] != "" || (i+1 < len(tokens) && quickCurRe.MatchString(tokens[i+1]))
		if hasMark || !marked {
			amountAt, marked = i, hasMark
		}
	}
	if amountAt < 0 {
		return q, model.Invalid("I couldn't find an amount. Try: lunch 4.5")
	}
	m := quickAmountRe.FindStringSubmatch(tokens[amountAt])
	q.Amount = m[2]
	if m[3] != "" {
		q.Amount += "." + m[3]
	}
	q.Currency = quickCurrency(m[1] + m[4])
	if m[1] != "" && m[4] != "" {
		q.Currency = quickCurrency(m[4])
	}
	skip := map[int]bool{amountAt: true}
	if q.Currency == "" && amountAt+1 < len(tokens) && quickCurRe.MatchString(tokens[amountAt+1]) {
		q.Currency = quickCurrency(tokens[amountAt+1])
		skip[amountAt+1] = true
	}
	var item []string
	for i, t := range tokens {
		switch {
		case skip[i]:
		case len(t) > 1 && strings.HasPrefix(t, "#"):
			q.Category = strings.TrimSpace(q.Category + " " + strings.ReplaceAll(t[1:], "_", " "))
		case len(t) > 1 && strings.HasPrefix(t, "@"):
			q.Payment = strings.ReplaceAll(t[1:], "_", " ")
		default:
			item = append(item, t)
		}
	}
	q.Item = strings.Join(item, " ")
	if q.Item == "" {
		return q, model.Invalid("What was it for? Try: lunch 4.5")
	}
	if r := []rune(q.Item); unicode.IsLower(r[0]) {
		r[0] = unicode.ToUpper(r[0])
		q.Item = string(r)
	}
	return q, nil
}

// letters keeps lower-cased letters and digits (drops emoji, "&", spaces).
func letters(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
}

// MatchCategory finds the category a short query means: exact name, then a
// name or any word in it starting with the query ("food" → "Food & Drinks").
// It returns the query itself when nothing matches (a new category).
func MatchCategory(cats []model.ExpenseCategory, query string) string {
	q := letters(query)
	if q == "" {
		return ""
	}
	for _, c := range cats {
		if letters(c.Name) == q {
			return c.Name
		}
	}
	for _, c := range cats {
		if strings.HasPrefix(letters(c.Name), q) {
			return c.Name
		}
	}
	for _, c := range cats {
		for _, w := range strings.Fields(c.Name) {
			if lw := letters(w); lw != "" && strings.HasPrefix(lw, q) {
				return c.Name
			}
		}
	}
	r := []rune(strings.TrimSpace(query))
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// CreateQuickExpense records an expense from chat text. The currency defaults to
// the last one used; the time is now.
func (s *Service) CreateQuickExpense(ctx context.Context, text string) (*model.Expense, error) {
	q, err := ParseQuickExpense(text)
	if err != nil {
		return nil, err
	}
	in := ExpenseInput{Item: q.Item, Amount: json.Number(q.Amount), Currency: q.Currency}
	if in.Currency == "" {
		in.Currency = s.LastCurrency(ctx)
	}
	if q.Category != "" {
		cats, err := s.st.ListExpenseCategories(ctx)
		if err != nil {
			return nil, err
		}
		in.Category = MatchCategory(cats, q.Category)
	}
	if q.Payment != "" {
		in.Payment = q.Payment
		if pays, err := s.st.ExpenseValues(ctx, "payment"); err == nil {
			for _, p := range pays {
				if strings.EqualFold(p, q.Payment) {
					in.Payment = p
					break
				}
			}
		}
	}
	return s.CreateExpense(ctx, in)
}

// modifyExpense applies fn to a stored expense and saves it.
func (s *Service) modifyExpense(ctx context.Context, id int64, fn func(tx store.Store, e *model.Expense) error) (*model.Expense, error) {
	var e *model.Expense
	err := s.st.InTx(ctx, func(tx store.Store) error {
		var err error
		if e, err = tx.GetExpense(ctx, id); err != nil {
			return err
		}
		if err := fn(tx, e); err != nil {
			return err
		}
		e.UpdatedAt = s.now().UTC().Truncate(time.Second)
		return tx.UpdateExpense(ctx, e)
	})
	return e, err
}

// SetExpenseCategory sets an expense's category from the category list.
func (s *Service) SetExpenseCategory(ctx context.Context, id, categoryID int64) (*model.Expense, error) {
	return s.modifyExpense(ctx, id, func(tx store.Store, e *model.Expense) error {
		c, err := tx.GetExpenseCategory(ctx, categoryID)
		if err != nil {
			return err
		}
		e.Category = c.Name
		return nil
	})
}

// SwitchExpenseCurrency re-reads the same number in the other currency
// (USD ↔ KHR): "8000" USD becomes 8,000៛. Amounts with cents cannot become riel.
func (s *Service) SwitchExpenseCurrency(ctx context.Context, id int64) (*model.Expense, error) {
	return s.modifyExpense(ctx, id, func(_ store.Store, e *model.Expense) error {
		to := "KHR"
		if e.Currency == "KHR" {
			to = "USD"
		}
		minor, err := ParseAmount(model.FormatMinor(e.AmountMinor, e.Currency), to)
		if err != nil {
			return model.Invalid("Can't switch: " + strings.TrimPrefix(err.Error(), "validation: "))
		}
		e.AmountMinor, e.Currency, e.Amount = minor, to, model.FormatMinor(minor, to)
		return nil
	})
}

// ExpenseTotals summarises the expenses in [from, to).
func (s *Service) ExpenseTotals(ctx context.Context, from, to time.Time) (ExpenseSummary, error) {
	list, err := s.st.ListExpenses(ctx, model.ExpenseFilter{From: &from, To: &to})
	if err != nil {
		return ExpenseSummary{}, err
	}
	return SummarizeExpenses(list), nil
}
