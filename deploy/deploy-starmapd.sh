#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  sudo bash deploy/deploy-starmapd.sh \
    --channel-url https://example.cos.ap-guangzhou.myqcloud.com/starmapd/channels/prod/current.json \
    --service-name starmapd-node-1 \
    --health-url http://127.0.0.1:8080/readyz

Or deploy a specific manifest directly:
  sudo bash deploy/deploy-starmapd.sh \
    --manifest-url https://example.cos.ap-guangzhou.myqcloud.com/starmapd/releases/v1.0.0/manifest.json \
    --service-name starmapd-node-1 \
    --health-url http://127.0.0.1:8080/readyz

Options:
  --channel-url URL           指向 channel current.json 的地址
  --manifest-url URL          指向具体 manifest.json 的地址
  --service-name NAME         systemd 服务名，例如 starmapd-node-1
  --install-root PATH         发布根目录，默认 /opt/starmap
  --binary-path PATH          当前 systemd runner 使用的固定二进制路径，默认 /opt/starmap/bin/starmapd
  --download-root PATH        下载缓存目录，默认 /var/tmp/starmap-deploy
  --health-url URL            健康检查地址，默认 http://127.0.0.1:8080/readyz
  --health-timeout SEC        健康检查总超时秒数，默认 60
  --health-interval SEC       健康检查重试间隔秒数，默认 2
  --curl-connect-timeout SEC  curl 建连超时秒数，默认 5
  --curl-max-time SEC         curl 单次请求最大时长秒数，默认 30
  --rollback-only             只执行回滚，不做下载和发布
  -h, --help                  查看帮助

Compatibility:
  1. 兼容当前 deploy/install-starmapd.sh 生成的 systemd 布局
  2. 如果现有节点仍然直接运行 /opt/starmap/bin/starmapd，本脚本会在首次发布时自动迁移到 current/previous 软链接布局
  3. 配置文件和数据目录位于共享目录，不跟随版本目录切换
  4. 发布包中至少包含 bin/starmapd
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

fetch_file() {
  local url="$1"
  local output="$2"

  curl \
    --fail \
    --location \
    --silent \
    --show-error \
    --connect-timeout "${CURL_CONNECT_TIMEOUT}" \
    --max-time "${CURL_MAX_TIME}" \
    -o "${output}" \
    "${url}"
}

json_get() {
  local file="$1"
  local key="$2"

  python3 - "$file" "$key" <<'PY'
import json
import sys

path = sys.argv[1]
key = sys.argv[2]
with open(path, "r", encoding="utf-8") as fh:
    data = json.load(fh)

value = data
for part in key.split("."):
    value = value[part]

if isinstance(value, (dict, list)):
    print(json.dumps(value, ensure_ascii=False))
else:
    print(value)
PY
}

ensure_release_layout() {
  install -d -m 0755 "${INSTALL_ROOT}" "${INSTALL_ROOT}/releases" "${INSTALL_ROOT}/bin" "${DOWNLOAD_ROOT}"
}

verify_sha256() {
  local file="$1"
  local expected="$2"
  local actual

  actual="$(sha256sum "${file}" | awk '{print $1}')"
  [[ "${actual}" == "${expected}" ]] || die "sha256 mismatch: expected=${expected} actual=${actual}"
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

current_target() {
  local link_path="$1"

  if [[ -L "${link_path}" ]]; then
    readlink -f "${link_path}"
    return
  fi
  echo ""
}

ensure_binary_link() {
  local binary_dir
  local temp_link

  binary_dir="$(dirname "${BINARY_PATH}")"
  install -d -m 0755 "${binary_dir}"
  temp_link="$(mktemp -u "${BINARY_PATH}.tmp.XXXXXX")"
  ln -sfn "${CURRENT_LINK}/bin/starmapd" "${temp_link}"
  mv -Tf "${temp_link}" "${BINARY_PATH}"
}

switch_link() {
  local target="$1"
  local link_path="$2"
  local temp_link

  temp_link="$(mktemp -u "${link_path}.tmp.XXXXXX")"
  ln -sfn "${target}" "${temp_link}"
  mv -Tf "${temp_link}" "${link_path}"
}

bootstrap_existing_installation() {
  local bootstrap_version
  local bootstrap_dir
  local current_binary_target
  local desired_binary_target

  if [[ -L "${BINARY_PATH}" ]]; then
    current_binary_target="$(readlink -f "${BINARY_PATH}")"
    if [[ -L "${CURRENT_LINK}" ]]; then
      desired_binary_target="$(readlink -f "${CURRENT_LINK}/bin/starmapd")"
    else
      desired_binary_target=""
    fi
    if [[ -n "${desired_binary_target}" && "${current_binary_target}" == "${desired_binary_target}" ]]; then
      return
    fi
  fi

  if [[ -L "${CURRENT_LINK}" ]]; then
    ensure_binary_link
    return
  fi

  if [[ -x "${BINARY_PATH}" && ! -L "${BINARY_PATH}" ]]; then
    bootstrap_version="bootstrap-local-$(date +%Y%m%d%H%M%S)"
    bootstrap_dir="${INSTALL_ROOT}/releases/${bootstrap_version}"

    log_step "bootstrapping current installation into ${bootstrap_dir}"
    install -d -m 0755 "${bootstrap_dir}/bin"
    cp -f "${BINARY_PATH}" "${bootstrap_dir}/bin/starmapd"
    chmod 0755 "${bootstrap_dir}/bin/starmapd"
    switch_link "${bootstrap_dir}" "${CURRENT_LINK}"
    ensure_binary_link
    return
  fi

  if [[ ! -e "${BINARY_PATH}" && ! -L "${CURRENT_LINK}" ]]; then
    return
  fi

  ensure_binary_link
}

rollback() {
  local previous_target

  previous_target="$(current_target "${PREVIOUS_LINK}")"
  [[ -n "${previous_target}" ]] || die "no previous release to rollback"

  log_step "rolling back to ${previous_target}"
  switch_link "${previous_target}" "${CURRENT_LINK}"
  ensure_binary_link
  systemctl restart "${SERVICE_NAME}"
  if ! wait_for_health; then
    die "rollback failed, service is still unhealthy"
  fi
  log_step "rollback completed"
}

deploy_release() {
  local manifest_url="$1"
  local tmp_dir manifest_file version artifact_name artifact_url expected_sha release_dir stage_dir archive_path previous_target extracted_root

  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "${tmp_dir}"' RETURN

  manifest_file="${tmp_dir}/manifest.json"
  log_step "downloading manifest: ${manifest_url}"
  fetch_file "${manifest_url}" "${manifest_file}"

  version="$(json_get "${manifest_file}" "version")"
  artifact_name="$(json_get "${manifest_file}" "artifact_name")"
  artifact_url="$(json_get "${manifest_file}" "artifact_url")"
  expected_sha="$(json_get "${manifest_file}" "sha256")"

  [[ -n "${version}" ]] || die "manifest version is empty"
  [[ -n "${artifact_name}" ]] || die "manifest artifact_name is empty"
  [[ -n "${artifact_url}" ]] || die "manifest artifact_url is empty"
  [[ -n "${expected_sha}" ]] || die "manifest sha256 is empty"

  release_dir="${INSTALL_ROOT}/releases/${version}"
  if [[ -x "${release_dir}/bin/starmapd" ]]; then
    log_step "release already extracted: ${version}"
  else
    archive_path="${DOWNLOAD_ROOT}/${artifact_name}"
    log_step "downloading artifact: ${artifact_url}"
    fetch_file "${artifact_url}" "${archive_path}"
    verify_sha256 "${archive_path}" "${expected_sha}"

    stage_dir="${INSTALL_ROOT}/releases/.staging-${version}-$$"
    rm -rf "${stage_dir}"
    install -d -m 0755 "${stage_dir}"
    tar -xzf "${archive_path}" -C "${stage_dir}"

    extracted_root=""
    if [[ -x "${stage_dir}/starmapd-linux-amd64/bin/starmapd" ]]; then
      extracted_root="${stage_dir}/starmapd-linux-amd64"
    elif [[ -x "${stage_dir}/bin/starmapd" ]]; then
      extracted_root="${stage_dir}"
    fi

    if [[ -z "${extracted_root}" ]]; then
      die "artifact layout invalid: expected bin/starmapd"
    fi

    if [[ "${extracted_root}" != "${stage_dir}" ]]; then
      mv "${extracted_root}" "${release_dir}"
      rmdir "${stage_dir}"
    else
      mv "${stage_dir}" "${release_dir}"
    fi
  fi

  previous_target="$(current_target "${CURRENT_LINK}")"
  if [[ -n "${previous_target}" ]]; then
    switch_link "${previous_target}" "${PREVIOUS_LINK}"
  fi

  log_step "switching current release to ${release_dir}"
  switch_link "${release_dir}" "${CURRENT_LINK}"
  ensure_binary_link

  log_step "restarting service ${SERVICE_NAME}"
  systemctl restart "${SERVICE_NAME}"

  if ! wait_for_health; then
    log_step "health check failed, starting rollback"
    if [[ -n "${previous_target}" ]]; then
      rollback
    fi
    die "deploy failed after health check"
  fi

  log_step "deploy succeeded, current version=${version}"
}

CHANNEL_URL=""
MANIFEST_URL=""
SERVICE_NAME=""
INSTALL_ROOT="/opt/starmap"
BINARY_PATH="/opt/starmap/bin/starmapd"
DOWNLOAD_ROOT="/var/tmp/starmap-deploy"
HEALTH_URL="http://127.0.0.1:8080/readyz"
HEALTH_TIMEOUT=60
HEALTH_INTERVAL=2
CURL_CONNECT_TIMEOUT=5
CURL_MAX_TIME=30
ROLLBACK_ONLY="false"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --channel-url)
      CHANNEL_URL="$2"
      shift 2
      ;;
    --manifest-url)
      MANIFEST_URL="$2"
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
    --binary-path)
      BINARY_PATH="$2"
      shift 2
      ;;
    --download-root)
      DOWNLOAD_ROOT="$2"
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
    --rollback-only)
      ROLLBACK_ONLY="true"
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

require_cmd curl
require_cmd tar
require_cmd sha256sum
require_cmd systemctl
require_cmd python3
require_cmd ln
require_cmd mv
require_cmd readlink

[[ -n "${SERVICE_NAME}" ]] || die "--service-name is required"
CURRENT_LINK="${INSTALL_ROOT}/current"
PREVIOUS_LINK="${INSTALL_ROOT}/previous"
ensure_release_layout
bootstrap_existing_installation

if [[ "${ROLLBACK_ONLY}" == "true" ]]; then
  rollback
  exit 0
fi

if [[ -n "${CHANNEL_URL}" && -n "${MANIFEST_URL}" ]]; then
  die "only one of --channel-url or --manifest-url can be specified"
fi
if [[ -z "${CHANNEL_URL}" && -z "${MANIFEST_URL}" ]]; then
  die "one of --channel-url or --manifest-url is required"
fi

if [[ -n "${CHANNEL_URL}" ]]; then
  tmp_channel="$(mktemp)"
  trap 'rm -f "${tmp_channel}"' EXIT
  log_step "downloading channel pointer: ${CHANNEL_URL}"
  fetch_file "${CHANNEL_URL}" "${tmp_channel}"
  MANIFEST_URL="$(json_get "${tmp_channel}" "manifest_url")"
  [[ -n "${MANIFEST_URL}" ]] || die "channel manifest_url is empty"
fi

deploy_release "${MANIFEST_URL}"
