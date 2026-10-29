# Installation & Deployment

## Requirements

- A Linux server with Docker Engine and the Compose plugin.
- Outbound HTTPS to `api.telegram.org` (for reminders) and, at build time, to
  Docker Hub and the Go module proxy.
- About 50 MB of disk for the image, plus your data.

## Install with Docker Compose

```bash
git clone -b main https://github.com/Rothsource/Spider-Notes.git spider-notes
cd spider-notes
cp .env.example .env
nano .env
```

Set at least the following (see [Configuration](configuration.md)):

```dotenv
APP_ADMIN_USERNAME=yourname
APP_ADMIN_PASSWORD=a-long-password          # min. 10 characters, used on first start only
APP_SESSION_SECRET=<openssl rand -base64 48>
TELEGRAM_BOT_TOKEN=123456789:AA...          # optional
TELEGRAM_CHAT_ID=123456789                  # optional
```

Start it:

```bash
docker compose up -d --build
docker compose ps                 # wait for "healthy"
docker compose logs --tail=30     # "http server listening", "telegram connected"
```

The container:

- runs as a non-root user on a distroless image,
- stores everything in the named volume `spider-notes-data` mounted at `/data`,
- restarts automatically (`restart: unless-stopped`),
- has a built-in health check (`/app healthcheck` → `GET /healthz`).

## Servers behind a firewall or jump host

When the server is only reachable through an SSH jump host:

**Copy or update the code** with git on the server, or copy a tarball through the jump host:

```powershell
# Windows PowerShell, from the project folder's parent
tar -czf spider-notes.tgz --exclude=.git Mynotes
scp -J user@JUMP spider-notes.tgz user@SERVER:/home/user/docker/
```

**Open the web app** through an SSH tunnel and browse to <http://localhost:8080>:

```powershell
ssh -J user@JUMP -L 8080:localhost:8080 user@SERVER
```

With a host alias in `~/.ssh/config` (for example `myserver` as the jump host):

```powershell
ssh -J myserver -L 8080:localhost:8080 vector@10.10.40.100
```

Telegram works without the tunnel, because the server connects out to Telegram.

**If the server cannot download during the build**, build on another machine and transfer the image:

```bash
docker compose build
docker save spider-notes:latest -o spider-notes-image.tar
# copy the file to the server, then on the server:
docker load -i spider-notes-image.tar && docker compose up -d
```

### Restricting network exposure

If you only use the tunnel, bind the port to localhost in `docker-compose.yml`:

```yaml
ports:
  - "127.0.0.1:8080:8080"
```

## Behind a reverse proxy (HTTPS)

To serve the app on a domain, put a reverse proxy in front and set
`APP_TRUST_PROXY=true`. Cookies are then marked `Secure` automatically when
`X-Forwarded-Proto: https` is present, and login rate limiting uses the real client IP.

Caddy example:

```caddyfile
notes.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

nginx example:

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_set_header X-Forwarded-Proto $scheme;
    client_max_body_size 60m;   # >= APP_MAX_UPLOAD_MB
}
```

Only set `APP_TRUST_PROXY=true` when the app is reachable **only** through the proxy.

## Running without Docker

```bash
go build -o app ./cmd/app
APP_DB_PATH=./data/app.db APP_EXPORT_DIR=./data/export APP_ATTACHMENTS_DIR=./data/attachments \
APP_ADMIN_USERNAME=me APP_ADMIN_PASSWORD=change-me-please \
APP_SESSION_SECRET="$(openssl rand -base64 48)" ./app serve
```

Requires Go 1.27.3+. No C compiler is needed.

## Command-line reference

| Command | Purpose |
|---|---|
| `app serve` | Run the web server, scheduler and Telegram bot (default) |
| `app backup [--out PATH]` | Consistent online copy of the database (`VACUUM INTO`) |
| `app import-memos --from DIR [--dry-run] [--archived] [-v]` | Import from a Memos data folder |
| `app healthcheck` | Exit 0 if `/healthz` responds (used by Docker) |
| `app version` | Print the version |

In Docker, run them with `docker compose exec app /app <command>`.
