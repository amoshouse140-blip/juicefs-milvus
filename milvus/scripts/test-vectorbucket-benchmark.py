#!/usr/bin/env python3
from __future__ import annotations

import argparse
import sys
import time
import uuid
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
if str(REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(REPO_ROOT))

from milvus.scripts.lib.vectorbucket_benchmark_dataset import (
    iter_records,
    sample_stream_queries,
    stream_profile,
)
from milvus.scripts.lib.vectorbucket_benchmark_report import dataclass_payload, summarize_latencies, write_report
from milvus.scripts.lib.vectorbucket_benchmark_runner import (
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
    parser.add_argument("--index-name", default="main")
    parser.add_argument("--batch-size", type=int, default=20)
    parser.add_argument("--query-count", type=int, default=200)
    parser.add_argument("--query-workers", type=int, default=8)
    parser.add_argument("--top-k", type=int, default=10)
    parser.add_argument("--skip-delete-check", action="store_true")
    parser.add_argument("--cleanup", action="store_true")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    query_rows = sample_stream_queries(stream_profile(args.profile), max(args.query_count, 10))
    if not query_rows:
        raise RuntimeError(f"no rows available for profile {args.profile}")
    first_record = query_rows[0]
    queries = [row["embedding"] for row in query_rows]
    bucket_name = f"bench-{args.profile}-{uuid.uuid4().hex[:8]}"
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

    create_schema(client, cfg)
    try:
        ingest = ingest_vectors(
            client,
            cfg,
            (
                {"key": record.key, "vector": record.vector, "metadata": record.metadata}
                for record in iter_records(stream_profile(args.profile))
            ),
        )
        query = run_queries(client, cfg, queries)
        delete_check = None
        if not args.skip_delete_check:
            delete_check = verify_delete_visibility(
                client,
                cfg,
                first_record["_id"],
                [float(x) for x in first_record["embedding"]],
            )
        payload = {
            "profile": args.profile,
            "vector_bucket_name": bucket_name,
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
        print(report_path)
    finally:
        if args.cleanup:
            cleanup_schema(client, cfg)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
