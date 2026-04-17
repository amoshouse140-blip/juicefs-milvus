package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	assert.Equal(t, ":9200", cfg.ListenAddr)
	assert.Equal(t, "localhost:19530", cfg.MilvusAddr)
	assert.Equal(t, 4096, cfg.LoadBudgetMB)
	assert.Equal(t, 4096, cfg.MaxDim)
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

func TestConfigLoadsBucketIndexPolicies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vectorbucket.json")
	err := os.WriteFile(path, []byte(`{
  "bucketIndexPolicies": {
    "kb-hot": {"indexType": "hnsw", "maxVectors": 200000, "hnswM": 32, "hnswEfConstruction": 400},
    "kb-cold": {"indexType": "ivf_sq8"},
    "kb-disk": {"indexType": "diskann", "maxVectors": 300000, "diskannSearchList": 128}
  }
}`), 0o644)
	require.NoError(t, err)

	t.Setenv("VB_CONFIG_PATH", path)
	cfg := LoadConfig()

	assert.Equal(t, "hnsw", cfg.PolicyForBucket("kb-hot").IndexType)
	assert.Equal(t, int64(200000), cfg.PolicyForBucket("kb-hot").MaxVectors)
	assert.Equal(t, 32, cfg.PolicyForBucket("kb-hot").HNSWM)
	assert.Equal(t, "ivf_sq8", cfg.PolicyForBucket("kb-cold").IndexType)
	assert.Equal(t, "diskann", cfg.PolicyForBucket("kb-disk").IndexType)
	assert.Equal(t, 128, cfg.PolicyForBucket("kb-disk").DiskANNSearchList)
	assert.Equal(t, "ivf_sq8", cfg.PolicyForBucket("kb-unknown").IndexType)
}

func TestConfigLoadsFromConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vectorbucket.json")
	err := os.WriteFile(path, []byte(`{
  "listenAddr": ":9300",
  "milvusAddr": "milvus-file:19530",
  "loadBudgetMB": 2048,
  "bucketIndexPolicies": {
    "kb-hot": {"indexType": "hnsw", "maxVectors": 123456}
  },
  "performanceBudgetMB": 4096,
  "performanceMaxVectors": 123456,
  "hnswM": 32,
  "hnswEfConstruction": 400,
  "diskannSearchList": 150
}`), 0o644)
	require.NoError(t, err)

	t.Setenv("VB_CONFIG_PATH", path)

	cfg := LoadConfig()
	assert.Equal(t, ":9300", cfg.ListenAddr)
	assert.Equal(t, "milvus-file:19530", cfg.MilvusAddr)
	assert.Equal(t, 2048, cfg.LoadBudgetMB)
	assert.Equal(t, 4096, cfg.PerformanceBudgetMB)
	assert.Equal(t, int64(123456), cfg.PerformanceMaxVectors)
	assert.Equal(t, 32, cfg.HNSWM)
	assert.Equal(t, 400, cfg.HNSWEFConstruction)
	assert.Equal(t, 150, cfg.DiskANNSearchList)
	assert.Equal(t, "hnsw", cfg.PolicyForBucket("kb-hot").IndexType)
	assert.Equal(t, "ivf_sq8", cfg.PolicyForBucket("kb-cold").IndexType)
}

func TestEnvOverridesConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vectorbucket.json")
	err := os.WriteFile(path, []byte(`{
  "listenAddr": ":9300",
  "bucketIndexPolicies": {
    "kb-file": {"indexType": "hnsw"}
  }
}`), 0o644)
	require.NoError(t, err)

	t.Setenv("VB_CONFIG_PATH", path)
	t.Setenv("VB_LISTEN_ADDR", ":9400")

	cfg := LoadConfig()
	assert.Equal(t, ":9400", cfg.ListenAddr)
	assert.Equal(t, "hnsw", cfg.PolicyForBucket("kb-file").IndexType)
}
