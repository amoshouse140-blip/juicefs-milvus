#!/usr/bin/env python3
"""
用途:
    对 S3 Vectors 兼容链路执行端到端写入 + 查询 benchmark。
    脚本会创建 schema、按指定 profile 写入数据、执行查询压测、
    可选校验删除可见性，并输出 JSON 报告。

前置条件:
    - JuiceFS gateway 已经部署并可访问
    - 当前 Python 环境已安装 boto3 和 benchmark 依赖

用法:
    python3 scripts/test-vectorbucket-benchmark.py [options]

示例:
    python3 scripts/test-vectorbucket-benchmark.py --profile 10k
    python3 scripts/test-vectorbucket-benchmark.py --profile 10k --vector-bucket-name bench-hnsw --cleanup
    python3 scripts/test-vectorbucket-benchmark.py --profile 100k --vector-bucket-name bench-diskann --batch-size 100 --cleanup
    python3 scripts/test-vectorbucket-benchmark.py --profile 10k --vector-bucket-name bench-hnsw --index-model hnsw --cleanup
    python3 scripts/test-vectorbucket-benchmark.py --profile 10k --vector-bucket-name bench-diskann --index-model diskann --keep-config

说明:
    - 脚本开始时会先打印当前 bucket 对应的索引模型。
    - `--index-model` 会在脚本开始前临时改写 `configs/vectorbucket.json` 中当前 bucket 的索引模型，
      默认结束后恢复原配置。
    - 如果传 `--keep-config`，脚本结束后不会恢复原配置。
    - 报告会写入 `.runtime/benchmarks/`。
"""
from __future__ import annotations

import argparse
import json
import sys
import time
import uuid
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[1]
if str(REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(REPO_ROOT))

from scripts.lib.vectorbucket_benchmark_dataset import (
    iter_records,
    load_profile,
    sample_stream_queries,
)
from scripts.lib.vectorbucket_benchmark_report import dataclass_payload, summarize_latencies, write_report
from scripts.lib.vectorbucket_bucket_policy import (
    apply_bucket_index_model,
    bucket_index_model,
    restore_bucket_policy_config,
)
from scripts.lib.vectorbucket_benchmark_runner import (
    BenchmarkConfig,
    build_client,
    cleanup_schema,
    create_schema,
    ingest_vectors,
    run_queries,
    verify_delete_visibility,
)


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--profile", choices=["10k", "100k", "1m"], default="10k")
    parser.add_argument("--endpoint", default="http://127.0.0.1:9000")
    parser.add_argument("--region", default="us-east-1")
    parser.add_argument("--access-key", default="admin")
    parser.add_argument("--secret-key", default="12345678")
    parser.add_argument("--vector-bucket-name", default="")
    parser.add_argument("--index-name", default="main")
    parser.add_argument("--batch-size", type=int, default=100)
    parser.add_argument("--query-count", type=int, default=200)
    parser.add_argument("--query-workers", type=int, default=8)
    parser.add_argument("--top-k", type=int, default=10)
    parser.add_argument("--skip-delete-check", action="store_true")
    parser.add_argument("--cleanup", action="store_true")
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


def main() -> int:
    args = parse_args()
    dataset = load_profile(args.profile, REPO_ROOT)
    query_rows = sample_stream_queries(dataset, max(args.query_count, 10))
    if not query_rows:
        raise RuntimeError(f"no rows available for profile {args.profile}")
    first_record = query_rows[0]
    queries = [row["embedding"] for row in query_rows]
    bucket_name = args.vector_bucket_name or f"bench-{args.profile}-{uuid.uuid4().hex[:8]}"
    previous_config = None
    if args.index_model:
        previous_config = apply_bucket_index_model(bucket_name, args.index_model)
        log("信息", "已按脚本参数临时写入 bucket 索引模型配置", {"vector_bucket_name": bucket_name, "index_model": args.index_model})
    index_model = bucket_index_model(bucket_name)
    cfg = BenchmarkConfig(
        endpoint_url=args.endpoint,
        region=args.region,
        access_key=args.access_key,
        secret_key=args.secret_key,
        vector_bucket_name=bucket_name,
        index_name=args.index_name,
        dimension=len(first_record["embedding"]),
        batch_size=args.batch_size,
        query_count=args.query_count,
        query_workers=args.query_workers,
        top_k=args.top_k,
    )

    client = build_client(cfg)
    started = time.strftime("%Y%m%d-%H%M%S")
    report_path = REPO_ROOT / ".runtime" / "benchmarks" / f"vectorbucket-{args.profile}-{started}.json"

    log(
        "信息",
        "开始执行 VectorBucket 综合 benchmark",
        {
            "profile": args.profile,
            "endpoint": args.endpoint,
            "vector_bucket_name": bucket_name,
            "index_name": args.index_name,
            "index_model": index_model,
            "batch_size": args.batch_size,
            "query_count": args.query_count,
            "query_workers": args.query_workers,
            "top_k": args.top_k,
            "cleanup": args.cleanup,
        },
    )

    create_schema_result = run_step("创建 vector bucket 和 index", lambda: create_schema(client, cfg))
    try:
        log(
            "信息",
            "开始批量写入向量",
            {
                "profile": args.profile,
                "batch_size": args.batch_size,
            },
        )
        ingest = run_step(
            "执行向量写入阶段",
            lambda: ingest_vectors(
                client,
                cfg,
                (
                    {"key": record.key, "vector": record.vector, "metadata": record.metadata}
                    for record in iter_records(dataset)
                ),
            ),
        )
        log(
            "成功",
            "写入阶段完成",
            {
                "inserted": ingest.get("inserted"),
                "elapsed_sec": round(float(ingest.get("elapsed_sec", 0)), 2),
                "qps": round(float(ingest.get("qps", 0)), 2),
                "batch_p50_ms": round(float(dataclass_payload(summarize_latencies(ingest["batch_latencies_ms"])).get("p50_ms", 0)), 2),
                "batch_p95_ms": round(float(dataclass_payload(summarize_latencies(ingest["batch_latencies_ms"])).get("p95_ms", 0)), 2),
                "batch_p99_ms": round(float(dataclass_payload(summarize_latencies(ingest["batch_latencies_ms"])).get("p99_ms", 0)), 2),
            },
        )
        query = run_step("执行查询阶段", lambda: run_queries(client, cfg, queries))
        log(
            "成功",
            "查询阶段完成",
            {
                "queries": query.get("queries"),
                "failures": query.get("failures"),
                "elapsed_sec": round(float(query.get("elapsed_sec", 0)), 2),
                "qps": round(float(query.get("qps", 0)), 2),
                "p50_ms": round(float(dataclass_payload(summarize_latencies(query["latencies_ms"])).get("p50_ms", 0)), 2),
                "p95_ms": round(float(dataclass_payload(summarize_latencies(query["latencies_ms"])).get("p95_ms", 0)), 2),
                "p99_ms": round(float(dataclass_payload(summarize_latencies(query["latencies_ms"])).get("p99_ms", 0)), 2),
            },
        )
        delete_check = None
        if not args.skip_delete_check:
            delete_check = run_step(
                "执行删除可见性校验",
                lambda: verify_delete_visibility(
                    client,
                    cfg,
                    first_record["_id"],
                    [float(x) for x in first_record["embedding"]],
                ),
            )
        payload = {
            "profile": args.profile,
            "vector_bucket_name": bucket_name,
            "index_model": index_model,
            "dimension": cfg.dimension,
            "cleanup": args.cleanup,
            "delete_check": delete_check,
            "ingest": {
                **ingest,
                "batch_latency_summary": dataclass_payload(summarize_latencies(ingest["batch_latencies_ms"])),
            },
            "query": {
                **query,
                "latency_summary": dataclass_payload(summarize_latencies(query["latencies_ms"])),
            },
        }
        write_report(report_path, payload)
        log("成功", "benchmark 报告已写出", {"report_path": str(report_path)})
    finally:
        if args.cleanup:
            run_step("清理 vector bucket 和 index", lambda: cleanup_schema(client, cfg))
        if previous_config is not None and not args.keep_config:
            restore_bucket_policy_config(previous_config)
            log("信息", "已恢复原始 bucket 索引模型配置")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f"[失败] benchmark 脚本异常退出: {exc}", file=sys.stderr, flush=True)
        raise SystemExit(1)
