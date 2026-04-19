#!/usr/bin/env python3
"""
用途:
    对已经存在数据的 VectorBucket index 执行纯查询 benchmark。
    脚本不会写入数据，只会做 warmup 和正式查询压测，并输出 JSON 报告。

前置条件:
    - JuiceFS gateway 已经部署并可访问
    - 目标 bucket/index 中已经写入过向量数据
    - 当前 Python 环境已安装 boto3 和 benchmark 依赖

用法:
    python3 scripts/test-vectorbucket-query-benchmark.py [options]

示例:
    python3 scripts/test-vectorbucket-query-benchmark.py --profile 1m --vector-bucket-name bench-hnsw
    python3 scripts/test-vectorbucket-query-benchmark.py --profile 100k --vector-bucket-name bench-ivf --query-workers 16
    python3 scripts/test-vectorbucket-query-benchmark.py --profile 10k --vector-bucket-name bench-hnsw --index-model hnsw
    python3 scripts/test-vectorbucket-query-benchmark.py --profile 10k --vector-bucket-name bench-diskann --index-model diskann --keep-config

说明:
    - 如果不传 `--vector-bucket-name`，脚本会默认读取 `.runtime/vectorbucket/metadata.db`
      里最近更新的 bucket。
    - 脚本开始时会先打印当前 bucket 对应的索引模型。
    - `--index-model` 会在脚本开始前临时改写 `configs/vectorbucket.json` 中当前 bucket 的索引模型，
      默认结束后恢复原配置。
    - 如果传 `--keep-config`，脚本结束后不会恢复原配置。
"""
from __future__ import annotations

import argparse
import json
import sqlite3
import sys
import time
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[1]
if str(REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(REPO_ROOT))

from scripts.lib.vectorbucket_benchmark_dataset import load_profile, sample_stream_queries
from scripts.lib.vectorbucket_bucket_policy import (
    apply_bucket_index_model,
    bucket_index_model,
    restore_bucket_policy_config,
)
from scripts.lib.vectorbucket_benchmark_report import dataclass_payload, summarize_latencies, write_report
from scripts.lib.vectorbucket_benchmark_runner import BenchmarkConfig, build_client, run_queries


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--profile", choices=["10k", "100k", "1m"], default="1m")
    parser.add_argument("--endpoint", default="http://127.0.0.1:9000")
    parser.add_argument("--region", default="us-east-1")
    parser.add_argument("--access-key", default="admin")
    parser.add_argument("--secret-key", default="12345678")
    parser.add_argument("--vector-bucket-name", default="")
    parser.add_argument("--index-name", default="main")
    parser.add_argument("--query-count", type=int, default=1000)
    parser.add_argument("--query-workers", type=int, default=8)
    parser.add_argument("--top-k", type=int, default=10)
    parser.add_argument("--warmup-count", type=int, default=20)
    parser.add_argument("--index-model", choices=["ivf_sq8", "hnsw", "diskann"], default="")
    parser.add_argument("--keep-config", action="store_true")
    return parser.parse_args()


def log(level: str, message: str, payload=None) -> None:
    print(f"[{level}] {message}", flush=True)
    if payload is not None:
        print(json.dumps(payload, ensure_ascii=True, default=str, indent=2), flush=True)


def run_step(step_name: str, func):
    started = time.perf_counter()
    log("开始", step_name)
    try:
        result = func()
    except Exception as exc:
        elapsed = time.perf_counter() - started
        log("失败", f"{step_name}，耗时 {elapsed:.2f}s", {"error": str(exc)})
        raise
    elapsed = time.perf_counter() - started
    log("成功", f"{step_name}，耗时 {elapsed:.2f}s")
    return result


def latest_vector_bucket_name(repo_root: Path) -> str:
    db = repo_root / ".runtime" / "vectorbucket" / "metadata.db"
    with sqlite3.connect(db) as conn:
        row = conn.execute(
            """
            select b.name
            from collections c
            join buckets b on b.id = c.bucket_id
            order by c.updated_at desc
            limit 1
            """
        ).fetchone()
    if row is None or not row[0]:
        raise RuntimeError(f"no vector bucket found in {db}")
    return str(row[0])


def main() -> int:
    args = parse_args()
    dataset = load_profile(args.profile, REPO_ROOT)
    query_rows = sample_stream_queries(
        dataset,
        max(args.query_count, args.warmup_count, 10),
    )
    if not query_rows:
        raise RuntimeError(f"no rows available for profile {args.profile}")

    bucket_name = args.vector_bucket_name or latest_vector_bucket_name(REPO_ROOT)
    previous_config = None
    if args.index_model:
        previous_config = apply_bucket_index_model(bucket_name, args.index_model)
        log("信息", "已按脚本参数临时写入 bucket 索引模型配置", {"vector_bucket_name": bucket_name, "index_model": args.index_model})
    index_model = bucket_index_model(bucket_name)
    dimension = len(query_rows[0]["embedding"])
    cfg = BenchmarkConfig(
        endpoint_url=args.endpoint,
        region=args.region,
        access_key=args.access_key,
        secret_key=args.secret_key,
        vector_bucket_name=bucket_name,
        index_name=args.index_name,
        dimension=dimension,
        batch_size=0,
        query_count=args.query_count,
        query_workers=args.query_workers,
        top_k=args.top_k,
    )

    client = build_client(cfg)
    log(
        "信息",
        "开始执行 VectorBucket 纯查询 benchmark",
        {
            "profile": args.profile,
            "endpoint": args.endpoint,
            "vector_bucket_name": bucket_name,
            "index_name": args.index_name,
            "index_model": index_model,
            "warmup_count": args.warmup_count,
            "query_count": args.query_count,
            "query_workers": args.query_workers,
            "top_k": args.top_k,
        },
    )
    warmup_queries = [row["embedding"] for row in query_rows[: args.warmup_count]]
    measured_queries = [row["embedding"] for row in query_rows[: args.query_count]]

    if warmup_queries:
        warmup_cfg = BenchmarkConfig(
            endpoint_url=cfg.endpoint_url,
            region=cfg.region,
            access_key=cfg.access_key,
            secret_key=cfg.secret_key,
            vector_bucket_name=cfg.vector_bucket_name,
            index_name=cfg.index_name,
            dimension=cfg.dimension,
            batch_size=cfg.batch_size,
            query_count=len(warmup_queries),
            query_workers=cfg.query_workers,
            top_k=cfg.top_k,
        )
        run_step("执行 warmup 查询", lambda: run_queries(client, warmup_cfg, warmup_queries))

    result = run_step("执行正式查询压测", lambda: run_queries(client, cfg, measured_queries))
    log(
        "成功",
        "正式查询压测完成",
        {
            "query_count": result.get("query_count"),
            "query_qps": round(float(result.get("query_qps", 0)), 2),
            "error_count": result.get("error_count"),
        },
    )
    started = time.strftime("%Y%m%d-%H%M%S")
    report_path = REPO_ROOT / ".runtime" / "benchmarks" / f"vectorbucket-query-{args.profile}-{started}.json"
    payload = {
        "profile": args.profile,
        "vector_bucket_name": bucket_name,
        "index_name": args.index_name,
        "index_model": index_model,
        "dimension": dimension,
        "warmup_count": args.warmup_count,
        "query": {
            **result,
            "latency_summary": dataclass_payload(summarize_latencies(result["latencies_ms"])),
        },
    }
    write_report(report_path, payload)
    log("成功", "query benchmark 报告已写出", {"report_path": str(report_path)})
    if previous_config is not None and not args.keep_config:
        restore_bucket_policy_config(previous_config)
        log("信息", "已恢复原始 bucket 索引模型配置")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f"[失败] query benchmark 脚本异常退出: {exc}", file=sys.stderr, flush=True)
        raise SystemExit(1)
