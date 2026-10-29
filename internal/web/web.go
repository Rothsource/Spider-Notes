// Package web serves the server-rendered HTML UI (html/template + htmx).
package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sovannroth/mynotes/internal/auth"
	"github.com/sovannroth/mynotes/internal/markdown"
	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/service"
)

//go:embed templates static
var assets embed.FS

// TelegramTester sends a test message (implemented by the Telegram bot).
type TelegramTester interface {
	SendTest(ctx context.Context) error
}

// Server is the web UI.
type Server struct {
	svc      *service.Service
	auth     *auth.Manager
	tg       TelegramTester
	loc      *time.Location
	pages    map[string]*template.Template
	partials *template.Template
	openapi  []byte
	version  string
	assetV   string // content hash of static files, appended to asset URLs for cache busting
}

// assetVersion hashes all embedded static files so asset URLs change whenever their content does.
func assetVersion() string {
	h := sha256.New()
	_ = fs.WalkDir(assets, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(assets, p)
		if err != nil {
			return err
		}
		h.Write([]byte(p))
		h.Write(b)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:10]
}

// New parses templates and returns the server.
func New(svc *service.Service, am *auth.Manager, tg TelegramTester, openapi []byte, version string) (*Server, error) {
	s := &Server{svc: svc, auth: am, tg: tg, loc: svc.Location(), pages: map[string]*template.Template{}, openapi: openapi, version: version,
		assetV: assetVersion()}
	funcs := s.funcs()
	base := []string{"templates/layout.html", "templates/partials.html"}
	var err error
	if s.partials, err = template.New("").Funcs(funcs).ParseFS(assets, base...); err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(assets, "templates/pages")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".html")
		t, err := template.New("").Funcs(funcs).ParseFS(assets, append(base, "templates/pages/"+e.Name())...)
		if err != nil {
			return nil, fmt.Errorf("template %s: %w", name, err)
		}
		s.pages[name] = t
	}
	return s, nil
}

// Register mounts web routes.
func (s *Server) Register(mux *http.ServeMux) {
	static, _ := fs.Sub(assets, "static")
	fileServer := http.StripPrefix("/static/", http.FileServer(http.FS(static)))
	mux.Handle("GET /static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Versioned URLs (?v=<content hash>) never change; anything else must be revalidated.
		if r.URL.Query().Get("v") == s.assetV {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(w, r)
	}))
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.loginSubmit)
	mux.HandleFunc("GET /openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(s.openapi)
	})

	p := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.auth.RequireWeb(h)) }
	p("POST /logout", s.logout)
	p("GET /{$}", s.dashboard)

	p("GET /tasks", s.tasksPage)
	p("GET /tasks/new", s.taskForm)
	p("POST /tasks", s.taskSave)
	p("GET /tasks/{id}", s.taskForm)
	p("POST /tasks/{id}", s.taskSave)
	p("POST /tasks/{id}/{action}", s.taskAction)

	p("GET /reminders", s.remindersPage)
	p("GET /reminders/new", s.reminderForm)
	p("POST /reminders", s.reminderSave)
	p("GET /reminders/{id}", s.reminderForm)
	p("POST /reminders/{id}", s.reminderSave)
	p("POST /reminders/{id}/{action}", s.reminderAction)

	p("POST /occurrences/{id}/{action}", s.occAction)

	p("GET /calendar", s.calendarPage)
	p("GET /calendar/day", s.calendarDay)

	p("GET /memos", s.memosPage)
	p("GET /memos/more", s.memosMore)
	p("POST /memos", s.memoCreate)
	p("GET /memos/{id}", s.memoCard)
	p("GET /memos/{id}/edit", s.memoEdit)
	p("POST /memos/{id}", s.memoUpdate)
	p("POST /memos/{id}/{action}", s.memoAction)

	p("GET /history", s.historyPage)
	p("GET /search", s.searchPage)

	p("GET /settings", s.settingsPage)
	p("POST /settings/{section}", s.settingsSave)
	p("GET /settings/export.zip", s.exportZip)
	p("POST /settings/tokens/{id}/revoke", s.revokeToken)
	s.registerNotes(p)
}

// ---- rendering ----

type page map[string]any

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, data page) {
	t, ok := s.pages[name]
	if !ok {
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}
	if data == nil {
		data = page{}
	}
	data["CSRF"] = auth.CSRFToken(r.Context())
	data["Path"] = r.URL.Path
	data["Now"] = s.svc.Now()
	data["Version"] = s.version
	data["TZ"] = s.loc.String()
	data["V"] = s.assetV
	if _, ok := data["Msg"]; !ok {
		data["Msg"] = r.URL.Query().Get("msg")
	}
	if _, ok := data["Error"]; !ok {
		data["Error"] = r.URL.Query().Get("err")
	}
	if data["CSRF"] != "" {
		data["User"] = s.auth.Username(r.Context())
		if open, err := s.svc.OpenOccurrences(r.Context()); err == nil {
			n := 0
			for _, o := range open {
				if o.Kind == "reminder" {
					n++
				}
			}
			data["OpenCount"] = n
		}
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", data); err != nil {
		slog.Error("render", "page", name, "err", err)
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

func (s *Server) partial(w http.ResponseWriter, r *http.Request, name string, data any) {
	var buf bytes.Buffer
	if err := s.partials.ExecuteTemplate(&buf, name, partialData{Data: data, CSRF: auth.CSRFToken(r.Context()), Now: s.svc.Now()}); err != nil {
		slog.Error("render partial", "name", name, "err", err)
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

// partialData is the root value for partial templates (uniform with page rendering via .Data).
type partialData struct {
	Data any
	CSRF string
	Now  time.Time
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ve *model.ValidationError
	switch {
	case errors.As(err, &ve):
		http.Error(w, ve.Msg, http.StatusBadRequest)
	case errors.Is(err, model.ErrNotFound):
		http.Error(w, "Not found", http.StatusNotFound)
	case errors.Is(err, model.ErrConflict):
		http.Error(w, "Already handled", http.StatusConflict)
	default:
		slog.Error("web error", "path", r.URL.Path, "err", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
	}
}

func isHX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

func redirect(w http.ResponseWriter, r *http.Request, to, msg string) {
	if msg != "" {
		sep := "?"
		if strings.Contains(to, "?") {
			sep = "&"
		}
		to += sep + "msg=" + url.QueryEscape(msg)
	}
	if isHX(r) {
		w.Header().Set("HX-Redirect", to)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther) // #nosec G710 -- targets are internal paths or validated by safeBack
}

// safeBack returns a local redirect target from the "back" form value.
func safeBack(r *http.Request, def string) string {
	b := r.FormValue("back")
	if b == "" || !strings.HasPrefix(b, "/") || strings.HasPrefix(b, "//") || strings.Contains(b, "\\") {
		return def
	}
	return b
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, model.ErrNotFound
	}
	return id, nil
}

// ---- template funcs ----

func (s *Server) funcs() template.FuncMap {
	loc := s.loc
	return template.FuncMap{
		"md":  func(src string) template.HTML { return markdown.Render(src, false) },
		"mdi": func(src string) template.HTML { return markdown.Render(src, true) },
		"dt": func(t any) string {
			if tt, ok := derefTime(t); ok {
				return tt.In(loc).Format("Mon 02 Jan 2006, 15:04")
			}
			return ""
		},
		"short": func(t any) string {
			tt, ok := derefTime(t)
			if !ok {
				return ""
			}
			tt = tt.In(loc)
			now := time.Now().In(loc)
			switch {
			case sameDay(tt, now):
				return "Today " + tt.Format("15:04")
			case sameDay(tt, now.AddDate(0, 0, 1)):
				return "Tomorrow " + tt.Format("15:04")
			case sameDay(tt, now.AddDate(0, 0, -1)):
				return "Yesterday " + tt.Format("15:04")
			case tt.Year() == now.Year():
				return tt.Format("Mon 02 Jan 15:04")
			}
			return tt.Format("02 Jan 2006 15:04")
		},
		"hm": func(t any) string {
			if tt, ok := derefTime(t); ok {
				return tt.In(loc).Format("15:04")
			}
			return ""
		},
		"day": func(t time.Time) string { return t.In(loc).Format("Monday, 02 January 2006") },
		"iso": func(t any) string {
			if tt, ok := derefTime(t); ok {
				return tt.In(loc).Format("2006-01-02")
			}
			return ""
		},
		"dtlocal": func(t any) string {
			if tt, ok := derefTime(t); ok {
				return tt.In(loc).Format("2006-01-02T15:04")
			}
			return ""
		},
		"past": func(t any) bool {
			tt, ok := derefTime(t)
			return ok && tt.Before(time.Now())
		},
		"rule": service.DescribeRule,
		"join": strings.Join,
		"has": func(list []int, v int) bool {
			for _, x := range list {
				if x == v {
					return true
				}
			}
			return false
		},
		"add": func(a, b int) int { return a + b },
		"pct": func(f float64) string { return strconv.Itoa(int(f*100+0.5)) + "%" },
		"snippet": func(s string) template.HTML {
			e := template.HTMLEscapeString(s)
			e = strings.NewReplacer("\x02", "<mark>", "\x03", "</mark>").Replace(e)
			return template.HTML(e) // #nosec -- escaped above
		},
		"firstline": model.FirstLine,
		"weekdays":  func() []string { return []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"} },
		"rem7":      func(i int) int { return i % 7 },
		"icon": func(name string) template.HTML {
			// width/height keep icons small even if the stylesheet fails to load.
			// #nosec G203 -- fixed markup; the icon name is escaped
			return template.HTML(`<svg class="i" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.9" aria-hidden="true"><use href="/static/icons.svg?v=` +
				s.assetV + `#` + template.HTMLEscapeString(name) + `"></use></svg>`) // #nosec -- name is escaped
		},
		// ago formats a time relative to now, Memos-style.
		"ago": func(t any) string {
			tt, ok := derefTime(t)
			if !ok {
				return ""
			}
			tt = tt.In(loc)
			now := time.Now().In(loc)
			d := now.Sub(tt)
			switch {
			case d < 0:
				return tt.Format("Mon 02 Jan 15:04")
			case d < time.Minute:
				return "just now"
			case d < time.Hour:
				if m := int(d.Minutes()); m > 1 {
					return strconv.Itoa(m) + " minutes ago"
				}
				return "1 minute ago"
			case sameDay(tt, now):
				if h := int(d.Hours()); h > 1 {
					return strconv.Itoa(h) + " hours ago"
				}
				return "1 hour ago"
			case sameDay(tt, now.AddDate(0, 0, -1)):
				return "Yesterday " + tt.Format("15:04")
			case tt.Year() == now.Year():
				return tt.Format("02 Jan 15:04")
			}
			return tt.Format("02 Jan 2006 15:04")
		},
		"prefix": strings.HasPrefix,
		"size": func(n int64) string {
			switch {
			case n >= 1<<30:
				return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
			case n >= 1<<20:
				return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
			case n >= 1<<10:
				return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
			}
			return fmt.Sprintf("%d B", n)
		},
		"fileicon": func(mt string) string {
			switch {
			case strings.HasPrefix(mt, "image/"):
				return "image"
			case mt == "application/pdf", strings.HasPrefix(mt, "text/"):
				return "file-text"
			case strings.HasPrefix(mt, "video/"), strings.HasPrefix(mt, "audio/"):
				return "play"
			}
			return "file"
		},
		"atturl": func(a model.Attachment) string { return service.AttachmentURL(&a) },
		"initial": func(s string) string {
			for _, r := range s {
				return strings.ToUpper(string(r))
			}
			return "?"
		},
		// slice2 returns part i of an "HH:MM-HH:MM" range.
		"slice2": func(s string, i int) string {
			parts := strings.SplitN(s, "-", 2)
			if len(parts) != 2 {
				return ""
			}
			return parts[i]
		},
		"seq": func(a, b int) []int {
			var out []int
			for i := a; i <= b; i++ {
				out = append(out, i)
			}
			return out
		},
		"deref": func(p *int64) int64 {
			if p == nil {
				return 0
			}
			return *p
		},
		"dict": func(kv ...any) map[string]any {
			m := map[string]any{}
			for i := 0; i+1 < len(kv); i += 2 {
				m[fmt.Sprint(kv[i])] = kv[i+1]
			}
			return m
		},
	}
}

func derefTime(t any) (time.Time, bool) {
	switch v := t.(type) {
	case time.Time:
		return v, !v.IsZero()
	case *time.Time:
		if v == nil {
			return time.Time{}, false
		}
		return *v, !v.IsZero()
	}
	return time.Time{}, false
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// SecurityHeaders adds standard security headers.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}
