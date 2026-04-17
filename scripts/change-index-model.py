#!/usr/bin/env python3
"""
用途:
    对已经创建好的 VectorBucket index 发起 Phase 3a 手动热切换请求。
    该脚本调用内部 ChangeIndexModel 管理接口，并默认轮询本地 SQLite metadata，
    观察迁移状态是否从 UPGRADING 收敛到完成。

前置条件:
    - 已通过 scripts/start-vectorbucket-standalone.sh 启动本地环境
    - 目标 vector bucket 和 index 已存在
    - 当前机器能访问本地 metadata 数据库（默认 .runtime/vectorbucket/metadata.db）

用法:
    python3 scripts/change-index-model.py [options]

示例:
    python3 scripts/change-index-model.py \
      --vector-bucket-name bench-ivf \
      --index-name main \
      --target-model hnsw

    python3 scripts/change-index-model.py \
      --vector-bucket-name bench-hnsw \
      --index-name main \
      --target-model diskann \
      --timeout-seconds 300

说明:
    - 这是内部管理脚本，不属于 boto3 标准 S3 Vectors API。
    - 默认会等待迁移完成；如果只想发起请求不等待，可传 --no-wait。
"""

from __future__ import annotations

import argparse
import json
import sqlite3
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any


REPO_ROOT = Path(__file__).resolve().parents[1]


def log(level: str, message: str, payload: dict[str, Any] | None = None, *, stream=None) -> None:
    if stream is None:
        stream = sys.stdout
    print(f"[{level}] {message}", file=stream, flush=True)
    if payload is not None:
        print(json.dumps(payload, ensure_ascii=False, indent=2), file=stream, flush=True)


def fetch_collection_row(sqlite_path: Path, bucket_name: str, index_name: str) -> dict[str, Any] | None:
    if not sqlite_path.exists():
        return None
    conn = sqlite3.connect(str(sqlite_path))
    conn.row_factory = sqlite3.Row
    try:
        row = conn.execute(
            """
            SELECT
              c.id,
              c.index_type,
              c.tier,
              c.physical_name,
              c.migrate_state,
              c.target_index_type,
              c.source_physical_name,
              c.target_physical_name,
              c.last_migrate_error,
              c.maintenance_since,
              c.updated_at
            FROM collections c
            JOIN buckets b ON b.id = c.bucket_id
            WHERE b.name = ? AND c.name = ?
            """,
            (bucket_name, index_name),
        ).fetchone()
        if row is None:
            return None
        return dict(row)
    finally:
        conn.close()


def post_change_index_model(endpoint: str, account_id: str, region: str, bucket_name: str, index_name: str, target_model: str) -> dict[str, Any]:
    payload = {
        "vectorBucketName": bucket_name,
        "indexName": index_name,
        "indexModel": target_model,
    }
    req = urllib.request.Request(
        url=f"{endpoint.rstrip('/')}/ChangeIndexModel",
        data=json.dumps(payload).encode("utf-8"),
        headers={
            "Content-Type": "application/json",
            "X-Amz-Account-Id": account_id,
            "X-Amz-Region": region,
        },
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            body = resp.read().decode("utf-8")
            return json.loads(body) if body else {}
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"HTTP {exc.code}: {detail}") from exc
    except urllib.error.URLError as exc:
        raise RuntimeError(f"request failed: {exc}") from exc


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="发起 VectorBucket index 模型热切换")
    parser.add_argument("--endpoint", default="http://127.0.0.1:9000")
    parser.add_argument("--account-id", default="123456789012")
    parser.add_argument("--region", default="us-east-1")
    parser.add_argument("--vector-bucket-name", required=True)
    parser.add_argument("--index-name", default="main")
    parser.add_argument("--target-model", required=True, choices=("ivf_sq8", "hnsw", "diskann"))
    parser.add_argument("--sqlite-path", default=str(REPO_ROOT / ".runtime" / "vectorbucket" / "metadata.db"))
    parser.add_argument("--timeout-seconds", type=int, default=180)
    parser.add_argument("--poll-interval-seconds", type=float, default=2.0)
    parser.add_argument("--no-wait", action="store_true")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    sqlite_path = Path(args.sqlite_path)

    before = fetch_collection_row(sqlite_path, args.vector_bucket_name, args.index_name)
    log(
        "信息",
        "准备发起 VectorBucket index 热切换",
        {
            "endpoint": args.endpoint,
            "vectorBucketName": args.vector_bucket_name,
            "indexName": args.index_name,
            "targetModel": args.target_model,
            "sqlitePath": str(sqlite_path),
            "before": before,
        },
    )

    started = time.perf_counter()
    log("开始", "调用内部 ChangeIndexModel 接口")
    resp = post_change_index_model(
        endpoint=args.endpoint,
        account_id=args.account_id,
        region=args.region,
        bucket_name=args.vector_bucket_name,
        index_name=args.index_name,
        target_model=args.target_model,
    )
    log("成功", f"切换请求已提交，耗时 {time.perf_counter() - started:.2f}s", resp)

    if args.no_wait:
        log("信息", "已按要求仅发起切换请求，不等待后台迁移完成")
        return 0

    deadline = time.time() + args.timeout_seconds
    log("开始", "轮询本地 metadata，等待迁移完成")
    while time.time() < deadline:
        row = fetch_collection_row(sqlite_path, args.vector_bucket_name, args.index_name)
        log("信息", "当前迁移状态", row)
        if row is None:
            print("[失败] 未找到目标 collection metadata", file=sys.stderr, flush=True)
            return 1
        if row.get("last_migrate_error"):
            print(f"[失败] 迁移失败: {row['last_migrate_error']}", file=sys.stderr, flush=True)
            return 1
        if row.get("migrate_state") == "" and row.get("index_type") == args.target_model:
            log("成功", "迁移已完成，逻辑 index 已切换到目标模型", row)
            return 0
        time.sleep(args.poll_interval_seconds)

    print(
        f"[失败] 等待迁移完成超时，超过 {args.timeout_seconds}s",
        file=sys.stderr,
        flush=True,
    )
    return 1


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except KeyboardInterrupt:
        print("\n[失败] 用户中断执行。", file=sys.stderr, flush=True)
        raise SystemExit(130)
    except Exception as exc:  # pragma: no cover
        print(f"[失败] 热切换脚本异常退出: {exc}", file=sys.stderr, flush=True)
        raise SystemExit(1)
