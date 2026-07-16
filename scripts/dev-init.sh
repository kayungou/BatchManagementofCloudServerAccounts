#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${ROOT_DIR}/.env.local"
MODE="local"
ENV_ONLY=false
SKIP_ADMIN=false
ADMIN_EMAIL_VALUE="${ADMIN_EMAIL:-}"
ADMIN_PASSWORD_VALUE="${ADMIN_PASSWORD:-}"

usage() {
  cat <<'EOF'
用法: ./scripts/dev-init.sh [选项]

初始化本机 PostgreSQL 18 开发数据库、生成主密钥并执行迁移。

选项:
  --docker                 使用 Docker Compose 中的 PostgreSQL 18
  --env-only               只生成或补全 .env.local
  --admin-email EMAIL      创建初始管理员（密码从终端或 ADMIN_PASSWORD 读取）
  --admin-password PASS    管理员密码；自动化时建议改用 ADMIN_PASSWORD 环境变量
  --skip-admin             不询问是否创建管理员
  -h, --help               显示帮助
EOF
}

die() {
  printf '错误: %s\n' "$*" >&2
  exit 1
}

need_command() {
  command -v "$1" >/dev/null 2>&1 || die "缺少命令 $1"
}

env_value() {
  local key="$1"
  awk -F= -v key="$key" '
    $1 == key {
      sub(/^[^=]*=/, "")
      gsub(/^[[:space:]"'\'' ]+|[[:space:]"'\'' ]+$/, "")
      print
      exit
    }
  ' "${ENV_FILE}" 2>/dev/null || true
}

generate_master_key() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -base64 32
    return
  fi
  if command -v base64 >/dev/null 2>&1 && [[ -r /dev/urandom ]]; then
    dd if=/dev/urandom bs=32 count=1 2>/dev/null | base64 | tr -d '\n'
    printf '\n'
    return
  fi
  die "无法生成 MASTER_KEY，请安装 openssl"
}

validate_master_key() {
  local key="$1"
  [[ "${key}" =~ ^[A-Za-z0-9+/]{43}=$ ]] || die "MASTER_KEY 必须是标准 Base64 编码的 32 字节密钥"
}

ensure_env_file() {
  local key temporary
  if [[ ! -f "${ENV_FILE}" ]]; then
    cp "${ROOT_DIR}/.env.example" "${ENV_FILE}"
  fi

  key="$(env_value MASTER_KEY)"
  if [[ -z "${key}" ]]; then
    key="$(generate_master_key)"
    temporary="$(mktemp "${ENV_FILE}.XXXXXX")"
    awk -v key="${key}" '
      BEGIN { replaced = 0 }
      /^MASTER_KEY=/ {
        print "MASTER_KEY=" key
        replaced = 1
        next
      }
      { print }
      END {
        if (!replaced) print "MASTER_KEY=" key
      }
    ' "${ENV_FILE}" >"${temporary}"
    mv "${temporary}" "${ENV_FILE}"
    printf '已生成新的 MASTER_KEY。该密钥用于解密托管凭据，请务必备份且不要更换。\n'
  fi
  validate_master_key "${key}"
  chmod 600 "${ENV_FILE}"
  printf '环境文件: %s\n' "${ENV_FILE}"
}

validate_identifier() {
  [[ "$1" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || die "非法 PostgreSQL 标识符: $1"
}

init_local_database() {
  local host port user password database exists
  host="${POSTGRES_HOST:-127.0.0.1}"
  port="${POSTGRES_PORT:-$(env_value POSTGRES_PORT)}"
  port="${port:-5432}"
  user="${POSTGRES_USER:-$(env_value POSTGRES_USER)}"
  user="${user:-ikun}"
  password="${POSTGRES_PASSWORD:-$(env_value POSTGRES_PASSWORD)}"
  password="${password:-ServBay.dev}"
  database="${POSTGRES_DB:-$(env_value POSTGRES_DB)}"
  database="${database:-cloud_account_manager}"

  validate_identifier "${database}"
  need_command psql
  need_command createdb
  need_command go

  export PGPASSWORD="${password}"
  if ! psql -X -v ON_ERROR_STOP=1 -h "${host}" -p "${port}" -U "${user}" -d postgres -Atc 'SELECT 1' >/dev/null 2>&1; then
    die "无法连接 PostgreSQL：${host}:${port}，用户 ${user}。请确认 PostgreSQL 18 已启动且密码正确"
  fi

  exists="$(psql -X -v ON_ERROR_STOP=1 -h "${host}" -p "${port}" -U "${user}" -d postgres -At -v db_name="${database}" <<'SQL'
SELECT 1 FROM pg_database WHERE datname = :'db_name';
SQL
)"
  if [[ "${exists}" != "1" ]]; then
    createdb -h "${host}" -p "${port}" -U "${user}" --maintenance-db=postgres "${database}"
    printf '已创建数据库 %s。\n' "${database}"
  else
    printf '数据库 %s 已存在，保留现有数据。\n' "${database}"
  fi

  (
    cd "${ROOT_DIR}"
    go run ./cmd/cloudmanager migrate
  )
}

compose() {
  docker compose --env-file "${ENV_FILE}" "$@"
}

init_docker_database() {
  local user database ready=false
  need_command docker
  docker compose version >/dev/null 2>&1 || die "需要 Docker Compose v2"
  user="${POSTGRES_USER:-$(env_value POSTGRES_USER)}"
  user="${user:-ikun}"
  database="${POSTGRES_DB:-$(env_value POSTGRES_DB)}"
  database="${database:-cloud_account_manager}"

  (
    cd "${ROOT_DIR}"
    compose up -d db
    for _ in {1..60}; do
      if compose exec -T db pg_isready -U "${user}" -d "${database}" >/dev/null 2>&1; then
        ready=true
        break
      fi
      sleep 1
    done
    [[ "${ready}" == true ]] || die "Docker PostgreSQL 在 60 秒内未就绪"
    compose build api
    compose run --rm --no-deps api migrate
  )
}

maybe_create_admin() {
  local answer existing
  if [[ "${SKIP_ADMIN}" == true ]]; then
    return
  fi

  if [[ -z "${ADMIN_EMAIL_VALUE}" ]]; then
    if [[ ! -t 0 ]]; then
      printf '未创建管理员。之后可运行 make admin。\n'
      return
    fi
    read -r -p '是否现在创建初始管理员？[y/N] ' answer
    if [[ ! "${answer}" =~ ^[Yy]$ ]]; then
      printf '已跳过管理员创建。之后可运行 make admin。\n'
      return
    fi
    read -r -p '管理员邮箱: ' ADMIN_EMAIL_VALUE
  fi

  if [[ -z "${ADMIN_PASSWORD_VALUE}" ]]; then
    [[ -t 0 ]] || die "指定管理员邮箱时还需通过 ADMIN_PASSWORD 提供密码"
    read -r -s -p '管理员密码（12-128 个字符）: ' ADMIN_PASSWORD_VALUE
    printf '\n'
  fi

  if [[ "${MODE}" == "docker" ]]; then
    local user database
    user="${POSTGRES_USER:-$(env_value POSTGRES_USER)}"
    user="${user:-ikun}"
    database="${POSTGRES_DB:-$(env_value POSTGRES_DB)}"
    database="${database:-cloud_account_manager}"
    existing="$(
      cd "${ROOT_DIR}"
      compose exec -T db psql -X -v ON_ERROR_STOP=1 -U "${user}" -d "${database}" -At -v email="${ADMIN_EMAIL_VALUE}" <<'SQL'
SELECT 1 FROM users WHERE email = :'email';
SQL
    )"
    if [[ "${existing}" == "1" ]]; then
      printf '用户 %s 已存在，未重复创建。\n' "${ADMIN_EMAIL_VALUE}"
      return
    fi
    (
      cd "${ROOT_DIR}"
      printf '%s\n' "${ADMIN_PASSWORD_VALUE}" | compose run --rm -T --no-deps api admin -email "${ADMIN_EMAIL_VALUE}"
    )
  else
    local database_url
    database_url="$(env_value DATABASE_URL)"
    existing="$(psql -X "${database_url}" -v ON_ERROR_STOP=1 -At -v email="${ADMIN_EMAIL_VALUE}" <<'SQL'
SELECT 1 FROM users WHERE email = :'email';
SQL
)"
    if [[ "${existing}" == "1" ]]; then
      printf '用户 %s 已存在，未重复创建。\n' "${ADMIN_EMAIL_VALUE}"
      return
    fi
    (
      cd "${ROOT_DIR}"
      printf '%s\n' "${ADMIN_PASSWORD_VALUE}" | go run ./cmd/cloudmanager admin -email "${ADMIN_EMAIL_VALUE}"
    )
  fi
  ADMIN_PASSWORD_VALUE=''
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --docker)
      MODE="docker"
      shift
      ;;
    --env-only)
      ENV_ONLY=true
      shift
      ;;
    --admin-email)
      [[ $# -ge 2 ]] || die "--admin-email 缺少参数"
      ADMIN_EMAIL_VALUE="$2"
      shift 2
      ;;
    --admin-password)
      [[ $# -ge 2 ]] || die "--admin-password 缺少参数"
      ADMIN_PASSWORD_VALUE="$2"
      shift 2
      ;;
    --skip-admin)
      SKIP_ADMIN=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "未知选项: $1"
      ;;
  esac
done

ensure_env_file
if [[ "${ENV_ONLY}" == true ]]; then
  exit 0
fi

if [[ "${MODE}" == "docker" ]]; then
  init_docker_database
else
  init_local_database
fi
maybe_create_admin

printf '\n初始化完成。\n'
if [[ "${MODE}" == "docker" ]]; then
  printf '运行 docker compose --env-file .env.local up -d 启动完整服务。\n'
else
  printf '运行 make build && make serve，然后访问 http://127.0.0.1:8080。\n'
fi
