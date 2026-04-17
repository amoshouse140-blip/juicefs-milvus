#!/usr/bin/env bash
# 用途:
#   停止由 `scripts/start-vectorbucket-standalone.sh` 启动的本地
#   JuiceFS gateway 和 Milvus standalone 环境。
#
# 前置条件:
#   - 之前已经启动过本地部署环境
#
# 用法:
#   ./scripts/stop-vectorbucket-standalone.sh
#
# 示例:
#   ./scripts/stop-vectorbucket-standalone.sh
#
# 说明:
#   - 不会删除 `.runtime/` 里的数据和日志。
#   - 也会顺带停止遗留的 `vectorbucket-milvus-bridge` 进程。
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
runtime_root="${repo_root}/.runtime"
log_dir="${runtime_root}/logs"

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

kill_matching() {
  local pattern="$1"
  local pids
  pids="$(pgrep -f "${pattern}" || true)"
  if [ -z "${pids}" ]; then
    return 0
  fi
  echo "${pids}" | xargs kill
}

stop_gateway() {
  kill_matching "${repo_root}/.runtime/bin/juicefs-milvus-integration gateway"
  kill_matching "${repo_root}/juicefs-1.3.0-rc1/juicefs gateway"
}

stop_bridge() {
  kill_matching "vectorbucket-milvus-bridge"
}

stop_milvus() {
  (
    cd "${repo_root}/milvus/deploy/standalone"
    docker compose down --remove-orphans
  )
}

run_step "停止 JuiceFS gateway" stop_gateway
run_step "停止遗留 bridge 进程" stop_bridge
run_step "停止 Milvus standalone 容器" stop_milvus

echo
log "成功" "停止完成"
echo "日志目录仍保留: ${log_dir}"
echo "运行时数据仍保留: ${runtime_root}"
