# Trackway CI/CD and Auto-Deploy Guide (Debian 12/13)

This setup provides:
- GitHub Actions CI (`go test` + `go build`)
- build/push image to GHCR
- auto-deploy over SSH on `main`
- SQLite default storage (very low RAM footprint)

## 1. What is in repo

- Workflow: `.github/workflows/ci-cd.yml`
- Runtime compose: `docker-compose.yml`

## 2. Prepare target server

Run as root (or with `sudo`):

```bash
apt update
apt install -y ca-certificates curl git jq rsync acl
apt install -y docker.io docker-compose-plugin || apt install -y docker.io docker-compose-v2
systemctl enable --now docker
```

Create deploy directory:

```bash
mkdir -p /opt/trackway
```

## 3. SSH deploy user

```bash
useradd -m -s /bin/bash deploy || true
usermod -aG docker deploy
mkdir -p /home/deploy/.ssh
chmod 700 /home/deploy/.ssh
touch /home/deploy/.ssh/authorized_keys
chmod 600 /home/deploy/.ssh/authorized_keys
chown -R deploy:deploy /home/deploy/.ssh
```

## 4. Deploy keypair

Generate on your secure host:

```bash
ssh-keygen -t ed25519 -C "trackway-github-actions" -f ~/.ssh/trackway_deploy -N ""
cat ~/.ssh/trackway_deploy.pub
```

Add pubkey on server:

```bash
echo "PASTE_PUBLIC_KEY_HERE" >> /home/deploy/.ssh/authorized_keys
chown deploy:deploy /home/deploy/.ssh/authorized_keys
chmod 600 /home/deploy/.ssh/authorized_keys
```

## 5. GitHub repository secrets

Required:
- `DEPLOY_SSH_HOST`
- `DEPLOY_SSH_USER` (usually `deploy`)
- `DEPLOY_SSH_PRIVATE_KEY` (contents of `~/.ssh/trackway_deploy`)

Optional:
- `DEPLOY_SSH_PORT` (default `22`)
- `DEPLOY_SSH_KNOWN_HOSTS` (recommended)
- `TRACKWAY_CONFIG_JSON` or `TRACKWAY_CONFIG_JSON_B64`
- `TRACKWAY_BIND_IP` (default `127.0.0.1`)
- `TRACKWAY_BIND_PORT` (default `8083`)
- `STORAGE_DRIVER` (default `sqlite`)
- `SQLITE_PATH` (default `/data/trackway.db`)
- `SQLITE_RETENTION_DAYS` (default `5`)
- `SQLITE_BUSY_TIMEOUT_MS` (default `5000`)
- `SQLITE_MAX_OPEN_CONNS` (default `1`)
- `SQLITE_MAX_IDLE_CONNS` (default `1`)

Legacy alias secrets also work:
- `SSH_HOST`, `SSH_USER`, `SSH_PRIVATE_KEY`, `SSH_PORT`, `SSH_KNOWN_HOSTS`

Get `known_hosts`:

```bash
ssh-keyscan -H -p 22 <YOUR_HOST>
```

Paste into `DEPLOY_SSH_KNOWN_HOSTS`.

## 6. Runtime config choice

Use one of:

### Option A (recommended): config file on server

```bash
nano /opt/trackway/config.json
chmod 644 /opt/trackway/config.json
```

### Option B: GitHub secret

Set one of:
- `TRACKWAY_CONFIG_JSON` (one-line JSON)
- `TRACKWAY_CONFIG_JSON_B64` (base64 JSON)

If secret config is provided, workflow writes/uses it automatically.

## 7. First deploy

Push to `main` or run workflow manually.

Workflow does:
1. tests + build
2. image push to GHCR
3. rsync repo to `/opt/trackway`
4. hard-cleanup of legacy ClickHouse containers/volumes from old deployments
5. `docker compose pull` + `docker compose up -d`
6. checks container state is `running`

## 8. Verify

On server:

```bash
docker compose --project-name trackway -f /opt/trackway/docker-compose.yml ps
docker compose --project-name trackway -f /opt/trackway/docker-compose.yml logs --tail=80 trackway
curl -fsS http://127.0.0.1:8083/healthz
```

Expected:
- `trackway` status `Up`
- `/healthz` returns `ok`

## 9. Caddy bind model

- Keep `TRACKWAY_BIND_IP=127.0.0.1`
- Keep `TRACKWAY_BIND_PORT=8083`
- In Caddy use local upstream:
  - `reverse_proxy 127.0.0.1:8083`

If you want Tailscale-only bind, set `TRACKWAY_BIND_IP` to your tailscale IP.

## 10. Move deploy to new server

1. Prepare new server (sections 2-4).
2. Update secrets:
   - `DEPLOY_SSH_HOST`
   - optionally `DEPLOY_SSH_PORT`
   - refresh `DEPLOY_SSH_KNOWN_HOSTS`
3. Keep same config secret/file format.
4. Push to `main`.

No DB backup migration is needed if you accept a fresh SQLite DB on new host.
