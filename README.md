# Trackway Bot (Go)

TCP port tracker with Telegram alerts, ClickHouse history, and a built-in Astro dashboard.

## Features
- Monitor `address:port` targets on interval.
- Telegram alerts on `DOWN` and `RECOVERED` (batched per cycle).
- Commands: `/start`, `/list`, `/status`, `/logs <track>`, `/authme`.
- ClickHouse-backed logs (`INIT`, `CHANGE`, `POLL`) with long retention.
- Dashboard with:
  - responsive table for all targets
  - availability timeline with hover timestamp
  - current-state pie chart
  - full raw logs viewer
  - dark/light theme
  - browser auth via `/authme` link
  - Telegram Mini App auto-auth (`initData` verification)

## Project layout
- `cmd/trackway/main.go` - runtime wiring.
- `internal/config` - config parsing and validation.
- `internal/logstore` - log storage backends (memory + ClickHouse).
- `internal/tracker` - monitor engine, alerts, commands, service facade.
- `internal/telegram` - Telegram adapter.
- `internal/dashboard` - auth flow, API, and embedded Astro dist.
- `docs/ARCHITECTURE.md` - dependency boundaries and extension rules.

## Config
Use `config.example.yaml` as the base. Minimal shape:

```yaml
bot:
  token: "PUT_BOT_TOKEN_HERE"
  chat_id: 123456789

monitoring:
  interval_seconds: 5
  connect_timeout_seconds: 2
  max_parallel_checks: 16

storage:
  clickhouse:
    addr: "clickhouse:9000"
    database: "trackway"
    username: "default"
    password: ""
    table: "track_logs"
    secure: false
    dial_timeout_seconds: 5
    max_open_conns: 10
    max_idle_conns: 5

dashboard:
  enabled: true
  listen_address: ":8080"
  public_url: "https://s2-lt.nexy.one"
  auth_token_ttl_seconds: 300
  secure_cookie: true
  mini_app_enabled: true
  mini_app_max_age_seconds: 86400
```

Notes:
- `dashboard.public_url` is used in `/authme` links.
- In production use HTTPS and keep `secure_cookie: true`.
- Session ends on browser restart or 24h server TTL.

## Dashboard auth flow
1. Send `/authme` to the bot.
2. Open the bot link.
3. On `/auth/verify` click `Authorize this browser`.
4. Browser gets a session cookie and is redirected to `/`.

Why two steps:
- Telegram/link previews can pre-open URLs.
- GET only renders confirmation.
- Token is consumed only on POST.

## Telegram Mini App auth
- Frontend tries auto-auth via `POST /api/auth/telegram-miniapp` if opened inside Telegram WebApp.
- Backend verifies Telegram `initData` signature with bot token and checks `auth_date`.
- To use Mini App in production, set bot domain in BotFather so WebApp can open your `dashboard.public_url`.

## Run locally
```powershell
go test ./...
go build -o trackway.exe ./cmd/trackway
.\trackway.exe
```

## Run with Docker Compose
```powershell
docker compose up -d --build
```

Current compose:
- starts `clickhouse` + `trackway`
- mounts `./config.yaml` read-only
- keeps `clickhouse` and `trackway` in the same Docker network
- exposes dashboard only on local host `http://127.0.0.1:8083` (container listens on `:8080`) for reverse proxy (Caddy/Nginx)

Stop:
```powershell
docker compose down
```

## Caddy reverse proxy
Reference config: `docs/Caddyfile.example`

Quick 502 checklist:
1. Backend health works on host:
   - `curl http://127.0.0.1:8083/healthz`
2. `dashboard.public_url` equals your external URL exactly.
3. Caddy upstream points to the same local port (`127.0.0.1:8083`).

## CI/CD and Auto Deploy
- CI/CD workflow: `.github/workflows/ci-cd.yml`
- Nightly ClickHouse backup: `.github/workflows/clickhouse-backup.yml`
- Full Debian 13 setup guide: `docs/DEPLOYMENT.md`
- SSH secrets for deploy: `DEPLOY_SSH_HOST`, `DEPLOY_SSH_USER`, `DEPLOY_SSH_PRIVATE_KEY` (and optional `DEPLOY_SSH_PORT`, `DEPLOY_SSH_KNOWN_HOSTS`)
- Legacy SSH secret aliases also work: `SSH_HOST`, `SSH_USER`, `SSH_PRIVATE_KEY`, `SSH_PORT`, `SSH_KNOWN_HOSTS`
- GHCR auth uses built-in `GITHUB_TOKEN` from GitHub Actions

## Frontend build
Astro assets are built once and embedded into the Go binary.

```powershell
cd internal/dashboard/frontend
npm install
npm run build
```
