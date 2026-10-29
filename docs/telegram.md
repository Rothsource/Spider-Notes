# Telegram Bot

Spider Notes uses a Telegram bot to deliver reminders. The bot is **send-only**:
it accepts no commands, and it only reacts to button presses from one chat.

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
- The counter appears from the second send onwards (nag mode, snooze).
- **Skip** appears only for repeating reminders.

When you act (in Telegram or the web app), the message is edited to show the
result and its buttons are removed, e.g. `✅ Done at 08:20`,
`⏰ Snoozed until Thu 29 Oct 08:30`, `⏭ Skipped`, `⚠️ Missed`.

## Behaviour and security rules

| Rule | Detail |
|---|---|
| One chat only | Button presses are accepted only from `TELEGRAM_CHAT_ID`. Everything from other chats is ignored silently. |
| No commands | Text messages are ignored. The owner's chat gets a single reply, “This bot only sends reminders.” |
| Validated callbacks | Button data is `o:<occurrence id>:<action>` (under Telegram's 64-byte limit) and must match a strict pattern. |
| Idempotent actions | Pressing a button on an already-handled reminder answers “Already handled” and updates the message. |
| Long polling | The server connects out to Telegram. No webhook, public URL or open port is needed. |
| Non-blocking delivery | Messages go through an outbox worker with retries, exponential backoff and Telegram's `retry_after` handling. The scheduler never waits on Telegram. |
| No secrets in logs | The bot token is redacted from all error and library log output. |

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
