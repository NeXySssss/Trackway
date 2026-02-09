#!/usr/bin/env bash
set -euo pipefail

DEPLOY_DIR="${1:-/opt/trackway}"
BACKUP_DIR="${2:-/opt/trackway/backups}"
PROJECT_NAME="${COMPOSE_PROJECT_NAME:-trackway}"
KEEP_COUNT="${BACKUP_KEEP:-30}"

if ! [[ "${KEEP_COUNT}" =~ ^[0-9]+$ ]] || [ "${KEEP_COUNT}" -lt 1 ]; then
  echo "BACKUP_KEEP must be a positive integer, got: ${KEEP_COUNT}" >&2
  exit 1
fi

compose=(
  docker compose
  --project-name "${PROJECT_NAME}"
  -f "${DEPLOY_DIR}/docker-compose.yml"
)

mkdir -p "${BACKUP_DIR}"

container_id="$("${compose[@]}" ps -q clickhouse || true)"
if [ -z "${container_id}" ]; then
  echo "ClickHouse container is not running yet, skipping backup."
  exit 0
fi

volume_name="$(docker inspect --format '{{range .Mounts}}{{if eq .Destination "/var/lib/clickhouse"}}{{.Name}}{{end}}{{end}}' "${container_id}")"
if [ -z "${volume_name}" ]; then
  echo "Cannot detect ClickHouse data volume from container: ${container_id}" >&2
  exit 1
fi

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
archive_name="clickhouse-${timestamp}.tar.gz"
archive_path="${BACKUP_DIR}/${archive_name}"
backup_container="trackway-ch-backup-${timestamp}"

docker run --rm \
  --name "${backup_container}" \
  -v "${volume_name}:/var/lib/clickhouse:ro" \
  -v "${BACKUP_DIR}:/backup" \
  alpine:3.20 \
  sh -euc "cd /var/lib/clickhouse && tar -czf /backup/${archive_name} ."

sha256sum "${archive_path}" > "${archive_path}.sha256"
echo "Backup created: ${archive_path}"

mapfile -t archives < <(ls -1t "${BACKUP_DIR}"/clickhouse-*.tar.gz 2>/dev/null || true)
if [ "${#archives[@]}" -le "${KEEP_COUNT}" ]; then
  exit 0
fi

for old_archive in "${archives[@]:${KEEP_COUNT}}"; do
  rm -f "${old_archive}" "${old_archive}.sha256"
done
