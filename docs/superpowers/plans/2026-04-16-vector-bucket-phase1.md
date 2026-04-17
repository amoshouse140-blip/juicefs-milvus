# Vector Bucket Phase 1 实现计划

> **给 Agent 执行者:** 必须使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务执行本计划。步骤使用 `- [ ]` 语法追踪进度。

> **实现位置修正:** Phase 1 应落在 **JuiceFS** 的 S3 Gateway 上实现，而不是在 Milvus 仓库里新增一个独立的 Gin/HTTP 服务。本文档迁到仓库根 `docs/`，作为 JuiceFS + Milvus 联动设计与任务拆解。

**目标:** 在 JuiceFS 的 S3 Gateway 上提供 Vector Bucket 能力，对外兼容 AWS S3 Vector Bucket API；JuiceFS 负责协议入口、bucket 生命周期、鉴权与请求编排，Milvus Standalone（IVF_SQ8+mmap）负责底层向量写入、索引和检索，同时补齐 LRU/TTL Load/Release 控制器、配额管控和监控指标。

**实现边界:** 不在 Milvus 仓库新增 `internal/vectorbucket` 独立服务。真正的实现方式是在 JuiceFS 现有 `cmd/gateway.go` / `pkg/gateway/gateway.go` 基础上扩展 Vector Bucket 语义，并把核心业务逻辑沉淀到 `pkg/gateway/vectorbucket/` 子模块。JuiceFS 当前 gateway 是通过 `jfsObjects` 实现 `minio.ObjectLayer` 并交给 `minio.ServerMainForJFS` 托管，不存在一个现成、独立维护的 Gin 路由层可直接挂载。

**架构:** `cmd/gateway.go` 负责启动参数和依赖装配，`pkg/gateway/gateway.go` 中的 `NewJFSGateway` / `jfsObjects` 是 JuiceFS 侧真正的扩展点。Phase 1 应把 Vector Bucket 运行时挂到 `jfsObjects` 上，由 bucket/object 生命周期方法做后端编排；如果当前 `github.com/juicedata/minio` fork 还不能识别 S3 Vector Bucket 请求，则再补一个最小协议 shim，把请求分发给 JuiceFS 定义的扩展接口。子模块内部继续分层：Runtime（依赖装配） -> Metadata Service（SQLite 持久化 bucket/collection 状态） -> Namespace Router（逻辑名 -> 物理名映射） -> Load/Release Controller（LRU+TTL 内存预算管理） -> Milvus Adapter（封装 `client/milvusclient`）。Gateway 与 Milvus Standalone 同 VM 部署，通过 gRPC localhost 连接。

**技术栈:** Go、JuiceFS 现有 S3 Gateway（基于 MinIO Gateway 集成）、`github.com/juicedata/minio` fork（开发期通过 JuiceFS `go.mod` 的 `replace` 指向本地 `JUICEDATA_MINIO_ROOT` 目录，而不是把 minio 代码放进当前仓库）、Milvus Go Client（`client/milvusclient`）、SQLite（`modernc.org/sqlite`，纯 Go 无 CGo）、Prometheus client_golang 指标。

**AWS API 对齐说明:** AWS S3 Vectors 当前使用的是一组独立 API 操作，而不是标准 S3 bucket/object API 变体。最关键的 Phase 1 操作包括 `CreateVectorBucket`、`GetVectorBucket`、`DeleteVectorBucket`、`CreateIndex`、`DeleteIndex`、`PutVectors`、`DeleteVectors`、`QueryVectors`。这意味着仅靠 `MakeBucketWithLocation`、`PutObject`、`DeleteObject` 等标准 `minio.ObjectLayer` 方法不足以实现“对外兼容 AWS S3 Vector Bucket API”；协议层 shim 需要优先验证，且大概率是必需的。

---

## 实现落点与文件结构

### 执行映射约定

- 下文所有代码路径均相对 **JuiceFS 仓库根目录**，除非特别说明。
- 本计划默认本地还存在一个与 `juicefs-1.3.0-rc1/` 平级的 `JUICEDATA_MINIO_ROOT` 目录，例如 `../juicedata-minio/`；它是**单独的本地仓库目录**，不是当前 `juicefs-milvus` 仓库的子目录、子模块或受管文件。
- 下文旧写法 `internal/vectorbucket/*` 在执行时统一映射为 `pkg/gateway/vectorbucket/*`。
- 下文旧写法 `github.com/juicedata/juicefs/pkg/gateway/vectorbucket/...` 在执行时统一映射为 `github.com/juicedata/juicefs/pkg/gateway/vectorbucket/...`。
- 下文旧写法 `internal/vectorbucket/cmd/main.go` 在执行时统一落实为 `cmd/gateway.go` 的接线改动，以及 `pkg/gateway/vectorbucket/bootstrap.go` 的初始化逻辑。
- 下文 `Gin`、`/v1/...`、`HTTP Handler` 相关描述只表示职责拆分；真正对外协议必须挂到 JuiceFS S3 Gateway 上，并遵循 AWS S3 Vector Bucket API 语义，而不是新开一套独立 REST 服务。
- 下文若代码块仍出现 `package gateway`、`NewServer()`、`ServeHTTP()` 或 `/v1/...`，都以 “挂接到 JuiceFS S3 gateway 请求路径 / object layer 扩展点” 为准，示例代码仅表达组件边界，不代表最终入口形式。
- JuiceFS 当前是通过 `jfsObjects` 实现 `minio.ObjectLayer`；因此任务 9-16 的**真实主落点**优先级是：`cmd/gateway.go` -> `pkg/gateway/gateway.go` -> `pkg/gateway/vectorbucket/*`。只有当 `github.com/juicedata/minio` fork 缺少 Vector Bucket 协议解析能力时，才额外引入 fork 侧的协议 shim 任务。
- 本计划中所有 minio 侧改动都指向 `$JUICEDATA_MINIO_ROOT`，并通过 JuiceFS `go.mod` 的本地 `replace github.com/minio/minio => <local-path>` 联调；不要把 minio 文件直接放进当前仓库，也不要把它作为本仓变更的一部分提交。
- 下文所有 `cd /root/xty/milvus` 的命令，在执行时统一替换为进入 JuiceFS 仓库根目录。

### 真实接入链

1. S3 请求由 `cmd/gateway.go` 启动的 `minio.ServerMainForJFS` 接收。
2. `github.com/juicedata/minio` fork 完成协议解析，并把标准 bucket/object 请求下沉到 `minio.ObjectLayer`。
3. `github.com/juicedata/minio` fork 需要识别独立的 Vector API，例如 `POST /CreateVectorBucket`、`POST /CreateIndex`、`POST /PutVectors`、`POST /QueryVectors`，并把这些请求转发到 JuiceFS 定义的扩展接口。
4. JuiceFS 侧由 `pkg/gateway/gateway.go` 中的 `jfsObjects` 或与其关联的 runtime 承接这些扩展调用，再委托给 `pkg/gateway/vectorbucket.Runtime`。
5. `Runtime` 调用 `metadata`、`router`、`controller`、`adapter` 完成状态持久化、Milvus 操作和负载控制。
6. 标准 `ObjectLayer` 方法继续服务普通 S3 请求；不要把 Vector API 错误地硬塞进普通 `CreateBucket` / `PutObject` 语义里。

```
cmd/
  gateway.go                            # JuiceFS S3 Gateway 入口，挂接 Vector Bucket 启动参数与初始化

pkg/gateway/
  gateway.go                            # 现有 S3 Gateway 实现；NewJFSGateway/jfsObjects 的真实接入点
  gateway_test.go                       # Gateway 级回归测试；验证 jfsObjects hook
  vectorbucket/
    bootstrap.go                        # 组件装配与生命周期管理
    runtime.go                          # Runtime 聚合：store/router/controller/adapter/quota/metrics
    protocol.go                         # JuiceFS 与 juicedata/minio 之间的协议桥接约定
    errors.go                           # MinIO / S3 / Vector Bucket 错误映射
    config/
      config.go                         # 配置结构体 + 环境变量加载
    metadata/
      store.go                          # MetadataStore 接口定义
      sqlite_store.go                   # SQLite 实现
      models.go                         # Bucket、LogicalCollection 结构体
    router/
      namespace_router.go               # 逻辑名 -> 物理 Milvus collection 名解析
    adapter/
      milvus_adapter.go                 # 封装 milvusclient，提供 collection/vector 操作
    controller/
      load_controller.go                # LRU + TTL load/release 逻辑
    bucket_service.go                   # Bucket 生命周期与 metadata/Milvus 编排
    object_service.go                   # Put/Delete/Tagging 等对象级写路径编排
    query_service.go                    # Query / Load / Release 编排
    quota/
      quota.go                          # 配额检查逻辑
    metrics/
      metrics.go                        # Prometheus 指标定义

$JUICEDATA_MINIO_ROOT/
  cmd/                                  # 本地独立 minio 仓库目录；仅在缺少 Vector Bucket 协议解析时修改
    <protocol shim>                     # 识别 CreateVectorBucket/CreateIndex/PutVectors/QueryVectors 等请求并调用 JuiceFS 扩展接口
```

```
pkg/gateway/vectorbucket/
  config/
    config_test.go
  runtime_test.go
  protocol_test.go
  metadata/
    sqlite_store_test.go
  router/
    namespace_router_test.go
  adapter/
    milvus_adapter_test.go
  controller/
    load_controller_test.go
  bucket_service_test.go
  object_service_test.go
  query_service_test.go
  quota/
    quota_test.go
  integration/
    gateway_contract_test.go            # 端到端集成测试

pkg/gateway/
  gateway_test.go                       # JuiceFS Gateway 与 Vector Bucket hook 集成验证
```

> 下文任务拆分大体保留原计划的组件顺序，但执行时必须以上述 JuiceFS 落点和映射规则为准。

---

## 任务 1：项目脚手架 + 配置模块

**文件:**
- 修改: `cmd/gateway.go`
- 新建: `pkg/gateway/vectorbucket/bootstrap.go`
- 新建: `pkg/gateway/vectorbucket/config/config.go`

- [ ] **步骤 1: 编写配置测试**

```go
// pkg/gateway/vectorbucket/config/config_test.go
package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	assert.Equal(t, ":9200", cfg.ListenAddr)
	assert.Equal(t, "localhost:19530", cfg.MilvusAddr)
	assert.Equal(t, 4096, cfg.LoadBudgetMB)
	assert.Equal(t, 30*60, cfg.TTLSeconds)
	assert.Equal(t, 10000, cfg.IndexBuildThreshold)
	assert.NotEmpty(t, cfg.SQLitePath)
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("VB_LISTEN_ADDR", ":8080")
	t.Setenv("VB_MILVUS_ADDR", "milvus:19530")
	t.Setenv("VB_LOAD_BUDGET_MB", "2048")
	cfg := LoadConfig()
	assert.Equal(t, ":8080", cfg.ListenAddr)
	assert.Equal(t, "milvus:19530", cfg.MilvusAddr)
	assert.Equal(t, 2048, cfg.LoadBudgetMB)
}
```

- [ ] **步骤 2: 运行测试确认失败**

运行: `cd $JUICEFS_ROOT && go test -count=1 ./pkg/gateway/vectorbucket/config/...`
预期: FAIL — package 不存在

- [ ] **步骤 3: 实现配置模块**

```go
// pkg/gateway/vectorbucket/config/config.go
package config

import (
	"os"
	"strconv"
)

type Config struct {
	ListenAddr          string // 服务监听地址
	MilvusAddr          string // Milvus gRPC 地址
	SQLitePath          string // SQLite 数据库路径
	LoadBudgetMB        int    // 活跃 load 总内存预算（MB）
	TTLSeconds          int    // 空闲 collection 自动 release 的 TTL（秒）
	IndexBuildThreshold int    // 触发建索引的向量数阈值
	MaxBucketsPerTenant int    // 单租户 bucket 上限
	MaxBucketsGlobal    int    // 全局 bucket 上限
	MaxCollPerBucket    int    // 单 bucket 下 collection 上限
	MaxVectorsPerColl   int    // 单 collection 向量数上限
	MaxDim              int    // 最大维度
	WriteQPSPerColl     int    // 单 collection 写入 QPS 上限
	QueryQPSPerColl     int    // 单 collection 查询 QPS 上限
	MaxLoadedColls      int    // 同时 loaded 的 collection 硬上限
}

func DefaultConfig() Config {
	return Config{
		ListenAddr:          ":9200",
		MilvusAddr:          "localhost:19530",
		SQLitePath:          "/var/lib/vectorbucket/metadata.db",
		LoadBudgetMB:        4096,
		TTLSeconds:          30 * 60,
		IndexBuildThreshold: 10000,
		MaxBucketsPerTenant: 100,
		MaxBucketsGlobal:    1000,
		MaxCollPerBucket:    50,
		MaxVectorsPerColl:   1000000,
		MaxDim:              1536,
		WriteQPSPerColl:     500,
		QueryQPSPerColl:     50,
		MaxLoadedColls:      50,
	}
}

func LoadConfig() Config {
	cfg := DefaultConfig()
	if v := os.Getenv("VB_LISTEN_ADDR"); v != "" {
		cfg.ListenAddr = v
	}
	if v := os.Getenv("VB_MILVUS_ADDR"); v != "" {
		cfg.MilvusAddr = v
	}
	if v := os.Getenv("VB_SQLITE_PATH"); v != "" {
		cfg.SQLitePath = v
	}
	if v := os.Getenv("VB_LOAD_BUDGET_MB"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.LoadBudgetMB = n
		}
	}
	if v := os.Getenv("VB_TTL_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.TTLSeconds = n
		}
	}
	return cfg
}
```

- [ ] **步骤 4: 运行测试确认通过**

运行: `cd $JUICEFS_ROOT && go test -count=1 ./pkg/gateway/vectorbucket/config/...`
预期: PASS

- [ ] **步骤 5: 编写 bootstrap.go 占位文件**

```go
// pkg/gateway/vectorbucket/bootstrap.go
package vectorbucket

import (
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/config"
)

type Bootstrap struct {
	Config config.Config
}

func NewBootstrap() *Bootstrap {
	cfg := config.LoadConfig()
	return &Bootstrap{Config: cfg}
}
```

- [ ] **步骤 6: 验证编译通过**

运行: `cd $JUICEFS_ROOT && go build ./cmd/... ./pkg/gateway/vectorbucket/...`
预期: 成功，无错误

- [ ] **步骤 7: 提交**

```bash
git add cmd/gateway.go pkg/gateway/vectorbucket/
git commit -s -m "feat(vectorbucket): add JuiceFS gateway scaffold and configuration

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## 任务 2：Metadata 模型 + Store 接口

**文件:**
- 新建: `pkg/gateway/vectorbucket/metadata/models.go`
- 新建: `pkg/gateway/vectorbucket/metadata/store.go`

- [ ] **步骤 1: 编写模型结构体和 Store 接口**

```go
// pkg/gateway/vectorbucket/metadata/models.go
package metadata

import "time"

type BucketStatus string

const (
	BucketStatusReady    BucketStatus = "READY"
	BucketStatusDeleting BucketStatus = "DELETING"
	BucketStatusDeleted  BucketStatus = "DELETED"
)

type CollectionStatus string

const (
	CollStatusInit     CollectionStatus = "INIT"
	CollStatusReady    CollectionStatus = "READY"
	CollStatusDeleting CollectionStatus = "DELETING"
	CollStatusDeleted  CollectionStatus = "DELETED"
)

type Bucket struct {
	ID        string
	Name      string
	Owner     string
	Status    BucketStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}

type LogicalCollection struct {
	ID           string
	BucketID     string
	Name         string
	Dim          int
	Metric       string // "COSINE" 或 "L2"
	Status       CollectionStatus
	PhysicalName string // "vb_{bucket_id}_{lc_id}"
	IndexBuilt   bool
	VectorCount  int64
	EstMemMB     float64
	LastAccessAt time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
```

```go
// pkg/gateway/vectorbucket/metadata/store.go
package metadata

import "context"

type Store interface {
	// Bucket 操作
	CreateBucket(ctx context.Context, b *Bucket) error
	GetBucket(ctx context.Context, id string) (*Bucket, error)
	GetBucketByName(ctx context.Context, name string) (*Bucket, error)
	ListBuckets(ctx context.Context) ([]*Bucket, error)
	UpdateBucketStatus(ctx context.Context, id string, status BucketStatus) error
	DeleteBucket(ctx context.Context, id string) error
	CountBuckets(ctx context.Context) (int, error)
	CountBucketsByOwner(ctx context.Context, owner string) (int, error)

	// Collection 操作
	CreateCollection(ctx context.Context, c *LogicalCollection) error
	GetCollection(ctx context.Context, bucketID, name string) (*LogicalCollection, error)
	GetCollectionByID(ctx context.Context, id string) (*LogicalCollection, error)
	ListCollections(ctx context.Context, bucketID string) ([]*LogicalCollection, error)
	UpdateCollectionStatus(ctx context.Context, id string, status CollectionStatus) error
	UpdateCollectionIndexBuilt(ctx context.Context, id string, built bool) error
	UpdateCollectionVectorCount(ctx context.Context, id string, delta int64) error
	UpdateCollectionLastAccess(ctx context.Context, id string) error
	DeleteCollection(ctx context.Context, id string) error
	CountCollections(ctx context.Context, bucketID string) (int, error)

	// 初始化和关闭
	Init(ctx context.Context) error
	Close() error
}
```

- [ ] **步骤 2: 验证编译通过**

运行: `cd $JUICEFS_ROOT && go build ./pkg/gateway/vectorbucket/metadata/`
预期: 成功

- [ ] **步骤 3: 提交**

```bash
git add pkg/gateway/vectorbucket/metadata/models.go pkg/gateway/vectorbucket/metadata/store.go
git commit -s -m "feat(vectorbucket): add metadata models and store interface

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## 任务 3：SQLite Metadata Store 实现

**文件:**
- 新建: `pkg/gateway/vectorbucket/metadata/sqlite_store.go`
- 新建: `pkg/gateway/vectorbucket/metadata/sqlite_store_test.go`

**说明:** 使用 `modernc.org/sqlite`（纯 Go，无 CGo 依赖）。需先执行 `go get modernc.org/sqlite`。

- [ ] **步骤 1: 添加 SQLite 依赖**

运行: `cd $JUICEFS_ROOT && go get modernc.org/sqlite`

- [ ] **步骤 2: 编写 Bucket CRUD 失败测试**

```go
// pkg/gateway/vectorbucket/metadata/sqlite_store_test.go
package metadata

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s := NewSQLiteStore(dbPath)
	require.NoError(t, s.Init(context.Background()))
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCreateAndGetBucket(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	b := &Bucket{ID: "b1", Name: "my-bucket", Owner: "user1", Status: BucketStatusReady}
	require.NoError(t, s.CreateBucket(ctx, b))

	got, err := s.GetBucket(ctx, "b1")
	require.NoError(t, err)
	assert.Equal(t, "my-bucket", got.Name)
	assert.Equal(t, "user1", got.Owner)
	assert.Equal(t, BucketStatusReady, got.Status)
}

func TestGetBucketByName(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	b := &Bucket{ID: "b1", Name: "my-bucket", Owner: "user1", Status: BucketStatusReady}
	require.NoError(t, s.CreateBucket(ctx, b))

	got, err := s.GetBucketByName(ctx, "my-bucket")
	require.NoError(t, err)
	assert.Equal(t, "b1", got.ID)
}

func TestCreateDuplicateBucketNameFails(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	b1 := &Bucket{ID: "b1", Name: "same-name", Owner: "user1", Status: BucketStatusReady}
	require.NoError(t, s.CreateBucket(ctx, b1))

	b2 := &Bucket{ID: "b2", Name: "same-name", Owner: "user1", Status: BucketStatusReady}
	err := s.CreateBucket(ctx, b2)
	assert.Error(t, err)
}

func TestListBuckets(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, s.CreateBucket(ctx, &Bucket{ID: "b1", Name: "a", Owner: "u1", Status: BucketStatusReady}))
	require.NoError(t, s.CreateBucket(ctx, &Bucket{ID: "b2", Name: "b", Owner: "u1", Status: BucketStatusReady}))

	list, err := s.ListBuckets(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 2)
}

func TestCountBuckets(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, s.CreateBucket(ctx, &Bucket{ID: "b1", Name: "a", Owner: "u1", Status: BucketStatusReady}))
	require.NoError(t, s.CreateBucket(ctx, &Bucket{ID: "b2", Name: "b", Owner: "u1", Status: BucketStatusReady}))

	cnt, err := s.CountBuckets(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, cnt)

	cnt, err = s.CountBucketsByOwner(ctx, "u1")
	require.NoError(t, err)
	assert.Equal(t, 2, cnt)
}

func TestDeleteBucket(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, s.CreateBucket(ctx, &Bucket{ID: "b1", Name: "a", Owner: "u1", Status: BucketStatusReady}))
	require.NoError(t, s.DeleteBucket(ctx, "b1"))

	_, err := s.GetBucket(ctx, "b1")
	assert.Error(t, err)
}
```

- [ ] **步骤 3: 运行测试确认失败**

运行: `cd $JUICEFS_ROOT && go test -count=1 ./pkg/gateway/vectorbucket/metadata/...`
预期: FAIL — `NewSQLiteStore` 未定义

- [ ] **步骤 4: 实现 SQLiteStore（Bucket + Collection 全部操作）**

```go
// pkg/gateway/vectorbucket/metadata/sqlite_store.go
package metadata

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	dbPath string
	db     *sql.DB
}

func NewSQLiteStore(dbPath string) *SQLiteStore {
	return &SQLiteStore{dbPath: dbPath}
}

func (s *SQLiteStore) Init(ctx context.Context) error {
	db, err := sql.Open("sqlite", s.dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	s.db = db

	schema := `
	CREATE TABLE IF NOT EXISTS buckets (
		id         TEXT PRIMARY KEY,
		name       TEXT NOT NULL UNIQUE,
		owner      TEXT NOT NULL,
		status     TEXT NOT NULL DEFAULT 'READY',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS collections (
		id              TEXT PRIMARY KEY,
		bucket_id       TEXT NOT NULL REFERENCES buckets(id),
		name            TEXT NOT NULL,
		dim             INTEGER NOT NULL,
		metric          TEXT NOT NULL,
		status          TEXT NOT NULL DEFAULT 'INIT',
		physical_name   TEXT NOT NULL,
		index_built     BOOLEAN NOT NULL DEFAULT FALSE,
		vector_count    INTEGER NOT NULL DEFAULT 0,
		est_mem_mb      REAL NOT NULL DEFAULT 0,
		last_access_at  DATETIME,
		created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(bucket_id, name)
	);
	`
	_, err = s.db.ExecContext(ctx, schema)
	if err != nil {
		return fmt.Errorf("create tables: %w", err)
	}
	return nil
}

func (s *SQLiteStore) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// --- Bucket 操作 ---

func (s *SQLiteStore) CreateBucket(ctx context.Context, b *Bucket) error {
	now := time.Now()
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO buckets (id, name, owner, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		b.ID, b.Name, b.Owner, b.Status, now, now)
	return err
}

func (s *SQLiteStore) GetBucket(ctx context.Context, id string) (*Bucket, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT id, name, owner, status, created_at, updated_at FROM buckets WHERE id = ?", id)
	return scanBucket(row)
}

func (s *SQLiteStore) GetBucketByName(ctx context.Context, name string) (*Bucket, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT id, name, owner, status, created_at, updated_at FROM buckets WHERE name = ?", name)
	return scanBucket(row)
}

func (s *SQLiteStore) ListBuckets(ctx context.Context) ([]*Bucket, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, name, owner, status, created_at, updated_at FROM buckets WHERE status != 'DELETED' ORDER BY created_at")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*Bucket
	for rows.Next() {
		b := &Bucket{}
		if err := rows.Scan(&b.ID, &b.Name, &b.Owner, &b.Status, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, b)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) UpdateBucketStatus(ctx context.Context, id string, status BucketStatus) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE buckets SET status = ?, updated_at = ? WHERE id = ?", status, time.Now(), id)
	return err
}

func (s *SQLiteStore) DeleteBucket(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM buckets WHERE id = ?", id)
	return err
}

func (s *SQLiteStore) CountBuckets(ctx context.Context) (int, error) {
	var cnt int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM buckets WHERE status != 'DELETED'").Scan(&cnt)
	return cnt, err
}

func (s *SQLiteStore) CountBucketsByOwner(ctx context.Context, owner string) (int, error) {
	var cnt int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM buckets WHERE owner = ? AND status != 'DELETED'", owner).Scan(&cnt)
	return cnt, err
}

// --- Collection 操作 ---

func (s *SQLiteStore) CreateCollection(ctx context.Context, c *LogicalCollection) error {
	now := time.Now()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO collections (id, bucket_id, name, dim, metric, status, physical_name, index_built, vector_count, est_mem_mb, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.BucketID, c.Name, c.Dim, c.Metric, c.Status, c.PhysicalName, c.IndexBuilt, c.VectorCount, c.EstMemMB, now, now)
	return err
}

func (s *SQLiteStore) GetCollection(ctx context.Context, bucketID, name string) (*LogicalCollection, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, bucket_id, name, dim, metric, status, physical_name, index_built, vector_count, est_mem_mb, last_access_at, created_at, updated_at
		 FROM collections WHERE bucket_id = ? AND name = ? AND status != 'DELETED'`, bucketID, name)
	return scanCollection(row)
}

func (s *SQLiteStore) GetCollectionByID(ctx context.Context, id string) (*LogicalCollection, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, bucket_id, name, dim, metric, status, physical_name, index_built, vector_count, est_mem_mb, last_access_at, created_at, updated_at
		 FROM collections WHERE id = ?`, id)
	return scanCollection(row)
}

func (s *SQLiteStore) ListCollections(ctx context.Context, bucketID string) ([]*LogicalCollection, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, bucket_id, name, dim, metric, status, physical_name, index_built, vector_count, est_mem_mb, last_access_at, created_at, updated_at
		 FROM collections WHERE bucket_id = ? AND status != 'DELETED' ORDER BY created_at`, bucketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*LogicalCollection
	for rows.Next() {
		c, err := scanCollectionRow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) UpdateCollectionStatus(ctx context.Context, id string, status CollectionStatus) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE collections SET status = ?, updated_at = ? WHERE id = ?", status, time.Now(), id)
	return err
}

func (s *SQLiteStore) UpdateCollectionIndexBuilt(ctx context.Context, id string, built bool) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE collections SET index_built = ?, updated_at = ? WHERE id = ?", built, time.Now(), id)
	return err
}

func (s *SQLiteStore) UpdateCollectionVectorCount(ctx context.Context, id string, delta int64) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE collections SET vector_count = vector_count + ?, updated_at = ? WHERE id = ?", delta, time.Now(), id)
	return err
}

func (s *SQLiteStore) UpdateCollectionLastAccess(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE collections SET last_access_at = ?, updated_at = ? WHERE id = ?", time.Now(), time.Now(), id)
	return err
}

func (s *SQLiteStore) DeleteCollection(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM collections WHERE id = ?", id)
	return err
}

func (s *SQLiteStore) CountCollections(ctx context.Context, bucketID string) (int, error) {
	var cnt int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM collections WHERE bucket_id = ? AND status != 'DELETED'", bucketID).Scan(&cnt)
	return cnt, err
}

// --- Scan 辅助函数 ---

func scanBucket(row *sql.Row) (*Bucket, error) {
	b := &Bucket{}
	err := row.Scan(&b.ID, &b.Name, &b.Owner, &b.Status, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func scanCollection(row *sql.Row) (*LogicalCollection, error) {
	c := &LogicalCollection{}
	var lastAccess sql.NullTime
	err := row.Scan(&c.ID, &c.BucketID, &c.Name, &c.Dim, &c.Metric, &c.Status, &c.PhysicalName,
		&c.IndexBuilt, &c.VectorCount, &c.EstMemMB, &lastAccess, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if lastAccess.Valid {
		c.LastAccessAt = lastAccess.Time
	}
	return c, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanCollectionRow(row rowScanner) (*LogicalCollection, error) {
	c := &LogicalCollection{}
	var lastAccess sql.NullTime
	err := row.Scan(&c.ID, &c.BucketID, &c.Name, &c.Dim, &c.Metric, &c.Status, &c.PhysicalName,
		&c.IndexBuilt, &c.VectorCount, &c.EstMemMB, &lastAccess, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if lastAccess.Valid {
		c.LastAccessAt = lastAccess.Time
	}
	return c, nil
}
```

- [ ] **步骤 5: 运行测试确认通过**

运行: `cd $JUICEFS_ROOT && go test -count=1 ./pkg/gateway/vectorbucket/metadata/...`
预期: PASS

- [ ] **步骤 6: 编写 Collection CRUD 测试**

```go
// 追加到 pkg/gateway/vectorbucket/metadata/sqlite_store_test.go

func TestCreateAndGetCollection(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, s.CreateBucket(ctx, &Bucket{ID: "b1", Name: "bkt", Owner: "u1", Status: BucketStatusReady}))

	c := &LogicalCollection{
		ID: "c1", BucketID: "b1", Name: "my-coll", Dim: 768, Metric: "COSINE",
		Status: CollStatusReady, PhysicalName: "vb_b1_c1",
	}
	require.NoError(t, s.CreateCollection(ctx, c))

	got, err := s.GetCollection(ctx, "b1", "my-coll")
	require.NoError(t, err)
	assert.Equal(t, "c1", got.ID)
	assert.Equal(t, 768, got.Dim)
	assert.Equal(t, "COSINE", got.Metric)
	assert.Equal(t, "vb_b1_c1", got.PhysicalName)
}

func TestDuplicateCollectionNameInBucketFails(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, s.CreateBucket(ctx, &Bucket{ID: "b1", Name: "bkt", Owner: "u1", Status: BucketStatusReady}))

	c1 := &LogicalCollection{ID: "c1", BucketID: "b1", Name: "coll", Dim: 768, Metric: "COSINE", Status: CollStatusReady, PhysicalName: "vb_b1_c1"}
	require.NoError(t, s.CreateCollection(ctx, c1))

	c2 := &LogicalCollection{ID: "c2", BucketID: "b1", Name: "coll", Dim: 768, Metric: "COSINE", Status: CollStatusReady, PhysicalName: "vb_b1_c2"}
	assert.Error(t, s.CreateCollection(ctx, c2))
}

func TestUpdateCollectionVectorCount(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, s.CreateBucket(ctx, &Bucket{ID: "b1", Name: "bkt", Owner: "u1", Status: BucketStatusReady}))
	c := &LogicalCollection{ID: "c1", BucketID: "b1", Name: "coll", Dim: 768, Metric: "COSINE", Status: CollStatusReady, PhysicalName: "vb_b1_c1"}
	require.NoError(t, s.CreateCollection(ctx, c))

	require.NoError(t, s.UpdateCollectionVectorCount(ctx, "c1", 100))
	got, _ := s.GetCollectionByID(ctx, "c1")
	assert.Equal(t, int64(100), got.VectorCount)

	require.NoError(t, s.UpdateCollectionVectorCount(ctx, "c1", -30))
	got, _ = s.GetCollectionByID(ctx, "c1")
	assert.Equal(t, int64(70), got.VectorCount)
}

func TestCountCollections(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, s.CreateBucket(ctx, &Bucket{ID: "b1", Name: "bkt", Owner: "u1", Status: BucketStatusReady}))
	require.NoError(t, s.CreateCollection(ctx, &LogicalCollection{ID: "c1", BucketID: "b1", Name: "a", Dim: 768, Metric: "COSINE", Status: CollStatusReady, PhysicalName: "vb_b1_c1"}))
	require.NoError(t, s.CreateCollection(ctx, &LogicalCollection{ID: "c2", BucketID: "b1", Name: "b", Dim: 768, Metric: "COSINE", Status: CollStatusReady, PhysicalName: "vb_b1_c2"}))

	cnt, err := s.CountCollections(ctx, "b1")
	require.NoError(t, err)
	assert.Equal(t, 2, cnt)
}
```

- [ ] **步骤 7: 运行全部 metadata 测试**

运行: `cd $JUICEFS_ROOT && go test -count=1 -v ./pkg/gateway/vectorbucket/metadata/...`
预期: 全部 PASS

- [ ] **步骤 8: 提交**

```bash
git add pkg/gateway/vectorbucket/metadata/
git commit -s -m "feat(vectorbucket): implement SQLite metadata store with bucket and collection CRUD

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## 任务 4：Namespace Router（命名空间路由）

**文件:**
- 新建: `pkg/gateway/vectorbucket/router/namespace_router.go`
- 新建: `pkg/gateway/vectorbucket/router/namespace_router_test.go`

- [ ] **步骤 1: 编写失败测试**

```go
// pkg/gateway/vectorbucket/router/namespace_router_test.go
package router

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metadata"
)

func newTestRouter(t *testing.T) *NamespaceRouter {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store := metadata.NewSQLiteStore(dbPath)
	require.NoError(t, store.Init(context.Background()))
	t.Cleanup(func() { store.Close() })
	return NewNamespaceRouter(store)
}

func TestPhysicalName(t *testing.T) {
	assert.Equal(t, "vb_b1_c1", PhysicalCollectionName("b1", "c1"))
}

func TestResolveCollection(t *testing.T) {
	r := newTestRouter(t)
	ctx := context.Background()

	// 准备：在 metadata 中创建 bucket + collection
	require.NoError(t, r.store.CreateBucket(ctx, &metadata.Bucket{
		ID: "b1", Name: "my-bucket", Owner: "u1", Status: metadata.BucketStatusReady,
	}))
	require.NoError(t, r.store.CreateCollection(ctx, &metadata.LogicalCollection{
		ID: "c1", BucketID: "b1", Name: "my-coll", Dim: 768, Metric: "COSINE",
		Status: metadata.CollStatusReady, PhysicalName: "vb_b1_c1",
	}))

	lc, err := r.Resolve(ctx, "my-bucket", "my-coll")
	require.NoError(t, err)
	assert.Equal(t, "vb_b1_c1", lc.PhysicalName)
	assert.Equal(t, 768, lc.Dim)
}

func TestResolveNotFound(t *testing.T) {
	r := newTestRouter(t)
	ctx := context.Background()

	_, err := r.Resolve(ctx, "no-bucket", "no-coll")
	assert.Error(t, err)
}
```

- [ ] **步骤 2: 运行测试确认失败**

运行: `cd $JUICEFS_ROOT && go test -count=1 ./pkg/gateway/vectorbucket/router/...`
预期: FAIL

- [ ] **步骤 3: 实现 NamespaceRouter**

```go
// pkg/gateway/vectorbucket/router/namespace_router.go
package router

import (
	"context"
	"fmt"

	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metadata"
)

// PhysicalCollectionName 生成物理 Milvus collection 名称
func PhysicalCollectionName(bucketID, collectionID string) string {
	return fmt.Sprintf("vb_%s_%s", bucketID, collectionID)
}

type NamespaceRouter struct {
	store metadata.Store
}

func NewNamespaceRouter(store metadata.Store) *NamespaceRouter {
	return &NamespaceRouter{store: store}
}

// Resolve 通过 bucket 名 + collection 名查找逻辑 collection，
// 返回包含物理 Milvus collection 名称的完整 LogicalCollection。
func (r *NamespaceRouter) Resolve(ctx context.Context, bucketName, collectionName string) (*metadata.LogicalCollection, error) {
	bucket, err := r.store.GetBucketByName(ctx, bucketName)
	if err != nil {
		return nil, fmt.Errorf("bucket %q not found: %w", bucketName, err)
	}
	if bucket.Status != metadata.BucketStatusReady {
		return nil, fmt.Errorf("bucket %q is not ready (status=%s)", bucketName, bucket.Status)
	}

	coll, err := r.store.GetCollection(ctx, bucket.ID, collectionName)
	if err != nil {
		return nil, fmt.Errorf("collection %q not found in bucket %q: %w", collectionName, bucketName, err)
	}
	if coll.Status != metadata.CollStatusReady {
		return nil, fmt.Errorf("collection %q is not ready (status=%s)", collectionName, coll.Status)
	}

	return coll, nil
}
```

- [ ] **步骤 4: 运行测试确认通过**

运行: `cd $JUICEFS_ROOT && go test -count=1 ./pkg/gateway/vectorbucket/router/...`
预期: PASS

- [ ] **步骤 5: 提交**

```bash
git add pkg/gateway/vectorbucket/router/
git commit -s -m "feat(vectorbucket): add namespace router for logical-to-physical collection resolution

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## 任务 5：Milvus Adapter（Milvus 适配层）

**文件:**
- 新建: `pkg/gateway/vectorbucket/adapter/milvus_adapter.go`
- 新建: `pkg/gateway/vectorbucket/adapter/milvus_adapter_test.go`

- [ ] **步骤 1: 定义 Adapter 接口和实现**

```go
// pkg/gateway/vectorbucket/adapter/milvus_adapter.go
package adapter

import (
	"context"
	"fmt"
	"math"

	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/index"
	"github.com/milvus-io/milvus/client/v2/milvusclient"
)

// Adapter 定义 Vector Bucket 对 Milvus 的操作接口
type Adapter interface {
	CreateCollection(ctx context.Context, name string, dim int, metric string) error
	DropCollection(ctx context.Context, name string) error
	HasCollection(ctx context.Context, name string) (bool, error)
	CreateIndex(ctx context.Context, name string, vectorCount int64, metric string) error
	LoadCollection(ctx context.Context, name string) error
	ReleaseCollection(ctx context.Context, name string) error
	Insert(ctx context.Context, name string, ids []string, vectors [][]float32, metadataJSON [][]byte, timestamps []int64) error
	Upsert(ctx context.Context, name string, ids []string, vectors [][]float32, metadataJSON [][]byte, timestamps []int64) error
	Delete(ctx context.Context, name string, ids []string) error
	Search(ctx context.Context, name string, vector []float32, topK int, nprobe int, filter string, metric string) ([]SearchResult, error)
	Close() error
}

type SearchResult struct {
	ID       string
	Score    float32
	Metadata []byte
}

type MilvusAdapter struct {
	client *milvusclient.Client
}

func NewMilvusAdapter(addr string) (*MilvusAdapter, error) {
	client, err := milvusclient.New(context.Background(), &milvusclient.ClientConfig{
		Address: addr,
	})
	if err != nil {
		return nil, fmt.Errorf("connect to milvus at %s: %w", addr, err)
	}
	return &MilvusAdapter{client: client}, nil
}

func (a *MilvusAdapter) Close() error {
	return a.client.Close(context.Background())
}

func (a *MilvusAdapter) CreateCollection(ctx context.Context, name string, dim int, metric string) error {
	schema := entity.NewSchema().
		WithField(entity.NewField().WithName("id").WithDataType(entity.FieldTypeVarChar).WithMaxLength(64).WithIsPrimaryKey(true)).
		WithField(entity.NewField().WithName("vector").WithDataType(entity.FieldTypeFloatVector).WithDim(int64(dim))).
		WithField(entity.NewField().WithName("metadata").WithDataType(entity.FieldTypeJSON)).
		WithField(entity.NewField().WithName("created_at").WithDataType(entity.FieldTypeInt64))

	return a.client.CreateCollection(ctx, milvusclient.NewCreateCollectionOption(name, schema).
		WithShardNum(1))
}

func (a *MilvusAdapter) DropCollection(ctx context.Context, name string) error {
	return a.client.DropCollection(ctx, milvusclient.NewDropCollectionOption(name))
}

func (a *MilvusAdapter) HasCollection(ctx context.Context, name string) (bool, error) {
	return a.client.HasCollection(ctx, milvusclient.NewHasCollectionOption(name))
}

// computeNlist 根据设计文档公式计算 IVF nlist 参数: clamp(sqrt(N)*4, 1024, 65536)
func computeNlist(vectorCount int64) int {
	nlist := int(math.Sqrt(float64(vectorCount)) * 4)
	if nlist < 1024 {
		nlist = 1024
	}
	if nlist > 65536 {
		nlist = 65536
	}
	return nlist
}

func metricTypeFromString(metric string) entity.MetricType {
	switch metric {
	case "L2":
		return entity.L2
	case "COSINE":
		return entity.COSINE
	default:
		return entity.COSINE
	}
}

func (a *MilvusAdapter) CreateIndex(ctx context.Context, name string, vectorCount int64, metric string) error {
	nlist := computeNlist(vectorCount)
	idx := index.NewIvfSQ8Index(metricTypeFromString(metric), nlist)
	return a.client.CreateIndex(ctx, milvusclient.NewCreateIndexOption(name, "vector", idx).
		WithExtraParam("mmap.enabled", "true"))
}

func (a *MilvusAdapter) LoadCollection(ctx context.Context, name string) error {
	loadTask, err := a.client.LoadCollection(ctx, milvusclient.NewLoadCollectionOption(name))
	if err != nil {
		return err
	}
	return loadTask.Await(ctx)
}

func (a *MilvusAdapter) ReleaseCollection(ctx context.Context, name string) error {
	return a.client.ReleaseCollection(ctx, milvusclient.NewReleaseCollectionOption(name))
}

func (a *MilvusAdapter) Insert(ctx context.Context, name string, ids []string, vectors [][]float32, metadataJSON [][]byte, timestamps []int64) error {
	_, err := a.client.Insert(ctx, milvusclient.NewColumnBasedInsertOption(name).
		WithVarcharColumn("id", ids).
		WithFloatVectorColumn("vector", len(vectors[0]), vectors).
		WithColumn(entity.NewColumnJSONBytes("metadata", metadataJSON)).
		WithInt64Column("created_at", timestamps))
	return err
}

func (a *MilvusAdapter) Upsert(ctx context.Context, name string, ids []string, vectors [][]float32, metadataJSON [][]byte, timestamps []int64) error {
	_, err := a.client.Upsert(ctx, milvusclient.NewColumnBasedInsertOption(name).
		WithVarcharColumn("id", ids).
		WithFloatVectorColumn("vector", len(vectors[0]), vectors).
		WithColumn(entity.NewColumnJSONBytes("metadata", metadataJSON)).
		WithInt64Column("created_at", timestamps))
	return err
}

func (a *MilvusAdapter) Delete(ctx context.Context, name string, ids []string) error {
	return a.client.Delete(ctx, milvusclient.NewDeleteOption(name).WithExpr(
		buildIDFilter(ids)))
}

func buildIDFilter(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	expr := "id in ["
	for i, id := range ids {
		if i > 0 {
			expr += ","
		}
		expr += fmt.Sprintf("%q", id)
	}
	expr += "]"
	return expr
}

func (a *MilvusAdapter) Search(ctx context.Context, name string, vector []float32, topK int, nprobe int, filter string, metric string) ([]SearchResult, error) {
	searchOpt := milvusclient.NewSearchOption(name, topK, []entity.Vector{entity.FloatVector(vector)}).
		WithSearchParam("nprobe", nprobe).
		WithOutputFields("id", "metadata")

	if filter != "" {
		searchOpt = searchOpt.WithFilter(filter)
	}

	results, err := a.client.Search(ctx, searchOpt)
	if err != nil {
		return nil, err
	}

	var out []SearchResult
	for _, rs := range results {
		for i := 0; i < rs.ResultCount; i++ {
			id, _ := rs.IDs.GetAsString(i)
			var meta []byte
			if col := rs.Fields.GetColumn("metadata"); col != nil {
				metaVal, _ := col.GetAsString(i)
				meta = []byte(metaVal)
			}
			out = append(out, SearchResult{
				ID:       id,
				Score:    rs.Scores[i],
				Metadata: meta,
			})
		}
	}
	return out, nil
}
```

- [ ] **步骤 2: 编写纯函数的单元测试**

```go
// pkg/gateway/vectorbucket/adapter/milvus_adapter_test.go
package adapter

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestComputeNlist(t *testing.T) {
	// 小数量 → 钳制到 1024
	assert.Equal(t, 1024, computeNlist(100))

	// sqrt(100000)*4 = 1264
	assert.Equal(t, 1264, computeNlist(100000))

	// 超大数量 → 钳制到 65536
	assert.Equal(t, 65536, computeNlist(1000000000))
}

func TestBuildIDFilter(t *testing.T) {
	assert.Equal(t, "", buildIDFilter(nil))
	assert.Equal(t, `id in ["a"]`, buildIDFilter([]string{"a"}))
	assert.Equal(t, `id in ["a","b","c"]`, buildIDFilter([]string{"a", "b", "c"}))
}

func TestMetricTypeFromString(t *testing.T) {
	assert.NotNil(t, metricTypeFromString("L2"))
	assert.NotNil(t, metricTypeFromString("COSINE"))
	assert.NotNil(t, metricTypeFromString("unknown"))
}
```

- [ ] **步骤 3: 运行测试**

运行: `cd $JUICEFS_ROOT && go test -count=1 ./pkg/gateway/vectorbucket/adapter/...`
预期: PASS（仅纯函数测试；Milvus 集成测试在任务 16）

- [ ] **步骤 4: 提交**

```bash
git add pkg/gateway/vectorbucket/adapter/
git commit -s -m "feat(vectorbucket): add Milvus adapter wrapping client for collection and vector operations

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## 任务 6：Load/Release 控制器

**文件:**
- 新建: `pkg/gateway/vectorbucket/controller/load_controller.go`
- 新建: `pkg/gateway/vectorbucket/controller/load_controller_test.go`

- [ ] **步骤 1: 编写失败测试**

```go
// pkg/gateway/vectorbucket/controller/load_controller_test.go
package controller

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockAdapter 记录 load/release 调用，不依赖真实 Milvus
type mockAdapter struct {
	mu        sync.Mutex
	loaded    map[string]bool
	loadDelay time.Duration
}

func newMockAdapter() *mockAdapter {
	return &mockAdapter{loaded: make(map[string]bool)}
}

func (m *mockAdapter) LoadCollection(ctx context.Context, name string) error {
	if m.loadDelay > 0 {
		time.Sleep(m.loadDelay)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loaded[name] = true
	return nil
}

func (m *mockAdapter) ReleaseCollection(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.loaded, name)
	return nil
}

func (m *mockAdapter) isLoaded(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loaded[name]
}

func TestEnsureLoaded(t *testing.T) {
	ma := newMockAdapter()
	ctrl := NewLoadController(ma, 4096, 30*time.Minute, 50)

	err := ctrl.EnsureLoaded(context.Background(), "coll1", 100.0)
	require.NoError(t, err)
	assert.True(t, ma.isLoaded("coll1"))
	assert.True(t, ctrl.IsLoaded("coll1"))
}

func TestEnsureLoadedIdempotent(t *testing.T) {
	ma := newMockAdapter()
	ctrl := NewLoadController(ma, 4096, 30*time.Minute, 50)

	require.NoError(t, ctrl.EnsureLoaded(context.Background(), "coll1", 100.0))
	require.NoError(t, ctrl.EnsureLoaded(context.Background(), "coll1", 100.0))
	assert.True(t, ctrl.IsLoaded("coll1"))
}

func TestLRUEviction(t *testing.T) {
	ma := newMockAdapter()
	// 预算: 200 MB
	ctrl := NewLoadController(ma, 200, 30*time.Minute, 50)

	// 加载两个 100MB 的 collection → 预算满
	require.NoError(t, ctrl.EnsureLoaded(context.Background(), "coll1", 100.0))
	require.NoError(t, ctrl.EnsureLoaded(context.Background(), "coll2", 100.0))

	// 加载第三个 → 应淘汰 coll1（LRU 最旧）
	require.NoError(t, ctrl.EnsureLoaded(context.Background(), "coll3", 100.0))
	assert.False(t, ctrl.IsLoaded("coll1"), "coll1 应被淘汰")
	assert.True(t, ctrl.IsLoaded("coll2"))
	assert.True(t, ctrl.IsLoaded("coll3"))
}

func TestTTLRelease(t *testing.T) {
	ma := newMockAdapter()
	// TTL 100ms，加速测试
	ctrl := NewLoadController(ma, 4096, 100*time.Millisecond, 50)

	require.NoError(t, ctrl.EnsureLoaded(context.Background(), "coll1", 100.0))
	assert.True(t, ctrl.IsLoaded("coll1"))

	// 等待 TTL 过期
	time.Sleep(200 * time.Millisecond)
	ctrl.RunTTLSweep()

	assert.False(t, ctrl.IsLoaded("coll1"))
}

func TestInFlightPreventsRelease(t *testing.T) {
	ma := newMockAdapter()
	ctrl := NewLoadController(ma, 4096, 100*time.Millisecond, 50)

	require.NoError(t, ctrl.EnsureLoaded(context.Background(), "coll1", 100.0))
	ctrl.InFlightInc("coll1")

	// 等待 TTL
	time.Sleep(200 * time.Millisecond)
	ctrl.RunTTLSweep()

	// 有 in-flight 查询，不应被 release
	assert.True(t, ctrl.IsLoaded("coll1"))

	ctrl.InFlightDec("coll1")
	ctrl.RunTTLSweep()
	// 现在应该被 release
	assert.False(t, ctrl.IsLoaded("coll1"))
}

func TestMaxLoadedCollections(t *testing.T) {
	ma := newMockAdapter()
	ctrl := NewLoadController(ma, 999999, 30*time.Minute, 2) // 最多 2 个

	require.NoError(t, ctrl.EnsureLoaded(context.Background(), "c1", 1.0))
	require.NoError(t, ctrl.EnsureLoaded(context.Background(), "c2", 1.0))

	// 第三个应淘汰 LRU
	require.NoError(t, ctrl.EnsureLoaded(context.Background(), "c3", 1.0))
	assert.False(t, ctrl.IsLoaded("c1"))
}

func TestTouchUpdatesLRU(t *testing.T) {
	ma := newMockAdapter()
	ctrl := NewLoadController(ma, 200, 30*time.Minute, 50)

	require.NoError(t, ctrl.EnsureLoaded(context.Background(), "coll1", 100.0))
	require.NoError(t, ctrl.EnsureLoaded(context.Background(), "coll2", 100.0))

	// Touch coll1，使其变为最近使用
	ctrl.Touch("coll1")

	// 加载 coll3 → 应淘汰 coll2（此时 LRU 最旧），而非 coll1
	require.NoError(t, ctrl.EnsureLoaded(context.Background(), "coll3", 100.0))
	assert.True(t, ctrl.IsLoaded("coll1"), "coll1 已被 touch，不应被淘汰")
	assert.False(t, ctrl.IsLoaded("coll2"), "coll2 应作为 LRU 被淘汰")
}
```

- [ ] **步骤 2: 运行测试确认失败**

运行: `cd $JUICEFS_ROOT && go test -count=1 ./pkg/gateway/vectorbucket/controller/...`
预期: FAIL

- [ ] **步骤 3: 实现 LoadController**

```go
// pkg/gateway/vectorbucket/controller/load_controller.go
package controller

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// LoadReleaser 是控制器所需的 Adapter 子集接口
type LoadReleaser interface {
	LoadCollection(ctx context.Context, name string) error
	ReleaseCollection(ctx context.Context, name string) error
}

type LoadEntry struct {
	Name          string
	LoadedAt      time.Time
	LastAccessAt  time.Time
	EstMemMB      float64
	InFlightCount int64
}

type LoadController struct {
	mu          sync.Mutex
	adapter     LoadReleaser
	budgetMB    float64
	ttl         time.Duration
	maxLoaded   int
	entries     map[string]*LoadEntry
	lruOrder    []string // 前端 = 最旧，尾部 = 最新
	loadLocks   map[string]*sync.Mutex
	loadLocksMu sync.Mutex
}

func NewLoadController(adapter LoadReleaser, budgetMB int, ttl time.Duration, maxLoaded int) *LoadController {
	return &LoadController{
		adapter:   adapter,
		budgetMB:  float64(budgetMB),
		ttl:       ttl,
		maxLoaded: maxLoaded,
		entries:   make(map[string]*LoadEntry),
		loadLocks: make(map[string]*sync.Mutex),
	}
}

func (c *LoadController) getLoadLock(name string) *sync.Mutex {
	c.loadLocksMu.Lock()
	defer c.loadLocksMu.Unlock()
	if _, ok := c.loadLocks[name]; !ok {
		c.loadLocks[name] = &sync.Mutex{}
	}
	return c.loadLocks[name]
}

func (c *LoadController) IsLoaded(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.entries[name]
	return ok
}

func (c *LoadController) Touch(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[name]; ok {
		e.LastAccessAt = time.Now()
		c.moveLRUToBack(name)
	}
}

func (c *LoadController) InFlightInc(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[name]; ok {
		e.InFlightCount++
	}
}

func (c *LoadController) InFlightDec(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[name]; ok {
		if e.InFlightCount > 0 {
			e.InFlightCount--
		}
	}
}

func (c *LoadController) EnsureLoaded(ctx context.Context, name string, estMemMB float64) error {
	// 快路径：已加载
	c.mu.Lock()
	if _, ok := c.entries[name]; ok {
		c.entries[name].LastAccessAt = time.Now()
		c.moveLRUToBack(name)
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	// 慢路径：获取 per-collection 互斥锁
	lock := c.getLoadLock(name)
	lock.Lock()
	defer lock.Unlock()

	// 双重检查
	c.mu.Lock()
	if _, ok := c.entries[name]; ok {
		c.entries[name].LastAccessAt = time.Now()
		c.moveLRUToBack(name)
		c.mu.Unlock()
		return nil
	}

	// 需要腾空间时淘汰
	if err := c.evictIfNeeded(estMemMB); err != nil {
		c.mu.Unlock()
		return err
	}
	c.mu.Unlock()

	// 调用 Milvus load（在锁外执行，避免阻塞）
	if err := c.adapter.LoadCollection(ctx, name); err != nil {
		return fmt.Errorf("load collection %s: %w", name, err)
	}

	// 注册
	c.mu.Lock()
	now := time.Now()
	c.entries[name] = &LoadEntry{
		Name:         name,
		LoadedAt:     now,
		LastAccessAt: now,
		EstMemMB:     estMemMB,
	}
	c.lruOrder = append(c.lruOrder, name)
	c.mu.Unlock()

	return nil
}

// evictIfNeeded 按 LRU 顺序淘汰 collection 直到预算足够。调用时必须持有 c.mu。
func (c *LoadController) evictIfNeeded(neededMB float64) error {
	for c.usedMB()+neededMB > c.budgetMB || len(c.entries) >= c.maxLoaded {
		if len(c.lruOrder) == 0 {
			return fmt.Errorf("no collections to evict, budget exhausted")
		}

		evicted := false
		for _, candidate := range c.lruOrder {
			entry := c.entries[candidate]
			if entry == nil {
				continue
			}
			if entry.InFlightCount > 0 {
				continue
			}
			if err := c.adapter.ReleaseCollection(context.Background(), candidate); err != nil {
				return fmt.Errorf("release collection %s: %w", candidate, err)
			}
			delete(c.entries, candidate)
			c.removeLRU(candidate)
			evicted = true
			break
		}
		if !evicted {
			return fmt.Errorf("all loaded collections have in-flight queries, cannot evict")
		}
	}
	return nil
}

func (c *LoadController) usedMB() float64 {
	var total float64
	for _, e := range c.entries {
		total += e.EstMemMB
	}
	return total
}

// RunTTLSweep 扫描并释放超过 TTL 且无 in-flight 查询的 collection
func (c *LoadController) RunTTLSweep() {
	c.mu.Lock()
	now := time.Now()
	var toRelease []string
	for name, entry := range c.entries {
		if now.Sub(entry.LastAccessAt) > c.ttl && entry.InFlightCount == 0 {
			toRelease = append(toRelease, name)
		}
	}
	c.mu.Unlock()

	for _, name := range toRelease {
		_ = c.adapter.ReleaseCollection(context.Background(), name)
		c.mu.Lock()
		delete(c.entries, name)
		c.removeLRU(name)
		c.mu.Unlock()
	}
}

// StartTTLSweepLoop 启动后台 goroutine 定时扫描 TTL
func (c *LoadController) StartTTLSweepLoop(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.RunTTLSweep()
			}
		}
	}()
}

// EstimateMemMB 计算 IVF_SQ8 模式下的预估内存占用
// IVF_SQ8: 每个向量 = dim * 1 byte (SQ8) * overhead_ratio
func EstimateMemMB(vectorCount int64, dim int) float64 {
	const overheadRatio = 1.2
	bytes := float64(vectorCount) * float64(dim) * 1.0 * overheadRatio
	return bytes / (1024 * 1024)
}

// --- LRU 辅助方法（调用时必须持有 c.mu）---

func (c *LoadController) moveLRUToBack(name string) {
	c.removeLRU(name)
	c.lruOrder = append(c.lruOrder, name)
}

func (c *LoadController) removeLRU(name string) {
	for i, n := range c.lruOrder {
		if n == name {
			c.lruOrder = append(c.lruOrder[:i], c.lruOrder[i+1:]...)
			return
		}
	}
}
```

- [ ] **步骤 4: 运行测试确认通过**

运行: `cd $JUICEFS_ROOT && go test -count=1 -v ./pkg/gateway/vectorbucket/controller/...`
预期: 全部 PASS

- [ ] **步骤 5: 提交**

```bash
git add pkg/gateway/vectorbucket/controller/
git commit -s -m "feat(vectorbucket): implement LRU+TTL Load/Release Controller with budget management

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## 任务 7：配额管控

**文件:**
- 新建: `pkg/gateway/vectorbucket/quota/quota.go`
- 新建: `pkg/gateway/vectorbucket/quota/quota_test.go`

- [ ] **步骤 1: 编写失败测试**

```go
// pkg/gateway/vectorbucket/quota/quota_test.go
package quota

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/config"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metadata"
)

func newTestChecker(t *testing.T) (*Checker, *metadata.SQLiteStore) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store := metadata.NewSQLiteStore(dbPath)
	require.NoError(t, store.Init(context.Background()))
	t.Cleanup(func() { store.Close() })
	cfg := config.DefaultConfig()
	return NewChecker(store, &cfg), store
}

func TestCanCreateBucket(t *testing.T) {
	checker, _ := newTestChecker(t)
	ctx := context.Background()

	err := checker.CanCreateBucket(ctx, "owner1")
	assert.NoError(t, err)
}

func TestCanCreateBucketExceedsPerTenantLimit(t *testing.T) {
	checker, store := newTestChecker(t)
	ctx := context.Background()

	// 填满单租户上限 (100)
	for i := 0; i < 100; i++ {
		require.NoError(t, store.CreateBucket(ctx, &metadata.Bucket{
			ID: fmt.Sprintf("b%d", i), Name: fmt.Sprintf("bkt-%d", i),
			Owner: "owner1", Status: metadata.BucketStatusReady,
		}))
	}

	err := checker.CanCreateBucket(ctx, "owner1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "per-tenant bucket limit")
}

func TestCanCreateCollection(t *testing.T) {
	checker, store := newTestChecker(t)
	ctx := context.Background()

	require.NoError(t, store.CreateBucket(ctx, &metadata.Bucket{
		ID: "b1", Name: "bkt", Owner: "u1", Status: metadata.BucketStatusReady,
	}))

	err := checker.CanCreateCollection(ctx, "b1")
	assert.NoError(t, err)
}

func TestCanCreateCollectionExceedsLimit(t *testing.T) {
	checker, store := newTestChecker(t)
	ctx := context.Background()

	require.NoError(t, store.CreateBucket(ctx, &metadata.Bucket{
		ID: "b1", Name: "bkt", Owner: "u1", Status: metadata.BucketStatusReady,
	}))

	for i := 0; i < 50; i++ {
		require.NoError(t, store.CreateCollection(ctx, &metadata.LogicalCollection{
			ID: fmt.Sprintf("c%d", i), BucketID: "b1", Name: fmt.Sprintf("coll-%d", i),
			Dim: 768, Metric: "COSINE", Status: metadata.CollStatusReady,
			PhysicalName: fmt.Sprintf("vb_b1_c%d", i),
		}))
	}

	err := checker.CanCreateCollection(ctx, "b1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "collection limit")
}

func TestCheckDimension(t *testing.T) {
	checker, _ := newTestChecker(t)
	assert.NoError(t, checker.CheckDimension(768))
	assert.NoError(t, checker.CheckDimension(1536))
	assert.Error(t, checker.CheckDimension(2048))
	assert.Error(t, checker.CheckDimension(0))
}

func TestCheckVectorCount(t *testing.T) {
	checker, _ := newTestChecker(t)
	assert.NoError(t, checker.CheckVectorCount(999999, 1))
	assert.Error(t, checker.CheckVectorCount(999999, 2)) // 999999 + 2 > 1000000
}
```

- [ ] **步骤 2: 运行测试确认失败**

运行: `cd $JUICEFS_ROOT && go test -count=1 ./pkg/gateway/vectorbucket/quota/...`
预期: FAIL

- [ ] **步骤 3: 实现 Checker**

```go
// pkg/gateway/vectorbucket/quota/quota.go
package quota

import (
	"context"
	"fmt"

	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/config"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metadata"
)

type Checker struct {
	store metadata.Store
	cfg   *config.Config
}

func NewChecker(store metadata.Store, cfg *config.Config) *Checker {
	return &Checker{store: store, cfg: cfg}
}

func (c *Checker) CanCreateBucket(ctx context.Context, owner string) error {
	globalCount, err := c.store.CountBuckets(ctx)
	if err != nil {
		return fmt.Errorf("count buckets: %w", err)
	}
	if globalCount >= c.cfg.MaxBucketsGlobal {
		return fmt.Errorf("global bucket limit reached (%d)", c.cfg.MaxBucketsGlobal)
	}

	ownerCount, err := c.store.CountBucketsByOwner(ctx, owner)
	if err != nil {
		return fmt.Errorf("count buckets by owner: %w", err)
	}
	if ownerCount >= c.cfg.MaxBucketsPerTenant {
		return fmt.Errorf("per-tenant bucket limit reached (%d)", c.cfg.MaxBucketsPerTenant)
	}

	return nil
}

func (c *Checker) CanCreateCollection(ctx context.Context, bucketID string) error {
	cnt, err := c.store.CountCollections(ctx, bucketID)
	if err != nil {
		return fmt.Errorf("count collections: %w", err)
	}
	if cnt >= c.cfg.MaxCollPerBucket {
		return fmt.Errorf("collection limit per bucket reached (%d)", c.cfg.MaxCollPerBucket)
	}
	return nil
}

func (c *Checker) CheckDimension(dim int) error {
	if dim <= 0 {
		return fmt.Errorf("dimension must be positive, got %d", dim)
	}
	if dim > c.cfg.MaxDim {
		return fmt.Errorf("dimension %d exceeds maximum %d", dim, c.cfg.MaxDim)
	}
	return nil
}

func (c *Checker) CheckVectorCount(currentCount int64, insertCount int) error {
	if currentCount+int64(insertCount) > int64(c.cfg.MaxVectorsPerColl) {
		return fmt.Errorf("insert would exceed max vectors per collection (%d)", c.cfg.MaxVectorsPerColl)
	}
	return nil
}

func (c *Checker) CheckMetric(metric string) error {
	switch metric {
	case "COSINE", "L2":
		return nil
	default:
		return fmt.Errorf("unsupported metric type %q, must be COSINE or L2", metric)
	}
}
```

- [ ] **步骤 4: 运行测试确认通过**

运行: `cd $JUICEFS_ROOT && go test -count=1 -v ./pkg/gateway/vectorbucket/quota/...`
预期: PASS

- [ ] **步骤 5: 提交**

```bash
git add pkg/gateway/vectorbucket/quota/
git commit -s -m "feat(vectorbucket): add quota enforcement for buckets, collections, dimensions, and vector counts

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## 任务 8：Prometheus 监控指标

**文件:**
- 新建: `pkg/gateway/vectorbucket/metrics/metrics.go`

- [ ] **步骤 1: 定义指标**

```go
// pkg/gateway/vectorbucket/metrics/metrics.go
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	BucketCount = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "vb_bucket_count",
		Help: "活跃 bucket 数量",
	})

	LogicalCollectionCount = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "vb_logical_collection_count",
		Help: "按状态统计的 logical collection 数量",
	}, []string{"status"})

	LoadedCollectionCount = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "vb_loaded_collection_count",
		Help: "当前已 load 的 collection 数量",
	})

	LoadDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "vb_load_duration_seconds",
		Help:    "LoadCollection 调用耗时",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 10), // 0.1s 到 ~51s
	})

	QueryDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "vb_query_duration_seconds",
		Help:    "查询各阶段耗时",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 15), // 1ms 到 ~16s
	}, []string{"phase"})

	ReleaseEvictions = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "vb_release_evictions_total",
		Help: "按原因统计的 collection release 次数",
	}, []string{"reason"})

	CollectionMemEstimate = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "vb_collection_mem_estimate_mb",
		Help: "单 collection 预估内存占用（MB）",
	}, []string{"collection"})

	InsertTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "vb_insert_total",
		Help: "插入向量总数",
	}, []string{"bucket", "collection"})

	QueryTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "vb_query_total",
		Help: "查询请求总数",
	}, []string{"bucket", "collection"})

	ErrorTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "vb_error_total",
		Help: "按类型统计的错误总数",
	}, []string{"type"})
)
```

- [ ] **步骤 2: 验证编译通过**

运行: `cd $JUICEFS_ROOT && go build ./pkg/gateway/vectorbucket/metrics/`
预期: 成功

- [ ] **步骤 3: 提交**

```bash
git add pkg/gateway/vectorbucket/metrics/
git commit -s -m "feat(vectorbucket): add Prometheus metric definitions

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## 任务 9：协议契约 + 错误映射

**文件:**
- 新建: `pkg/gateway/vectorbucket/protocol.go`
- 新建: `pkg/gateway/vectorbucket/errors.go`
- 修改: `pkg/gateway/gateway.go`

> **收口目标:** 这里不再出现 `gin.Context` 或独立 handler helper。`protocol.go` 只定义 `juicedata/minio` shim 和 JuiceFS runtime 之间的接口、请求结构、响应结构；`errors.go` 只定义内部错误种类与对外协议错误映射。协议名、字段名和操作名直接对齐 AWS 当前公开 API：`CreateVectorBucket`、`GetVectorBucket`、`DeleteVectorBucket`、`CreateIndex`、`DeleteIndex`、`PutVectors`、`DeleteVectors`、`QueryVectors`。

> **本地联调约束:** 从本任务开始，如果需要改 minio 侧协议层，先在 JuiceFS `go.mod` 暂时把 `github.com/minio/minio` 的 `replace` 改到本地 `$JUICEDATA_MINIO_ROOT`；不要把 minio 代码复制进当前仓库。

- [ ] **步骤 1: 在 `protocol.go` 定义运行时扩展契约**

```go
// pkg/gateway/vectorbucket/protocol.go
package vectorbucket

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type RequestContext struct {
	AccountID string
	Region    string
	RequestID string
	Headers   http.Header
}

type Target struct {
	VectorBucketName string `json:"vectorBucketName,omitempty"`
	VectorBucketARN  string `json:"vectorBucketArn,omitempty"`
	IndexName        string `json:"indexName,omitempty"`
	IndexARN         string `json:"indexArn,omitempty"`
}

func (t Target) ResolveNames() (bucketName string, indexName string, err error) {
	if t.VectorBucketName != "" {
		bucketName = t.VectorBucketName
	}
	if t.IndexName != "" {
		indexName = t.IndexName
	}
	if bucketName != "" {
		return bucketName, indexName, nil
	}
	if t.IndexARN != "" {
		parts := strings.Split(strings.TrimPrefix(t.IndexARN, "arn:aws:s3vectors:"), ":")
		if len(parts) < 3 {
			return "", "", fmt.Errorf("%w: invalid indexArn", ErrValidation)
		}
		resource := parts[2]
		tokens := strings.Split(resource, "/")
		if len(tokens) != 4 || tokens[0] != "bucket" || tokens[2] != "index" {
			return "", "", fmt.Errorf("%w: invalid indexArn resource", ErrValidation)
		}
		return tokens[1], tokens[3], nil
	}
	if t.VectorBucketARN != "" {
		parts := strings.Split(strings.TrimPrefix(t.VectorBucketARN, "arn:aws:s3vectors:"), ":")
		if len(parts) < 3 {
			return "", "", fmt.Errorf("%w: invalid vectorBucketArn", ErrValidation)
		}
		resource := parts[2]
		tokens := strings.Split(resource, "/")
		if len(tokens) != 2 || tokens[0] != "bucket" {
			return "", "", fmt.Errorf("%w: invalid vectorBucketArn resource", ErrValidation)
		}
		return tokens[1], "", nil
	}
	return "", "", fmt.Errorf("%w: missing vector bucket target", ErrValidation)
}

type EncryptionConfiguration struct {
	SSEType   string `json:"sseType,omitempty"`
	KMSKeyARN string `json:"kmsKeyArn,omitempty"`
}

type MetadataConfiguration struct {
	NonFilterableMetadataKeys []string `json:"nonFilterableMetadataKeys,omitempty"`
}

type VectorData struct {
	Float32 []float32 `json:"float32"`
}

type PutInputVector struct {
	Key      string          `json:"key"`
	Data     VectorData      `json:"data"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

type QueryResultVector struct {
	Key      string          `json:"key"`
	Distance float32         `json:"distance,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

type CreateVectorBucketRequest struct {
	RequestContext
	VectorBucketName         string                   `json:"vectorBucketName"`
	EncryptionConfiguration  *EncryptionConfiguration `json:"encryptionConfiguration,omitempty"`
	Tags                     map[string]string        `json:"tags,omitempty"`
}

type CreateVectorBucketResponse struct {
	VectorBucketARN string `json:"vectorBucketArn"`
}

type GetVectorBucketRequest struct {
	RequestContext
	Target
}

type GetVectorBucketResponse struct {
	VectorBucketARN        string                   `json:"vectorBucketArn"`
	VectorBucketName       string                   `json:"vectorBucketName"`
	CreationTime           string                   `json:"creationTime"`
	EncryptionConfiguration *EncryptionConfiguration `json:"encryptionConfiguration,omitempty"`
}

type ListVectorBucketsRequest struct {
	RequestContext
	MaxResults int    `json:"maxResults,omitempty"`
	NextToken  string `json:"nextToken,omitempty"`
}

type VectorBucketSummary struct {
	VectorBucketARN  string `json:"vectorBucketArn"`
	VectorBucketName string `json:"vectorBucketName"`
	CreationTime     string `json:"creationTime"`
}

type ListVectorBucketsResponse struct {
	VectorBuckets []VectorBucketSummary `json:"vectorBuckets"`
	NextToken     string                `json:"nextToken,omitempty"`
}

type DeleteVectorBucketRequest struct {
	RequestContext
	Target
}

type CreateIndexRequest struct {
	RequestContext
	Target
	IndexName                string                   `json:"indexName"`
	DataType                 string                   `json:"dataType"`
	Dimension                int                      `json:"dimension"`
	DistanceMetric           string                   `json:"distanceMetric"`
	EncryptionConfiguration  *EncryptionConfiguration `json:"encryptionConfiguration,omitempty"`
	MetadataConfiguration    *MetadataConfiguration   `json:"metadataConfiguration,omitempty"`
	Tags                     map[string]string        `json:"tags,omitempty"`
}

type CreateIndexResponse struct {
	IndexARN string `json:"indexArn"`
}

type DeleteIndexRequest struct {
	RequestContext
	Target
}

type PutVectorsRequest struct {
	RequestContext
	Target
	Vectors []PutInputVector `json:"vectors"`
}

type DeleteVectorsRequest struct {
	RequestContext
	Target
	Keys []string `json:"keys"`
}

type QueryVectorsRequest struct {
	RequestContext
	Target
	QueryVector    VectorData       `json:"queryVector"`
	TopK           int              `json:"topK"`
	Filter         json.RawMessage  `json:"filter,omitempty"`
	ReturnDistance bool             `json:"returnDistance,omitempty"`
	ReturnMetadata bool             `json:"returnMetadata,omitempty"`
}

type QueryVectorsResponse struct {
	DistanceMetric string              `json:"distanceMetric"`
	Vectors        []QueryResultVector `json:"vectors"`
}

type Extension interface {
	CreateVectorBucket(context.Context, *CreateVectorBucketRequest) (*CreateVectorBucketResponse, error)
	GetVectorBucket(context.Context, *GetVectorBucketRequest) (*GetVectorBucketResponse, error)
	ListVectorBuckets(context.Context, *ListVectorBucketsRequest) (*ListVectorBucketsResponse, error)
	DeleteVectorBucket(context.Context, *DeleteVectorBucketRequest) error
	CreateIndex(context.Context, *CreateIndexRequest) (*CreateIndexResponse, error)
	DeleteIndex(context.Context, *DeleteIndexRequest) error
	PutVectors(context.Context, *PutVectorsRequest) error
	DeleteVectors(context.Context, *DeleteVectorsRequest) error
	QueryVectors(context.Context, *QueryVectorsRequest) (*QueryVectorsResponse, error)
}
```

- [ ] **步骤 2: 在 `errors.go` 定义内部错误与协议映射**

```go
// pkg/gateway/vectorbucket/errors.go
package vectorbucket

import (
	"errors"
	"net/http"
)

var (
	ErrValidation         = errors.New("validation failed")
	ErrConflict           = errors.New("resource already exists")
	ErrNotFound           = errors.New("resource not found")
	ErrQuotaExceeded      = errors.New("service quota exceeded")
	ErrTooManyRequests    = errors.New("request throttled")
	ErrServiceUnavailable = errors.New("service unavailable")
	ErrAccessDenied       = errors.New("access denied")
	ErrRequestTimeout     = errors.New("request timeout")
	ErrInternal           = errors.New("internal server error")
)

type APIError struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Status   int    `json:"-"`
	RetryAfter int  `json:"-"`
}

func TranslateError(err error) APIError {
	switch {
	case err == nil:
		return APIError{}
	case errors.Is(err, ErrValidation):
		return APIError{Code: "ValidationException", Message: err.Error(), Status: http.StatusBadRequest}
	case errors.Is(err, ErrConflict):
		return APIError{Code: "ConflictException", Message: err.Error(), Status: http.StatusConflict}
	case errors.Is(err, ErrNotFound):
		return APIError{Code: "NotFoundException", Message: err.Error(), Status: http.StatusNotFound}
	case errors.Is(err, ErrQuotaExceeded):
		return APIError{Code: "ServiceQuotaExceededException", Message: err.Error(), Status: http.StatusPaymentRequired}
	case errors.Is(err, ErrTooManyRequests):
		return APIError{Code: "TooManyRequestsException", Message: err.Error(), Status: http.StatusTooManyRequests}
	case errors.Is(err, ErrServiceUnavailable):
		return APIError{Code: "ServiceUnavailableException", Message: err.Error(), Status: http.StatusServiceUnavailable, RetryAfter: 5}
	case errors.Is(err, ErrAccessDenied):
		return APIError{Code: "AccessDeniedException", Message: err.Error(), Status: http.StatusForbidden}
	case errors.Is(err, ErrRequestTimeout):
		return APIError{Code: "RequestTimeoutException", Message: err.Error(), Status: http.StatusRequestTimeout}
	default:
		return APIError{Code: "InternalServerException", Message: err.Error(), Status: http.StatusInternalServerError}
	}
}
```

- [ ] **步骤 3: 在 `pkg/gateway/gateway.go` 增加 shim 可发现接口**

```go
// pkg/gateway/gateway.go
package gateway

import "github.com/juicedata/juicefs/pkg/gateway/vectorbucket"

type vectorBucketProvider interface {
	VectorBucketExtension() vectorbucket.Extension
}
```

- [ ] **步骤 4: 验证编译通过**

运行: `cd $JUICEFS_ROOT && go build ./pkg/gateway/vectorbucket/... ./pkg/gateway/...`
预期: 成功

- [ ] **步骤 5: 提交**

```bash
git add pkg/gateway/vectorbucket/protocol.go pkg/gateway/vectorbucket/errors.go pkg/gateway/gateway.go
git commit -s -m "feat(vectorbucket): define vector bucket protocol contract and error mapping

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## 任务 10：`jfsObjects` 装配与 Runtime 挂接

> **S3 API 映射约定:** 本任务到任务 16 的目标不是保留 `/v1/...` REST 路径，而是在 JuiceFS S3 gateway 上实现与 AWS S3 Vectors 独立操作对齐的协议路径。Phase 1 的主操作集应至少包含 `CreateVectorBucket`、`GetVectorBucket`、`DeleteVectorBucket`、`CreateIndex`、`DeleteIndex`、`PutVectors`、`DeleteVectors`、`QueryVectors`；若示例测试仍用 `/v1/...`，执行时应改写为 `juicedata/minio` shim 侧协议测试、`jfsObjects` 级测试或 `gateway_test.go` 集成测试。

**文件:**
- 修改: `pkg/gateway/gateway.go`
- 新建: `pkg/gateway/vectorbucket/runtime.go`
- 新建: `pkg/gateway/vectorbucket/runtime_test.go`

> **实际落点修正:** 这里的目标只有两个：第一，把 `vectorbucket.Extension` 挂到真实的 `jfsObjects` 上；第二，把 metadata/router/controller/adapter/quota 组装成一个可被 shim 调用的 `Runtime`。不要引入任何 `api.NewServer()`、`Engine()` 或独立端口。

- [ ] **步骤 1: 在 `runtime.go` 组装 Runtime 和各 service**

```go
// pkg/gateway/vectorbucket/runtime.go
package vectorbucket

import (
	"context"

	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/adapter"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/config"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/controller"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metadata"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/quota"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/router"
)

type Runtime struct {
	cfg        config.Config
	store      metadata.Store
	router     *router.NamespaceRouter
	adapter    adapter.Adapter
	controller *controller.LoadController
	quota      *quota.Checker
	buckets    *BucketService
	objects    *ObjectService
	query      *QueryService
}

func NewRuntime(cfg config.Config, store metadata.Store, milvus adapter.Adapter, ctrl *controller.LoadController) *Runtime {
	r := &Runtime{
		cfg:        cfg,
		store:      store,
		router:     router.NewNamespaceRouter(store),
		adapter:    milvus,
		controller: ctrl,
		quota:      quota.NewChecker(store, &cfg),
	}
	r.buckets = NewBucketService(store, r.quota)
	r.objects = NewObjectService(store, r.router, milvus, ctrl, r.quota, cfg)
	r.query = NewQueryService(store, r.router, milvus, ctrl)
	return r
}

func (r *Runtime) Close(context.Context) error {
	if r.store != nil {
		return r.store.Close()
	}
	return nil
}

func (r *Runtime) CreateVectorBucket(ctx context.Context, req *CreateVectorBucketRequest) (*CreateVectorBucketResponse, error) {
	return r.buckets.CreateVectorBucket(ctx, req)
}

func (r *Runtime) GetVectorBucket(ctx context.Context, req *GetVectorBucketRequest) (*GetVectorBucketResponse, error) {
	return r.buckets.GetVectorBucket(ctx, req)
}

func (r *Runtime) ListVectorBuckets(ctx context.Context, req *ListVectorBucketsRequest) (*ListVectorBucketsResponse, error) {
	return r.buckets.ListVectorBuckets(ctx, req)
}

func (r *Runtime) DeleteVectorBucket(ctx context.Context, req *DeleteVectorBucketRequest) error {
	return r.buckets.DeleteVectorBucket(ctx, req)
}

func (r *Runtime) CreateIndex(ctx context.Context, req *CreateIndexRequest) (*CreateIndexResponse, error) {
	return r.objects.CreateIndex(ctx, req)
}

func (r *Runtime) DeleteIndex(ctx context.Context, req *DeleteIndexRequest) error {
	return r.objects.DeleteIndex(ctx, req)
}

func (r *Runtime) PutVectors(ctx context.Context, req *PutVectorsRequest) error {
	return r.objects.PutVectors(ctx, req)
}

func (r *Runtime) DeleteVectors(ctx context.Context, req *DeleteVectorsRequest) error {
	return r.objects.DeleteVectors(ctx, req)
}

func (r *Runtime) QueryVectors(ctx context.Context, req *QueryVectorsRequest) (*QueryVectorsResponse, error) {
	return r.query.QueryVectors(ctx, req)
}
```

- [ ] **步骤 2: 在 `pkg/gateway/gateway.go` 挂上 runtime 引用**

```go
// pkg/gateway/gateway.go
package gateway

import "github.com/juicedata/juicefs/pkg/gateway/vectorbucket"

type Config struct {
	MultiBucket  bool
	KeepEtag     bool
	Umask        uint16
	ObjTag       bool
	ObjMeta      bool
	HeadDir      bool
	HideDir      bool
	ReadOnly     bool
	VectorBucket vectorbucket.Extension
}

type jfsObjects struct {
	conf         *vfs.Config
	fs           *fs.FileSystem
	listPool     *minio.TreeWalkPool
	nsMutex      *minio.NsLockMap
	gConf        *Config
	vectorbucket vectorbucket.Extension
}

func NewJFSGateway(jfs *fs.FileSystem, conf *vfs.Config, gConf *Config) (minio.ObjectLayer, error) {
	mctx = meta.NewContext(uint32(os.Getpid()), uint32(os.Getuid()), []uint32{uint32(os.Getgid())})
	jfsObj := &jfsObjects{
		fs:           jfs,
		conf:         conf,
		listPool:     minio.NewTreeWalkPool(time.Minute * 30),
		gConf:        gConf,
		nsMutex:      minio.NewNSLock(false),
		vectorbucket: gConf.VectorBucket,
	}
	go jfsObj.cleanup()
	return jfsObj, nil
}

func (n *jfsObjects) VectorBucketExtension() vectorbucket.Extension {
	return n.vectorbucket
}
```

- [ ] **步骤 3: 增加运行时挂接测试**

```go
// pkg/gateway/vectorbucket/runtime_test.go
package vectorbucket

import "testing"

func TestRuntimeImplementsExtension(t *testing.T) {
	var _ Extension = (*Runtime)(nil)
}
```

```go
// pkg/gateway/gateway_test.go
package gateway

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket"
)

type stubVectorExtension struct{}

func (stubVectorExtension) CreateVectorBucket(ctx context.Context, req *vectorbucket.CreateVectorBucketRequest) (*vectorbucket.CreateVectorBucketResponse, error) {
	return nil, nil
}
func (stubVectorExtension) GetVectorBucket(ctx context.Context, req *vectorbucket.GetVectorBucketRequest) (*vectorbucket.GetVectorBucketResponse, error) {
	return nil, nil
}
func (stubVectorExtension) ListVectorBuckets(ctx context.Context, req *vectorbucket.ListVectorBucketsRequest) (*vectorbucket.ListVectorBucketsResponse, error) {
	return nil, nil
}
func (stubVectorExtension) DeleteVectorBucket(ctx context.Context, req *vectorbucket.DeleteVectorBucketRequest) error {
	return nil
}
func (stubVectorExtension) CreateIndex(ctx context.Context, req *vectorbucket.CreateIndexRequest) (*vectorbucket.CreateIndexResponse, error) {
	return nil, nil
}
func (stubVectorExtension) DeleteIndex(ctx context.Context, req *vectorbucket.DeleteIndexRequest) error {
	return nil
}
func (stubVectorExtension) PutVectors(ctx context.Context, req *vectorbucket.PutVectorsRequest) error {
	return nil
}
func (stubVectorExtension) DeleteVectors(ctx context.Context, req *vectorbucket.DeleteVectorsRequest) error {
	return nil
}
func (stubVectorExtension) QueryVectors(ctx context.Context, req *vectorbucket.QueryVectorsRequest) (*vectorbucket.QueryVectorsResponse, error) {
	return nil, nil
}

func TestGatewayCarriesVectorBucketExtension(t *testing.T) {
	ext := stubVectorExtension{}
	obj := &jfsObjects{gConf: &Config{VectorBucket: ext}, vectorbucket: ext}
	require.Same(t, ext, obj.VectorBucketExtension())
}
```

- [ ] **步骤 4: 运行测试**

运行: `cd $JUICEFS_ROOT && go test -count=1 -v ./pkg/gateway/vectorbucket/... ./pkg/gateway/...`
预期: PASS

- [ ] **步骤 5: 提交**

```bash
git add pkg/gateway/gateway.go pkg/gateway/gateway_test.go pkg/gateway/vectorbucket/runtime.go pkg/gateway/vectorbucket/runtime_test.go
git commit -s -m "feat(vectorbucket): attach runtime to jfs gateway object layer

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## 任务 11：Vector Bucket 管理协议 Shim

**文件:**
- 修改: `pkg/gateway/gateway.go`
- 新建: `pkg/gateway/vectorbucket/bucket_service.go`
- 新建: `pkg/gateway/vectorbucket/bucket_service_test.go`
- 修改: `pkg/gateway/gateway_test.go`
- 可能新增: `$JUICEDATA_MINIO_ROOT/cmd/s3vectors_handlers.go`
- 可能新增: `$JUICEDATA_MINIO_ROOT/cmd/s3vectors_handlers_test.go`

> **实际落点修正:** 这里不应再以标准 `MakeBucketWithLocation` / `GetBucketInfo` / `DeleteBucket` 为主入口来模拟 Vector Bucket。按照 AWS 当前协议，外部能力应优先对齐 `CreateVectorBucket`、`GetVectorBucket`、`DeleteVectorBucket`、`ListVectorBuckets`。JuiceFS 侧负责承接这些操作后的 metadata 注册、ARN 生成、配额检查和删除前校验；如果 `juicedata/minio` fork 缺少这些操作的解析，则本任务要先补 shim。

> **目录约束:** 这里涉及的 shim 文件位于本地 `$JUICEDATA_MINIO_ROOT/cmd/`，只作为联调目标目录，不纳入当前仓库的 git 变更范围。

- [ ] **步骤 1: 在 `bucket_service.go` 实现 vector bucket 生命周期**

```go
// pkg/gateway/vectorbucket/bucket_service.go
package vectorbucket

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metadata"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/quota"
)

type BucketService struct {
	store metadata.Store
	quota *quota.Checker
}

func NewBucketService(store metadata.Store, q *quota.Checker) *BucketService {
	return &BucketService{store: store, quota: q}
}

func (s *BucketService) CreateVectorBucket(ctx context.Context, req *CreateVectorBucketRequest) (*CreateVectorBucketResponse, error) {
	if len(req.VectorBucketName) < 3 {
		return nil, fmt.Errorf("%w: vectorBucketName too short", ErrValidation)
	}
	if err := s.quota.CanCreateBucket(ctx, req.AccountID); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrQuotaExceeded, err)
	}

	bucket := &metadata.Bucket{
		ID:        uuid.NewString(),
		Name:      req.VectorBucketName,
		Owner:     req.AccountID,
		Status:    metadata.BucketStatusReady,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.store.CreateBucket(ctx, bucket); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConflict, err)
	}
	return &CreateVectorBucketResponse{
		VectorBucketARN: fmt.Sprintf("arn:aws:s3vectors:%s:%s:bucket/%s", req.Region, req.AccountID, req.VectorBucketName),
	}, nil
}

func (s *BucketService) GetVectorBucket(ctx context.Context, req *GetVectorBucketRequest) (*GetVectorBucketResponse, error) {
	bucketName, _, err := req.Target.ResolveNames()
	if err != nil {
		return nil, err
	}
	bucket, err := s.store.GetBucketByName(ctx, bucketName)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	return &GetVectorBucketResponse{
		VectorBucketARN:  fmt.Sprintf("arn:aws:s3vectors:%s:%s:bucket/%s", req.Region, req.AccountID, bucket.Name),
		VectorBucketName: bucket.Name,
		CreationTime:     bucket.CreatedAt.UTC().Format(time.RFC3339),
	}, nil
}

func (s *BucketService) ListVectorBuckets(ctx context.Context, req *ListVectorBucketsRequest) (*ListVectorBucketsResponse, error) {
	rows, err := s.store.ListBuckets(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInternal, err)
	}
	resp := &ListVectorBucketsResponse{VectorBuckets: make([]VectorBucketSummary, 0, len(rows))}
	for _, bucket := range rows {
		resp.VectorBuckets = append(resp.VectorBuckets, VectorBucketSummary{
			VectorBucketARN:  fmt.Sprintf("arn:aws:s3vectors:%s:%s:bucket/%s", req.Region, req.AccountID, bucket.Name),
			VectorBucketName: bucket.Name,
			CreationTime:     bucket.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return resp, nil
}

func (s *BucketService) DeleteVectorBucket(ctx context.Context, req *DeleteVectorBucketRequest) error {
	bucketName, _, err := req.Target.ResolveNames()
	if err != nil {
		return err
	}
	bucket, err := s.store.GetBucketByName(ctx, bucketName)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	count, err := s.store.CountCollections(ctx, bucket.ID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInternal, err)
	}
	if count > 0 {
		return errors.Join(ErrConflict, errors.New("delete all vector indexes before deleting the vector bucket"))
	}
	if err := s.store.DeleteBucket(ctx, bucket.ID); err != nil {
		return fmt.Errorf("%w: %v", ErrInternal, err)
	}
	return nil
}
```

- [ ] **步骤 2: 在 `juicedata/minio` shim 注册 bucket 级协议入口**

```go
// $JUICEDATA_MINIO_ROOT/cmd/s3vectors_handlers.go
package cmd

import (
	"encoding/json"
	"net/http"

	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket"
)

func registerS3VectorsHandlers(router *mux.Router, api objectAPIHandlers) {
	router.Methods(http.MethodPost).Path("/CreateVectorBucket").HandlerFunc(api.CreateVectorBucketHandler)
	router.Methods(http.MethodPost).Path("/GetVectorBucket").HandlerFunc(api.GetVectorBucketHandler)
	router.Methods(http.MethodPost).Path("/ListVectorBuckets").HandlerFunc(api.ListVectorBucketsHandler)
	router.Methods(http.MethodPost).Path("/DeleteVectorBucket").HandlerFunc(api.DeleteVectorBucketHandler)
}

func (api objectAPIHandlers) vectorExtension() (vectorbucket.Extension, error) {
	objAPI := api.ObjectAPI()
	provider, ok := objAPI.(interface{ VectorBucketExtension() vectorbucket.Extension })
	if !ok || provider.VectorBucketExtension() == nil {
		return nil, errVectorBucketNotEnabled
	}
	return provider.VectorBucketExtension(), nil
}

func (api objectAPIHandlers) CreateVectorBucketHandler(w http.ResponseWriter, r *http.Request) {
	var req vectorbucket.CreateVectorBucketRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeVectorAPIError(w, vectorbucket.TranslateError(vectorbucket.ErrValidation))
		return
	}
	req.RequestID = mustGetRequestID(r)
	req.Region = globalSite.Region()
	req.AccountID = mustResolveAccountID(r)

	ext, err := api.vectorExtension()
	if err != nil {
		writeVectorAPIError(w, vectorbucket.TranslateError(err))
		return
	}
	resp, err := ext.CreateVectorBucket(r.Context(), &req)
	if err != nil {
		writeVectorAPIError(w, vectorbucket.TranslateError(err))
		return
	}
	writeVectorJSON(w, http.StatusOK, resp)
}
```

- [ ] **步骤 3: 增加 bucket 生命周期测试**

```go
// pkg/gateway/vectorbucket/bucket_service_test.go
package vectorbucket

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/config"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metadata"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/quota"
)

func TestBucketServiceLifecycle(t *testing.T) {
	db := filepath.Join(t.TempDir(), "vectorbucket.db")
	store := metadata.NewSQLiteStore(db)
	require.NoError(t, store.Init(context.Background()))
	t.Cleanup(func() { _ = store.Close() })

	cfg := config.DefaultConfig()
	svc := NewBucketService(store, quota.NewChecker(store, &cfg))

	createResp, err := svc.CreateVectorBucket(context.Background(), &CreateVectorBucketRequest{
		RequestContext:   RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		VectorBucketName: "demo-bucket",
	})
	require.NoError(t, err)
	require.Contains(t, createResp.VectorBucketARN, "bucket/demo-bucket")

	getResp, err := svc.GetVectorBucket(context.Background(), &GetVectorBucketRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         Target{VectorBucketName: "demo-bucket"},
	})
	require.NoError(t, err)
	require.Equal(t, "demo-bucket", getResp.VectorBucketName)

	listResp, err := svc.ListVectorBuckets(context.Background(), &ListVectorBucketsRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
	})
	require.NoError(t, err)
	require.Len(t, listResp.VectorBuckets, 1)

	require.NoError(t, svc.DeleteVectorBucket(context.Background(), &DeleteVectorBucketRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         Target{VectorBucketName: "demo-bucket"},
	}))
}
```

- [ ] **步骤 4: 运行测试**

运行: `cd $JUICEFS_ROOT && go test -count=1 -v ./pkg/gateway/vectorbucket/... ./pkg/gateway/...`
预期: PASS

- [ ] **步骤 5: 提交**

```bash
git add pkg/gateway/gateway_test.go pkg/gateway/vectorbucket/bucket_service.go pkg/gateway/vectorbucket/bucket_service_test.go
git commit -s -m "feat(vectorbucket): add vector bucket lifecycle service and shim hooks

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## 任务 12：Index 管理与写入协议 Shim（CreateIndex / DeleteIndex / PutVectors / DeleteVectors）

**文件:**
- 修改: `pkg/gateway/gateway.go`
- 新建: `pkg/gateway/vectorbucket/object_service.go`
- 新建: `pkg/gateway/vectorbucket/object_service_test.go`
- 修改: `pkg/gateway/gateway_test.go`
- 可能新增: `$JUICEDATA_MINIO_ROOT/cmd/s3vectors_handlers.go`

> **实际落点修正:** AWS 当前写入主路径不是普通 `PutObject`，而是 `CreateIndex`、`DeleteIndex`、`PutVectors`、`DeleteVectors` 这些独立操作。因此本任务应优先设计并实现这些操作的 shim 和 JuiceFS runtime 对接；只有在内部需要落地 metadata/tagging/持久化信息时，才复用普通对象接口。不要把向量写入错误地等同于通用对象上传。

> **内部术语对齐:** 这里的 AWS `Index` 在 Phase 1 内部继续复用现有 `metadata.LogicalCollection` 模型；也就是说 `LogicalCollection.Name == indexName`，`LogicalCollection.PhysicalName` 是 Milvus 真正 collection 名。

- [ ] **步骤 1: 在 `object_service.go` 实现 `CreateIndex/DeleteIndex/PutVectors/DeleteVectors`**

```go
// pkg/gateway/vectorbucket/object_service.go
package vectorbucket

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/adapter"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/config"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/controller"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metadata"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/quota"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/router"
)

type ObjectService struct {
	store      metadata.Store
	router     *router.NamespaceRouter
	adapter    adapter.Adapter
	controller *controller.LoadController
	quota      *quota.Checker
	cfg        config.Config
}

func NewObjectService(store metadata.Store, ns *router.NamespaceRouter, milvus adapter.Adapter, ctrl *controller.LoadController, q *quota.Checker, cfg config.Config) *ObjectService {
	return &ObjectService{store: store, router: ns, adapter: milvus, controller: ctrl, quota: q, cfg: cfg}
}

func (s *ObjectService) CreateIndex(ctx context.Context, req *CreateIndexRequest) (*CreateIndexResponse, error) {
	if req.DataType != "float32" {
		return nil, fmt.Errorf("%w: dataType must be float32", ErrValidation)
	}
	if req.Dimension < 1 || req.Dimension > 4096 {
		return nil, fmt.Errorf("%w: dimension out of range", ErrValidation)
	}
	if req.DistanceMetric != "cosine" && req.DistanceMetric != "euclidean" {
		return nil, fmt.Errorf("%w: unsupported distanceMetric", ErrValidation)
	}

	bucketName, _, err := req.Target.ResolveNames()
	if err != nil {
		return nil, err
	}
	bucket, err := s.store.GetBucketByName(ctx, bucketName)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	if err := s.quota.CanCreateCollection(ctx, bucket.ID); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrQuotaExceeded, err)
	}

	indexID := uuid.NewString()
	physicalName := router.PhysicalCollectionName(bucket.ID, indexID)
	indexMeta := &metadata.LogicalCollection{
		ID:           indexID,
		BucketID:     bucket.ID,
		Name:         req.IndexName,
		Dim:          req.Dimension,
		Metric:       strings.ToUpper(req.DistanceMetric),
		Status:       metadata.CollStatusInit,
		PhysicalName: physicalName,
	}
	if err := s.store.CreateCollection(ctx, indexMeta); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConflict, err)
	}
	if s.adapter != nil {
		if err := s.adapter.CreateCollection(ctx, physicalName, req.Dimension, indexMeta.Metric); err != nil {
			_ = s.store.DeleteCollection(ctx, indexID)
			return nil, fmt.Errorf("%w: %v", ErrInternal, err)
		}
	}
	if err := s.store.UpdateCollectionStatus(ctx, indexID, metadata.CollStatusReady); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInternal, err)
	}
	return &CreateIndexResponse{
		IndexARN: fmt.Sprintf("arn:aws:s3vectors:%s:%s:bucket/%s/index/%s", req.Region, req.AccountID, bucketName, req.IndexName),
	}, nil
}

func (s *ObjectService) DeleteIndex(ctx context.Context, req *DeleteIndexRequest) error {
	bucketName, indexName, err := req.Target.ResolveNames()
	if err != nil {
		return err
	}
	indexMeta, err := s.router.Resolve(ctx, bucketName, indexName)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	_ = s.store.UpdateCollectionStatus(ctx, indexMeta.ID, metadata.CollStatusDeleting)
	if s.adapter != nil {
		_ = s.adapter.ReleaseCollection(ctx, indexMeta.PhysicalName)
		_ = s.adapter.DropCollection(ctx, indexMeta.PhysicalName)
	}
	if err := s.store.DeleteCollection(ctx, indexMeta.ID); err != nil {
		return fmt.Errorf("%w: %v", ErrInternal, err)
	}
	return nil
}

func (s *ObjectService) PutVectors(ctx context.Context, req *PutVectorsRequest) error {
	if len(req.Vectors) == 0 || len(req.Vectors) > 500 {
		return fmt.Errorf("%w: vectors length must be between 1 and 500", ErrValidation)
	}
	bucketName, indexName, err := req.Target.ResolveNames()
	if err != nil {
		return err
	}
	indexMeta, err := s.router.Resolve(ctx, bucketName, indexName)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	}

	ids := make([]string, 0, len(req.Vectors))
	data := make([][]float32, 0, len(req.Vectors))
	metaJSON := make([][]byte, 0, len(req.Vectors))
	for _, item := range req.Vectors {
		if len(item.Data.Float32) != indexMeta.Dim {
			return fmt.Errorf("%w: vector dimension mismatch", ErrValidation)
		}
		ids = append(ids, item.Key)
		data = append(data, item.Data.Float32)
		metaJSON = append(metaJSON, item.Metadata)
	}

	if err := s.quota.CheckVectorCount(indexMeta.VectorCount, len(req.Vectors)); err != nil {
		return fmt.Errorf("%w: %v", ErrQuotaExceeded, err)
	}
	if s.adapter != nil {
		if err := s.adapter.Insert(ctx, indexMeta.PhysicalName, ids, data, metaJSON, nil); err != nil {
			return fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
		}
	}
	_ = s.store.UpdateCollectionVectorCount(ctx, indexMeta.ID, int64(len(req.Vectors)))
	return nil
}

func (s *ObjectService) DeleteVectors(ctx context.Context, req *DeleteVectorsRequest) error {
	if len(req.Keys) == 0 {
		return fmt.Errorf("%w: keys cannot be empty", ErrValidation)
	}
	bucketName, indexName, err := req.Target.ResolveNames()
	if err != nil {
		return err
	}
	indexMeta, err := s.router.Resolve(ctx, bucketName, indexName)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	if s.adapter != nil {
		if err := s.adapter.Delete(ctx, indexMeta.PhysicalName, req.Keys); err != nil {
			return fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
		}
	}
	_ = s.store.UpdateCollectionVectorCount(ctx, indexMeta.ID, -int64(len(req.Keys)))
	return nil
}
```

- [ ] **步骤 2: 在 shim 注册 index 和 vector 写入入口**

```go
// $JUICEDATA_MINIO_ROOT/cmd/s3vectors_handlers.go
func registerS3VectorsHandlers(router *mux.Router, api objectAPIHandlers) {
	router.Methods(http.MethodPost).Path("/CreateIndex").HandlerFunc(api.CreateIndexHandler)
	router.Methods(http.MethodPost).Path("/DeleteIndex").HandlerFunc(api.DeleteIndexHandler)
	router.Methods(http.MethodPost).Path("/PutVectors").HandlerFunc(api.PutVectorsHandler)
	router.Methods(http.MethodPost).Path("/DeleteVectors").HandlerFunc(api.DeleteVectorsHandler)
}

func (api objectAPIHandlers) PutVectorsHandler(w http.ResponseWriter, r *http.Request) {
	var req vectorbucket.PutVectorsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeVectorAPIError(w, vectorbucket.TranslateError(vectorbucket.ErrValidation))
		return
	}
	req.RequestID = mustGetRequestID(r)
	req.Region = globalSite.Region()
	req.AccountID = mustResolveAccountID(r)

	ext, err := api.vectorExtension()
	if err != nil {
		writeVectorAPIError(w, vectorbucket.TranslateError(err))
		return
	}
	if err := ext.PutVectors(r.Context(), &req); err != nil {
		writeVectorAPIError(w, vectorbucket.TranslateError(err))
		return
	}
	writeVectorJSON(w, http.StatusOK, struct{}{})
}
```

- [ ] **步骤 3: 增加 index 和写入测试**

```go
// pkg/gateway/vectorbucket/object_service_test.go
package vectorbucket

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/config"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metadata"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/quota"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/router"
)

func TestObjectServiceCreateIndexAndPutVectors(t *testing.T) {
	db := filepath.Join(t.TempDir(), "vectorbucket.db")
	store := metadata.NewSQLiteStore(db)
	require.NoError(t, store.Init(context.Background()))
	t.Cleanup(func() { _ = store.Close() })

	cfg := config.DefaultConfig()
	bucketSvc := NewBucketService(store, quota.NewChecker(store, &cfg))
	_, err := bucketSvc.CreateVectorBucket(context.Background(), &CreateVectorBucketRequest{
		RequestContext:   RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		VectorBucketName: "demo-bucket",
	})
	require.NoError(t, err)

	objSvc := NewObjectService(store, router.NewNamespaceRouter(store), nil, nil, quota.NewChecker(store, &cfg), cfg)
	indexResp, err := objSvc.CreateIndex(context.Background(), &CreateIndexRequest{
		RequestContext:   RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:           Target{VectorBucketName: "demo-bucket"},
		IndexName:        "demo-index",
		DataType:         "float32",
		Dimension:        4,
		DistanceMetric:   "cosine",
	})
	require.NoError(t, err)
	require.Contains(t, indexResp.IndexARN, "/index/demo-index")

	err = objSvc.PutVectors(context.Background(), &PutVectorsRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         Target{VectorBucketName: "demo-bucket", IndexName: "demo-index"},
		Vectors: []PutInputVector{
			{Key: "v1", Data: VectorData{Float32: []float32{0.1, 0.2, 0.3, 0.4}}, Metadata: json.RawMessage(`{"tag":"a"}`)},
			{Key: "v2", Data: VectorData{Float32: []float32{0.4, 0.3, 0.2, 0.1}}},
		},
	})
	require.NoError(t, err)

	err = objSvc.DeleteVectors(context.Background(), &DeleteVectorsRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         Target{VectorBucketName: "demo-bucket", IndexName: "demo-index"},
		Keys:           []string{"v1"},
	})
	require.NoError(t, err)
}
```

- [ ] **步骤 4: 运行测试**

运行: `cd $JUICEFS_ROOT && go test -count=1 -v ./pkg/gateway/vectorbucket/... ./pkg/gateway/...`
预期: PASS

- [ ] **步骤 5: 提交**

```bash
git add pkg/gateway/gateway_test.go pkg/gateway/vectorbucket/object_service.go pkg/gateway/vectorbucket/object_service_test.go
git commit -s -m "feat(vectorbucket): add index lifecycle and vector write service

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## 任务 13：`QueryVectors` 协议 Shim（含 Load/Release 集成）

**文件:**
- 新建: `pkg/gateway/vectorbucket/query_service.go`
- 新建: `pkg/gateway/vectorbucket/query_service_test.go`
- 新建: `pkg/gateway/vectorbucket/filter/translator.go`
- 新建: `pkg/gateway/vectorbucket/filter/translator_test.go`
- 可能修改: `$JUICEDATA_MINIO_ROOT/cmd/<protocol shim>`
- 修改: `pkg/gateway/vectorbucket/protocol.go`

> **实际落点修正:** AWS 当前查询主路径是独立的 `POST /QueryVectors` JSON API，而不是普通对象读路径。因此本任务应默认需要 `juicedata/minio` shim；只有在确认 fork 已经内置这组操作时，才可以直接接 runtime。JuiceFS 仓库负责实现 query service、Load/Release、Milvus search 编排，并对齐 `topK`、`returnDistance`、`returnMetadata`、metadata filter 等关键字段。

> **目录约束:** 查询协议 shim 仍然改本地 `$JUICEDATA_MINIO_ROOT/cmd/`；当前仓库只记录 JuiceFS 侧改动和对应计划，不把 minio 代码纳入本仓版本管理。

- [ ] **步骤 1: 在 `filter/translator.go` 把 JSON filter 归一到 Milvus 表达式**

```go
// pkg/gateway/vectorbucket/filter/translator.go
package filter

import (
	"encoding/json"
	"fmt"
)

func Translate(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", err
	}

	field, ok := doc["field"].(string)
	if !ok || field == "" {
		return "", fmt.Errorf("filter.field is required")
	}
	op, ok := doc["op"].(string)
	if !ok || op == "" {
		return "", fmt.Errorf("filter.op is required")
	}

	switch op {
	case "eq":
		return fmt.Sprintf("metadata_%s == %q", field, doc["value"]), nil
	case "in":
		rawValues, ok := doc["value"].([]any)
		if !ok {
			return "", fmt.Errorf("filter.value must be array for op=in")
		}
		return fmt.Sprintf("metadata_%s in %v", field, rawValues), nil
	default:
		return "", fmt.Errorf("unsupported filter op %q", op)
	}
}
```

- [ ] **步骤 2: 在 `query_service.go` 接入 filter 翻译、Load/Release 和返回裁剪**

```go
// pkg/gateway/vectorbucket/query_service.go
package vectorbucket

import (
	"context"
	"fmt"
	"strings"

	vbfilter "github.com/juicedata/juicefs/pkg/gateway/vectorbucket/filter"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/adapter"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/controller"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metadata"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/router"
)

type QueryService struct {
	store      metadata.Store
	router     *router.NamespaceRouter
	adapter    adapter.Adapter
	controller *controller.LoadController
}

func NewQueryService(store metadata.Store, ns *router.NamespaceRouter, milvus adapter.Adapter, ctrl *controller.LoadController) *QueryService {
	return &QueryService{store: store, router: ns, adapter: milvus, controller: ctrl}
}

func (s *QueryService) QueryVectors(ctx context.Context, req *QueryVectorsRequest) (*QueryVectorsResponse, error) {
	if req.TopK < 1 {
		return nil, fmt.Errorf("%w: topK must be >= 1", ErrValidation)
	}
	bucketName, indexName, err := req.Target.ResolveNames()
	if err != nil {
		return nil, err
	}
	indexMeta, err := s.router.Resolve(ctx, bucketName, indexName)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	if len(req.QueryVector.Float32) != indexMeta.Dim {
		return nil, fmt.Errorf("%w: queryVector dimension mismatch", ErrValidation)
	}

	filterExpr, err := vbfilter.Translate(req.Filter)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidation, err)
	}

	estMem := controller.EstimateMemMB(indexMeta.VectorCount, indexMeta.Dim)
	if err := s.controller.EnsureLoaded(ctx, indexMeta.PhysicalName, estMem); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}
	s.controller.InFlightInc(indexMeta.PhysicalName)
	defer s.controller.InFlightDec(indexMeta.PhysicalName)
	s.controller.Touch(indexMeta.PhysicalName)
	_ = s.store.UpdateCollectionLastAccess(ctx, indexMeta.ID)

	if s.adapter == nil {
		return &QueryVectorsResponse{DistanceMetric: strings.ToLower(indexMeta.Metric), Vectors: []QueryResultVector{}}, nil
	}

	rows, err := s.adapter.Search(ctx, indexMeta.PhysicalName, req.QueryVector.Float32, req.TopK, 16, filterExpr, indexMeta.Metric)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}

	out := make([]QueryResultVector, 0, len(rows))
	for _, row := range rows {
		item := QueryResultVector{Key: row.ID}
		if req.ReturnDistance {
			item.Distance = row.Score
		}
		if req.ReturnMetadata {
			item.Metadata = row.Metadata
		}
		out = append(out, item)
	}
	return &QueryVectorsResponse{
		DistanceMetric: strings.ToLower(indexMeta.Metric),
		Vectors:        out,
	}, nil
}
```

- [ ] **步骤 3: 增加查询测试**

```go
// pkg/gateway/vectorbucket/query_service_test.go
package vectorbucket

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/config"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/controller"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metadata"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/quota"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/router"
)

type mockLoadReleaser struct{}

func (mockLoadReleaser) LoadCollection(ctx context.Context, name string) error    { return nil }
func (mockLoadReleaser) ReleaseCollection(ctx context.Context, name string) error { return nil }

func TestQueryServiceLifecycle(t *testing.T) {
	db := filepath.Join(t.TempDir(), "vectorbucket.db")
	store := metadata.NewSQLiteStore(db)
	require.NoError(t, store.Init(context.Background()))
	t.Cleanup(func() { _ = store.Close() })

	cfg := config.DefaultConfig()
	bucketSvc := NewBucketService(store, quota.NewChecker(store, &cfg))
	_, err := bucketSvc.CreateVectorBucket(context.Background(), &CreateVectorBucketRequest{
		RequestContext:   RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		VectorBucketName: "demo-bucket",
	})
	require.NoError(t, err)

	objSvc := NewObjectService(store, router.NewNamespaceRouter(store), nil, nil, quota.NewChecker(store, &cfg), cfg)
	_, err = objSvc.CreateIndex(context.Background(), &CreateIndexRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         Target{VectorBucketName: "demo-bucket"},
		IndexName:      "demo-index",
		DataType:       "float32",
		Dimension:      4,
		DistanceMetric: "cosine",
	})
	require.NoError(t, err)

	ctrl := controller.NewLoadController(mockLoadReleaser{}, cfg.LoadBudgetMB, 30*time.Minute, cfg.MaxLoadedColls)
	querySvc := NewQueryService(store, router.NewNamespaceRouter(store), nil, ctrl)
	resp, err := querySvc.QueryVectors(context.Background(), &QueryVectorsRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         Target{VectorBucketName: "demo-bucket", IndexName: "demo-index"},
		QueryVector:    VectorData{Float32: []float32{0.1, 0.2, 0.3, 0.4}},
		TopK:           5,
		Filter:         json.RawMessage(`{"field":"tenant","op":"eq","value":"t1"}`),
		ReturnDistance: true,
		ReturnMetadata: true,
	})
	require.NoError(t, err)
	require.Equal(t, "cosine", resp.DistanceMetric)
}
```

- [ ] **步骤 4: 在 shim 注册 `/QueryVectors` 并写协议测试**

```go
// $JUICEDATA_MINIO_ROOT/cmd/s3vectors_handlers.go
func registerS3VectorsHandlers(router *mux.Router, api objectAPIHandlers) {
	router.Methods(http.MethodPost).Path("/QueryVectors").HandlerFunc(api.QueryVectorsHandler)
}

func (api objectAPIHandlers) QueryVectorsHandler(w http.ResponseWriter, r *http.Request) {
	var req vectorbucket.QueryVectorsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeVectorAPIError(w, vectorbucket.TranslateError(vectorbucket.ErrValidation))
		return
	}
	req.RequestID = mustGetRequestID(r)
	req.Region = globalSite.Region()
	req.AccountID = mustResolveAccountID(r)

	ext, err := api.vectorExtension()
	if err != nil {
		writeVectorAPIError(w, vectorbucket.TranslateError(err))
		return
	}
	resp, err := ext.QueryVectors(r.Context(), &req)
	if err != nil {
		writeVectorAPIError(w, vectorbucket.TranslateError(err))
		return
	}
	writeVectorJSON(w, http.StatusOK, resp)
}
```

- [ ] **步骤 5: 运行测试**

运行: `cd $JUICEFS_ROOT && go test -count=1 -v ./pkg/gateway/vectorbucket/... ./pkg/gateway/...`
预期: PASS

- [ ] **步骤 6: 提交**

```bash
git add pkg/gateway/vectorbucket/query_service.go pkg/gateway/vectorbucket/query_service_test.go pkg/gateway/vectorbucket/filter/translator.go pkg/gateway/vectorbucket/filter/translator_test.go
git commit -s -m "feat(vectorbucket): add query service and filter translation for protocol shim

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## 任务 14：`cmd/gateway.go` + `NewJFSGateway` + 协议 Shim 入口接线

**文件:**
- 修改: `cmd/gateway.go`
- 新建或修改: `pkg/gateway/vectorbucket/bootstrap.go`

> 说明: 真正的接线点必须放在 `cmd/gateway.go`、`pkg/gateway/gateway.go` 的 `NewJFSGateway` 路径，以及需要时的 `juicedata/minio` 协议 shim 上，不再新增独立 `main.go` 入口。Phase 1 最终应表现为 gateway 启动后自动具备 S3 Vectors 独立操作能力。

> **联调方式约束:** 接线验证时，默认 JuiceFS 通过本地 `replace github.com/minio/minio => $JUICEDATA_MINIO_ROOT` 消费 shim 变更，而不是依赖远端 minio 提交。

- [ ] **步骤 1: 在 `bootstrap.go` 提供从 gateway 入口可直接调用的装配函数**

```go
// pkg/gateway/vectorbucket/bootstrap.go
package vectorbucket

import (
	"context"
	"time"

	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/adapter"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/config"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/controller"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metadata"
)

type BootstrapResult struct {
	Runtime    *Runtime
	Stop       func(context.Context) error
}

func Bootstrap(ctx context.Context, cfg config.Config) (*BootstrapResult, error) {
	store := metadata.NewSQLiteStore(cfg.SQLitePath)
	if err := store.Init(ctx); err != nil {
		return nil, err
	}

	milvusAdapter, err := adapter.NewMilvusAdapter(cfg.MilvusAddr)
	if err != nil {
		_ = store.Close()
		return nil, err
	}

	ctrl := controller.NewLoadController(milvusAdapter, cfg.LoadBudgetMB, time.Duration(cfg.TTLSeconds)*time.Second, cfg.MaxLoadedColls)
	ctrl.StartTTLSweepLoop(ctx, 60*time.Second)

	runtime := NewRuntime(cfg, store, milvusAdapter, ctrl)
	return &BootstrapResult{
		Runtime: runtime,
		Stop: func(stopCtx context.Context) error {
			_ = milvusAdapter.Close()
			return runtime.Close(stopCtx)
		},
	}, nil
}
```

- [ ] **步骤 2: 在 `cmd/gateway.go` 使用真实 gateway 启动链接线**

```go
// cmd/gateway.go
func gateway(c *cli.Context) error {
	setup(c, 2)
	metaAddr := c.Args().Get(0)
	listenAddr := c.Args().Get(1)
	conf, jfs := initForSvc(c, "s3gateway", metaAddr, listenAddr)

	vbCfg := vectorbucketconfig.LoadConfig()
	vbBoot, err := vectorbucket.Bootstrap(context.Background(), vbCfg)
	if err != nil {
		return err
	}

	jfsGateway, err = jfsgateway.NewJFSGateway(
		jfs,
		conf,
		&jfsgateway.Config{
			MultiBucket:  c.Bool("multi-buckets"),
			KeepEtag:     c.Bool("keep-etag"),
			Umask:        uint16(umask),
			ObjTag:       c.Bool("object-tag"),
			ObjMeta:      c.Bool("object-meta"),
			HeadDir:      c.Bool("head-dir"),
			HideDir:      c.Bool("hide-dir-object"),
			ReadOnly:     readonly,
			VectorBucket: vbBoot.Runtime,
		},
	)
	if err != nil {
		_ = vbBoot.Stop(context.Background())
		return err
	}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		_ = vbBoot.Stop(context.Background())
	}()

	app := &mcli.App{Action: gateway2, Flags: gatewayFlags}
	return app.Run([]string{"server", "--address", listenAddr, "--anonymous"})
}
```

- [ ] **步骤 3: 验证编译通过**

运行: `cd $JUICEFS_ROOT && go build ./cmd/... ./pkg/gateway/vectorbucket/... ./pkg/gateway/...`
预期: 成功

- [ ] **步骤 4: 提交**

```bash
git add cmd/gateway.go pkg/gateway/vectorbucket/bootstrap.go
git commit -s -m "feat(vectorbucket): wire vector bucket bootstrap into gateway entry

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## 任务 15：全量测试 + 协议兼容性修复

- [ ] **步骤 1: 运行所有 vectorbucket 测试**

运行: `cd $JUICEFS_ROOT && go test -count=1 -v ./pkg/gateway/vectorbucket/... ./pkg/gateway/... ./cmd/...`
预期: 全部 PASS

- [ ] **步骤 2: 运行 go vet**

运行: `cd $JUICEFS_ROOT && go vet ./pkg/gateway/vectorbucket/... ./pkg/gateway/... ./cmd/...`
预期: 无问题

- [ ] **步骤 3: 如果改动了 `juicedata/minio` shim，再运行 fork 侧测试**

运行: `cd $JUICEDATA_MINIO_ROOT && go test -count=1 -v ./cmd/...`
预期: PASS

- [ ] **步骤 3.5: 恢复或确认 JuiceFS `go.mod` 仍指向本地 minio 目录**

运行: `cd $JUICEFS_ROOT && rg -n "replace github.com/minio/minio" go.mod`
预期: `replace` 指向本地 `$JUICEDATA_MINIO_ROOT`，而不是远端版本号

- [ ] **步骤 4: 修复步骤 1-3 发现的编译或测试错误**

常见问题:
- `pkg/gateway` 与 `pkg/gateway/vectorbucket` 相互引用导致 import cycle
- `vectorBucketName/indexName` 和 `vectorBucketArn/indexArn` 的二选一解析遗漏
- `queryVector.float32` 或 `vectors[].data.float32` 的 union 解析不完整
- `ServiceQuotaExceededException`、`TooManyRequestsException`、`ServiceUnavailableException` 映射到错误的 HTTP 状态码
- filter 翻译器生成的 Milvus 表达式与 metadata schema 不匹配

- [ ] **步骤 5: 如有修复则提交**

```bash
git add pkg/gateway/vectorbucket/ pkg/gateway/
git commit -s -m "fix(vectorbucket): fix compilation and test issues from full test suite run

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## 任务 16：验证完整 Gateway / Protocol 契约

- [ ] **步骤 1: 编写端到端 Gateway / Protocol 契约测试（基于 JuiceFS gateway 集成）**

> **实际落点修正:** 这里要把两条链都测到。第一条是 `Runtime` 直连链，验证 JuiceFS 侧业务编排没有歧义；第二条是 shim HTTP 链，验证 `POST /CreateVectorBucket`、`/CreateIndex`、`/PutVectors`、`/QueryVectors`、`/DeleteVectors`、`/DeleteIndex`、`/DeleteVectorBucket` 的请求体和返回体都能走通。

```go
// pkg/gateway/vectorbucket/integration/gateway_contract_test.go
package integration

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/config"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/controller"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metadata"
)

type mockLR struct{}

func (mockLR) LoadCollection(ctx context.Context, name string) error    { return nil }
func (mockLR) ReleaseCollection(ctx context.Context, name string) error { return nil }

func TestRuntimeContract(t *testing.T) {
	db := filepath.Join(t.TempDir(), "vectorbucket.db")
	store := metadata.NewSQLiteStore(db)
	require.NoError(t, store.Init(context.Background()))
	t.Cleanup(func() { _ = store.Close() })

	cfg := config.DefaultConfig()
	ctrl := controller.NewLoadController(mockLR{}, cfg.LoadBudgetMB, 30*time.Minute, cfg.MaxLoadedColls)
	runtime := vectorbucket.NewRuntime(cfg, store, nil, ctrl)

	_, err := runtime.CreateVectorBucket(context.Background(), &vectorbucket.CreateVectorBucketRequest{
		RequestContext:   vectorbucket.RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		VectorBucketName: "demo-bucket",
	})
	require.NoError(t, err)

	_, err = runtime.CreateIndex(context.Background(), &vectorbucket.CreateIndexRequest{
		RequestContext: vectorbucket.RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         vectorbucket.Target{VectorBucketName: "demo-bucket"},
		IndexName:      "demo-index",
		DataType:       "float32",
		Dimension:      4,
		DistanceMetric: "cosine",
	})
	require.NoError(t, err)

	err = runtime.PutVectors(context.Background(), &vectorbucket.PutVectorsRequest{
		RequestContext: vectorbucket.RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         vectorbucket.Target{VectorBucketName: "demo-bucket", IndexName: "demo-index"},
		Vectors: []vectorbucket.PutInputVector{
			{Key: "v1", Data: vectorbucket.VectorData{Float32: []float32{0.1, 0.2, 0.3, 0.4}}, Metadata: json.RawMessage(`{"tenant":"t1"}`)},
			{Key: "v2", Data: vectorbucket.VectorData{Float32: []float32{0.4, 0.3, 0.2, 0.1}}},
		},
	})
	require.NoError(t, err)

	queryResp, err := runtime.QueryVectors(context.Background(), &vectorbucket.QueryVectorsRequest{
		RequestContext: vectorbucket.RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         vectorbucket.Target{VectorBucketName: "demo-bucket", IndexName: "demo-index"},
		QueryVector:    vectorbucket.VectorData{Float32: []float32{0.1, 0.2, 0.3, 0.4}},
		TopK:           5,
		ReturnDistance: true,
		ReturnMetadata: true,
	})
	require.NoError(t, err)
	require.Equal(t, "cosine", queryResp.DistanceMetric)

	require.NoError(t, runtime.DeleteVectors(context.Background(), &vectorbucket.DeleteVectorsRequest{
		RequestContext: vectorbucket.RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         vectorbucket.Target{VectorBucketName: "demo-bucket", IndexName: "demo-index"},
		Keys:           []string{"v1"},
	}))
	require.NoError(t, runtime.DeleteIndex(context.Background(), &vectorbucket.DeleteIndexRequest{
		RequestContext: vectorbucket.RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         vectorbucket.Target{VectorBucketName: "demo-bucket", IndexName: "demo-index"},
	}))
	require.NoError(t, runtime.DeleteVectorBucket(context.Background(), &vectorbucket.DeleteVectorBucketRequest{
		RequestContext: vectorbucket.RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         vectorbucket.Target{VectorBucketName: "demo-bucket"},
	}))
}
```

```go
// $JUICEDATA_MINIO_ROOT/cmd/s3vectors_handlers_test.go
func TestCreateVectorBucketHandler(t *testing.T) {
	body := bytes.NewBufferString(`{"vectorBucketName":"demo-bucket"}`)
	req := httptest.NewRequest(http.MethodPost, "/CreateVectorBucket", body)
	rec := httptest.NewRecorder()

	router := mux.NewRouter()
	registerS3VectorsHandlers(router, newTestObjectAPIHandlers(t))
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"vectorBucketArn":"arn:aws:s3vectors:us-east-1:123456789012:bucket/demo-bucket"}`, rec.Body.String())
}

func TestPutVectorsHandler(t *testing.T) {
	body := bytes.NewBufferString(`{
	  "vectorBucketName":"demo-bucket",
	  "indexName":"demo-index",
	  "vectors":[
	    {"key":"v1","data":{"float32":[0.1,0.2,0.3,0.4]},"metadata":{"tenant":"t1"}}
	  ]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/PutVectors", body)
	rec := httptest.NewRecorder()

	router := mux.NewRouter()
	registerS3VectorsHandlers(router, newTestObjectAPIHandlers(t))
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestQueryVectorsHandler(t *testing.T) {
	body := bytes.NewBufferString(`{
	  "vectorBucketName":"demo-bucket",
	  "indexName":"demo-index",
	  "topK":5,
	  "queryVector":{"float32":[0.1,0.2,0.3,0.4]},
	  "returnDistance":true,
	  "returnMetadata":true
	}`)
	req := httptest.NewRequest(http.MethodPost, "/QueryVectors", body)
	rec := httptest.NewRecorder()

	router := mux.NewRouter()
	registerS3VectorsHandlers(router, newTestObjectAPIHandlers(t))
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}
```

- [ ] **步骤 2: 运行集成测试**

运行: `cd $JUICEFS_ROOT && go test -count=1 -v ./pkg/gateway/vectorbucket/integration/... ./pkg/gateway/...`
预期: PASS

- [ ] **步骤 3: 如果改动了 `juicedata/minio` shim，再运行 handler 契约测试**

运行: `cd $JUICEDATA_MINIO_ROOT && go test -count=1 -v ./cmd/...`
预期: PASS

- [ ] **步骤 3.5: 确认当前仓库没有把 minio 目录纳入版本管理**

运行: `cd $JUICEFS_ROOT && git status --short`
预期: 只出现 JuiceFS 侧改动；不会出现 `$JUICEDATA_MINIO_ROOT` 下的文件

- [ ] **步骤 4: 提交**

```bash
git add pkg/gateway/vectorbucket/integration/ pkg/gateway/gateway_test.go
git commit -s -m "test(vectorbucket): add runtime and protocol contract coverage for phase 1

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## 总览

| 任务 | 组件 | 核心交付物 |
|------|------|-----------|
| 1 | JuiceFS 网关挂载 + 配置 | `cmd/gateway.go` 接线、`bootstrap.go`、`config.go` |
| 2 | Metadata 模型 | `models.go`、`store.go` 接口 |
| 3 | SQLite Store | Bucket + Collection 完整 CRUD |
| 4 | Namespace Router | 逻辑名 -> 物理名解析 |
| 5 | Milvus Adapter | 封装 client 的 collection/vector/search 操作 |
| 6 | Load/Release 控制器 | LRU + TTL + 预算 + in-flight 追踪 |
| 7 | 配额管控 | Bucket/Collection/维度/向量数限制 |
| 8 | Prometheus 指标 | Phase 1 全部指标定义 |
| 9 | 协议契约 | JuiceFS 扩展接口 + S3 / Vector Bucket 错误映射 |
| 10 | Runtime 挂接 | `jfsObjects` 挂载 Vector Bucket runtime，并向协议层暴露扩展接口 |
| 11 | Vector Bucket 协议 | `CreateVectorBucket/GetVectorBucket/DeleteVectorBucket/ListVectorBuckets` |
| 12 | Index 与写入协议 | `CreateIndex/DeleteIndex/PutVectors/DeleteVectors` |
| 13 | 查询协议 | `QueryVectors` + Load/Release + 必要的 juicedata/minio 协议 shim |
| 14 | Gateway 入口接线 | `cmd/gateway.go` + `NewJFSGateway` + 协议 shim 串联所有组件 |
| 15 | 全量验证 | 编译、测试、协议兼容性修复 |
| 16 | Gateway / Protocol 契约测试 | 端到端完整流程验证 |
