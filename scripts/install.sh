#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_DIR="/opt/cloud-account-manager"
CONFIG_DIR="/etc/cloud-account-manager"
ENV_FILE="${CONFIG_DIR}/cloudmanager.env"
BINARY_PATH="/usr/local/bin/cloudmanager"
GO_VERSION="${GO_VERSION:-1.26.0}"
NODE_VERSION="${NODE_VERSION:-22.17.0}"
APP_BASE_URL_VALUE="${INSTALL_APP_BASE_URL:-}"
DATABASE_URL_VALUE="${INSTALL_DATABASE_URL:-}"
ADMIN_EMAIL_VALUE="${ADMIN_EMAIL:-}"
ADMIN_PASSWORD_VALUE="${ADMIN_PASSWORD:-}"
APP_VERSION_VALUE="${APP_VERSION:-dev}"
APP_COMMIT_VALUE="${APP_COMMIT:-unknown}"
APP_BUILD_TIME_VALUE="${APP_BUILD_TIME:-}"
POSTGRES_MODE="auto"
SKIP_ADMIN=false
NO_START=false

usage() {
  cat <<'EOF'
用法: sudo ./scripts/install.sh [选项]

在 Ubuntu 24.04 或 Debian 12 上构建并安装 Cloud Account Manager。
默认安装本机 PostgreSQL 18；API 仅监听 127.0.0.1:8080。

选项:
  --app-base-url URL       对外访问地址，例如 https://cloud.example.com
  --database-url URL       使用外部 PostgreSQL 18，并跳过本机数据库安装
  --with-postgres          强制安装并使用本机 PostgreSQL 18
  --no-postgres            不安装 PostgreSQL；需同时提供 --database-url
  --admin-email EMAIL      创建初始管理员
  --admin-password PASS    管理员密码；自动化时建议改用 ADMIN_PASSWORD 环境变量
  --skip-admin             不询问是否创建管理员
  --no-start               安装并启用服务，但暂不启动
  -h, --help               显示帮助

也可通过 GO_VERSION、NODE_VERSION、APP_VERSION、APP_COMMIT、
APP_BUILD_TIME、INSTALL_APP_BASE_URL、INSTALL_DATABASE_URL、
ADMIN_EMAIL、ADMIN_PASSWORD 环境变量传值。
EOF
}

die() {
  printf '错误: %s\n' "$*" >&2
  exit 1
}

need_root() {
  [[ "${EUID}" -eq 0 ]] || die "请使用 sudo 运行安装脚本"
}

need_value() {
  [[ $# -ge 2 ]] || die "$1 缺少参数"
}

env_value_from_file() {
  local key="$1" file="$2"
  awk -F= -v key="$key" '
    $1 == key {
      sub(/^[^=]*=/, "")
      gsub(/^[[:space:]"'\'' ]+|[[:space:]"'\'' ]+$/, "")
      print
      exit
    }
  ' "${file}" 2>/dev/null || true
}

set_env_value() {
  local key="$1" value="$2" temporary
  temporary="$(mktemp "${ENV_FILE}.XXXXXX")"
  awk -v key="${key}" -v value="${value}" '
    BEGIN { replaced = 0 }
    index($0, key "=") == 1 {
      print key "=" value
      replaced = 1
      next
    }
    { print }
    END { if (!replaced) print key "=" value }
  ' "${ENV_FILE}" >"${temporary}"
  mv "${temporary}" "${ENV_FILE}"
  chmod 640 "${ENV_FILE}"
  chown root:cloudmanager "${ENV_FILE}"
}

random_hex() {
  openssl rand -hex "$1"
}

random_master_key() {
  openssl rand -base64 32
}

validate_master_key() {
  local key="$1" decoded_length
  [[ "${key}" =~ ^[A-Za-z0-9+/]{43}=$ ]] || die "MASTER_KEY 必须是标准 Base64 编码的 32 字节密钥"
  decoded_length="$(printf '%s' "${key}" | openssl base64 -d -A 2>/dev/null | wc -c | tr -d '[:space:]')"
  [[ "${decoded_length}" == "32" ]] || die "MASTER_KEY 无法解码为 32 字节"
}

validate_url() {
  local name="$1" value="$2"
  [[ "${value}" != *$'\n'* && "${value}" != *$'\r'* ]] || die "${name} 不能包含换行符"
}

validate_identifier() {
  [[ "$1" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || die "非法 PostgreSQL 标识符: $1"
}

detect_platform() {
  [[ -r /etc/os-release ]] || die "无法识别操作系统"
  # shellcheck disable=SC1091
  source /etc/os-release
  case "${ID}:${VERSION_ID}" in
    ubuntu:24.04)
      PG_CODENAME="noble-pgdg"
      ;;
    debian:12)
      PG_CODENAME="bookworm-pgdg"
      ;;
    *)
      die "仅支持 Ubuntu 24.04 和 Debian 12，当前为 ${PRETTY_NAME:-unknown}"
      ;;
  esac

  case "$(dpkg --print-architecture)" in
    amd64)
      GO_ARCH="amd64"
      NODE_ARCH="x64"
      ;;
    arm64)
      GO_ARCH="arm64"
      NODE_ARCH="arm64"
      ;;
    *)
      die "仅支持 amd64 和 arm64"
      ;;
  esac
}

select_postgres_mode() {
  local existing_url
  if [[ "${POSTGRES_MODE}" != "auto" ]]; then
    return
  fi
  if [[ -n "${DATABASE_URL_VALUE}" ]]; then
    POSTGRES_MODE="external"
    return
  fi
  if [[ -f "${ENV_FILE}" ]]; then
    existing_url="$(env_value_from_file DATABASE_URL "${ENV_FILE}")"
    if [[ "${existing_url}" == *"@127.0.0.1:"* || "${existing_url}" == *"@localhost:"* ]]; then
      POSTGRES_MODE="local"
    else
      POSTGRES_MODE="external"
    fi
    return
  fi
  POSTGRES_MODE="local"
}

install_base_packages() {
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y --no-install-recommends ca-certificates curl gnupg openssl xz-utils postgresql-client
}

install_postgresql_18() {
  export DEBIAN_FRONTEND=noninteractive
  install -d -m 0755 /usr/share/postgresql-common/pgdg
  curl -fsSL https://www.postgresql.org/media/keys/ACCC4CF8.asc -o /usr/share/postgresql-common/pgdg/apt.postgresql.org.asc
  printf 'deb [signed-by=/usr/share/postgresql-common/pgdg/apt.postgresql.org.asc] https://apt.postgresql.org/pub/repos/apt %s main\n' "${PG_CODENAME}" >/etc/apt/sources.list.d/pgdg.list
  apt-get update
  apt-get install -y --no-install-recommends postgresql-18 postgresql-client-18
  systemctl enable --now postgresql.service
}

download_and_verify() {
  local base_url="$1" archive="$2" sums_name="$3" destination="$4" expected actual
  curl -fsSLo "${BUILD_TMP}/${archive}" "${base_url}/${archive}"
  curl -fsSLo "${BUILD_TMP}/${sums_name}" "${base_url}/${sums_name}"
  if [[ "${sums_name}" == *.sha256 ]]; then
    expected="$(tr -d '[:space:]' <"${BUILD_TMP}/${sums_name}")"
    actual="$(sha256sum "${BUILD_TMP}/${archive}" | awk '{print $1}')"
    [[ "${actual}" == "${expected}" ]] || die "${archive} 的 SHA-256 校验失败"
  else
    (
      cd "${BUILD_TMP}"
      grep -E "[[:space:]]${archive}$" "${sums_name}" | sha256sum -c -
    )
  fi
  mv "${BUILD_TMP}/${archive}" "${destination}"
}

install_go() {
  local archive="go${GO_VERSION}.linux-${GO_ARCH}.tar.gz"
  if [[ -x /usr/local/go/bin/go ]] && /usr/local/go/bin/go version | grep -Fq "go${GO_VERSION} "; then
    return
  fi
  download_and_verify "https://go.dev/dl" "${archive}" "go${GO_VERSION}.linux-${GO_ARCH}.tar.gz.sha256" "${BUILD_TMP}/${archive}.verified"
  rm -rf /usr/local/go
  tar -xzf "${BUILD_TMP}/${archive}.verified" -C /usr/local
}

install_node() {
  local archive="node-v${NODE_VERSION}-linux-${NODE_ARCH}.tar.xz"
  NODE_HOME="/opt/node-v${NODE_VERSION}-linux-${NODE_ARCH}"
  if [[ -x "${NODE_HOME}/bin/node" ]]; then
    return
  fi
  download_and_verify "https://nodejs.org/dist/v${NODE_VERSION}" "${archive}" "SHASUMS256.txt" "${BUILD_TMP}/${archive}.verified"
  tar -xJf "${BUILD_TMP}/${archive}.verified" -C /opt
}

ensure_service_user() {
  getent group cloudmanager >/dev/null 2>&1 || groupadd --system cloudmanager
  id cloudmanager >/dev/null 2>&1 || useradd --system --gid cloudmanager --home-dir "${APP_DIR}" --shell /usr/sbin/nologin cloudmanager
  install -d -m 0755 -o root -g root "${APP_DIR}" "${APP_DIR}/web"
  install -d -m 0750 -o root -g cloudmanager "${CONFIG_DIR}"
}

write_or_update_config() {
  local db_password cookie_secure
  if [[ ! -f "${ENV_FILE}" ]]; then
    APP_BASE_URL_VALUE="${APP_BASE_URL_VALUE:-http://127.0.0.1:8080}"
    if [[ "${POSTGRES_MODE}" == "local" ]]; then
      db_password="$(random_hex 24)"
      DATABASE_URL_VALUE="postgres://cloudmanager:${db_password}@127.0.0.1:5432/cloud_account_manager?sslmode=disable"
    else
      [[ -n "${DATABASE_URL_VALUE}" ]] || die "--no-postgres 需要同时提供 --database-url"
    fi
    [[ "${APP_BASE_URL_VALUE}" == http://* || "${APP_BASE_URL_VALUE}" == https://* ]] || die "APP_BASE_URL 必须以 http:// 或 https:// 开头"
    validate_url APP_BASE_URL "${APP_BASE_URL_VALUE}"
    validate_url DATABASE_URL "${DATABASE_URL_VALUE}"
    cookie_secure=false
    [[ "${APP_BASE_URL_VALUE}" == https://* ]] && cookie_secure=true
    umask 077
    cat >"${ENV_FILE}" <<EOF
# Cloud Account Manager production environment. Keep this file secret.
APP_ENV=production
LISTEN_ADDR=127.0.0.1:8080
APP_BASE_URL=${APP_BASE_URL_VALUE}
DATABASE_URL=${DATABASE_URL_VALUE}
MASTER_KEY=$(random_master_key)
COOKIE_NAME=cloud_manager_session
COOKIE_SECURE=${cookie_secure}
SESSION_TTL=168h
FRONTEND_DIR=${APP_DIR}/web/dist
DEV_EXPOSE_TOKENS=false
RUN_WORKER=false
WORKER_CONCURRENCY=4
WORKER_POLL_INTERVAL=2s
SYNC_INTERVAL=5m
EOF
  else
    printf '保留现有配置和 MASTER_KEY: %s\n' "${ENV_FILE}"
    if [[ -n "${APP_BASE_URL_VALUE}" ]]; then
      validate_url APP_BASE_URL "${APP_BASE_URL_VALUE}"
      [[ "${APP_BASE_URL_VALUE}" == http://* || "${APP_BASE_URL_VALUE}" == https://* ]] || die "APP_BASE_URL 必须以 http:// 或 https:// 开头"
      set_env_value APP_BASE_URL "${APP_BASE_URL_VALUE}"
      if [[ "${APP_BASE_URL_VALUE}" == https://* ]]; then
        set_env_value COOKIE_SECURE true
      else
        set_env_value COOKIE_SECURE false
      fi
    fi
    if [[ -n "${DATABASE_URL_VALUE}" ]]; then
      validate_url DATABASE_URL "${DATABASE_URL_VALUE}"
      set_env_value DATABASE_URL "${DATABASE_URL_VALUE}"
    fi
  fi
  chmod 640 "${ENV_FILE}"
  chown root:cloudmanager "${ENV_FILE}"
  DATABASE_URL_VALUE="$(env_value_from_file DATABASE_URL "${ENV_FILE}")"
  MASTER_KEY_VALUE="$(env_value_from_file MASTER_KEY "${ENV_FILE}")"
  [[ -n "${DATABASE_URL_VALUE}" && -n "${MASTER_KEY_VALUE}" ]] || die "生产配置缺少 DATABASE_URL 或 MASTER_KEY"
  validate_master_key "${MASTER_KEY_VALUE}"
}

configure_local_database() {
  local without_scheme credentials address path db_user db_password db_host db_name
  without_scheme="${DATABASE_URL_VALUE#postgres://}"
  [[ "${without_scheme}" != "${DATABASE_URL_VALUE}" && "${without_scheme}" == *@*/* ]] || die "本机 DATABASE_URL 格式无效"
  credentials="${without_scheme%%@*}"
  address="${without_scheme#*@}"
  path="${address#*/}"
  db_user="${credentials%%:*}"
  db_password="${credentials#*:}"
  db_host="${address%%:*}"
  db_name="${path%%\?*}"
  [[ "${db_host}" == "127.0.0.1" || "${db_host}" == "localhost" ]] || die "本机数据库 URL 必须使用 127.0.0.1 或 localhost"
  [[ "${db_user}" != "${db_password}" ]] || die "DATABASE_URL 必须包含数据库密码"
  validate_identifier "${db_user}"
  validate_identifier "${db_name}"

  runuser -u postgres -- psql -X -v ON_ERROR_STOP=1 \
    -v role_name="${db_user}" -v role_password="${db_password}" -v db_name="${db_name}" <<'SQL'
SELECT format('CREATE ROLE %I LOGIN PASSWORD %L', :'role_name', :'role_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'role_name') \gexec
ALTER ROLE :"role_name" LOGIN PASSWORD :'role_password';
SELECT format('CREATE DATABASE %I OWNER %I', :'db_name', :'role_name')
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = :'db_name') \gexec
SQL
}

build_and_install_application() {
  local build_time ldflags
  build_time="${APP_BUILD_TIME_VALUE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
  ldflags="-s -w -X github.com/kayungou/BatchManagementofCloudServerAccounts/internal/buildinfo.Version=${APP_VERSION_VALUE} -X github.com/kayungou/BatchManagementofCloudServerAccounts/internal/buildinfo.Commit=${APP_COMMIT_VALUE} -X github.com/kayungou/BatchManagementofCloudServerAccounts/internal/buildinfo.BuildTime=${build_time}"
  export PATH="${NODE_HOME}/bin:/usr/local/go/bin:${PATH}"
  (
    cd "${ROOT_DIR}/web"
    npm ci
    npm run build
  )
  (
    cd "${ROOT_DIR}"
    go build -trimpath -ldflags="${ldflags}" -o "${BUILD_TMP}/cloudmanager" ./cmd/cloudmanager
  )

  systemctl stop cloudmanager-worker.service cloudmanager-api.service >/dev/null 2>&1 || true
  install -m 0755 -o root -g root "${BUILD_TMP}/cloudmanager" "${BINARY_PATH}"
  rm -rf "${APP_DIR}/web/dist"
  install -d -m 0755 -o root -g root "${APP_DIR}/web/dist"
  cp -a "${ROOT_DIR}/web/dist/." "${APP_DIR}/web/dist/"
  chown -R root:root "${APP_DIR}/web/dist"
}

install_systemd_units() {
  install -m 0644 -o root -g root "${ROOT_DIR}/deploy/systemd/cloudmanager-api.service" /etc/systemd/system/cloudmanager-api.service
  install -m 0644 -o root -g root "${ROOT_DIR}/deploy/systemd/cloudmanager-worker.service" /etc/systemd/system/cloudmanager-worker.service
  systemctl daemon-reload
  systemctl enable cloudmanager-api.service cloudmanager-worker.service
}

run_migrations() {
  runuser -u cloudmanager -- env \
    APP_ENV=production \
    DATABASE_URL="${DATABASE_URL_VALUE}" \
    MASTER_KEY="${MASTER_KEY_VALUE}" \
    FRONTEND_DIR="${APP_DIR}/web/dist" \
    "${BINARY_PATH}" migrate
}

maybe_create_admin() {
  local active_admins existing answer
  active_admins="$(psql -X "${DATABASE_URL_VALUE}" -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM users WHERE role='admin' AND status='active'")"
  if [[ "${SKIP_ADMIN}" == true ]]; then
    return
  fi
  if [[ -z "${ADMIN_EMAIL_VALUE}" ]]; then
    if [[ "${active_admins}" != "0" ]]; then
      return
    fi
    if [[ ! -t 0 ]]; then
      printf '当前没有管理员。请稍后运行 sudo -u cloudmanager %s admin。\n' "${BINARY_PATH}"
      return
    fi
    read -r -p '尚无管理员，是否现在创建？[Y/n] ' answer
    if [[ "${answer}" =~ ^[Nn]$ ]]; then
      return
    fi
    read -r -p '管理员邮箱: ' ADMIN_EMAIL_VALUE
  fi

  existing="$(psql -X "${DATABASE_URL_VALUE}" -v ON_ERROR_STOP=1 -At -v email="${ADMIN_EMAIL_VALUE}" <<'SQL'
SELECT 1 FROM users WHERE email = :'email';
SQL
)"
  if [[ "${existing}" == "1" ]]; then
    printf '用户 %s 已存在，未重复创建。\n' "${ADMIN_EMAIL_VALUE}"
    return
  fi
  if [[ -z "${ADMIN_PASSWORD_VALUE}" ]]; then
    [[ -t 0 ]] || die "指定管理员邮箱时还需通过 ADMIN_PASSWORD 提供密码"
    read -r -s -p '管理员密码（12-128 个字符）: ' ADMIN_PASSWORD_VALUE
    printf '\n'
  fi
  printf '%s\n' "${ADMIN_PASSWORD_VALUE}" | runuser -u cloudmanager -- env \
    APP_ENV=production \
    DATABASE_URL="${DATABASE_URL_VALUE}" \
    MASTER_KEY="${MASTER_KEY_VALUE}" \
    FRONTEND_DIR="${APP_DIR}/web/dist" \
    "${BINARY_PATH}" admin -email "${ADMIN_EMAIL_VALUE}"
  ADMIN_PASSWORD_VALUE=''
}

start_services() {
  local ready=false
  if [[ "${NO_START}" == true ]]; then
    printf '服务已启用但未启动。可运行 systemctl start cloudmanager-api cloudmanager-worker。\n'
    return
  fi
  systemctl restart cloudmanager-api.service
  for _ in {1..45}; do
    if curl -fsS http://127.0.0.1:8080/readyz >/dev/null 2>&1; then
      ready=true
      break
    fi
    sleep 1
  done
  if [[ "${ready}" != true ]]; then
    systemctl --no-pager --full status cloudmanager-api.service || true
    die "API 在 45 秒内未就绪，请查看 journalctl -u cloudmanager-api"
  fi
  systemctl restart cloudmanager-worker.service
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --app-base-url)
      need_value "$@"
      APP_BASE_URL_VALUE="$2"
      shift 2
      ;;
    --database-url)
      need_value "$@"
      DATABASE_URL_VALUE="$2"
      POSTGRES_MODE="external"
      shift 2
      ;;
    --with-postgres)
      POSTGRES_MODE="local"
      shift
      ;;
    --no-postgres)
      POSTGRES_MODE="external"
      shift
      ;;
    --admin-email)
      need_value "$@"
      ADMIN_EMAIL_VALUE="$2"
      shift 2
      ;;
    --admin-password)
      need_value "$@"
      ADMIN_PASSWORD_VALUE="$2"
      shift 2
      ;;
    --skip-admin)
      SKIP_ADMIN=true
      shift
      ;;
    --no-start)
      NO_START=true
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

need_root
detect_platform
select_postgres_mode
install_base_packages
if [[ "${POSTGRES_MODE}" == "local" ]]; then
  install_postgresql_18
elif [[ ! -f "${ENV_FILE}" && -z "${DATABASE_URL_VALUE}" ]]; then
  die "跳过本机 PostgreSQL 时必须提供 --database-url"
fi

BUILD_TMP="$(mktemp -d)"
trap 'rm -rf "${BUILD_TMP}"' EXIT
install_go
install_node
ensure_service_user
write_or_update_config
if [[ "${POSTGRES_MODE}" == "local" ]]; then
  configure_local_database
fi
build_and_install_application
install_systemd_units
run_migrations
maybe_create_admin
start_services

printf '\n安装完成。\n'
printf '本机服务: http://127.0.0.1:8080\n'
printf '配置文件: %s\n' "${ENV_FILE}"
printf '状态检查: systemctl status cloudmanager-api cloudmanager-worker\n'
printf '运行日志: journalctl -u cloudmanager-api -u cloudmanager-worker -f\n'
if [[ "$(env_value_from_file APP_BASE_URL "${ENV_FILE}")" == http://127.0.0.1:* ]]; then
  printf '请配置 HTTPS 反向代理，并把 APP_BASE_URL 与 COOKIE_SECURE 改为公网域名对应值。\n'
fi
