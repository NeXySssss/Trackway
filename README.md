# Trackway Bot (Go)

TCP port tracker for Telegram with low memory usage.

Uses latest deps in this repo:
- `github.com/go-telegram/bot v1.18.0`
- `gopkg.in/yaml.v3 v3.0.1`

## Features
- Monitor `address:port` targets on interval.
- Telegram alerts on `DOWN` and `RECOVERED`.
- Commands: `/start`, `/list`, `/status`, `/logs <track>`.
- Per-track logs, filtered by last 7 days in `/logs`.

## Project layout
- `cmd/trackway/main.go` - entrypoint and wiring.
- `internal/config` - config parsing and validation.
- `internal/telegram` - Telegram client adapter.
- `internal/tracker` - monitoring + bot command handlers.
- `internal/logstore` - append/read logs by track.
- `internal/util` - shared text/time helpers.

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
targets:
  - name: "track-ssh"
    address: "100.64.0.10"
    port: 22
```

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
