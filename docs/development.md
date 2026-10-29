# Development

## Prerequisites

- Go **1.27.3** or newer (`go.mod` pins the version; older Go downloads it automatically)
- Git
- Docker (optional, for image builds)
- No C compiler is required: SQLite is a pure-Go driver

## Run locally

```bash
git clone https://github.com/Rothsource/Spider-Notes.git
cd Spider-Notes
go build -o app ./cmd/app

APP_DB_PATH=./data/app.db APP_EXPORT_DIR=./data/export APP_ATTACHMENTS_DIR=./data/attachments \
APP_ADMIN_USERNAME=dev APP_ADMIN_PASSWORD=dev-password-123 \
APP_SESSION_SECRET=dev-secret-dev-secret-dev-secret-0 APP_SCHEDULER_INTERVAL=5s \
./app serve
```

Open <http://localhost:8080>. Templates and static files are embedded, so rebuild after changing them.

## Tests and checks

```bash
gofmt -l .                 # must print nothing
go vet ./...
go test ./...              # unit + integration tests (temp-file SQLite, mocked Telegram)
go test -race ./...        # race detector (needs a C toolchain; runs in CI)

go run golang.org/x/vuln/cmd/govulncheck@latest ./...
go run github.com/securego/gosec/v2/cmd/gosec@latest -exclude-dir=.claude ./...
```

What the tests cover:

| Area | Package |
|---|---|
| Next-fire calculation (daily, weekdays, month end, every N hours, windows, end date, count), quiet hours | `internal/schedule` |
| Nag, snooze, skip, quiet-hour holding, restart catch-up, missed, pause/resume, repeating tasks, export | `internal/service` |
| Folders (cycles, duplicates, recursive delete), notes, uploads (limits, sniffing, names) | `internal/service` |
| Store integration, migrations, FTS sync, backup | `internal/store/sqlite` |
| Markdown sanitising, checkbox toggling, tag parsing | `internal/markdown` |
| Telegram delivery, retries, callbacks, chat restriction, token redaction | `internal/telegram` |
| Login, CSRF, rate limiting, every page rendering, API endpoints, file serving headers | `internal/api` |
| Memos import (new and old schemas, files, idempotency, dry run) | `internal/memosimport` |

## Project layout

```
cmd/app/                 entry point and subcommands
internal/config/         environment configuration
internal/model/          domain types
internal/store/          persistence interfaces; sqlite/ implementation
migrations/              embedded SQL migrations (0001_…, 0002_…, …)
internal/schedule/       recurrence rules and quiet hours
internal/service/        business logic
internal/scheduler/      background loop
internal/telegram/       Telegram bot
internal/auth/           authentication and security middleware
internal/web/            HTML UI: handlers, templates/, static/
internal/api/            JSON API
internal/markdown/       Markdown rendering and tags
internal/memosimport/    Memos importer
internal/xlsx/           dependency-free Excel (.xlsx) writer for exports
docs/                    this documentation
openapi.yaml             API description (embedded, served at /openapi.yaml)
```

## Branching and releases

| Branch | Purpose |
|---|---|
| `dev` | Day-to-day work. Every push runs CI and the security checks. Dependabot targets this branch. |
| `main` | Stable. Only receives merges from `dev` after the checks are green. Servers deploy from `main`. |

```bash
git checkout dev
# … change, test …
git add -A && git commit -m "Describe the change"
git push                                   # CI + Security run on dev

# when dev is green:
git checkout main
git merge --no-ff dev -m "Merge dev into main"
git push
git checkout dev
```

Never force-push `main`.

## Continuous integration

| Workflow | Jobs |
|---|---|
| `.github/workflows/ci.yml` | gofmt, go vet, `go test -race`, binary build, Docker image build |
| `.github/workflows/security.yml` | govulncheck, gosec, gitleaks (full history), trivy image scan; also weekly |
| `.github/dependabot.yml` | Weekly updates for Go modules, GitHub Actions, Docker base image (into `dev`) |

## Conventions

- **Layers**: handlers call the service; the service talks to `store.Store` only. Do not import
  `store/sqlite` outside `cmd/` and tests.
- **Transactions**: use `store.InTx`, and only the `tx` store inside it. The SQLite store uses a
  single connection, so calling the outer store inside a transaction deadlocks.
- **Validation** returns `model.Invalid("user-readable message")`; handlers turn it into a
  400 (API) or an on-page error (web).
- **Time**: store UTC, compute schedules in the configured location, inject `Now` for tests.
- **Migrations**: add a new numbered file to `migrations/`; never edit an applied one.
- **UI**: no inline scripts (CSP). Behaviour lives in `static/app.js`; asset URLs are versioned automatically.
- **Security exceptions**: mark reviewed gosec findings with `#nosec <rule> -- <reason>` and list
  them in [security.md](security.md).
- **Formatting**: `gofmt`; files are LF (`.gitattributes`).
