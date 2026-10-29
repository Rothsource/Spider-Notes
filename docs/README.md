# Spider Notes Documentation

Spider Notes is a self-hosted, single-user productivity app: tasks, reminders
with Telegram delivery, a calendar, Memos-style quick notes, folders of
Markdown notes and file storage. It ships as one Go binary with an embedded
web UI and an SQLite database.

## Contents

| Document | What it covers |
|---|---|
| [Overview](overview.md) | What the app does, key concepts, feature list |
| [User Guide](user-guide.md) | Every page of the web app and how to use it |
| [Reminders & Scheduling](reminders-and-scheduling.md) | Repeat rules, nag mode, quiet hours, snooze, the occurrence lifecycle |
| [Telegram Bot](telegram.md) | Bot setup, message format, buttons, security rules |
| [Integrations and Tokens](integrations.md) | Telegram setup, API tokens, connecting scripts, n8n, Home Assistant, phone shortcuts, AI agents |
| [Installation & Deployment](deployment.md) | Docker, servers behind a jump host, SSH tunnels, reverse proxies |
| [Configuration](configuration.md) | Environment variables and in-app settings |
| [Operations](operations.md) | Backup, restore, upgrades, Memos import, logs, troubleshooting |
| [Architecture](architecture.md) | Components, request and reminder flows, design decisions |
| [Data Model](data-model.md) | Database tables, relationships, storage conventions |
| [API Reference](api.md) | JSON API, authentication, errors, endpoints, examples |
| [Security](security.md) | Security model, hardening, automated scanning |
| [Development](development.md) | Local setup, tests, project layout, branching and CI |

## Quick facts

| | |
|---|---|
| Language / runtime | Go 1.27.3, single static binary (no CGO) |
| Database | SQLite (WAL mode) via `modernc.org/sqlite` |
| UI | Server-rendered `html/template` + htmx, light and dark themes |
| Notifications | Telegram Bot API, long polling (no inbound webhook needed) |
| Deployment | Docker image (distroless, non-root), `docker compose` |
| Default port | `8080` |
| Default time zone | `Asia/Phnom_Penh` |
| Repository | <https://github.com/Rothsource/Spider-Notes> |
