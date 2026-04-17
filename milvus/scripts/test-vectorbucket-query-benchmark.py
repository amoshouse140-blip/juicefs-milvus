#!/usr/bin/env python3
from __future__ import annotations

import argparse
import sqlite3
import sys
import time
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
if str(REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(REPO_ROOT))

from milvus.scripts.lib.vectorbucket_benchmark_dataset import sample_stream_queries, stream_profile
from milvus.scripts.lib.vectorbucket_benchmark_report import dataclass_payload, summarize_latencies, write_report
from milvus.scripts.lib.vectorbucket_benchmark_runner import BenchmarkConfig, build_client, run_queries


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
    return parser.parse_args()


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
    query_rows = sample_stream_queries(
        stream_profile(args.profile),
        max(args.query_count, args.warmup_count, 10),
    )
    if not query_rows:
        raise RuntimeError(f"no rows available for profile {args.profile}")

    bucket_name = args.vector_bucket_name or latest_vector_bucket_name(REPO_ROOT)
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
        run_queries(client, warmup_cfg, warmup_queries)

    result = run_queries(client, cfg, measured_queries)
    started = time.strftime("%Y%m%d-%H%M%S")
    report_path = REPO_ROOT / ".runtime" / "benchmarks" / f"vectorbucket-query-{args.profile}-{started}.json"
    payload = {
        "profile": args.profile,
        "vector_bucket_name": bucket_name,
        "index_name": args.index_name,
        "dimension": dimension,
        "warmup_count": args.warmup_count,
        "query": {
            **result,
            "latency_summary": dataclass_payload(summarize_latencies(result["latencies_ms"])),
        },
    }
    write_report(report_path, payload)
    print(report_path)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
