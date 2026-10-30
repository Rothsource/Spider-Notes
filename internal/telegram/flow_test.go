package telegram

import (
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/sovannroth/mynotes/internal/model"
)

func inline(t *testing.T, m tgbotapi.MessageConfig) tgbotapi.InlineKeyboardMarkup {
	t.Helper()
	kb, ok := m.ReplyMarkup.(tgbotapi.InlineKeyboardMarkup)
	if !ok {
		t.Fatalf("message %q has no inline buttons", m.Text)
	}
	return kb
}

func TestGuidedExpenseEntry(t *testing.T) {
	svc, bot, mc, ctx := setup(t)

	// The main keyboard comes with plain replies.
	bot.HandleUpdate(ctx, ownerMsg("/start"))
	if _, ok := mc.lastSent().ReplyMarkup.(tgbotapi.ReplyKeyboardMarkup); !ok {
		t.Fatal("help should show the main keyboard")
	}

	// 1 What → 2 price (Riel button, bad answer re-asked) → 3 category → 4 description.
	bot.HandleUpdate(ctx, ownerMsg(btnAdd))
	if !strings.Contains(mc.lastSent().Text, "What did you spend on?") {
		t.Fatalf("start: %q", mc.lastSent().Text)
	}
	bot.HandleUpdate(ctx, ownerMsg("Tuk tuk to market"))
	ask := mc.lastSent()
	if !strings.Contains(ask.Text, "How much for Tuk tuk to market?") || !strings.Contains(ask.Text, "Dollar or Riel") {
		t.Fatalf("price prompt: %q", ask.Text)
	}
	bot.HandleUpdate(ctx, press(findButton(t, inline(t, ask), "Riel"), ownerChat, 5))
	if ed := mc.lastEdit(); !strings.Contains(ed.Text, "e.g. 8000") || ed.ReplyMarkup.InlineKeyboard[0][1].Text != "✅ ៛ Riel" {
		t.Fatalf("riel not selected: %+v", ed)
	}
	bot.HandleUpdate(ctx, ownerMsg("cheap"))
	if !strings.Contains(mc.lastSent().Text, "Send just the amount") {
		t.Fatalf("bad amount: %q", mc.lastSent().Text)
	}
	bot.HandleUpdate(ctx, ownerMsg("8,000"))
	ask = mc.lastSent()
	if !strings.Contains(ask.Text, "Category for Tuk tuk to market (8,000៛)") {
		t.Fatalf("category prompt: %q", ask.Text)
	}
	bot.HandleUpdate(ctx, press(findButton(t, inline(t, ask), "Transport"), ownerChat, 20))
	if ed := mc.lastEdit(); ed.MessageID != 20 || !strings.Contains(ed.Text, "Description? (optional)") || !strings.Contains(ed.Text, "🛺 Transport") {
		t.Fatalf("description prompt: %q", ed.Text)
	}
	if list, _ := svc.ListExpenses(ctx, model.ExpenseFilter{}); len(list) != 0 {
		t.Fatal("must not save before the description step")
	}
	bot.HandleUpdate(ctx, ownerMsg("shared with Dara"))
	list, _ := svc.ListExpenses(ctx, model.ExpenseFilter{})
	if len(list) != 1 || list[0].Item != "Tuk tuk to market" || list[0].AmountMinor != 8000 || list[0].Currency != "KHR" ||
		list[0].Category != "Transport" || list[0].Note != "shared with Dara" {
		t.Fatalf("saved %+v", list)
	}
	if !strings.Contains(mc.lastSent().Text, "✅ Saved") {
		t.Fatalf("confirmation %q", mc.lastSent().Text)
	}

	// Dollar is preselected from the last expense only when it was dollars: here riel stays.
	bot.HandleUpdate(ctx, ownerMsg(btnAdd))
	bot.HandleUpdate(ctx, ownerMsg("Iced coffee"))
	if !strings.Contains(mc.lastSent().Text, "e.g. 8000") {
		t.Fatalf("last currency (riel) should be preselected: %q", mc.lastSent().Text)
	}
	bot.HandleUpdate(ctx, ownerMsg("$1.25"))  // a typed mark overrides the button
	bot.HandleUpdate(ctx, ownerMsg("drinks")) // typed category
	desc := mc.lastSent()
	bot.HandleUpdate(ctx, press(findButton(t, inline(t, desc), "Save without description"), ownerChat, 21))
	list, _ = svc.ListExpenses(ctx, model.ExpenseFilter{})
	if list[0].Item != "Iced coffee" || list[0].Currency != "USD" || list[0].AmountMinor != 125 || list[0].Category != "Food & Drinks" || list[0].Note != "" {
		t.Fatalf("second %+v", list[0])
	}
	if ed := mc.lastEdit(); ed.MessageID != 21 || !strings.Contains(ed.Text, "✅ Saved") {
		t.Fatalf("save without description: %q", ed.Text)
	}

	// No category.
	bot.HandleUpdate(ctx, ownerMsg(btnAdd))
	bot.HandleUpdate(ctx, ownerMsg("Gift"))
	bot.HandleUpdate(ctx, ownerMsg("20"))
	bot.HandleUpdate(ctx, press("a:nocat", ownerChat, 30))
	bot.HandleUpdate(ctx, press("a:nonote", ownerChat, 30))
	list, _ = svc.ListExpenses(ctx, model.ExpenseFilter{})
	if list[0].Item != "Gift" || list[0].Category != "" || list[0].AmountMinor != 2000 || list[0].Currency != "USD" {
		t.Fatalf("no category %+v", list[0])
	}

	// Buttons out of order, tampering, cancel, expiry.
	bot.HandleUpdate(ctx, ownerMsg(btnAdd))
	for _, data := range []string{"a:nocat", "a:nonote", "a:cur:KHR"} {
		bot.HandleUpdate(ctx, press(data, ownerChat, 31))
		if mc.lastAnswer() != "Answer the question above first" {
			t.Fatalf("%s early: %q", data, mc.lastAnswer())
		}
	}
	for _, data := range []string{"a:c1x", "a:cur:EUR"} {
		bot.HandleUpdate(ctx, press(data, ownerChat, 31))
		if mc.lastAnswer() != "Unknown action" {
			t.Fatalf("%s tampered: %q", data, mc.lastAnswer())
		}
	}
	bot.HandleUpdate(ctx, press("a:cancel", 777, 31)) // a stranger cannot touch the entry
	if bot.getDraft(ownerChat) == nil {
		t.Fatal("stranger cancelled the owner's entry")
	}
	bot.HandleUpdate(ctx, press("a:cancel", ownerChat, 31))
	if mc.lastAnswer() != "Cancelled" || bot.getDraft(ownerChat) != nil {
		t.Fatal("cancel should clear the entry")
	}
	bot.HandleUpdate(ctx, press("a:nonote", ownerChat, 31))
	if mc.lastAnswer() != "This entry has expired" {
		t.Fatalf("stale button: %q", mc.lastAnswer())
	}
	bot.HandleUpdate(ctx, ownerMsg(btnAdd))
	bot.mu.Lock()
	bot.drafts[ownerChat].updated = time.Now().Add(-draftTTL - time.Minute)
	bot.mu.Unlock()
	bot.HandleUpdate(ctx, ownerMsg("Old answer"))
	if !strings.Contains(mc.lastSent().Text, "couldn't find an amount") {
		t.Fatalf("expired entry should not capture text: %q", mc.lastSent().Text)
	}

	bot.HandleUpdate(ctx, ownerMsg(btnToday))
	if msg := mc.lastSent(); !strings.Contains(msg.Text, "Today") {
		t.Fatalf("today button: %q", msg.Text)
	}
}

// In groups, bots in privacy mode only get replies to their own messages, so
// every typed question opens a reply.
func TestGuidedEntryInGroup(t *testing.T) {
	svc, bot, mc, ctx := setup(t)
	group := func(text string) tgbotapi.Update {
		return tgbotapi.Update{Message: &tgbotapi.Message{Text: text, Chat: &tgbotapi.Chat{ID: ownerChat, Type: "group"}}}
	}
	isForceReply := func(m tgbotapi.MessageConfig) bool { _, ok := m.ReplyMarkup.(tgbotapi.ForceReply); return ok }

	bot.HandleUpdate(ctx, group("/add@spideryou_bot"))
	if q := mc.lastSent(); !strings.Contains(q.Text, "1/4") || !isForceReply(q) {
		t.Fatalf("item question should open a reply: %+v", q)
	}
	bot.HandleUpdate(ctx, group("Dinner"))
	if q := mc.lastSent(); q.Text != "↩️ Price:" || !isForceReply(q) {
		t.Fatalf("price should open a reply: %+v", q)
	}
	bot.HandleUpdate(ctx, group("16000r"))
	bot.HandleUpdate(ctx, group("/skip@spideryou_bot")) // no category
	if q := mc.lastSent(); !strings.Contains(q.Text, "Description") || !isForceReply(q) {
		t.Fatalf("description should open a reply: %+v", q)
	}
	bot.HandleUpdate(ctx, group("/skip")) // no description
	list, _ := svc.ListExpenses(ctx, model.ExpenseFilter{})
	if len(list) != 1 || list[0].Item != "Dinner" || list[0].AmountMinor != 16000 || list[0].Currency != "KHR" || list[0].Category != "" {
		t.Fatalf("saved %+v", list)
	}
}
