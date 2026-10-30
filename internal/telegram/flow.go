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
	kb.InputFieldPlaceholder = "Tap ➕ Add expense"
	return kb
}

// Guided entry: what → price (dollar or riel) → category → description.
type step int

const (
	stepItem step = iota
	stepAmount
	stepCategory
	stepNote
)

// draftTTL is how long an unfinished guided entry waits for the next answer.
const draftTTL = 15 * time.Minute

type draft struct {
	step     step
	item     string
	amount   string
	currency string // chosen with the Dollar / Riel buttons
	category string
	group    bool // a group chat: typed answers must be replies (bot privacy mode)
	updated  time.Time
}

// Buttons of the guided entry.
//
//	cancel     stop the entry
//	cur:USD    price in dollars / cur:KHR price in riel
//	c<id>      category / nocat: no category
//	nonote     save without a description
var flowCallbackRe = regexp.MustCompile(`^a:(cancel|cur:(?:USD|KHR)|nocat|nonote|c[1-9][0-9]{0,9})$`)

func cancelButton() tgbotapi.InlineKeyboardButton {
	return tgbotapi.NewInlineKeyboardButtonData("✖ Cancel", "a:cancel")
}

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

// ---- prompts ----

func (b *Bot) startFlow(ctx context.Context, c Client, chatID int64, group bool) {
	cur := b.svc.LastCurrency(ctx)
	if cur != "KHR" {
		cur = "USD"
	}
	b.setDraft(chatID, &draft{step: stepItem, currency: cur, group: group})
	if group {
		b.askReply(c, chatID, "➕ New expense\n\n1/4 🧾 What did you spend on?\n(/cancel to stop)", "What did you spend on?")
		return
	}
	kb := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(cancelButton()))
	b.send(c, chatID, "➕ New expense\n\n1/4 🧾 What did you spend on?", &kb)
}

// askReply sends a question that opens a reply to itself. In groups, bots in
// privacy mode only receive replies to their own messages, so typed answers
// must be replies.
func (b *Bot) askReply(c Client, chatID int64, text, placeholder string) {
	msg := tgbotapi.NewMessage(chatID, truncate(text))
	msg.ReplyMarkup = tgbotapi.ForceReply{ForceReply: true, InputFieldPlaceholder: placeholder}
	if _, err := c.Send(msg); err != nil {
		slog.Warn("telegram send failed", "err", b.redact(err))
	}
}

// skipStep answers /skip: no category, or no description.
func (b *Bot) skipStep(ctx context.Context, c Client, chatID int64) {
	d := b.getDraft(chatID)
	switch {
	case d == nil:
		b.sendMain(c, chatID, "Nothing to skip. Tap ➕ Add expense to start.")
	case d.step == stepCategory:
		cats, _ := b.svc.ListExpenseCategories(ctx)
		b.toNote(c, chatID, 0, d, "", cats)
	case d.step == stepNote:
		b.finishFlow(ctx, c, chatID, 0, "")
	default:
		b.send(c, chatID, "This question can't be skipped.", nil)
	}
}

func amountPrompt(d *draft) (string, tgbotapi.InlineKeyboardMarkup) {
	usd, khr := "💵 Dollar", "៛ Riel"
	example := "e.g. 4.5"
	if d.currency == "KHR" {
		khr = "✅ " + khr
		example = "e.g. 8000"
	} else {
		usd = "✅ " + usd
	}
	text := "2/4 💵 How much for " + d.item + "?\n" +
		"Tap Dollar or Riel, then type the price (" + example + ")."
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(usd, "a:cur:USD"),
			tgbotapi.NewInlineKeyboardButtonData(khr, "a:cur:KHR")),
		tgbotapi.NewInlineKeyboardRow(cancelButton()))
	return text, kb
}

func categoryPrompt(d *draft, cats []model.ExpenseCategory) (string, tgbotapi.InlineKeyboardMarkup) {
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
		tgbotapi.NewInlineKeyboardButtonData("⏭ No category", "a:nocat"), cancelButton()))
	how := "Tap one, or type a new name."
	if d.group {
		how = "Tap one, or reply to this message with a new name."
	}
	text := "3/4 🏷 Category for " + d.item + " (" + priceText(d) + ")?\n" + how
	return text, tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func notePrompt(d *draft, cats []model.ExpenseCategory) (string, tgbotapi.InlineKeyboardMarkup) {
	cat := "no category"
	if d.category != "" {
		cat = categoryLabel(cats, d.category)
	}
	text := "4/4 📝 Description? (optional)\n" + d.item + " · " + priceText(d) + " · " + cat + "\n" +
		"Type a note, or tap Save."
	kb := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("💾 Save without description", "a:nonote")),
		tgbotapi.NewInlineKeyboardRow(cancelButton()))
	return text, kb
}

func priceText(d *draft) string {
	minor, err := service.ParseAmount(d.amount, d.currency)
	if err != nil {
		return d.amount + " " + d.currency
	}
	return model.FormatMoney(minor, d.currency)
}

// ---- answers ----

// continueFlow handles a typed answer while a guided entry is open. It reports
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
		msg, kb := amountPrompt(d)
		b.send(c, chatID, msg, &kb)
		if d.group {
			b.askReply(c, chatID, "↩️ Price:", "e.g. 4.5 or 8000")
		}
	case stepAmount:
		amount, cur, err := service.ParseQuickAmount(text)
		if err == nil {
			if cur == "" || (cur != "USD" && cur != "KHR") {
				cur = d.currency
			}
			_, err = service.ParseAmount(amount, cur)
		}
		if err != nil {
			msg, kb := amountPrompt(d)
			b.send(c, chatID, "🤔 "+err.Error()+"\n\n"+msg, &kb)
			if d.group {
				b.askReply(c, chatID, "↩️ Price:", "e.g. 4.5 or 8000")
			}
			return true
		}
		d.amount, d.currency, d.step = amount, cur, stepCategory
		b.setDraft(chatID, d)
		cats, _ := b.svc.ListExpenseCategories(ctx)
		msg, kb := categoryPrompt(d, cats)
		b.send(c, chatID, msg, &kb)
	case stepCategory:
		cats, _ := b.svc.ListExpenseCategories(ctx)
		b.toNote(c, chatID, 0, d, service.MatchCategory(cats, text), cats)
	case stepNote:
		b.finishFlow(ctx, c, chatID, 0, text)
	}
	return true
}

// toNote stores the category and asks for the description. editMsg > 0 turns the
// category question into the description question; otherwise a new message is sent.
func (b *Bot) toNote(c Client, chatID int64, editMsg int, d *draft, category string, cats []model.ExpenseCategory) {
	d.category, d.step = category, stepNote
	b.setDraft(chatID, d)
	msg, kb := notePrompt(d, cats)
	if editMsg > 0 {
		b.edit(c, chatID, editMsg, msg, &kb)
	} else {
		b.send(c, chatID, msg, &kb)
	}
	if d.group {
		b.askReply(c, chatID, "↩️ Description (or /skip):", "Optional description")
	}
}

// finishFlow saves the drafted expense. editMsg > 0 turns that message (the
// description question) into the confirmation; otherwise a new message is sent.
func (b *Bot) finishFlow(ctx context.Context, c Client, chatID int64, editMsg int, note string) {
	d := b.getDraft(chatID)
	if d == nil || d.step != stepNote {
		return
	}
	e, err := b.svc.CreateExpense(ctx, service.ExpenseInput{Item: d.item, Amount: json.Number(d.amount), Currency: d.currency,
		Category: d.category, Note: note})
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

// handleFlowCallback handles the buttons of a guided entry.
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
	code := m[1]
	switch {
	case code == "cancel":
		b.setDraft(chatID, nil)
		answer("Cancelled")
		b.edit(c, chatID, msgID, "✖ Cancelled.", nil)
	case strings.HasPrefix(code, "cur:"):
		if d.step != stepAmount {
			answer("Answer the question above first")
			return
		}
		d.currency = strings.TrimPrefix(code, "cur:")
		b.setDraft(chatID, d)
		answer(map[string]string{"USD": "💵 Dollar", "KHR": "៛ Riel"}[d.currency])
		msg, kb := amountPrompt(d)
		b.edit(c, chatID, msgID, msg, &kb)
	case code == "nonote":
		if d.step != stepNote {
			answer("Answer the question above first")
			return
		}
		answer("")
		b.finishFlow(ctx, c, chatID, msgID, "")
	case d.step != stepCategory:
		answer("Answer the question above first")
	case code == "nocat":
		answer("")
		cats, _ := b.svc.ListExpenseCategories(ctx)
		b.toNote(c, chatID, msgID, d, "", cats)
	default:
		id, _ := strconv.ParseInt(code[1:], 10, 64)
		cats, _ := b.svc.ListExpenseCategories(ctx)
		name := ""
		for _, x := range cats {
			if x.ID == id {
				name = x.Name
			}
		}
		if name == "" {
			answer("That category no longer exists")
			return
		}
		answer("")
		b.toNote(c, chatID, msgID, d, name, cats)
	}
}
