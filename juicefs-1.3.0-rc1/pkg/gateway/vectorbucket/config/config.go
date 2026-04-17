package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	ListenAddr          string
	MilvusAddr          string
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
	BucketIndexPolicies map[string]BucketIndexPolicy
	PerformanceBudgetMB int
	StandardBudgetMB    int
	PerformanceMaxVectors int64
	HNSWM               int
	HNSWEFConstruction  int
	DiskANNSearchList   int
}

type BucketIndexPolicy struct {
	IndexType            string `json:"indexType"`
	MaxVectors           int64  `json:"maxVectors"`
	HNSWM                int    `json:"hnswM"`
	HNSWEFConstruction   int    `json:"hnswEfConstruction"`
	DiskANNSearchList    int    `json:"diskannSearchList"`
}

type fileConfig struct {
	ListenAddr            *string   `json:"listenAddr"`
	MilvusAddr            *string   `json:"milvusAddr"`
	SQLitePath            *string   `json:"sqlitePath"`
	LoadBudgetMB          *int      `json:"loadBudgetMB"`
	TTLSeconds            *int      `json:"ttlSeconds"`
	IndexBuildThreshold   *int      `json:"indexBuildThreshold"`
	MaxBucketsPerTenant   *int      `json:"maxBucketsPerTenant"`
	MaxBucketsGlobal      *int      `json:"maxBucketsGlobal"`
	MaxCollPerBucket      *int      `json:"maxCollPerBucket"`
	MaxVectorsPerColl     *int      `json:"maxVectorsPerColl"`
	MaxDim                *int      `json:"maxDim"`
	WriteQPSPerColl       *int      `json:"writeQPSPerColl"`
	QueryQPSPerColl       *int      `json:"queryQPSPerColl"`
	MaxLoadedColls        *int      `json:"maxLoadedColls"`
	BucketIndexPolicies   map[string]BucketIndexPolicy `json:"bucketIndexPolicies"`
	PerformanceBudgetMB   *int      `json:"performanceBudgetMB"`
	StandardBudgetMB      *int      `json:"standardBudgetMB"`
	PerformanceMaxVectors *int64    `json:"performanceMaxVectors"`
	HNSWM                 *int      `json:"hnswM"`
	HNSWEFConstruction    *int      `json:"hnswEfConstruction"`
	DiskANNSearchList     *int      `json:"diskannSearchList"`
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
		MaxDim:              4096,
		WriteQPSPerColl:     500,
		QueryQPSPerColl:     50,
		MaxLoadedColls:      50,
		BucketIndexPolicies: map[string]BucketIndexPolicy{},
		PerformanceBudgetMB: 3072,
		StandardBudgetMB:    3072,
		PerformanceMaxVectors: 200000,
		HNSWM:               16,
		HNSWEFConstruction:  200,
		DiskANNSearchList:   100,
	}
}

func LoadConfig() Config {
	cfg := DefaultConfig()
	loadFromConfigFile(&cfg)
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
	loadPositiveInt("VB_PERFORMANCE_BUDGET_MB", &cfg.PerformanceBudgetMB)
	loadPositiveInt("VB_STANDARD_BUDGET_MB", &cfg.StandardBudgetMB)
	loadPositiveInt("VB_HNSW_M", &cfg.HNSWM)
	loadPositiveInt("VB_HNSW_EF_CONSTRUCTION", &cfg.HNSWEFConstruction)
	loadPositiveInt("VB_DISKANN_SEARCH_LIST", &cfg.DiskANNSearchList)
	loadPositiveInt64("VB_PERFORMANCE_MAX_VECTORS", &cfg.PerformanceMaxVectors)

	if v := os.Getenv("VB_LISTEN_ADDR"); v != "" {
		cfg.ListenAddr = v
	}
	if v := os.Getenv("VB_MILVUS_ADDR"); v != "" {
		cfg.MilvusAddr = v
	}
	if v := os.Getenv("VB_SQLITE_PATH"); v != "" {
		cfg.SQLitePath = v
	}
	return cfg
}

func loadFromConfigFile(cfg *Config) {
	path, ok := findConfigFile()
	if !ok {
		return
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var fc fileConfig
	if err := json.Unmarshal(content, &fc); err != nil {
		return
	}

	applyString(&cfg.ListenAddr, fc.ListenAddr)
	applyString(&cfg.MilvusAddr, fc.MilvusAddr)
	applyString(&cfg.SQLitePath, fc.SQLitePath)
	applyInt(&cfg.LoadBudgetMB, fc.LoadBudgetMB)
	applyInt(&cfg.TTLSeconds, fc.TTLSeconds)
	applyInt(&cfg.IndexBuildThreshold, fc.IndexBuildThreshold)
	applyInt(&cfg.MaxBucketsPerTenant, fc.MaxBucketsPerTenant)
	applyInt(&cfg.MaxBucketsGlobal, fc.MaxBucketsGlobal)
	applyInt(&cfg.MaxCollPerBucket, fc.MaxCollPerBucket)
	applyInt(&cfg.MaxVectorsPerColl, fc.MaxVectorsPerColl)
	applyInt(&cfg.MaxDim, fc.MaxDim)
	applyInt(&cfg.WriteQPSPerColl, fc.WriteQPSPerColl)
	applyInt(&cfg.QueryQPSPerColl, fc.QueryQPSPerColl)
	applyInt(&cfg.MaxLoadedColls, fc.MaxLoadedColls)
	applyInt(&cfg.PerformanceBudgetMB, fc.PerformanceBudgetMB)
	applyInt(&cfg.StandardBudgetMB, fc.StandardBudgetMB)
	applyInt64(&cfg.PerformanceMaxVectors, fc.PerformanceMaxVectors)
	applyInt(&cfg.HNSWM, fc.HNSWM)
	applyInt(&cfg.HNSWEFConstruction, fc.HNSWEFConstruction)
	applyInt(&cfg.DiskANNSearchList, fc.DiskANNSearchList)
	if len(fc.BucketIndexPolicies) > 0 {
		cfg.BucketIndexPolicies = make(map[string]BucketIndexPolicy, len(fc.BucketIndexPolicies))
		for bucket, policy := range fc.BucketIndexPolicies {
			bucket = strings.TrimSpace(bucket)
			if bucket == "" {
				continue
			}
			cfg.BucketIndexPolicies[bucket] = cfg.normalizePolicy(policy)
		}
	}
}

func findConfigFile() (string, bool) {
	if v := strings.TrimSpace(os.Getenv("VB_CONFIG_PATH")); v != "" {
		return v, true
	}
	candidates := []string{
		"vectorbucket.json",
		filepath.Join("configs", "vectorbucket.json"),
		filepath.Join("..", "configs", "vectorbucket.json"),
		"/etc/juicefs/vectorbucket.json",
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, true
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			continue
		}
	}
	return "", false
}

func applyString(target *string, value *string) {
	if value != nil && *value != "" {
		*target = *value
	}
}

func applyInt(target *int, value *int) {
	if value != nil && *value > 0 {
		*target = *value
	}
}

func applyInt64(target *int64, value *int64) {
	if value != nil && *value > 0 {
		*target = *value
	}
}

func loadPositiveInt(key string, target *int) {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			*target = n
		}
	}
}

func loadPositiveInt64(key string, target *int64) {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			*target = n
		}
	}
}

func normalizeIndexType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "ivf", "ivf_sq8":
		return "ivf_sq8"
	case "hnsw":
		return "hnsw"
	case "diskann":
		return "diskann"
	default:
		return ""
	}
}

func (c Config) normalizePolicy(policy BucketIndexPolicy) BucketIndexPolicy {
	policy.IndexType = normalizeIndexType(policy.IndexType)
	if policy.IndexType == "" {
		policy.IndexType = "ivf_sq8"
	}
	if policy.MaxVectors <= 0 && policy.IndexType != "ivf_sq8" {
		policy.MaxVectors = c.PerformanceMaxVectors
	}
	if policy.HNSWM <= 0 {
		policy.HNSWM = c.HNSWM
	}
	if policy.HNSWEFConstruction <= 0 {
		policy.HNSWEFConstruction = c.HNSWEFConstruction
	}
	if policy.DiskANNSearchList <= 0 {
		policy.DiskANNSearchList = c.DiskANNSearchList
	}
	return policy
}

func (c Config) PolicyForBucket(name string) BucketIndexPolicy {
	policy, ok := c.BucketIndexPolicies[name]
	if !ok {
		return c.normalizePolicy(BucketIndexPolicy{IndexType: "ivf_sq8"})
	}
	return c.normalizePolicy(policy)
}

func (c Config) IsPinnedIndexType(indexType string) bool {
	switch normalizeIndexType(indexType) {
	case "hnsw", "diskann":
		return true
	default:
		return false
	}
}

func (c Config) TierForIndexType(indexType string) string {
	if c.IsPinnedIndexType(indexType) {
		return "performance"
	}
	return "standard"
}
