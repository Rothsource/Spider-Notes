// Package api exposes the service layer as a versioned JSON API under /api/v1.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/sovannroth/mynotes/internal/auth"
	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/service"
)

// API holds handler dependencies.
type API struct {
	svc  *service.Service
	auth *auth.Manager
	loc  *time.Location
}

// Register mounts all routes on mux under /api/v1.
func Register(mux *http.ServeMux, svc *service.Service, am *auth.Manager) {
	a := &API{svc: svc, auth: am, loc: svc.Location()}
	protect := am.RequireAPI(writeError)
	h := func(pattern string, fn func(w http.ResponseWriter, r *http.Request) (any, error)) {
		mux.Handle(pattern, protect(a.wrap(fn)))
	}
	const p = "/api/v1"
	h("GET "+p+"/tasks", a.listTasks)
	h("POST "+p+"/tasks", a.createTask)
	h("GET "+p+"/tasks/{id}", a.getTask)
	h("PUT "+p+"/tasks/{id}", a.updateTask)
	h("DELETE "+p+"/tasks/{id}", a.deleteTask)
	h("POST "+p+"/tasks/{id}/complete", a.taskAction)
	h("POST "+p+"/tasks/{id}/uncomplete", a.taskAction)
	h("POST "+p+"/tasks/{id}/skip", a.taskAction)

	h("GET "+p+"/reminders", a.listReminders)
	h("POST "+p+"/reminders", a.createReminder)
	h("GET "+p+"/reminders/{id}", a.getReminder)
	h("PUT "+p+"/reminders/{id}", a.updateReminder)
	h("DELETE "+p+"/reminders/{id}", a.deleteReminder)
	h("POST "+p+"/reminders/{id}/pause", a.pauseReminder)
	h("POST "+p+"/reminders/{id}/resume", a.pauseReminder)

	h("GET "+p+"/occurrences", a.listOccurrences)
	h("GET "+p+"/occurrences/{id}", a.getOccurrence)
	h("POST "+p+"/occurrences/{id}/done", a.occAction)
	h("POST "+p+"/occurrences/{id}/snooze", a.occAction)
	h("POST "+p+"/occurrences/{id}/skip", a.occAction)

	h("GET "+p+"/memos", a.listMemos)
	h("POST "+p+"/memos", a.createMemo)
	h("GET "+p+"/memos/{id}", a.getMemo)
	h("PUT "+p+"/memos/{id}", a.updateMemo)
	h("DELETE "+p+"/memos/{id}", a.deleteMemo)
	h("POST "+p+"/memos/{id}/pin", a.pinMemo)
	h("POST "+p+"/memos/{id}/unpin", a.pinMemo)

	h("GET "+p+"/tags", a.tags)
	h("GET "+p+"/calendar", a.calendar)
	h("GET "+p+"/history", a.history)
	h("GET "+p+"/stats", a.stats)
	h("GET "+p+"/search", a.search)
	mux.Handle(p+"/", protect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "unknown endpoint")
	})))
}

// ---- plumbing ----

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	var b errorBody
	b.Error.Code, b.Error.Message = code, msg
	writeJSON(w, status, b)
}

type noContent struct{}

func (a *API) wrap(fn func(w http.ResponseWriter, r *http.Request) (any, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v, err := fn(w, r)
		if err != nil {
			var ve *model.ValidationError
			switch {
			case errors.As(err, &ve):
				writeError(w, http.StatusBadRequest, "validation", ve.Msg)
			case errors.Is(err, model.ErrNotFound):
				writeError(w, http.StatusNotFound, "not_found", "not found")
			case errors.Is(err, model.ErrConflict):
				writeError(w, http.StatusConflict, "conflict", "already handled or in a conflicting state")
			default:
				slog.Error("api error", "path", r.URL.Path, "err", err)
				writeError(w, http.StatusInternalServerError, "internal", "internal error")
			}
			return
		}
		if _, ok := v.(noContent); ok {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		status := http.StatusOK
		if r.Method == http.MethodPost && !strings.Contains(r.URL.Path[len("/api/v1/"):], "/") {
			status = http.StatusCreated
		}
		writeJSON(w, status, localize(v, a.loc))
	})
}

func decode(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return model.Invalid("invalid JSON body: " + err.Error())
	}
	return nil
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, model.ErrNotFound
	}
	return id, nil
}

func queryInt(r *http.Request, k string) int64 {
	n, _ := strconv.ParseInt(r.URL.Query().Get(k), 10, 64)
	return n
}

// queryTime parses an RFC 3339 timestamp or a YYYY-MM-DD date (local midnight).
func (a *API) queryTime(r *http.Request, k string, def time.Time) (time.Time, error) {
	v := r.URL.Query().Get(k)
	if v == "" {
		return def, nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", v, a.loc); err == nil {
		return t, nil
	}
	return def, model.Invalid(k + ": use RFC 3339 or YYYY-MM-DD")
}

// localize converts every time.Time reachable in v to the app time zone so
// JSON timestamps carry the local offset (e.g. +07:00).
func localize(v any, loc *time.Location) any {
	if v == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	cp := reflect.New(rv.Type()).Elem()
	cp.Set(rv)
	walk(cp, loc, 0)
	return cp.Interface()
}

var timeType = reflect.TypeOf(time.Time{})

func walk(v reflect.Value, loc *time.Location, depth int) {
	if depth > 8 {
		return
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return
		}
		if v.Kind() == reflect.Pointer && v.Elem().Type() == timeType {
			t := v.Elem().Interface().(time.Time).In(loc)
			if v.CanSet() {
				v.Set(reflect.ValueOf(&t))
			}
			return
		}
		if v.Kind() == reflect.Pointer {
			// Copy the pointee so callers' values are not mutated.
			cp := reflect.New(v.Elem().Type())
			cp.Elem().Set(v.Elem())
			walk(cp.Elem(), loc, depth+1)
			if v.CanSet() {
				v.Set(cp)
			}
			return
		}
		inner := reflect.New(v.Elem().Type()).Elem()
		inner.Set(v.Elem())
		walk(inner, loc, depth+1)
		if v.CanSet() {
			v.Set(inner)
		}
	case reflect.Struct:
		if v.Type() == timeType {
			if v.CanSet() {
				v.Set(reflect.ValueOf(v.Interface().(time.Time).In(loc)))
			}
			return
		}
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				walk(v.Field(i), loc, depth+1)
			}
		}
	case reflect.Slice:
		if v.IsNil() {
			return
		}
		cp := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		reflect.Copy(cp, v)
		for i := 0; i < cp.Len(); i++ {
			walk(cp.Index(i), loc, depth+1)
		}
		if v.CanSet() {
			v.Set(cp)
		}
	case reflect.Map:
		if v.IsNil() || !v.CanSet() {
			return
		}
		cp := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			val := reflect.New(iter.Value().Type()).Elem()
			val.Set(iter.Value())
			walk(val, loc, depth+1)
			cp.SetMapIndex(iter.Key(), val)
		}
		v.Set(cp)
	}
}
