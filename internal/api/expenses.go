package api

import (
	"bytes"
	"log/slog"
	"net/http"
	"time"

	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/service"
)

func (a *API) registerExpenses(h func(string, func(http.ResponseWriter, *http.Request) (any, error)), protect func(http.Handler) http.Handler, mux *http.ServeMux) {
	const p = "/api/v1"
	h("GET "+p+"/expenses", a.listExpenses)
	h("POST "+p+"/expenses", a.createExpense)
	h("GET "+p+"/expenses/summary", a.expenseSummary)
	h("GET "+p+"/expenses/{id}", a.getExpense)
	h("PUT "+p+"/expenses/{id}", a.updateExpense)
	h("DELETE "+p+"/expenses/{id}", a.deleteExpense)
	mux.Handle("GET "+p+"/expenses/export.xlsx", protect(http.HandlerFunc(a.exportExpenses)))
}

// expenseFilter reads from, to, category, currency and q.
func (a *API) expenseFilter(r *http.Request) (model.ExpenseFilter, error) {
	q := r.URL.Query()
	f := model.ExpenseFilter{Category: q.Get("category"), Currency: q.Get("currency"), Query: q.Get("q")}
	if q.Has("from") {
		t, err := a.queryTime(r, "from", time.Time{})
		if err != nil {
			return f, err
		}
		f.From = &t
	}
	if q.Has("to") {
		t, err := a.queryTime(r, "to", time.Time{})
		if err != nil {
			return f, err
		}
		f.To = &t
	}
	return f, nil
}

func (a *API) listExpenses(_ http.ResponseWriter, r *http.Request) (any, error) {
	f, err := a.expenseFilter(r)
	if err != nil {
		return nil, err
	}
	f.Limit = int(queryInt(r, "limit"))
	if f.Limit <= 0 || f.Limit > 5000 {
		f.Limit = 500
	}
	list, err := a.svc.ListExpenses(r.Context(), f)
	if err != nil {
		return nil, err
	}
	return nonNil(list), nil
}

func (a *API) expenseSummary(_ http.ResponseWriter, r *http.Request) (any, error) {
	f, err := a.expenseFilter(r)
	if err != nil {
		return nil, err
	}
	list, err := a.svc.ListExpenses(r.Context(), f)
	if err != nil {
		return nil, err
	}
	return service.SummarizeExpenses(list), nil
}

func (a *API) createExpense(_ http.ResponseWriter, r *http.Request) (any, error) {
	var in service.ExpenseInput
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	return a.svc.CreateExpense(r.Context(), in)
}

func (a *API) getExpense(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	return a.svc.GetExpense(r.Context(), id)
}

func (a *API) updateExpense(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	var in service.ExpenseInput
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	return a.svc.UpdateExpense(r.Context(), id, in)
}

func (a *API) deleteExpense(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	return noContent{}, a.svc.DeleteExpense(r.Context(), id)
}

func (a *API) exportExpenses(w http.ResponseWriter, r *http.Request) {
	f, err := a.expenseFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation", err.Error())
		return
	}
	ServeExpensesXLSX(w, r, a.svc, f)
}

// ServeExpensesXLSX streams an Excel export.
func ServeExpensesXLSX(w http.ResponseWriter, r *http.Request, svc *service.Service, f model.ExpenseFilter) {
	name, title := svc.ExpenseExportName(f)
	var buf bytes.Buffer // build fully first so an error never sends a broken file
	if err := svc.ExportExpensesXLSX(r.Context(), &buf, f, title); err != nil {
		slog.Error("expense export", "err", err)
		http.Error(w, "export failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", service.XLSXContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = buf.WriteTo(w)
}
