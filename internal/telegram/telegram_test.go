package telegram

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/service"
	"github.com/sovannroth/mynotes/internal/store/sqlite"
)

const ownerChat = 42

type mockClient struct {
	mu        sync.Mutex
	sent      []tgbotapi.MessageConfig
	edits     []tgbotapi.EditMessageTextConfig
	answers   []tgbotapi.CallbackConfig
	failSends int
	nextID    int
}

func (m *mockClient) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failSends > 0 {
		m.failSends--
		return tgbotapi.Message{}, &tgbotapi.Error{Code: 429, Message: "Too Many Requests", ResponseParameters: tgbotapi.ResponseParameters{RetryAfter: 0}}
	}
	msg := c.(tgbotapi.MessageConfig)
	m.sent = append(m.sent, msg)
	m.nextID++
	return tgbotapi.Message{MessageID: m.nextID, Chat: &tgbotapi.Chat{ID: msg.ChatID}}, nil
}

func (m *mockClient) Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch v := c.(type) {
	case tgbotapi.EditMessageTextConfig:
		m.edits = append(m.edits, v)
	case tgbotapi.CallbackConfig:
		m.answers = append(m.answers, v)
	}
	return &tgbotapi.APIResponse{Ok: true}, nil
}

func (m *mockClient) GetUpdates(tgbotapi.UpdateConfig) ([]tgbotapi.Update, error) {
	time.Sleep(10 * time.Millisecond)
	return nil, nil
}

func (m *mockClient) snapshot() (int, int, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent), len(m.edits), len(m.answers)
}

func setup(t *testing.T) (*service.Service, *Bot, *mockClient, context.Context) {
	t.Helper()
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	svc := service.New(service.Options{Store: db, Location: time.UTC})
	mc := &mockClient{}
	bot := New(svc, func(string) (Client, error) { return mc, nil })
	bot.retryBase = time.Millisecond
	svc.SetNotifier(bot)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go bot.Run(ctx)
	bot.Configure("123:abc", ownerChat)
	waitFor(t, func() bool { c, _ := bot.current(); return c != nil })
	return svc, bot, mc, ctx
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met")
}

func TestDeliverAndDoneButton(t *testing.T) {
	svc, bot, mc, ctx := setup(t)
	mc.failSends = 2 // rate limited twice, then succeeds
	r, err := svc.CreateReminder(ctx, service.ReminderInput{Message: "Stand up", FireAt: time.Now().Add(-30 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ProcessDue(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { n, _, _ := mc.snapshot(); return n == 1 })
	msg := mc.sent[0]
	kb := msg.ReplyMarkup.(tgbotapi.InlineKeyboardMarkup)
	if len(kb.InlineKeyboard[0]) != 3 {
		t.Fatalf("one-time reminder should have 3 buttons (no Skip), got %d", len(kb.InlineKeyboard[0]))
	}
	data := *kb.InlineKeyboard[0][0].CallbackData
	if len(data) > 64 || !strings.HasPrefix(data, "o:") {
		t.Fatalf("bad callback data %q", data)
	}
	occs, _ := svc.ListOccurrences(ctx, model.OccurrenceFilter{ReminderID: r.ID})
	waitFor(t, func() bool { o, _ := svc.GetOccurrence(ctx, occs[0].ID); return o.TelegramMessageID == 1 })

	// A stranger pressing the button is ignored.
	bot.HandleUpdate(ctx, tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{ID: "x", Data: data,
		Message: &tgbotapi.Message{MessageID: 1, Chat: &tgbotapi.Chat{ID: 999}}}})
	if o, _ := svc.GetOccurrence(ctx, occs[0].ID); o.Status != model.OccSent {
		t.Fatal("other chat must not change state")
	}
	// Owner presses Done.
	bot.HandleUpdate(ctx, tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{ID: "y", Data: data,
		Message: &tgbotapi.Message{MessageID: 1, Chat: &tgbotapi.Chat{ID: ownerChat}}}})
	o, _ := svc.GetOccurrence(ctx, occs[0].ID)
	if o.Status != model.OccDone || o.ActionSource != model.SourceTelegram {
		t.Fatalf("occurrence %+v", o)
	}
	waitFor(t, func() bool { _, e, a := mc.snapshot(); return e >= 1 && a >= 1 })
	mc.mu.Lock()
	edit := mc.edits[0]
	mc.mu.Unlock()
	if !strings.Contains(edit.Text, "✅ Done at") || len(edit.ReplyMarkup.InlineKeyboard) != 0 {
		t.Fatalf("message not updated: %+v", edit)
	}
	// Tampered callback data is rejected.
	bot.HandleUpdate(ctx, tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{ID: "z", Data: "o:1:drop table",
		Message: &tgbotapi.Message{MessageID: 1, Chat: &tgbotapi.Chat{ID: ownerChat}}}})
	mc.mu.Lock()
	last := mc.answers[len(mc.answers)-1].Text
	mc.mu.Unlock()
	if last != "Unknown action" {
		t.Fatalf("answer %q", last)
	}
}

func ownerMsg(text string) tgbotapi.Update {
	return tgbotapi.Update{Message: &tgbotapi.Message{Text: text, Chat: &tgbotapi.Chat{ID: ownerChat}}}
}

func press(data string, chat int64, msgID int) tgbotapi.Update {
	return tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{ID: "cb", Data: data,
		Message: &tgbotapi.Message{MessageID: msgID, Chat: &tgbotapi.Chat{ID: chat}}}}
}

func (m *mockClient) lastSent() tgbotapi.MessageConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sent[len(m.sent)-1]
}

func (m *mockClient) lastEdit() tgbotapi.EditMessageTextConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.edits[len(m.edits)-1]
}

func (m *mockClient) lastAnswer() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.answers[len(m.answers)-1].Text
}

func findButton(t *testing.T, kb tgbotapi.InlineKeyboardMarkup, label string) string {
	t.Helper()
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			if strings.Contains(btn.Text, label) {
				if len(*btn.CallbackData) > 64 {
					t.Fatalf("callback data too long: %q", *btn.CallbackData)
				}
				return *btn.CallbackData
			}
		}
	}
	t.Fatalf("no button %q", label)
	return ""
}

func TestExpensesFromChat(t *testing.T) {
	svc, bot, mc, ctx := setup(t)

	// Other chats are ignored completely.
	bot.HandleUpdate(ctx, tgbotapi.Update{Message: &tgbotapi.Message{Text: "lunch 4.5", Chat: &tgbotapi.Chat{ID: 777}}})
	if n, _, _ := mc.snapshot(); n != 0 {
		t.Fatal("stranger got a reply")
	}
	if list, _ := svc.ListExpenses(ctx, model.ExpenseFilter{}); len(list) != 0 {
		t.Fatal("stranger created an expense")
	}

	bot.HandleUpdate(ctx, ownerMsg("/start"))
	if !strings.Contains(mc.lastSent().Text, "lunch 4.5") {
		t.Fatalf("help: %q", mc.lastSent().Text)
	}
	bot.HandleUpdate(ctx, ownerMsg("hello there"))
	if !strings.Contains(mc.lastSent().Text, "couldn't find an amount") {
		t.Fatalf("no amount: %q", mc.lastSent().Text)
	}

	// An expense without a category: saved, with category buttons.
	bot.HandleUpdate(ctx, ownerMsg("lunch 4.5"))
	reply := mc.lastSent()
	list, _ := svc.ListExpenses(ctx, model.ExpenseFilter{})
	if len(list) != 1 || list[0].Item != "Lunch" || list[0].AmountMinor != 450 || list[0].Currency != "USD" {
		t.Fatalf("expense %+v", list)
	}
	if !strings.Contains(reply.Text, "✅ Saved") || !strings.Contains(reply.Text, "$4.50") || !strings.Contains(reply.Text, "Pick a category") {
		t.Fatalf("reply %q", reply.Text)
	}
	kb := reply.ReplyMarkup.(tgbotapi.InlineKeyboardMarkup)
	bot.HandleUpdate(ctx, press(findButton(t, kb, "Food & Drinks"), ownerChat, 10))
	if e, _ := svc.GetExpense(ctx, list[0].ID); e.Category != "Food & Drinks" {
		t.Fatalf("category not set: %q", e.Category)
	}
	if ed := mc.lastEdit(); !strings.Contains(ed.Text, "🍜 Food & Drinks") || ed.MessageID != 10 {
		t.Fatalf("edit %q", ed.Text)
	}
	// $4.50 cannot become riel.
	bot.HandleUpdate(ctx, press(findButton(t, kb, "Riel"), ownerChat, 10))
	if !strings.Contains(mc.lastAnswer(), "Can't switch") {
		t.Fatalf("answer %q", mc.lastAnswer())
	}

	// Riel with #category: matched to the preset; switch to dollars; undo.
	bot.HandleUpdate(ctx, ownerMsg("tuk tuk 8000r #transport @cash"))
	reply = mc.lastSent()
	list, _ = svc.ListExpenses(ctx, model.ExpenseFilter{})
	tuk := list[0]
	if tuk.Currency != "KHR" || tuk.AmountMinor != 8000 || tuk.Category != "Transport" || tuk.Payment != "cash" {
		t.Fatalf("tuk %+v", tuk)
	}
	kb = reply.ReplyMarkup.(tgbotapi.InlineKeyboardMarkup)
	if strings.Contains(reply.Text, "Pick a category") {
		t.Fatal("category given: no picker expected")
	}
	bot.HandleUpdate(ctx, press(findButton(t, kb, "Dollar"), ownerChat, 11))
	if e, _ := svc.GetExpense(ctx, tuk.ID); e.Currency != "USD" || e.AmountMinor != 800000 {
		t.Fatalf("switch: %+v", e)
	}
	// Next message without a currency uses the last one (USD now).
	bot.HandleUpdate(ctx, ownerMsg("water 1"))
	list, _ = svc.ListExpenses(ctx, model.ExpenseFilter{})
	if list[0].Currency != "USD" {
		t.Fatalf("default currency %s", list[0].Currency)
	}

	// A stranger pressing an expense button changes nothing.
	undo := findButton(t, kb, "Undo")
	bot.HandleUpdate(ctx, press(undo, 777, 11))
	if _, err := svc.GetExpense(ctx, tuk.ID); err != nil {
		t.Fatal("stranger deleted an expense")
	}
	bot.HandleUpdate(ctx, press(undo, ownerChat, 11))
	if _, err := svc.GetExpense(ctx, tuk.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatal("undo should delete")
	}
	if !strings.Contains(mc.lastEdit().Text, "Deleted") || len(mc.lastEdit().ReplyMarkup.InlineKeyboard) != 0 {
		t.Fatalf("undo edit %+v", mc.lastEdit())
	}
	bot.HandleUpdate(ctx, press(undo, ownerChat, 11)) // again
	if mc.lastAnswer() != "This expense no longer exists" {
		t.Fatalf("second undo: %q", mc.lastAnswer())
	}
	bot.HandleUpdate(ctx, press("e:1:c1;drop", ownerChat, 11))
	if mc.lastAnswer() != "Unknown action" {
		t.Fatalf("tampered: %q", mc.lastAnswer())
	}

	bot.HandleUpdate(ctx, ownerMsg("/today"))
	if txt := mc.lastSent().Text; !strings.Contains(txt, "$5.50") || !strings.Contains(txt, "2 expenses") {
		t.Fatalf("today: %q", txt)
	}
	bot.HandleUpdate(ctx, ownerMsg("/month@SpiderBot"))
	if !strings.Contains(mc.lastSent().Text, "This month") {
		t.Fatalf("month: %q", mc.lastSent().Text)
	}
}

func TestRedact(t *testing.T) {
	b := New(nil, nil)
	b.token = "123:secret"
	if s := b.redact(errors.New(`Post "https://api.telegram.org/bot123:secret/sendMessage": timeout`)); strings.Contains(s, "secret") {
		t.Fatalf("token leaked: %s", s)
	}
}
