# VectorBucket Milvus Bridge

这个服务把 JuiceFS VectorBucket runtime 需要的 Milvus 操作暴露成一个很薄的 HTTP API。

用途：

- JuiceFS 主模块默认构建不再直接引入 Milvus SDK
- Milvus 依赖图被隔离在这个独立模块里
- JuiceFS 通过 `VB_MILVUS_BRIDGE_ADDR` 调这个 bridge

## Start

```bash
cd milvus/bridge
export MILVUS_ADDR=127.0.0.1:19530
export BRIDGE_LISTEN_ADDR=:19531
GOWORK=off go run ./cmd/vectorbucket-milvus-bridge
```

## Health

```bash
curl -f http://127.0.0.1:19531/healthz
```
