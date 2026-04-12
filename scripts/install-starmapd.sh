#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  sudo bash scripts/install-starmapd.sh \
    --repo-dir /opt/src/StarMap \
    --node-id 1 \
    --cluster-id 100 \
    --peer-ids 1,2,3 \
    --peer-http-addrs 1=10.0.0.11:8080,2=10.0.0.12:8080,3=10.0.0.13:8080 \
    --peer-admin-addrs 1=127.0.0.1:18080,2=127.0.0.1:18080,3=127.0.0.1:18080 \
    --peer-grpc-addrs 1=10.0.0.11:19090,2=10.0.0.12:19090,3=10.0.0.13:19090

Options:
  --repo-dir PATH                  StarMap 仓库目录，脚本会在该目录执行 go build
  --node-id ID                     当前节点 ID
  --cluster-id ID                  当前集群 ID
  --region NAME                    当前节点所属 region，默认 default
  --data-dir PATH                  数据目录，默认 /var/lib/starmap/node-{node-id}
  --http-addr ADDR                 对外 HTTP 地址，默认 :8080
  --admin-addr ADDR                admin HTTP 地址，默认 127.0.0.1:18080
  --grpc-addr ADDR                 节点间 gRPC 地址，默认 :19090
  --admin-token TOKEN              admin token，默认 starmap
  --replication-token TOKEN        replication token，默认复用 admin token
  --prometheus-sd-token TOKEN      Prometheus SD token，默认复用 replication token
  --replication-targets-file PATH  replication target JSON 文件，脚本会复制到 /etc/starmapd
  --peer-ids IDS                   集群节点 ID 列表，默认仅当前 node-id
  --peer-http-addrs MAP            节点 HTTP 地址映射
  --peer-admin-addrs MAP           节点 admin 地址映射
  --peer-grpc-addrs MAP            节点 gRPC 地址映射
  --request-timeout DUR            单次请求超时，默认 5s
  --shutdown-timeout DUR           优雅退出超时，默认 10s
  --registry-cleanup-interval DUR  过期清理扫描间隔，默认 1s
  --registry-cleanup-delete-limit N 过期清理单轮上限，默认 128
  --service-name NAME              systemd 服务名，默认 starmapd-node-{node-id}
  --bin-dir PATH                   二进制安装目录，默认 /opt/starmap/bin
  --config-dir PATH                配置目录，默认 /etc/starmapd
  --config-file-name NAME          配置文件名，默认 {service-name}.toml
  --run-user USER                  运行用户，默认 starmap
  --run-group GROUP                运行组，默认 starmap
  --skip-start                     仅安装，不立即启动
  -h, --help                       查看帮助
EOF
}

die() {
  echo "ERROR: $*" >&2
  exit 1
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
      --home-dir /var/lib/starmap \
      --create-home \
      --shell "$(detect_nologin_shell)" \
      "$user"
  fi
}

escape_toml_string() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  printf '%s' "$value"
}

escape_sed_replacement() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//&/\\&}"
  value="${value//|/\\|}"
  printf '%s' "$value"
}

replace_placeholder() {
  local path="$1"
  local placeholder="$2"
  local value="$3"
  local escaped

  escaped="$(escape_sed_replacement "${value}")"
  sed -i "s|${placeholder}|${escaped}|g" "${path}"
}

render_config_from_template() {
  local template_path="$1"
  local output_path="$2"

  [[ -f "${template_path}" ]] || die "config template not found: ${template_path}"

  install -m 0640 -o "${RUN_USER}" -g "${RUN_GROUP}" "${template_path}" "${output_path}"

  replace_placeholder "${output_path}" "__NODE_ID__" "${NODE_ID}"
  replace_placeholder "${output_path}" "__CLUSTER_ID__" "${CLUSTER_ID}"
  replace_placeholder "${output_path}" "__REGION__" "$(escape_toml_string "${REGION}")"
  replace_placeholder "${output_path}" "__DATA_DIR__" "$(escape_toml_string "${DATA_DIR}")"
  replace_placeholder "${output_path}" "__HTTP_ADDR__" "$(escape_toml_string "${HTTP_ADDR}")"
  replace_placeholder "${output_path}" "__ADMIN_ADDR__" "$(escape_toml_string "${ADMIN_ADDR}")"
  replace_placeholder "${output_path}" "__GRPC_ADDR__" "$(escape_toml_string "${GRPC_ADDR}")"
  replace_placeholder "${output_path}" "__ADMIN_TOKEN__" "$(escape_toml_string "${ADMIN_TOKEN}")"
  replace_placeholder "${output_path}" "__REPLICATION_TOKEN__" "$(escape_toml_string "${REPLICATION_TOKEN}")"
  replace_placeholder "${output_path}" "__PROMETHEUS_SD_TOKEN__" "$(escape_toml_string "${PROMETHEUS_SD_TOKEN}")"
  replace_placeholder "${output_path}" "__PEER_IDS__" "$(escape_toml_string "${PEER_IDS}")"
  replace_placeholder "${output_path}" "__PEER_GRPC_ADDRS__" "$(escape_toml_string "${PEER_GRPC_ADDRS}")"
  replace_placeholder "${output_path}" "__PEER_HTTP_ADDRS__" "$(escape_toml_string "${PEER_HTTP_ADDRS}")"
  replace_placeholder "${output_path}" "__PEER_ADMIN_ADDRS__" "$(escape_toml_string "${PEER_ADMIN_ADDRS}")"
  replace_placeholder "${output_path}" "__REQUEST_TIMEOUT__" "$(escape_toml_string "${REQUEST_TIMEOUT}")"
  replace_placeholder "${output_path}" "__SHUTDOWN_TIMEOUT__" "$(escape_toml_string "${SHUTDOWN_TIMEOUT}")"
  replace_placeholder "${output_path}" "__REGISTRY_CLEANUP_INTERVAL__" "$(escape_toml_string "${REGISTRY_CLEANUP_INTERVAL}")"
  replace_placeholder "${output_path}" "__REGISTRY_CLEANUP_DELETE_LIMIT__" "${REGISTRY_CLEANUP_DELETE_LIMIT}"
  replace_placeholder "${output_path}" "__REPLICATION_TARGETS_FILE__" "$(escape_toml_string "${REPLICATION_TARGETS_FILE}")"
}

write_runner() {
  local path="$1"
  local binary_path="$2"
  local config_path="$3"

  {
    echo '#!/usr/bin/env bash'
    echo 'set -euo pipefail'
    printf 'exec %q --config=%q\n' "${binary_path}" "${config_path}"
  } >"${path}"
  chmod 0755 "${path}"
}

write_unit() {
  local unit_path="$1"
  local runner_path="$2"

  cat >"${unit_path}" <<EOF
[Unit]
Description=StarMap node ${NODE_ID}
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${RUN_USER}
Group=${RUN_GROUP}
WorkingDirectory=${DATA_DIR}
ExecStart=${runner_path}
Restart=always
RestartSec=3
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF
}

NODE_ID=""
CLUSTER_ID=""
REGION="default"
REPO_DIR=""
DATA_DIR=""
HTTP_ADDR=":8080"
ADMIN_ADDR="127.0.0.1:18080"
GRPC_ADDR=":19090"
ADMIN_TOKEN="starmap"
REPLICATION_TOKEN=""
PROMETHEUS_SD_TOKEN=""
REPLICATION_TARGETS_FILE=""
PEER_IDS=""
PEER_HTTP_ADDRS=""
PEER_ADMIN_ADDRS=""
PEER_GRPC_ADDRS=""
REQUEST_TIMEOUT="5s"
SHUTDOWN_TIMEOUT="10s"
REGISTRY_CLEANUP_INTERVAL="1s"
REGISTRY_CLEANUP_DELETE_LIMIT="128"
SERVICE_NAME=""
BIN_DIR="/opt/starmap/bin"
CONFIG_DIR="/etc/starmapd"
CONFIG_FILE_NAME=""
RUN_USER="starmap"
RUN_GROUP="starmap"
SKIP_START="false"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo-dir)
      REPO_DIR="$2"
      shift 2
      ;;
    --node-id)
      NODE_ID="$2"
      shift 2
      ;;
    --cluster-id)
      CLUSTER_ID="$2"
      shift 2
      ;;
    --region)
      REGION="$2"
      shift 2
      ;;
    --data-dir)
      DATA_DIR="$2"
      shift 2
      ;;
    --http-addr)
      HTTP_ADDR="$2"
      shift 2
      ;;
    --admin-addr)
      ADMIN_ADDR="$2"
      shift 2
      ;;
    --grpc-addr)
      GRPC_ADDR="$2"
      shift 2
      ;;
    --admin-token)
      ADMIN_TOKEN="$2"
      shift 2
      ;;
    --replication-token)
      REPLICATION_TOKEN="$2"
      shift 2
      ;;
    --prometheus-sd-token)
      PROMETHEUS_SD_TOKEN="$2"
      shift 2
      ;;
    --replication-targets-file)
      REPLICATION_TARGETS_FILE="$2"
      shift 2
      ;;
    --peer-ids)
      PEER_IDS="$2"
      shift 2
      ;;
    --peer-http-addrs)
      PEER_HTTP_ADDRS="$2"
      shift 2
      ;;
    --peer-admin-addrs)
      PEER_ADMIN_ADDRS="$2"
      shift 2
      ;;
    --peer-grpc-addrs)
      PEER_GRPC_ADDRS="$2"
      shift 2
      ;;
    --request-timeout)
      REQUEST_TIMEOUT="$2"
      shift 2
      ;;
    --shutdown-timeout)
      SHUTDOWN_TIMEOUT="$2"
      shift 2
      ;;
    --registry-cleanup-interval)
      REGISTRY_CLEANUP_INTERVAL="$2"
      shift 2
      ;;
    --registry-cleanup-delete-limit)
      REGISTRY_CLEANUP_DELETE_LIMIT="$2"
      shift 2
      ;;
    --service-name)
      SERVICE_NAME="$2"
      shift 2
      ;;
    --bin-dir)
      BIN_DIR="$2"
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

require_cmd go
require_cmd systemctl
require_cmd install
require_cmd sed

[[ -n "${REPO_DIR}" ]] || die "--repo-dir is required"
[[ -n "${NODE_ID}" ]] || die "--node-id is required"
[[ -n "${CLUSTER_ID}" ]] || die "--cluster-id is required"

REPO_DIR="$(cd "${REPO_DIR}" && pwd)"
[[ -f "${REPO_DIR}/go.mod" ]] || die "invalid repo dir: ${REPO_DIR}"
TEMPLATE_PATH="${REPO_DIR}/config/starmapd.toml"

if [[ -z "${SERVICE_NAME}" ]]; then
  SERVICE_NAME="starmapd-node-${NODE_ID}"
fi
if [[ -z "${CONFIG_FILE_NAME}" ]]; then
  CONFIG_FILE_NAME="${SERVICE_NAME}.toml"
fi
if [[ -z "${DATA_DIR}" ]]; then
  DATA_DIR="/var/lib/starmap/node-${NODE_ID}"
fi
if [[ -z "${PEER_IDS}" ]]; then
  PEER_IDS="${NODE_ID}"
fi
if [[ -z "${REPLICATION_TOKEN}" ]]; then
  REPLICATION_TOKEN="${ADMIN_TOKEN}"
fi
if [[ -z "${PROMETHEUS_SD_TOKEN}" ]]; then
  PROMETHEUS_SD_TOKEN="${REPLICATION_TOKEN}"
fi

IFS=',' read -r -a peer_array <<<"${PEER_IDS}"
if (( ${#peer_array[@]} > 1 )); then
  [[ -n "${PEER_HTTP_ADDRS}" ]] || die "--peer-http-addrs is required when peer count > 1"
  [[ -n "${PEER_ADMIN_ADDRS}" ]] || die "--peer-admin-addrs is required when peer count > 1"
  [[ -n "${PEER_GRPC_ADDRS}" ]] || die "--peer-grpc-addrs is required when peer count > 1"
fi

ensure_service_user "${RUN_USER}" "${RUN_GROUP}"

install -d -m 0755 "${BIN_DIR}" "${CONFIG_DIR}" "${DATA_DIR}"
chown -R "${RUN_USER}:${RUN_GROUP}" "${DATA_DIR}"

echo "==> building starmapd binary"
(
  cd "${REPO_DIR}"
  go build -o "${BIN_DIR}/starmapd" ./cmd/starmapd
)
chmod 0755 "${BIN_DIR}/starmapd"

if [[ -n "${REPLICATION_TARGETS_FILE}" ]]; then
  [[ -f "${REPLICATION_TARGETS_FILE}" ]] || die "replication targets file not found: ${REPLICATION_TARGETS_FILE}"
  copied_targets_file="${CONFIG_DIR}/${SERVICE_NAME}-replication-targets.json"
  install -m 0640 -o "${RUN_USER}" -g "${RUN_GROUP}" "${REPLICATION_TARGETS_FILE}" "${copied_targets_file}"
  REPLICATION_TARGETS_FILE="${copied_targets_file}"
fi

CONFIG_PATH="${CONFIG_DIR}/${CONFIG_FILE_NAME}"
render_config_from_template "${TEMPLATE_PATH}" "${CONFIG_PATH}"

RUNNER_PATH="${BIN_DIR}/${SERVICE_NAME}-run.sh"
UNIT_PATH="/etc/systemd/system/${SERVICE_NAME}.service"

write_runner "${RUNNER_PATH}" "${BIN_DIR}/starmapd" "${CONFIG_PATH}"
write_unit "${UNIT_PATH}" "${RUNNER_PATH}"

systemctl daemon-reload
systemctl enable "${SERVICE_NAME}" >/dev/null
if [[ "${SKIP_START}" == "true" ]]; then
  echo "==> installation completed, service not started"
else
  systemctl restart "${SERVICE_NAME}"
  echo "==> service started: ${SERVICE_NAME}"
fi

echo
echo "Service name : ${SERVICE_NAME}"
echo "Config file  : ${CONFIG_PATH}"
echo "Data dir     : ${DATA_DIR}"
echo "HTTP addr    : ${HTTP_ADDR}"
echo "Admin addr   : ${ADMIN_ADDR}"
echo "gRPC addr    : ${GRPC_ADDR}"
echo
echo "Check status : sudo systemctl status ${SERVICE_NAME}"
echo "View logs    : sudo journalctl -u ${SERVICE_NAME} -f"
