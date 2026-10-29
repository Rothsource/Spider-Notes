# Security

Spider Notes is designed for a single trusted user on a private server. This
document describes how it protects that user's data and how the code is checked.

## Authentication and sessions

| Control | Implementation |
|---|---|
| Password hashing | argon2id (64 MiB memory, 3 iterations, 2 threads, 16-byte salt), PHC string format, constant-time comparison |
| Password policy | 10–256 characters; changing it signs out all other sessions |
| Sessions | Server-side. The cookie holds a random 256-bit token plus an HMAC-SHA256 signature (`APP_SESSION_SECRET`); the database stores only the token's SHA-256 |
| Cookie flags | `HttpOnly`, `SameSite=Lax`, `Secure` when served over HTTPS |
| Idle timeout | `APP_SESSION_IDLE_TIMEOUT` (default 12 h), sliding |
| CSRF | Per-session token required on every state-changing request (form field or `X-CSRF-Token` header); a signed double-submit token protects the login form |
| Login rate limit | 5 attempts per minute per client IP; failed and unknown-user logins take the same time |
| API tokens | Random 256-bit, prefixed `mnt_`, stored as SHA-256, shown once, revocable |

## Web protections

- **Content-Security-Policy**: `default-src 'self'; script-src 'self'` with no inline
  scripts, plus `frame-ancestors 'none'`, `base-uri 'self'` and `form-action 'self'`.
- `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin`.
- **Markdown** is rendered with goldmark and sanitised with bluemonday's UGC policy
  (no scripts, event handlers or `javascript:` URLs).
- Templates use `html/template` contextual escaping; search snippets are escaped before highlighting.
- Redirect targets are restricted to local paths.
- Request bodies are capped globally and per upload handler.

## File uploads

- Streamed to disk with a size limit (`APP_MAX_UPLOAD_MB`) and a SHA-256 hash.
- Stored under random names (`YYYY/MM/<random>.<ext>`); user file names are never used as paths.
- The content type is sniffed from the bytes, not trusted from the client.
- Only images (not SVG), PDF, audio, video and plain text are displayed inline. Everything
  else, including HTML and SVG, is served as a download with `Content-Type: application/octet-stream`.
- Responses carry `Content-Security-Policy: sandbox` (except PDFs) and `nosniff`.
- Files require a signed-in session or an API token.

## Telegram

- Only button presses from `TELEGRAM_CHAT_ID` are processed; other chats are ignored silently.
- Callback data is validated against a strict pattern (`o:<id>:<action>`).
- The bot token is redacted from all logs, including the Telegram library's own output.
- Long polling means no inbound port or public webhook.

## Data protection

- Nothing is hard-deleted: soft deletes keep history and allow recovery from backups.
- No secrets in logs: passwords, tokens and query strings are never logged.
- `.env`, databases, uploads, exports and backups are excluded by `.gitignore` and `.dockerignore`.
- The Memos importer opens the source database strictly read-only (`immutable=1`).

## Container hardening

- Multi-stage build; the runtime image is `gcr.io/distroless/static-debian12:nonroot`,
  with no shell and no package manager.
- Runs as UID 65532 (non-root); only `/data` is writable.
- Static binary (`CGO_ENABLED=0`) with the time-zone database embedded.

## Automated security checks

Every push and pull request to `main` and `dev` runs, and the Security workflow
also runs every Monday for newly published vulnerabilities:

| Check | Tool | Scope |
|---|---|---|
| Known vulnerabilities | `govulncheck` | Go standard library and dependencies, reachable code only |
| Static analysis | `gosec` | Insecure patterns in the Go code |
| Secret scanning | `gitleaks` | The **full git history** (`.gitleaks.toml` allowlists dummy test credentials) |
| Image scanning | `trivy` | OS packages and the Go binary in the Docker image (HIGH/CRITICAL, fixed) |
| Dependency updates | Dependabot | Weekly PRs into `dev` for Go modules, GitHub Actions and the base image |
| Tests | `go test -race` | Including auth, CSRF, rate limiting, upload handling and sanitisation tests |

### Reviewed exceptions

Some gosec findings are false positives for this app. Each is marked in the
code with `#nosec <rule> -- <reason>`:

| Rule | Where | Why it is safe |
|---|---|---|
| G203 | Markdown rendering, icons, search snippets | Output is sanitised (bluemonday) or escaped first |
| G705 | Markdown preview endpoint | Same sanitised renderer |
| G201 / G202 | Memos importer | Column/table names come from a fixed allowlist checked against the schema |
| G304 | Attachment and export file access | Paths are built from server-generated keys and checked to stay inside the data folder |
| G710 | Redirect helper | Targets are internal paths or validated by `safeBack` |
| G124 | Cookies | `Secure` is set whenever the request is HTTPS; plain HTTP is supported on private networks |
| G704 | Health-check command | Fixed local address from `APP_LISTEN` |
| G101 | `token_hash` column list | Column names, not a credential |
| G115 | argon2 key length | Bounded by the stored hash length |
| G120 | Multipart upload parsing | Body already capped with `http.MaxBytesReader` |

## Deployment recommendations

- Keep the app on a private network or VPN, or behind an HTTPS reverse proxy.
- If you use only an SSH tunnel, bind the port to `127.0.0.1`.
- Use a long random `APP_SESSION_SECRET` and a strong password.
- Back up the database **and** the attachments folder; keep a copy off the server.
- Keep the image up to date: `git pull && docker compose up -d --build`.

## Reporting a problem

This is a personal project. Report security issues privately to the repository
owner instead of opening a public issue.
