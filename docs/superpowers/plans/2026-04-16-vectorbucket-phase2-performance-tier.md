# VectorBucket Phase 2 Performance Tier Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在不改变 AWS S3 Vectors 标准 API 的前提下，为 Vector Bucket 增加 bucket 级索引模型配置：由 JuiceFS 服务端配置文件决定 bucket 默认使用 `ivf_sq8 / hnsw / diskann` 中哪种模型，创建 index 时自动落到对应的 Milvus 索引与 load 策略。

**Architecture:** 对外继续保持 `CreateVectorBucket / CreateIndex / PutVectors / QueryVectors / DeleteVectors` 这组标准 S3 Vectors 动作 API，boto3 无需改模型。索引模型判定不进入公开协议字段，而是在 JuiceFS 内部通过 bucket 级配置文件完成：`CreateIndex` 读取 bucket 名，查 `configs/vectorbucket.json` 中的 `bucketIndexPolicies` 决定该 index 的 `indexType`，并将最终模型固化到 metadata；后续写入、查询、删除都只读 metadata，不依赖实时配置漂移。`ivf_sq8` 对应按需 load + LRU/TTL，`hnsw` 和 `diskann` 对应 pinned/load 常驻路径。

**Tech Stack:** Go, JuiceFS gateway, juicedata/minio fork, Milvus standalone, SQLite metadata store, Prometheus metrics, shell deployment scripts.

---

## 文件结构与职责

**JuiceFS 主实现路径**
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/config/config.go`
  - 增加 bucket 级索引模型配置、Pinned 模型预算和算法默认参数。
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/metadata/models.go`
  - 让 `LogicalCollection` 记录最终 `IndexType`, `Tier`, `MaxVectors`, `Pinned`。
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/metadata/sqlite_store.go`
  - 持久化 tier/pinned/maxVectors 字段。
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/router/namespace_router.go`
  - 根据 tier 生成 `vb_` / `vbh_` 两种 physical collection 前缀。
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/adapter/types.go`
  - 抽象 tier-aware 索引规格。
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/adapter/milvus_adapter_integration.go`
  - `ivf_sq8 / hnsw / diskann` 三种模型分别映射到对应 Milvus 索引。
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/controller/load_controller.go`
  - 增加 pinned collection 语义。
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/bootstrap.go`
  - 启动时恢复性能档 collection 的 load + pin。
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/object_service.go`
  - 在 `CreateIndex` 时根据 bucket 配置选择 tier，并做预算/上限校验；删除时 unpin。
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/query_service.go`
  - 保持查询 API 不变，但保证 pinned collection 不被 LRU/TTL 回收。
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/quota/quota.go`
  - 新增性能档预算与 maxVectors 检查。
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/metrics/metrics.go`
  - 增加 tier 维度指标。

**测试路径**
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/config/config_test.go`
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/metadata/sqlite_store_test.go`
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/router/namespace_router_test.go`
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/adapter/milvus_adapter_integration_test.go`
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/controller/load_controller_test.go`
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/service_test.go`
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/integration/gateway_contract_test.go`

**部署与文档**
- Modify: `scripts/start-vectorbucket-standalone.sh`
- Modify: `scripts/stop-vectorbucket-standalone.sh`
- Modify: `scripts/test-vectorbucket-boto3.py`
- Modify: `scripts/test-vectorbucket-benchmark.py`
- Modify: `milvus/deploy/standalone/README.md`
- Modify: `docs/vector_bucket_iteration_plan_v2_3.md`

---

### Task 1: 配置层支持 bucket -> indexType 映射

**Files:**
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/config/config.go`
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/config/config_test.go`

- [ ] **Step 1: 写 failing test，要求支持配置 bucket 级索引模型**

```go
func TestConfigLoadsBucketIndexPolicies(t *testing.T) {
	cfg := LoadConfig()
	assert.Equal(t, "hnsw", cfg.PolicyForBucket("kb-hot").IndexType)
	assert.Equal(t, "ivf_sq8", cfg.PolicyForBucket("kb-cold").IndexType)
	assert.Equal(t, "diskann", cfg.PolicyForBucket("kb-disk").IndexType)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd juicefs-1.3.0-rc1 && GOWORK=off go test -count=1 ./pkg/gateway/vectorbucket/config/... -run TestConfigLoadsBucketIndexPolicies`
Expected: FAIL

- [ ] **Step 3: 增加配置项与查询方法**

```go
type Config struct {
	// existing fields
	BucketIndexPolicies  map[string]BucketIndexPolicy
	PerformanceBudgetMB  int
	StandardBudgetMB     int
	PerformanceMaxVectors int64
	HNSWM                int
	HNSWEFConstruction   int
}

func (c Config) PolicyForBucket(name string) BucketIndexPolicy {
	// ...
}
```

配置文件：
- `configs/vectorbucket.json`
- `bucketIndexPolicies.<bucket>.indexType = ivf_sq8 | hnsw | diskann`
- `bucketIndexPolicies.<bucket>.maxVectors = ...`
- `bucketIndexPolicies.<bucket>.hnswM = ...`
- `bucketIndexPolicies.<bucket>.hnswEfConstruction = ...`
- `bucketIndexPolicies.<bucket>.diskannSearchList = ...`

- [ ] **Step 4: 运行 config 测试确认通过**
- [ ] **Step 5: 提交**

### Task 2: metadata 持久化最终 tier / pinned / maxVectors

**Files:**
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/metadata/models.go`
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/metadata/sqlite_store.go`
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/metadata/sqlite_store_test.go`

- [ ] **Step 1: 写 failing test，要求 CreateCollection 后能读回 tier/maxVectors/pinned**
- [ ] **Step 2: 运行测试确认失败**
- [ ] **Step 3: 给 `LogicalCollection` 增加字段**

```go
type LogicalCollection struct {
	// existing fields
	Tier       string
	MaxVectors int64
	Pinned     bool
}
```

- [ ] **Step 4: 扩展 SQLite schema、scan、insert/update 逻辑**
- [ ] **Step 5: 运行 metadata 测试确认通过**
- [ ] **Step 6: 提交**

### Task 3: 路由和物理命名支持索引模型映射后的 tier

**Files:**
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/router/namespace_router.go`
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/router/namespace_router_test.go`

- [ ] **Step 1: 写 failing test，要求 pinned 模型生成 `vbh_` 前缀**
- [ ] **Step 2: 运行测试确认失败**
- [ ] **Step 3: 实现按 tier 命名函数**

```go
func PhysicalCollectionNameForTier(tier, bucketID, collectionID string) string {
	prefix := "vb"
	if tier == "performance" {
		prefix = "vbh"
	}
	return fmt.Sprintf("%s_%s_%s", prefix, sanitizeCollectionToken(bucketID), sanitizeCollectionToken(collectionID))
}
```

- [ ] **Step 4: 保持默认标准档兼容旧 `vb_` 前缀**
- [ ] **Step 5: 运行 router 测试确认通过**
- [ ] **Step 6: 提交**

### Task 4: adapter 支持 `ivf_sq8 / hnsw / diskann`

**Files:**
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/adapter/types.go`
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/adapter/milvus_adapter_integration.go`
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/adapter/milvus_adapter_integration_test.go`

- [ ] **Step 1: 写 failing test，要求 indexType 不同则索引类型不同**
- [ ] **Step 2: 运行测试确认失败**
- [ ] **Step 3: 定义 tier-aware 索引规格**

```go
type IndexSpec struct {
	Tier               string
	VectorCount        int64
	Metric             string
	HNSWM              int
	HNSWEFConstruction int
}
```

- [ ] **Step 4: 按 tier 创建索引**

```go
switch spec.IndexType {
case "hnsw":
	idx := milvusclient.NewCreateIndexOption(name, "vector", index.NewHNSWIndex(metricTypeFromString(spec.Metric), spec.HNSWM, spec.HNSWEFConstruction))
case "diskann":
	idx := milvusclient.NewCreateIndexOption(name, "vector", index.NewDiskANNIndex(metricTypeFromString(spec.Metric)))
default:
	idx := milvusclient.NewCreateIndexOption(name, "vector", index.NewIvfSQ8Index(metricTypeFromString(spec.Metric), computeNlist(spec.VectorCount)))
}
```

- [ ] **Step 5: `ivf_sq8` 继续走按需 load；`hnsw / diskann` 走 pinned**
- [ ] **Step 6: 跑 adapter 测试**
- [ ] **Step 7: 提交**

### Task 5: LoadController 增加 pinned collection 语义

**Files:**
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/controller/load_controller.go`
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/controller/load_controller_test.go`

- [ ] **Step 1: 写 failing test，要求 pinned collection 不参与 TTL/LRU eviction**
- [ ] **Step 2: 运行测试确认失败**
- [ ] **Step 3: 给 `LoadEntry` 增加 `Pinned bool`**
- [ ] **Step 4: 实现 `Pin(name)`, `Unpin(name)`, `IsPinned(name)`**
- [ ] **Step 5: `RunTTLSweep` 和 `evictIfNeededLocked` 跳过 pinned**
- [ ] **Step 6: 运行 controller 测试确认通过**
- [ ] **Step 7: 提交**

### Task 6: quota 支持 pinned 模型预算与上限

**Files:**
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/quota/quota.go`
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/quota/quota_test.go`

- [ ] **Step 1: 写 failing test，要求超出 pinned 模型预算时拒绝 `hnsw / diskann` 的 CreateIndex**
- [ ] **Step 2: 运行测试确认失败**
- [ ] **Step 3: 写估算函数**

```go
func EstimatePerformanceMemMB(maxVectors int64, dim int) float64 {
	return float64(maxVectors) * float64(dim) * 4 * 1.5 / (1024 * 1024)
}
```

- [ ] **Step 4: 增加 `CanCreatePerformanceCollection` 与 `CheckPerformanceVectorLimit`**
- [ ] **Step 5: 运行 quota 测试确认通过**
- [ ] **Step 6: 提交**

### Task 7: ObjectService 在 CreateIndex 时根据 bucket 配置固化 indexType

**Files:**
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/object_service.go`
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/service_test.go`

- [ ] **Step 1: 写 failing test，覆盖以下行为**
- `hnsw` bucket 上创建 index 时，自动落 `indexType=hnsw`
- `ivf_sq8` bucket 上创建 index 时，自动落 `indexType=ivf_sq8`
- `diskann` bucket 上创建 index 时，自动落 `indexType=diskann`
- pinned 模型创建 index 时自动设定 `maxVectors`
- pinned 模型创建 index 时执行 budget 检查
- pinned 模型创建 index 后执行 `LoadCollection + Pin`
- DeleteIndex 时若为 pinned 模型则 `Unpin`

- [ ] **Step 2: 运行测试确认失败**
- [ ] **Step 3: 在 `CreateIndex` 中根据 `bucketName` 调 `cfg.PolicyForBucket(bucketName)` 决定 indexType**
- [ ] **Step 4: 生成 physical name 并把 `indexType/tier/maxVectors/pinned` 固化到 metadata**
- [ ] **Step 5: 调 indexType-aware adapter `CreateIndex`**
- [ ] **Step 6: 对 pinned 模型立即 `LoadCollection + Pin`**
- [ ] **Step 7: `PutVectors` 对 pinned 模型做 `MaxVectors` 上限检查**
- [ ] **Step 8: `DeleteIndex` 时对 pinned 模型 `Unpin`**
- [ ] **Step 9: 运行 service tests**
- [ ] **Step 10: 提交**

### Task 8: Bootstrap 启动时恢复 pinned 模型 collections

**Files:**
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/bootstrap.go`
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/runtime_test.go`

- [ ] **Step 1: 写 failing test，要求启动时把 metadata 中 `pinned=true && status=READY` 的 collection 自动 load+pin**
- [ ] **Step 2: 运行测试确认失败**
- [ ] **Step 3: 在 bootstrap 完成 adapter/controller 初始化后扫描 metadata 恢复 pinned collection**
- [ ] **Step 4: 运行 runtime tests**
- [ ] **Step 5: 提交**

### Task 9: 指标与部署脚本支持 bucket 索引模型配置

**Files:**
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/metrics/metrics.go`
- Modify: `scripts/start-vectorbucket-standalone.sh`
- Modify: `scripts/stop-vectorbucket-standalone.sh`
- Modify: `milvus/deploy/standalone/README.md`

- [ ] **Step 1: 写 failing test 或最小检查，要求部署脚本展示 `configs/vectorbucket.json`**
- [ ] **Step 2: 运行检查确认失败**
- [ ] **Step 3: 增加指标 `vb_logical_collection_count{tier=*}` 和 `vb_pinned_collection_count`**
- [ ] **Step 4: 在脚本中展示/使用 `configs/vectorbucket.json`**
- [ ] **Step 5: 更新部署 README，说明如何把某些 bucket 配成 `ivf_sq8 / hnsw / diskann`**
- [ ] **Step 6: 验证脚本语法通过**
- [ ] **Step 7: 提交**

### Task 10: boto3 / benchmark / integration 回归

**Files:**
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/integration/gateway_contract_test.go`
- Modify: `scripts/test-vectorbucket-boto3.py`
- Modify: `scripts/test-vectorbucket-benchmark.py`
- Modify: `docs/vector_bucket_iteration_plan_v2_3.md`

- [ ] **Step 1: 写 integration test，覆盖 `hnsw` bucket 在标准 API 下的 create/put/query/delete**
- [ ] **Step 2: 运行测试确认失败**
- [ ] **Step 3: 更新 boto3 smoke script，允许通过环境变量指定 bucket 名，从而验证 bucket 索引模型配置路径**
- [ ] **Step 4: 更新 benchmark script，允许指定 bucket 名并对比 `ivf_sq8 / hnsw / diskann`**
- [ ] **Step 5: 更新 V2.3 文档，明确 Phase 2 的 indexType 来源是 bucket 级服务端配置，而不是公开 API 字段**
- [ ] **Step 6: 跑完整验证**

Run:
```bash
cd juicefs-1.3.0-rc1 && GOWORK=off go test -count=1 ./pkg/gateway/vectorbucket/...
cd juicefs-1.3.0-rc1 && GOWORK=off go test -tags milvus_integration -count=1 ./pkg/gateway/vectorbucket/...
cd juicefs-1.3.0-rc1 && GOWORK=off go build -tags milvus_integration .
./scripts/start-vectorbucket-standalone.sh
VECTOR_BUCKET_NAME=bench-hnsw .runtime/venv-boto3/bin/python scripts/test-vectorbucket-boto3.py
```
Expected: 所有测试通过；标准 API 不变；bucket 自动走 `ivf_sq8 / hnsw / diskann` 中配置的模型；默认 bucket 行为保持不变。

- [ ] **Step 7: 提交**

```bash
git add juicefs-1.3.0-rc1/pkg/gateway/vectorbucket \
        scripts \
        milvus/deploy/standalone/README.md \
        docs/vector_bucket_iteration_plan_v2_3.md
git commit -m "feat(vectorbucket): add bucket-configured index model policies"
```

---

## 自检

- 对外 API 仍保持 AWS S3 Vectors 标准动作 API，不新增 `tier`/`maxVectors` 公共字段。
- Phase 2 的 tier 来源已改为 bucket 配置，不依赖 boto3 模型扩展。
- Phase 3 范围仍然排除：在线 tier 切换、自动毕业/降级、双写、一致性校验。
- 计划继续基于当前 JuiceFS gateway + Milvus integration 主路径，不回退到旧 bridge 方案。
