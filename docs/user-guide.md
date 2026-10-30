# User Guide

All times are shown in the configured time zone (default `Asia/Phnom_Penh`).
The left sidebar links to every page. On phones it opens from the ☰ button.

## Signing in

Open the app and sign in with the username and password created on first
start (`APP_ADMIN_USERNAME` / `APP_ADMIN_PASSWORD`). Sessions end after
`APP_SESSION_IDLE_TIMEOUT` of inactivity (default 12 hours). After 5 failed
attempts in one minute, sign-in is blocked for a minute.

## Today

The home page shows:

- **Needs attention**: reminders that fired and are waiting for an answer.
  Use **Done**, **Snooze** (10 min, 30 min, 1 hour, tomorrow morning) or **Skip**
  (repeating reminders only).
- **Overdue** and **Later today** tasks; tick the circle to complete.
- **Add a task…**: a one-line quick add with an optional due date.
- **Next 24 hours**: upcoming reminders.
- **Pinned** memos.

A red badge on *Today* in the sidebar counts reminders waiting for you.

## Spider Notes (memo timeline)

- Type in the editor and press **Enter** to save. **Shift+Enter** adds a new line.
- Toolbar: insert a `#tag`, a checkbox (`- [ ]`), code, or attach a file (📎).
  You can also drag a file onto the editor or paste a screenshot.
- Write `#tags` anywhere; they appear in the sidebar with counts. Click a tag to filter.
- Tick checkboxes directly in a saved memo.
- The `⋯` menu on a memo offers **Pin**, **Edit** and **Delete**.
- The sidebar shows totals and an activity heatmap; click a day to see its memos.
- Older memos load automatically as you scroll.

## Folders

A file manager for longer documents and files.

| Action | How |
|---|---|
| New note | **New note** (opens the editor) |
| New folder | **New folder** → type a name |
| Upload files | **Upload** (select several) or drag files onto the page |
| Rename / move / delete | The `⋯` menu on any folder, note or file |
| Pin a note to the top | `⋯` → **Pin to top** |
| Navigate | Folder tree on the right, or the path at the top (Folders / Work / …) |

**Notes** have a title and Markdown content. Use the **Write / Preview** tabs;
**Ctrl+S** saves without leaving the editor, **Ctrl+Enter** saves and closes. You
are warned before leaving with unsaved changes. Checkboxes can be ticked while reading.

**Files**: images and PDFs open in the browser; other types download. Files
attached to a note are listed under it. The maximum size per file is set by
`APP_MAX_UPLOAD_MB` (default 50 MB). If some files in a batch fail (too large,
empty), the others are still uploaded and the failures are listed.

> Deleting a folder deletes everything inside it (after a confirmation).
> Deleted items are hidden, not erased, in line with the app's history policy.

## Tasks

- **Tabs**: Pending / Done / All. **Chips**: any time, today, this week, overdue, no date.
- Filter by priority and tag, or search the text.
- **New task**: title, Markdown notes, start and due date/time, alert, priority, tags and repeat.
  - **Start** (optional) and **Due** give the task a time range, shown as `start → due` in the list.
  - **Alert** sends a Telegram message with Done / Snooze / Skip buttons: *None*, *At start time*,
    *At due time*, or 5 min / 15 min / 30 min / 1 h / 2 h / 1 day before the due time.
    An alert needs a due date. Global quiet hours apply.
  - Repeat: never, daily (every *N* days), weekly (choose weekdays), monthly (day of month), optional end date.
  - The repeat time comes from the due time; the start moves along, keeping its distance to the due time.
- Completing a repeating task records the completion and moves the due date to
  the next occurrence. **Skip occurrence** moves on without completing.
- **Save & add reminder** opens a reminder pre-filled for that task. Use reminders when you need
  more than one notification, nag mode, or a custom message; the alert covers the common case.
- The task page lists its reminders and its history.

## Reminders

Create one with **New reminder**:

1. **Message**: what Telegram will say (Markdown allowed, up to 4,000 characters).
2. **Attach to task** (optional): the message then includes the task title, and pressing
   Done also completes the task if it is due that day or earlier.
3. **When**: *One time* (date and time) or *Repeating*:
   - **Daily** / **Weekdays** / **Monthly** at a time of day,
   - **Hours** / **Minutes**: every *N* (minutes: at least 5) with an optional
     "only from … until …" window,
   - **Starts on**, optional **Ends on** and **Max times**.
4. **Nag mode**: re-send every *N* minutes until Done or Snooze.
5. **Quiet hours**: use the global setting, none, or a custom window; or
   **Ignore quiet hours** to always send.
6. **Enabled**: pause without deleting.

The list shows each reminder's schedule, next fire time and options. The toggle
pauses or resumes it. Resuming schedules from now; nothing missed while paused is sent.
See [Reminders & Scheduling](reminders-and-scheduling.md) for exact rules.

## Calendar

- **Month** and **Week** views; **‹ Today ›** to navigate.
- Colours: tasks (blue), reminders (purple), overdue (red), done (green, struck through),
  missed/skipped (amber/faded), paused (grey). A dot marks days with memos.
- Click a day for the side panel: everything on that day, its memos, and
  **+ Task** / **+ Reminder** buttons pre-filled with the date. Click an item to edit it.
- Reminders that fire many times a day (e.g. every 15 minutes) are shown once with a count.

## Expenses

Track what you spend.

- **Add an expense** at the top of the page:
  - **Item**: what you spent on, e.g. *Lunch*.
  - **Amount** and **currency**: USD, KHR, THB or EUR. Commas are fine (`12,000`). USD, THB and EUR take
    up to 2 decimals; KHR takes none. The currency you used last is preselected.
  - **Category**: click the box to open the list. Ready-made: 🍜 Food & Drinks, 🛒 Groceries, 🛺 Transport, 🛍️ Shopping,
    💡 Bills, 🏠 Rent, 📱 Phone & Internet, 💊 Health, 📚 Education, 🎬 Entertainment, 👪 Family,
    🎁 Gifts, ✈️ Travel, 📦 Other. Choose **＋ New category…** at the bottom of the list to type a new one;
    it is added to the list.
  - **Payment** (optional, e.g. *Cash*, *ABA*): values you used before are suggested.
  - **Description**: optional notes.
  - The **date and time are recorded automatically**; there is nothing to fill in.
- **Manage** (above the category box) opens the category list: add one with an optional emoji,
  rename it (existing expenses are renamed too), change its emoji, or remove it (expenses keep the name).
- **Views**: Day / Week / Month (default) / Year / All, with ‹ › and a date picker.
  Filter by category or search the text.
- The **summary** shows the total per currency and the number of expenses. Each day shows
  its own total. The right-hand panel breaks the period down **by category**; click a
  category to filter.
- Click an expense to **edit** or **delete** it. The edit page shows the recorded date and time;
  change it only to correct a mistake.
- **From Telegram**: tap **➕ Add expense** and answer what / price (dollar or riel) / category /
  description, or send one
  message such as `lunch 4.5` or `tuk tuk 8000r #transport`;
  see [Telegram › Expenses](telegram.md#expenses-from-telegram).
- **Export Excel** downloads the current view (same period, category and search) as an `.xlsx`
  file. It has two sheets:

  | Sheet | Contents |
  |---|---|
  | Expenses | Date & time, Item, Category, Amount, Currency, Payment, Description; oldest first, with filter buttons and a frozen header |
  | Summary | Totals per currency, then per category with count and share |

  Amounts are real numbers and dates are real dates in Excel, so you can sort, filter,
  sum and chart them. Currencies are never mixed in a total.

## History

- **Day / Week / Month** views with previous/next and a date picker.
- Filter by type (tasks/reminders), outcome (done/skipped/missed) and tag.
- Each entry shows the scheduled time, outcome time, source (web/telegram/api),
  number of sends and snoozes.
- **Completion rate**: per item, over the last 7 and 30 days.

## Search

Searches memos, notes, tasks and reminders. Every word matches as a prefix
(“doc” finds “doctor”); all words must match.

## Settings

| Section | Contents |
|---|---|
| Reminders | Global quiet hours, “tomorrow morning” snooze time, default nag interval, daily export time |
| Telegram | Bot token, chat ID, **Send test message** |
| Export | **Export to Markdown** (to the server folder), **Download .zip** |
| Account | Change password (signs out other sessions) |
| API | Create and revoke personal API tokens |

## Themes and devices

The ◐ **Theme** button cycles system → light → dark. The layout adapts to
phones: the sidebar becomes a drawer and side panels move below the content.
