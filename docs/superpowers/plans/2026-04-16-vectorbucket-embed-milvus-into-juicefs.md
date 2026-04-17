# VectorBucket Milvus In-Process 回收计划

> **给执行者:** 用 `superpowers:executing-plans` 或 `superpowers:subagent-driven-development` 执行。每一步都先验证，再推进到下一步。不要一边改依赖一边大面积改业务逻辑。

**目标:** 去掉当前临时的 `vectorbucket-milvus-bridge` 外部进程，把 Milvus client 回收到 JuiceFS 进程内，实现 `JuiceFS gateway -> VectorBucket runtime -> Milvus SDK` 的单进程调用链，同时保留 Phase 1 已有的协议兼容、LRU/TTL load/release、配额和 benchmark 能力。

**当前问题:** 当前实现依赖 `vectorbucket-milvus-bridge` 规避 JuiceFS 主模块与 Milvus SDK 依赖图冲突。这个方案能跑，但有额外 HTTP hop、额外运维进程、额外错误语义层，而且 `CreateIndex`/load/release 这类行为会分散在两套进程间。要回收进 JuiceFS 内部，真正难点不是业务代码，而是 **JuiceFS `go.mod` 依赖收敛**。

**成功标准:**
- JuiceFS 默认主构建仍然可编译
- `milvus_integration` 构建 tag 下，JuiceFS 可直接调用 Milvus SDK，不需要 bridge
- 现有 boto3 `s3vectors` 测试脚本、`1m` benchmark、查询 benchmark 能继续工作
- `CreateIndex` 语义保持“显式创建”，不再有隐式默认索引或重复建索引

---

## 范围与落点

### 主要改动路径

- 修改: `juicefs-1.3.0-rc1/go.mod`
- 修改: `juicefs-1.3.0-rc1/go.sum`
- 修改: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/bootstrap.go`
- 修改: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/adapter/factory_integration.go`
- 修改: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/adapter/milvus_adapter_integration.go`
- 修改: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/adapter/milvus_adapter_test.go`
- 修改: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/object_service.go`
- 修改: `juicefs-1.3.0-rc1/pkg/gateway/vectorbucket/query_service.go` only if load/release semantics drift
- 修改: `juicefs-1.3.0-rc1/cmd/gateway.go` only if config wiring changes

### 需要删除或降级为可选的部分

- `milvus/bridge/` 整体目录
- `milvus/scripts/run-vectorbucket-bridge.sh`
- `VB_MILVUS_BRIDGE_ADDR` 配置路径
- `adapter/milvus_adapter_http.go` 及相关 factory wiring

### 需要保留的部分

- boto3 兼容脚本
- benchmark 脚本
- standalone 部署目录
- `milvus_integration` build tag 分离

---

## Task 1: 固化当前临时方案基线

**目标:** 在动依赖前，锁定当前 bridge 方案行为，避免后续回收时失去对照组。

- [ ] 记录当前主分支可工作的验证命令
  - `go test -count=1 ./pkg/gateway/vectorbucket/...`
  - `go build ./cmd/... ./pkg/gateway/...`
  - `milvus/bridge` 的 `go test` / `go build`
  - boto3 `CreateVectorBucket/CreateIndex/PutVectors/QueryVectors/DeleteVectors` 验证
- [ ] 记录当前 `1m` benchmark 与 query benchmark 报告路径
- [ ] 记录 bridge 方案下的关键运行参数
  - `VB_MILVUS_BRIDGE_ADDR`
  - `VB_SQLITE_PATH`
  - Milvus standalone 配置
- [ ] 输出一份“回收前基线”摘要，作为后续回归对照

---

## Task 2: 收敛 JuiceFS 与 Milvus SDK 依赖图

**目标:** 在不碰业务逻辑的前提下，先让 `milvus_integration` 构建重新可编译。

- [ ] 写出当前失败命令并保存错误日志
  - `cd juicefs-1.3.0-rc1 && GOWORK=off go build -tags milvus_integration .`
- [ ] 梳理当前冲突模块
  - `google.golang.org/grpc`
  - `google.golang.org/grpc/stats/opentelemetry`
  - `cloud.google.com/go/storage`
  - 其他由 Milvus client 引入的传递依赖
- [ ] 选择一个明确的 Milvus client 版本作为收敛目标
  - 优先与当前 `milvus/bridge/go.mod` 对齐
  - 如果不兼容，再尝试更贴近 JuiceFS 依赖树的版本
- [ ] 最小化修改 `juicefs-1.3.0-rc1/go.mod`
  - 只加必要依赖
  - 尽量少动已有 replace
- [ ] 跑 `go mod tidy` 和 `go build -tags milvus_integration .`
- [ ] 若失败，记录具体冲突点并单独解决，不同时改业务代码

**验收:**
- `GOWORK=off go build -tags milvus_integration .` 通过

---

## Task 3: 回收桥接层业务语义到内置 adapter

**目标:** 把 bridge 内已经验证过的正确行为迁回 `milvus_adapter_integration.go`。

- [ ] 对照 `milvus/bridge/internal/service/service.go`，同步这些语义
  - `Delete -> Flush -> Await`
  - 主键最大长度支持长 `_id`
  - 维度与 metric 映射
  - `CreateIndex` 采用显式入口
  - `LoadCollection` / `ReleaseCollection`
- [ ] 确认 `CreateCollection` 不再隐式预建默认索引
- [ ] 确认 `CreateIndex` 参数与当前显式 API 语义一致
- [ ] 确认 `Search` 路径与 query benchmark 当前行为一致

**验收:**
- `pkg/gateway/vectorbucket/adapter` 测试通过

---

## Task 4: 去掉 HTTP adapter 与 bridge wiring

**目标:** JuiceFS 运行时不再依赖 `VB_MILVUS_BRIDGE_ADDR` 和 HTTP adapter。

- [ ] 修改 `bootstrap.go`
  - `milvus_integration` 构建下直接选择内置 Milvus adapter
  - 默认构建仍可保持 stub/disabled 行为
- [ ] 调整 `factory_integration.go`
  - 让它返回内置 Milvus SDK adapter
- [ ] 降级或删除 `factory_http.go` / `milvus_adapter_http.go`
  - 如果仍保留 bridge 作为 fallback，需要明确 build tag 或 config 语义
- [ ] 清理 `VB_MILVUS_BRIDGE_ADDR` 相关配置和 README

**验收:**
- `milvus_integration` 模式下不需要 bridge 进程即可启动 JuiceFS gateway

---

## Task 5: 保持 VectorBucket 协议语义正确

**目标:** 回收进程内实现时，不把已经修好的协议语义退回旧问题。

- [ ] 保留“显式 `CreateIndex` 才建索引”的语义
- [ ] 保留 `PutVectors` 不再自动重复建索引
- [ ] 保留 Vector API 请求体 `20 MiB` 限制
- [ ] 保留 `DeleteVectors` 后 `Flush` 可见性修复
- [ ] 保留 `LoadController` 的 TTL/LRU 按需 load/release

**验收:**
- boto3 CRUD 脚本通过
- Delete 后再 Query 不返回已删 key

---

## Task 6: 替换部署与脚本

**目标:** 部署文档和脚本从 “需要 bridge” 切到 “进程内直连 Milvus”。

- [ ] 更新 `milvus/deploy/standalone/README.md`
  - 去掉 bridge 启动步骤
  - 改成 `VB_MILVUS_ADDR=<milvus-host>:19530`
- [ ] 更新 `start-vectorbucket-standalone.sh`
  - 不再起 bridge
  - 直接起带 `milvus_integration` 的 JuiceFS gateway
- [ ] 更新 `stop-vectorbucket-standalone.sh`
  - 不再停 bridge
- [ ] 更新 benchmark / query benchmark 的文档说明

---

## Task 7: 回归验证

**目标:** 确保回收后不只是能编译，而是真能跑。

- [ ] 编译验证
  - `GOWORK=off go build ./cmd/... ./pkg/gateway/...`
  - `GOWORK=off go build -tags milvus_integration .`
- [ ] 单元测试
  - `go test -count=1 ./pkg/gateway/vectorbucket/...`
- [ ] boto3 兼容测试
  - `test-vectorbucket-boto3.py`
- [ ] 端到端 smoke test
  - `start-vectorbucket-standalone.sh`
- [ ] 小规模 benchmark
  - `10k`
- [ ] 中规模 benchmark
  - `100k`
- [ ] 查询 benchmark
  - 写中
  - 写完后

**可选扩展:**
- [ ] 再跑一次 `1m`，和 bridge 方案做吞吐、查询、内存对比

---

## Task 8: 下线 bridge

**目标:** 在新方案验证通过后，清理桥接层残留。

- [ ] 删除 `milvus/bridge/`
- [ ] 删除 bridge 启动脚本
- [ ] 删除 bridge 相关部署文档
- [ ] 清理 `VB_MILVUS_BRIDGE_ADDR` 相关说明
- [ ] 更新 superpowers 文档和计划，标记 bridge 为历史过渡方案

---

## 风险与决策点

### 风险 1: 依赖冲突迟迟无法收敛

如果 `go.mod` 依赖图始终打不通，不能盲目继续堆 patch。应暂停回收，把 bridge 方案作为正式过渡层保留，并把“依赖收敛”拆成独立技术债。

### 风险 2: 内置 Milvus SDK 路径导致主构建被污染

必须坚持 `milvus_integration` build tag 隔离。默认主构建不能被 Milvus 依赖拖死。

### 风险 3: 性能不升反降

进程内直连理论上应优于 bridge，但如果依赖版本差异、Milvus client 行为差异或 load/release 语义变化导致性能退化，需要以 benchmark 报告为准，不要凭感觉认定“内置就一定更快”。

---

## 预期结果

完成后应形成两条清晰构建路径：

- **默认构建**
  - 不启用真实 Milvus 集成
  - 仍可编译，可跑非集成测试

- **`milvus_integration` 构建**
  - JuiceFS 进程内直接调用 Milvus SDK
  - 无需 `vectorbucket-milvus-bridge`
  - 保持 S3 Vector Bucket 协议与 benchmark 能力
