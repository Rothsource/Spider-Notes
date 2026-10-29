# Overview

Spider Notes keeps everything a single person needs to organise their day in
one private place they host themselves:

- **Tasks** with due dates, priorities, tags and repeat rules.
- **Reminders** that arrive in Telegram at the right local time, with
  ✅ Done / ⏰ Snooze / ⏭ Skip buttons that work from the chat.
- **Calendar** month and week views that expand repeating items.
- **Spider Notes timeline**: fast, Memos-style capture of short notes.
- **Folders**: nested folders of titled Markdown notes and uploaded files.
- **History** of everything completed, skipped or missed, with completion rates.
- **Expenses**: record what you spend, see totals per period and category, export to Excel.
- **Search** across memos, notes, tasks and reminders.
- **Export** of everything to plain Markdown files.
- **JSON API** with personal tokens, described by an OpenAPI file.

There is exactly one user account. There is no sign-up and there are no roles.

## Key concepts

| Concept | Meaning |
|---|---|
| **Task** | Something to do. Optional due date/time, priority (low/normal/high), tags, Markdown description. A task can repeat (daily, weekly on chosen days, monthly). |
| **Reminder** | A message delivered to Telegram at a time. Either *standalone* or *attached to a task*. One-time or repeating. |
| **Occurrence** | One firing of a reminder or one due instance of a task. Every outcome (done, snoozed, skipped, missed) is recorded on an occurrence row, which forms the history. |
| **Nag mode** | A reminder setting that re-sends the message every N minutes until you press Done or Snooze. |
| **Quiet hours** | A daily window (e.g. 22:00–06:30) during which messages are held and delivered when it ends. |
| **Memo** | A short note in the Spider Notes timeline. Markdown, `#tags`, clickable checkboxes, pinning. |
| **Note** | A longer titled Markdown document stored in a folder. |
| **Attachment** | An uploaded file. It lives in a folder or belongs to a memo or note. |

## Feature summary

### Tasks
- Create, edit, complete, reopen, delete (soft delete keeps history).
- Filters: pending / done / all; today / this week / overdue / no date; tag; priority; text search.
- Repeating tasks advance to the next occurrence when completed or skipped. If an occurrence is not done before the next one arrives, it is recorded as *missed*.

### Reminders
- One-time, or repeating: daily at a time, on chosen weekdays, monthly on day *D*
  (29–31 fall back to the last day of shorter months), or every *N* hours or minutes
  with an optional active window. Optional end date and maximum count.
- Nag mode with a chosen interval; global and per-reminder quiet hours; an
  "ignore quiet hours" switch.
- Pause/resume without deleting. Editing a schedule recalculates the next fire time immediately.
- Snooze: 10 minutes, 30 minutes, 1 hour, or until tomorrow morning (configurable).

### Calendar
- Month and week (hourly grid) views with prev/next/today navigation.
- Tasks and reminders in different colours. Overdue, done, skipped, missed and paused items look different.
- Repeating items appear on every day they occur. Days with memos show a marker.
- Clicking a day opens a side panel with everything on it and buttons to add a task or reminder for that date.

### Spider Notes timeline (memos)
- Type and press **Enter** to save; **Shift+Enter** inserts a new line.
- Markdown (GitHub-flavoured), sanitised; `- [ ]` checkboxes can be ticked in place.
- `#tags` are detected automatically; a tag sidebar shows counts.
- Pin, edit, delete; activity heatmap; infinite scroll; attachments by 📎, drag-and-drop or paste.

### Folders, notes and files
- Nested folders; titled Markdown notes with Write/Preview tabs and **Ctrl+S** to save.
- Upload any file (multi-select or drag-and-drop). Images and PDFs open in the browser; other types download.
- Rename, move and delete every folder, note and file. Deleting a folder deletes its contents.

### Expenses
- Item, amount, currency (USD, KHR, THB, EUR), category, payment method and description.
- Ready-made categories with emoji, plus your own; date and time are recorded automatically.
- Day / week / month / year / all views with totals per currency and a per-category breakdown.
- **Export Excel** (.xlsx) of the current view: an *Expenses* sheet with filters and a *Summary* sheet.

### History and statistics
- Every occurrence outcome with its time and source (web, Telegram, API).
- Day / week / month views; filter by type, outcome and tag.
- Completion rate per reminder and task over the last 7 and 30 days.

### Export and import
- Markdown export to a folder (manual or daily) and as a `.zip` download.
- One-command import from a [Memos](https://usememos.com) data folder (keeps dates, pins, tags and files).

## Out of scope

The following are intentionally not part of the current version. The API and
schema are kept clean so they can be added later.

- Creating tasks or reminders from Telegram (the bot sends reminders and records expenses).
- Multiple users or shared spaces.
- External calendar sync (Google Calendar, iCal).
- AI features (the JSON API and OpenAPI file are ready for LLM tool use).
