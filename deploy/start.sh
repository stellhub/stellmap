#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  sudo bash /data/start.sh

Or override install options:
  sudo bash /data/start.sh \
    --source-dir /data \
    --service-name stellmapd-node-1 \
    --install-root /opt/stellmap \
    --config-dir /etc/stellmapd

Options:
  --source-dir PATH            源文件目录，默认脚本所在目录
  --binary-file PATH           stellmapd 二进制路径，默认 {source-dir}/stellmapd
  --ctl-file PATH              stellmapctl 二进制路径，默认 {source-dir}/stellmapctl
  --config-file PATH           启动配置文件路径，默认 {source-dir}/stellmapd.toml
  --service-name NAME          systemd 服务名，默认 stellmapd
  --install-root PATH          安装根目录，默认 /opt/stellmap
  --config-dir PATH            配置目录，默认 /etc/stellmapd
  --config-file-name NAME      安装后的配置文件名，默认 stellmapd.toml
  --run-user USER              运行用户，默认 stellmap
  --run-group GROUP            运行组，默认 stellmap
  --health-url URL             健康检查地址，默认根据配置中的 http_addr 推导
  --health-timeout SEC         健康检查总超时秒数，默认 60
  --health-interval SEC        健康检查重试间隔秒数，默认 2
  --curl-connect-timeout SEC   curl 建连超时秒数，默认 5
  --curl-max-time SEC          curl 单次请求最大时长秒数，默认 30
  --skip-start                 仅安装，不立即启动
  -h, --help                   查看帮助

Behavior:
  1. 从本地目录读取 stellmapd、stellmapctl、stellmapd.toml。
  2. 安装到 /opt/stellmap/bin 和 /etc/stellmapd。
  3. 自动创建 systemd unit 并启动/重启服务。
EOF
}

die() {
  echo "ERROR: $*" >&2
  exit 1
}

log_step() {
  echo "==> $*"
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

detect_nologin_shell() {
  if [[ -x /usr/sbin/nologin ]]; then
    echo /usr/sbin/nologin
    return
  fi
  if [[ -x /sbin/nologin ]]; then
    echo /sbin/nologin
    return
  fi
  echo /bin/false
}

ensure_service_user() {
  local user="$1"
  local group="$2"

  if ! getent group "$group" >/dev/null 2>&1; then
    groupadd --system "$group"
  fi
  if ! id "$user" >/dev/null 2>&1; then
    useradd \
      --system \
      --gid "$group" \
      --home-dir /var/lib/stellmap \
      --create-home \
      --shell "$(detect_nologin_shell)" \
      "$user"
  fi
}

trim() {
  local value="$1"
  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  printf '%s' "${value}"
}

toml_get() {
  local file="$1"
  local section="$2"
  local key="$3"

  awk -v target_section="${section}" -v target_key="${key}" '
    function trim_value(value) {
      sub(/^[[:space:]]+/, "", value)
      sub(/[[:space:]]+$/, "", value)
      return value
    }

    /^\[[^]]+\][[:space:]]*$/ {
      current = $0
      gsub(/^\[/, "", current)
      gsub(/\]$/, "", current)
      current = trim_value(current)
      in_section = (current == target_section)
      next
    }

    in_section {
      line = $0
      sub(/#.*/, "", line)
      if (line !~ /=/) {
        next
      }

      split(line, pair, "=")
      candidate = trim_value(pair[1])
      if (candidate != target_key) {
        next
      }

      value = substr(line, index(line, "=") + 1)
      value = trim_value(value)
      gsub(/^"/, "", value)
      gsub(/"$/, "", value)
      print value
      exit
    }
  ' "${file}"
}

extract_port() {
  local addr="$1"

  [[ "${addr}" == *:* ]] || die "address must include port: ${addr}"
  printf '%s' "${addr##*:}"
}

wait_for_health() {
  local deadline now

  deadline=$(( $(date +%s) + HEALTH_TIMEOUT ))
  while true; do
    if curl \
      --fail \
      --silent \
      --show-error \
      --connect-timeout "${CURL_CONNECT_TIMEOUT}" \
      --max-time "${CURL_MAX_TIME}" \
      "${HEALTH_URL}" >/dev/null; then
      return 0
    fi

    now="$(date +%s)"
    if (( now >= deadline )); then
      return 1
    fi
    sleep "${HEALTH_INTERVAL}"
  done
}

write_unit() {
  local unit_path="$1"

  cat >"${unit_path}" <<EOF
[Unit]
Description=StellMap service ${SERVICE_NAME}
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${RUN_USER}
Group=${RUN_GROUP}
WorkingDirectory=${DATA_DIR}
ExecStart=${BINARY_PATH} --config=${CONFIG_PATH}
Restart=always
RestartSec=3
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SOURCE_DIR="${SCRIPT_DIR}"
BINARY_FILE=""
CTL_FILE=""
CONFIG_FILE=""
SERVICE_NAME="stellmapd"
INSTALL_ROOT="/opt/stellmap"
CONFIG_DIR="/etc/stellmapd"
CONFIG_FILE_NAME="stellmapd.toml"
RUN_USER="stellmap"
RUN_GROUP="stellmap"
HEALTH_URL=""
HEALTH_TIMEOUT=60
HEALTH_INTERVAL=2
CURL_CONNECT_TIMEOUT=5
CURL_MAX_TIME=30
SKIP_START="false"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --source-dir)
      SOURCE_DIR="$2"
      shift 2
      ;;
    --binary-file)
      BINARY_FILE="$2"
      shift 2
      ;;
    --ctl-file)
      CTL_FILE="$2"
      shift 2
      ;;
    --config-file)
      CONFIG_FILE="$2"
      shift 2
      ;;
    --service-name)
      SERVICE_NAME="$2"
      shift 2
      ;;
    --install-root)
      INSTALL_ROOT="$2"
      shift 2
      ;;
    --config-dir)
      CONFIG_DIR="$2"
      shift 2
      ;;
    --config-file-name)
      CONFIG_FILE_NAME="$2"
      shift 2
      ;;
    --run-user)
      RUN_USER="$2"
      shift 2
      ;;
    --run-group)
      RUN_GROUP="$2"
      shift 2
      ;;
    --health-url)
      HEALTH_URL="$2"
      shift 2
      ;;
    --health-timeout)
      HEALTH_TIMEOUT="$2"
      shift 2
      ;;
    --health-interval)
      HEALTH_INTERVAL="$2"
      shift 2
      ;;
    --curl-connect-timeout)
      CURL_CONNECT_TIMEOUT="$2"
      shift 2
      ;;
    --curl-max-time)
      CURL_MAX_TIME="$2"
      shift 2
      ;;
    --skip-start)
      SKIP_START="true"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "unknown argument: $1"
      ;;
  esac
done

if [[ "${EUID}" -ne 0 ]]; then
  die "please run as root or use sudo"
fi

require_cmd systemctl
require_cmd install
require_cmd curl
require_cmd awk
require_cmd getent
require_cmd id
require_cmd useradd
require_cmd groupadd

SOURCE_DIR="$(cd "${SOURCE_DIR}" && pwd)"
if [[ -z "${BINARY_FILE}" ]]; then
  BINARY_FILE="${SOURCE_DIR}/stellmapd"
fi
if [[ -z "${CTL_FILE}" ]]; then
  CTL_FILE="${SOURCE_DIR}/stellmapctl"
fi
if [[ -z "${CONFIG_FILE}" ]]; then
  CONFIG_FILE="${SOURCE_DIR}/stellmapd.toml"
fi

[[ -x "${BINARY_FILE}" ]] || die "stellmapd binary not found or not executable: ${BINARY_FILE}"
[[ -x "${CTL_FILE}" ]] || die "stellmapctl binary not found or not executable: ${CTL_FILE}"
[[ -f "${CONFIG_FILE}" ]] || die "config file not found: ${CONFIG_FILE}"

DATA_DIR="$(trim "$(toml_get "${CONFIG_FILE}" "node" "data_dir")")"
[[ -n "${DATA_DIR}" ]] || die "node.data_dir is required in ${CONFIG_FILE}"

if [[ -z "${HEALTH_URL}" ]]; then
  HTTP_ADDR="$(trim "$(toml_get "${CONFIG_FILE}" "server" "http_addr")")"
  [[ -n "${HTTP_ADDR}" ]] || die "server.http_addr is required in ${CONFIG_FILE}"
  HEALTH_URL="http://127.0.0.1:$(extract_port "${HTTP_ADDR}")/readyz"
fi

BIN_DIR="${INSTALL_ROOT}/bin"
BINARY_PATH="${BIN_DIR}/stellmapd"
CTL_PATH="${BIN_DIR}/stellmapctl"
CONFIG_PATH="${CONFIG_DIR}/${CONFIG_FILE_NAME}"
UNIT_PATH="/etc/systemd/system/${SERVICE_NAME}.service"

ensure_service_user "${RUN_USER}" "${RUN_GROUP}"

log_step "preparing directories"
install -d -m 0755 "${BIN_DIR}" "${CONFIG_DIR}" "${DATA_DIR}"
chown -R "${RUN_USER}:${RUN_GROUP}" "${DATA_DIR}"

log_step "installing binaries"
install -m 0755 "${BINARY_FILE}" "${BINARY_PATH}"
install -m 0755 "${CTL_FILE}" "${CTL_PATH}"

log_step "installing config"
install -m 0640 -o "${RUN_USER}" -g "${RUN_GROUP}" "${CONFIG_FILE}" "${CONFIG_PATH}"

log_step "writing systemd unit"
write_unit "${UNIT_PATH}"
systemctl daemon-reload
systemctl enable "${SERVICE_NAME}" >/dev/null

if [[ "${SKIP_START}" == "true" ]]; then
  log_step "installation completed, service not started"
else
  log_step "starting service ${SERVICE_NAME}"
  systemctl restart "${SERVICE_NAME}"
  if ! wait_for_health; then
    die "service health check failed: ${HEALTH_URL}"
  fi
fi

echo
echo "Service name : ${SERVICE_NAME}"
echo "Binary path  : ${BINARY_PATH}"
echo "Ctl path     : ${CTL_PATH}"
echo "Config path  : ${CONFIG_PATH}"
echo "Data dir     : ${DATA_DIR}"
echo "Health url   : ${HEALTH_URL}"
