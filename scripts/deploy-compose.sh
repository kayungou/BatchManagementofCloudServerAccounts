#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="${1:-${APP_IMAGE:-}}"
ENV_FILE="${ENV_FILE:-${ROOT_DIR}/.env.production}"
COMPOSE_FILE="${COMPOSE_FILE:-${ROOT_DIR}/deploy/docker-compose.production.yml}"
BACKUP_DIR="${BACKUP_DIR:-${ROOT_DIR}/backups}"
CURRENT_IMAGE_FILE="${ROOT_DIR}/.deploy-current-image"
PREVIOUS_IMAGE=""
HOST_PORT="${APP_PORT:-}"

die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

on_error() {
  printf 'deployment failed. Database migrations are not rolled back automatically.\n' >&2
  if [[ -n "${PREVIOUS_IMAGE}" ]]; then
    printf 'previous image: %s\n' "${PREVIOUS_IMAGE}" >&2
  fi
}
trap on_error ERR

[[ -n "${IMAGE}" ]] || die "pass a GHCR image tag or digest"
[[ "${IMAGE}" =~ ^ghcr\.io/[a-z0-9._-]+/[a-z0-9._/-]+(:[A-Za-z0-9._-]+|@sha256:[a-f0-9]{64})$ ]] || die "invalid GHCR image reference"
[[ -f "${ENV_FILE}" ]] || die "missing ${ENV_FILE}"
[[ -f "${COMPOSE_FILE}" ]] || die "missing ${COMPOSE_FILE}"
command -v docker >/dev/null 2>&1 || die "docker is required"
command -v curl >/dev/null 2>&1 || die "curl is required"
docker compose version >/dev/null 2>&1 || die "Docker Compose v2 is required"

if [[ -f "${CURRENT_IMAGE_FILE}" ]]; then
  PREVIOUS_IMAGE="$(<"${CURRENT_IMAGE_FILE}")"
fi

if [[ -z "${HOST_PORT}" ]]; then
  HOST_PORT="$(awk -F= '$1 == "APP_PORT" { sub(/^[^=]*=/, ""); gsub(/^[[:space:]"'\'' ]+|[[:space:]"'\'' ]+$/, ""); print; exit }' "${ENV_FILE}")"
  HOST_PORT="${HOST_PORT:-8080}"
fi
[[ "${HOST_PORT}" =~ ^[0-9]+$ ]] || die "APP_PORT must be numeric"

if command -v flock >/dev/null 2>&1; then
  exec 9>"${ROOT_DIR}/.deploy.lock"
  flock -n 9 || die "another deployment is already running"
fi

compose() {
  APP_IMAGE="${IMAGE}" docker compose --env-file "${ENV_FILE}" -f "${COMPOSE_FILE}" "$@"
}

mkdir -p "${BACKUP_DIR}"
compose pull api worker
compose up -d --wait --wait-timeout 180 db

backup_path="${BACKUP_DIR}/pre-deploy-$(date -u +%Y%m%dT%H%M%SZ).dump"
compose exec -T db sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' >"${backup_path}"
printf 'database backup: %s\n' "${backup_path}"

compose run --rm --no-deps api migrate
compose up -d --no-build --wait --wait-timeout 180 api worker
curl --fail --silent --show-error "http://127.0.0.1:${HOST_PORT}/readyz" >/dev/null

printf '%s\n' "${IMAGE}" >"${CURRENT_IMAGE_FILE}"
printf 'deployment ready: %s\n' "${IMAGE}"
