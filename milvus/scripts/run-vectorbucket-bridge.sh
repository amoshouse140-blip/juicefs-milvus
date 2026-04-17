#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"

export MILVUS_ADDR="${MILVUS_ADDR:-127.0.0.1:19530}"
export BRIDGE_LISTEN_ADDR="${BRIDGE_LISTEN_ADDR:-:19531}"

cd "${repo_root}/bridge"
exec env GOWORK=off go run ./cmd/vectorbucket-milvus-bridge
