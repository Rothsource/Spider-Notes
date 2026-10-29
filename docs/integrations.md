# Integrations and Tokens

This guide explains every credential Spider Notes uses and how to connect it to
other tools: Telegram, scripts, automation platforms, phone shortcuts and AI agents.

## What exists

| Credential | Direction | Where it comes from | Used for |
|---|---|---|---|
| **Telegram bot token** | Spider Notes → Telegram | @BotFather | Sending reminders and receiving button presses |
| **Telegram chat ID** | — | Your Telegram account | The only chat allowed to receive and control reminders |
| **Personal API token** (`mnt_…`) | Other tools → Spider Notes | **Settings → API tokens** | Full JSON API access (`/api/v1`) |
| **Session secret** (`APP_SESSION_SECRET`) | Internal | You generate it | Signing login cookies. Never shared with other tools |

What is **not** available (by design, for a private single-user server):

- No outgoing webhooks. Other tools *pull* from the API; Spider Notes only *pushes* to Telegram.
- No OAuth, no per-token scopes and no token expiry. Every API token has full access.
- No iCal/CalDAV feed. Use `GET /api/v1/calendar` instead.
- The Telegram bot understands expense messages and `/today` `/week` `/month` `/help`;
  tasks and reminders are created in the web app or through the API.

---

## 1. Telegram

Full details: [telegram.md](telegram.md). Short version:

### 1.1 Create the bot

1. In Telegram, open **@BotFather** → `/newbot` → choose a name and a username ending in `bot`.
2. Copy the token (`123456789:AA…`). Treat it like a password.
3. Optional: `/setuserpic` → send `branding/spider-notes-logo.png`.
4. Open your new bot and press **Start**. A bot cannot message you before you do this.

### 1.2 Find your chat ID

Either ask **@userinfobot**, or after pressing Start open:

```
https://api.telegram.org/bot<TOKEN>/getUpdates
```

and read `"chat":{"id":123456789}`. Personal chats are positive numbers; groups are negative.

### 1.3 Configure Spider Notes

Choose **one** of these:

| Method | How | When to use |
|---|---|---|
| In the app (recommended) | **Settings → Telegram** → paste token and chat ID → **Save** → **Send test message** | No restart; reconnects immediately |
| Environment | In `.env`: `TELEGRAM_BOT_TOKEN=…` and `TELEGRAM_CHAT_ID=…`, then `docker compose up -d` | Configuration as code |

Values saved in the app override `.env`. `TELEGRAM_CHAT_ID` must be an integer or the app refuses to start.

### 1.4 Verify

```bash
docker compose logs -f app | grep -i telegram
# expect: telegram connected
```

The server only needs **outbound** HTTPS to `api.telegram.org`. No port, domain or webhook is required.

### 1.5 Rotate or disable

- **Token leaked?** In @BotFather: `/mybots` → your bot → **API Token** → **Revoke current token**.
  Paste the new token in **Settings → Telegram**.
- **Disable:** tick **Remove token** and save. Reminders still show on the Today page.

---

## 2. Personal API tokens

### 2.1 Create a token

1. Sign in → **Settings → API tokens**.
2. Enter a name that says where it will be used, e.g. `n8n`, `iphone`, `backup-script`.
3. Press **Create**, then **Copy**. The token is shown **once**; only its SHA-256 hash is stored.

Use one token per tool. The list shows when each token was last used, and you can
revoke one tool without breaking the others.

### 2.2 Use it

Send it in the `Authorization` header on every request:

```
Authorization: Bearer mnt_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

Test it:

```bash
curl -s -H "Authorization: Bearer $SPIDER_TOKEN" http://localhost:8080/api/v1/tasks?range=today
```

| Response | Meaning |
|---|---|
| `200` with JSON | Working |
| `401 unauthorized` "invalid authorization header" | Missing `Bearer ` prefix, or the token does not start with `mnt_` |
| `401 unauthorized` "invalid or revoked token" | Wrong, mistyped or revoked token |

Requests with a token need **no CSRF header**; that is only for the browser session.

### 2.3 Store it safely

- Put it in the tool's secret store (n8n credentials, Home Assistant `secrets.yaml`,
  an environment variable), never directly in a script that is committed to git.
- Never put a token in a URL query string; URLs end up in logs and browser history.

### 2.4 Rotate or revoke

Create a new token → update the tool → **Revoke** the old one in **Settings → API tokens**.
Revocation takes effect on the next request.

### 2.5 API description

The full machine-readable API is served at **`/openapi.yaml`** (no login needed; it
contains no data). Import it into Postman, Insomnia, Bruno, n8n or an AI agent framework
to get every endpoint ready-made. Human-readable reference: [api.md](api.md).

---

## 3. Making the API reachable

Your server is on a private network behind a jump host, so first decide how a tool reaches it.

| Tool runs on | Option |
|---|---|
| The server itself (cron, scripts) | `http://localhost:8080` |
| Your PC, occasionally | SSH tunnel: `ssh -J myserver -L 8080:localhost:8080 vector@10.10.40.100`, then `http://localhost:8080` |
| Your phone or a cloud service, always | A VPN (Tailscale/WireGuard) into the network, **or** an HTTPS reverse proxy (see [deployment.md](deployment.md)) |

Do not expose plain HTTP port 8080 to the internet: the token would travel unencrypted.
Behind a reverse proxy, set `APP_TRUST_PROXY=true` so rate limiting sees real client IPs.

---

## 4. Recipes

All examples use `BASE` = your server URL and `SPIDER_TOKEN` = an API token.

### 4.1 Shell (curl)

```bash
export BASE=http://localhost:8080/api/v1
export SPIDER_TOKEN=mnt_...
H="Authorization: Bearer $SPIDER_TOKEN"

# Quick memo
curl -s -H "$H" -X POST $BASE/memos -d '{"content_md":"Idea: try the new route #ideas"}'

# Task due tomorrow 09:00
curl -s -H "$H" -X POST $BASE/tasks -d '{
  "title":"Call the bank", "due_at":"2026-10-30T09:00:00+07:00", "priority":"high", "tags":["finance"]}'

# One-time reminder that ignores quiet hours
curl -s -H "$H" -X POST $BASE/reminders -d '{
  "message_md":"Take the medicine", "fire_at":"2026-10-29T21:00:00+07:00", "ignore_quiet_hours":true}'

# Upload a file into folder 3
curl -s -H "$H" -F folder_id=3 -F file=@scan.pdf $BASE/attachments
```

### 4.2 PowerShell (Windows)

```powershell
$base = "http://localhost:8080/api/v1"
$h = @{ Authorization = "Bearer $env:SPIDER_TOKEN" }

Invoke-RestMethod "$base/tasks?range=today" -Headers $h

$body = @{ content_md = "Written from PowerShell #pc" } | ConvertTo-Json
Invoke-RestMethod "$base/memos" -Method Post -Headers $h -ContentType "application/json" -Body $body
```

### 4.3 Python

```python
import os, requests

BASE = os.environ["SPIDER_BASE"]          # e.g. http://localhost:8080/api/v1
S = requests.Session()
S.headers["Authorization"] = f"Bearer {os.environ['SPIDER_TOKEN']}"

def check(r):
    if r.status_code >= 400:
        raise RuntimeError(r.json()["error"]["message"])
    return r.json() if r.content else None

pending = check(S.get(f"{BASE}/occurrences", params={"status": "sent,snoozed"}))
for occ in pending:
    print(occ["id"], occ["scheduled_for"])

check(S.post(f"{BASE}/memos", json={"content_md": "Logged from Python #auto"}))
```

### 4.4 n8n / Make / Node-RED

Generic setup for any automation tool with an HTTP node:

| Field | Value |
|---|---|
| Method | `GET` / `POST` / `PUT` / `DELETE` |
| URL | `https://notes.example.com/api/v1/<endpoint>` |
| Authentication | **Header auth**: name `Authorization`, value `Bearer mnt_…` (store as a credential) |
| Body | JSON, `Content-Type: application/json` |

Ideas:
- **RSS / email → memo:** on a new item, `POST /memos` with `{"content_md": "…"}`.
- **Daily digest:** every morning, `GET /tasks?range=today` and send the list somewhere else.
- **Form → task:** a form submission becomes `POST /tasks`.

Only send data to services you trust; whatever you forward leaves your server.

### 4.5 Home Assistant

`configuration.yaml`:

```yaml
rest_command:
  spider_memo:
    url: "http://10.10.40.100:8080/api/v1/memos"
    method: post
    headers:
      Authorization: !secret spider_notes_auth     # "Bearer mnt_..."
      Content-Type: application/json
    payload: '{"content_md": "{{ text }}"}'
```

Call it from an automation, e.g. *washing machine finished*:
`service: rest_command.spider_memo` with `data: {text: "Laundry done #home"}`.

### 4.6 iPhone Shortcuts / Android HTTP Shortcuts

Create a shortcut with **Get contents of URL**:

- URL: `https://notes.example.com/api/v1/memos`
- Method: `POST`
- Headers: `Authorization` = `Bearer mnt_…`
- Request body: JSON, key `content_md` = *Ask for input* (or dictated text)

Add it to the home screen or trigger it from Siri for one-tap capture.
The phone must reach the server (VPN or HTTPS proxy, see section 3).

### 4.7 Backup or sync script (cron on the server)

```bash
#!/bin/sh
# /home/vector/bin/spider-tasks.sh: save today's open tasks as JSON every hour
curl -sf -H "Authorization: Bearer $SPIDER_TOKEN" \
  "http://localhost:8080/api/v1/tasks?status=pending" \
  > /home/vector/spider-tasks.json
```

For full backups use the built-in command instead (see [operations.md](operations.md)).

### 4.8 AI assistants and agents

`openapi.yaml` is written so it can be turned into tool definitions for an LLM agent
(Claude tool use, an MCP server, LangChain, n8n AI agent, …):

1. Create a dedicated token, e.g. `assistant`.
2. Give the agent the OpenAPI file (or a few selected endpoints: `POST /memos`,
   `GET /search`, `POST /reminders`, `GET /occurrences`).
3. Keep the token in the agent's secret configuration, not in the prompt.

Remember that the token has **full access**, including delete. Revoke it when you stop using the agent.

---

## 5. Security checklist

- [ ] One token per tool, named after the tool.
- [ ] Tokens stored as secrets, never in git, URLs or chat messages.
- [ ] HTTPS or VPN for anything outside the server.
- [ ] Old or unused tokens revoked (check **last used** in Settings).
- [ ] Telegram chat ID set to your own chat only.
- [ ] Telegram bot token revoked in @BotFather if it was ever exposed.

## 6. Troubleshooting

| Symptom | Fix |
|---|---|
| `401 invalid authorization header` | Header must be exactly `Authorization: Bearer mnt_…` |
| `401 invalid or revoked token` | Create a new token; check for spaces or line breaks when pasting |
| `400 validation` "invalid JSON body: unknown field …" | Field name typo; check [api.md](api.md) or `/openapi.yaml` |
| `403 csrf` | You sent a session cookie without a token. Add the `Authorization` header |
| Times are off by hours | Send RFC 3339 with an offset (`+07:00`), not a bare local time |
| Connection refused / timeout | The tool cannot reach the server; see section 3 |
| Telegram test fails | Press **Start** in the bot; check the chat ID; check outbound HTTPS |
