package model

import (
	"strconv"
	"strings"
	"time"
)

// Expense is one recorded spending.
type Expense struct {
	ID          int64      `json:"id"`
	Item        string     `json:"item"`
	AmountMinor int64      `json:"amount_minor"` // in the currency's minor unit (cents, riel)
	Amount      string     `json:"amount"`       // decimal form of AmountMinor, e.g. "12.50"
	Currency    string     `json:"currency"`
	Category    string     `json:"category"`
	Payment     string     `json:"payment"`
	Note        string     `json:"note"`
	SpentAt     time.Time  `json:"spent_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
}

// ExpenseFilter selects expenses; zero values mean "any".
type ExpenseFilter struct {
	From, To *time.Time // spent_at range, To exclusive
	Category string
	Currency string
	Query    string // substring of item, category, payment or note
	Limit    int    // 0 = no limit
}

// Currency describes a supported currency.
type Currency struct {
	Code     string
	Symbol   string
	Decimals int
	Suffix   bool // symbol after the number (e.g. 12,000៛)
}

// Currencies are the supported currencies; the first is the default.
var Currencies = []Currency{
	{Code: "USD", Symbol: "$", Decimals: 2},
	{Code: "KHR", Symbol: "៛", Decimals: 0, Suffix: true},
	{Code: "THB", Symbol: "฿", Decimals: 2},
	{Code: "EUR", Symbol: "€", Decimals: 2},
}

// LookupCurrency returns the currency with the given code.
func LookupCurrency(code string) (Currency, bool) {
	for _, c := range Currencies {
		if c.Code == code {
			return c, true
		}
	}
	return Currency{}, false
}

// FormatMinor renders a minor-unit amount as a plain decimal ("1234.50").
func FormatMinor(minor int64, code string) string {
	c, _ := LookupCurrency(code)
	neg := minor < 0
	if neg {
		minor = -minor
	}
	s := strconv.FormatInt(minor, 10)
	if c.Decimals > 0 {
		for len(s) <= c.Decimals {
			s = "0" + s
		}
		s = s[:len(s)-c.Decimals] + "." + s[len(s)-c.Decimals:]
	}
	if neg {
		s = "-" + s
	}
	return s
}

// FormatMoney renders an amount with thousands separators and symbol ("$1,234.50", "12,000៛").
func FormatMoney(minor int64, code string) string {
	c, ok := LookupCurrency(code)
	plain := FormatMinor(minor, code)
	neg := strings.HasPrefix(plain, "-")
	plain = strings.TrimPrefix(plain, "-")
	whole, frac, _ := strings.Cut(plain, ".")
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	num := b.String()
	if frac != "" {
		num += "." + frac
	}
	sign := ""
	if neg {
		sign = "-"
	}
	switch {
	case !ok:
		return sign + num + " " + code
	case c.Suffix:
		return sign + num + c.Symbol
	default:
		return sign + c.Symbol + num
	}
}

// ExpenseCategory is a category offered when recording an expense.
type ExpenseCategory struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Emoji     string    `json:"emoji"`
	Position  int       `json:"position"`
	CreatedAt time.Time `json:"created_at"`
}
