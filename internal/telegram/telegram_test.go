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

func TestMessagesIgnoredAndRepliedOnce(t *testing.T) {
	_, bot, mc, ctx := setup(t)
	for i := 0; i < 3; i++ {
		bot.HandleUpdate(ctx, tgbotapi.Update{Message: &tgbotapi.Message{Text: "/start", Chat: &tgbotapi.Chat{ID: ownerChat}}})
		bot.HandleUpdate(ctx, tgbotapi.Update{Message: &tgbotapi.Message{Text: "hi", Chat: &tgbotapi.Chat{ID: 777}}})
	}
	n, _, _ := mc.snapshot()
	if n != 1 || mc.sent[0].Text != replyText || mc.sent[0].ChatID != ownerChat {
		t.Fatalf("want exactly one reply to owner, got %d", n)
	}
}

func TestRedact(t *testing.T) {
	b := New(nil, nil)
	b.token = "123:secret"
	if s := b.redact(errors.New(`Post "https://api.telegram.org/bot123:secret/sendMessage": timeout`)); strings.Contains(s, "secret") {
		t.Fatalf("token leaked: %s", s)
	}
}
