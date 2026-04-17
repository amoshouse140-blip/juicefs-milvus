package config

import (
	"os"
	"strconv"
)

type Config struct {
	ListenAddr          string
	MilvusAddr          string
	MilvusBridgeAddr    string
	SQLitePath          string
	LoadBudgetMB        int
	TTLSeconds          int
	IndexBuildThreshold int
	MaxBucketsPerTenant int
	MaxBucketsGlobal    int
	MaxCollPerBucket    int
	MaxVectorsPerColl   int
	MaxDim              int
	WriteQPSPerColl     int
	QueryQPSPerColl     int
	MaxLoadedColls      int
}

func DefaultConfig() Config {
	return Config{
		ListenAddr:          ":9200",
		MilvusAddr:          "localhost:19530",
		MilvusBridgeAddr:    "http://127.0.0.1:19531",
		SQLitePath:          "/var/lib/vectorbucket/metadata.db",
		LoadBudgetMB:        4096,
		TTLSeconds:          30 * 60,
		IndexBuildThreshold: 10000,
		MaxBucketsPerTenant: 100,
		MaxBucketsGlobal:    1000,
		MaxCollPerBucket:    50,
		MaxVectorsPerColl:   1000000,
		MaxDim:              4096,
		WriteQPSPerColl:     500,
		QueryQPSPerColl:     50,
		MaxLoadedColls:      50,
	}
}

func LoadConfig() Config {
	cfg := DefaultConfig()
	loadPositiveInt("VB_LOAD_BUDGET_MB", &cfg.LoadBudgetMB)
	loadPositiveInt("VB_TTL_SECONDS", &cfg.TTLSeconds)
	loadPositiveInt("VB_INDEX_BUILD_THRESHOLD", &cfg.IndexBuildThreshold)
	loadPositiveInt("VB_MAX_BUCKETS_PER_TENANT", &cfg.MaxBucketsPerTenant)
	loadPositiveInt("VB_MAX_BUCKETS_GLOBAL", &cfg.MaxBucketsGlobal)
	loadPositiveInt("VB_MAX_COLL_PER_BUCKET", &cfg.MaxCollPerBucket)
	loadPositiveInt("VB_MAX_VECTORS_PER_COLL", &cfg.MaxVectorsPerColl)
	loadPositiveInt("VB_MAX_DIM", &cfg.MaxDim)
	loadPositiveInt("VB_WRITE_QPS_PER_COLL", &cfg.WriteQPSPerColl)
	loadPositiveInt("VB_QUERY_QPS_PER_COLL", &cfg.QueryQPSPerColl)
	loadPositiveInt("VB_MAX_LOADED_COLLS", &cfg.MaxLoadedColls)

	if v := os.Getenv("VB_LISTEN_ADDR"); v != "" {
		cfg.ListenAddr = v
	}
	if v := os.Getenv("VB_MILVUS_ADDR"); v != "" {
		cfg.MilvusAddr = v
	}
	if v := os.Getenv("VB_MILVUS_BRIDGE_ADDR"); v != "" {
		cfg.MilvusBridgeAddr = v
	}
	if v := os.Getenv("VB_SQLITE_PATH"); v != "" {
		cfg.SQLitePath = v
	}
	return cfg
}

func loadPositiveInt(key string, target *int) {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			*target = n
		}
	}
}
