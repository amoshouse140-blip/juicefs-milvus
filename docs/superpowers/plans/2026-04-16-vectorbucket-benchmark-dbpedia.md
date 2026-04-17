# Vector Bucket Benchmark Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 增加一套基于官方 `boto3 s3vectors` client 的 Vector Bucket 规模与性能测试脚本，使用开源 `DBpedia OpenAI 1M` embedding 数据集，支持 `10K / 100K / 1M` 三档压测。

**Architecture:** 基准脚本运行在仓库外层 `scripts/`，只通过 `boto3.client("s3vectors")` 调用本地 JuiceFS gateway，不直接依赖 bridge 或 Milvus 私有 API。数据集下载与切片逻辑单独封装到数据加载模块，benchmark runner 负责创建 bucket/index、批量写入、并发查询、可选删除验证，并将结果写到 `.runtime/benchmarks/` JSON 报告中。

**Tech Stack:** Python 3, boto3 `s3vectors`, botocore, Hugging Face `datasets`, Parquet, JuiceFS S3 Gateway, Milvus bridge

---

## File Structure

### 计划涉及的文件

- Create: `scripts/test-vectorbucket-benchmark.py`
- Create: `scripts/lib/vectorbucket_benchmark_dataset.py`
- Create: `scripts/lib/vectorbucket_benchmark_runner.py`
- Create: `scripts/lib/vectorbucket_benchmark_report.py`
- Create: `scripts/requirements-benchmark.txt`
- Modify: `scripts/test-vectorbucket-boto3.py`
- Modify: `milvus/deploy/standalone/README.md`

### 运行时目录

- Dataset cache: `.runtime/datasets/dbpedia-openai-1m/`
- Benchmark output: `.runtime/benchmarks/`
- Default endpoint: `http://127.0.0.1:9000`
- Default vector bucket prefix: `bench-`

### 数据集策略

- Source dataset: `filipecosta90/dbpedia-openai-1M-text-embedding-3-large-2048d`
- Profiles:
  - `10k` → 前 `10,000` 条向量
  - `100k` → 前 `100,000` 条向量
  - `1m` → 前 `1,000,000` 条向量
- Query set:
  - 从相同数据集中按固定步长抽样
  - 不单独生成伪造向量

---

### Task 1: Add Benchmark Python Dependencies

**Files:**
- Create: `scripts/requirements-benchmark.txt`

- [ ] **Step 1: Write the benchmark dependency file**

```txt
boto3>=1.42.0,<2
botocore>=1.42.0,<2
datasets>=3.0.0
pyarrow>=18.0.0
numpy>=1.26.0
```

- [ ] **Step 2: Verify the file contents**

Run: `sed -n '1,120p' scripts/requirements-benchmark.txt`
Expected: shows the five pinned dependency ranges above

- [ ] **Step 3: Install into the existing local venv**

Run: `.runtime/venv-boto3/bin/pip install -r scripts/requirements-benchmark.txt`
Expected: pip installs `datasets`, `pyarrow`, and confirms `boto3` remains available

- [ ] **Step 4: Verify imports**

Run: `.runtime/venv-boto3/bin/python -c 'import boto3, datasets, pyarrow, numpy; print("ok")'`
Expected: prints `ok`

---

### Task 2: Implement Dataset Loader For DBpedia 1M

**Files:**
- Create: `scripts/lib/vectorbucket_benchmark_dataset.py`

- [ ] **Step 1: Write a failing smoke command for the loader**

Run: `.runtime/venv-boto3/bin/python -c 'from milvus.scripts.lib.vectorbucket_benchmark_dataset import load_profile; print(load_profile("10k"))'`
Expected: FAIL with `ModuleNotFoundError` or `ImportError`

- [ ] **Step 2: Add the dataset loader module**

```python
from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
from typing import Iterator

from datasets import load_dataset


DATASET_ID = "filipecosta90/dbpedia-openai-1M-text-embedding-3-large-2048d"
PROFILE_SIZES = {"10k": 10_000, "100k": 100_000, "1m": 1_000_000}


@dataclass(frozen=True)
class VectorRecord:
    key: str
    vector: list[float]
    metadata: dict[str, str]


def cache_dir(repo_root: Path) -> Path:
    return repo_root / ".runtime" / "datasets" / "dbpedia-openai-1m"


def load_profile(profile: str, repo_root: Path | None = None):
    if profile not in PROFILE_SIZES:
        raise ValueError(f"unknown profile: {profile}")
    kwargs = {}
    if repo_root is not None:
        kwargs["cache_dir"] = str(cache_dir(repo_root))
    dataset = load_dataset(DATASET_ID, split="train", **kwargs)
    return dataset.select(range(PROFILE_SIZES[profile]))


def iter_records(dataset) -> Iterator[VectorRecord]:
    for row in dataset:
        yield VectorRecord(
            key=row["_id"],
            vector=[float(x) for x in row["embedding"]],
            metadata={"title": row["title"]},
        )


def sample_queries(dataset, limit: int):
    if limit <= 0:
        return []
    step = max(len(dataset) // limit, 1)
    rows = []
    for idx in range(0, len(dataset), step):
        rows.append(dataset[idx])
        if len(rows) == limit:
            break
    return rows
```

- [ ] **Step 3: Verify the loader works for the smallest profile**

Run: `.runtime/venv-boto3/bin/python -c 'from pathlib import Path; from milvus.scripts.lib.vectorbucket_benchmark_dataset import load_profile; ds = load_profile("10k", Path(".")); print(len(ds), len(ds[0]["embedding"]))'`
Expected: prints `10000` and a positive embedding dimension

- [ ] **Step 4: Verify sampled query rows are non-empty**

Run: `.runtime/venv-boto3/bin/python -c 'from pathlib import Path; from milvus.scripts.lib.vectorbucket_benchmark_dataset import load_profile, sample_queries; ds = load_profile("10k", Path(".")); rows = sample_queries(ds, 5); print(len(rows), rows[0]["_id"])'`
Expected: prints `5` and a non-empty row id

---

### Task 3: Implement Benchmark Reporting Utilities

**Files:**
- Create: `scripts/lib/vectorbucket_benchmark_report.py`

- [ ] **Step 1: Write a failing import check**

Run: `.runtime/venv-boto3/bin/python -c 'from milvus.scripts.lib.vectorbucket_benchmark_report import summarize_latencies'`
Expected: FAIL with `ModuleNotFoundError` or `ImportError`

- [ ] **Step 2: Add report helpers for percentiles and JSON output**

```python
from __future__ import annotations

import json
from dataclasses import asdict, dataclass
from pathlib import Path

import numpy as np


@dataclass(frozen=True)
class LatencySummary:
    count: int
    p50_ms: float
    p95_ms: float
    p99_ms: float


def summarize_latencies(samples_ms: list[float]) -> LatencySummary:
    if not samples_ms:
        return LatencySummary(count=0, p50_ms=0.0, p95_ms=0.0, p99_ms=0.0)
    arr = np.array(samples_ms, dtype=float)
    return LatencySummary(
        count=int(arr.size),
        p50_ms=float(np.percentile(arr, 50)),
        p95_ms=float(np.percentile(arr, 95)),
        p99_ms=float(np.percentile(arr, 99)),
    )


def write_report(path: Path, payload: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(payload, ensure_ascii=True, indent=2) + "\n")


def dataclass_payload(value) -> dict:
    return asdict(value)
```

- [ ] **Step 3: Verify percentile math**

Run: `.runtime/venv-boto3/bin/python -c 'from milvus.scripts.lib.vectorbucket_benchmark_report import summarize_latencies; s = summarize_latencies([1,2,3,4,5]); print(s.count, round(s.p50_ms, 2), round(s.p95_ms, 2))'`
Expected: prints `5 3.0` and a valid `p95` float

- [ ] **Step 4: Verify report writing**

Run: `.runtime/venv-boto3/bin/python -c 'from pathlib import Path; from milvus.scripts.lib.vectorbucket_benchmark_report import write_report; p = Path(\".runtime/benchmarks/report-smoke.json\"); write_report(p, {\"ok\": True}); print(p.exists())'`
Expected: prints `True`

---

### Task 4: Implement Benchmark Runner

**Files:**
- Create: `scripts/lib/vectorbucket_benchmark_runner.py`

- [ ] **Step 1: Write a failing import check**

Run: `.runtime/venv-boto3/bin/python -c 'from milvus.scripts.lib.vectorbucket_benchmark_runner import BenchmarkConfig'`
Expected: FAIL with `ModuleNotFoundError` or `ImportError`

- [ ] **Step 2: Add benchmark runner types and boto3 client builder**

```python
from __future__ import annotations

from dataclasses import dataclass
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
```

- [ ] **Step 3: Add create/cleanup helpers**

```python
import time


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
    for start in range(0, len(items), batch_size):
        yield items[start : start + batch_size]
```

- [ ] **Step 4: Add ingest benchmark**

```python
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
```

- [ ] **Step 5: Add concurrent query benchmark**

```python
from concurrent.futures import ThreadPoolExecutor, as_completed


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
```

- [ ] **Step 6: Verify the runner module imports**

Run: `.runtime/venv-boto3/bin/python -c 'from milvus.scripts.lib.vectorbucket_benchmark_runner import BenchmarkConfig, chunked; print(list(chunked([1,2,3,4,5], 2)))'`
Expected: prints `[[1, 2], [3, 4], [5]]`

---

### Task 5: Implement The CLI Benchmark Script

**Files:**
- Create: `scripts/test-vectorbucket-benchmark.py`

- [ ] **Step 1: Write a failing invocation**

Run: `.runtime/venv-boto3/bin/python scripts/test-vectorbucket-benchmark.py --help`
Expected: FAIL with `No such file or directory`

- [ ] **Step 2: Add CLI parsing and profile mapping**

```python
#!/usr/bin/env python3
from __future__ import annotations

import argparse
import time
import uuid
from pathlib import Path

from milvus.scripts.lib.vectorbucket_benchmark_dataset import iter_records, load_profile, sample_queries
from milvus.scripts.lib.vectorbucket_benchmark_report import dataclass_payload, summarize_latencies, write_report
from milvus.scripts.lib.vectorbucket_benchmark_runner import (
    BenchmarkConfig,
    build_client,
    cleanup_schema,
    create_schema,
    ingest_vectors,
    run_queries,
)


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--profile", choices=["10k", "100k", "1m"], default="10k")
    parser.add_argument("--endpoint", default="http://127.0.0.1:9000")
    parser.add_argument("--region", default="us-east-1")
    parser.add_argument("--access-key", default="admin")
    parser.add_argument("--secret-key", default="12345678")
    parser.add_argument("--index-name", default="main")
    parser.add_argument("--batch-size", type=int, default=200)
    parser.add_argument("--query-count", type=int, default=200)
    parser.add_argument("--query-workers", type=int, default=8)
    parser.add_argument("--top-k", type=int, default=10)
    parser.add_argument("--skip-delete-check", action="store_true")
    return parser.parse_args()
```

- [ ] **Step 3: Add main flow**

```python
def main() -> int:
    args = parse_args()
    repo_root = Path(__file__).resolve().parents[2]
    dataset = load_profile(args.profile, repo_root)
    records = list(iter_records(dataset))
    queries = [row["embedding"] for row in sample_queries(dataset, max(args.query_count, 10))]
    bucket_name = f"bench-{args.profile}-{uuid.uuid4().hex[:8]}"
    cfg = BenchmarkConfig(
        endpoint_url=args.endpoint,
        region=args.region,
        access_key=args.access_key,
        secret_key=args.secret_key,
        vector_bucket_name=bucket_name,
        index_name=args.index_name,
        dimension=len(records[0].vector),
        batch_size=args.batch_size,
        query_count=args.query_count,
        query_workers=args.query_workers,
        top_k=args.top_k,
    )

    client = build_client(cfg)
    started = time.strftime("%Y%m%d-%H%M%S")
    report_path = repo_root / ".runtime" / "benchmarks" / f"vectorbucket-{args.profile}-{started}.json"

    create_schema(client, cfg)
    try:
        ingest = ingest_vectors(
            client,
            cfg,
            [{"key": r.key, "vector": r.vector, "metadata": r.metadata} for r in records],
        )
        query = run_queries(client, cfg, queries)
        payload = {
            "profile": args.profile,
            "vector_bucket_name": bucket_name,
            "dimension": cfg.dimension,
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
        cleanup_schema(client, cfg)
    return 0
```

- [ ] **Step 4: Verify CLI help**

Run: `.runtime/venv-boto3/bin/python scripts/test-vectorbucket-benchmark.py --help`
Expected: prints `--profile`, `--batch-size`, `--query-count`, and `--query-workers`

---

### Task 6: Add Delete Visibility Check To The Benchmark

**Files:**
- Modify: `scripts/lib/vectorbucket_benchmark_runner.py`
- Modify: `scripts/test-vectorbucket-benchmark.py`

- [ ] **Step 1: Add delete check helper to the runner**

```python
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
```

- [ ] **Step 2: Call the delete check from the CLI unless skipped**

```python
        delete_check = None
        if not args.skip_delete_check:
            delete_check = verify_delete_visibility(client, cfg, records[0].key, records[0].vector)
        payload = {
            "profile": args.profile,
            "vector_bucket_name": bucket_name,
            "dimension": cfg.dimension,
            "delete_check": delete_check,
            "ingest": {
```

- [ ] **Step 3: Verify the delete check result is emitted**

Run: `.runtime/venv-boto3/bin/python scripts/test-vectorbucket-benchmark.py --profile 10k --query-count 20`
Expected: prints a JSON report path; report contains `"delete_check"` and `"visible_after_delete": false`

---

### Task 7: Document How To Run The Benchmark

**Files:**
- Modify: `milvus/deploy/standalone/README.md`
- Modify: `scripts/test-vectorbucket-boto3.py`

- [ ] **Step 1: Add a benchmark section to the standalone README**

```markdown
## Vector Bucket benchmark

Install benchmark dependencies:

```bash
.runtime/venv-boto3/bin/pip install -r scripts/requirements-benchmark.txt
```

Run the three supported profiles:

```bash
.runtime/venv-boto3/bin/python scripts/test-vectorbucket-benchmark.py --profile 10k
.runtime/venv-boto3/bin/python scripts/test-vectorbucket-benchmark.py --profile 100k
.runtime/venv-boto3/bin/python scripts/test-vectorbucket-benchmark.py --profile 1m
```

Reports are written to `.runtime/benchmarks/`.
```

- [ ] **Step 2: Update the boto3 smoke script header comment**

```python
#!/usr/bin/env python3
"""
Smoke test for boto3 S3 Vectors compatibility.
For scale and performance testing, use test-vectorbucket-benchmark.py.
"""
```

- [ ] **Step 3: Verify documentation references the benchmark script**

Run: `rg -n 'test-vectorbucket-benchmark.py|requirements-benchmark.txt|.runtime/benchmarks' milvus/deploy/standalone/README.md scripts/test-vectorbucket-boto3.py`
Expected: output shows all three strings

---

### Task 8: Verify 10K End-To-End Benchmark

**Files:**
- Test: `scripts/test-vectorbucket-benchmark.py`
- Test: `scripts/lib/vectorbucket_benchmark_dataset.py`
- Test: `scripts/lib/vectorbucket_benchmark_runner.py`

- [ ] **Step 1: Verify dataset loader and report helpers**

Run: `.runtime/venv-boto3/bin/python -m py_compile scripts/test-vectorbucket-benchmark.py scripts/lib/vectorbucket_benchmark_dataset.py scripts/lib/vectorbucket_benchmark_runner.py scripts/lib/vectorbucket_benchmark_report.py`
Expected: exits 0 with no output

- [ ] **Step 2: Run a 10K benchmark against the local stack**

Run: `.runtime/venv-boto3/bin/python scripts/test-vectorbucket-benchmark.py --profile 10k --batch-size 200 --query-count 100 --query-workers 8`
Expected: prints a report path under `.runtime/benchmarks/` and exits 0

- [ ] **Step 3: Verify the 10K report fields**

Run: `latest=$(ls -t .runtime/benchmarks/vectorbucket-10k-*.json | head -n 1) && jq '.profile, .dimension, .ingest.inserted, .query.queries, .delete_check.visible_after_delete' "$latest"`
Expected: prints `"10k"`, a positive dimension, `10000`, a positive query count, and `false`

---

### Task 9: Verify 100K And 1M Profiles

**Files:**
- Test: `scripts/test-vectorbucket-benchmark.py`

- [ ] **Step 1: Run the 100K benchmark**

Run: `.runtime/venv-boto3/bin/python scripts/test-vectorbucket-benchmark.py --profile 100k --batch-size 500 --query-count 500 --query-workers 16`
Expected: exits 0 and writes `.runtime/benchmarks/vectorbucket-100k-*.json`

- [ ] **Step 2: Run the 1M benchmark**

Run: `.runtime/venv-boto3/bin/python scripts/test-vectorbucket-benchmark.py --profile 1m --batch-size 1000 --query-count 1000 --query-workers 16`
Expected: exits 0 and writes `.runtime/benchmarks/vectorbucket-1m-*.json`

- [ ] **Step 3: Compare the generated reports**

Run: `ls -1 .runtime/benchmarks/vectorbucket-{100k,1m}-*.json | tail -n 2`
Expected: lists one `100k` report and one `1m` report

- [ ] **Step 4: Capture the summary metrics for review**

Run: `for f in $(ls -t .runtime/benchmarks/vectorbucket-{10k,100k,1m}-*.json | head -n 3); do echo "== $f =="; jq '{profile, ingest_qps: .ingest.qps, query_qps: .query.qps, query_p95_ms: .query.latency_summary.p95_ms}' "$f"; done`
Expected: prints comparable metrics for all three profiles

---

## Self-Review

- Spec coverage:
  - 开源数据集：Task 2
  - `10K / 100K / 1M` 三档：Task 2 + Task 9
  - 只走 `boto3 s3vectors`：Task 4 + Task 5
  - 性能指标与 JSON 输出：Task 3 + Task 5
  - 删除可见性验证：Task 6
  - 文档与运行方法：Task 7

- Placeholder scan:
  - 已消除 `TODO/TBD` 与“稍后实现”描述
  - 每个 task 都给出目标文件、命令和期望结果

- Type consistency:
  - `BenchmarkConfig`、`VectorRecord`、`LatencySummary` 在任务之间命名一致
  - CLI 和 runner 使用同一组字段名：`profile`、`query_count`、`query_workers`、`top_k`

