# JuiceFS To Milvus Standalone Deployment Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在单机环境中部署并打通 `JuiceFS S3 Gateway -> Milvus Standalone`，让 Milvus 使用 JuiceFS bucket 作为底层对象存储，并为后续 Vector Bucket 真实写查联调准备好运行环境。

**Architecture:** JuiceFS 提供两层能力：一是通过自身 metadata + 本地 data 目录提供文件系统与 S3 Gateway；二是通过一个普通 S3 bucket 作为 Milvus 的底层 object storage。Milvus 使用 Standalone 形态运行，保留 `etcd` 作为元数据协调存储，关闭内置 MinIO，改为访问 JuiceFS Gateway。完成环境后，再单独恢复 JuiceFS 分支中的 `milvus_integration` 构建链，验证 Vector Bucket 请求能真正落到 Milvus。

**Tech Stack:** JuiceFS, JuiceFS S3 Gateway, Docker Compose, Milvus Standalone, etcd, S3-compatible object storage, Go build tags (`milvus_integration`)

---

## File Structure

本计划默认落地在 `juicefs-milvus` 仓库根目录，部署文件与说明集中到 `milvus/docs/superpowers/` 旁边的执行目录里，避免污染 JuiceFS 源码主路径。

### 计划涉及的文件

- Create: `milvus/deploy/standalone/docker-compose.yml`
- Create: `milvus/deploy/standalone/milvus.yaml`
- Create: `milvus/deploy/standalone/.env.example`
- Create: `milvus/deploy/standalone/README.md`
- Create: `milvus/scripts/test-vectorbucket.sh`
- Modify: `juicefs-1.3.0-rc1/go.mod`
- Modify: `juicefs-1.3.0-rc1/go.sum`
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/adapter/milvus_adapter_integration.go` only if Milvus client API version drift requires it

### Runtime Topology

- JuiceFS metadata backend: 你已有的一套 meta 服务
- JuiceFS data backend: 本地目录，例如 `/var/lib/juicefs-data`
- JuiceFS S3 Gateway: `127.0.0.1:9000`
- Milvus object storage bucket on JuiceFS S3: `milvus-storage`
- etcd: `127.0.0.1:2379`
- Milvus Standalone gRPC: `127.0.0.1:19530`
- VectorBucket SQLite metadata: `/var/lib/vectorbucket/metadata.db`

---

### Task 1: Prepare JuiceFS Runtime

**Files:**
- Create: `milvus/deploy/standalone/README.md`

- [ ] **Step 1: Document the required local directories and ports**

```markdown
## Local prerequisites

- JuiceFS metadata backend is reachable
- Local data directory exists: `/var/lib/juicefs-data`
- Local VectorBucket state directory exists: `/var/lib/vectorbucket`
- Ports are free:
  - `9000` for JuiceFS S3 Gateway
  - `2379` for etcd
  - `19530` for Milvus gRPC
  - `9091` for Milvus health/metrics if enabled
```

- [ ] **Step 2: Create the local directories**

Run: `sudo mkdir -p /var/lib/juicefs-data /var/lib/vectorbucket`
Expected: command exits 0

- [ ] **Step 3: Record the JuiceFS format and gateway command in the README**

```markdown
## JuiceFS bootstrap

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
```

- [ ] **Step 4: Verify the README exists and is readable**

Run: `sed -n '1,120p' milvus/deploy/standalone/README.md`
Expected: shows the prerequisites and JuiceFS bootstrap section

---

### Task 2: Provision the Milvus Storage Bucket on JuiceFS S3

**Files:**
- Modify: `milvus/deploy/standalone/README.md`

- [ ] **Step 1: Add the bucket creation command to the README**

```markdown
## Create Milvus storage bucket on JuiceFS S3

```bash
mc alias set jfs http://127.0.0.1:9000 admin 12345678
mc mb jfs/milvus-storage
mc ls jfs
```
```

- [ ] **Step 2: Verify the bucket command sequence manually**

Run: `mc ls jfs`
Expected: output contains `milvus-storage`

- [ ] **Step 3: Note the purpose of the bucket**

```markdown
`milvus-storage` is Milvus' backend object storage bucket for segments, indexes, and logs.
It is not the same thing as the business-facing Vector Bucket API bucket names.
```

---

### Task 3: Create Milvus Standalone Compose Files

**Files:**
- Create: `milvus/deploy/standalone/docker-compose.yml`
- Create: `milvus/deploy/standalone/milvus.yaml`
- Create: `milvus/deploy/standalone/.env.example`

- [ ] **Step 1: Write the environment template**

```dotenv
# milvus/deploy/standalone/.env.example
ETCD_ENDPOINTS=etcd:2379
MILVUS_S3_ADDRESS=host.docker.internal:9000
MILVUS_S3_ACCESS_KEY=admin
MILVUS_S3_SECRET_KEY=12345678
MILVUS_S3_BUCKET_NAME=milvus-storage
MILVUS_S3_USE_SSL=false
MILVUS_S3_ROOT_PATH=files
```

- [ ] **Step 2: Write the Milvus configuration file**

```yaml
# milvus/deploy/standalone/milvus.yaml
etcd:
  endpoints:
    - etcd:2379

minio:
  address: host.docker.internal
  port: 9000
  accessKeyID: admin
  secretAccessKey: 12345678
  useSSL: false
  bucketName: milvus-storage
  rootPath: files
  useIAM: false
  cloudProvider: aws

common:
  storageType: remote
```

- [ ] **Step 3: Write the Docker Compose file**

```yaml
# milvus/deploy/standalone/docker-compose.yml
services:
  etcd:
    image: quay.io/coreos/etcd:v3.5.5
    command:
      - /usr/local/bin/etcd
      - --advertise-client-urls=http://0.0.0.0:2379
      - --listen-client-urls=http://0.0.0.0:2379
      - --data-dir=/etcd
    ports:
      - "2379:2379"
    volumes:
      - etcd_data:/etcd

  milvus:
    image: milvusdb/milvus:v2.4.4
    command: ["milvus", "run", "standalone"]
    depends_on:
      - etcd
    ports:
      - "19530:19530"
      - "9091:9091"
    volumes:
      - ./milvus.yaml:/milvus/configs/milvus.yaml
    extra_hosts:
      - "host.docker.internal:host-gateway"

volumes:
  etcd_data:
```

- [ ] **Step 4: Verify the compose file parses**

Run: `docker compose -f milvus/deploy/standalone/docker-compose.yml config`
Expected: resolved YAML is printed without errors

---

### Task 4: Bring Up Milvus Standalone

**Files:**
- Modify: `milvus/deploy/standalone/README.md`

- [ ] **Step 1: Add the startup commands to the README**

```markdown
## Start Milvus Standalone

```bash
cd milvus/deploy/standalone
docker compose up -d
docker compose ps
```
```

- [ ] **Step 2: Add the health verification commands**

```markdown
## Verify Milvus

```bash
nc -z 127.0.0.1 19530
docker compose logs milvus --tail=200
```
```

- [ ] **Step 3: Verify etcd and Milvus are running**

Run: `docker compose -f milvus/deploy/standalone/docker-compose.yml ps`
Expected: both `etcd` and `milvus` are `Up`

---

### Task 5: Restore the Real Milvus Integration Build

**Files:**
- Modify: `juicefs-1.3.0-rc1/go.mod`
- Modify: `juicefs-1.3.0-rc1/go.sum`
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/adapter/milvus_adapter_integration.go` only if needed

- [ ] **Step 1: Write the failing integration build command**

Run: `cd juicefs-1.3.0-rc1 && GOWORK=off go build -tags milvus_integration ./cmd/juicefs`
Expected: FAIL because Milvus client modules are not present in `go.mod`

- [ ] **Step 2: Add the Milvus client dependencies back to `go.mod`**

```go
require (
    github.com/milvus-io/milvus/client/v2 v2.4.2
    github.com/milvus-io/milvus/pkg/v2 v2.4.2
)
```

- [ ] **Step 3: Resolve module graph**

Run: `cd juicefs-1.3.0-rc1 && GOWORK=off go mod tidy`
Expected: succeeds without replacing the local MinIO path

- [ ] **Step 4: Re-run the integration build**

Run: `cd juicefs-1.3.0-rc1 && GOWORK=off go build -tags milvus_integration ./cmd/juicefs`
Expected: PASS

- [ ] **Step 5: If the build fails on Milvus client API drift, patch the adapter minimally**

```go
// Only update the exact changed method signatures or import paths.
// Do not refactor unrelated vectorbucket logic.
```

---

### Task 6: Start the Real JuiceFS Gateway With VectorBucket Enabled

**Files:**
- Modify: `milvus/deploy/standalone/README.md`

- [ ] **Step 1: Add the environment variables for VectorBucket runtime**

```markdown
## VectorBucket runtime environment

```bash
export MINIO_ROOT_USER=admin
export MINIO_ROOT_PASSWORD=12345678
export VB_SQLITE_PATH=/var/lib/vectorbucket/metadata.db
export VB_MILVUS_ADDR=127.0.0.1:19530
```
```

- [ ] **Step 2: Add the build and launch commands**

```markdown
## Build and run JuiceFS with real Milvus integration

```bash
cd juicefs-1.3.0-rc1
GOWORK=off go build -tags milvus_integration -o ./juicefs ./cmd/juicefs

./juicefs gateway redis://127.0.0.1:6379/1 127.0.0.1:9000
```
```

- [ ] **Step 3: Verify the gateway is listening**

Run: `nc -z 127.0.0.1 9000`
Expected: exit status 0

---

### Task 7: Add an End-To-End VectorBucket Smoke Test Script

**Files:**
- Create: `milvus/scripts/test-vectorbucket.sh`
- Modify: `milvus/deploy/standalone/README.md`

- [ ] **Step 1: Create the smoke test script**

```bash
#!/usr/bin/env bash
set -euo pipefail

base_url="${1:-http://127.0.0.1:9000}"

curl -sS -X POST "${base_url}/CreateVectorBucket" \
  -H 'Content-Type: application/json' \
  -H 'X-Amz-Account-Id: 123456789012' \
  -d '{"vectorBucketName":"demo-bucket"}'

curl -sS -X POST "${base_url}/CreateIndex" \
  -H 'Content-Type: application/json' \
  -H 'X-Amz-Account-Id: 123456789012' \
  -d '{"vectorBucketName":"demo-bucket","indexName":"demo-index","dataType":"float32","dimension":4,"distanceMetric":"cosine"}'

curl -sS -X POST "${base_url}/PutVectors" \
  -H 'Content-Type: application/json' \
  -H 'X-Amz-Account-Id: 123456789012' \
  -d '{"vectorBucketName":"demo-bucket","indexName":"demo-index","vectors":[{"key":"v1","data":{"float32":[0.1,0.2,0.3,0.4]},"metadata":{"tenant":"t1"}}]}'

curl -sS -X POST "${base_url}/QueryVectors" \
  -H 'Content-Type: application/json' \
  -H 'X-Amz-Account-Id: 123456789012' \
  -d '{"vectorBucketName":"demo-bucket","indexName":"demo-index","queryVector":{"float32":[0.1,0.2,0.3,0.4]},"topK":5,"returnDistance":true,"returnMetadata":true}'

curl -sS -X POST "${base_url}/DeleteVectors" \
  -H 'Content-Type: application/json' \
  -H 'X-Amz-Account-Id: 123456789012' \
  -d '{"vectorBucketName":"demo-bucket","indexName":"demo-index","keys":["v1"]}'

curl -sS -X POST "${base_url}/DeleteIndex" \
  -H 'Content-Type: application/json' \
  -H 'X-Amz-Account-Id: 123456789012' \
  -d '{"vectorBucketName":"demo-bucket","indexName":"demo-index"}'

curl -sS -X POST "${base_url}/DeleteVectorBucket" \
  -H 'Content-Type: application/json' \
  -H 'X-Amz-Account-Id: 123456789012' \
  -d '{"vectorBucketName":"demo-bucket"}'
```

- [ ] **Step 2: Make the script executable**

Run: `chmod +x milvus/scripts/test-vectorbucket.sh`
Expected: exit status 0

- [ ] **Step 3: Add the smoke test invocation to the README**

```markdown
## End-to-end smoke test

```bash
./milvus/scripts/test-vectorbucket.sh http://127.0.0.1:9000
```
```

- [ ] **Step 4: Run the smoke test**

Run: `./milvus/scripts/test-vectorbucket.sh http://127.0.0.1:9000`
Expected: all requests return 2xx JSON responses and `QueryVectors` returns at least one vector

---

### Task 8: Verify Milvus Is Actually Writing to JuiceFS

**Files:**
- Modify: `milvus/deploy/standalone/README.md`

- [ ] **Step 1: Add the storage verification commands**

```markdown
## Verify Milvus object data is stored on JuiceFS

```bash
mc find jfs/milvus-storage
mc tree jfs/milvus-storage
```
```

- [ ] **Step 2: Run the verification after the smoke test**

Run: `mc tree jfs/milvus-storage`
Expected: output contains Milvus-created objects under the configured root path, such as `files/`

- [ ] **Step 3: Record the success criteria in the README**

```markdown
Success means:
- Milvus gRPC is reachable on `:19530`
- JuiceFS gateway is reachable on `:9000`
- VectorBucket API calls return success
- `QueryVectors` returns a result
- Milvus backend objects appear in `jfs/milvus-storage`
```

---

### Task 9: Final Verification

**Files:**
- Modify: `milvus/deploy/standalone/README.md`

- [ ] **Step 1: Run the relevant JuiceFS tests**

Run: `cd juicefs-1.3.0-rc1 && GOWORK=off go test -mod=mod -count=1 ./pkg/gateway/vectorbucket/... ./pkg/gateway/...`
Expected: PASS

- [ ] **Step 2: Run the local MinIO shim tests**

Run: `cd .worktrees/juicedata-minio && GOWORK=off go test -mod=mod -count=1 -vet=off ./cmd -run 'Test(CreateVectorBucket|PutVectors|QueryVectors)Handler'`
Expected: PASS

- [ ] **Step 3: Re-run the standalone smoke test**

Run: `./milvus/scripts/test-vectorbucket.sh http://127.0.0.1:9000`
Expected: PASS

- [ ] **Step 4: Commit the deployment artifacts**

```bash
git add \
  milvus/deploy/standalone/docker-compose.yml \
  milvus/deploy/standalone/milvus.yaml \
  milvus/deploy/standalone/.env.example \
  milvus/deploy/standalone/README.md \
  milvus/scripts/test-vectorbucket.sh \
  milvus/docs/superpowers/plans/2026-04-16-juicefs-milvus-standalone-deployment.md

git commit -m "docs: add executable standalone deployment plan for juicefs to milvus"
```

---

## Self-Review

- Spec coverage: 覆盖了 JuiceFS runtime、Milvus Standalone、JuiceFS bucket 作为 Milvus object storage、`milvus_integration` 构建恢复、以及端到端验证。
- Placeholder scan: 没有使用 TBD/TODO；每个任务都包含了文件、命令和预期结果。
- Type consistency: 计划中的接口名和路径与当前分支里的 `VectorBucket`、`milvus_integration`、`CreateVectorBucket/CreateIndex/PutVectors/QueryVectors` 命名一致。

