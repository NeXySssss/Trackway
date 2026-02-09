#!/usr/bin/env bash
set -euo pipefail

if [ "${#}" -lt 1 ]; then
  echo "Usage: $0 <backup-archive.tar.gz> [deploy-dir]" >&2
  exit 1
fi

BACKUP_ARCHIVE="${1}"
DEPLOY_DIR="${2:-/opt/trackway}"
PROJECT_NAME="${COMPOSE_PROJECT_NAME:-trackway}"

if [ ! -f "${BACKUP_ARCHIVE}" ]; then
  echo "Backup archive not found: ${BACKUP_ARCHIVE}" >&2
  exit 1
fi

compose=(
  docker compose
  --project-name "${PROJECT_NAME}"
  -f "${DEPLOY_DIR}/docker-compose.yml"
)

"${compose[@]}" up -d clickhouse >/dev/null
container_id="$("${compose[@]}" ps -q clickhouse)"
if [ -z "${container_id}" ]; then
  echo "Cannot find ClickHouse container after startup." >&2
  exit 1
fi

volume_name="$(docker inspect --format '{{range .Mounts}}{{if eq .Destination "/var/lib/clickhouse"}}{{.Name}}{{end}}{{end}}' "${container_id}")"
if [ -z "${volume_name}" ]; then
  echo "Cannot detect ClickHouse volume for restore." >&2
  exit 1
fi

"${compose[@]}" stop trackway clickhouse >/dev/null || true

docker run --rm \
  -v "${volume_name}:/var/lib/clickhouse" \
  alpine:3.20 \
  sh -euc "rm -rf /var/lib/clickhouse/*"

backup_dir="$(cd "$(dirname "${BACKUP_ARCHIVE}")" && pwd)"
backup_file="$(basename "${BACKUP_ARCHIVE}")"

docker run --rm \
  -v "${volume_name}:/var/lib/clickhouse" \
  -v "${backup_dir}:/backup:ro" \
  alpine:3.20 \
  sh -euc "cd /var/lib/clickhouse && tar -xzf /backup/${backup_file}"

"${compose[@]}" up -d
echo "Restore completed from ${BACKUP_ARCHIVE}"
