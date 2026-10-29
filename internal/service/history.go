package service

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/sovannroth/mynotes/internal/model"
)

var historyStatuses = []string{model.OccDone, model.OccSkipped, model.OccMissed}

// HistoryFilter selects history entries.
type HistoryFilter struct {
	From, To time.Time
	Kind     string // reminder | task | ""
	Tag      string
	Status   string // done | skipped | missed | "" (all closed)
}

// History returns closed occurrences whose outcome happened in the range
// (done_at, or scheduled time for missed items), newest first.
func (s *Service) History(ctx context.Context, f HistoryFilter) ([]model.Occurrence, error) {
	statuses := historyStatuses
	if f.Status != "" {
		statuses = []string{f.Status}
	}
	return s.st.ListOccurrences(ctx, model.OccurrenceFilter{From: &f.From, To: &f.To, Kind: f.Kind, Tag: f.Tag, Statuses: statuses, Limit: 5000, ByOutcome: true})
}

// OutcomeTime is when an occurrence's outcome happened (used to group history).
func OutcomeTime(o *model.Occurrence) time.Time {
	if o.DoneAt != nil {
		return *o.DoneAt
	}
	return o.ScheduledFor
}

// Stat is completion statistics for one reminder or task.
type Stat struct {
	Kind    string  `json:"kind"`
	ID      int64   `json:"id"`
	Title   string  `json:"title"`
	Done7   int     `json:"done_7"`
	Total7  int     `json:"total_7"`
	Rate7   float64 `json:"rate_7"`
	Done30  int     `json:"done_30"`
	Total30 int     `json:"total_30"`
	Rate30  float64 `json:"rate_30"`
}

// Stats computes completion rates over the last 7 and 30 days, counting only
// occurrences that are already decided (done, skipped or missed).
func (s *Service) Stats(ctx context.Context) ([]Stat, error) {
	now := s.now().UTC()
	from := now.AddDate(0, 0, -30)
	occs, err := s.st.ListOccurrences(ctx, model.OccurrenceFilter{From: &from, To: &now, Statuses: historyStatuses, Limit: 5000})
	if err != nil {
		return nil, err
	}
	type key struct {
		kind string
		id   int64
	}
	m := map[key]*Stat{}
	cut7 := now.AddDate(0, 0, -7)
	for _, o := range occs {
		k := key{o.Kind, 0}
		if o.ReminderID != nil {
			k.id = *o.ReminderID
		} else {
			k.id = *o.TaskID
		}
		st := m[k]
		if st == nil {
			st = &Stat{Kind: k.kind, ID: k.id, Title: o.Title}
			m[k] = st
		}
		st.Total30++
		if o.Status == model.OccDone {
			st.Done30++
		}
		if !o.ScheduledFor.Before(cut7) {
			st.Total7++
			if o.Status == model.OccDone {
				st.Done7++
			}
		}
	}
	out := make([]Stat, 0, len(m))
	for _, st := range m {
		if st.Total7 > 0 {
			st.Rate7 = float64(st.Done7) / float64(st.Total7)
		}
		st.Rate30 = float64(st.Done30) / float64(st.Total30)
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Total30 != out[j].Total30 {
			return out[i].Total30 > out[j].Total30
		}
		return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
	})
	return out, nil
}

func firstLine(s string) string { return model.FirstLine(s) }
