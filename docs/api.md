# API Reference

Every action in the web app is also available as JSON under **`/api/v1`**.
The complete machine-readable description is [`openapi.yaml`](../openapi.yaml),
also served by the app at `/openapi.yaml`. It is suitable for generating clients
or LLM tool definitions.

## Authentication

**Personal API tokens** (recommended for scripts):

1. In the app: **Settings → API tokens → Create**. The token (`mnt_…`) is shown once.
2. Send it as a header: `Authorization: Bearer mnt_…`
3. Revoke it from the same page at any time. Only a SHA-256 hash is stored.

**Session cookie**: requests from the web UI. State-changing requests must also send
the `X-CSRF-Token` header.

## Conventions

- JSON request and response bodies (`Content-Type: application/json`); unknown fields are rejected.
- Timestamps are RFC 3339 **with the local offset**, e.g. `2026-10-29T07:30:00+07:00`.
- Query parameters `from` / `to` accept RFC 3339 or `YYYY-MM-DD` (local midnight); `to` is exclusive.
- `POST` that creates a resource returns `201`; `DELETE` returns `204`.
- Errors always have this shape:

```json
{ "error": { "code": "validation", "message": "Message is required" } }
```

| HTTP | `code` | When |
|---|---|---|
| 400 | `validation` | Invalid input; the message is user-readable |
| 401 | `unauthorized` | Missing, invalid or revoked token |
| 403 | `csrf` | Session request without a valid `X-CSRF-Token` |
| 404 | `not_found` | Unknown id or endpoint |
| 409 | `conflict` | Already handled (e.g. completing a done task) |
| 500 | `internal` | Unexpected error (details are only in the server log) |

## Endpoints

### Tasks
| Method | Path | Description |
|---|---|---|
| GET | `/tasks?status=&range=&tag=&priority=&q=&limit=` | List. `status`: pending (default) / done / all. `range`: today / week / overdue / nodate |
| POST | `/tasks` | Create |
| GET / PUT / DELETE | `/tasks/{id}` | Read / replace / soft delete |
| POST | `/tasks/{id}/complete` · `/uncomplete` · `/skip` | Complete (repeating: advance) / reopen / skip occurrence |

### Reminders
| Method | Path | Description |
|---|---|---|
| GET | `/reminders?task_id=` | List |
| POST | `/reminders` | Create |
| GET / PUT / DELETE | `/reminders/{id}` | Read / replace (recomputes next fire) / soft delete |
| POST | `/reminders/{id}/pause` · `/resume` | Pause / resume |

### Occurrences
| Method | Path | Description |
|---|---|---|
| GET | `/occurrences?from=&to=&status=sent,snoozed&kind=&tag=&reminder_id=&task_id=` | List |
| GET | `/occurrences/{id}` | Read |
| POST | `/occurrences/{id}/done` · `/skip` | Act |
| POST | `/occurrences/{id}/snooze` | Body `{"minutes": 10\|30\|60}` or `{"until": "morning"}` |

### Memos, notes, folders, files
| Method | Path | Description |
|---|---|---|
| GET | `/memos?q=&tag=&before=&limit=` | List newest first; use `next_before` for the next page |
| POST / GET / PUT / DELETE | `/memos`, `/memos/{id}` | `{"content_md": "…"}` |
| POST | `/memos/{id}/pin` · `/unpin` | Pin state |
| GET / POST | `/folders` | List all (with paths) / create `{"name", "parent_id"}` |
| GET | `/folders/{id}` | Contents (subfolders, notes, files); `0` = top level |
| PUT / DELETE | `/folders/{id}` | Rename `{"name"}`, move `{"parent_id": 5}` or to the top level `{"parent_id": null, "move": true}` / delete recursively |
| GET / POST | `/notes?folder_id=` · `/notes` | List / create `{"title", "content_md", "folder_id"}` |
| GET / PUT / DELETE | `/notes/{id}` | PUT accepts any of `title`, `content_md`, `folder_id` (0 = top), `pinned` |
| GET | `/attachments?folder_id=\|memo_id=\|note_id=` | List files |
| POST | `/attachments` | Multipart upload: `file` plus optional `folder_id` / `memo_id` / `note_id` |
| GET / DELETE | `/attachments/{id}` | Metadata / delete |
| GET | `/attachments/{id}/content` | Download (supports `Range`) |

### Expenses
| Method | Path | Description |
|---|---|---|
| GET | `/expenses?from=&to=&category=&currency=&q=&limit=` | List, newest first (default limit 500) |
| POST | `/expenses` | Create `{"item", "amount", "currency", "category", "payment", "note", "spent_at"}`; `spent_at` defaults to now |
| GET / PUT / DELETE | `/expenses/{id}` | Read / replace (omit `spent_at` to keep it) / delete |
| GET | `/expenses/summary?from=&to=&category=&currency=&q=` | Totals per currency and per category |
| GET | `/expenses/export.xlsx?from=&to=&category=&currency=&q=` | Excel download |
| GET / POST | `/expense-categories` | List / add `{"name", "emoji"}` |
| PUT / DELETE | `/expense-categories/{id}` | Rename or change emoji (expenses follow) / remove from the list |

`amount` accepts a number (`12.5`) or a string (`"12.50"`). A `category` that does not exist yet
is added to the category list; an existing one matches regardless of case.

### Views
| Method | Path | Description |
|---|---|---|
| GET | `/calendar?from=&to=` | Expanded items (max 100 days) and `memo_days` |
| GET | `/history?from=&to=&kind=&tag=&status=` | Done / skipped / missed occurrences |
| GET | `/stats` | Completion rates (7 and 30 days) |
| GET | `/tags?kind=memo\|task\|reminder\|note` | Tags with counts |
| GET | `/search?q=&limit=` | Full-text search across memos, notes, tasks, reminders |

## Repeat rules in requests

```jsonc
"repeat": {
  "freq": "weekly",            // daily | weekly | monthly | hourly | minutely
  "interval": 1,
  "weekdays": [1, 3, 5],       // 0 = Sunday … 6 = Saturday
  "month_day": 31,             // monthly
  "time": "07:30",             // daily / weekly / monthly reminders
  "window": "08:00-20:00",     // hourly / minutely
  "until": "2026-12-31",
  "count": 0
}
```

`"repeat": null` (or omitted) means one-time. Tasks take the time from `due_at`.

## Task start and alert

```jsonc
{
  "title": "Write report",
  "start_at": "2026-11-02T08:00:00+07:00",   // optional, not after due_at
  "due_at":   "2026-11-02T10:00:00+07:00",
  "alert": "15m"                             // "" | start | due | 5m | 15m | 30m | 1h | 2h | 1d
}
```

Responses include `next_alert_at` while an alert is scheduled. Alerts are delivered as
occurrences, so `/occurrences/{id}/done`, `/skip` and `/snooze` work on them.

## Examples

```bash
H="Authorization: Bearer $TOKEN"; B=http://localhost:8080/api/v1

# Create a repeating reminder with nag mode
curl -s -H "$H" -X POST $B/reminders -d '{
  "message_md": "You should not eat this this morning",
  "fire_at": "2026-11-01T00:00:00+07:00",
  "repeat": {"freq": "weekly", "weekdays": [1,3,5], "time": "07:30"},
  "nag_enabled": true, "nag_interval_min": 10 }'

# What is waiting for me?
curl -s -H "$H" "$B/occurrences?status=sent,snoozed"

# Snooze one for 30 minutes
curl -s -H "$H" -X POST $B/occurrences/42/snooze -d '{"minutes": 30}'

# Capture a memo and upload a file into a folder
curl -s -H "$H" -X POST $B/memos -d '{"content_md": "Slept well #health"}'
curl -s -H "$H" -F folder_id=3 -F file=@report.pdf $B/attachments

# Record an expense and download this month as Excel
curl -s -H "$H" -X POST $B/expenses -d '{"item": "Lunch", "amount": 4.5, "category": "Food", "payment": "Cash"}'
curl -s -H "$H" -o expenses.xlsx "$B/expenses/export.xlsx?from=2026-10-01&to=2026-11-01"

# Search everything
curl -s -H "$H" "$B/search?q=doctor"
```
