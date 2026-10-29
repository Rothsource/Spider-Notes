package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sovannroth/mynotes/internal/api"
	"github.com/sovannroth/mynotes/internal/auth"
	"github.com/sovannroth/mynotes/internal/service"
	"github.com/sovannroth/mynotes/internal/store/sqlite"
	"github.com/sovannroth/mynotes/internal/web"
)

type testApp struct {
	srv    *httptest.Server
	client *http.Client
	svc    *service.Service
	am     *auth.Manager
	t      *testing.T
}

func newApp(t *testing.T) *testApp {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	loc, _ := time.LoadLocation("Asia/Phnom_Penh")
	svc := service.New(service.Options{Store: db, Location: loc, ExportDir: t.TempDir(), AttachDir: t.TempDir()})
	am := auth.NewManager(db, strings.Repeat("s", 40), time.Hour, false)
	if err := am.EnsureAdmin(ctx, "me", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	ui, err := web.New(svc, am, nil, []byte("openapi: 3.1.0"), "test")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	api.ServeFile = web.ServeAttachment
	api.Register(mux, svc, am)
	ui.Register(mux)
	srv := httptest.NewServer(web.SecurityHeaders(mux))
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &testApp{srv: srv, client: c, svc: svc, am: am, t: t}
}

var csrfRe = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)
var metaRe = regexp.MustCompile(`<meta name="csrf-token" content="([^"]+)">`)

func (a *testApp) get(path string) (int, string) {
	a.t.Helper()
	resp, err := a.client.Get(a.srv.URL + path)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (a *testApp) postForm(path string, v url.Values) (int, string, http.Header) {
	a.t.Helper()
	resp, err := a.client.PostForm(a.srv.URL+path, v)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func (a *testApp) login() string {
	a.t.Helper()
	_, body := a.get("/login")
	m := csrfRe.FindStringSubmatch(body)
	if m == nil {
		a.t.Fatal("no login csrf token")
	}
	code, _, h := a.postForm("/login", url.Values{"csrf_token": {m[1]}, "username": {"me"}, "password": {"correct-horse-battery"}})
	if code != http.StatusSeeOther || h.Get("Location") != "/" {
		a.t.Fatalf("login failed: %d %v", code, h)
	}
	_, body = a.get("/")
	mm := metaRe.FindStringSubmatch(body)
	if mm == nil {
		a.t.Fatal("no csrf meta after login")
	}
	return mm[1]
}

func (a *testApp) apiCall(method, path, token string, body any) (int, map[string]any) {
	a.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, a.srv.URL+"/api/v1"+path, r)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestLoginAndPagesRender(t *testing.T) {
	a := newApp(t)
	if code, _ := a.get("/tasks"); code != http.StatusSeeOther {
		t.Fatalf("unauthenticated page should redirect, got %d", code)
	}
	// Wrong password, and login without CSRF token.
	_, body := a.get("/login")
	tok := csrfRe.FindStringSubmatch(body)[1]
	if code, _, _ := a.postForm("/login", url.Values{"csrf_token": {tok}, "username": {"me"}, "password": {"nope"}}); code != http.StatusUnauthorized {
		t.Fatalf("bad password: %d", code)
	}
	if code, _, _ := a.postForm("/login", url.Values{"username": {"me"}, "password": {"correct-horse-battery"}}); code == http.StatusSeeOther {
		t.Fatal("login without csrf token must fail")
	}
	csrf := a.login()

	// Create data through the web forms.
	if code, body, _ := a.postForm("/tasks", url.Values{"csrf_token": {csrf}, "title": {"Gym"}, "start": {"2030-01-07T17:00"}, "due": {"2030-01-07T18:00"},
		"alert": {"15m"}, "priority": {"high"}, "tags": {"health"}, "repeat_freq": {"weekly"}, "repeat_weekdays": {"1", "3"}}); code != http.StatusSeeOther {
		t.Fatalf("create task: %d %s", code, body)
	}
	if code, body, _ := a.postForm("/tasks", url.Values{"csrf_token": {csrf}, "title": {"x"}, "alert": {"start"}, "due": {"2030-01-07T18:00"}}); code != http.StatusBadRequest || !strings.Contains(body, "needs a start time") {
		t.Fatalf("alert validation should re-render the form: %d", code)
	}
	if code, body, _ := a.postForm("/reminders", url.Values{"csrf_token": {csrf}, "message": {"You should not eat this this morning"},
		"type": {"repeating"}, "freq": {"daily"}, "time": {"07:30"}, "start_date": {"2030-01-01"}, "nag": {"on"}, "nag_interval": {"10"},
		"quiet_mode": {"custom"}, "quiet_start": {"23:00"}, "quiet_end": {"06:00"}, "enabled": {"on"}}); code != http.StatusSeeOther {
		t.Fatalf("create reminder: %d %s", code, body)
	}
	if code, body, _ := a.postForm("/reminders", url.Values{"csrf_token": {csrf}, "message": {"x"}, "type": {"one_time"}, "fire_at": {"2001-01-01T08:00"}}); code != http.StatusBadRequest || !strings.Contains(body, "in the past") {
		t.Fatalf("validation error should re-render the form: %d", code)
	}
	if code, _, _ := a.postForm("/memos", url.Values{"csrf_token": {csrf}, "content": {"Slept well #health\n- [ ] water"}}); code != http.StatusSeeOther {
		t.Fatalf("create memo: %d", code)
	}
	// Missing CSRF token is rejected.
	if code, _, _ := a.postForm("/memos", url.Values{"content": {"x"}}); code != http.StatusForbidden {
		t.Fatalf("csrf not enforced: %d", code)
	}

	pages := []string{"/", "/tasks", "/tasks?status=all&range=week&tag=health", "/tasks/new", "/tasks/1", "/reminders", "/reminders/new?task_id=1",
		"/reminders/1", "/calendar", "/calendar?view=week&date=2030-01-07", "/calendar?view=month&date=2030-01-07", "/calendar/day?date=2030-01-07",
		"/memos", "/memos?tag=health", "/memos?q=slept", "/history", "/history?range=month", "/history?range=day", "/search?q=gym", "/settings"}
	for _, p := range pages {
		code, body := a.get(p)
		if code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", p, code, body)
		}
		if strings.Contains(body, "ZgotmplZ") || strings.Contains(body, "<no value>") {
			t.Fatalf("%s: template output problem", p)
		}
	}
	_, cal := a.get("/calendar?view=month&date=2030-01-07")
	if strings.Count(cal, "Gym") < 4 {
		t.Fatal("repeating task should appear on each occurrence day in the month view")
	}
	if _, form := a.get("/tasks/1"); !strings.Contains(form, `value="2030-01-07T17:00"`) || !strings.Contains(form, `value="15m" selected`) {
		t.Fatal("task form should show the saved start and alert")
	}
	_, memos := a.get("/memos")
	if !strings.Contains(memos, `data-cb="0"`) || !strings.Contains(memos, "#health") {
		t.Fatal("memo checkbox or tag not rendered")
	}
	if code, _ := a.get("/settings/export.zip"); code != http.StatusOK {
		t.Fatalf("zip export: %d", code)
	}
}

func TestAPIWithToken(t *testing.T) {
	a := newApp(t)
	if code, out := a.apiCall("GET", "/tasks", "", nil); code != http.StatusUnauthorized || out["error"] == nil {
		t.Fatalf("no auth: %d %v", code, out)
	}
	if code, _ := a.apiCall("GET", "/tasks", "mnt_bogus", nil); code != http.StatusUnauthorized {
		t.Fatalf("bad token: %d", code)
	}
	tok, rec, err := a.am.CreateToken(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}

	due := time.Now().Add(48 * time.Hour).Format(time.RFC3339)
	code, task := a.apiCall("POST", "/tasks", tok, map[string]any{"title": "Pay rent", "due_at": due, "tags": []string{"home"},
		"repeat": map[string]any{"freq": "monthly", "month_day": 31}})
	if code != http.StatusCreated {
		t.Fatalf("create task: %d %v", code, task)
	}
	if !strings.HasSuffix(task["due_at"].(string), "+07:00") {
		t.Fatalf("timestamps should carry the local offset: %v", task["due_at"])
	}
	id := int(task["id"].(float64))

	code, rem := a.apiCall("POST", "/reminders", tok, map[string]any{"message_md": "Bring the receipt", "task_id": id,
		"fire_at": time.Now().Add(time.Hour).Format(time.RFC3339), "nag_enabled": true, "nag_interval_min": 15})
	if code != http.StatusCreated || rem["next_fire_at"] == nil {
		t.Fatalf("create reminder: %d %v", code, rem)
	}
	rid := int(rem["id"].(float64))
	if code, r := a.apiCall("POST", "/reminders/"+itoa(rid)+"/pause", tok, nil); code != 200 || r["enabled"] != false {
		t.Fatalf("pause: %d %v", code, r)
	}
	if code, out := a.apiCall("POST", "/reminders", tok, map[string]any{"message_md": "x", "fire_at": due, "repeat": map[string]any{"freq": "yearly"}}); code != http.StatusBadRequest || out["error"].(map[string]any)["code"] != "validation" {
		t.Fatalf("validation: %d %v", code, out)
	}
	if code, _ := a.apiCall("POST", "/reminders", tok, map[string]any{"unknown_field": 1}); code != http.StatusBadRequest {
		t.Fatalf("unknown fields should be rejected: %d", code)
	}

	code, done := a.apiCall("POST", "/tasks/"+itoa(id)+"/complete", tok, nil)
	if code != 200 || done["status"] != "pending" {
		t.Fatalf("repeating complete should advance: %d %v", code, done)
	}

	code, memo := a.apiCall("POST", "/memos", tok, map[string]any{"content_md": "Idea for #work project"})
	if code != http.StatusCreated || memo["tags"].([]any)[0] != "work" {
		t.Fatalf("memo: %d %v", code, memo)
	}
	mid := int(memo["id"].(float64))
	if code, m := a.apiCall("POST", "/memos/"+itoa(mid)+"/pin", tok, nil); code != 200 || m["pinned"] != true {
		t.Fatalf("pin: %d %v", code, m)
	}
	if code, s := a.apiCall("GET", "/search?q=idea", tok, nil); code != 200 || len(s["results"].([]any)) != 1 {
		t.Fatalf("search: %d %v", code, s)
	}
	if code, c := a.apiCall("GET", "/calendar?from="+time.Now().Format("2006-01-02")+"&to="+time.Now().AddDate(0, 2, 0).Format("2006-01-02"), tok, nil); code != 200 || len(c["items"].([]any)) == 0 {
		t.Fatalf("calendar: %d %v", code, c)
	}
	if code, h := a.apiCall("GET", "/history?from=2000-01-01", tok, nil); code != 200 || len(h["history"].([]any)) != 1 {
		t.Fatalf("history: %d %v", code, h)
	}
	for _, p := range []string{"/tags", "/stats", "/occurrences", "/reminders", "/memos?limit=1", "/tasks?status=all"} {
		if code, out := a.apiCall("GET", p, tok, nil); code != 200 {
			t.Fatalf("%s: %d %v", p, code, out)
		}
	}
	if code, _ := a.apiCall("GET", "/tasks/99999", tok, nil); code != http.StatusNotFound {
		t.Fatalf("not found: %d", code)
	}
	if code, _ := a.apiCall("DELETE", "/memos/"+itoa(mid), tok, nil); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
	// Revoked tokens stop working.
	if err := a.am.RevokeToken(context.Background(), rec.ID); err != nil {
		t.Fatal(err)
	}
	if code, _ := a.apiCall("GET", "/tasks", tok, nil); code != http.StatusUnauthorized {
		t.Fatalf("revoked token: %d", code)
	}
}

func TestAPISessionNeedsCSRFHeader(t *testing.T) {
	a := newApp(t)
	csrf := a.login()
	do := func(withCSRF bool) int {
		req, _ := http.NewRequest("POST", a.srv.URL+"/api/v1/memos", strings.NewReader(`{"content_md":"hi"}`))
		if withCSRF {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		resp, err := a.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := do(false); c != http.StatusForbidden {
		t.Fatalf("without CSRF header: %d", c)
	}
	if c := do(true); c != http.StatusCreated {
		t.Fatalf("with CSRF header: %d", c)
	}
}

func TestLoginRateLimit(t *testing.T) {
	a := newApp(t)
	_, body := a.get("/login")
	tok := csrfRe.FindStringSubmatch(body)[1]
	var last int
	for i := 0; i < 6; i++ {
		last, _, _ = a.postForm("/login", url.Values{"csrf_token": {tok}, "username": {"me"}, "password": {"wrong"}})
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("6th attempt should be rate limited, got %d", last)
	}
}

func itoa(i int) string {
	return strings.TrimSpace(strings.Replace(time.Duration(i).String(), "ns", "", 1))
}
