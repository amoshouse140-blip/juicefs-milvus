#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
runtime_root="${repo_root}/.runtime"
meta_dir="${runtime_root}/meta"
data_dir="${runtime_root}/juicefs-data"
vb_dir="${runtime_root}/vectorbucket"
log_dir="${runtime_root}/logs"
bin_dir="${runtime_root}/bin"

juicefs_dir="${repo_root}/juicefs-1.3.0-rc1"
juicefs_bin="${juicefs_dir}/juicefs"
meta_url="sqlite3://${meta_dir}/jfs.db"
volume_name="jfs-milvus"
gateway_addr="${GATEWAY_ADDR:-127.0.0.1:9000}"
bridge_url="${BRIDGE_URL:-http://127.0.0.1:19531}"
bridge_addr="${BRIDGE_ADDR:-:19531}"
milvus_addr="${MILVUS_ADDR:-127.0.0.1:19530}"
access_key="${MINIO_ROOT_USER:-admin}"
secret_key="${MINIO_ROOT_PASSWORD:-12345678}"
mc_bin="${bin_dir}/mc"

mkdir -p "${meta_dir}" "${data_dir}" "${vb_dir}" "${log_dir}" "${bin_dir}"

wait_http_ok() {
  local url="$1"
  local retries="${2:-60}"
  for _ in $(seq 1 "${retries}"); do
    if curl -fsS "${url}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 2
  done
  return 1
}

wait_tcp_ok() {
  local host="$1"
  local port="$2"
  local retries="${3:-60}"
  for _ in $(seq 1 "${retries}"); do
    if nc -z "${host}" "${port}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 2
  done
  return 1
}

ensure_mc() {
  if [ -x "${mc_bin}" ]; then
    return 0
  fi
  local os arch url
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"
  case "${os}/${arch}" in
    darwin/arm64) url="https://dl.min.io/client/mc/release/darwin-arm64/mc" ;;
    darwin/x86_64) url="https://dl.min.io/client/mc/release/darwin-amd64/mc" ;;
    linux/x86_64) url="https://dl.min.io/client/mc/release/linux-amd64/mc" ;;
    linux/aarch64|linux/arm64) url="https://dl.min.io/client/mc/release/linux-arm64/mc" ;;
    *)
      echo "unsupported platform for mc: ${os}/${arch}" >&2
      exit 1
      ;;
  esac
  curl -fsSL "${url}" -o "${mc_bin}"
  chmod +x "${mc_bin}"
}

ensure_juicefs_bin() {
  if [ ! -x "${juicefs_bin}" ]; then
    (cd "${juicefs_dir}" && GOWORK=off go build -o ./juicefs .)
  fi
}

ensure_volume_formatted() {
  if [ -f "${meta_dir}/jfs.db" ]; then
    return 0
  fi
  (
    cd "${juicefs_dir}"
    ./juicefs format "${meta_url}" "${volume_name}" --storage file --bucket "${data_dir}"
  )
}

start_gateway() {
  if wait_tcp_ok 127.0.0.1 9000 1; then
    return 0
  fi
  (
    cd "${juicefs_dir}"
    nohup env \
      MINIO_ROOT_USER="${access_key}" \
      MINIO_ROOT_PASSWORD="${secret_key}" \
      VB_SQLITE_PATH="${vb_dir}/metadata.db" \
      VB_MILVUS_BRIDGE_ADDR="${bridge_url}" \
      ./juicefs gateway "${meta_url}" "${gateway_addr}" \
      >"${log_dir}/juicefs-gateway.log" 2>&1 &
  )
  wait_tcp_ok 127.0.0.1 9000 60 || {
    echo "juicefs gateway failed to start, see ${log_dir}/juicefs-gateway.log" >&2
    exit 1
  }
}

prepare_bucket_view() {
  ensure_mc
  "${mc_bin}" alias set jfs "http://${gateway_addr}" "${access_key}" "${secret_key}" >/dev/null
  "${mc_bin}" ls jfs >/dev/null
  echo "JuiceFS gateway exposes fixed bucket: ${volume_name}"
}

start_milvus() {
  (
    cd "${repo_root}/milvus/deploy/standalone"
    docker compose up -d
  )
  wait_tcp_ok 127.0.0.1 19530 90 || {
    docker logs --tail=150 milvus-standalone >&2 || true
    exit 1
  }
  wait_http_ok "http://127.0.0.1:9091/healthz" 90 || {
    docker logs --tail=150 milvus-standalone >&2 || true
    exit 1
  }
}

start_bridge() {
  if wait_http_ok "${bridge_url}/healthz" 1; then
    return 0
  fi
  (
    cd "${repo_root}/milvus/bridge"
    nohup env \
      MILVUS_ADDR="${milvus_addr}" \
      BRIDGE_LISTEN_ADDR="${bridge_addr}" \
      GOWORK=off go run ./cmd/vectorbucket-milvus-bridge \
      >"${log_dir}/vectorbucket-bridge.log" 2>&1 &
  )
  wait_http_ok "${bridge_url}/healthz" 60 || {
    echo "milvus bridge failed to start, see ${log_dir}/vectorbucket-bridge.log" >&2
    exit 1
  }
}

run_smoke_test() {
  (
    cd "${repo_root}"
    ./milvus/scripts/test-vectorbucket.sh "http://${gateway_addr}"
  )
}

show_summary() {
  echo
  echo "Ready:"
  echo "  gateway: http://${gateway_addr}"
  echo "  bridge:  ${bridge_url}"
  echo "  milvus:  ${milvus_addr}"
  echo "  bucket:  ${volume_name}/milvus-storage/files"
  echo
  echo "Logs:"
  echo "  ${log_dir}/juicefs-gateway.log"
  echo "  ${log_dir}/vectorbucket-bridge.log"
}

main() {
  ensure_juicefs_bin
  ensure_volume_formatted
  start_gateway
  prepare_bucket_view
  start_milvus
  start_bridge
  run_smoke_test
  show_summary
}

main "$@"
