package telegram

import (
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/sovannroth/mynotes/internal/model"
)

func TestGuidedExpenseEntry(t *testing.T) {
	svc, bot, mc, ctx := setup(t)

	// The main keyboard comes with plain replies.
	bot.HandleUpdate(ctx, ownerMsg("/start"))
	if _, ok := mc.lastSent().ReplyMarkup.(tgbotapi.ReplyKeyboardMarkup); !ok {
		t.Fatal("help should show the main keyboard")
	}

	// What → how much (a bad answer is asked again) → tap a category.
	bot.HandleUpdate(ctx, ownerMsg(btnAdd))
	if !strings.Contains(mc.lastSent().Text, "What did you spend on?") {
		t.Fatalf("start: %q", mc.lastSent().Text)
	}
	bot.HandleUpdate(ctx, ownerMsg("Tuk tuk to market"))
	if !strings.Contains(mc.lastSent().Text, "How much for Tuk tuk to market?") {
		t.Fatalf("amount prompt: %q", mc.lastSent().Text)
	}
	bot.HandleUpdate(ctx, ownerMsg("cheap"))
	if !strings.Contains(mc.lastSent().Text, "Send just the amount") {
		t.Fatalf("bad amount: %q", mc.lastSent().Text)
	}
	bot.HandleUpdate(ctx, ownerMsg("8,000 riel"))
	ask := mc.lastSent()
	if !strings.Contains(ask.Text, "Category?") {
		t.Fatalf("category prompt: %q", ask.Text)
	}
	bot.HandleUpdate(ctx, press(findButton(t, ask.ReplyMarkup.(tgbotapi.InlineKeyboardMarkup), "Transport"), ownerChat, 20))
	list, _ := svc.ListExpenses(ctx, model.ExpenseFilter{})
	if len(list) != 1 || list[0].Item != "Tuk tuk to market" || list[0].AmountMinor != 8000 || list[0].Currency != "KHR" || list[0].Category != "Transport" {
		t.Fatalf("saved %+v", list)
	}
	if ed := mc.lastEdit(); ed.MessageID != 20 || !strings.Contains(ed.Text, "✅ Saved") || !strings.Contains(ed.Text, "8,000៛") {
		t.Fatalf("confirmation %q", ed.Text)
	}
	// The entry is finished: free text is a one-line expense again.
	bot.HandleUpdate(ctx, ownerMsg("water 1"))
	if list, _ = svc.ListExpenses(ctx, model.ExpenseFilter{}); len(list) != 2 || list[0].Currency != "KHR" || list[0].AmountMinor != 1 {
		t.Fatalf("one-line after flow (last currency KHR): %+v", list[0])
	}

	// Typing a category name works too.
	bot.HandleUpdate(ctx, ownerMsg("/add"))
	bot.HandleUpdate(ctx, ownerMsg("Iced coffee"))
	bot.HandleUpdate(ctx, ownerMsg("$1.25"))
	bot.HandleUpdate(ctx, ownerMsg("drinks"))
	list, _ = svc.ListExpenses(ctx, model.ExpenseFilter{})
	if list[0].Item != "Iced coffee" || list[0].Currency != "USD" || list[0].Category != "Food & Drinks" {
		t.Fatalf("typed category %+v", list[0])
	}

	// Cancel in the middle; old buttons then report expiry.
	bot.HandleUpdate(ctx, ownerMsg(btnAdd))
	bot.HandleUpdate(ctx, ownerMsg("Snacks"))
	bot.HandleUpdate(ctx, press("a:cancel", ownerChat, 30))
	if mc.lastAnswer() != "Cancelled" || bot.getDraft(ownerChat) != nil {
		t.Fatal("cancel should clear the entry")
	}
	bot.HandleUpdate(ctx, press("a:skip", ownerChat, 30))
	if mc.lastAnswer() != "This entry has expired" {
		t.Fatalf("stale button: %q", mc.lastAnswer())
	}

	// Category buttons are refused before the amount is known; tampered data rejected.
	bot.HandleUpdate(ctx, ownerMsg(btnAdd))
	bot.HandleUpdate(ctx, press("a:skip", ownerChat, 31))
	if mc.lastAnswer() != "Answer the question above first" {
		t.Fatalf("early skip: %q", mc.lastAnswer())
	}
	bot.HandleUpdate(ctx, press("a:c1x", ownerChat, 31))
	if mc.lastAnswer() != "Unknown action" {
		t.Fatalf("tampered: %q", mc.lastAnswer())
	}

	// An abandoned entry expires.
	bot.mu.Lock()
	bot.drafts[ownerChat].updated = time.Now().Add(-draftTTL - time.Minute)
	bot.mu.Unlock()
	bot.HandleUpdate(ctx, ownerMsg("Old answer"))
	if !strings.Contains(mc.lastSent().Text, "couldn't find an amount") {
		t.Fatalf("expired entry should not capture text: %q", mc.lastSent().Text)
	}

	// No category, and a stranger cannot drive the owner's entry.
	bot.HandleUpdate(ctx, ownerMsg(btnAdd))
	bot.HandleUpdate(ctx, ownerMsg("Gift"))
	bot.HandleUpdate(ctx, ownerMsg("20"))
	bot.HandleUpdate(ctx, press("a:skip", 777, 40))
	if bot.getDraft(ownerChat) == nil {
		t.Fatal("stranger finished the owner's entry")
	}
	bot.HandleUpdate(ctx, press("a:skip", ownerChat, 40))
	list, _ = svc.ListExpenses(ctx, model.ExpenseFilter{})
	if list[0].Item != "Gift" || list[0].Category != "" || list[0].AmountMinor != 2000 {
		t.Fatalf("no category %+v", list[0])
	}

	bot.HandleUpdate(ctx, ownerMsg(btnToday))
	if msg := mc.lastSent(); !strings.Contains(msg.Text, "Today") {
		t.Fatalf("today button: %q", msg.Text)
	}
}
