# Operations

Commands below run in the project folder on the server (the folder with
`docker-compose.yml`). `docker compose` fails with “no configuration file
provided” when run from another folder.

## What to back up

| Data | Location (in the container) | How |
|---|---|---|
| Database (tasks, reminders, memos, notes, history, settings) | `/data/app.db` | `app backup` |
| Uploaded files | `/data/attachments/` | copy the folder |
| Configuration and secrets | `.env` on the host | copy the file |

Everything under `/data` lives in the Docker volume `spider-notes-data`.

## Backup

Online and safe while the app runs:

```bash
docker compose exec -T app /app backup --out /data/backup-$(date +%F).db
mkdir -p ~/spider-backups/$(date +%F)
docker compose cp app:/data/backup-$(date +%F).db ~/spider-backups/$(date +%F)/
docker compose cp app:/data/attachments ~/spider-backups/$(date +%F)/
```

Daily at 03:00 with cron (`crontab -e`):

```cron
0 3 * * * cd /home/vector/docker/note/Spider-Notes && docker compose exec -T app /app backup --out /data/backup-$(date +\%F).db && mkdir -p /home/vector/spider-backups/$(date +\%F) && docker compose cp app:/data/backup-$(date +\%F).db /home/vector/spider-backups/$(date +\%F)/ && docker compose cp app:/data/attachments /home/vector/spider-backups/$(date +\%F)/
```

Keep at least one copy on another machine.

## Restore

```bash
docker compose stop app
docker run --rm -v spider-notes_spider-notes-data:/data -v "$PWD":/b alpine sh -c \
  'cp /b/backup.db /data/app.db && rm -f /data/app.db-wal /data/app.db-shm && \
   cp -r /b/attachments /data/ 2>/dev/null; chown -R 65532:65532 /data'
docker compose start app
```

The volume name is `<project folder>_spider-notes-data`; list volumes with `docker volume ls`.

## Upgrading

```bash
docker compose exec -T app /app backup --out /data/before-upgrade.db
git pull
docker compose up -d --build
docker compose logs --tail=30
```

Database migrations are embedded and run automatically at start-up, each one
once and inside a transaction. Data in the volume is never touched by a rebuild.
After an upgrade, browsers fetch the new assets automatically (URLs are versioned).

## Import from Memos

The importer reads a **copy** of a Memos data folder, read-only. It keeps the original
dates, pinned state, `#tags` and attached files (from `assets/` or stored in the database).
Running it again never creates duplicates.

```bash
# 1. Back up the Memos data folder (stop Memos briefly so the DB is consistent)
docker stop memos
cp -a /path/to/memos-data ~/memos-backup-$(date +%F)
docker start memos

# 2. Copy the backup into Spider Notes and preview
docker compose cp ~/memos-backup-YYYY-MM-DD app:/data/memos-import
docker compose exec app /app import-memos --from /data/memos-import --dry-run

# 3. Import
docker compose exec app /app import-memos --from /data/memos-import
```

| Option | Effect |
|---|---|
| `--dry-run` | List what would be imported; write nothing |
| `--archived` | Also import archived memos (tagged `#archived`) |
| `-v` | Print every imported memo |

Images in the Memos `assets` folder that are not attached to any memo are not imported.
Files stored in S3 or as external links are reported as problems and not copied.

## Markdown export

**Settings → Export to Markdown** (or the daily export) writes to `APP_EXPORT_DIR`:

```
memos/YYYY-MM-DD.md              memos of that day under "## HH:MM" headings
history/YYYY-MM-DD.md            checklist of what was done / skipped / missed that day
tasks.md                         open tasks
notes/<folder path>/<title>.md   every note, keeping the folder structure
files/<folder path>/<file>       files uploaded into folders
attachments/<id>/<file>          files attached to memos and notes
```

**Download .zip** streams the same content. The export is read-only output; the database
remains the source of truth.

## Monitoring and logs

```bash
docker compose ps                  # health status
docker compose logs -f             # JSON logs
docker compose logs | grep -i telegram
```

- `GET /healthz` returns `ok` when the database is reachable.
- Logs are structured JSON (`log/slog`). Request logs contain method, path, status and
  duration. Query strings, passwords, tokens and the Telegram token are never logged.

## Troubleshooting

| Problem | Solution |
|---|---|
| `no configuration file provided` | `cd` into the folder that contains `docker-compose.yml`. |
| Container restarting | `docker compose logs app`. Usually a missing or short `APP_SESSION_SECRET`, or `APP_ADMIN_PASSWORD` under 10 characters on first start. |
| Page looks unstyled | Hard refresh (Ctrl+F5) after an upgrade. |
| `permission denied` on the Docker socket | Use `sudo`, or add your user to the `docker` group. |
| Forgot the password | Stop the app, restore a backup, or delete the `users` row with an SQLite tool and set `APP_ADMIN_*` again before starting. |
| Telegram not connected | See [Telegram troubleshooting](telegram.md#troubleshooting). |
| Upload rejected | File larger than `APP_MAX_UPLOAD_MB`, or empty. Behind a proxy, also raise the proxy's body size limit. |
