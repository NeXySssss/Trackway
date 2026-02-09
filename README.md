# Trackway Bot (Go)

TCP port tracker for Telegram with low memory usage.

Uses latest deps in this repo:
- `github.com/go-telegram/bot v1.18.0`
- `gopkg.in/yaml.v3 v3.0.1`

## Features
- Monitor `address:port` targets on interval.
- Telegram alerts on `DOWN` and `RECOVERED` (batched per check cycle).
- If a `state-change` DOWN recovers within 30s, the original DOWN message is edited to `DOWN -> RECOVERED`.
- Commands: `/start`, `/list`, `/status`, `/logs <track>`, `/authme`.
- Per-track logs, filtered by last 7 days in `/logs`.
- Built-in web dashboard with:
  - live status table for all tracks
  - charts (snapshot distribution + per-track timeline from logs)
  - light/dark theme toggle
  - full logs view per track
  - Telegram-issued browser auth links (`/authme`)
  - session cookie that expires on browser restart or after 24h

## Project layout
- `cmd/trackway/main.go` - entrypoint and wiring.
- `internal/config` - config parsing and validation.
- `internal/telegram` - Telegram client adapter.
- `internal/tracker` - monitor engine + alert orchestration + bot commands.
- `internal/dashboard` - HTTP server, auth flow, dashboard API and Astro UI assets.
- `internal/logstore` - append/read logs by track.
- `internal/util` - shared text/time helpers.
- `docs/ARCHITECTURE.md` - module map, dependency direction and extension rules.

## Config
`config.yaml` format:

```yaml
bot:
  token: "PUT_BOT_TOKEN_HERE"
  chat_id: 123456789
monitoring:
  interval_seconds: 5
  connect_timeout_seconds: 2
  max_parallel_checks: 16
storage:
  log_dir: "logs"
dashboard:
  enabled: true
  listen_address: ":8080"
  public_url: "https://s2-lt.nexy.one"
  auth_token_ttl_seconds: 300
  secure_cookie: true
targets:
  - name: "track-ssh"
    address: "100.64.0.10"
    port: 22
```

`dashboard.public_url` is used in `/authme` links.
Use a public HTTPS URL in production and set `secure_cookie: true`.

Log reasons:
- `INIT` - first check after service start
- `CHANGE` - state changed (`UP <-> DOWN`)
- `POLL` - regular check without state change

## Dashboard auth flow
1. Send `/authme` to the bot from `bot.chat_id`.
2. Open the link from bot message.
3. On `/auth/verify` click **Authorize this browser** (token is consumed on POST only).
4. Browser receives a session cookie and is redirected to dashboard `/`.
5. Session ends on browser restart (session cookie) or after 24h (server-side TTL), whichever comes first.

Note:
- Open `/authme` link in the same browser where you use dashboard.
- Telegram/link-preview crawlers may open the URL; two-step verify prevents token loss before your click.

## Local run
```powershell
go build -o port-tracker.exe ./cmd/trackway
.\port-tracker.exe
```

## Docker Compose run
```powershell
docker compose up -d --build
```

Compose config in `docker-compose.yml`:
- read-only root filesystem (`read_only: true`)
- writable logs only via named volume `trackway-data:/data/logs`
- config mounted read-only `./config.yaml:/app/config.yaml:ro`
- runtime user is `root` to avoid host/volume UID permission issues
- dashboard published on `http://localhost:8080`

## Caddy reverse proxy
Example Caddy config: `docs/Caddyfile.example`

Checklist for `502 Bad Gateway`:
1. Trackway process is running and listening on `:8080`.
2. `dashboard.public_url` matches your public domain exactly (`https://s2-lt.nexy.one`).
3. Caddy upstream points to the real backend (`127.0.0.1:8080` on same host, or service name in Docker network).
4. Health endpoint works from proxy host:
   - `curl http://127.0.0.1:8080/healthz`
5. If using HTTPS domain, `secure_cookie: true`.

## Stop
```powershell
docker compose down
```

## Quality checks
```powershell
go test ./...
go vet ./...
go build ./...
```

`max_parallel_checks` controls how many targets are probed concurrently in each cycle.

To rebuild Astro assets after UI edits:

```powershell
cd internal/dashboard/frontend
npm install
npm run build
```
