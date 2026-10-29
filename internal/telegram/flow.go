package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/service"
)

// Main keyboard (always under the chat's text box). Tapping a button sends its text.
const (
	btnAdd   = "➕ Add expense"
	btnToday = "📊 Today"
	btnMonth = "📅 Month"
)

func mainKeyboard() tgbotapi.ReplyKeyboardMarkup {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton(btnAdd)),
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton(btnToday), tgbotapi.NewKeyboardButton(btnMonth)),
	)
	kb.ResizeKeyboard = true
	kb.InputFieldPlaceholder = "lunch 4.5 · or tap ➕ Add expense"
	return kb
}

// Guided entry: what → how much → category.
type step int

const (
	stepItem step = iota
	stepAmount
	stepCategory
)

// draftTTL is how long an unfinished guided entry waits for the next answer.
const draftTTL = 15 * time.Minute

type draft struct {
	step             step
	item             string
	amount, currency string
	updated          time.Time
}

// Buttons of the guided entry: cancel, skip (no category) or c<id>.
var flowCallbackRe = regexp.MustCompile(`^a:(cancel|skip|c[1-9][0-9]{0,9})$`)

var cancelRow = tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("✖ Cancel", "a:cancel"))

func (b *Bot) getDraft(chatID int64) *draft {
	b.mu.Lock()
	defer b.mu.Unlock()
	d := b.drafts[chatID]
	if d != nil && time.Since(d.updated) > draftTTL {
		delete(b.drafts, chatID)
		return nil
	}
	return d
}

func (b *Bot) setDraft(chatID int64, d *draft) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if d == nil {
		delete(b.drafts, chatID)
		return
	}
	d.updated = time.Now()
	b.drafts[chatID] = d
}

// sendMain sends a plain message that also (re)shows the main keyboard.
func (b *Bot) sendMain(c Client, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, truncate(text))
	msg.ReplyMarkup = mainKeyboard()
	if _, err := c.Send(msg); err != nil {
		slog.Warn("telegram send failed", "err", b.redact(err))
	}
}

func (b *Bot) startFlow(c Client, chatID int64) {
	b.setDraft(chatID, &draft{step: stepItem})
	kb := tgbotapi.NewInlineKeyboardMarkup(cancelRow)
	b.send(c, chatID, "🧾 What did you spend on?", &kb)
}

// continueFlow handles a text answer while a guided entry is open. It reports
// false when there is no open entry.
func (b *Bot) continueFlow(ctx context.Context, c Client, chatID int64, text string) bool {
	d := b.getDraft(chatID)
	if d == nil {
		return false
	}
	switch d.step {
	case stepItem:
		if len([]rune(text)) > service.MaxExpenseItemLen {
			b.send(c, chatID, "That's too long. What did you spend on? (a few words)", nil)
			return true
		}
		d.item, d.step = text, stepAmount
		b.setDraft(chatID, d)
		kb := tgbotapi.NewInlineKeyboardMarkup(cancelRow)
		b.send(c, chatID, "💵 How much for "+text+"?\ne.g. 4.5 · $4.50 · 8000r (without a mark: "+b.svc.LastCurrency(ctx)+")", &kb)
	case stepAmount:
		amount, cur, err := service.ParseQuickAmount(text)
		if cur == "" {
			cur = b.svc.LastCurrency(ctx)
		}
		if err == nil {
			_, err = service.ParseAmount(amount, cur)
		}
		if err != nil {
			kb := tgbotapi.NewInlineKeyboardMarkup(cancelRow)
			b.send(c, chatID, "🤔 "+err.Error()+"\n💵 How much? e.g. 4.5 or 8000r", &kb)
			return true
		}
		d.amount, d.currency, d.step = amount, cur, stepCategory
		b.setDraft(chatID, d)
		cats, _ := b.svc.ListExpenseCategories(ctx)
		kb := categoryPickKeyboard(cats)
		b.send(c, chatID, "🏷 Category? Tap one, or type a name.", &kb)
	case stepCategory:
		cats, _ := b.svc.ListExpenseCategories(ctx)
		b.finishFlow(ctx, c, chatID, 0, service.MatchCategory(cats, text))
	}
	return true
}

func categoryPickKeyboard(cats []model.ExpenseCategory) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton
	var row []tgbotapi.InlineKeyboardButton
	for i, c := range cats {
		if i == maxCategoryButtons {
			break
		}
		row = append(row, tgbotapi.NewInlineKeyboardButtonData(strings.TrimSpace(c.Emoji+" "+c.Name), "a:c"+strconv.FormatInt(c.ID, 10)))
		if len(row) == 2 {
			rows, row = append(rows, row), nil
		}
	}
	if row != nil {
		rows = append(rows, row)
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("⏭ No category", "a:skip"),
		tgbotapi.NewInlineKeyboardButtonData("✖ Cancel", "a:cancel")))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// finishFlow saves the drafted expense. editMsg > 0 turns that message (the
// category question) into the confirmation; otherwise a new message is sent.
func (b *Bot) finishFlow(ctx context.Context, c Client, chatID int64, editMsg int, category string) {
	d := b.getDraft(chatID)
	if d == nil || d.step != stepCategory {
		return
	}
	e, err := b.svc.CreateExpense(ctx, service.ExpenseInput{Item: d.item, Amount: json.Number(d.amount), Currency: d.currency, Category: category})
	if err != nil {
		var ve *model.ValidationError
		if errors.As(err, &ve) {
			b.setDraft(chatID, nil)
			b.sendMain(c, chatID, "🤔 "+ve.Msg+"\nTap ➕ Add expense to try again.")
			return
		}
		slog.Error("telegram expense", "err", err)
		b.sendMain(c, chatID, "Something went wrong, try again.")
		return
	}
	b.setDraft(chatID, nil)
	cats, _ := b.svc.ListExpenseCategories(ctx)
	kb := expenseKeyboard(e, cats, false)
	text := expenseText(b.svc, e, cats, "✅ Saved")
	if editMsg > 0 {
		b.edit(c, chatID, editMsg, text, &kb)
	} else {
		b.send(c, chatID, text, &kb)
	}
}

// handleFlowCallback handles Cancel, No category and category buttons of a guided entry.
func (b *Bot) handleFlowCallback(ctx context.Context, c Client, cq *tgbotapi.CallbackQuery, answer func(string)) {
	m := flowCallbackRe.FindStringSubmatch(cq.Data)
	if m == nil {
		answer("Unknown action")
		return
	}
	chatID, msgID := cq.Message.Chat.ID, cq.Message.MessageID
	d := b.getDraft(chatID)
	if d == nil {
		answer("This entry has expired")
		b.edit(c, chatID, msgID, "⌛ Expired. Tap ➕ Add expense to start again.", nil)
		return
	}
	switch code := m[1]; {
	case code == "cancel":
		b.setDraft(chatID, nil)
		answer("Cancelled")
		b.edit(c, chatID, msgID, "✖ Cancelled.", nil)
	case d.step != stepCategory:
		answer("Answer the question above first")
	case code == "skip":
		answer("")
		b.finishFlow(ctx, c, chatID, msgID, "")
	default:
		id, _ := strconv.ParseInt(code[1:], 10, 64)
		cat, err := b.svc.ListExpenseCategories(ctx)
		name := ""
		if err == nil {
			for _, x := range cat {
				if x.ID == id {
					name = x.Name
				}
			}
		}
		if name == "" {
			answer("That category no longer exists")
			return
		}
		answer("")
		b.finishFlow(ctx, c, chatID, msgID, name)
	}
}
