#!/usr/bin/env python3
"""
Smoke test for boto3 S3 Vectors compatibility.
For scale and performance testing, use test-vectorbucket-benchmark.py.
"""

import json
import os
import sys
import uuid

import boto3
from botocore.config import Config


def env(name: str, default: str) -> str:
    value = os.getenv(name)
    return value if value else default


def log(step: str, payload=None) -> None:
    print(step)
    if payload is not None:
        print(json.dumps(payload, ensure_ascii=True, default=str, indent=2))


def main() -> int:
    endpoint_url = env("S3VECTORS_ENDPOINT_URL", "http://127.0.0.1:9000")
    region = env("AWS_REGION", "us-east-1")
    access_key = env("AWS_ACCESS_KEY_ID", "admin")
    secret_key = env("AWS_SECRET_ACCESS_KEY", "12345678")
    bucket_name = env("VECTOR_BUCKET_NAME", f"boto3-demo-{uuid.uuid4().hex[:8]}")
    index_name = env("VECTOR_INDEX_NAME", "main")

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
        resp = client.create_vector_bucket(vectorBucketName=bucket_name)
        bucket_created = True
        log("1. create_vector_bucket", resp)

        resp = client.create_index(
            vectorBucketName=bucket_name,
            indexName=index_name,
            dataType="float32",
            dimension=4,
            distanceMetric="cosine",
        )
        index_created = True
        log("2. create_index", resp)

        client.put_vectors(
            vectorBucketName=bucket_name,
            indexName=index_name,
            vectors=vectors,
        )
        log("3. put_vectors", {"inserted": [item["key"] for item in vectors]})

        resp = client.query_vectors(
            vectorBucketName=bucket_name,
            indexName=index_name,
            queryVector={"float32": [0.10, 0.20, 0.30, 0.40]},
            topK=2,
            returnDistance=True,
            returnMetadata=True,
        )
        log("4. query_vectors before delete", resp)
        returned = resp.get("vectors", [])
        assert returned, "expected at least one query result"
        assert returned[0]["key"] == "v1", f"expected top result v1, got {returned[0].get('key')}"
        assert returned[0].get("metadata", {}).get("tenant") == "t1", "expected metadata tenant=t1"

        client.delete_vectors(
            vectorBucketName=bucket_name,
            indexName=index_name,
            keys=["v1"],
        )
        log("5. delete_vectors", {"deleted": ["v1"]})

        resp = client.query_vectors(
            vectorBucketName=bucket_name,
            indexName=index_name,
            queryVector={"float32": [0.10, 0.20, 0.30, 0.40]},
            topK=2,
            returnDistance=True,
            returnMetadata=True,
        )
        log("6. query_vectors after delete", resp)
        remaining_keys = [item.get("key") for item in resp.get("vectors", [])]
        assert "v1" not in remaining_keys, f"expected v1 to be deleted, got {remaining_keys}"

        client.delete_index(vectorBucketName=bucket_name, indexName=index_name)
        index_created = False
        log("7. delete_index", {"indexName": index_name})

        client.delete_vector_bucket(vectorBucketName=bucket_name)
        bucket_created = False
        log("8. delete_vector_bucket", {"vectorBucketName": bucket_name})

        print("\nBoto3 VectorBucket test passed.")
        return 0
    finally:
        if index_created:
            try:
                client.delete_index(vectorBucketName=bucket_name, indexName=index_name)
            except Exception:
                pass
        if bucket_created:
            try:
                client.delete_vector_bucket(vectorBucketName=bucket_name)
            except Exception:
                pass


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except AssertionError as exc:
        print(f"ASSERTION FAILED: {exc}", file=sys.stderr)
        raise SystemExit(1)
