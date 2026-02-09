# Trackway CI/CD and Auto-Deploy Guide (Debian 13)

This guide sets up:
- GitHub Actions CI (`go test` + `go build`)
- Build and push Trackway image to GHCR
- Auto-deploy over SSH to your server on `main`
- ClickHouse backups before each deploy and nightly backup

## 1. What is added in repo

- CI/CD workflow: `.github/workflows/ci-cd.yml`
- Nightly backup workflow: `.github/workflows/clickhouse-backup.yml`
- Backup script: `scripts/backup-clickhouse.sh`
- Restore script: `scripts/restore-clickhouse.sh`

## 2. Debian 13 server prep

Run as root (or with `sudo`):

```bash
apt update
apt install -y ca-certificates curl git jq rsync acl
apt install -y docker.io docker-compose-plugin || apt install -y docker.io docker-compose-v2
systemctl enable --now docker
```

Create deployment directories:

```bash
mkdir -p /opt/trackway/backups
```

## 3. Install GitHub self-hosted runner (if not already)

Create dedicated runner user:

```bash
useradd -m -s /bin/bash actions || true
usermod -aG docker actions
chown -R actions:actions /opt/trackway
```

Download runner (replace version if newer):

```bash
su - actions -c '
set -e
mkdir -p ~/actions-runner
cd ~/actions-runner
curl -L -o actions-runner.tar.gz https://github.com/actions/runner/releases/download/v2.323.0/actions-runner-linux-x64-2.323.0.tar.gz
tar xzf actions-runner.tar.gz
'
```

Generate a runner token in GitHub:
- Repo -> `Settings` -> `Actions` -> `Runners` -> `New self-hosted runner`.

Configure runner:

```bash
su - actions -c '
cd ~/actions-runner
./config.sh \
  --url https://github.com/<OWNER>/<REPO> \
  --token <RUNNER_TOKEN> \
  --labels self-hosted,linux,x64 \
  --unattended
'
```

Install and start service:

```bash
/home/actions/actions-runner/svc.sh install actions
/home/actions/actions-runner/svc.sh start
```

Verify in GitHub UI that runner is online.

## 4. Prepare SSH deploy access

Create deploy user on target server:

```bash
useradd -m -s /bin/bash deploy || true
usermod -aG docker deploy
mkdir -p /home/deploy/.ssh
chmod 700 /home/deploy/.ssh
touch /home/deploy/.ssh/authorized_keys
chmod 600 /home/deploy/.ssh/authorized_keys
chown -R deploy:deploy /home/deploy/.ssh
```

Generate key pair on runner host (or secure workstation):

```bash
ssh-keygen -t ed25519 -C "trackway-github-actions" -f ~/.ssh/trackway_deploy -N ""
cat ~/.ssh/trackway_deploy.pub
```

Add the printed public key to server:

```bash
echo "PASTE_PUBLIC_KEY_HERE" >> /home/deploy/.ssh/authorized_keys
chown deploy:deploy /home/deploy/.ssh/authorized_keys
chmod 600 /home/deploy/.ssh/authorized_keys
```

## 5. Configure GitHub Secrets

Set these repository secrets:
- `DEPLOY_SSH_HOST` - target host/IP (example: `185.185.68.147`)
- `DEPLOY_SSH_USER` - SSH user (example: `deploy`)
- `DEPLOY_SSH_PRIVATE_KEY` - full private key (`~/.ssh/trackway_deploy`)
- `DEPLOY_SSH_PORT` - optional, default `22`
- `DEPLOY_SSH_KNOWN_HOSTS` - optional but recommended (`ssh-keyscan -H <host>`)
- `TRACKWAY_CONFIG_YAML` - optional full runtime config YAML

GHCR login for build/push and deploy uses built-in `${{ secrets.GITHUB_TOKEN }}`.

Legacy secret names are also accepted:
- `SSH_HOST`, `SSH_USER`, `SSH_PRIVATE_KEY`, `SSH_PORT`, `SSH_KNOWN_HOSTS`

Create `known_hosts` value:

```bash
ssh-keyscan -H -p 22 <YOUR_HOST>
```

Paste output into `DEPLOY_SSH_KNOWN_HOSTS`.

## 6. Configure runtime config

Choose one way:

### Option A (recommended): file on server
Create `/opt/trackway/config.yaml` manually:

```bash
cp /opt/trackway/config.yaml /opt/trackway/config.yaml.bak 2>/dev/null || true
nano /opt/trackway/config.yaml
chmod 644 /opt/trackway/config.yaml
```

### Option B: GitHub secret
Set repo secret:
- `TRACKWAY_CONFIG_YAML` = full YAML content.

Workflow will write it to `/opt/trackway/config.yaml` on each deploy.

## 7. Enable auto-deploy

No extra scripts needed:
- Push to `main` triggers `.github/workflows/ci-cd.yml`.
- It runs tests, builds/pushes image to GHCR, uploads repo to `/opt/trackway` over SSH, creates ClickHouse backup, starts ClickHouse and waits for health, then pulls and starts `trackway`.

First bootstrap deployment can be run manually:

```bash
cd /opt/trackway
TRACKWAY_IMAGE=ghcr.io/<owner>/trackway:latest docker compose --project-name trackway up -d
```

## 8. Database safety model

Protection layers in this setup:

1. Stable named volume: `trackway-clickhouse-data` in `docker-compose.yml`.
2. Deploy uses `pull + up -d` (not destructive volume recreate).
3. Backup before each deploy (`scripts/backup-clickhouse.sh`).
4. Nightly backup workflow (`clickhouse-backup.yml`).
5. Old backups rotation with `BACKUP_KEEP` (default 30).

Critical rule:
- Never run `docker compose down -v` in production.

## 9. Verify after deploy

```bash
docker compose --project-name trackway -f /opt/trackway/docker-compose.yml ps
curl -fsS http://127.0.0.1:8083/healthz
ls -lh /opt/trackway/backups
```

Note:
- `trackway` listens on `127.0.0.1:8083` on the host (for Caddy local reverse_proxy).
- `clickhouse` and `trackway` are connected through shared Docker network (`trackway-net`).
- In containerized mode, set `storage.clickhouse.addr: "clickhouse:9000"` in `/opt/trackway/config.yaml`.

## 10. Restore ClickHouse from backup

Pick backup archive:

```bash
ls -1t /opt/trackway/backups/clickhouse-*.tar.gz | head
```

Restore:

```bash
COMPOSE_PROJECT_NAME=trackway bash /opt/trackway/scripts/restore-clickhouse.sh \
  /opt/trackway/backups/clickhouse-YYYYMMDDTHHMMSSZ.tar.gz \
  /opt/trackway
```

Then verify:

```bash
docker compose --project-name trackway -f /opt/trackway/docker-compose.yml ps
```

## 11. Recommended GitHub settings

- Protect `main` branch:
  - Require PR and passing checks.
- Keep Actions enabled for repository.
- Ensure self-hosted runner has labels `self-hosted`, `Linux`, and `X64`.
