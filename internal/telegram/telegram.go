// Package telegram implements the Telegram bot: an outbox worker that delivers
// reminder messages with retries, and a long-polling loop that handles button
// presses and expense messages from the configured chat only.
package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/service"
)

// Client is the subset of the Telegram API used (mocked in tests).
type Client interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
	Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error)
	GetUpdates(cfg tgbotapi.UpdateConfig) ([]tgbotapi.Update, error)
}

// Factory creates a Client for a token (replaced in tests).
type Factory func(token string) (Client, error)

// DefaultFactory connects to the real Bot API.
func DefaultFactory(token string) (Client, error) {
	api, err := tgbotapi.NewBotAPIWithClient(token, tgbotapi.APIEndpoint, &http.Client{Timeout: 70 * time.Second})
	if err != nil {
		return nil, err
	}
	return api, nil
}

// Actions an occurrence ID can carry in callback data.
var actionCodes = map[string]string{
	"d":   service.ActionDone,
	"s10": service.ActionSnooze10,
	"s60": service.ActionSnooze60,
	"k":   service.ActionSkip,
}

var callbackRe = regexp.MustCompile(`^o:([1-9][0-9]{0,17}):(d|s10|s60|k)$`)

type jobKind int

const (
	jobSend jobKind = iota
	jobEdit
)

type job struct {
	kind      jobKind
	occID     int64
	chatID    int64
	messageID int
	text      string
	allowSkip bool
}

// Bot is the Telegram integration. It implements service.Notifier.
type Bot struct {
	svc     *service.Service
	factory Factory
	queue   chan job

	mu      sync.RWMutex
	client  Client
	token   string
	chatID  int64
	cancel  context.CancelFunc
	baseCtx context.Context
	drafts  map[int64]*draft // open guided expense entries per chat

	retryBase time.Duration
}

// New creates the bot; call Run to start the worker.
func New(svc *service.Service, f Factory) *Bot {
	if f == nil {
		f = DefaultFactory
	}
	return &Bot{svc: svc, factory: f, queue: make(chan job, 1000), drafts: map[int64]*draft{}, retryBase: time.Second}
}

var _ service.Notifier = (*Bot)(nil)

// Run starts the outbox worker and blocks until ctx is done.
func (b *Bot) Run(ctx context.Context) {
	b.mu.Lock()
	b.baseCtx = ctx
	b.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			b.mu.Lock()
			if b.cancel != nil {
				b.cancel()
			}
			b.mu.Unlock()
			// The poller may sit in a 50s long-poll; it exits on its own once
			// the request returns, so shutdown does not wait for it.
			return
		case j := <-b.queue:
			b.process(ctx, j)
		}
	}
}

// Configure (re)connects with new settings. An empty token disables the bot.
func (b *Bot) Configure(token string, chatID int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cancel != nil {
		b.cancel()
		b.cancel = nil
	}
	b.client, b.token, b.chatID = nil, token, chatID
	if token == "" || chatID == 0 {
		slog.Info("telegram disabled (token or chat id not set)")
		return
	}
	base := b.baseCtx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)
	b.cancel = cancel
	go func() {
		b.connectAndPoll(ctx, token)
	}()
}

func (b *Bot) connectAndPoll(ctx context.Context, token string) {
	delay := b.retryBase
	var c Client
	for {
		var err error
		if c, err = b.factory(token); err == nil {
			break
		}
		slog.Error("telegram connect failed", "err", b.redact(err), "retry_in", delay.String())
		if !sleep(ctx, delay) {
			return
		}
		delay = min(delay*2, 5*time.Minute)
	}
	b.mu.Lock()
	if ctx.Err() != nil {
		b.mu.Unlock()
		return
	}
	b.client = c
	b.mu.Unlock()
	slog.Info("telegram connected")
	// The "/" menu in the chat (best effort).
	if _, err := c.Request(tgbotapi.NewSetMyCommands(
		tgbotapi.BotCommand{Command: "add", Description: "Add an expense step by step"},
		tgbotapi.BotCommand{Command: "today", Description: "Spent today"},
		tgbotapi.BotCommand{Command: "week", Description: "Spent this week"},
		tgbotapi.BotCommand{Command: "month", Description: "Spent this month"},
		tgbotapi.BotCommand{Command: "skip", Description: "Skip category or description"},
		tgbotapi.BotCommand{Command: "cancel", Description: "Stop the current entry"},
		tgbotapi.BotCommand{Command: "help", Description: "How to log an expense"},
	)); err != nil {
		slog.Warn("telegram set commands failed", "err", b.redact(err))
	}
	b.poll(ctx, c)
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// poll long-polls updates (no webhooks, the server is private).
func (b *Bot) poll(ctx context.Context, c Client) {
	cfg := tgbotapi.NewUpdate(0)
	cfg.Timeout = 50
	cfg.AllowedUpdates = []string{"message", "callback_query"}
	delay := b.retryBase
	for ctx.Err() == nil {
		updates, err := c.GetUpdates(cfg)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if strings.Contains(err.Error(), "Conflict") {
				// Telegram gives each update to only one poller: messages get split between copies.
				slog.Error("telegram: another copy of this bot is running with the same token; stop it, or messages will be lost")
			} else {
				slog.Warn("telegram getUpdates failed", "err", b.redact(err))
			}
			if !sleep(ctx, delay) {
				return
			}
			delay = min(delay*2, time.Minute)
			continue
		}
		delay = b.retryBase
		for _, u := range updates {
			if u.UpdateID >= cfg.Offset {
				cfg.Offset = u.UpdateID + 1
			}
			b.HandleUpdate(ctx, u)
		}
	}
}

func (b *Bot) current() (Client, int64) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.client, b.chatID
}

// Enabled implements service.Notifier.
func (b *Bot) Enabled() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.token != "" && b.chatID != 0
}

// Deliver implements service.Notifier (non-blocking).
func (b *Bot) Deliver(m service.Message) {
	if !b.Enabled() {
		return
	}
	b.enqueue(job{kind: jobSend, occID: m.OccurrenceID, text: m.Text, allowSkip: m.AllowSkip})
}

// Finalize implements service.Notifier (non-blocking).
func (b *Bot) Finalize(chatID int64, messageID int, text string) {
	if chatID == 0 || messageID == 0 {
		return
	}
	b.enqueue(job{kind: jobEdit, chatID: chatID, messageID: messageID, text: text})
}

func (b *Bot) enqueue(j job) {
	select {
	case b.queue <- j:
	default:
		slog.Error("telegram outbox full, dropping message", "occurrence", j.occID)
	}
}

func keyboard(occID int64, allowSkip bool) tgbotapi.InlineKeyboardMarkup {
	cb := func(code string) string { return fmt.Sprintf("o:%d:%s", occID, code) }
	row := []tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardButtonData("✅ Done", cb("d")),
		tgbotapi.NewInlineKeyboardButtonData("⏰ 10m", cb("s10")),
		tgbotapi.NewInlineKeyboardButtonData("⏰ 1h", cb("s60")),
	}
	if allowSkip {
		row = append(row, tgbotapi.NewInlineKeyboardButtonData("⏭ Skip", cb("k")))
	}
	return tgbotapi.NewInlineKeyboardMarkup(row)
}

// process delivers one job with retries and exponential backoff, honouring
// Telegram's retry_after on rate limits. It runs on the worker goroutine only,
// so the scheduler never waits on Telegram.
func (b *Bot) process(ctx context.Context, j job) {
	delay := b.retryBase
	for attempt := 1; attempt <= 6; attempt++ {
		c, chatID := b.current()
		if c == nil {
			if !b.Enabled() {
				return // disabled: drop
			}
			// Still connecting: wait.
			if !sleep(ctx, delay) {
				return
			}
			delay = min(delay*2, time.Minute)
			continue
		}
		var err error
		switch j.kind {
		case jobSend:
			msg := tgbotapi.NewMessage(chatID, truncate(j.text))
			msg.ReplyMarkup = keyboard(j.occID, j.allowSkip)
			var sent tgbotapi.Message
			if sent, err = c.Send(msg); err == nil {
				b.svc.RecordTelegramMessage(ctx, j.occID, chatID, sent.MessageID)
				return
			}
		case jobEdit:
			edit := tgbotapi.NewEditMessageText(j.chatID, j.messageID, truncate(j.text))
			edit.ReplyMarkup = &tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
			if _, err = c.Request(edit); err == nil || isPermanentEditError(err) {
				return
			}
		}
		wait := delay
		var apiErr *tgbotapi.Error
		if errors.As(err, &apiErr) {
			if apiErr.RetryAfter > 0 {
				wait = time.Duration(apiErr.RetryAfter) * time.Second
			} else if apiErr.Code >= 400 && apiErr.Code < 500 && apiErr.Code != 429 {
				slog.Error("telegram request rejected", "err", b.redact(err), "occurrence", j.occID)
				return
			}
		}
		slog.Warn("telegram request failed, retrying", "err", b.redact(err), "attempt", attempt, "retry_in", wait.String())
		if !sleep(ctx, wait) {
			return
		}
		delay = min(delay*2, 5*time.Minute)
	}
	slog.Error("telegram request failed permanently", "occurrence", j.occID)
}

func isPermanentEditError(err error) bool {
	s := err.Error()
	return strings.Contains(s, "message is not modified") || strings.Contains(s, "message to edit not found") ||
		strings.Contains(s, "message can't be edited")
}

func truncate(s string) string {
	if r := []rune(s); len(r) > 4000 {
		return string(r[:3990]) + "…"
	}
	return s
}

// HandleUpdate processes one update from the configured chat: button presses,
// and text messages (expenses and commands). Other chats are ignored silently.
func (b *Bot) HandleUpdate(ctx context.Context, u tgbotapi.Update) {
	c, chatID := b.current()
	if c == nil {
		return
	}
	if cq := u.CallbackQuery; cq != nil {
		if cq.Message == nil || cq.Message.Chat == nil || cq.Message.Chat.ID != chatID {
			return // not our chat: ignore silently
		}
		b.handleCallback(ctx, c, cq)
		return
	}
	if m := u.Message; m != nil && m.Chat != nil && m.Chat.ID == chatID {
		b.handleMessage(ctx, c, chatID, m)
	}
}

func (b *Bot) handleCallback(ctx context.Context, c Client, cq *tgbotapi.CallbackQuery) {
	answer := func(text string) {
		if _, err := c.Request(tgbotapi.NewCallback(cq.ID, text)); err != nil {
			slog.Warn("telegram answer callback failed", "err", b.redact(err))
		}
	}
	switch {
	case strings.HasPrefix(cq.Data, "e:"):
		b.handleExpenseCallback(ctx, c, cq, answer)
		return
	case strings.HasPrefix(cq.Data, "a:"):
		b.handleFlowCallback(ctx, c, cq, answer)
		return
	}
	m := callbackRe.FindStringSubmatch(cq.Data)
	if m == nil {
		answer("Unknown action")
		return
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		answer("Unknown action")
		return
	}
	action := actionCodes[m[2]]
	o, err := b.svc.OccurrenceAction(ctx, id, action, model.SourceTelegram)
	switch {
	case err == nil:
		answer(map[string]string{service.ActionDone: "✅ Done", service.ActionSkip: "⏭ Skipped",
			service.ActionSnooze10: "⏰ Snoozed 10 minutes", service.ActionSnooze60: "⏰ Snoozed 1 hour"}[action])
		if o, err = b.svc.GetOccurrence(ctx, id); err != nil {
			return
		}
	case errors.Is(err, model.ErrConflict):
		answer("Already handled")
	case errors.Is(err, model.ErrNotFound):
		answer("This reminder no longer exists")
		return
	case errors.Is(err, model.ErrValidation):
		answer(err.Error())
		return
	default:
		slog.Error("telegram action failed", "err", err, "occurrence", id)
		answer("Something went wrong, try again")
		return
	}
	// The service already edits the message it recorded; edit the pressed one if different.
	if o != nil && cq.Message.MessageID != o.TelegramMessageID {
		b.Finalize(cq.Message.Chat.ID, cq.Message.MessageID, b.svc.ResultText(ctx, o))
	}
}

// SendTest sends a test message synchronously (Settings page button).
func (b *Bot) SendTest(ctx context.Context) error {
	c, chatID := b.current()
	if !b.Enabled() {
		return errors.New("telegram is not configured (set the bot token and chat id)")
	}
	if c == nil {
		return errors.New("telegram is still connecting or the token is invalid, check the logs")
	}
	_, err := c.Send(tgbotapi.NewMessage(chatID, "✅ Test message from Spider Notes 🕷️. Reminders will arrive here at "+time.Now().In(b.svc.Location()).Format("15:04 MST")+"."))
	if err != nil {
		return errors.New(b.redact(err))
	}
	return nil
}

// redact removes the bot token from error text (URLs contain it).
func (b *Bot) redact(err error) string {
	if err == nil {
		return ""
	}
	b.mu.RLock()
	tok := b.token
	b.mu.RUnlock()
	s := err.Error()
	if tok != "" {
		s = strings.ReplaceAll(s, tok, "<redacted>")
	}
	return s
}

// redactingLogger keeps the library's own log output free of tokens.
type redactingLogger struct{ b *Bot }

func (l redactingLogger) Println(v ...any) {
	slog.Debug("telegram-bot-api", "msg", l.b.redact(errors.New(fmt.Sprint(v...))))
}

func (l redactingLogger) Printf(format string, v ...any) {
	slog.Debug("telegram-bot-api", "msg", l.b.redact(fmt.Errorf(format, v...)))
}

// InstallLogger routes the library logger through slog with redaction.
func (b *Bot) InstallLogger() { _ = tgbotapi.SetLogger(redactingLogger{b}) }
