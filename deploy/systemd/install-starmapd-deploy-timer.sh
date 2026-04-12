#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  sudo bash deploy/systemd/install-starmapd-deploy-timer.sh \
    --channel-url https://example.cos.ap-guangzhou.myqcloud.com/starmapd/channels/prod/current.json \
    --service-name starmapd-node-1 \
    --health-url http://127.0.0.1:8080/readyz \
    --workdir /opt/src/StarMap \
    --enable-now

Options:
  --channel-url URL      current.json 地址
  --service-name NAME    StarMap systemd 服务名，例如 starmapd-node-1
  --health-url URL       健康检查地址
  --workdir PATH         StarMap 仓库目录
  --unit-name NAME       deploy timer 的 unit 前缀，默认 starmapd-deploy
  --output-dir PATH      systemd unit 输出目录，默认 /etc/systemd/system
  --enable-now           安装完成后立即 enable --now timer
  -h, --help             查看帮助
EOF
}

die() {
  echo "ERROR: $*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

escape_sed_replacement() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//&/\\&}"
  value="${value//|/\\|}"
  printf '%s' "$value"
}

replace_placeholder() {
  local file="$1"
  local placeholder="$2"
  local value="$3"

  sed -i "s|${placeholder}|$(escape_sed_replacement "${value}")|g" "${file}"
}

CHANNEL_URL=""
SERVICE_NAME=""
HEALTH_URL=""
WORKDIR=""
UNIT_NAME="starmapd-deploy"
OUTPUT_DIR="/etc/systemd/system"
ENABLE_NOW="false"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --channel-url)
      CHANNEL_URL="$2"
      shift 2
      ;;
    --service-name)
      SERVICE_NAME="$2"
      shift 2
      ;;
    --health-url)
      HEALTH_URL="$2"
      shift 2
      ;;
    --workdir)
      WORKDIR="$2"
      shift 2
      ;;
    --unit-name)
      UNIT_NAME="$2"
      shift 2
      ;;
    --output-dir)
      OUTPUT_DIR="$2"
      shift 2
      ;;
    --enable-now)
      ENABLE_NOW="true"
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

require_cmd install
require_cmd sed
require_cmd systemctl

[[ -n "${CHANNEL_URL}" ]] || die "--channel-url is required"
[[ -n "${SERVICE_NAME}" ]] || die "--service-name is required"
[[ -n "${HEALTH_URL}" ]] || die "--health-url is required"
[[ -n "${WORKDIR}" ]] || die "--workdir is required"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVICE_TEMPLATE="${SCRIPT_DIR}/starmapd-deploy.service"
TIMER_TEMPLATE="${SCRIPT_DIR}/starmapd-deploy.timer"

[[ -f "${SERVICE_TEMPLATE}" ]] || die "service template not found: ${SERVICE_TEMPLATE}"
[[ -f "${TIMER_TEMPLATE}" ]] || die "timer template not found: ${TIMER_TEMPLATE}"

install -d -m 0755 "${OUTPUT_DIR}"

SERVICE_OUTPUT="${OUTPUT_DIR}/${UNIT_NAME}.service"
TIMER_OUTPUT="${OUTPUT_DIR}/${UNIT_NAME}.timer"

install -m 0644 "${SERVICE_TEMPLATE}" "${SERVICE_OUTPUT}"
install -m 0644 "${TIMER_TEMPLATE}" "${TIMER_OUTPUT}"

replace_placeholder "${SERVICE_OUTPUT}" "__CHANNEL_URL__" "${CHANNEL_URL}"
replace_placeholder "${SERVICE_OUTPUT}" "__SERVICE_NAME__" "${SERVICE_NAME}"
replace_placeholder "${SERVICE_OUTPUT}" "__HEALTH_URL__" "${HEALTH_URL}"
replace_placeholder "${SERVICE_OUTPUT}" "__WORKDIR__" "${WORKDIR}"

replace_placeholder "${TIMER_OUTPUT}" "starmapd-deploy.service" "${UNIT_NAME}.service"
replace_placeholder "${TIMER_OUTPUT}" "starmapd-deploy.timer" "${UNIT_NAME}.timer"

replace_placeholder "${SERVICE_OUTPUT}" "starmapd-deploy.service" "${UNIT_NAME}.service"
replace_placeholder "${SERVICE_OUTPUT}" "starmapd-deploy.timer" "${UNIT_NAME}.timer"

systemctl daemon-reload

if [[ "${ENABLE_NOW}" == "true" ]]; then
  systemctl enable --now "${UNIT_NAME}.timer"
fi

echo "Service unit : ${SERVICE_OUTPUT}"
echo "Timer unit   : ${TIMER_OUTPUT}"
if [[ "${ENABLE_NOW}" == "true" ]]; then
  echo "Status       : enabled and started"
else
  echo "Next step    : sudo systemctl enable --now ${UNIT_NAME}.timer"
fi
