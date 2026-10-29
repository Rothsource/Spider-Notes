package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/service"
)

func (s *Server) registerExpenses(p func(string, http.HandlerFunc)) {
	p("GET /expenses", s.expensesPage)
	p("POST /expenses", s.expenseSave)
	p("GET /expenses/export.xlsx", s.expensesExport)
	p("GET /expenses/{id}", s.expenseForm)
	p("POST /expenses/{id}", s.expenseSave)
	p("POST /expenses/{id}/delete", s.expenseDelete)
}

// expenseView is the period and filter shown on the expenses page.
type expenseView struct {
	Range     string // day | week | month | year | all
	From, To  time.Time
	Last      time.Time // last day of the period (for display)
	Prev      string
	Next      string
	Category  string
	Query     string
	HasPeriod bool
}

func (s *Server) expenseView(r *http.Request) (expenseView, model.ExpenseFilter) {
	q := r.URL.Query()
	anchor := s.svc.StartOfDay(s.svc.Now())
	if d, err := time.ParseInLocation("2006-01-02", q.Get("date"), s.loc); err == nil {
		anchor = d
	}
	v := expenseView{Range: q.Get("range"), Category: q.Get("category"), Query: q.Get("q"), HasPeriod: true}
	var prev time.Time
	switch v.Range {
	case "day":
		v.From = anchor
		v.To, prev = v.From.AddDate(0, 0, 1), v.From.AddDate(0, 0, -1)
	case "week":
		v.From = anchor.AddDate(0, 0, -((int(anchor.Weekday()) + 6) % 7))
		v.To, prev = v.From.AddDate(0, 0, 7), v.From.AddDate(0, 0, -7)
	case "year":
		v.From = time.Date(anchor.Year(), 1, 1, 0, 0, 0, 0, s.loc)
		v.To, prev = v.From.AddDate(1, 0, 0), v.From.AddDate(-1, 0, 0)
	case "all":
		v.HasPeriod = false
	default:
		v.Range = "month"
		v.From = time.Date(anchor.Year(), anchor.Month(), 1, 0, 0, 0, 0, s.loc)
		v.To, prev = v.From.AddDate(0, 1, 0), v.From.AddDate(0, -1, 0)
	}
	f := model.ExpenseFilter{Category: v.Category, Query: v.Query}
	if v.HasPeriod {
		v.Last = v.To.AddDate(0, 0, -1)
		v.Prev, v.Next = prev.Format("2006-01-02"), v.To.Format("2006-01-02")
		f.From, f.To = &v.From, &v.To
	}
	return v, f
}

// URL returns the expenses URL for this view with overrides (empty value removes a key).
func (v expenseView) URL(path string, kv ...string) string {
	q := url.Values{}
	q.Set("range", v.Range)
	if v.HasPeriod {
		q.Set("date", v.From.Format("2006-01-02"))
	}
	if v.Category != "" {
		q.Set("category", v.Category)
	}
	if v.Query != "" {
		q.Set("q", v.Query)
	}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] == "" {
			q.Del(kv[i])
		} else {
			q.Set(kv[i], kv[i+1])
		}
	}
	return path + "?" + q.Encode()
}

type expenseDay struct {
	Day    time.Time
	Items  []model.Expense
	Totals []service.MoneyTotal
}

func (s *Server) expensesPage(w http.ResponseWriter, r *http.Request) {
	v, f := s.expenseView(r)
	list, err := s.svc.ListExpenses(r.Context(), f)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var days []expenseDay
	start := 0
	for i := range list {
		d := s.svc.StartOfDay(list[i].SpentAt)
		if i == len(list)-1 || !s.svc.StartOfDay(list[i+1].SpentAt).Equal(d) {
			items := list[start : i+1]
			days = append(days, expenseDay{Day: d, Items: items, Totals: service.SummarizeExpenses(items).Totals})
			start = i + 1
		}
	}
	cats, pays, _ := s.svc.ExpenseSuggestions(r.Context())
	s.render(w, r, "expenses", page{"Title": "Expenses", "View": v, "Days": days, "Summary": service.SummarizeExpenses(list),
		"Categories": cats, "Payments": pays, "Currencies": model.Currencies, "Currency": s.svc.LastCurrency(r.Context()),
		"Back": v.URL("/expenses")})
}

func (s *Server) expenseForm(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	e, err := s.svc.GetExpense(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.renderExpenseForm(w, r, e, "")
}

func (s *Server) renderExpenseForm(w http.ResponseWriter, r *http.Request, e *model.Expense, errMsg string) {
	cats, pays, _ := s.svc.ExpenseSuggestions(r.Context())
	data := page{"Title": "Expense", "E": e, "Categories": cats, "Payments": pays, "Currencies": model.Currencies,
		"Back": safeBack(r, "/expenses")}
	if errMsg != "" {
		data["Error"] = errMsg
		w.WriteHeader(http.StatusBadRequest)
	}
	s.render(w, r, "expense_form", data)
}

func (s *Server) expenseSave(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, model.Invalid("Bad form"))
		return
	}
	spent, err := s.parseDT(r.FormValue("spent_at"))
	in := service.ExpenseInput{Item: r.FormValue("item"), Amount: json.Number(r.FormValue("amount")), Currency: r.FormValue("currency"),
		Category: r.FormValue("category"), Payment: r.FormValue("payment"), Note: r.FormValue("note"), SpentAt: spent}
	var e *model.Expense
	if err == nil {
		if r.PathValue("id") != "" {
			var id int64
			if id, err = pathID(r); err == nil {
				e, err = s.svc.UpdateExpense(r.Context(), id, in)
			}
		} else {
			e, err = s.svc.CreateExpense(r.Context(), in)
		}
	}
	if err != nil {
		var ve *model.ValidationError
		if errors.As(err, &ve) {
			// Re-render with the user's input.
			draft := &model.Expense{Item: in.Item, Amount: string(in.Amount), Currency: in.Currency, Category: in.Category,
				Payment: in.Payment, Note: in.Note}
			if id, perr := pathID(r); perr == nil {
				draft.ID = id
			}
			if spent != nil {
				draft.SpentAt = *spent
			}
			s.renderExpenseForm(w, r, draft, ve.Msg)
			return
		}
		s.fail(w, r, err)
		return
	}
	redirect(w, r, safeBack(r, "/expenses"), "Saved "+e.Item+" · "+model.FormatMoney(e.AmountMinor, e.Currency))
}

func (s *Server) expenseDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err == nil {
		err = s.svc.DeleteExpense(r.Context(), id)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	redirect(w, r, safeBack(r, "/expenses"), "Expense deleted")
}

func (s *Server) expensesExport(w http.ResponseWriter, r *http.Request) {
	_, f := s.expenseView(r)
	name, title := s.svc.ExpenseExportName(f)
	var buf bytes.Buffer // build fully first so an error never sends a broken file
	if err := s.svc.ExportExpensesXLSX(r.Context(), &buf, f, title); err != nil {
		slog.Error("expense export", "err", err)
		http.Error(w, "Export failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", service.XLSXContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = buf.WriteTo(w)
}
