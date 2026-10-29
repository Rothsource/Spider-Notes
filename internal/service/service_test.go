package service

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/store/sqlite"
)

var pp, _ = time.LoadLocation("Asia/Phnom_Penh")

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) set(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = at(s)
}
func (c *clock) add(d time.Duration) { c.mu.Lock(); defer c.mu.Unlock(); c.t = c.t.Add(d) }

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, pp)
	if err != nil {
		panic(err)
	}
	return t
}

type fakeNotifier struct {
	mu        sync.Mutex
	sent      []Message
	finalized []string
}

func (f *fakeNotifier) Enabled() bool { return true }
func (f *fakeNotifier) Deliver(m Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
}
func (f *fakeNotifier) Finalize(_ int64, _ int, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finalized = append(f.finalized, text)
}
func (f *fakeNotifier) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.sent) }

type env struct {
	svc  *Service
	clk  *clock
	n    *fakeNotifier
	db   *sqlite.DB
	path string
	t    *testing.T
}

func newEnv(t *testing.T, start string) *env {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "t.db")
	db, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	clk := &clock{}
	clk.set(start)
	e := &env{clk: clk, n: &fakeNotifier{}, db: db, path: path, t: t}
	e.svc = New(Options{Store: db, Location: pp, Now: clk.now, ExportDir: filepath.Join(dir, "export")})
	e.svc.SetNotifier(e.n)
	return e
}

// restart simulates a process restart: a new Service on the same DB.
func (e *env) restart() {
	e.svc = New(Options{Store: e.db, Location: pp, Now: e.clk.now})
	e.svc.SetNotifier(e.n)
}

func (e *env) tick() {
	e.t.Helper()
	if err := e.svc.ProcessDue(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) occs(reminderID int64) []model.Occurrence {
	e.t.Helper()
	o, err := e.db.ListOccurrences(context.Background(), model.OccurrenceFilter{ReminderID: reminderID})
	if err != nil {
		e.t.Fatal(err)
	}
	return o
}

func daily(hm string) *RepeatInput { return &RepeatInput{Freq: "daily", Time: hm} }

func TestOneTimeReminderSendsOnce(t *testing.T) {
	e := newEnv(t, "2026-03-01 07:00")
	ctx := context.Background()
	r, err := e.svc.CreateReminder(ctx, ReminderInput{Message: "You should not eat this this morning", FireAt: at("2026-03-01 08:00")})
	if err != nil {
		t.Fatal(err)
	}
	e.tick()
	if e.n.count() != 0 {
		t.Fatal("sent too early")
	}
	e.clk.set("2026-03-01 08:00")
	e.tick()
	e.tick()
	if e.n.count() != 1 {
		t.Fatalf("want 1 send, got %d", e.n.count())
	}
	if !strings.Contains(e.n.sent[0].Text, "should not eat") || e.n.sent[0].AllowSkip {
		t.Fatalf("bad message %+v", e.n.sent[0])
	}
	got, _ := e.svc.GetReminder(ctx, r.ID)
	if got.NextFireAt != nil {
		t.Fatal("one-time reminder should be finished")
	}
}

func TestNagUntilDone(t *testing.T) {
	e := newEnv(t, "2026-03-01 07:59")
	ctx := context.Background()
	r, err := e.svc.CreateReminder(ctx, ReminderInput{Message: "Take medicine", FireAt: at("2026-03-01 08:00"), NagEnabled: true, NagIntervalMin: 10})
	if err != nil {
		t.Fatal(err)
	}
	e.clk.set("2026-03-01 08:00")
	e.tick()
	e.clk.add(5 * time.Minute)
	e.tick()
	if e.n.count() != 1 {
		t.Fatalf("nag too early: %d", e.n.count())
	}
	e.clk.add(5 * time.Minute)
	e.tick()
	e.clk.add(10 * time.Minute)
	e.tick()
	if e.n.count() != 3 || !strings.Contains(e.n.sent[2].Text, "(reminder #3)") {
		t.Fatalf("want 3 sends with counter, got %d %+v", e.n.count(), e.n.sent)
	}
	occ := e.occs(r.ID)[0]
	if _, err := e.svc.OccurrenceAction(ctx, occ.ID, ActionDone, model.SourceTelegram); err != nil {
		t.Fatal(err)
	}
	e.clk.add(time.Hour)
	e.tick()
	if e.n.count() != 3 {
		t.Fatal("nag continued after done")
	}
	occ = e.occs(r.ID)[0]
	if occ.Status != model.OccDone || occ.ActionSource != model.SourceTelegram || occ.SentCount != 3 || occ.DoneAt == nil {
		t.Fatalf("bad occurrence %+v", occ)
	}
	if _, err := e.svc.OccurrenceAction(ctx, occ.ID, ActionDone, model.SourceWeb); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("second action should conflict, got %v", err)
	}
}

func TestSnoozeResends(t *testing.T) {
	e := newEnv(t, "2026-03-01 07:00")
	ctx := context.Background()
	r, _ := e.svc.CreateReminder(ctx, ReminderInput{Message: "Call mom", FireAt: at("2026-03-01 08:00")})
	e.clk.set("2026-03-01 08:00")
	e.tick()
	occ := e.occs(r.ID)[0]
	if _, err := e.svc.OccurrenceAction(ctx, occ.ID, ActionSnooze10, model.SourceTelegram); err != nil {
		t.Fatal(err)
	}
	e.clk.add(9 * time.Minute)
	e.tick()
	if e.n.count() != 1 {
		t.Fatal("snooze expired too early")
	}
	e.clk.add(time.Minute)
	e.tick()
	occ = e.occs(r.ID)[0]
	if e.n.count() != 2 || occ.Status != model.OccSent || occ.SnoozeCount != 1 {
		t.Fatalf("snooze resend failed: %d %+v", e.n.count(), occ)
	}
	// Snooze until tomorrow morning (default 07:00).
	o, err := e.svc.OccurrenceAction(ctx, occ.ID, ActionSnoozeAM, model.SourceWeb)
	if err != nil {
		t.Fatal(err)
	}
	if !o.SnoozedUntil.Equal(at("2026-03-02 07:00")) {
		t.Fatalf("snooze morning: %v", o.SnoozedUntil.In(pp))
	}
}

func TestSkipOnlyRepeating(t *testing.T) {
	e := newEnv(t, "2026-03-01 07:00")
	ctx := context.Background()
	one, _ := e.svc.CreateReminder(ctx, ReminderInput{Message: "once", FireAt: at("2026-03-01 08:00")})
	rep, _ := e.svc.CreateReminder(ctx, ReminderInput{Message: "daily", FireAt: at("2026-03-01 00:00"), Repeat: daily("08:00")})
	e.clk.set("2026-03-01 08:00")
	e.tick()
	if _, err := e.svc.OccurrenceAction(ctx, e.occs(one.ID)[0].ID, ActionSkip, model.SourceWeb); !errors.Is(err, model.ErrValidation) {
		t.Fatalf("skip one-time should fail: %v", err)
	}
	if !e.n.sent[1].AllowSkip && !e.n.sent[0].AllowSkip {
		t.Fatal("repeating message should allow skip")
	}
	o, err := e.svc.OccurrenceAction(ctx, e.occs(rep.ID)[0].ID, ActionSkip, model.SourceWeb)
	if err != nil || o.Status != model.OccSkipped {
		t.Fatalf("skip: %v %+v", err, o)
	}
	got, _ := e.svc.GetReminder(ctx, rep.ID)
	if !got.NextFireAt.Equal(at("2026-03-02 08:00")) {
		t.Fatalf("next fire: %v", got.NextFireAt.In(pp))
	}
}

func TestQuietHoursHold(t *testing.T) {
	e := newEnv(t, "2026-03-01 20:00")
	ctx := context.Background()
	if err := e.svc.UpdateSettings(ctx, SettingsInput{QuietHours: "22:00-06:30", SnoozeMorning: "07:00", DefaultNagInterval: 30, ExportTime: "23:55"}); err != nil {
		t.Fatal(err)
	}
	held, _ := e.svc.CreateReminder(ctx, ReminderInput{Message: "late", FireAt: at("2026-03-01 23:00")})
	urgent, _ := e.svc.CreateReminder(ctx, ReminderInput{Message: "urgent", FireAt: at("2026-03-01 23:00"), IgnoreQuietHours: true})
	e.clk.set("2026-03-01 23:00")
	e.tick()
	if e.n.count() != 1 || !strings.Contains(e.n.sent[0].Text, "urgent") {
		t.Fatalf("only the ignore-quiet reminder should be sent: %+v", e.n.sent)
	}
	o := e.occs(held.ID)[0]
	if o.Status != model.OccPending || !o.NextSendAt.Equal(at("2026-03-02 06:30")) {
		t.Fatalf("held occurrence %+v", o)
	}
	e.clk.set("2026-03-02 06:29")
	e.tick()
	if e.n.count() != 1 {
		t.Fatal("released too early")
	}
	e.clk.set("2026-03-02 06:30")
	e.tick()
	if e.n.count() != 2 {
		t.Fatal("not released at end of quiet hours")
	}
	// Per-reminder override "off" disables quiet hours.
	_, _ = e.svc.UpdateReminder(ctx, urgent.ID, ReminderInput{Message: "urgent", FireAt: at("2026-03-02 23:30"), QuietHoursOverride: "off"})
	e.clk.set("2026-03-02 23:30")
	e.tick()
	if e.n.count() != 3 {
		t.Fatal("override off should send during quiet hours")
	}
}

func TestRestartCatchUpSendsOnce(t *testing.T) {
	e := newEnv(t, "2026-03-01 07:00")
	ctx := context.Background()
	r, _ := e.svc.CreateReminder(ctx, ReminderInput{Message: "Water plants", FireAt: at("2026-03-01 00:00"), Repeat: daily("08:00")})
	e.clk.set("2026-03-01 08:00")
	e.tick()
	if e.n.count() != 1 {
		t.Fatal("first send missing")
	}
	// Server down for 3 days, then restarts.
	e.clk.set("2026-03-04 09:00")
	e.restart()
	e.tick()
	e.tick()
	if e.n.count() != 2 {
		t.Fatalf("want exactly one catch-up message, got %d sends", e.n.count())
	}
	if !strings.Contains(e.n.sent[1].Text, "04 Mar 08:00") {
		t.Fatalf("catch-up should be the latest occurrence: %s", e.n.sent[1].Text)
	}
	occs := e.occs(r.ID)
	statuses := map[string]int{}
	for _, o := range occs {
		statuses[o.Status]++
	}
	// Mar 1 (sent, then missed when Mar 2 arrived), Mar 2, Mar 3 missed; Mar 4 sent.
	if statuses[model.OccMissed] != 3 || statuses[model.OccSent] != 1 || len(occs) != 4 {
		t.Fatalf("unexpected history %v (%d rows)", statuses, len(occs))
	}
	got, _ := e.svc.GetReminder(ctx, r.ID)
	if !got.NextFireAt.Equal(at("2026-03-05 08:00")) {
		t.Fatalf("next fire %v", got.NextFireAt.In(pp))
	}
	// A restart between ticks never re-sends.
	e.restart()
	e.tick()
	if e.n.count() != 2 {
		t.Fatal("double send after restart")
	}
}

func TestMissedAfter24h(t *testing.T) {
	e := newEnv(t, "2026-03-01 07:00")
	ctx := context.Background()
	r, _ := e.svc.CreateReminder(ctx, ReminderInput{Message: "once", FireAt: at("2026-03-01 08:00")})
	e.clk.set("2026-03-01 08:00")
	e.tick()
	e.clk.set("2026-03-02 08:01")
	e.tick()
	if o := e.occs(r.ID)[0]; o.Status != model.OccMissed {
		t.Fatalf("want missed, got %s", o.Status)
	}
}

func TestPauseResumeAndEdit(t *testing.T) {
	e := newEnv(t, "2026-03-01 07:00")
	ctx := context.Background()
	r, _ := e.svc.CreateReminder(ctx, ReminderInput{Message: "x", FireAt: at("2026-03-01 00:00"), Repeat: daily("08:00")})
	if _, err := e.svc.SetReminderEnabled(ctx, r.ID, false); err != nil {
		t.Fatal(err)
	}
	e.clk.set("2026-03-03 10:00")
	e.tick()
	if e.n.count() != 0 {
		t.Fatal("paused reminder sent")
	}
	got, _ := e.svc.SetReminderEnabled(ctx, r.ID, true)
	if !got.NextFireAt.Equal(at("2026-03-04 08:00")) {
		t.Fatalf("resume should schedule from now: %v", got.NextFireAt.In(pp))
	}
	// Editing the schedule recalculates immediately.
	got, err := e.svc.UpdateReminder(ctx, r.ID, ReminderInput{Message: "x", FireAt: at("2026-03-01 00:00"), Repeat: &RepeatInput{Freq: "weekly", Weekdays: []int{5}, Time: "18:30"}})
	if err != nil {
		t.Fatal(err)
	}
	if !got.NextFireAt.Equal(at("2026-03-06 18:30")) {
		t.Fatalf("edit recalculation: %v", got.NextFireAt.In(pp))
	}
	if err := e.svc.DeleteReminder(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetReminder(ctx, r.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatal("deleted reminder should be hidden")
	}
}

func TestRepeatingTaskCompletion(t *testing.T) {
	e := newEnv(t, "2026-01-30 07:00")
	ctx := context.Background()
	due := at("2026-01-31 09:00")
	task, err := e.svc.CreateTask(ctx, TaskInput{Title: "Pay rent", DueAt: &due, Tags: []string{"#Home"}, Repeat: &RepeatInput{Freq: "monthly"}})
	if err != nil {
		t.Fatal(err)
	}
	if task.Tags[0] != "home" {
		t.Fatalf("tags %v", task.Tags)
	}
	task, err = e.svc.CompleteTask(ctx, task.ID, model.SourceWeb)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != model.TaskPending || !task.DueAt.Equal(at("2026-02-28 09:00")) {
		t.Fatalf("should advance to Feb 28: %+v", task.DueAt.In(pp))
	}
	task, _ = e.svc.CompleteTask(ctx, task.ID, model.SourceWeb)
	if !task.DueAt.Equal(at("2026-03-31 09:00")) {
		t.Fatalf("should advance to Mar 31: %v", task.DueAt.In(pp))
	}
	hist, _ := e.svc.History(ctx, HistoryFilter{From: at("2026-01-01 00:00"), To: at("2026-04-01 00:00")})
	if len(hist) != 2 || hist[0].Status != model.OccDone {
		t.Fatalf("history %+v", hist)
	}
	// Not done on time: the scheduler marks it missed when the next one arrives.
	e.clk.set("2026-04-30 10:00")
	e.tick()
	task, _ = e.svc.GetTask(ctx, task.ID)
	if !task.DueAt.Equal(at("2026-04-30 09:00")) {
		t.Fatalf("overdue repeating task should advance: %v", task.DueAt.In(pp))
	}
	hist, _ = e.svc.History(ctx, HistoryFilter{From: at("2026-01-01 00:00"), To: at("2026-06-01 00:00"), Status: model.OccMissed})
	if len(hist) != 1 {
		t.Fatalf("missed rows %d", len(hist))
	}
}

func TestTaskReminderDoneCompletesTask(t *testing.T) {
	e := newEnv(t, "2026-03-01 07:00")
	ctx := context.Background()
	due := at("2026-03-01 12:00")
	task, _ := e.svc.CreateTask(ctx, TaskInput{Title: "Submit report", DueAt: &due})
	r, err := e.svc.CreateReminder(ctx, ReminderInput{TaskID: &task.ID, FireAt: at("2026-03-01 09:00"), NagEnabled: true, NagIntervalMin: 30})
	if err != nil {
		t.Fatal(err)
	}
	e.clk.set("2026-03-01 09:00")
	e.tick()
	if !strings.Contains(e.n.sent[0].Text, "Task: Submit report") {
		t.Fatalf("message should include task title: %s", e.n.sent[0].Text)
	}
	if _, err := e.svc.OccurrenceAction(ctx, e.occs(r.ID)[0].ID, ActionDone, model.SourceTelegram); err != nil {
		t.Fatal(err)
	}
	task, _ = e.svc.GetTask(ctx, task.ID)
	if task.Status != model.TaskDone {
		t.Fatal("task should be done")
	}
	// Completing the task directly also stops nags of attached reminders.
	due2 := at("2026-03-02 12:00")
	t2, _ := e.svc.CreateTask(ctx, TaskInput{Title: "Other", DueAt: &due2})
	r2, _ := e.svc.CreateReminder(ctx, ReminderInput{TaskID: &t2.ID, Message: "nag", FireAt: at("2026-03-01 10:00"), NagEnabled: true, NagIntervalMin: 10})
	e.clk.set("2026-03-01 10:00")
	e.tick()
	if _, err := e.svc.CompleteTask(ctx, t2.ID, model.SourceWeb); err != nil {
		t.Fatal(err)
	}
	n := e.n.count()
	e.clk.add(time.Hour)
	e.tick()
	if e.n.count() != n || e.occs(r2.ID)[0].Status != model.OccDone {
		t.Fatal("nags should stop when the task is completed")
	}
}

func TestEveryNHoursWindowReminder(t *testing.T) {
	e := newEnv(t, "2026-03-01 07:00")
	ctx := context.Background()
	r, err := e.svc.CreateReminder(ctx, ReminderInput{Message: "Stretch", FireAt: at("2026-03-01 08:00"), Repeat: &RepeatInput{Freq: "hourly", Interval: 2, Window: "08:00-12:00", Count: 4}})
	if err != nil {
		t.Fatal(err)
	}
	var fired []string
	for e.clk.now().Before(at("2026-03-03 00:00")) {
		before := e.n.count()
		e.tick()
		if e.n.count() > before {
			fired = append(fired, e.clk.now().In(pp).Format("02 15:04"))
		}
		e.clk.add(30 * time.Minute)
	}
	want := "01 08:00,01 10:00,01 12:00,02 08:00"
	if strings.Join(fired, ",") != want {
		t.Fatalf("fired %v want %s", fired, want)
	}
	got, _ := e.svc.GetReminder(ctx, r.ID)
	if got.NextFireAt != nil {
		t.Fatal("count exhausted, should be finished")
	}
}

func TestValidation(t *testing.T) {
	e := newEnv(t, "2026-03-01 07:00")
	ctx := context.Background()
	cases := []ReminderInput{
		{Message: "", FireAt: at("2026-03-01 08:00")},
		{Message: "x"},
		{Message: "x", FireAt: at("2026-02-01 08:00")},
		{Message: "x", FireAt: at("2026-03-01 08:00"), NagEnabled: true, NagIntervalMin: 0},
		{Message: "x", FireAt: at("2026-03-01 08:00"), Repeat: &RepeatInput{Freq: "weekly", Time: "08:00"}},
		{Message: "x", FireAt: at("2026-03-01 08:00"), QuietHoursOverride: "nonsense"},
		{Message: strings.Repeat("a", MaxMessageLen+1), FireAt: at("2026-03-01 08:00")},
	}
	for i, c := range cases {
		if _, err := e.svc.CreateReminder(ctx, c); !errors.Is(err, model.ErrValidation) {
			t.Fatalf("case %d: want validation error, got %v", i, err)
		}
	}
	if _, err := e.svc.CreateMemo(ctx, strings.Repeat("a", MaxMemoLen+1)); !errors.Is(err, model.ErrValidation) {
		t.Fatal("memo length limit")
	}
	if _, err := e.svc.CreateTask(ctx, TaskInput{Title: "x", Priority: "urgent"}); !errors.Is(err, model.ErrValidation) {
		t.Fatal("priority validation")
	}
}

func TestMemosCalendarExport(t *testing.T) {
	e := newEnv(t, "2026-03-01 07:00")
	ctx := context.Background()
	m, err := e.svc.CreateMemo(ctx, "Slept well #health\n- [ ] drink water")
	if err != nil || len(m.Tags) != 1 {
		t.Fatalf("memo %v %+v", err, m)
	}
	if m, err = e.svc.ToggleMemoCheckbox(ctx, m.ID, 0); err != nil || !strings.Contains(m.ContentMD, "- [x] drink") {
		t.Fatalf("toggle %v %+v", err, m)
	}
	hits, err := e.svc.Search(ctx, `slept "well`, 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("search %v %d", err, len(hits))
	}
	due := at("2026-03-02 10:00")
	if _, err := e.svc.CreateTask(ctx, TaskInput{Title: "Gym", DueAt: &due, Repeat: &RepeatInput{Freq: "weekly", Weekdays: []int{1, 3}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateReminder(ctx, ReminderInput{Message: "Vitamins", FireAt: at("2026-03-01 00:00"), Repeat: daily("09:00")}); err != nil {
		t.Fatal(err)
	}
	cal, err := e.svc.Calendar(ctx, at("2026-03-01 00:00"), at("2026-03-15 00:00"))
	if err != nil {
		t.Fatal(err)
	}
	var gym, vit int
	for _, it := range cal.Items {
		switch it.Title {
		case "Gym":
			gym++
		case "Vitamins":
			vit++
		}
	}
	// Gym: Mon 2, Wed 4, Mon 9, Wed 11. Vitamins daily 1..14.
	if gym != 4 || vit != 14 || cal.MemoDays["2026-03-01"] != 1 {
		t.Fatalf("calendar gym=%d vit=%d memodays=%v", gym, vit, cal.MemoDays)
	}

	files, err := e.svc.BuildExport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	memo := string(files["memos/2026-03-01.md"])
	if !strings.Contains(memo, "## 07:00") || !strings.Contains(memo, "#health") {
		t.Fatalf("memo export: %s", memo)
	}
	if !strings.Contains(string(files["tasks.md"]), "- [ ] Gym") {
		t.Fatalf("tasks export: %s", files["tasks.md"])
	}
	if n, err := e.svc.ExportToDir(ctx); err != nil || n < 2 {
		t.Fatalf("export dir: %v %d", err, n)
	}
	if _, err := os.Stat(filepath.Join(e.svc.exportDir, "tasks.md")); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := e.svc.ExportZip(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil || len(zr.File) != len(files) {
		t.Fatalf("zip: %v", err)
	}
}
