package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/service"
)

func source(r *http.Request) string {
	if strings.HasPrefix(r.Header.Get("User-Agent"), "Mozilla") && r.Header.Get("Authorization") == "" {
		return model.SourceWeb
	}
	return model.SourceAPI
}

// ---- tasks ----

type taskOut struct {
	*model.Task
	Repeat      *service.RepeatInput `json:"repeat"`
	RepeatLabel string               `json:"repeat_description,omitempty"`
}

func (a *API) taskOut(t *model.Task) taskOut {
	return taskOut{Task: t, Repeat: service.RepeatFromRule(t.RepeatRule, "", a.loc), RepeatLabel: service.DescribeRule(t.RepeatRule, "")}
}

func (a *API) listTasks(_ http.ResponseWriter, r *http.Request) (any, error) {
	q := r.URL.Query()
	f := model.TaskFilter{Status: q.Get("status"), Range: q.Get("range"), Tag: q.Get("tag"), Priority: q.Get("priority"), Query: q.Get("q"), Limit: int(queryInt(r, "limit"))}
	if f.Status == "" {
		f.Status = model.TaskPending
	}
	tasks, err := a.svc.ListTasks(r.Context(), f)
	if err != nil {
		return nil, err
	}
	out := make([]taskOut, len(tasks))
	for i := range tasks {
		out[i] = a.taskOut(&tasks[i])
	}
	return map[string]any{"tasks": out}, nil
}

func (a *API) createTask(_ http.ResponseWriter, r *http.Request) (any, error) {
	var in service.TaskInput
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	t, err := a.svc.CreateTask(r.Context(), in)
	if err != nil {
		return nil, err
	}
	return a.taskOut(t), nil
}

func (a *API) getTask(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	t, err := a.svc.GetTask(r.Context(), id)
	if err != nil {
		return nil, err
	}
	return a.taskOut(t), nil
}

func (a *API) updateTask(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	var in service.TaskInput
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	t, err := a.svc.UpdateTask(r.Context(), id, in)
	if err != nil {
		return nil, err
	}
	return a.taskOut(t), nil
}

func (a *API) deleteTask(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	return noContent{}, a.svc.DeleteTask(r.Context(), id)
}

func (a *API) taskAction(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	var t *model.Task
	switch {
	case strings.HasSuffix(r.URL.Path, "/uncomplete"):
		t, err = a.svc.UncompleteTask(r.Context(), id)
	case strings.HasSuffix(r.URL.Path, "/skip"):
		t, err = a.svc.SkipTask(r.Context(), id, source(r))
	default:
		t, err = a.svc.CompleteTask(r.Context(), id, source(r))
	}
	if err != nil {
		return nil, err
	}
	return a.taskOut(t), nil
}

// ---- reminders ----

type reminderOut struct {
	*model.Reminder
	Repeat      *service.RepeatInput `json:"repeat"`
	RepeatLabel string               `json:"repeat_description,omitempty"`
}

func (a *API) reminderOut(m *model.Reminder) reminderOut {
	return reminderOut{Reminder: m, Repeat: service.RepeatFromRule(m.RepeatRule, m.ActiveWindow, a.loc),
		RepeatLabel: service.DescribeRule(m.RepeatRule, m.ActiveWindow)}
}

func (a *API) listReminders(_ http.ResponseWriter, r *http.Request) (any, error) {
	rs, err := a.svc.ListReminders(r.Context(), queryInt(r, "task_id"))
	if err != nil {
		return nil, err
	}
	out := make([]reminderOut, len(rs))
	for i := range rs {
		out[i] = a.reminderOut(&rs[i])
	}
	return map[string]any{"reminders": out}, nil
}

func (a *API) createReminder(_ http.ResponseWriter, r *http.Request) (any, error) {
	var in service.ReminderInput
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	m, err := a.svc.CreateReminder(r.Context(), in)
	if err != nil {
		return nil, err
	}
	return a.reminderOut(m), nil
}

func (a *API) getReminder(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	m, err := a.svc.GetReminder(r.Context(), id)
	if err != nil {
		return nil, err
	}
	return a.reminderOut(m), nil
}

func (a *API) updateReminder(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	var in service.ReminderInput
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	m, err := a.svc.UpdateReminder(r.Context(), id, in)
	if err != nil {
		return nil, err
	}
	return a.reminderOut(m), nil
}

func (a *API) deleteReminder(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	return noContent{}, a.svc.DeleteReminder(r.Context(), id)
}

func (a *API) pauseReminder(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	m, err := a.svc.SetReminderEnabled(r.Context(), id, strings.HasSuffix(r.URL.Path, "/resume"))
	if err != nil {
		return nil, err
	}
	return a.reminderOut(m), nil
}

// ---- occurrences ----

func (a *API) listOccurrences(_ http.ResponseWriter, r *http.Request) (any, error) {
	q := r.URL.Query()
	now := a.svc.Now()
	from, err := a.queryTime(r, "from", now.AddDate(0, 0, -7))
	if err != nil {
		return nil, err
	}
	to, err := a.queryTime(r, "to", now.AddDate(0, 0, 1))
	if err != nil {
		return nil, err
	}
	f := model.OccurrenceFilter{From: &from, To: &to, Kind: q.Get("kind"), Tag: q.Get("tag"),
		ReminderID: queryInt(r, "reminder_id"), TaskID: queryInt(r, "task_id"), Limit: int(queryInt(r, "limit"))}
	if s := q.Get("status"); s != "" {
		f.Statuses = strings.Split(s, ",")
	}
	occs, err := a.svc.ListOccurrences(r.Context(), f)
	if err != nil {
		return nil, err
	}
	return map[string]any{"occurrences": nonNil(occs)}, nil
}

func (a *API) getOccurrence(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	return a.svc.GetOccurrence(r.Context(), id)
}

func (a *API) occAction(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	action := service.ActionDone
	switch {
	case strings.HasSuffix(r.URL.Path, "/skip"):
		action = service.ActionSkip
	case strings.HasSuffix(r.URL.Path, "/snooze"):
		var body struct {
			Minutes int    `json:"minutes"`
			Until   string `json:"until"`
		}
		if r.ContentLength != 0 {
			if err := decode(r, &body); err != nil {
				return nil, err
			}
		}
		switch {
		case body.Until == "morning":
			action = service.ActionSnoozeAM
		case body.Minutes == 0 || body.Minutes == 10:
			action = service.ActionSnooze10
		case body.Minutes == 30:
			action = service.ActionSnooze30
		case body.Minutes == 60:
			action = service.ActionSnooze60
		default:
			return nil, model.Invalid(`snooze: minutes must be 10, 30 or 60, or until "morning"`)
		}
	}
	return a.svc.OccurrenceAction(r.Context(), id, action, source(r))
}

// ---- memos ----

type memoBody struct {
	Content string `json:"content_md"`
}

func (a *API) listMemos(_ http.ResponseWriter, r *http.Request) (any, error) {
	q := r.URL.Query()
	f := model.MemoFilter{Tag: q.Get("tag"), Query: q.Get("q"), Limit: int(queryInt(r, "limit"))}
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	if b := queryInt(r, "before"); b > 0 {
		m, err := a.svc.GetMemo(r.Context(), b)
		if err != nil {
			return nil, model.Invalid("before: memo not found")
		}
		f.Before, f.BeforeID = &m.CreatedAt, m.ID
	}
	memos, err := a.svc.ListMemos(r.Context(), f)
	if err != nil {
		return nil, err
	}
	res := map[string]any{"memos": nonNil(memos)}
	if len(memos) == f.Limit {
		res["next_before"] = memos[len(memos)-1].ID
	}
	return res, nil
}

func (a *API) createMemo(_ http.ResponseWriter, r *http.Request) (any, error) {
	var b memoBody
	if err := decode(r, &b); err != nil {
		return nil, err
	}
	return a.svc.CreateMemo(r.Context(), b.Content)
}

func (a *API) getMemo(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	return a.svc.GetMemo(r.Context(), id)
}

func (a *API) updateMemo(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	var b memoBody
	if err := decode(r, &b); err != nil {
		return nil, err
	}
	return a.svc.UpdateMemo(r.Context(), id, b.Content)
}

func (a *API) deleteMemo(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	return noContent{}, a.svc.DeleteMemo(r.Context(), id)
}

func (a *API) pinMemo(_ http.ResponseWriter, r *http.Request) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	return a.svc.SetMemoPinned(r.Context(), id, strings.HasSuffix(r.URL.Path, "/pin"))
}

// ---- misc ----

func (a *API) tags(_ http.ResponseWriter, r *http.Request) (any, error) {
	tc, err := a.svc.TagCounts(r.Context(), r.URL.Query().Get("kind"))
	if err != nil {
		return nil, err
	}
	return map[string]any{"tags": nonNil(tc)}, nil
}

func (a *API) calendar(_ http.ResponseWriter, r *http.Request) (any, error) {
	start := a.svc.StartOfDay(a.svc.Now())
	from, err := a.queryTime(r, "from", start)
	if err != nil {
		return nil, err
	}
	to, err := a.queryTime(r, "to", from.AddDate(0, 0, 7))
	if err != nil {
		return nil, err
	}
	return a.svc.Calendar(r.Context(), from, to)
}

func (a *API) history(_ http.ResponseWriter, r *http.Request) (any, error) {
	q := r.URL.Query()
	now := a.svc.Now()
	from, err := a.queryTime(r, "from", a.svc.StartOfDay(now).AddDate(0, 0, -7))
	if err != nil {
		return nil, err
	}
	to, err := a.queryTime(r, "to", now.Add(time.Minute))
	if err != nil {
		return nil, err
	}
	occs, err := a.svc.History(r.Context(), service.HistoryFilter{From: from, To: to, Kind: q.Get("kind"), Tag: q.Get("tag"), Status: q.Get("status")})
	if err != nil {
		return nil, err
	}
	return map[string]any{"history": nonNil(occs)}, nil
}

func (a *API) stats(_ http.ResponseWriter, r *http.Request) (any, error) {
	st, err := a.svc.Stats(r.Context())
	if err != nil {
		return nil, err
	}
	return map[string]any{"stats": st}, nil
}

func (a *API) search(_ http.ResponseWriter, r *http.Request) (any, error) {
	hits, err := a.svc.Search(r.Context(), r.URL.Query().Get("q"), int(queryInt(r, "limit")))
	if err != nil {
		return nil, err
	}
	for i := range hits {
		hits[i].Snippet = strings.NewReplacer("\x02", "", "\x03", "").Replace(hits[i].Snippet)
	}
	return map[string]any{"results": nonNil(hits)}, nil
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
