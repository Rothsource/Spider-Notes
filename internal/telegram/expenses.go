package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/sovannroth/mynotes/internal/model"
	"github.com/sovannroth/mynotes/internal/service"
)

// Expense buttons: u = undo, x = switch USD/KHR, cats = show categories, c<id> = set category.
var expenseCallbackRe = regexp.MustCompile(`^e:([1-9][0-9]{0,17}):(u|x|cats|c[1-9][0-9]{0,9})$`)

// maxCategoryButtons keeps the category keyboard short enough for a phone screen.
const maxCategoryButtons = 24

func helpText(currency string) string {
	return "💸 Log an expense by sending a message:\n" +
		"• lunch 4.5\n" +
		"• tuk tuk 8000r   (r, ៛ or riel = KHR · $ = USD)\n" +
		"• coffee 2.25 #food @aba   (#category, @payment)\n" +
		"Without a currency mark I use the last one you used (now " + currency + ").\n" +
		"The date and time are recorded automatically.\n\n" +
		"/today · /week · /month: totals\n" +
		"/help: this message"
}

// handleMessage handles a text message from the owner: a command or an expense.
func (b *Bot) handleMessage(ctx context.Context, c Client, chatID int64, m *tgbotapi.Message) {
	text := strings.TrimSpace(m.Text)
	if text == "" {
		b.send(c, chatID, "Send an expense as text, e.g. lunch 4.5\n/help for more.", nil)
		return
	}
	if strings.HasPrefix(text, "/") {
		cmd, _, _ := strings.Cut(strings.Fields(text)[0], "@") // "/today@MyBot" in groups
		switch strings.ToLower(cmd) {
		case "/today", "/week", "/month":
			b.send(c, chatID, b.totalsText(ctx, strings.TrimPrefix(strings.ToLower(cmd), "/")), nil)
		default:
			b.send(c, chatID, helpText(b.svc.LastCurrency(ctx)), nil)
		}
		return
	}
	e, err := b.svc.CreateQuickExpense(ctx, text)
	if err != nil {
		var ve *model.ValidationError
		if errors.As(err, &ve) {
			b.send(c, chatID, "🤔 "+ve.Msg+"\n/help for examples.", nil)
			return
		}
		slog.Error("telegram expense", "err", err)
		b.send(c, chatID, "Something went wrong, try again.", nil)
		return
	}
	cats, _ := b.svc.ListExpenseCategories(ctx)
	kb := expenseKeyboard(e, cats, false)
	b.send(c, chatID, expenseText(b.svc, e, cats, "✅ Saved"), &kb)
}

func (b *Bot) send(c Client, chatID int64, text string, kb *tgbotapi.InlineKeyboardMarkup) {
	msg := tgbotapi.NewMessage(chatID, truncate(text))
	if kb != nil {
		msg.ReplyMarkup = *kb
	}
	if _, err := c.Send(msg); err != nil {
		slog.Warn("telegram send failed", "err", b.redact(err))
	}
}

func (b *Bot) edit(c Client, chatID int64, messageID int, text string, kb *tgbotapi.InlineKeyboardMarkup) {
	ed := tgbotapi.NewEditMessageText(chatID, messageID, truncate(text))
	if kb == nil {
		kb = &tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
	}
	ed.ReplyMarkup = kb
	if _, err := c.Request(ed); err != nil && !isPermanentEditError(err) {
		slog.Warn("telegram edit failed", "err", b.redact(err))
	}
}

func categoryLabel(cats []model.ExpenseCategory, name string) string {
	for _, c := range cats {
		if c.Name == name && c.Emoji != "" {
			return c.Emoji + " " + name
		}
	}
	return "🏷 " + name
}

func expenseText(svc *service.Service, e *model.Expense, cats []model.ExpenseCategory, header string) string {
	var b strings.Builder
	b.WriteString(header + "\n🧾 " + e.Item + " — " + model.FormatMoney(e.AmountMinor, e.Currency))
	var extra []string
	if e.Category != "" {
		extra = append(extra, categoryLabel(cats, e.Category))
	}
	if e.Payment != "" {
		extra = append(extra, "💳 "+e.Payment)
	}
	if len(extra) > 0 {
		b.WriteString("\n" + strings.Join(extra, " · "))
	}
	b.WriteString("\n🕒 " + e.SpentAt.In(svc.Location()).Format("Mon 02 Jan 15:04"))
	if e.Category == "" {
		b.WriteString("\n\nPick a category:")
	}
	return b.String()
}

func expenseKeyboard(e *model.Expense, cats []model.ExpenseCategory, showCats bool) tgbotapi.InlineKeyboardMarkup {
	cb := func(code string) string { return "e:" + strconv.FormatInt(e.ID, 10) + ":" + code }
	var rows [][]tgbotapi.InlineKeyboardButton
	pick := showCats || e.Category == ""
	if pick {
		var row []tgbotapi.InlineKeyboardButton
		for i, c := range cats {
			if i == maxCategoryButtons {
				break
			}
			label := strings.TrimSpace(c.Emoji + " " + c.Name)
			row = append(row, tgbotapi.NewInlineKeyboardButtonData(label, cb("c"+strconv.FormatInt(c.ID, 10))))
			if len(row) == 2 {
				rows, row = append(rows, row), nil
			}
		}
		if row != nil {
			rows = append(rows, row)
		}
	}
	var bottom []tgbotapi.InlineKeyboardButton
	if !pick {
		bottom = append(bottom, tgbotapi.NewInlineKeyboardButtonData("🏷 Category", cb("cats")))
	}
	switch e.Currency {
	case "USD":
		bottom = append(bottom, tgbotapi.NewInlineKeyboardButtonData("💱 Riel", cb("x")))
	case "KHR":
		bottom = append(bottom, tgbotapi.NewInlineKeyboardButtonData("💱 Dollar", cb("x")))
	}
	bottom = append(bottom, tgbotapi.NewInlineKeyboardButtonData("🗑 Undo", cb("u")))
	return tgbotapi.NewInlineKeyboardMarkup(append(rows, bottom)...)
}

// handleExpenseCallback handles the buttons under a saved expense.
func (b *Bot) handleExpenseCallback(ctx context.Context, c Client, cq *tgbotapi.CallbackQuery, answer func(string)) {
	m := expenseCallbackRe.FindStringSubmatch(cq.Data)
	if m == nil {
		answer("Unknown action")
		return
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		answer("Unknown action")
		return
	}
	chatID, msgID := cq.Message.Chat.ID, cq.Message.MessageID
	var e *model.Expense
	header, note := "✅ Updated", ""
	switch code := m[2]; {
	case code == "u":
		if e, err = b.svc.GetExpense(ctx, id); err == nil {
			if err = b.svc.DeleteExpense(ctx, id); err == nil {
				answer("🗑 Deleted")
				b.edit(c, chatID, msgID, "🗑 Deleted: "+e.Item+" — "+model.FormatMoney(e.AmountMinor, e.Currency), nil)
				return
			}
		}
	case code == "x":
		if e, err = b.svc.SwitchExpenseCurrency(ctx, id); err == nil {
			note = "💱 " + e.Currency
		}
	case code == "cats":
		if e, err = b.svc.GetExpense(ctx, id); err == nil {
			cats, _ := b.svc.ListExpenseCategories(ctx)
			kb := expenseKeyboard(e, cats, true)
			answer("")
			b.edit(c, chatID, msgID, expenseText(b.svc, e, cats, "✅ Saved")+"\n\nPick a category:", &kb)
			return
		}
	default:
		catID, _ := strconv.ParseInt(code[1:], 10, 64)
		if e, err = b.svc.SetExpenseCategory(ctx, id, catID); err == nil {
			note = "🏷 " + e.Category
		}
	}
	var ve *model.ValidationError
	switch {
	case err == nil:
	case errors.Is(err, model.ErrNotFound):
		answer("This expense no longer exists")
		b.edit(c, chatID, msgID, "🗑 This expense was deleted.", nil)
		return
	case errors.As(err, &ve):
		answer(ve.Msg)
		return
	default:
		slog.Error("telegram expense action failed", "err", err, "expense", id)
		answer("Something went wrong, try again")
		return
	}
	answer(note)
	cats, _ := b.svc.ListExpenseCategories(ctx)
	kb := expenseKeyboard(e, cats, false)
	b.edit(c, chatID, msgID, expenseText(b.svc, e, cats, header), &kb)
}

// totalsText answers /today, /week and /month.
func (b *Bot) totalsText(ctx context.Context, period string) string {
	now := b.svc.Now()
	from := b.svc.StartOfDay(now)
	var to time.Time
	title := "Today · " + from.Format("Mon 02 Jan")
	switch period {
	case "week":
		from = from.AddDate(0, 0, -((int(from.Weekday()) + 6) % 7))
		to = from.AddDate(0, 0, 7)
		title = "This week · from " + from.Format("Mon 02 Jan")
	case "month":
		from = time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, from.Location())
		to = from.AddDate(0, 1, 0)
		title = "This month · " + from.Format("January 2006")
	default:
		to = from.AddDate(0, 0, 1)
	}
	sum, err := b.svc.ExpenseTotals(ctx, from, to)
	if err != nil {
		slog.Error("telegram totals", "err", err)
		return "Something went wrong, try again."
	}
	if sum.Count == 0 {
		return "📊 " + title + "\nNothing spent yet."
	}
	var totals []string
	for _, t := range sum.Totals {
		totals = append(totals, t.Display)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "📊 %s\n💰 %s  (%d expense%s)\n", title, strings.Join(totals, " · "), sum.Count, map[bool]string{true: "", false: "s"}[sum.Count == 1])
	cats, _ := b.svc.ListExpenseCategories(ctx)
	for i, ct := range sum.ByCategory {
		if i == 12 {
			break
		}
		name := "Uncategorised"
		if ct.Category != "" {
			name = categoryLabel(cats, ct.Category)
		}
		sb.WriteString("\n" + name + " — " + ct.Display)
	}
	return sb.String()
}
