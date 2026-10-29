package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/sovannroth/mynotes/internal/model"
)

func TestParseAmount(t *testing.T) {
	ok := []struct {
		in, cur string
		want    int64
	}{
		{"12.5", "USD", 1250}, {"12.50", "USD", 1250}, {"$1,234.05", "USD", 123405}, {".99", "USD", 99}, {"3", "USD", 300},
		{"12,000", "KHR", 12000}, {"12000៛", "KHR", 12000}, {" 7 ", "THB", 700},
	}
	for _, c := range ok {
		got, err := ParseAmount(c.in, c.cur)
		if err != nil || got != c.want {
			t.Errorf("ParseAmount(%q, %s) = %d, %v; want %d", c.in, c.cur, got, err, c.want)
		}
	}
	bad := []struct{ in, cur string }{
		{"", "USD"}, {"0", "USD"}, {"-5", "USD"}, {"1.234", "USD"}, {"12.5", "KHR"}, {"abc", "USD"}, {"1.2.3", "USD"},
		{"10", "XYZ"}, {"99999999999999999999", "USD"}, {"1e5", "USD"},
	}
	for _, c := range bad {
		if _, err := ParseAmount(c.in, c.cur); !errors.Is(err, model.ErrValidation) {
			t.Errorf("ParseAmount(%q, %s) should fail, got %v", c.in, c.cur, err)
		}
	}
}

func TestFormatMoney(t *testing.T) {
	cases := map[string]string{
		model.FormatMoney(123405, "USD"):    "$1,234.05",
		model.FormatMoney(5, "USD"):         "$0.05",
		model.FormatMoney(1250000, "KHR"):   "1,250,000៛",
		model.FormatMoney(-1999, "USD"):     "-$19.99",
		model.FormatMinor(100, "USD"):       "1.00",
		model.FormatMinor(4000, "KHR"):      "4000",
		model.FormatMoney(123456789, "EUR"): "€1,234,567.89",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestExpenseLifecycle(t *testing.T) {
	e := newEnv(t, "2026-03-05 12:30")
	ctx := context.Background()
	lunch, err := e.svc.CreateExpense(ctx, ExpenseInput{Item: "Lunch", Amount: "4.5", Category: " Food ", Payment: "Cash"})
	if err != nil {
		t.Fatal(err)
	}
	if lunch.Currency != "USD" || lunch.AmountMinor != 450 || lunch.Amount != "4.50" || lunch.Category != "Food" {
		t.Fatalf("lunch %+v", lunch)
	}
	if !lunch.SpentAt.Equal(at("2026-03-05 12:30")) {
		t.Fatalf("time should be recorded automatically: %v", lunch.SpentAt.In(pp))
	}
	when := at("2026-03-04 08:00")
	if _, err := e.svc.CreateExpense(ctx, ExpenseInput{Item: "Tuk-tuk", Amount: "8,000", Currency: "khr", Category: "Transport", SpentAt: &when}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateExpense(ctx, ExpenseInput{Item: "Coffee", Amount: "2.25", Category: "Food", Note: "with Dara"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateExpense(ctx, ExpenseInput{Item: "", Amount: "1"}); !errors.Is(err, model.ErrValidation) {
		t.Fatal("item is required")
	}

	// Editing without a time keeps the recorded time.
	e.clk.set("2026-03-06 09:00")
	lunch, err = e.svc.UpdateExpense(ctx, lunch.ID, ExpenseInput{Item: "Lunch", Amount: "5", Category: "Food", Payment: "Cash"})
	if err != nil || lunch.AmountMinor != 500 || !lunch.SpentAt.Equal(at("2026-03-05 12:30")) {
		t.Fatalf("update %+v %v", lunch, err)
	}

	from, to := at("2026-03-01 00:00"), at("2026-04-01 00:00")
	list, _ := e.svc.ListExpenses(ctx, model.ExpenseFilter{From: &from, To: &to})
	if len(list) != 3 || list[0].Item != "Coffee" {
		t.Fatalf("list newest first: %+v", list)
	}
	if got, _ := e.svc.ListExpenses(ctx, model.ExpenseFilter{Query: "dara"}); len(got) != 1 {
		t.Fatalf("search in description: %d", len(got))
	}
	if got, _ := e.svc.ListExpenses(ctx, model.ExpenseFilter{Query: "100%"}); len(got) != 0 {
		t.Fatal("LIKE wildcards must be escaped")
	}
	sum := SummarizeExpenses(list)
	if len(sum.Totals) != 2 || sum.Totals[0].Display != "$7.25" || sum.Totals[1].Display != "8,000៛" {
		t.Fatalf("totals %+v", sum.Totals)
	}
	if sum.ByCategory[0].Category != "Food" || sum.ByCategory[0].Share != 1 {
		t.Fatalf("by category %+v", sum.ByCategory)
	}
	if pays, _ := e.svc.ExpensePayments(ctx); len(pays) != 1 || pays[0] != "Cash" {
		t.Fatalf("payments %v", pays)
	}
	if e.svc.LastCurrency(ctx) != "USD" {
		t.Fatal("last currency")
	}

	// Excel export: valid zip of well-formed XML parts, with the data inside.
	var buf bytes.Buffer
	if err := e.svc.ExportExpensesXLSX(ctx, &buf, model.ExpenseFilter{From: &from, To: &to}, "Expenses March"); err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	parts := map[string]string{}
	for _, f := range z.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		parts[f.Name] = string(b)
		dec := xml.NewDecoder(bytes.NewReader(b))
		for {
			if _, err := dec.Token(); err == io.EOF {
				break
			} else if err != nil {
				t.Fatalf("%s: invalid XML: %v", f.Name, err)
			}
		}
	}
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml", "xl/styles.xml", "xl/worksheets/sheet1.xml", "xl/worksheets/sheet2.xml"} {
		if _, ok := parts[name]; !ok {
			t.Fatalf("missing part %s", name)
		}
	}
	sheet := parts["xl/worksheets/sheet1.xml"]
	for _, want := range []string{"Tuk-tuk", "with Dara", "<v>8000</v>", "<v>2.25</v>", `<autoFilter ref="A1:G4"/>`} {
		if !strings.Contains(sheet, want) {
			t.Errorf("sheet1 missing %q", want)
		}
	}
	if strings.Index(sheet, "Tuk-tuk") > strings.Index(sheet, "Coffee") {
		t.Error("export should be oldest first")
	}
	if !strings.Contains(parts["xl/worksheets/sheet2.xml"], "Expenses March") {
		t.Error("summary title missing")
	}

	if err := e.svc.DeleteExpense(ctx, lunch.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetExpense(ctx, lunch.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatal("deleted expense should be gone")
	}
}

func TestExpenseInputJSON(t *testing.T) {
	var in ExpenseInput
	for _, body := range []string{`{"item":"a","amount":12.5}`, `{"item":"a","amount":"12.5"}`} {
		if err := json.Unmarshal([]byte(body), &in); err != nil || in.Amount.String() != "12.5" {
			t.Fatalf("%s: %v %q", body, err, in.Amount)
		}
	}
}

func TestExpenseCategories(t *testing.T) {
	e := newEnv(t, "2026-03-05 12:30")
	ctx := context.Background()
	cats, err := e.svc.ListExpenseCategories(ctx)
	if err != nil || len(cats) < 10 || cats[0].Name != "Food & Drinks" || cats[0].Emoji == "" || cats[len(cats)-1].Name != "Other" {
		t.Fatalf("preset categories: %v %+v", err, cats)
	}
	n := len(cats)

	// Using an existing category with different case keeps the stored spelling.
	x, err := e.svc.CreateExpense(ctx, ExpenseInput{Item: "Bus", Amount: "1", Category: "transport"})
	if err != nil || x.Category != "Transport" {
		t.Fatalf("canonical name: %v %q", err, x.Category)
	}
	// A new name typed on an expense becomes a category (before "Other").
	if _, err := e.svc.CreateExpense(ctx, ExpenseInput{Item: "Latte", Amount: "2", Category: "Coffee"}); err != nil {
		t.Fatal(err)
	}
	cats, _ = e.svc.ListExpenseCategories(ctx)
	if len(cats) != n+1 || cats[len(cats)-2].Name != "Coffee" {
		t.Fatalf("auto-created category: %+v", cats)
	}

	c, err := e.svc.CreateExpenseCategory(ctx, CategoryInput{Name: "Pets", Emoji: "🐶"})
	if err != nil || c.Emoji != "🐶" {
		t.Fatalf("create: %v %+v", err, c)
	}
	for _, in := range []CategoryInput{{Name: "pets"}, {Name: ""}, {Name: "x", Emoji: "<b>"}, {Name: strings.Repeat("a", 61)}} {
		if _, err := e.svc.CreateExpenseCategory(ctx, in); !errors.Is(err, model.ErrValidation) {
			t.Errorf("%+v should be rejected, got %v", in, err)
		}
	}
	// Renaming carries existing expenses along.
	coffee := cats[len(cats)-2]
	if _, err := e.svc.UpdateExpenseCategory(ctx, coffee.ID, CategoryInput{Name: "Café", Emoji: "☕"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.svc.ListExpenses(ctx, model.ExpenseFilter{Category: "Café"}); len(got) != 1 {
		t.Fatalf("rename should update expenses: %d", len(got))
	}
	if _, err := e.svc.UpdateExpenseCategory(ctx, coffee.ID, CategoryInput{Name: "Pets"}); !errors.Is(err, model.ErrValidation) {
		t.Fatal("rename onto an existing name must fail")
	}
	// Deleting removes it from the picker but keeps the expense text.
	if err := e.svc.DeleteExpenseCategory(ctx, coffee.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.svc.ListExpenses(ctx, model.ExpenseFilter{Category: "Café"}); len(got) != 1 {
		t.Fatal("expense should keep its category after the category is removed")
	}
}
