# Spider Notes 🕷️

Spider Notes is a self-hosted personal **to-do list, reminder, calendar and memo** app for one person.
It is a single Go binary with SQLite, a server-rendered web UI (htmx), and a
**send-only Telegram bot** that delivers reminders with ✅ Done / ⏰ Snooze / ⏭ Skip buttons.

- Times are scheduled and shown in `Asia/Phnom_Penh` (configurable).
- Data stays on your server in one SQLite file.
- It ships with a JSON API (`/api/v1`) and an OpenAPI description, so an LLM or a script can use it later.

---

## Pages

| Page | What it does |
|---|---|
| **Today** (`/`) | Reminders waiting for you (Done/Snooze/Skip), overdue tasks, tasks later today, reminders in the next 24h, pinned memos, quick-add box. |
| **Tasks** | Create/edit/delete tasks with Markdown description, due date, priority, tags and repeat rules (daily / every N days, weekly on chosen weekdays, monthly on day D). Filter by pending/done/all, today/this week/overdue/no date, tag, priority, and text search. Click the circle to complete. A repeating task records the completion and moves to its next occurrence. |
| **Reminders** | Standalone or attached to a task. One-time, or repeating: daily at HH:MM, on weekdays, monthly on day D (31 means "last day" in short months), every N hours/minutes with an optional active window, with an end date or max count. Options: **nag mode** (re-send every N minutes until Done/Snooze), per-reminder quiet-hours override, "ignore quiet hours", pause/resume. Each reminder shows its history. |
| **Calendar** | Month and week (hourly grid) views with prev/next/today. Tasks and reminders have different colours, and overdue, done, skipped, missed and paused items look different. Repeating items appear on every day they occur. A ● marks days with memos. Click a day to open a side panel with everything on it and buttons to add a task or reminder for that date. |
| **Spider Notes** (memo timeline) | Memos-app style capture: type and press **Enter** (Shift+Enter for a new line). Timeline grouped by day with infinite scroll. Markdown (GFM, sanitized), clickable `- [ ]` checkboxes, `#tags` parsed automatically with a tag sidebar, search, pin, edit, delete. |
| **Folders** | A small file manager for longer notes and files. Create nested folders, titled Markdown notes (Write/Preview, Ctrl+S to save, clickable checkboxes, #tags) and upload any file (drag and drop works). Every folder, note and file can be renamed, moved or deleted from its `⋯` menu. Deleting a folder deletes everything inside it. Images and PDFs open in the browser; other types download. |
| **Attachments** | In any memo or note editor, use 📎, drag and drop, or paste a screenshot to attach a file. Images appear inline. |
| **History** | Everything done, skipped or missed, by day/week/month, filterable by type, outcome and tag. Shows the completion rate per reminder/task over the last 7 and 30 days. |
| **Search** | Full-text search (SQLite FTS5) across memos, tasks and reminders. |
| **Settings** | Global quiet hours, the "snooze until tomorrow morning" time, default nag interval, daily auto-export, Telegram token/chat ID with a **Send test message** button, Markdown export and .zip download, change password, API tokens. |

Light/dark theme follows your system setting. Use the ◐ button to override it. The layout works on phones.

---

## Quick start (Docker)

### 1. Create the Telegram bot (optional but recommended)

1. In Telegram, open **@BotFather** and send `/newbot`. Pick a name and a username.
2. BotFather replies with a **token** like `123456789:AAH...`. Keep it secret.
3. Open your new bot and press **Start** (send it any message). A bot can only message you after you have written to it first.
4. Get your **chat ID**: open `https://api.telegram.org/bot<TOKEN>/getUpdates` in a browser.
   Find `"chat":{"id":123456789,...}`. That number is your chat ID.
   (Or message **@userinfobot**, which replies with your ID.)

### 2. Configure

```bash
cp .env.example .env
openssl rand -base64 48        # paste the output into APP_SESSION_SECRET
```

Edit `.env`: set `APP_ADMIN_USERNAME`, `APP_ADMIN_PASSWORD` (at least 10 characters),
`APP_SESSION_SECRET`, and optionally `TELEGRAM_BOT_TOKEN` and `TELEGRAM_CHAT_ID`.

### 3. Run

```bash
docker compose up -d
docker compose logs -f        # look for "http server listening" and "telegram connected"
```

Open `http://<server>:8080` and log in. Go to **Settings → Send test message** to check Telegram.

> The app speaks plain HTTP. If you reach it over the internet, put it behind a
> reverse proxy with HTTPS (Caddy, nginx, Traefik) and set `APP_TRUST_PROXY=true`.
> Cookies are then marked `Secure` automatically. On a private network or VPN
> (e.g. Tailscale) plain HTTP is fine.

### Run without Docker

```bash
go build -o app ./cmd/app
APP_DB_PATH=./app.db APP_EXPORT_DIR=./export APP_ADMIN_USERNAME=me \
APP_ADMIN_PASSWORD=change-me-please APP_SESSION_SECRET=$(openssl rand -base64 48) ./app serve
```

Requires Go 1.27+. No CGO and no C compiler: SQLite is pure Go.

---

## Configuration

All settings are environment variables (see `.env.example`).

| Variable | Default | Notes |
|---|---|---|
| `APP_LISTEN` | `:8080` | HTTP bind address |
| `APP_DB_PATH` | `/data/app.db` | SQLite file (WAL mode, foreign keys on) |
| `APP_TIMEZONE` | `Asia/Phnom_Penh` | All scheduling and display use this zone |
| `APP_ADMIN_USERNAME` | (required on first run) | Creates the single account if none exists |
| `APP_ADMIN_PASSWORD` | (required on first run) | Hashed with argon2id; ignored once the user exists |
| `APP_SESSION_SECRET` | (required) | ≥ 32 chars; signs session cookies |
| `APP_SESSION_IDLE_TIMEOUT` | `12h` | Auto-logout after inactivity |
| `TELEGRAM_BOT_TOKEN` | (empty) | Empty = Telegram disabled; the app still works |
| `TELEGRAM_CHAT_ID` | (empty) | The only chat that receives messages and may press buttons |
| `APP_SCHEDULER_INTERVAL` | `30s` | How often due reminders are checked |
| `APP_EXPORT_DIR` | `/data/export` | Markdown export location |
| `APP_ATTACHMENTS_DIR` | `/data/attachments` | Uploaded files (stored under random names) |
| `APP_MAX_UPLOAD_MB` | `50` | Maximum size of one uploaded file |
| `APP_TRUST_PROXY` | `false` | Trust `X-Forwarded-For` / `X-Forwarded-Proto` from a reverse proxy |
| `APP_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` (JSON logs to stdout) |

Settings changed in the web UI (Telegram token/chat ID, quiet hours, nag and snooze
defaults, auto-export) are stored in the database. **Once saved, database values
override the environment.**

---

## How reminders behave

- **Telegram message**: the message text, the linked task title, the scheduled time, `(reminder #3)` on nags, and buttons **✅ Done**, **⏰ 10m**, **⏰ 1h**, **⏭ Skip** (Skip only on repeating items). Pressing a button updates the database, stops nags, and edits the message to show the result (e.g. "✅ Done at 08:20") with the buttons removed. Actions taken in the web UI edit the Telegram message too.
- **The bot accepts no commands.** It replies once with "This bot only sends reminders." to your own chat and ignores every other chat and every button press from anyone else.
- **Nag mode** re-sends every N minutes until the occurrence is Done or Snoozed. When a nag is sent, the previous message loses its buttons.
- **Quiet hours** (global, e.g. `22:00–06:30`, plus a per-reminder override) hold messages and send them when quiet hours end. A reminder marked "ignore quiet hours" is always sent.
- **Snooze**: 10 min, 30 min, 1 hour, or until tomorrow morning (time set in Settings).
- **Missed**: an occurrence still open when the next one arrives is marked *missed*. One without nag mode is also marked missed after 24 hours.
- **Restart-safe**: all progress lives in the database (`next_fire_at`, occurrence rows, `next_send_at`), and the DB is updated *before* a message is queued, so a restart never sends twice. After downtime only **one catch-up message** per reminder is sent, and older occurrences are recorded as missed.
- Pausing a reminder and resuming it later schedules from "now". Nothing that was missed while paused gets sent.
- Telegram calls run on a separate worker with retries, exponential backoff and `retry_after` handling, so a slow Telegram never blocks the scheduler.
- **Attached reminders**: pressing Done on a reminder attached to a task also completes the task (the current occurrence, for repeating tasks). Completing the task stops that reminder's nags.

**History is never overwritten or hard-deleted.** Deleting a task, reminder or memo
only sets `deleted_at`, and open occurrences of a deleted reminder are closed as *skipped*.

---

## Backup and restore

The database is one file on the `spider-notes-data` volume. Make a consistent online
backup (`VACUUM INTO`) while the app is running:

```bash
docker compose exec app /app backup                        # -> /data/backup-YYYY-MM-DD.db
docker compose exec app /app backup --out /data/before-upgrade.db
docker compose cp app:/data/backup-2026-10-29.db ./        # copy it off the server
```

**Restore:**

```bash
docker compose stop app
docker compose cp ./backup-2026-10-29.db app:/data/app.db
docker compose start app
```

(If the copied file ends up owned by root, run `docker compose run --rm --user root --entrypoint /bin/sh app` with an image that has a shell, or restore into the volume with a helper container such as `docker run --rm -v spider-notes_spider-notes-data:/data -v $PWD:/b alpine sh -c "cp /b/backup.db /data/app.db && chown 65532:65532 /data/app.db && rm -f /data/app.db-wal /data/app.db-shm"`.)

**Uploaded files** live in `/data/attachments`, not in the database, so back them up too:

```bash
docker compose cp app:/data/attachments ./attachments-backup
```

Back up the `.env` file too: it holds the session secret and the initial credentials.

## Markdown export

**Settings → Export to Markdown** (or the optional daily auto-export) writes to `APP_EXPORT_DIR`:

```
memos/YYYY-MM-DD.md     that day's memos, each under "## HH:MM", tags kept
history/YYYY-MM-DD.md   checklist of what was done / skipped / missed that day, with times
tasks.md                current open tasks
notes/<folder path>/<title>.md   every note, keeping the folder structure
files/<folder path>/<file>       files uploaded into folders
attachments/<id>/<file>          files attached to memos and notes
```

**Download .zip** gives the same files in one archive. The database stays the
source of truth; the export is read-only output and is overwritten on each run.
Copy it out with `docker compose cp app:/data/export ./export`.

## Upgrading

```bash
docker compose exec app /app backup --out /data/before-upgrade.db
git pull
docker compose up -d --build
```

Schema migrations are embedded and applied automatically at startup. Each runs
once, inside a transaction.

---

## API

Every UI action is also available as JSON under **`/api/v1`**. The full
description is in [`openapi.yaml`](openapi.yaml), also served at `/openapi.yaml`.
It is meant for scripts and for giving an LLM tools later.

**Create a token:** Settings → API tokens → name it → **Create token**. The token
(`mnt_...`) is shown **once**. Only its hash is stored. You can revoke it at any time.

```bash
TOKEN=mnt_xxx
H="Authorization: Bearer $TOKEN"

# tasks
curl -H "$H" "http://localhost:8080/api/v1/tasks?range=today"
curl -H "$H" -X POST http://localhost:8080/api/v1/tasks -d '{
  "title": "Pay rent", "due_at": "2026-11-30T09:00:00+07:00", "tags": ["home"],
  "repeat": {"freq": "monthly", "month_day": 31}}'
curl -H "$H" -X POST http://localhost:8080/api/v1/tasks/1/complete

# reminders
curl -H "$H" -X POST http://localhost:8080/api/v1/reminders -d '{
  "message_md": "You should not eat this this morning",
  "fire_at": "2026-11-01T00:00:00+07:00",
  "repeat": {"freq": "weekly", "weekdays": [1,3,5], "time": "07:30"},
  "nag_enabled": true, "nag_interval_min": 10}'
curl -H "$H" -X POST http://localhost:8080/api/v1/reminders/1/pause

# occurrences (each firing)
curl -H "$H" "http://localhost:8080/api/v1/occurrences?status=sent,snoozed"
curl -H "$H" -X POST http://localhost:8080/api/v1/occurrences/5/snooze -d '{"minutes": 30}'

# memos, search, calendar, history
curl -H "$H" -X POST http://localhost:8080/api/v1/memos -d '{"content_md": "Slept well #health"}'
curl -H "$H" "http://localhost:8080/api/v1/search?q=slept"
curl -H "$H" "http://localhost:8080/api/v1/calendar?from=2026-11-01&to=2026-12-01"
curl -H "$H" "http://localhost:8080/api/v1/history?from=2026-10-01"
```

Endpoints: `folders` (list, create, contents, rename/move, delete), `notes` (CRUD, move, pin),
`attachments` (multipart upload, list, metadata, `/{id}/content` download, delete), `tasks` (CRUD, `complete`, `uncomplete`, `skip`), `reminders` (CRUD,
`pause`, `resume`), `occurrences` (list, `done`, `snooze`, `skip`), `memos`
(CRUD, `pin`, `unpin`, search with `q`), `tags`, `calendar`, `history`, `stats`, `search`.

- Timestamps are RFC 3339 with the local offset (`+07:00`). `from`/`to` also accept `YYYY-MM-DD`.
- Errors look like `{"error": {"code": "validation", "message": "..."}}`, with codes `validation` (400), `unauthorized` (401), `csrf` (403), `not_found` (404), `conflict` (409, e.g. already done), `internal` (500).
- The web session cookie also works for the API, but state-changing requests must then send the `X-CSRF-Token` header.

---

## Security notes

- One user, no sign-up. Passwords are hashed with argon2id. The login is rate-limited to 5 attempts per minute per IP.
- Server-side sessions with a signed, HttpOnly, SameSite=Lax cookie (Secure over HTTPS) and an idle timeout. Changing the password logs out other sessions.
- CSRF tokens on every state-changing request. Strict Content-Security-Policy (no inline scripts), `X-Frame-Options: DENY`.
- Markdown is rendered with goldmark and sanitized with bluemonday.
- Secrets never appear in logs: the Telegram token is redacted from library and HTTP errors, and query strings are not logged.
- The container runs as a non-root user on a distroless image.

## Development

```bash
go test ./...          # unit + integration tests (temp-file SQLite, mocked Telegram)
go vet ./...
golangci-lint run      # config in .golangci.yml
```

Layout:

```
cmd/app/            main: serve, backup, healthcheck, version
internal/config/    environment configuration
internal/model/     domain types
internal/store/     repository interfaces; sqlite/ implementation
migrations/         embedded SQL migrations
internal/schedule/  recurrence rules (RRULE subset), next-fire calculation, quiet hours
internal/service/   business logic: tasks, reminders, scheduler engine, memos, calendar, history, export
internal/scheduler/ background tick loop
internal/telegram/  outbox worker + long-polling callback handler
internal/auth/      passwords, sessions, CSRF, rate limit, API tokens
internal/api/       JSON API
internal/web/       HTML handlers, templates, static assets (embedded)
```

To add PostgreSQL later, implement `store.Store` in a new package. The services
only depend on the interface. The SQL uses `?` placeholders and plain types, so
porting is mechanical.
