package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	assert.Equal(t, ":9200", cfg.ListenAddr)
	assert.Equal(t, "localhost:19530", cfg.MilvusAddr)
	assert.Equal(t, "http://127.0.0.1:19531", cfg.MilvusBridgeAddr)
	assert.Equal(t, 4096, cfg.LoadBudgetMB)
	assert.Equal(t, 4096, cfg.MaxDim)
	assert.Equal(t, 30*60, cfg.TTLSeconds)
	assert.Equal(t, 10000, cfg.IndexBuildThreshold)
	assert.NotEmpty(t, cfg.SQLitePath)
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("VB_LISTEN_ADDR", ":8080")
	t.Setenv("VB_MILVUS_ADDR", "milvus:19530")
	t.Setenv("VB_MILVUS_BRIDGE_ADDR", "http://bridge:19531")
	t.Setenv("VB_LOAD_BUDGET_MB", "2048")

	cfg := LoadConfig()
	assert.Equal(t, ":8080", cfg.ListenAddr)
	assert.Equal(t, "milvus:19530", cfg.MilvusAddr)
	assert.Equal(t, "http://bridge:19531", cfg.MilvusBridgeAddr)
	assert.Equal(t, 2048, cfg.LoadBudgetMB)
}
