# JuiceFS To Milvus Standalone Deployment

## Prerequisites

- A reachable JuiceFS metadata backend
- A local data directory for JuiceFS, for example `/var/lib/juicefs-data`
- A local state directory for VectorBucket SQLite metadata, for example `/var/lib/vectorbucket`
- Docker and Docker Compose
- `mc`, `curl`, and `nc`

Ports used by this setup:

- `9000` for JuiceFS S3 Gateway
- `2379` for etcd
- `19530` for Milvus gRPC
- `19531` for the VectorBucket Milvus bridge
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

## Start the VectorBucket Milvus bridge

```bash
./milvus/scripts/run-vectorbucket-bridge.sh
```

Or run it manually:

```bash
cd milvus/bridge
export MILVUS_ADDR=127.0.0.1:19530
export BRIDGE_LISTEN_ADDR=:19531
GOWORK=off go run ./cmd/vectorbucket-milvus-bridge
```

Verify:

```bash
curl -f http://127.0.0.1:19531/healthz
```

## VectorBucket runtime environment on JuiceFS

```bash
export MINIO_ROOT_USER=admin
export MINIO_ROOT_PASSWORD=12345678
export VB_SQLITE_PATH=/var/lib/vectorbucket/metadata.db
export VB_MILVUS_BRIDGE_ADDR=http://127.0.0.1:19531
```

`VB_MILVUS_ADDR` is only needed when building JuiceFS with the optional in-process `milvus_integration` tag.

## Build JuiceFS for scheme B

```bash
cd juicefs-1.3.0-rc1
GOWORK=off go build -o ./juicefs ./cmd/juicefs
```

## Start the JuiceFS Gateway

```bash
cd juicefs-1.3.0-rc1
./juicefs gateway redis://127.0.0.1:6379/1 127.0.0.1:9000
```

## End-to-end smoke test

```bash
./milvus/scripts/test-vectorbucket.sh http://127.0.0.1:9000
```

## Vector Bucket benchmark

Install benchmark dependencies:

```bash
.runtime/venv-boto3/bin/pip install -r milvus/scripts/requirements-benchmark.txt
```

Run the three supported profiles:

```bash
.runtime/venv-boto3/bin/python milvus/scripts/test-vectorbucket-benchmark.py --profile 10k
.runtime/venv-boto3/bin/python milvus/scripts/test-vectorbucket-benchmark.py --profile 100k
.runtime/venv-boto3/bin/python milvus/scripts/test-vectorbucket-benchmark.py --profile 1m
```

Reports are written to `.runtime/benchmarks/`.
The script defaults to `--batch-size 20` because the `DBpedia OpenAI 1M` dataset uses 2048-dimensional vectors and larger JSON batches are significantly heavier on the local Gateway bridge path.

## Verify Milvus data lands on JuiceFS

```bash
mc tree jfs/milvus-storage
mc find jfs/milvus-storage
```

Success means:

- Milvus gRPC is reachable on `:19530`
- The VectorBucket Milvus bridge is reachable on `:19531`
- JuiceFS gateway is reachable on `:9000`
- VectorBucket API requests return success
- `QueryVectors` returns a result
- Milvus backend objects appear under `jfs/milvus-storage/files`
