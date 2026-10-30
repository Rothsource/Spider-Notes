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
	btnAdd   = "Add expense"
	btnToday = "Today"
	btnMonth = "This month"
)

// Button texts of the first version, still on some phones until the keyboard is resent.
var legacyButtons = map[string]string{"➕ Add expense": btnAdd, "📊 Today": btnToday, "📅 Month": btnMonth}

func mainKeyboard() tgbotapi.ReplyKeyboardMarkup {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton(btnAdd)),
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton(btnToday), tgbotapi.NewKeyboardButton(btnMonth)),
	)
	kb.ResizeKeyboard = true
	kb.InputFieldPlaceholder = "Tap Add expense"
	return kb
}

// Guided entry: what → currency → amount → category → description.
type step int

const (
	stepItem step = iota
	stepCurrency
	stepAmount
	stepCategory
	stepNote
)

const totalSteps = "5"

// draftTTL is how long an unfinished guided entry waits for the next answer.
const draftTTL = 15 * time.Minute

type draft struct {
	step     step
	item     string
	currency string
	amount   string
	category string
	group    bool // a group chat: typed answers must be replies (bot privacy mode)
	updated  time.Time
}

// Buttons of the guided entry.
//
//	cancel     stop the entry
//	cur:USD    dollar / cur:KHR riel
//	c<id>      category / nocat: no category
//	nonote     save without a description
var flowCallbackRe = regexp.MustCompile(`^a:(cancel|cur:(?:USD|KHR)|nocat|nonote|c[1-9][0-9]{0,9})$`)

var currencyNames = map[string]string{"USD": "Dollar", "KHR": "Riel"}

func cancelButton() tgbotapi.InlineKeyboardButton {
	return tgbotapi.NewInlineKeyboardButtonData("Cancel", "a:cancel")
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

// ask sends a question that expects a typed answer. In a group it opens a reply
// (bots in privacy mode only receive replies to their own messages) and the
// buttons are left out; in a private chat it carries the given buttons.
func (b *Bot) ask(c Client, chatID int64, d *draft, text, placeholder string, kb *tgbotapi.InlineKeyboardMarkup) {
	msg := tgbotapi.NewMessage(chatID, truncate(text))
	switch {
	case d.group:
		msg.ReplyMarkup = tgbotapi.ForceReply{ForceReply: true, InputFieldPlaceholder: placeholder}
	case kb != nil:
		msg.ReplyMarkup = *kb
	}
	if _, err := c.Send(msg); err != nil {
		slog.Warn("telegram send failed", "err", b.redact(err))
	}
}

func cancelOnly() *tgbotapi.InlineKeyboardMarkup {
	kb := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(cancelButton()))
	return &kb
}

// ---- questions ----

func (b *Bot) startFlow(c Client, chatID int64, group bool) {
	d := &draft{step: stepItem, group: group}
	b.setDraft(chatID, d)
	text := "New expense (1/" + totalSteps + ")\nWhat did you spend on?"
	if group {
		text += "\n\nType /cancel to stop."
	}
	b.ask(c, chatID, d, text, "e.g. Dinner", cancelOnly())
}

func (b *Bot) askCurrency(c Client, chatID int64, d *draft) {
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Dollar ($)", "a:cur:USD"),
			tgbotapi.NewInlineKeyboardButtonData("Riel (៛)", "a:cur:KHR")),
		tgbotapi.NewInlineKeyboardRow(cancelButton()))
	b.send(c, chatID, d.item+"\n\nChoose the currency (2/"+totalSteps+"):", &kb)
}

func (b *Bot) askAmount(c Client, chatID int64, d *draft, problem string) {
	example := "4.50"
	if d.currency == "KHR" {
		example = "16000"
	}
	text := "Enter the amount in " + strings.ToLower(currencyNames[d.currency]) + " (3/" + totalSteps + "):\nExample: " + example
	if problem != "" {
		text = problem + "\n\n" + text
	}
	b.ask(c, chatID, d, text, "Example: "+example, cancelOnly())
}

func (b *Bot) askCategory(c Client, chatID int64, d *draft, cats []model.ExpenseCategory) {
	var rows [][]tgbotapi.InlineKeyboardButton
	var row []tgbotapi.InlineKeyboardButton
	for i, cat := range cats {
		if i == maxCategoryButtons {
			break
		}
		row = append(row, tgbotapi.NewInlineKeyboardButtonData(strings.TrimSpace(cat.Emoji+" "+cat.Name), "a:c"+strconv.FormatInt(cat.ID, 10)))
		if len(row) == 2 {
			rows, row = append(rows, row), nil
		}
	}
	if row != nil {
		rows = append(rows, row)
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("No category", "a:nocat"), cancelButton()))
	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	how := "Tap one, or type a new name."
	if d.group {
		how = "Tap one, or reply to this message with a new name."
	}
	b.send(c, chatID, d.item+" — "+priceText(d)+"\n\nChoose a category (4/"+totalSteps+"):\n"+how, &kb)
}

func (b *Bot) askNote(c Client, chatID int64, d *draft) {
	text := "Description (5/" + totalSteps + "), optional:\nType a note, or tap Save."
	kb := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Save without description", "a:nonote"), cancelButton()))
	if d.group {
		text = "Description (5/" + totalSteps + "), optional:\nType a note, or send /skip to save without one."
	}
	b.ask(c, chatID, d, text, "Optional description", &kb)
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
			b.ask(c, chatID, d, "That's too long. What did you spend on? (a few words)", "e.g. Dinner", cancelOnly())
			return true
		}
		d.item, d.step = text, stepCurrency
		b.setDraft(chatID, d)
		b.askCurrency(c, chatID, d)
	case stepCurrency:
		b.send(c, chatID, "Please tap Dollar or Riel above first.", nil)
	case stepAmount:
		amount, cur, err := service.ParseQuickAmount(text)
		if err == nil && cur != "" && cur != d.currency {
			err = model.Invalid("You chose " + strings.ToLower(currencyNames[d.currency]) + ". Type just the number.")
		}
		if err == nil {
			_, err = service.ParseAmount(amount, d.currency)
		}
		if err != nil {
			b.askAmount(c, chatID, d, err.Error())
			return true
		}
		d.amount, d.step = amount, stepCategory
		b.setDraft(chatID, d)
		cats, _ := b.svc.ListExpenseCategories(ctx)
		b.askCategory(c, chatID, d, cats)
	case stepCategory:
		cats, _ := b.svc.ListExpenseCategories(ctx)
		b.toNote(c, chatID, d, service.MatchCategory(cats, text))
	case stepNote:
		b.finishFlow(ctx, c, chatID, 0, text)
	}
	return true
}

func (b *Bot) toNote(c Client, chatID int64, d *draft, category string) {
	d.category, d.step = category, stepNote
	b.setDraft(chatID, d)
	b.askNote(c, chatID, d)
}

// skipStep answers /skip: no category, or no description.
func (b *Bot) skipStep(ctx context.Context, c Client, chatID int64) {
	d := b.getDraft(chatID)
	switch {
	case d == nil:
		b.sendMain(c, chatID, "Nothing to skip. Tap Add expense to start.")
	case d.step == stepCategory:
		b.toNote(c, chatID, d, "")
	case d.step == stepNote:
		b.finishFlow(ctx, c, chatID, 0, "")
	default:
		b.send(c, chatID, "This question can't be skipped.", nil)
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
			b.sendMain(c, chatID, ve.Msg+"\nTap Add expense to try again.")
			return
		}
		slog.Error("telegram expense", "err", err)
		b.sendMain(c, chatID, "Something went wrong, try again.")
		return
	}
	b.setDraft(chatID, nil)
	cats, _ := b.svc.ListExpenseCategories(ctx)
	kb := expenseKeyboard(e, cats, false)
	text := expenseText(b.svc, e, cats, "Saved")
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
		b.edit(c, chatID, msgID, "Expired. Tap Add expense to start again.", nil)
		return
	}
	code := m[1]
	switch {
	case code == "cancel":
		b.setDraft(chatID, nil)
		answer("Cancelled")
		b.edit(c, chatID, msgID, "Cancelled.", nil)
	case strings.HasPrefix(code, "cur:"):
		if d.step != stepCurrency {
			answer("Answer the question above first")
			return
		}
		d.currency, d.step = strings.TrimPrefix(code, "cur:"), stepAmount
		b.setDraft(chatID, d)
		answer("")
		b.edit(c, chatID, msgID, d.item+"\nCurrency: "+currencyNames[d.currency], nil)
		b.askAmount(c, chatID, d, "")
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
		b.edit(c, chatID, msgID, d.item+" — "+priceText(d)+"\nCategory: none", nil)
		b.toNote(c, chatID, d, "")
	default:
		id, _ := strconv.ParseInt(code[1:], 10, 64)
		cats, _ := b.svc.ListExpenseCategories(ctx)
		var cat *model.ExpenseCategory
		for i := range cats {
			if cats[i].ID == id {
				cat = &cats[i]
			}
		}
		if cat == nil {
			answer("That category no longer exists")
			return
		}
		answer("")
		b.edit(c, chatID, msgID, d.item+" — "+priceText(d)+"\nCategory: "+categoryLabel(cats, cat.Name), nil)
		b.toNote(c, chatID, d, cat.Name)
	}
}

// currencyName is "Dollar" / "Riel", or the code for other currencies.
func currencyName(code string) string {
	if n, ok := currencyNames[code]; ok {
		return n
	}
	return code
}
