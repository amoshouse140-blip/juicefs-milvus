# VectorBucket Phase 3a Manual Hot Switch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为运行中的 VectorBucket index 增加手动热切换能力，在切换期间禁止写入但保持查询可用，并在后台完成 `ivf_sq8 / hnsw / diskann` 之间的物理 collection 迁移。

**Architecture:** 只做 Phase 3a，不做双写。切换入口走内部管理 API，不改 boto3 标准 S3 Vectors API。切换开始后 metadata 进入迁移态，`PutVectors/DeleteVectors` 返回 `503 + Retry-After`，`QueryVectors` 继续读源 collection。后台 worker 创建目标 collection，离线扫描源 collection 并导入目标，校验成功后用 CAS 原子切换 metadata 指向，再删除源 collection 并解除维护态。

**Tech Stack:** Go, JuiceFS gateway, Milvus client v2, SQLite metadata, background worker loop, Prometheus metrics, shell deployment scripts.

---

## 文件结构与职责

**Metadata 与状态机**
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/metadata/models.go`
  - 为 `LogicalCollection` 增加迁移状态、源/目标 physical name、目标 index type、错误信息、维护态时间戳。
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/metadata/sqlite_store.go`
  - 持久化新增字段，增加 CAS 切换和迁移状态更新方法。
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/metadata/sqlite_store_test.go`
  - 覆盖迁移字段读写、CAS 切换、迁移状态更新。

**服务与迁移控制**
- Create: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/migration/service.go`
  - 提供 `RequestIndexModelChange`、`RunOnce`、`MigrateCollection` 等迁移控制逻辑。
- Create: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/migration/service_test.go`
  - 覆盖迁移状态机和 worker 行为。
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/object_service.go`
  - 写入和删除在迁移态下拒绝。
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/query_service.go`
  - 查询在迁移态下继续读源 collection。
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/bootstrap.go`
  - 启动后台迁移 worker。

**Milvus 适配**
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/adapter/types.go`
  - 增加 scan/readback 所需的数据结构。
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/adapter/milvus_adapter_integration.go`
  - 增加 scan source collection、批量导入目标 collection、count 校验与 sample query 校验能力。
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/adapter/milvus_adapter_integration_test.go`
  - 覆盖 scan/upsert/count/sample compare 路径。

**管理接口与错误语义**
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/protocol.go`
  - 增加内部 `ChangeIndexModelRequest/Response` 协议结构。
- Modify: `juicedata-minio/cmd/vector-api.go`
  - 注册内部切换接口路由。
- Modify: `juicedata-minio/cmd/vector-api-handlers.go`
  - 实现 `ChangeIndexModel` handler。
- Modify: `juicedata-minio/cmd/vector-api-handlers_test.go`
  - 覆盖切换请求和迁移态 503 行为。

**文档与脚本**
- Modify: `scripts/start-vectorbucket-standalone.sh`
  - 输出迁移 worker 启动信息。
- Modify: `milvus/deploy/standalone/README.md`
  - 增加手动热切换说明。
- Modify: `docs/vector_bucket_iteration_plan_v2_3.md`
  - 标记 Phase 3a 已落地范围。

---

### Task 1: 扩展 metadata 支持迁移状态

**Files:**
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/metadata/models.go`
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/metadata/sqlite_store.go`
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/metadata/sqlite_store_test.go`

- [ ] **Step 1: 写 failing test，要求 collection 能持久化迁移字段**

```go
func TestSQLiteStorePersistsMigrationFields(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()
	coll := &LogicalCollection{
		ID:                 "lc-1",
		BucketID:           "b-1",
		Name:               "main",
		Dim:                4,
		Metric:             "COSINE",
		IndexType:          "ivf_sq8",
		Tier:               "standard",
		Status:             CollStatusReady,
		PhysicalName:       "vb_b_1_lc_1",
		MigrateState:       "UPGRADING",
		TargetIndexType:    "hnsw",
		SourcePhysicalName: "vb_b_1_lc_1",
		TargetPhysicalName: "vbh_b_1_lc_1",
	}
	require.NoError(t, store.CreateCollection(ctx, coll))
	got, err := store.GetCollectionByID(ctx, "lc-1")
	require.NoError(t, err)
	require.Equal(t, "UPGRADING", got.MigrateState)
	require.Equal(t, "hnsw", got.TargetIndexType)
	require.Equal(t, "vb_b_1_lc_1", got.SourcePhysicalName)
	require.Equal(t, "vbh_b_1_lc_1", got.TargetPhysicalName)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd juicefs-1.3.0-rc1 && GOWORK=off go test -count=1 ./pkg/gateway/vectorbucket/metadata/... -run TestSQLiteStorePersistsMigrationFields`
Expected: FAIL，提示字段不存在或 scan/schema 不匹配

- [ ] **Step 3: 在 `LogicalCollection` 中增加迁移字段**

```go
type LogicalCollection struct {
	ID                 string
	BucketID           string
	Name               string
	Dim                int
	Metric             string
	IndexType          string
	Tier               string
	MaxVectors         int64
	Pinned             bool
	Status             CollectionStatus
	PhysicalName       string
	IndexBuilt         bool
	VectorCount        int64
	EstMemMB           float64
	LastAccessAt       time.Time
	MigrateState       string
	TargetIndexType    string
	SourcePhysicalName string
	TargetPhysicalName string
	MaintenanceSince   time.Time
	LastMigrateError   string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}
```

- [ ] **Step 4: 更新 SQLite schema 与 scan/insert/update**

```sql
ALTER TABLE collections ADD COLUMN migrate_state TEXT NOT NULL DEFAULT '';
ALTER TABLE collections ADD COLUMN target_index_type TEXT NOT NULL DEFAULT '';
ALTER TABLE collections ADD COLUMN source_physical_name TEXT NOT NULL DEFAULT '';
ALTER TABLE collections ADD COLUMN target_physical_name TEXT NOT NULL DEFAULT '';
ALTER TABLE collections ADD COLUMN maintenance_since TIMESTAMP NULL;
ALTER TABLE collections ADD COLUMN last_migrate_error TEXT NOT NULL DEFAULT '';
```

- [ ] **Step 5: 新增 CAS 切换和迁移状态更新方法**

```go
type Store interface {
	UpdateCollectionMigrationState(ctx context.Context, id string, state string, targetIndexType string, sourcePhysical string, targetPhysical string, maintenanceSince time.Time, lastErr string) error
	SwitchCollectionPhysical(ctx context.Context, id string, oldPhysical string, newPhysical string, newIndexType string, newTier string, newPinned bool, newMaxVectors int64) error
	ListCollectionsInMigration(ctx context.Context) ([]*LogicalCollection, error)
}
```

- [ ] **Step 6: 跑 metadata 测试确认通过**

Run: `cd juicefs-1.3.0-rc1 && GOWORK=off go test -count=1 ./pkg/gateway/vectorbucket/metadata/...`
Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/metadata/models.go \
        juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/metadata/sqlite_store.go \
        juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/metadata/sqlite_store_test.go
git commit -m "feat: add vectorbucket migration metadata state"
```

### Task 2: 写路径支持迁移态禁写

**Files:**
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/object_service.go`
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/service_test.go`

- [ ] **Step 1: 写 failing test，要求迁移态时 `PutVectors` 返回 503**

```go
func TestPutVectorsReturnsServiceUnavailableDuringMigration(t *testing.T) {
	svc, deps := newObjectServiceForTest(t)
	deps.store.mustCreateReadyCollectionWithMigrationState("bucket-a", "main", "UPGRADING")
	_, err := svc.PutVectors(context.Background(), &PutVectorsRequest{
		VectorBucketName: "bucket-a",
		IndexName:        "main",
		Vectors:          []VectorPayload{{Key: "v1", Data: VectorData{Float32: []float32{0.1, 0.2, 0.3, 0.4}}}},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrServiceUnavailable)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd juicefs-1.3.0-rc1 && GOWORK=off go test -count=1 ./pkg/gateway/vectorbucket/... -run TestPutVectorsReturnsServiceUnavailableDuringMigration`
Expected: FAIL

- [ ] **Step 3: 在 `PutVectors/DeleteVectors` 前增加迁移态检查**

```go
if coll.MigrateState != "" {
	return fmt.Errorf("%w: collection is migrating", ErrServiceUnavailable)
}
```

- [ ] **Step 4: 保持 `QueryVectors` 不受迁移态影响**

```go
// Query path continues to use coll.PhysicalName until CAS switch completes.
```

- [ ] **Step 5: 跑服务测试确认通过**

Run: `cd juicefs-1.3.0-rc1 && GOWORK=off go test -count=1 ./pkg/gateway/vectorbucket/...`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/object_service.go \
        juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/service_test.go
git commit -m "feat: block vector writes during migration"
```

### Task 3: 增加内部手动切换入口

**Files:**
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/protocol.go`
- Modify: `juicedata-minio/cmd/vector-api.go`
- Modify: `juicedata-minio/cmd/vector-api-handlers.go`
- Modify: `juicedata-minio/cmd/vector-api-handlers_test.go`

- [ ] **Step 1: 写 failing test，要求支持 `ChangeIndexModel` 请求**

```go
func TestChangeIndexModelHandler(t *testing.T) {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ChangeIndexModel", strings.NewReader(`{
	  "vectorBucketName":"bench-ivf",
	  "indexName":"main",
	  "targetIndexModel":"hnsw"
	}`))
	req.Header.Set("Content-Type", "application/json")
	apiRouter().ServeHTTP(recorder, req)
	require.Equal(t, http.StatusAccepted, recorder.Code)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd juicedata-minio && GOWORK=off go test -count=1 ./cmd -run TestChangeIndexModelHandler`
Expected: FAIL

- [ ] **Step 3: 定义内部协议结构**

```go
type ChangeIndexModelRequest struct {
	VectorBucketName string `json:"vectorBucketName"`
	IndexName        string `json:"indexName"`
	TargetIndexModel string `json:"targetIndexModel"`
}

type ChangeIndexModelResponse struct {
	Status string `json:"status"`
}
```

- [ ] **Step 4: 新增 handler 调用迁移服务**

```go
func (api objectAPIHandlers) ChangeIndexModelHandler(w http.ResponseWriter, r *http.Request) {
	var req vectorbucket.ChangeIndexModelRequest
	if err := decodeVectorRequest(r, &req); err != nil { ... }
	if err := api.VectorBucketExtension().ChangeIndexModel(r.Context(), &req); err != nil { ... }
	writeSuccessResponseJSON(w, mustGetClaimsFromToken(r), ChangeIndexModelResponse{Status: "ACCEPTED"})
}
```

- [ ] **Step 5: 注册路由**

```go
router.Methods(http.MethodPost).Path("/ChangeIndexModel").HandlerFunc(api.ChangeIndexModelHandler)
```

- [ ] **Step 6: 跑 handler 测试确认通过**

Run: `cd juicedata-minio && GOWORK=off go test -count=1 ./cmd -run TestChangeIndexModelHandler`
Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/protocol.go \
        juicedata-minio/cmd/vector-api.go \
        juicedata-minio/cmd/vector-api-handlers.go \
        juicedata-minio/cmd/vector-api-handlers_test.go
git commit -m "feat: add manual index model change api"
```

### Task 4: 增加迁移服务与后台 worker

**Files:**
- Create: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/migration/service.go`
- Create: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/migration/service_test.go`
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/bootstrap.go`

- [ ] **Step 1: 写 failing test，要求请求切换后 metadata 进入迁移态**

```go
func TestRequestIndexModelChangeMarksCollectionMigrating(t *testing.T) {
	svc, deps := newMigrationServiceForTest(t)
	deps.store.mustCreateReadyCollection("bench-ivf", "main", "ivf_sq8", "standard", "vb_old")
	err := svc.RequestIndexModelChange(context.Background(), "bench-ivf", "main", "hnsw")
	require.NoError(t, err)
	coll := deps.store.mustGetCollection("bench-ivf", "main")
	require.Equal(t, "UPGRADING", coll.MigrateState)
	require.Equal(t, "hnsw", coll.TargetIndexType)
	require.Equal(t, "vb_old", coll.SourcePhysicalName)
	require.NotEmpty(t, coll.TargetPhysicalName)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd juicefs-1.3.0-rc1 && GOWORK=off go test -count=1 ./pkg/gateway/vectorbucket/migration/... -run TestRequestIndexModelChangeMarksCollectionMigrating`
Expected: FAIL

- [ ] **Step 3: 实现迁移服务骨架**

```go
type Service struct {
	store      metadata.Store
	router     *router.NamespaceRouter
	adapter    adapter.Adapter
	controller *controller.LoadController
	cfg        config.Config
}

func (s *Service) RequestIndexModelChange(ctx context.Context, bucketName, indexName, targetIndexModel string) error
func (s *Service) RunOnce(ctx context.Context) error
func (s *Service) migrateCollection(ctx context.Context, coll *metadata.LogicalCollection) error
```

- [ ] **Step 4: 在 `RequestIndexModelChange` 中写入迁移态**

```go
targetTier := s.cfg.TierForIndexType(targetIndexModel)
targetPhysical := router.PhysicalCollectionNameForTier(targetTier, coll.BucketID, uuid.NewString())
state := "UPGRADING"
if coll.Tier == "performance" && targetTier == "standard" {
	state = "DOWNGRADING"
}
return s.store.UpdateCollectionMigrationState(ctx, coll.ID, state, targetIndexModel, coll.PhysicalName, targetPhysical, time.Now().UTC(), "")
```

- [ ] **Step 5: 在 bootstrap 启动 worker loop**

```go
go func() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = migrationSvc.RunOnce(ctx)
		}
	}
}()
```

- [ ] **Step 6: 跑迁移服务测试确认通过**

Run: `cd juicefs-1.3.0-rc1 && GOWORK=off go test -count=1 ./pkg/gateway/vectorbucket/migration/...`
Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/migration/service.go \
        juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/migration/service_test.go \
        juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/bootstrap.go
git commit -m "feat: add vectorbucket migration worker"
```

### Task 5: Milvus 适配层支持扫描与离线复制

**Files:**
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/adapter/types.go`
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/adapter/milvus_adapter_integration.go`
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/adapter/milvus_adapter_integration_test.go`

- [ ] **Step 1: 写 failing test，要求 adapter 能扫描 source collection 并返回向量记录**

```go
func TestMilvusAdapterScanCollection(t *testing.T) {
	adapter := newTestMilvusAdapter(t)
	rows, err := adapter.ScanCollection(context.Background(), "vb_source", 1000)
	require.NoError(t, err)
	require.NotNil(t, rows)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd juicefs-1.3.0-rc1 && GOWORK=off go test -tags milvus_integration -count=1 ./pkg/gateway/vectorbucket/adapter/... -run TestMilvusAdapterScanCollection`
Expected: FAIL

- [ ] **Step 3: 增加 scan 数据结构**

```go
type ScanRow struct {
	Key       string
	Vector    []float32
	Metadata  []byte
	Timestamp int64
}

type Adapter interface {
	ScanCollection(ctx context.Context, physicalName string, batchSize int) ([]ScanRow, error)
	CountCollection(ctx context.Context, physicalName string) (int64, error)
}
```

- [ ] **Step 4: 实现 scan/count**

```go
func (a *MilvusAdapter) ScanCollection(ctx context.Context, physicalName string, batchSize int) ([]ScanRow, error) {
	// query output fields: id, vector, metadata, updated_at
}
```

- [ ] **Step 5: 跑 adapter 测试确认通过**

Run: `cd juicefs-1.3.0-rc1 && GOWORK=off go test -tags milvus_integration -count=1 ./pkg/gateway/vectorbucket/adapter/...`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/adapter/types.go \
        juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/adapter/milvus_adapter_integration.go \
        juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/adapter/milvus_adapter_integration_test.go
git commit -m "feat: add milvus scan support for migration"
```

### Task 6: 完成后台迁移、校验、CAS 切换和回滚

**Files:**
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/migration/service.go`
- Modify: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/migration/service_test.go`

- [ ] **Step 1: 写 failing test，要求 worker 完成迁移后切换到目标 physical collection**

```go
func TestRunOnceMigratesAndSwitchesCollection(t *testing.T) {
	svc, deps := newMigrationServiceForTest(t)
	deps.store.mustCreateMigratingCollection("bench-ivf", "main", "UPGRADING", "ivf_sq8", "hnsw", "vb_src", "vbh_dst")
	deps.adapter.seedCollection("vb_src", 100)
	err := svc.RunOnce(context.Background())
	require.NoError(t, err)
	coll := deps.store.mustGetCollection("bench-ivf", "main")
	require.Equal(t, "hnsw", coll.IndexType)
	require.Equal(t, "vbh_dst", coll.PhysicalName)
	require.Equal(t, "", coll.MigrateState)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd juicefs-1.3.0-rc1 && GOWORK=off go test -count=1 ./pkg/gateway/vectorbucket/migration/... -run TestRunOnceMigratesAndSwitchesCollection`
Expected: FAIL

- [ ] **Step 3: 迁移流程实现**

```go
func (s *Service) migrateCollection(ctx context.Context, coll *metadata.LogicalCollection) error {
	rows, err := s.adapter.ScanCollection(ctx, coll.SourcePhysicalName, 1000)
	if err != nil { return err }
	if err := s.adapter.CreateCollection(ctx, coll.TargetPhysicalName, coll.Dim, coll.Metric); err != nil { return err }
	if err := s.adapter.CreateIndex(ctx, coll.TargetPhysicalName, adapter.IndexSpec{IndexType: coll.TargetIndexType, Metric: coll.Metric, HNSWM: ..., HNSWEFConstruction: ...}); err != nil { return err }
	if err := s.adapter.UpsertRows(ctx, coll.TargetPhysicalName, rows); err != nil { return err }
	if err := s.adapter.LoadCollection(ctx, coll.TargetPhysicalName); err != nil { return err }
	return s.validateAndSwitch(ctx, coll)
}
```

- [ ] **Step 4: 校验实现**

```go
func (s *Service) validateAndSwitch(ctx context.Context, coll *metadata.LogicalCollection) error {
	srcCount, _ := s.adapter.CountCollection(ctx, coll.SourcePhysicalName)
	dstCount, _ := s.adapter.CountCollection(ctx, coll.TargetPhysicalName)
	if srcCount != dstCount {
		return fmt.Errorf("count mismatch: src=%d dst=%d", srcCount, dstCount)
	}
	return s.store.SwitchCollectionPhysical(ctx, coll.ID, coll.SourcePhysicalName, coll.TargetPhysicalName, coll.TargetIndexType, s.cfg.TierForIndexType(coll.TargetIndexType), s.cfg.IsPinnedIndexType(coll.TargetIndexType), coll.MaxVectors)
}
```

- [ ] **Step 5: 失败回滚**

```go
defer func() {
	if err != nil {
		_ = s.store.UpdateCollectionMigrationState(ctx, coll.ID, "", "", "", "", time.Time{}, err.Error())
		_ = s.adapter.DropCollection(ctx, coll.TargetPhysicalName)
	}
}()
```

- [ ] **Step 6: 切换后清理源 collection**

```go
_ = s.adapter.ReleaseCollection(ctx, coll.SourcePhysicalName)
_ = s.adapter.DropCollection(ctx, coll.SourcePhysicalName)
```

- [ ] **Step 7: 跑迁移测试确认通过**

Run: `cd juicefs-1.3.0-rc1 && GOWORK=off go test -count=1 ./pkg/gateway/vectorbucket/migration/...`
Expected: PASS

- [ ] **Step 8: 提交**

```bash
git add juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/migration/service.go \
        juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/migration/service_test.go
git commit -m "feat: add manual hot switch worker flow"
```

### Task 7: 部署、回归与文档

**Files:**
- Modify: `scripts/start-vectorbucket-standalone.sh`
- Modify: `milvus/deploy/standalone/README.md`
- Modify: `docs/vector_bucket_iteration_plan_v2_3.md`

- [ ] **Step 1: 在启动脚本中打印迁移 worker 启动信息**

```bash
echo "[信息] VectorBucket migration worker 已启动，轮询间隔 5s"
```

- [ ] **Step 2: 在 README 中增加手动热切换示例**

```markdown
## 手动热切换

```bash
curl -X POST http://127.0.0.1:9000/ChangeIndexModel \
  -H 'Content-Type: application/json' \
  -d '{"vectorBucketName":"bench-ivf","indexName":"main","targetIndexModel":"hnsw"}'
```
```

- [ ] **Step 3: 更新设计文档说明仅实现 Phase 3a，不做双写**

```markdown
当前实现仅支持 Phase 3a：
- 切换期间写入返回 503
- 查询继续读源 collection
- 后台迁移完成后 CAS 切换
```

- [ ] **Step 4: 跑端到端回归**

Run:
```bash
./scripts/start-vectorbucket-standalone.sh --fresh
python3 scripts/test-vectorbucket-boto3.py --vector-bucket-name bench-ivf --index-model ivf_sq8
curl -X POST http://127.0.0.1:9000/ChangeIndexModel -H 'Content-Type: application/json' -d '{"vectorBucketName":"bench-ivf","indexName":"main","targetIndexModel":"hnsw"}'
python3 scripts/test-vectorbucket-query-benchmark.py --profile 10k --vector-bucket-name bench-ivf
```
Expected: 查询继续成功，迁移完成后新的模型生效

- [ ] **Step 5: 提交**

```bash
git add scripts/start-vectorbucket-standalone.sh \
        milvus/deploy/standalone/README.md \
        docs/vector_bucket_iteration_plan_v2_3.md \
        docs/superpowers/plans/2026-04-17-vectorbucket-phase3a-manual-hot-switch.md
git commit -m "docs: add phase3a vectorbucket hot switch plan"
```

---

## 自检

- 已覆盖 metadata 扩展、迁移态禁写、内部切换入口、后台 worker、Milvus scan/复制、CAS 切换和文档更新。
- 未引入 Phase 3b 的双写、回滚窗口、自动毕业/降级逻辑。
- 计划只围绕 “切换期间禁写、查询继续可用” 这一条主线，没有把双写一致性一起混进来。
