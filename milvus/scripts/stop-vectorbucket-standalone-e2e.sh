#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
runtime_root="${repo_root}/.runtime"
log_dir="${runtime_root}/logs"

kill_matching() {
  local pattern="$1"
  local pids
  pids="$(pgrep -f "${pattern}" || true)"
  if [ -z "${pids}" ]; then
    return 0
  fi
  echo "${pids}" | xargs kill
}

echo "Stopping JuiceFS gateway..."
kill_matching "${repo_root}/juicefs-1.3.0-rc1/juicefs gateway"

echo "Stopping VectorBucket Milvus bridge..."
kill_matching "vectorbucket-milvus-bridge"

echo "Stopping Milvus standalone compose..."
(
  cd "${repo_root}/milvus/deploy/standalone"
  docker compose down --remove-orphans
)

echo
echo "Stopped."
echo "Logs remain under: ${log_dir}"
echo "Runtime data remain under: ${runtime_root}"
