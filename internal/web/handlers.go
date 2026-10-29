package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sovannroth/mynotes/internal/auth"
	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/service"
)

// ---- login ----

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if s.auth.IsLoggedIn(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, r, "login", page{"Title": "Log in", "LoginCSRF": s.auth.LoginCSRFToken(w, r)})
}

func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if !s.auth.CheckLoginCSRF(r) {
		s.render(w, r, "login", page{"Title": "Log in", "Error": "Your session expired, please try again.", "LoginCSRF": s.auth.LoginCSRFToken(w, r)})
		return
	}
	err := s.auth.Login(r.Context(), w, r, r.PostFormValue("username"), r.PostFormValue("password"))
	if err != nil {
		msg := "Invalid username or password."
		status := http.StatusUnauthorized
		if errors.Is(err, auth.ErrRateLimited) {
			msg, status = err.Error(), http.StatusTooManyRequests
		} else if !errors.Is(err, auth.ErrInvalidLogin) {
			s.fail(w, r, err)
			return
		}
		w.WriteHeader(status)
		s.render(w, r, "login", page{"Title": "Log in", "Error": msg, "Username": r.PostFormValue("username"), "LoginCSRF": s.auth.LoginCSRFToken(w, r)})
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	s.auth.Logout(r.Context(), w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// ---- dashboard ----

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	open, err := s.svc.OpenOccurrences(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var openRem []model.Occurrence
	for _, o := range open {
		if o.Kind == "reminder" {
			openRem = append(openRem, o)
		}
	}
	overdue, err := s.svc.ListTasks(ctx, model.TaskFilter{Status: model.TaskPending, Range: "overdue"})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	today, err := s.svc.ListTasks(ctx, model.TaskFilter{Status: model.TaskPending, Range: "today"})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Today's tasks that are not overdue yet.
	var todayLater []model.Task
	now := time.Now()
	for _, t := range today {
		if t.DueAt != nil && !t.DueAt.Before(now) {
			todayLater = append(todayLater, t)
		}
	}
	rems, err := s.svc.ListReminders(ctx, 0)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var upcoming []model.Reminder
	limit := now.Add(24 * time.Hour)
	for _, rm := range rems {
		if rm.Enabled && rm.NextFireAt != nil && rm.NextFireAt.Before(limit) {
			upcoming = append(upcoming, rm)
		}
	}
	pinned, _ := s.svc.PinnedMemos(ctx)
	s.render(w, r, "dashboard", page{"Title": "Today", "Open": openRem, "Overdue": overdue, "Today": todayLater,
		"Upcoming": upcoming, "Pinned": pinned, "Quiet": s.svc.QuietNow(ctx)})
}

// ---- tasks ----

func (s *Server) tasksPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := model.TaskFilter{Status: q.Get("status"), Range: q.Get("range"), Tag: q.Get("tag"), Priority: q.Get("priority"), Query: q.Get("q")}
	if f.Status == "" {
		f.Status = model.TaskPending
	}
	tasks, err := s.svc.ListTasks(r.Context(), f)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	tags, _ := s.svc.TagCounts(r.Context(), "task")
	s.render(w, r, "tasks", page{"Title": "Tasks", "Tasks": tasks, "F": f, "Tags": tags})
}

func (s *Server) taskForm(w http.ResponseWriter, r *http.Request) {
	data := page{"Title": "New task"}
	t := &model.Task{Priority: model.PriorityNormal}
	if r.PathValue("id") != "" {
		id, err := pathID(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if t, err = s.svc.GetTask(r.Context(), id); err != nil {
			s.fail(w, r, err)
			return
		}
		data["Title"] = "Edit task"
		rems, _ := s.svc.ListReminders(r.Context(), id)
		data["Reminders"] = rems
		hist, _ := s.svc.ListOccurrences(r.Context(), model.OccurrenceFilter{TaskID: id, Kind: "task", Limit: 20})
		data["History"] = hist
	} else if d := r.URL.Query().Get("date"); d != "" {
		if day, err := time.ParseInLocation("2006-01-02", d, s.loc); err == nil {
			due := day.Add(9 * time.Hour)
			t.DueAt = &due
		}
	}
	data["Task"] = t
	data["Repeat"] = service.RepeatFromRule(t.RepeatRule, "", s.loc)
	s.render(w, r, "task_form", data)
}

func (s *Server) parseDT(v string) (*time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, v, s.loc); err == nil {
			return &t, nil
		}
	}
	return nil, model.Invalid("Invalid date/time: " + v)
}

func formInts(r *http.Request, k string) []int {
	var out []int
	for _, v := range r.Form[k] {
		if n, err := strconv.Atoi(v); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func formInt(r *http.Request, k string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(r.FormValue(k)))
	return n
}

func (s *Server) taskSave(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, model.Invalid("Bad form"))
		return
	}
	due, err := s.parseDT(r.FormValue("due"))
	in := service.TaskInput{Title: r.FormValue("title"), Description: r.FormValue("description"), DueAt: due,
		Priority: r.FormValue("priority"), Tags: []string{r.FormValue("tags")}}
	if f := r.FormValue("repeat_freq"); f != "" {
		in.Repeat = &service.RepeatInput{Freq: f, Interval: formInt(r, "repeat_interval"), Weekdays: formInts(r, "repeat_weekdays"),
			MonthDay: formInt(r, "repeat_monthday"), Until: r.FormValue("repeat_until"), Count: formInt(r, "repeat_count")}
	}
	var t *model.Task
	if err == nil {
		if r.PathValue("id") != "" {
			var id int64
			if id, err = pathID(r); err == nil {
				t, err = s.svc.UpdateTask(r.Context(), id, in)
			}
		} else {
			t, err = s.svc.CreateTask(r.Context(), in)
		}
	}
	if err != nil {
		var ve *model.ValidationError
		if errors.As(err, &ve) {
			// Re-render the form with the user's input.
			tt := &model.Task{Title: in.Title, DescriptionMD: in.Description, DueAt: due, Priority: in.Priority}
			if id, e := pathID(r); e == nil {
				tt.ID = id
			}
			tt.Tags = strings.FieldsFunc(r.FormValue("tags"), func(c rune) bool { return c == ',' || c == ' ' })
			w.WriteHeader(http.StatusBadRequest)
			s.render(w, r, "task_form", page{"Title": "Task", "Task": tt, "Repeat": in.Repeat, "Error": ve.Msg})
			return
		}
		s.fail(w, r, err)
		return
	}
	if r.FormValue("add_reminder") != "" {
		redirect(w, r, "/reminders/new?task_id="+strconv.FormatInt(t.ID, 10), "Task saved")
		return
	}
	redirect(w, r, safeBack(r, "/tasks"), "Task saved")
}

func (s *Server) taskAction(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	var t *model.Task
	switch r.PathValue("action") {
	case "complete":
		t, err = s.svc.CompleteTask(ctx, id, model.SourceWeb)
	case "uncomplete":
		t, err = s.svc.UncompleteTask(ctx, id)
	case "skip":
		t, err = s.svc.SkipTask(ctx, id, model.SourceWeb)
	case "delete":
		if err = s.svc.DeleteTask(ctx, id); err == nil {
			if isHX(r) {
				w.WriteHeader(http.StatusOK) // empty body removes the row
				return
			}
			redirect(w, r, "/tasks", "Task deleted")
			return
		}
	default:
		err = model.ErrNotFound
	}
	if err != nil && !errors.Is(err, model.ErrConflict) {
		s.fail(w, r, err)
		return
	}
	if isHX(r) {
		if t == nil {
			t, _ = s.svc.GetTask(ctx, id)
		}
		s.partial(w, r, "taskrow", t)
		return
	}
	redirect(w, r, safeBack(r, "/tasks"), "")
}

// ---- reminders ----

func (s *Server) remindersPage(w http.ResponseWriter, r *http.Request) {
	rems, err := s.svc.ListReminders(r.Context(), 0)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	open, _ := s.svc.OpenOccurrences(r.Context())
	var openRem []model.Occurrence
	for _, o := range open {
		if o.Kind == "reminder" {
			openRem = append(openRem, o)
		}
	}
	s.render(w, r, "reminders", page{"Title": "Reminders", "Reminders": rems, "Open": openRem})
}

type reminderFormData struct {
	R           *model.Reminder
	Repeat      service.RepeatInput
	IsRepeating bool
	FireAt      string // datetime-local
	StartDate   string
	WinStart    string
	WinEnd      string
	QuietMode   string // global | off | custom
	QuietStart  string
	QuietEnd    string
}

func (s *Server) reminderFormData(rm *model.Reminder) reminderFormData {
	fd := reminderFormData{R: rm, QuietMode: "global", Repeat: service.RepeatInput{Freq: "daily", Interval: 1, Time: "08:00", MonthDay: 1}}
	fire := rm.FireAt
	if fire.IsZero() {
		fire = time.Now().In(s.loc).Add(time.Hour).Truncate(time.Hour)
	}
	fd.FireAt = fire.In(s.loc).Format("2006-01-02T15:04")
	fd.StartDate = fire.In(s.loc).Format("2006-01-02")
	if rp := service.RepeatFromRule(rm.RepeatRule, rm.ActiveWindow, s.loc); rp != nil {
		fd.Repeat, fd.IsRepeating = *rp, true
		if a, b, ok := strings.Cut(rp.Window, "-"); ok {
			fd.WinStart, fd.WinEnd = a, b
		}
	}
	switch q := rm.QuietHoursOverride; {
	case q == "off":
		fd.QuietMode = "off"
	case q != "":
		fd.QuietMode = "custom"
		fd.QuietStart, fd.QuietEnd, _ = strings.Cut(q, "-")
	}
	return fd
}

func (s *Server) reminderForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := page{"Title": "New reminder"}
	set, _ := s.svc.Settings(ctx)
	rm := &model.Reminder{Enabled: true, NagIntervalMin: set.DefaultNagInterval}
	if r.PathValue("id") != "" {
		id, err := pathID(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if rm, err = s.svc.GetReminder(ctx, id); err != nil {
			s.fail(w, r, err)
			return
		}
		data["Title"] = "Edit reminder"
		hist, _ := s.svc.ListOccurrences(ctx, model.OccurrenceFilter{ReminderID: id, Limit: 20})
		data["History"] = hist
	} else {
		q := r.URL.Query()
		if tid, err := strconv.ParseInt(q.Get("task_id"), 10, 64); err == nil && tid > 0 {
			rm.TaskID = &tid
			if t, err := s.svc.GetTask(ctx, tid); err == nil && t.DueAt != nil && t.DueAt.After(time.Now()) {
				rm.FireAt = t.DueAt.Add(-30 * time.Minute)
			}
		}
		if d := q.Get("date"); d != "" {
			if day, err := time.ParseInLocation("2006-01-02", d, s.loc); err == nil {
				rm.FireAt = day.Add(9 * time.Hour)
			}
		}
	}
	s.renderReminderForm(w, r, data, rm, "")
}

func (s *Server) renderReminderForm(w http.ResponseWriter, r *http.Request, data page, rm *model.Reminder, errMsg string) {
	tasks, _ := s.svc.ListTasks(r.Context(), model.TaskFilter{Status: model.TaskPending, Limit: 300})
	data["Form"] = s.reminderFormData(rm)
	data["Tasks"] = tasks
	if errMsg != "" {
		data["Error"] = errMsg
	}
	s.render(w, r, "reminder_form", data)
}

func (s *Server) reminderInput(r *http.Request) (service.ReminderInput, error) {
	in := service.ReminderInput{Message: r.FormValue("message"), NagEnabled: r.FormValue("nag") == "on",
		NagIntervalMin: formInt(r, "nag_interval"), IgnoreQuietHours: r.FormValue("ignore_quiet") == "on",
		Tags: []string{r.FormValue("tags")}}
	enabled := r.FormValue("enabled") == "on"
	in.Enabled = &enabled
	if tid, err := strconv.ParseInt(r.FormValue("task_id"), 10, 64); err == nil && tid > 0 {
		in.TaskID = &tid
	}
	switch r.FormValue("quiet_mode") {
	case "off":
		in.QuietHoursOverride = "off"
	case "custom":
		in.QuietHoursOverride = r.FormValue("quiet_start") + "-" + r.FormValue("quiet_end")
	}
	if r.FormValue("type") == model.Repeating {
		start, err := s.parseDT(r.FormValue("start_date"))
		if err != nil {
			return in, err
		}
		if start == nil {
			now := s.svc.StartOfDay(time.Now())
			start = &now
		}
		freq := r.FormValue("freq")
		rp := &service.RepeatInput{Freq: freq, Interval: formInt(r, "interval"), Weekdays: formInts(r, "weekdays"),
			MonthDay: formInt(r, "month_day"), Time: r.FormValue("time"), Until: r.FormValue("until"), Count: formInt(r, "count")}
		if ws, we := r.FormValue("window_start"), r.FormValue("window_end"); ws != "" && we != "" {
			rp.Window = ws + "-" + we
		}
		if freq == "hourly" || freq == "minutely" {
			// Sub-daily series start at the chosen start time of day.
			if m := r.FormValue("sub_start"); m != "" {
				if t, err := time.ParseInLocation("2006-01-02 15:04", start.Format("2006-01-02")+" "+m, s.loc); err == nil {
					start = &t
				}
			}
		}
		in.Repeat, in.FireAt = rp, *start
	} else {
		at, err := s.parseDT(r.FormValue("fire_at"))
		if err != nil {
			return in, err
		}
		if at != nil {
			in.FireAt = *at
		}
	}
	return in, nil
}

func (s *Server) reminderSave(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, model.Invalid("Bad form"))
		return
	}
	in, err := s.reminderInput(r)
	var id int64
	if err == nil {
		if r.PathValue("id") != "" {
			if id, err = pathID(r); err == nil {
				_, err = s.svc.UpdateReminder(r.Context(), id, in)
			}
		} else {
			_, err = s.svc.CreateReminder(r.Context(), in)
		}
	}
	if err != nil {
		var ve *model.ValidationError
		if errors.As(err, &ve) {
			rm := &model.Reminder{ID: id, TaskID: in.TaskID, MessageMD: in.Message, FireAt: in.FireAt, NagEnabled: in.NagEnabled,
				NagIntervalMin: in.NagIntervalMin, IgnoreQuietHours: in.IgnoreQuietHours, Enabled: *in.Enabled, QuietHoursOverride: in.QuietHoursOverride}
			rm.Tags = strings.FieldsFunc(r.FormValue("tags"), func(c rune) bool { return c == ',' || c == ' ' })
			w.WriteHeader(http.StatusBadRequest)
			data := page{"Title": "Reminder"}
			s.renderReminderFormWithRepeat(w, r, data, rm, in.Repeat, ve.Msg)
			return
		}
		s.fail(w, r, err)
		return
	}
	redirect(w, r, safeBack(r, "/reminders"), "Reminder saved")
}

func (s *Server) renderReminderFormWithRepeat(w http.ResponseWriter, r *http.Request, data page, rm *model.Reminder, rp *service.RepeatInput, msg string) {
	tasks, _ := s.svc.ListTasks(r.Context(), model.TaskFilter{Status: model.TaskPending, Limit: 300})
	fd := s.reminderFormData(rm)
	if rp != nil {
		fd.Repeat, fd.IsRepeating = *rp, true
		fd.WinStart, fd.WinEnd, _ = strings.Cut(rp.Window, "-")
	}
	data["Form"], data["Tasks"], data["Error"] = fd, tasks, msg
	s.render(w, r, "reminder_form", data)
}

func (s *Server) reminderAction(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	msg := ""
	switch r.PathValue("action") {
	case "pause":
		_, err = s.svc.SetReminderEnabled(ctx, id, false)
		msg = "Reminder paused"
	case "resume":
		_, err = s.svc.SetReminderEnabled(ctx, id, true)
		msg = "Reminder resumed"
	case "delete":
		err = s.svc.DeleteReminder(ctx, id)
		msg = "Reminder deleted"
	default:
		err = model.ErrNotFound
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	redirect(w, r, safeBack(r, "/reminders"), msg)
}

// ---- occurrences ----

var webActions = map[string]string{
	"done": service.ActionDone, "skip": service.ActionSkip, "snooze10": service.ActionSnooze10,
	"snooze30": service.ActionSnooze30, "snooze60": service.ActionSnooze60, "snooze_morning": service.ActionSnoozeAM,
}

func (s *Server) occAction(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	action, ok := webActions[r.PathValue("action")]
	if !ok {
		s.fail(w, r, model.ErrNotFound)
		return
	}
	_, err = s.svc.OccurrenceAction(r.Context(), id, action, model.SourceWeb)
	if err != nil && !errors.Is(err, model.ErrConflict) {
		s.fail(w, r, err)
		return
	}
	if isHX(r) {
		o, err := s.svc.GetOccurrence(r.Context(), id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.partial(w, r, "occ", o)
		return
	}
	redirect(w, r, safeBack(r, "/"), "")
}
