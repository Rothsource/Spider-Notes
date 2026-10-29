package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/store"
)

func openTest(t *testing.T) *DB {
	t.Helper()
	d, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestMigrateTwiceAndTasks(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "m.db")
	d, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	d, err = Open(ctx, path) // idempotent migrations
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	now := time.Now().UTC().Truncate(time.Second)
	due := now.Add(time.Hour)
	task := &model.Task{Title: "Buy rice", DescriptionMD: "jasmine rice", DueAt: &due, Priority: "high", Status: "pending", CreatedAt: now, UpdatedAt: now}
	if err := d.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := d.SetTaskTags(ctx, task.ID, []string{"home", "food"}); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Buy rice" || !got.DueAt.Equal(due) || len(got.Tags) != 2 {
		t.Fatalf("unexpected task %+v", got)
	}
	list, err := d.ListTasks(ctx, model.TaskFilter{Status: "pending", Tag: "food", Query: `"jasmine"*`})
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %d", err, len(list))
	}
	if err := d.SoftDeleteTask(ctx, task.ID, now); err != nil {
		t.Fatal(err)
	}
	list, _ = d.ListTasks(ctx, model.TaskFilter{Status: "all"})
	if len(list) != 0 {
		t.Fatal("deleted task listed")
	}
	if _, err := d.GetTask(ctx, task.ID); err != nil {
		t.Fatal("soft-deleted task should still be readable for history")
	}
}

func TestOccurrenceIdempotentInsertAndTx(t *testing.T) {
	ctx := context.Background()
	d := openTest(t)
	now := time.Now().UTC().Truncate(time.Second)
	r := &model.Reminder{MessageMD: "Drink water", ScheduleType: model.OneTime, FireAt: now, Timezone: "UTC", Enabled: true, NextFireAt: &now, NagIntervalMin: 10, CreatedAt: now, UpdatedAt: now}
	if err := d.CreateReminder(ctx, r); err != nil {
		t.Fatal(err)
	}
	due, err := d.DueReminders(ctx, now)
	if err != nil || len(due) != 1 {
		t.Fatalf("due: %v %d", err, len(due))
	}
	o := &model.Occurrence{ReminderID: &r.ID, ScheduledFor: now, Status: model.OccPending, NextSendAt: &now, CreatedAt: now, UpdatedAt: now}
	created, err := d.InsertOccurrence(ctx, o)
	if err != nil || !created {
		t.Fatalf("insert: %v %v", err, created)
	}
	dup := *o
	created, err = d.InsertOccurrence(ctx, &dup)
	if err != nil || created {
		t.Fatalf("duplicate insert should be ignored: %v %v", err, created)
	}
	dd, err := d.DueDeliveries(ctx, now)
	if err != nil || len(dd) != 1 || dd[0].Title != "Drink water" || dd[0].Kind != "reminder" {
		t.Fatalf("deliveries: %v %+v", err, dd)
	}
	// Rolled-back transaction leaves no trace.
	_ = d.InTx(ctx, func(s store.Store) error {
		_ = s.SetSetting(ctx, "k", "v")
		return context.Canceled
	})
	m, _ := d.GetSettings(ctx)
	if _, ok := m["k"]; ok {
		t.Fatal("rollback failed")
	}
}

func TestMemosSearchAndTags(t *testing.T) {
	ctx := context.Background()
	d := openTest(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, c := range []string{"Morning run 5km #health", "Meeting notes #work", "Doctor visit #health"} {
		m := &model.Memo{ContentMD: c, CreatedAt: base.Add(time.Duration(i) * time.Hour), UpdatedAt: base}
		if err := d.CreateMemo(ctx, m); err != nil {
			t.Fatal(err)
		}
		tag := "work"
		if i != 1 {
			tag = "health"
		}
		if err := d.SetMemoTags(ctx, m.ID, []string{tag}); err != nil {
			t.Fatal(err)
		}
	}
	tc, err := d.TagCounts(ctx, "memo")
	if err != nil || len(tc) != 2 || tc[0].Name != "health" || tc[0].Count != 2 {
		t.Fatalf("tag counts: %v %+v", err, tc)
	}
	hits, err := d.Search(ctx, `"doctor"*`, 10)
	if err != nil || len(hits) != 1 || hits[0].Kind != "memo" {
		t.Fatalf("search: %v %+v", err, hits)
	}
	page, _ := d.ListMemos(ctx, model.MemoFilter{Limit: 2}, false)
	if len(page) != 2 || page[0].ContentMD != "Doctor visit #health" {
		t.Fatalf("page1 %+v", page)
	}
	page2, _ := d.ListMemos(ctx, model.MemoFilter{Limit: 2, Before: &page[1].CreatedAt, BeforeID: page[1].ID}, false)
	if len(page2) != 1 || page2[0].ContentMD != "Morning run 5km #health" {
		t.Fatalf("page2 %+v", page2)
	}
	// FTS stays in sync on update.
	page2[0].ContentMD = "Evening swim"
	if err := d.UpdateMemo(ctx, &page2[0]); err != nil {
		t.Fatal(err)
	}
	if hits, _ := d.Search(ctx, `"swim"*`, 10); len(hits) != 1 {
		t.Fatal("fts update not synced")
	}
	if hits, _ := d.Search(ctx, `"run"*`, 10); len(hits) != 0 {
		t.Fatal("stale fts entry")
	}
	if err := d.Backup(ctx, filepath.Join(t.TempDir(), "b.db")); err != nil {
		t.Fatal(err)
	}
}
