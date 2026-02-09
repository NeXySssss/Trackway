# Trackway Bot (Go)

TCP port tracker with Telegram alerts, SQLite history, and a built-in Astro dashboard.

## Features
- Monitor `address:port` targets on interval.
- Manage targets from dashboard (`add/update/delete`) with DB persistence.
- Telegram alerts on `DOWN` and `RECOVERED` (batched per cycle).
- Commands: `/start`, `/list`, `/status`, `/logs <track>`, `/authme`.
- SQLite-backed logs (`INIT`, `CHANGE`, `POLL`) with 5-day retention by default.
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
- `internal/logstore` - log storage backends (memory + SQLite).
- `internal/tracker` - monitor engine, alerts, commands, service facade.
- `internal/telegram` - Telegram adapter.
- `internal/dashboard` - auth flow, API, and embedded Astro dist.
- `docs/ARCHITECTURE.md` - dependency boundaries and extension rules.

## Architecture overview
- `cmd/trackway/main.go` is the single composition root.
- `internal/config` loads and validates JSON/env configuration.
- `internal/logstore` is the storage boundary (SQLite).
- `internal/tracker` owns monitoring loop, state transitions, and alert logic.
- `internal/telegram` is a thin adapter over Telegram Bot API.
- `internal/dashboard` exposes HTTP API + embedded frontend and auth/session handling.
- Dashboard never talks to storage directly; it goes through `tracker.Service`.
- All network checks run with explicit dial timeout and bounded worker concurrency.
- Target definitions are persisted and reloaded on every monitoring cycle.
- Session auth is short-lived one-time token -> browser session cookie.

## Config
Use `config.example.json` as the base. Minimal shape:

```json
{
  "bot": {
    "token": "PUT_BOT_TOKEN_HERE",
    "chat_id": 123456789
  },
  "monitoring": {
    "interval_seconds": 5,
    "connect_timeout_seconds": 2,
    "max_parallel_checks": 16
  },
  "storage": {
    "driver": "sqlite",
    "sqlite": {
      "path": "/data/trackway.db",
      "retention_days": 5,
      "busy_timeout_ms": 5000,
      "max_open_conns": 1,
      "max_idle_conns": 1
    }
  },
  "dashboard": {
    "enabled": true,
    "listen_address": ":8080",
    "public_url": "https://s2-lt.nexy.one",
    "auth_token_ttl_seconds": 300,
    "secure_cookie": true,
    "mini_app_enabled": true,
    "mini_app_max_age_seconds": 86400
  }
}
```

Notes:
- `dashboard.public_url` is used in `/authme` links.
- In production use HTTPS and keep `secure_cookie: true`.
- Session ends on browser restart or 24h server TTL.
- `targets` are optional in config and are inserted only once when DB target storage is empty.
- Runtime config can be passed in one line:
  - `TRACKWAY_CONFIG_JSON='{"bot":...}'`
  - or `TRACKWAY_CONFIG_JSON_B64='<base64-json>'`
- Storage env overrides:
  - `STORAGE_DRIVER=sqlite`
  - `SQLITE_PATH`, `SQLITE_RETENTION_DAYS`, `SQLITE_BUSY_TIMEOUT_MS`, `SQLITE_MAX_OPEN_CONNS`, `SQLITE_MAX_IDLE_CONNS`

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
make format
make lint
make test
make typecheck
make security
go build -o trackway.exe ./cmd/trackway
.\trackway.exe
```

If `make` is unavailable, run direct Go commands:
- `go fmt ./...`
- `go vet ./...`
- `go test ./...`
- `go test -run '^$' ./...`
- `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`

## Run with Docker Compose
```powershell
docker compose up -d --build
```

Current compose:
- starts `trackway` only (SQLite in local Docker volume)
- mounts `./config.json` read-only
- persists DB in named volume `trackway-data` mounted to `/data`
- exposes dashboard on `${TRACKWAY_BIND_IP:-127.0.0.1}:${TRACKWAY_BIND_PORT:-8083}` (container listens on `:8080`) for reverse proxy (Caddy/Nginx)

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
- Workflow: `.github/workflows/ci-cd.yml`
- Full setup guide: `docs/DEPLOYMENT.md`
- SSH secrets for deploy: `DEPLOY_SSH_HOST`, `DEPLOY_SSH_USER`, `DEPLOY_SSH_PRIVATE_KEY` (optional `DEPLOY_SSH_PORT`, `DEPLOY_SSH_KNOWN_HOSTS`)
- Optional runtime config secrets: `TRACKWAY_CONFIG_JSON` or `TRACKWAY_CONFIG_JSON_B64`
- Optional bind secrets: `TRACKWAY_BIND_IP`, `TRACKWAY_BIND_PORT`
- Optional SQLite secrets: `STORAGE_DRIVER`, `SQLITE_PATH`, `SQLITE_RETENTION_DAYS`, `SQLITE_BUSY_TIMEOUT_MS`, `SQLITE_MAX_OPEN_CONNS`, `SQLITE_MAX_IDLE_CONNS`

## Security
- See `SECURITY.md` for policy, threat model, and secure development checklist.
- Use `.env.example` as the non-secret environment template.

## Frontend build
Astro assets are built once and embedded into the Go binary.

```powershell
cd internal/dashboard/frontend
npm install
npm run build
```
