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

## Config
`config.yaml` format:

```yaml
bot:
  token: "PUT_BOT_TOKEN_HERE"
  chat_id: 123456789
monitoring:
  interval_seconds: 5
  connect_timeout_seconds: 2
storage:
  log_dir: "logs"
targets:
  - name: "track-ssh"
    address: "100.64.0.10"
    port: 22
```

## Local run
```powershell
go build -o port-tracker.exe
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
