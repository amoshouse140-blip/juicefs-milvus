#!/usr/bin/env python3
"""
用途:
    使用 boto3 对 S3 Vectors 兼容接口做一轮功能冒烟测试。
    脚本会依次执行 create bucket、create index、put、query、delete、
    delete index、delete bucket。

前置条件:
    - JuiceFS gateway 已经部署并可访问
    - 当前 Python 环境已安装 boto3

用法:
    python3 scripts/test-vectorbucket-boto3.py

示例:
    python3 scripts/test-vectorbucket-boto3.py
    VECTOR_BUCKET_NAME=bench-hnsw python3 scripts/test-vectorbucket-boto3.py
    python3 scripts/test-vectorbucket-boto3.py --vector-bucket-name bench-hnsw --index-model hnsw
    python3 scripts/test-vectorbucket-boto3.py --vector-bucket-name bench-diskann --index-model diskann --keep-config

说明:
    - 脚本开始时会先打印当前 bucket 对应的索引模型。
    - `--index-model` 会在脚本开始前临时改写 `configs/vectorbucket.json` 中当前 bucket 的索引模型，
      默认结束后恢复原配置。
    - 如果传 `--keep-config`，脚本结束后不会恢复原配置。
    - 如果要做规模和时延测试，请改用 `test-vectorbucket-benchmark.py`。
"""

import json
import os
import sys
import time
import uuid
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[1]
if str(REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(REPO_ROOT))

import boto3
from botocore.config import Config

from scripts.lib.vectorbucket_bucket_policy import (
    apply_bucket_index_model,
    bucket_index_model,
    restore_bucket_policy_config,
)


def env(name: str, default: str) -> str:
    value = os.getenv(name)
    return value if value else default


def parse_args():
    import argparse

    parser = argparse.ArgumentParser()
    parser.add_argument("--endpoint", default=env("S3VECTORS_ENDPOINT_URL", "http://127.0.0.1:9000"))
    parser.add_argument("--region", default=env("AWS_REGION", "us-east-1"))
    parser.add_argument("--access-key", default=env("AWS_ACCESS_KEY_ID", "admin"))
    parser.add_argument("--secret-key", default=env("AWS_SECRET_ACCESS_KEY", "12345678"))
    parser.add_argument("--vector-bucket-name", default=env("VECTOR_BUCKET_NAME", f"boto3-demo-{uuid.uuid4().hex[:8]}"))
    parser.add_argument("--index-name", default=env("VECTOR_INDEX_NAME", "main"))
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
    if result is None:
        log("成功", f"{step_name}，耗时 {elapsed:.2f}s")
    else:
        log("成功", f"{step_name}，耗时 {elapsed:.2f}s", result)
    return result


def main() -> int:
    args = parse_args()
    endpoint_url = args.endpoint
    region = args.region
    access_key = args.access_key
    secret_key = args.secret_key
    bucket_name = args.vector_bucket_name
    index_name = args.index_name
    previous_config = None
    if args.index_model:
        previous_config = apply_bucket_index_model(bucket_name, args.index_model)
        log("信息", "已按脚本参数临时写入 bucket 索引模型配置", {"vectorBucketName": bucket_name, "indexModel": args.index_model})
    index_model = bucket_index_model(bucket_name)

    client = boto3.client(
        "s3vectors",
        region_name=region,
        endpoint_url=endpoint_url,
        aws_access_key_id=access_key,
        aws_secret_access_key=secret_key,
        config=Config(signature_version="v4"),
    )

    vectors = [
        {
            "key": "v1",
            "data": {"float32": [0.10, 0.20, 0.30, 0.40]},
            "metadata": {"tenant": "t1", "color": "red"},
        },
        {
            "key": "v2",
            "data": {"float32": [0.91, 0.81, 0.71, 0.61]},
            "metadata": {"tenant": "t2", "color": "blue"},
        },
    ]

    bucket_created = False
    index_created = False

    try:
        log(
            "信息",
            "开始执行 boto3 VectorBucket 功能测试",
            {
                "endpoint": endpoint_url,
                "region": region,
                "vectorBucketName": bucket_name,
                "indexName": index_name,
                "indexModel": index_model,
            },
        )

        resp = run_step(
            "创建 vector bucket",
            lambda: client.create_vector_bucket(vectorBucketName=bucket_name),
        )
        bucket_created = True

        resp = run_step(
            "创建 index",
            lambda: client.create_index(
                vectorBucketName=bucket_name,
                indexName=index_name,
                dataType="float32",
                dimension=4,
                distanceMetric="cosine",
            ),
        )
        index_created = True

        run_step(
            "写入 2 条向量",
            lambda: client.put_vectors(
                vectorBucketName=bucket_name,
                indexName=index_name,
                vectors=vectors,
            ),
        )
        log("信息", "写入完成", {"inserted": [item["key"] for item in vectors]})

        resp = run_step(
            "删除前查询向量",
            lambda: client.query_vectors(
                vectorBucketName=bucket_name,
                indexName=index_name,
                queryVector={"float32": [0.10, 0.20, 0.30, 0.40]},
                topK=2,
                returnDistance=True,
                returnMetadata=True,
            ),
        )
        returned = resp.get("vectors", [])
        assert returned, "expected at least one query result"
        assert returned[0]["key"] == "v1", f"expected top result v1, got {returned[0].get('key')}"
        assert returned[0].get("metadata", {}).get("tenant") == "t1", "expected metadata tenant=t1"
        log("成功", "删除前查询结果校验通过")

        run_step(
            "删除向量 v1",
            lambda: client.delete_vectors(
                vectorBucketName=bucket_name,
                indexName=index_name,
                keys=["v1"],
            ),
        )
        log("信息", "删除请求已提交", {"deleted": ["v1"]})

        resp = run_step(
            "删除后查询向量",
            lambda: client.query_vectors(
                vectorBucketName=bucket_name,
                indexName=index_name,
                queryVector={"float32": [0.10, 0.20, 0.30, 0.40]},
                topK=2,
                returnDistance=True,
                returnMetadata=True,
            ),
        )
        remaining_keys = [item.get("key") for item in resp.get("vectors", [])]
        assert "v1" not in remaining_keys, f"expected v1 to be deleted, got {remaining_keys}"
        log("成功", "删除后查询结果校验通过", {"remainingKeys": remaining_keys})

        run_step(
            "删除 index",
            lambda: client.delete_index(vectorBucketName=bucket_name, indexName=index_name),
        )
        index_created = False

        run_step(
            "删除 vector bucket",
            lambda: client.delete_vector_bucket(vectorBucketName=bucket_name),
        )
        bucket_created = False

        print("\n[成功] boto3 VectorBucket 功能测试全部通过。", flush=True)
        return 0
    finally:
        if index_created:
            try:
                log("信息", "清理残留 index", {"indexName": index_name})
                client.delete_index(vectorBucketName=bucket_name, indexName=index_name)
            except Exception:
                log("失败", "清理残留 index 失败，忽略继续退出")
        if bucket_created:
            try:
                log("信息", "清理残留 vector bucket", {"vectorBucketName": bucket_name})
                client.delete_vector_bucket(vectorBucketName=bucket_name)
            except Exception:
                log("失败", "清理残留 vector bucket 失败，忽略继续退出")
        if previous_config is not None and not args.keep_config:
            restore_bucket_policy_config(previous_config)
            log("信息", "已恢复原始 bucket 索引模型配置")


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except AssertionError as exc:
        print(f"[失败] 断言失败: {exc}", file=sys.stderr, flush=True)
        raise SystemExit(1)
    except Exception as exc:
        print(f"[失败] 脚本异常退出: {exc}", file=sys.stderr, flush=True)
        raise SystemExit(1)
