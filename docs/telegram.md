# Telegram Bot

Spider Notes uses a Telegram bot to deliver reminders and to record expenses.
It only listens to one chat: yours (`TELEGRAM_CHAT_ID`).

## Setup

1. In Telegram, open **@BotFather**, send `/newbot` and follow the prompts.
   You receive a token like `123456789:AA…`. Treat it like a password.
2. Open your new bot and press **Start**. A bot can only message you after you have written to it.
3. Find your **chat ID**: open `https://api.telegram.org/bot<TOKEN>/getUpdates` and read
   `"chat":{"id":…}`, or ask **@userinfobot**.
4. Configure Spider Notes, either:
   - in `.env`: `TELEGRAM_BOT_TOKEN` and `TELEGRAM_CHAT_ID`, or
   - in the app: **Settings → Telegram** (values saved here override `.env`).
5. Press **Send test message**.

Optional: set the bot's picture with `/setuserpic` in @BotFather using
`branding/spider-notes-logo.png`.

## Message format

```
🔔 You should not eat this this morning
📋 Task: Breakfast plan
🕒 Thu 29 Oct 07:30
(reminder #2)

[✅ Done] [⏰ 10m] [⏰ 1h] [⏭ Skip]
```

- The task line appears for reminders attached to a task.
- **Task alerts** (the *Alert* field of a task) look like this:

  ```
  🔔 📋 Write report
  ▶️ Start Sun 01 Mar 08:00
  🕒 Due Sun 01 Mar 10:00

  [✅ Done] [⏰ 10m] [⏰ 1h] [⏭ Skip]
  ```

  Done completes the task; Skip appears only for repeating tasks.
- The counter appears from the second send onwards (nag mode, snooze).
- **Skip** appears only for repeating reminders.

When you act (in Telegram or the web app), the message is edited to show the
result and its buttons are removed, e.g. `✅ Done at 08:20`,
`⏰ Snoozed until Thu 29 Oct 08:30`, `⏭ Skipped`, `⚠️ Missed`.

## Behaviour and security rules

| Rule | Detail |
|---|---|
| One chat only | Messages and button presses are accepted only from `TELEGRAM_CHAT_ID`. Everything from other chats is ignored silently. |
| Messages | In your chat, text is read as an expense; `/today`, `/week`, `/month`, `/help` are commands. Nothing else is accepted. |
| Validated callbacks | Button data is `o:<occurrence id>:<action>` or `e:<expense id>:<action>` (under Telegram's 64-byte limit) and must match a strict pattern. |
| Idempotent actions | Pressing a button on an already-handled reminder answers “Already handled” and updates the message. |
| Long polling | The server connects out to Telegram. No webhook, public URL or open port is needed. |
| Non-blocking delivery | Messages go through an outbox worker with retries, exponential backoff and Telegram's `retry_after` handling. The scheduler never waits on Telegram. |
| No secrets in logs | The bot token is redacted from all error and library log output. |

For API tokens and connecting other platforms, see [integrations.md](integrations.md).

## Expenses from Telegram

The date and time of every expense are recorded automatically. Send `/start` once to get
the button bar under the text box:

```
[        ➕ Add expense        ]
[ 📊 Today ]      [ 📅 Month ]
```

### Step by step

Tap **➕ Add expense** (or send `/add`) and answer four questions:

```
You: (tap ➕ Add expense)
Bot: 1/4 🧾 What did you spend on?                       [✖ Cancel]
You: Lunch
Bot: 2/4 💵 How much for Lunch?
     Tap Dollar or Riel, then type the price (e.g. 4.5).
     [✅ 💵 Dollar] [៛ Riel]
You: (tap ៛ Riel if needed), then type 16000
Bot: 3/4 🏷 Category for Lunch (16,000៛)?
     [🍜 Food & Drinks] [🛒 Groceries]
     [🛺 Transport]     [🛍️ Shopping]   …
     [⏭ No category]    [✖ Cancel]
You: (tap 🍜 Food & Drinks)
Bot: 4/4 📝 Description? (optional)
     Lunch · 16,000៛ · 🍜 Food & Drinks
     [💾 Save without description]
You: with the team        (or tap Save without description)
Bot: ✅ Saved
     🧾 Lunch — 16,000៛
     🍜 Food & Drinks
     🕒 Thu 29 Oct 12:30           [🏷 Category] [💱 Dollar] [🗑 Undo]
```

- **Dollar or Riel**: the currency of your last expense is preselected (✅). Typing a mark
  (`$4.50`, `8000r`) also works and wins over the buttons.
- A wrong price is simply asked again. Typing a category name (e.g. `drinks`) works like tapping one;
  a new name becomes a new category.
- Nothing is saved until the last step.
- **✖ Cancel** or `/cancel` stops the entry. An unfinished entry expires after 15 minutes.
- **📊 Today** and **📅 Month** can be tapped at any time, also in the middle of an entry.

### In one message

Or send **what** and **how much** in one message:

| You send | Recorded |
|---|---|
| `lunch 4.5` | Lunch · $4.50 (last-used currency) |
| `4.5 lunch` | the same; the order does not matter |
| `tuk tuk 8000r` · `8000៛ tuk tuk` · `tuk tuk 8000 riel` | Tuk tuk · 8,000៛ |
| `coffee $2.25 #food @aba` | Coffee · $2.25 · 🍜 Food & Drinks · paid with aba |
| `rent 300 usd #rent @bank_transfer` | Rent · $300.00 · 🏠 Rent · bank transfer |
| `2 coffees 3.5` | 2 coffees · $3.50 (the last number is the amount) |

- **Currency marks**: `$`, `usd` → USD · `r`, `៛`, `riel`, `khr` → KHR · `฿`, `baht`, `thb` → THB · `€`, `eur` → EUR.
  Without a mark, the currency of your last expense is used.
- **`#category`**: a short word is enough. `#food` and `#drinks` both match *Food & Drinks*; a word that
  matches nothing becomes a new category. Use `_` for spaces (`#pet_food`).
- **`@payment`**: e.g. `@cash`, `@aba`. Use `_` for spaces.

The bot answers with what it saved and buttons:

```
✅ Saved
🧾 Lunch — $4.50
🕒 Thu 29 Oct 12:30

Pick a category:
[🍜 Food & Drinks] [🛒 Groceries]
[🛺 Transport]     [🛍️ Shopping]   …
[💱 Riel] [🗑 Undo]
```

| Button | Does |
|---|---|
| a category | Sets the category (shown when you gave none) |
| 🏷 Category | Shows the category buttons again to change it |
| 💱 Riel / 💱 Dollar | Re-reads the same number in the other currency (`8000` USD → 8,000៛). Amounts with cents cannot become riel. |
| 🗑 Undo | Deletes the expense |

**Commands** (also in the `/` menu of the chat):

| Command | Shows |
|---|---|
| `/add` | Start a step-by-step entry (same as ➕ Add expense) |
| `/today` | Total spent today per currency, then per category (same as 📊 Today) |
| `/week` | The same for this week (Monday to Sunday) |
| `/month` | The same for this month (same as 📅 Month) |
| `/cancel` | Stop a step-by-step entry |
| `/help` | The examples above, and shows the button bar again |

Everything recorded here appears on the **Expenses** page and in the Excel export.

## Changing or disabling the bot

- Change the token or chat ID in **Settings → Telegram**; the bot reconnects immediately.
- To disable Telegram, tick **Remove token** and save. Reminders still appear on the
  Today page and in history.

## Troubleshooting

| Symptom | Check |
|---|---|
| Test message fails | Token correct? Did you press **Start** in the bot? Chat ID is your user ID (groups are negative numbers). |
| No `telegram connected` in logs | The server must reach `api.telegram.org` over HTTPS (outbound). |
| Buttons do nothing | Presses from a chat other than `TELEGRAM_CHAT_ID` are ignored by design. |
| Reminder arrived late | Quiet hours were active, or the scheduler interval (default 30 s) adds up to that delay. |
