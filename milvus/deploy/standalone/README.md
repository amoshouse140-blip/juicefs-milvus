# JuiceFS To Milvus Standalone Deployment

## Prerequisites

- A reachable JuiceFS metadata backend
- A local data directory for JuiceFS, for example `/var/lib/juicefs-data`
- A local state directory for VectorBucket SQLite metadata, for example `/var/lib/vectorbucket`
- Docker and Docker Compose
- `mc`, `curl`, and `nc`

## Recommended local workflow

Use the repository scripts instead of running the individual setup commands by hand:

```bash
./scripts/start-vectorbucket-standalone.sh
python3 scripts/test-vectorbucket-boto3.py
./scripts/stop-vectorbucket-standalone.sh
```

Ports used by this setup:

- `9000` for JuiceFS S3 Gateway
- `2379` for etcd
- `19530` for Milvus gRPC
- `9091` for Milvus health endpoint

## Local directories

```bash
sudo mkdir -p /var/lib/juicefs-data /var/lib/vectorbucket
```

## Start JuiceFS and S3 Gateway

```bash
export MINIO_ROOT_USER=admin
export MINIO_ROOT_PASSWORD=12345678

juicefs format \
  --storage file \
  --bucket /var/lib/juicefs-data \
  redis://127.0.0.1:6379/1 \
  jfs-milvus

juicefs gateway \
  redis://127.0.0.1:6379/1 \
  127.0.0.1:9000
```

## Create the Milvus backend storage bucket on JuiceFS

```bash
mc alias set jfs http://127.0.0.1:9000 admin 12345678
mc mb jfs/milvus-storage
mc ls jfs
```

`milvus-storage` is only Milvus' backend object storage bucket. It is not the same thing as the business-facing Vector Bucket names exposed by the API shim.

## Start Milvus Standalone

```bash
cd milvus/deploy/standalone
cp .env.example .env
docker compose up -d
docker compose ps
```

## Verify Milvus

```bash
nc -z 127.0.0.1 19530
docker compose logs standalone --tail=200
curl -f http://127.0.0.1:9091/healthz
```

## VectorBucket runtime environment on JuiceFS

```bash
export MINIO_ROOT_USER=admin
export MINIO_ROOT_PASSWORD=12345678
export VB_SQLITE_PATH=/var/lib/vectorbucket/metadata.db
export VB_MILVUS_ADDR=127.0.0.1:19530
```

VectorBucket index model is now driven by the repository config file:

- [configs/vectorbucket.json](/Users/xty/code/juicefs-milvus/configs/vectorbucket.json)

Example:

```json
{
  "bucketIndexPolicies": {
    "bench-ivf": {
      "indexType": "ivf_sq8"
    },
    "bench-hnsw": {
      "indexType": "hnsw",
      "maxVectors": 200000,
      "hnswM": 16,
      "hnswEfConstruction": 200
    },
    "bench-diskann": {
      "indexType": "diskann",
      "maxVectors": 300000,
      "diskannSearchList": 100
    }
  }
}
```

Supported `indexType` values:

- `ivf_sq8`: on-demand load + LRU/TTL
- `hnsw`: pinned/load-resident
- `diskann`: pinned/load-resident

The public S3 Vectors API does not change. `boto3` still uses the standard `create_vector_bucket/create_index/...` calls; JuiceFS decides the backend index model from the bucket policy at index creation time.

## Build JuiceFS with in-process Milvus integration

```bash
cd juicefs-1.3.0-rc1
GOWORK=off go build -tags milvus_integration -o ./juicefs .
```

## Start the JuiceFS Gateway

```bash
cd juicefs-1.3.0-rc1
./juicefs gateway redis://127.0.0.1:6379/1 127.0.0.1:9000
```

## End-to-end boto3 smoke test

```bash
python3 scripts/test-vectorbucket-boto3.py
```

## Vector Bucket benchmark

Install benchmark dependencies:

```bash
python3 -m pip install --user boto3 -r scripts/requirements-benchmark.txt
```

Run the three supported profiles:

```bash
python3 scripts/test-vectorbucket-benchmark.py --profile 10k
python3 scripts/test-vectorbucket-benchmark.py --profile 100k
python3 scripts/test-vectorbucket-benchmark.py --profile 1m
```

To benchmark an `hnsw` bucket, target a bucket configured with `"indexType": "hnsw"` in `configs/vectorbucket.json`:

```bash
VECTOR_BUCKET_NAME=bench-hnsw python3 scripts/test-vectorbucket-boto3.py

python3 scripts/test-vectorbucket-benchmark.py \
  --profile 10k \
  --vector-bucket-name bench-hnsw \
  --cleanup
```

To benchmark an `ivf_sq8` bucket explicitly:

```bash
python3 scripts/test-vectorbucket-benchmark.py \
  --profile 10k \
  --vector-bucket-name bench-ivf \
  --cleanup
```

To benchmark a `diskann` bucket:

```bash
VECTOR_BUCKET_NAME=bench-diskann python3 scripts/test-vectorbucket-boto3.py

python3 scripts/test-vectorbucket-benchmark.py \
  --profile 10k \
  --vector-bucket-name bench-diskann \
  --cleanup
```

## Manual hot switch

Phase 3a adds an internal management action for manual index model switching. This is not part of the standard boto3 S3 Vectors API.

Example:

```bash
python3 scripts/change-index-model.py \
  --vector-bucket-name bench-ivf \
  --index-name main \
  --target-model hnsw
```

Behavior:

- writes are rejected with `503` while migration is in progress
- queries continue serving from the old physical collection until cutover
- the script polls `.runtime/vectorbucket/metadata.db` until migration finishes or fails

Reports are written to `.runtime/benchmarks/`.
The script defaults to `--batch-size 20` because the `DBpedia OpenAI 1M` dataset uses 2048-dimensional vectors and larger JSON batches are significantly heavier on the local Gateway and VectorBucket path.
For `milvus_integration`, you can usually start from `--batch-size 100` or `200` and tune from there.

## Verify Milvus data lands on JuiceFS

```bash
mc tree jfs/milvus-storage
mc find jfs/milvus-storage
```

Success means:

- Milvus gRPC is reachable on `:19530`
- JuiceFS gateway is reachable on `:9000`
- VectorBucket API requests return success
- `QueryVectors` returns a result
- Milvus backend objects appear under `jfs/milvus-storage/files`

## Historical note

`vectorbucket-milvus-bridge` remains in the repository as a fallback and comparison path, but the preferred deployment path is now `milvus_integration`, where JuiceFS talks to Milvus directly in-process.
