package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sovannroth/mynotes/internal/model"
)

func TestTaskAlertBeforeDue(t *testing.T) {
	e := newEnv(t, "2026-03-01 07:00")
	ctx := context.Background()
	start, due := at("2026-03-01 08:00"), at("2026-03-01 10:00")
	task, err := e.svc.CreateTask(ctx, TaskInput{Title: "Write report", StartAt: &start, DueAt: &due, Alert: "15m"})
	if err != nil {
		t.Fatal(err)
	}
	if task.NextAlertAt == nil || !task.NextAlertAt.Equal(at("2026-03-01 09:45")) {
		t.Fatalf("next alert %v", task.NextAlertAt)
	}
	e.clk.set("2026-03-01 09:44")
	e.tick()
	if e.n.count() != 0 {
		t.Fatal("alert sent too early")
	}
	e.clk.set("2026-03-01 09:45")
	e.tick()
	if e.n.count() != 1 {
		t.Fatalf("expected 1 alert, got %d", e.n.count())
	}
	msg := e.n.sent[0]
	if !strings.Contains(msg.Text, "Write report") || !strings.Contains(msg.Text, "Start Sun 01 Mar 08:00") || !strings.Contains(msg.Text, "Due Sun 01 Mar 10:00") {
		t.Fatalf("message text %q", msg.Text)
	}
	// No second send: no nagging, and the due time reuses the same occurrence.
	e.clk.set("2026-03-01 10:05")
	e.tick()
	if e.n.count() != 1 {
		t.Fatalf("alert repeated: %d", e.n.count())
	}
	// Done from Telegram completes the task.
	if _, err := e.svc.OccurrenceAction(ctx, msg.OccurrenceID, ActionDone, model.SourceTelegram); err != nil {
		t.Fatal(err)
	}
	task, _ = e.svc.GetTask(ctx, task.ID)
	if task.Status != model.TaskDone || task.NextAlertAt != nil {
		t.Fatalf("task %+v", task)
	}
}

func TestTaskAlertSnoozeAndQuietHours(t *testing.T) {
	e := newEnv(t, "2026-03-01 20:00")
	ctx := context.Background()
	if err := e.svc.UpdateSettings(ctx, SettingsInput{QuietHours: "22:00-07:00", SnoozeMorning: "07:00", ExportTime: "21:00", DefaultNagInterval: 10}); err != nil {
		t.Fatal(err)
	}
	due := at("2026-03-01 23:00")
	task, err := e.svc.CreateTask(ctx, TaskInput{Title: "Night task", DueAt: &due, Alert: AlertDue})
	if err != nil {
		t.Fatal(err)
	}
	e.clk.set("2026-03-01 23:00")
	e.tick()
	if e.n.count() != 0 {
		t.Fatal("alert should wait for the end of quiet hours")
	}
	e.clk.set("2026-03-02 07:00")
	e.tick()
	if e.n.count() != 1 {
		t.Fatalf("alert after quiet hours: %d", e.n.count())
	}
	id := e.n.sent[0].OccurrenceID
	if _, err := e.svc.OccurrenceAction(ctx, id, ActionSnooze10, model.SourceTelegram); err != nil {
		t.Fatal(err)
	}
	e.clk.set("2026-03-02 07:10")
	e.tick()
	if e.n.count() != 2 {
		t.Fatalf("snoozed alert should be sent again: %d", e.n.count())
	}
	task, _ = e.svc.GetTask(ctx, task.ID)
	if task.Status != model.TaskPending {
		t.Fatal("snooze must not complete the task")
	}
}

func TestRepeatingTaskAlertMovesWithDue(t *testing.T) {
	e := newEnv(t, "2026-03-02 06:00") // Monday
	ctx := context.Background()
	start, due := at("2026-03-02 08:00"), at("2026-03-02 09:00")
	task, err := e.svc.CreateTask(ctx, TaskInput{Title: "Standup", StartAt: &start, DueAt: &due, Alert: AlertStart,
		Repeat: &RepeatInput{Freq: "daily", Interval: 1}})
	if err != nil {
		t.Fatal(err)
	}
	e.clk.set("2026-03-02 08:00")
	e.tick()
	if e.n.count() != 1 {
		t.Fatalf("first alert: %d", e.n.count())
	}
	// The next alert is already scheduled for tomorrow's start, even if today is never completed.
	task, _ = e.svc.GetTask(ctx, task.ID)
	if task.NextAlertAt == nil || !task.NextAlertAt.Equal(at("2026-03-03 08:00")) {
		t.Fatalf("next alert %v", task.NextAlertAt)
	}
	e.clk.set("2026-03-03 08:00")
	e.tick()
	if e.n.count() != 2 {
		t.Fatalf("second alert: %d", e.n.count())
	}
	e.svc.RecordTelegramMessage(ctx, e.n.sent[1].OccurrenceID, 1, 100) // as the bot does after sending
	// Today's due passes without completion: the old instance becomes missed, the start moves along.
	e.clk.set("2026-03-03 09:00")
	e.tick()
	task, _ = e.svc.GetTask(ctx, task.ID)
	if !task.DueAt.Equal(at("2026-03-03 09:00")) || !task.StartAt.Equal(at("2026-03-03 08:00")) {
		t.Fatalf("due %v start %v", task.DueAt.In(pp), task.StartAt.In(pp))
	}
	// Completing advances start and due together; the alert sent for this instance is closed.
	task, err = e.svc.CompleteTask(ctx, task.ID, model.SourceWeb)
	if err != nil {
		t.Fatal(err)
	}
	if !task.DueAt.Equal(at("2026-03-04 09:00")) || !task.StartAt.Equal(at("2026-03-04 08:00")) {
		t.Fatalf("after complete: due %v start %v", task.DueAt.In(pp), task.StartAt.In(pp))
	}
	if task.NextAlertAt == nil || !task.NextAlertAt.Equal(at("2026-03-04 08:00")) {
		t.Fatalf("next alert after complete %v", task.NextAlertAt)
	}
	if len(e.n.finalized) == 0 {
		t.Fatal("telegram buttons of the completed alert should be removed")
	}
}

func TestTaskAlertValidation(t *testing.T) {
	e := newEnv(t, "2026-03-01 07:00")
	ctx := context.Background()
	start, due := at("2026-03-01 10:00"), at("2026-03-01 09:00")
	cases := []TaskInput{
		{Title: "a", Alert: "15m"},                   // no due
		{Title: "b", DueAt: &due, Alert: AlertStart}, // start alert without start
		{Title: "c", DueAt: &due, Alert: "3w"},       // unknown
		{Title: "d", StartAt: &start, DueAt: &due},   // start after due
	}
	for _, in := range cases {
		if _, err := e.svc.CreateTask(ctx, in); !errors.Is(err, model.ErrValidation) {
			t.Errorf("%s: expected validation error, got %v", in.Title, err)
		}
	}
	// Alert time already past but the task is still ahead: fires on the next tick.
	soon := at("2026-03-01 07:10")
	if _, err := e.svc.CreateTask(ctx, TaskInput{Title: "soon", DueAt: &soon, Alert: "1h"}); err != nil {
		t.Fatal(err)
	}
	e.tick()
	if e.n.count() != 1 {
		t.Fatalf("late alert should fire at once: %d", e.n.count())
	}
	// Editing the title must not send the alert again.
	tasks, _ := e.svc.ListTasks(ctx, model.TaskFilter{Query: "soon"})
	if _, err := e.svc.UpdateTask(ctx, tasks[0].ID, TaskInput{Title: "soon!", DueAt: &soon, Alert: "1h"}); err != nil {
		t.Fatal(err)
	}
	e.tick()
	if e.n.count() != 1 {
		t.Fatalf("edit re-sent the alert: %d", e.n.count())
	}
}
