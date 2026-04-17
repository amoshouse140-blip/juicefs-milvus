from __future__ import annotations

import time
from concurrent.futures import ThreadPoolExecutor, as_completed
from dataclasses import dataclass
from itertools import islice
from typing import Any

import boto3
from botocore.config import Config


@dataclass(frozen=True)
class BenchmarkConfig:
    endpoint_url: str
    region: str
    access_key: str
    secret_key: str
    vector_bucket_name: str
    index_name: str
    dimension: int
    batch_size: int
    query_count: int
    query_workers: int
    top_k: int


def build_client(cfg: BenchmarkConfig):
    return boto3.client(
        "s3vectors",
        region_name=cfg.region,
        endpoint_url=cfg.endpoint_url,
        aws_access_key_id=cfg.access_key,
        aws_secret_access_key=cfg.secret_key,
        config=Config(signature_version="v4"),
    )


def create_schema(client: Any, cfg: BenchmarkConfig) -> None:
    client.create_vector_bucket(vectorBucketName=cfg.vector_bucket_name)
    client.create_index(
        vectorBucketName=cfg.vector_bucket_name,
        indexName=cfg.index_name,
        dataType="float32",
        dimension=cfg.dimension,
        distanceMetric="cosine",
    )


def cleanup_schema(client: Any, cfg: BenchmarkConfig) -> None:
    try:
        client.delete_index(vectorBucketName=cfg.vector_bucket_name, indexName=cfg.index_name)
    except Exception:
        pass
    try:
        client.delete_vector_bucket(vectorBucketName=cfg.vector_bucket_name)
    except Exception:
        pass


def chunked(items, batch_size: int):
    iterator = iter(items)
    while True:
        batch = list(islice(iterator, batch_size))
        if not batch:
            return
        yield batch


def ingest_vectors(client: Any, cfg: BenchmarkConfig, records: list[dict]) -> dict:
    started = time.perf_counter()
    batch_latencies_ms = []
    inserted = 0
    for batch in chunked(records, cfg.batch_size):
        payload = [
            {"key": row["key"], "data": {"float32": row["vector"]}, "metadata": row["metadata"]}
            for row in batch
        ]
        batch_started = time.perf_counter()
        client.put_vectors(
            vectorBucketName=cfg.vector_bucket_name,
            indexName=cfg.index_name,
            vectors=payload,
        )
        batch_latencies_ms.append((time.perf_counter() - batch_started) * 1000.0)
        inserted += len(batch)
    elapsed = time.perf_counter() - started
    return {
        "inserted": inserted,
        "elapsed_sec": elapsed,
        "qps": inserted / elapsed if elapsed > 0 else 0.0,
        "batch_latencies_ms": batch_latencies_ms,
    }


def run_queries(client: Any, cfg: BenchmarkConfig, queries: list[list[float]]) -> dict:
    latencies_ms = []
    failures = 0

    def do_query(vector: list[float]) -> float:
        started = time.perf_counter()
        client.query_vectors(
            vectorBucketName=cfg.vector_bucket_name,
            indexName=cfg.index_name,
            queryVector={"float32": vector},
            topK=cfg.top_k,
            returnDistance=True,
            returnMetadata=False,
        )
        return (time.perf_counter() - started) * 1000.0

    started = time.perf_counter()
    with ThreadPoolExecutor(max_workers=cfg.query_workers) as pool:
        futures = [pool.submit(do_query, vector) for vector in queries[: cfg.query_count]]
        for future in as_completed(futures):
            try:
                latencies_ms.append(future.result())
            except Exception:
                failures += 1
    elapsed = time.perf_counter() - started
    ok = len(latencies_ms)
    return {
        "queries": ok,
        "failures": failures,
        "elapsed_sec": elapsed,
        "qps": ok / elapsed if elapsed > 0 else 0.0,
        "latencies_ms": latencies_ms,
    }


def verify_delete_visibility(client: Any, cfg: BenchmarkConfig, key: str, vector: list[float]) -> dict:
    client.delete_vectors(
        vectorBucketName=cfg.vector_bucket_name,
        indexName=cfg.index_name,
        keys=[key],
    )
    response = client.query_vectors(
        vectorBucketName=cfg.vector_bucket_name,
        indexName=cfg.index_name,
        queryVector={"float32": vector},
        topK=cfg.top_k,
        returnDistance=True,
        returnMetadata=False,
    )
    keys = [item.get("key") for item in response.get("vectors", [])]
    return {"deleted_key": key, "visible_after_delete": key in keys, "remaining_keys": keys}
