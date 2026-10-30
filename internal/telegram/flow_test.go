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

	// 1 what → 2 currency → 3 amount → 4 category → 5 description.
	bot.HandleUpdate(ctx, ownerMsg(btnAdd))
	if !strings.Contains(mc.lastSent().Text, "What did you spend on?") {
		t.Fatalf("start: %q", mc.lastSent().Text)
	}
	bot.HandleUpdate(ctx, ownerMsg("Dinner"))
	ask := mc.lastSent()
	if !strings.Contains(ask.Text, "Choose the currency (2/5)") {
		t.Fatalf("currency question: %q", ask.Text)
	}
	bot.HandleUpdate(ctx, ownerMsg("16000")) // typing before choosing
	if !strings.Contains(mc.lastSent().Text, "tap Dollar or Riel") {
		t.Fatalf("currency first: %q", mc.lastSent().Text)
	}
	bot.HandleUpdate(ctx, press(findButton(t, inline(t, ask), "Riel"), ownerChat, 5))
	// The tapped message becomes the amount question: one message, not two.
	sentBefore, _, _ := mc.snapshot()
	if ed := mc.lastEdit(); ed.MessageID != 5 || !strings.Contains(ed.Text, "Dinner\nCurrency: Riel\n\nEnter the amount in riel (3/5)") {
		t.Fatalf("currency + amount question: %q", ed.Text)
	}
	if n, _, _ := mc.snapshot(); n != sentBefore {
		t.Fatal("no extra message expected")
	}
	bot.HandleUpdate(ctx, ownerMsg("16000.50"))
	if !strings.Contains(mc.lastSent().Text, "no decimals") {
		t.Fatalf("riel decimals: %q", mc.lastSent().Text)
	}
	bot.HandleUpdate(ctx, ownerMsg("$5"))
	if !strings.Contains(mc.lastSent().Text, "You chose riel") {
		t.Fatalf("other currency typed: %q", mc.lastSent().Text)
	}
	bot.HandleUpdate(ctx, ownerMsg("16,000"))
	ask = mc.lastSent()
	if !strings.Contains(ask.Text, "Dinner — 16,000៛") || !strings.Contains(ask.Text, "Choose a category (4/5)") {
		t.Fatalf("category question: %q", ask.Text)
	}
	bot.HandleUpdate(ctx, press(findButton(t, inline(t, ask), "Food & Drinks"), ownerChat, 20))
	if ed := mc.lastEdit(); ed.MessageID != 20 || !strings.Contains(ed.Text, "Category: 🍜 Food & Drinks\n\nDescription (5/5)") {
		t.Fatalf("category + description question: %q", ed.Text)
	}
	if list, _ := svc.ListExpenses(ctx, model.ExpenseFilter{}); len(list) != 0 {
		t.Fatal("must not save before the last step")
	}
	bot.HandleUpdate(ctx, ownerMsg("with family"))
	list, _ := svc.ListExpenses(ctx, model.ExpenseFilter{})
	if len(list) != 1 || list[0].Item != "Dinner" || list[0].AmountMinor != 16000 || list[0].Currency != "KHR" ||
		list[0].Category != "Food & Drinks" || list[0].Note != "with family" {
		t.Fatalf("saved %+v", list)
	}
	if txt := mc.lastSent().Text; !strings.HasPrefix(txt, "Saved\nDinner — 16,000៛") || !strings.Contains(txt, "Description: with family") {
		t.Fatalf("confirmation %q", txt)
	}

	// Dollar, typed category, save without description.
	bot.HandleUpdate(ctx, ownerMsg(btnAdd))
	bot.HandleUpdate(ctx, ownerMsg("Iced coffee"))
	bot.HandleUpdate(ctx, press("a:cur:USD", ownerChat, 6))
	bot.HandleUpdate(ctx, ownerMsg("1.25"))
	bot.HandleUpdate(ctx, ownerMsg("drinks"))
	bot.HandleUpdate(ctx, press(findButton(t, inline(t, mc.lastSent()), "Save without description"), ownerChat, 21))
	list, _ = svc.ListExpenses(ctx, model.ExpenseFilter{})
	if list[0].Item != "Iced coffee" || list[0].Currency != "USD" || list[0].AmountMinor != 125 || list[0].Category != "Food & Drinks" || list[0].Note != "" {
		t.Fatalf("second %+v", list[0])
	}
	if ed := mc.lastEdit(); ed.MessageID != 21 || !strings.HasPrefix(ed.Text, "Saved") {
		t.Fatalf("save without description: %q", ed.Text)
	}

	// No category; old keyboard texts still work.
	bot.HandleUpdate(ctx, ownerMsg("➕ Add expense"))
	bot.HandleUpdate(ctx, ownerMsg("Gift"))
	bot.HandleUpdate(ctx, press("a:cur:USD", ownerChat, 7))
	bot.HandleUpdate(ctx, ownerMsg("20"))
	bot.HandleUpdate(ctx, press("a:nocat", ownerChat, 30))
	bot.HandleUpdate(ctx, press("a:nonote", ownerChat, 31))
	list, _ = svc.ListExpenses(ctx, model.ExpenseFilter{})
	if list[0].Item != "Gift" || list[0].Category != "" || list[0].AmountMinor != 2000 {
		t.Fatalf("no category %+v", list[0])
	}

	// Buttons out of order, tampering, cancel, expiry.
	bot.HandleUpdate(ctx, ownerMsg(btnAdd))
	for _, data := range []string{"a:nocat", "a:nonote", "a:cur:KHR"} {
		bot.HandleUpdate(ctx, press(data, ownerChat, 32))
		if mc.lastAnswer() != "Answer the question above first" {
			t.Fatalf("%s early: %q", data, mc.lastAnswer())
		}
	}
	for _, data := range []string{"a:c1x", "a:cur:EUR"} {
		bot.HandleUpdate(ctx, press(data, ownerChat, 32))
		if mc.lastAnswer() != "Unknown action" {
			t.Fatalf("%s tampered: %q", data, mc.lastAnswer())
		}
	}
	bot.HandleUpdate(ctx, press("a:cancel", 777, 32)) // a stranger cannot touch the entry
	if bot.getDraft(ownerChat) == nil {
		t.Fatal("stranger cancelled the owner's entry")
	}
	bot.HandleUpdate(ctx, press("a:cancel", ownerChat, 32))
	if mc.lastAnswer() != "Cancelled" || bot.getDraft(ownerChat) != nil {
		t.Fatal("cancel should clear the entry")
	}
	bot.HandleUpdate(ctx, press("a:nonote", ownerChat, 32))
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
	if txt := mc.lastSent().Text; !strings.HasPrefix(txt, "Today (") || !strings.Contains(txt, "Total: ") {
		t.Fatalf("today button: %q", txt)
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
	if q := mc.lastSent(); !strings.Contains(q.Text, "What did you spend on?") || !isForceReply(q) {
		t.Fatalf("item question should open a reply: %+v", q)
	}
	bot.HandleUpdate(ctx, group("Dinner"))
	bot.HandleUpdate(ctx, press("a:cur:KHR", ownerChat, 3))
	if q := mc.lastSent(); !strings.Contains(q.Text, "Currency: Riel\n\nEnter the amount in riel") || !isForceReply(q) {
		t.Fatalf("amount should open a reply: %+v", q)
	}
	bot.HandleUpdate(ctx, group("16000"))
	bot.HandleUpdate(ctx, group("/skip@spideryou_bot")) // no category
	if q := mc.lastSent(); !strings.Contains(q.Text, "/skip") || !isForceReply(q) {
		t.Fatalf("description should open a reply: %+v", q)
	}
	bot.HandleUpdate(ctx, group("/skip")) // no description
	list, _ := svc.ListExpenses(ctx, model.ExpenseFilter{})
	if len(list) != 1 || list[0].Item != "Dinner" || list[0].AmountMinor != 16000 || list[0].Currency != "KHR" || list[0].Category != "" {
		t.Fatalf("saved %+v", list)
	}
}
