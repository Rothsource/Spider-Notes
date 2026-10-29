package api_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestExpensesWebAndAPI(t *testing.T) {
	a := newApp(t)
	csrf := a.login()

	// Web: add through the form (time defaults to now), then a validation error re-renders the form.
	if code, body, _ := a.postForm("/expenses", url.Values{"csrf_token": {csrf}, "item": {"Noodle soup"}, "amount": {"2.50"},
		"currency": {"USD"}, "category": {"Food"}, "payment": {"Cash"}, "note": {"Lunch near <office>"}, "back": {"/expenses?range=month"}}); code != http.StatusSeeOther {
		t.Fatalf("add expense: %d %s", code, body)
	}
	if code, body, _ := a.postForm("/expenses", url.Values{"csrf_token": {csrf}, "item": {"Bad"}, "amount": {"abc"}}); code != http.StatusBadRequest || !strings.Contains(body, "must be a number") {
		t.Fatalf("validation should re-render: %d", code)
	}
	for _, p := range []string{"/expenses", "/expenses?range=day", "/expenses?range=week&category=Food", "/expenses?range=year&q=soup", "/expenses?range=all", "/expenses/1"} {
		code, body := a.get(p)
		if code != http.StatusOK {
			t.Fatalf("%s: %d %s", p, code, body)
		}
		if strings.Contains(body, "ZgotmplZ") || strings.Contains(body, "<no value>") {
			t.Fatalf("%s: template output problem", p)
		}
	}
	if _, body := a.get("/expenses"); !strings.Contains(body, "Noodle soup") || !strings.Contains(body, "$2.50") || strings.Contains(body, "<office>") {
		t.Fatal("expense list should show the item, the formatted amount, and escape the note")
	}
	if code, _, _ := a.postForm("/expenses/1", url.Values{"csrf_token": {csrf}, "item": {"Noodle soup"}, "amount": {"3"}, "currency": {"USD"}}); code != http.StatusSeeOther {
		t.Fatalf("edit: %d", code)
	}

	// Excel download from the web page.
	resp, err := a.client.Get(a.srv.URL + "/expenses/export.xlsx?range=all")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "spreadsheetml") ||
		!strings.Contains(resp.Header.Get("Content-Disposition"), "expenses.xlsx") || string(b[:2]) != "PK" {
		t.Fatalf("export: %d %v", resp.StatusCode, resp.Header)
	}

	// API with a token.
	tok, _, err := a.am.CreateToken(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	code, e := a.apiCall("POST", "/expenses", tok, map[string]any{"item": "Tuk-tuk", "amount": 8000, "currency": "KHR", "category": "Transport"})
	if code != http.StatusCreated || e["amount"] != "8000" || e["spent_at"] == nil {
		t.Fatalf("create: %d %v", code, e)
	}
	if code, out := a.apiCall("POST", "/expenses", tok, map[string]any{"item": "x", "amount": "1.234"}); code != http.StatusBadRequest {
		t.Fatalf("too many decimals: %d %v", code, out)
	}
	code, sum := a.apiCall("GET", "/expenses/summary", tok, nil)
	if code != 200 || sum["count"].(float64) != 2 || len(sum["totals"].([]any)) != 2 {
		t.Fatalf("summary: %d %v", code, sum)
	}
	if code, _ := a.apiCall("GET", "/expenses?from=2000-01-01&category=Transport", tok, nil); code != 200 {
		t.Fatalf("list: %d", code)
	}
	req, _ := http.NewRequest("GET", a.srv.URL+"/api/v1/expenses/export.xlsx?from=2000-01-01&to=2100-01-01", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Disposition"), "expenses_2000-01-01_to_2099-12-31.xlsx") {
		t.Fatalf("api export: %d %v", resp.StatusCode, resp.Header)
	}
	if code, _ := a.apiCall("DELETE", "/expenses/1", tok, nil); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := a.apiCall("GET", "/expenses/1", tok, nil); code != http.StatusNotFound {
		t.Fatalf("deleted: %d", code)
	}
}
