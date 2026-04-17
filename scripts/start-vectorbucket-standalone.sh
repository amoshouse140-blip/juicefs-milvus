#!/usr/bin/env bash
# 用途:
#   启动本地 VectorBucket 所需的 JuiceFS + Milvus Standalone 环境。
#   这是唯一保留的启动入口。脚本会先停止旧环境，再重新构建带
#   `milvus_integration` 的 JuiceFS 二进制，准备运行目录，启动
#   JuiceFS gateway 和 Milvus standalone，最后打印当前配置和在线状态。
#
# 前置条件:
#   - 已安装 Docker / docker compose
#   - 已安装 Go 工具链
#   - 已准备好 `configs/vectorbucket.json`
#
# 用法:
#   ./scripts/start-vectorbucket-standalone.sh
#
# 示例:
#   ./scripts/start-vectorbucket-standalone.sh
#   ./scripts/start-vectorbucket-standalone.sh --fresh
#
# 说明:
#   - 这个脚本只负责部署和健康检查，不做功能验证。
#   - `--fresh` 会删除 `.runtime/` 和 Milvus 本地 volume，彻底清空旧数据后再重建。
#   - 功能验证请单独运行：
#     `python3 scripts/test-vectorbucket-boto3.py`
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
runtime_root="${repo_root}/.runtime"
meta_dir="${runtime_root}/meta"
data_dir="${runtime_root}/juicefs-data"
vb_dir="${runtime_root}/vectorbucket"
log_dir="${runtime_root}/logs"
bin_dir="${runtime_root}/bin"
milvus_deploy_dir="${repo_root}/milvus/deploy/standalone"
milvus_volumes_dir="${milvus_deploy_dir}/volumes"

juicefs_dir="${repo_root}/juicefs-1.3.0-rc1"
juicefs_bin="${bin_dir}/juicefs-milvus-integration"
meta_url="sqlite3://${meta_dir}/jfs.db"
volume_name="jfs-milvus"
gateway_addr="${GATEWAY_ADDR:-127.0.0.1:9000}"
milvus_addr="${MILVUS_ADDR:-127.0.0.1:19530}"
access_key="${MINIO_ROOT_USER:-admin}"
secret_key="${MINIO_ROOT_PASSWORD:-12345678}"
mc_bin="${bin_dir}/mc"
fresh_mode=0

parse_args() {
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --fresh)
        fresh_mode=1
        ;;
      -h|--help)
        cat <<'EOF'
用法:
  ./scripts/start-vectorbucket-standalone.sh [--fresh]

选项:
  --fresh    删除 .runtime/ 和 Milvus 本地 volumes，彻底清空旧数据后重新部署
EOF
        exit 0
        ;;
      *)
        echo "未知参数: $1" >&2
        exit 1
        ;;
    esac
    shift
  done
}

log() {
  local level="$1"
  local message="$2"
  echo "[${level}] ${message}"
}

run_step() {
  local step_name="$1"
  shift
  local started elapsed
  started="$(date +%s)"
  log "开始" "${step_name}"
  if "$@"; then
    elapsed="$(( $(date +%s) - started ))"
    log "成功" "${step_name}，耗时 ${elapsed}s"
    return 0
  fi
  elapsed="$(( $(date +%s) - started ))"
  log "失败" "${step_name}，耗时 ${elapsed}s"
  return 1
}

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

kill_matching() {
  local pattern="$1"
  local pids
  pids="$(pgrep -f "${pattern}" || true)"
  if [ -z "${pids}" ]; then
    return 0
  fi
  echo "${pids}" | xargs kill
}

stop_existing() {
  log "信息" "Step 1/4: 停止旧的 gateway、bridge 和 Milvus 容器"
  kill_matching "${repo_root}/.runtime/bin/juicefs-milvus-integration gateway"
  kill_matching "${repo_root}/juicefs-1.3.0-rc1/juicefs gateway"
  kill_matching "vectorbucket-milvus-bridge"
  (
    cd "${milvus_deploy_dir}"
    docker compose down --remove-orphans
  )
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
  log "信息" "Step 2/4: 构建 JuiceFS milvus_integration 二进制"
  rm -f "${juicefs_bin}"
  (
    cd "${juicefs_dir}"
    GOWORK=off go build -tags milvus_integration -o "${juicefs_bin}" .
  )
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
    log "信息" "JuiceFS gateway 已在运行，跳过重复启动"
    return 0
  fi
  : >"${log_dir}/juicefs-gateway.log"
  GATEWAY_LOG="${log_dir}/juicefs-gateway.log" \
  JUICEFS_DIR="${juicefs_dir}" \
  JUICEFS_BIN="${juicefs_bin}" \
  META_URL="${meta_url}" \
  GATEWAY_ADDR="${gateway_addr}" \
  MINIO_ROOT_USER="${access_key}" \
  MINIO_ROOT_PASSWORD="${secret_key}" \
  VB_SQLITE_PATH="${vb_dir}/metadata.db" \
  VB_MILVUS_ADDR="${milvus_addr}" \
    python3 - <<'PY'
import os
import subprocess

with open(os.environ["GATEWAY_LOG"], "ab", buffering=0) as log:
    subprocess.Popen(
        [os.environ["JUICEFS_BIN"], "gateway", os.environ["META_URL"], os.environ["GATEWAY_ADDR"]],
        cwd=os.environ["JUICEFS_DIR"],
        env=os.environ.copy(),
        stdin=subprocess.DEVNULL,
        stdout=log,
        stderr=subprocess.STDOUT,
        start_new_session=True,
        close_fds=True,
    )
PY
  wait_tcp_ok 127.0.0.1 9000 60 || {
    log "失败" "JuiceFS gateway 启动失败，请检查 ${log_dir}/juicefs-gateway.log"
    exit 1
  }
}

prepare_bucket_view() {
  ensure_mc
  "${mc_bin}" alias set jfs "http://${gateway_addr}" "${access_key}" "${secret_key}" >/dev/null
  "${mc_bin}" ls jfs >/dev/null
  log "信息" "JuiceFS gateway 当前暴露固定 bucket: ${volume_name}"
}

cleanup_runtime_state() {
  if [ "${fresh_mode}" != "1" ]; then
    return 0
  fi
  log "信息" "已启用 --fresh，将删除 .runtime/ 下所有历史数据"
  rm -rf "${runtime_root}"
  mkdir -p "${meta_dir}" "${data_dir}" "${vb_dir}" "${log_dir}" "${bin_dir}"
}

cleanup_milvus_state() {
  if [ "${fresh_mode}" != "1" ]; then
    return 0
  fi
  log "信息" "已启用 --fresh，将删除 Milvus 本地 volumes"
  rm -rf "${milvus_volumes_dir}/etcd" "${milvus_volumes_dir}/milvus"
  mkdir -p "${milvus_volumes_dir}/etcd" "${milvus_volumes_dir}/milvus"
}

start_milvus() {
  (
    cd "${milvus_deploy_dir}"
    docker compose up -d
  )
  wait_tcp_ok 127.0.0.1 19530 90 || {
    log "失败" "Milvus gRPC 端口 19530 未就绪，输出最近日志"
    docker logs --tail=150 milvus-standalone >&2 || true
    exit 1
  }
  wait_http_ok "http://127.0.0.1:9091/healthz" 90 || {
    log "失败" "Milvus healthz 未就绪，输出最近日志"
    docker logs --tail=150 milvus-standalone >&2 || true
    exit 1
  }
}

show_summary() {
  echo
  log "信息" "Step 4/4: 校验最终进程和服务状态"
  pgrep -af 'juicefs-milvus-integration gateway|vectorbucket-milvus-bridge|juicefs gateway' || true
  docker ps --format 'table {{.Names}}\t{{.Status}}\t{{.Ports}}' | rg 'milvus-(standalone|etcd)' || true
  echo
  log "成功" "部署完成，可用服务如下"
  echo "  gateway: http://${gateway_addr}"
  echo "  milvus:  ${milvus_addr}"
  echo "  bucket:  ${volume_name}/milvus-storage/files"
  echo "  config:  ${repo_root}/configs/vectorbucket.json"
  echo
  cat "${repo_root}/configs/vectorbucket.json"
  echo
  log "信息" "日志文件位置"
  echo "  ${log_dir}/juicefs-gateway.log"
}

main() {
  parse_args "$@"
  mkdir -p "${meta_dir}" "${data_dir}" "${vb_dir}" "${log_dir}" "${bin_dir}"
  run_step "停止旧环境" stop_existing
  run_step "清理 VectorBucket 运行态" cleanup_runtime_state
  run_step "清理 Milvus 本地状态" cleanup_milvus_state
  run_step "构建 JuiceFS 二进制" ensure_juicefs_bin
  run_step "初始化 JuiceFS volume" ensure_volume_formatted
  log "信息" "Step 3/4: 启动 JuiceFS gateway 和 Milvus standalone"
  run_step "启动 JuiceFS gateway" start_gateway
  run_step "检查并准备 JuiceFS bucket 视图" prepare_bucket_view
  run_step "启动 Milvus standalone" start_milvus
  show_summary
}

main "$@"
