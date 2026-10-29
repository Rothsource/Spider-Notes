# Configuration

Spider Notes is configured with environment variables (normally in `.env`,
loaded by Docker Compose) and with settings saved from the web app.

## Environment variables

### Required

| Variable | Description |
|---|---|
| `APP_ADMIN_USERNAME` | Username of the single account. Used only on first start, when no user exists. |
| `APP_ADMIN_PASSWORD` | Initial password (min. 10 characters). Hashed with argon2id; ignored once the account exists. Change it later in Settings. |
| `APP_SESSION_SECRET` | At least 32 characters; signs session cookies. Generate with `openssl rand -base64 48`. Changing it signs everyone out. |

### Optional

| Variable | Default | Description |
|---|---|---|
| `APP_LISTEN` | `:8080` | HTTP listen address |
| `APP_DB_PATH` | `/data/app.db` | SQLite database file |
| `APP_TIMEZONE` | `Asia/Phnom_Penh` | IANA time zone for all scheduling and display |
| `APP_SESSION_IDLE_TIMEOUT` | `12h` | Sign out after this much inactivity (Go duration: `30m`, `12h`, `168h`) |
| `APP_SCHEDULER_INTERVAL` | `30s` | How often the scheduler checks for due reminders (min. `1s`) |
| `APP_EXPORT_DIR` | `/data/export` | Where Markdown exports are written |
| `APP_ATTACHMENTS_DIR` | `/data/attachments` | Where uploaded files are stored |
| `APP_MAX_UPLOAD_MB` | `50` | Maximum size of one uploaded file (1–2048) |
| `APP_TRUST_PROXY` | `false` | Trust `X-Forwarded-For` / `X-Forwarded-Proto` (only behind your own reverse proxy) |
| `APP_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `TELEGRAM_BOT_TOKEN` | — | Bot token from @BotFather. Empty = Telegram disabled. |
| `TELEGRAM_CHAT_ID` | — | The only chat that receives reminders and may press buttons |

Invalid values stop the server at start-up with a clear error message.

## In-app settings

Saved in the database from **Settings**. They take effect immediately.

| Setting | Default | Description |
|---|---|---|
| Quiet hours | off | Daily window during which messages are held, e.g. `22:00–06:30` |
| “Tomorrow morning” snooze | `07:00` | Time used by the snooze-until-morning action |
| Default nag interval | `30` min | Pre-filled interval for new nagging reminders |
| Daily Markdown export | off, `23:55` | Writes the export folder once a day after this time |
| Telegram token / chat ID | from `.env` | **Once saved here, these override the environment variables** |

## Limits

| Item | Limit |
|---|---|
| Memo | 20,000 characters |
| Note | 200,000 characters |
| Reminder message | 4,000 characters (Telegram allows 4,096) |
| Task title | 300 characters |
| Folder / file name | 120 / 200 characters |
| Folder nesting | 32 levels |
| Uploaded file | `APP_MAX_UPLOAD_MB` (default 50 MB) |
| Minute-based repeat | every 5 minutes or more |
| Calendar API range | 100 days |
| Login attempts | 5 per minute per IP |
| Password | 10–256 characters |
