package vectorbucket

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/adapter"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/config"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/controller"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metadata"
)

type stubAdapter struct {
	collections map[string]struct{}
	records     map[string][]adapter.VectorRecord
	upserts     []string
	deletes     []string
	search      []adapter.SearchResult
	createIndex []string
	indexSpecs  []adapter.IndexSpec
	searchSpecs []adapter.SearchSpec
	loads       []string
	releases    []string
}

func newStubAdapter() *stubAdapter {
	return &stubAdapter{collections: make(map[string]struct{}), records: make(map[string][]adapter.VectorRecord)}
}

func (s *stubAdapter) CreateCollection(ctx context.Context, name string, dim int, metric string) error {
	s.collections[name] = struct{}{}
	return nil
}

func (s *stubAdapter) DropCollection(ctx context.Context, name string) error {
	delete(s.collections, name)
	return nil
}

func (s *stubAdapter) HasCollection(ctx context.Context, name string) (bool, error) {
	_, ok := s.collections[name]
	return ok, nil
}

func (s *stubAdapter) CreateIndex(ctx context.Context, name string, spec adapter.IndexSpec) error {
	s.createIndex = append(s.createIndex, name)
	s.indexSpecs = append(s.indexSpecs, spec)
	return nil
}

func (s *stubAdapter) LoadCollection(ctx context.Context, name string) error {
	s.loads = append(s.loads, name)
	return nil
}

func (s *stubAdapter) ReleaseCollection(ctx context.Context, name string) error {
	s.releases = append(s.releases, name)
	return nil
}

func (s *stubAdapter) Insert(ctx context.Context, name string, ids []string, vectors [][]float32, metadataJSON [][]byte, timestamps []int64) error {
	return nil
}

func (s *stubAdapter) Upsert(ctx context.Context, name string, ids []string, vectors [][]float32, metadataJSON [][]byte, timestamps []int64) error {
	s.upserts = append(s.upserts, ids...)
	for i := range ids {
		s.records[name] = append(s.records[name], adapter.VectorRecord{
			ID:        ids[i],
			Vector:    append([]float32(nil), vectors[i]...),
			Metadata:  append([]byte(nil), metadataJSON[i]...),
			CreatedAt: timestamps[i],
		})
	}
	return nil
}

func (s *stubAdapter) Delete(ctx context.Context, name string, ids []string) error {
	s.deletes = append(s.deletes, ids...)
	return nil
}

func (s *stubAdapter) Search(ctx context.Context, name string, vector []float32, spec adapter.SearchSpec) ([]adapter.SearchResult, error) {
	s.searchSpecs = append(s.searchSpecs, spec)
	return s.search, nil
}

func (s *stubAdapter) Scan(ctx context.Context, name string, batchSize int, fn func([]adapter.VectorRecord) error) error {
	rows := s.records[name]
	if batchSize <= 0 {
		batchSize = len(rows)
	}
	for start := 0; start < len(rows); start += batchSize {
		end := start + batchSize
		if end > len(rows) {
			end = len(rows)
		}
		batch := append([]adapter.VectorRecord(nil), rows[start:end]...)
		if err := fn(batch); err != nil {
			return err
		}
	}
	return nil
}

func (s *stubAdapter) Count(ctx context.Context, name string) (int64, error) {
	return int64(len(s.records[name])), nil
}

func (s *stubAdapter) Close() error {
	return nil
}

func newTestRuntime(t *testing.T) (*Runtime, *stubAdapter) {
	t.Helper()
	db := filepath.Join(t.TempDir(), "vectorbucket.db")
	store := metadata.NewSQLiteStore(db)
	require.NoError(t, store.Init(context.Background()))
	t.Cleanup(func() { _ = store.Close() })

	cfg := config.DefaultConfig()
	cfg.IndexBuildThreshold = 1
	adp := newStubAdapter()
	ctrl := controller.NewLoadController(adp, cfg.LoadBudgetMB, time.Minute, cfg.MaxLoadedColls)
	return NewRuntime(cfg, store, adp, ctrl), adp
}

func TestBucketLifecycle(t *testing.T) {
	runtime, _ := newTestRuntime(t)

	createResp, err := runtime.CreateVectorBucket(context.Background(), &CreateVectorBucketRequest{
		RequestContext:   RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		VectorBucketName: "demo-bucket",
	})
	require.NoError(t, err)
	require.Contains(t, createResp.VectorBucketARN, "bucket/demo-bucket")

	getResp, err := runtime.GetVectorBucket(context.Background(), &GetVectorBucketRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         Target{VectorBucketName: "demo-bucket"},
	})
	require.NoError(t, err)
	assert.Equal(t, "demo-bucket", getResp.VectorBucketName)

	listResp, err := runtime.ListVectorBuckets(context.Background(), &ListVectorBucketsRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
	})
	require.NoError(t, err)
	assert.Len(t, listResp.VectorBuckets, 1)
}

func TestIndexWriteQueryLifecycle(t *testing.T) {
	runtime, adp := newTestRuntime(t)
	_, err := runtime.CreateVectorBucket(context.Background(), &CreateVectorBucketRequest{
		RequestContext:   RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		VectorBucketName: "demo-bucket",
	})
	require.NoError(t, err)

	_, err = runtime.CreateIndex(context.Background(), &CreateIndexRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         Target{VectorBucketName: "demo-bucket"},
		IndexName:      "demo-index",
		DataType:       "float32",
		Dimension:      4,
		DistanceMetric: "cosine",
	})
	require.NoError(t, err)
	assert.Len(t, adp.createIndex, 1)

	err = runtime.PutVectors(context.Background(), &PutVectorsRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         Target{VectorBucketName: "demo-bucket", IndexName: "demo-index"},
		Vectors: []PutInputVector{
			{Key: "v1", Data: VectorData{Float32: []float32{0.1, 0.2, 0.3, 0.4}}, Metadata: json.RawMessage(`{"tenant":"t1"}`)},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"v1"}, adp.upserts)
	assert.Len(t, adp.createIndex, 1)

	adp.search = []adapter.SearchResult{{ID: "v1", Score: 0.9, Metadata: []byte(`{"tenant":"t1"}`)}}
	queryResp, err := runtime.QueryVectors(context.Background(), &QueryVectorsRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         Target{VectorBucketName: "demo-bucket", IndexName: "demo-index"},
		QueryVector:    VectorData{Float32: []float32{0.1, 0.2, 0.3, 0.4}},
		TopK:           5,
		ReturnDistance: true,
		ReturnMetadata: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "cosine", queryResp.DistanceMetric)
	assert.Len(t, queryResp.Vectors, 1)
	assert.Equal(t, "v1", queryResp.Vectors[0].Key)

	err = runtime.DeleteVectors(context.Background(), &DeleteVectorsRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         Target{VectorBucketName: "demo-bucket", IndexName: "demo-index"},
		Keys:           []string{"v1"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"v1"}, adp.deletes)

	require.NoError(t, runtime.DeleteIndex(context.Background(), &DeleteIndexRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         Target{VectorBucketName: "demo-bucket", IndexName: "demo-index"},
	}))
	require.NoError(t, runtime.DeleteVectorBucket(context.Background(), &DeleteVectorBucketRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         Target{VectorBucketName: "demo-bucket"},
	}))
}

func TestCreateIndexUsesHNSWBucketPolicy(t *testing.T) {
	runtime, adp := newTestRuntime(t)
	runtime.cfg.BucketIndexPolicies = map[string]config.BucketIndexPolicy{
		"hot-bucket": {IndexType: "hnsw", MaxVectors: 200000, HNSWM: 32, HNSWEFConstruction: 400},
	}
	runtime.objects.cfg = runtime.cfg
	_, err := runtime.CreateVectorBucket(context.Background(), &CreateVectorBucketRequest{
		RequestContext:   RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		VectorBucketName: "hot-bucket",
	})
	require.NoError(t, err)

	_, err = runtime.CreateIndex(context.Background(), &CreateIndexRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         Target{VectorBucketName: "hot-bucket"},
		IndexName:      "main",
		DataType:       "float32",
		Dimension:      128,
		DistanceMetric: "cosine",
	})
	require.NoError(t, err)

	bucket, err := runtime.store.GetBucketByName(context.Background(), "hot-bucket")
	require.NoError(t, err)
	coll, err := runtime.store.GetCollection(context.Background(), bucket.ID, "main")
	require.NoError(t, err)
	assert.Equal(t, "hnsw", coll.IndexType)
	assert.Equal(t, "performance", coll.Tier)
	assert.Equal(t, int64(200000), coll.MaxVectors)
	assert.True(t, coll.Pinned)
	assert.True(t, strings.HasPrefix(coll.PhysicalName, "vbh_"))
	assert.Equal(t, []string{coll.PhysicalName}, adp.loads)
	assert.True(t, runtime.controller.IsPinned(coll.PhysicalName))
	require.Len(t, adp.indexSpecs, 1)
	assert.Equal(t, "hnsw", adp.indexSpecs[0].IndexType)
}

func TestCreateIndexUsesStandardBucketTier(t *testing.T) {
	runtime, adp := newTestRuntime(t)
	runtime.cfg.BucketIndexPolicies = map[string]config.BucketIndexPolicy{
		"hot-bucket": {IndexType: "hnsw", MaxVectors: 200000},
	}
	runtime.objects.cfg = runtime.cfg
	_, err := runtime.CreateVectorBucket(context.Background(), &CreateVectorBucketRequest{
		RequestContext:   RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		VectorBucketName: "cold-bucket",
	})
	require.NoError(t, err)

	_, err = runtime.CreateIndex(context.Background(), &CreateIndexRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         Target{VectorBucketName: "cold-bucket"},
		IndexName:      "main",
		DataType:       "float32",
		Dimension:      128,
		DistanceMetric: "cosine",
	})
	require.NoError(t, err)

	bucket, err := runtime.store.GetBucketByName(context.Background(), "cold-bucket")
	require.NoError(t, err)
	coll, err := runtime.store.GetCollection(context.Background(), bucket.ID, "main")
	require.NoError(t, err)
	assert.Equal(t, "ivf_sq8", coll.IndexType)
	assert.Equal(t, "standard", coll.Tier)
	assert.Zero(t, coll.MaxVectors)
	assert.False(t, coll.Pinned)
	assert.True(t, strings.HasPrefix(coll.PhysicalName, "vb_"))
	assert.Empty(t, adp.loads)
}

func TestPinnedIndexPutVectorsRespectsMaxVectors(t *testing.T) {
	runtime, _ := newTestRuntime(t)
	runtime.cfg.BucketIndexPolicies = map[string]config.BucketIndexPolicy{
		"hot-bucket": {IndexType: "hnsw", MaxVectors: 1},
	}
	runtime.objects.cfg = runtime.cfg

	_, err := runtime.CreateVectorBucket(context.Background(), &CreateVectorBucketRequest{
		RequestContext:   RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		VectorBucketName: "hot-bucket",
	})
	require.NoError(t, err)
	_, err = runtime.CreateIndex(context.Background(), &CreateIndexRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         Target{VectorBucketName: "hot-bucket"},
		IndexName:      "main",
		DataType:       "float32",
		Dimension:      4,
		DistanceMetric: "cosine",
	})
	require.NoError(t, err)

	err = runtime.PutVectors(context.Background(), &PutVectorsRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         Target{VectorBucketName: "hot-bucket", IndexName: "main"},
		Vectors: []PutInputVector{
			{Key: "v1", Data: VectorData{Float32: []float32{0.1, 0.2, 0.3, 0.4}}},
			{Key: "v2", Data: VectorData{Float32: []float32{0.2, 0.3, 0.4, 0.5}}},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max vectors")
}

func TestPinnedIndexPutVectorsRefreshesLoadedCollection(t *testing.T) {
	runtime, adp := newTestRuntime(t)
	runtime.cfg.BucketIndexPolicies = map[string]config.BucketIndexPolicy{
		"hot-bucket": {IndexType: "hnsw", MaxVectors: 200000},
	}
	runtime.objects.cfg = runtime.cfg
	runtime.query.cfg = runtime.cfg

	_, err := runtime.CreateVectorBucket(context.Background(), &CreateVectorBucketRequest{
		RequestContext:   RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		VectorBucketName: "hot-bucket",
	})
	require.NoError(t, err)
	_, err = runtime.CreateIndex(context.Background(), &CreateIndexRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         Target{VectorBucketName: "hot-bucket"},
		IndexName:      "main",
		DataType:       "float32",
		Dimension:      4,
		DistanceMetric: "cosine",
	})
	require.NoError(t, err)

	err = runtime.PutVectors(context.Background(), &PutVectorsRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         Target{VectorBucketName: "hot-bucket", IndexName: "main"},
		Vectors: []PutInputVector{
			{Key: "v1", Data: VectorData{Float32: []float32{0.1, 0.2, 0.3, 0.4}}},
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, adp.releases)
	require.Len(t, adp.loads, 2)
}

func TestCreateIndexUsesDiskANNBucketPolicy(t *testing.T) {
	runtime, adp := newTestRuntime(t)
	runtime.cfg.BucketIndexPolicies = map[string]config.BucketIndexPolicy{
		"disk-bucket": {IndexType: "diskann", MaxVectors: 300000, DiskANNSearchList: 128},
	}
	runtime.objects.cfg = runtime.cfg
	runtime.query.cfg = runtime.cfg

	_, err := runtime.CreateVectorBucket(context.Background(), &CreateVectorBucketRequest{
		RequestContext:   RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		VectorBucketName: "disk-bucket",
	})
	require.NoError(t, err)
	_, err = runtime.CreateIndex(context.Background(), &CreateIndexRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         Target{VectorBucketName: "disk-bucket"},
		IndexName:      "main",
		DataType:       "float32",
		Dimension:      128,
		DistanceMetric: "cosine",
	})
	require.NoError(t, err)

	bucket, err := runtime.store.GetBucketByName(context.Background(), "disk-bucket")
	require.NoError(t, err)
	coll, err := runtime.store.GetCollection(context.Background(), bucket.ID, "main")
	require.NoError(t, err)
	assert.Equal(t, "diskann", coll.IndexType)
	assert.Equal(t, "performance", coll.Tier)
	assert.True(t, coll.Pinned)
	require.Len(t, adp.indexSpecs, 1)
	assert.Equal(t, "diskann", adp.indexSpecs[0].IndexType)

	adp.search = []adapter.SearchResult{{ID: "v1", Score: 0.9}}
	_, err = runtime.QueryVectors(context.Background(), &QueryVectorsRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         Target{VectorBucketName: "disk-bucket", IndexName: "main"},
		QueryVector:    VectorData{Float32: make([]float32, 128)},
		TopK:           5,
	})
	require.NoError(t, err)
	require.NotEmpty(t, adp.searchSpecs)
	assert.Equal(t, "diskann", adp.searchSpecs[len(adp.searchSpecs)-1].IndexType)
}

func TestPutVectorsReturnsServiceUnavailableDuringMigration(t *testing.T) {
	runtime, adp := newTestRuntime(t)
	_, err := runtime.CreateVectorBucket(context.Background(), &CreateVectorBucketRequest{
		RequestContext:   RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		VectorBucketName: "demo-bucket",
	})
	require.NoError(t, err)
	_, err = runtime.CreateIndex(context.Background(), &CreateIndexRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         Target{VectorBucketName: "demo-bucket"},
		IndexName:      "demo-index",
		DataType:       "float32",
		Dimension:      4,
		DistanceMetric: "cosine",
	})
	require.NoError(t, err)

	bucket, err := runtime.store.GetBucketByName(context.Background(), "demo-bucket")
	require.NoError(t, err)
	coll, err := runtime.store.GetCollection(context.Background(), bucket.ID, "demo-index")
	require.NoError(t, err)
	require.NoError(t, runtime.store.UpdateCollectionMigrationState(context.Background(), coll.ID, "UPGRADING", "hnsw", coll.PhysicalName, "vbh_target", time.Now().UTC(), ""))

	err = runtime.PutVectors(context.Background(), &PutVectorsRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         Target{VectorBucketName: "demo-bucket", IndexName: "demo-index"},
		Vectors: []PutInputVector{
			{Key: "v1", Data: VectorData{Float32: []float32{0.1, 0.2, 0.3, 0.4}}},
		},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrServiceUnavailable)
	assert.Empty(t, adp.upserts)
}

func TestDeleteVectorsReturnsServiceUnavailableDuringMigration(t *testing.T) {
	runtime, adp := newTestRuntime(t)
	_, err := runtime.CreateVectorBucket(context.Background(), &CreateVectorBucketRequest{
		RequestContext:   RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		VectorBucketName: "demo-bucket",
	})
	require.NoError(t, err)
	_, err = runtime.CreateIndex(context.Background(), &CreateIndexRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         Target{VectorBucketName: "demo-bucket"},
		IndexName:      "demo-index",
		DataType:       "float32",
		Dimension:      4,
		DistanceMetric: "cosine",
	})
	require.NoError(t, err)

	bucket, err := runtime.store.GetBucketByName(context.Background(), "demo-bucket")
	require.NoError(t, err)
	coll, err := runtime.store.GetCollection(context.Background(), bucket.ID, "demo-index")
	require.NoError(t, err)
	require.NoError(t, runtime.store.UpdateCollectionMigrationState(context.Background(), coll.ID, "UPGRADING", "diskann", coll.PhysicalName, "vbh_target", time.Now().UTC(), ""))

	err = runtime.DeleteVectors(context.Background(), &DeleteVectorsRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         Target{VectorBucketName: "demo-bucket", IndexName: "demo-index"},
		Keys:           []string{"v1"},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrServiceUnavailable)
	assert.Empty(t, adp.deletes)
}

func TestChangeIndexModelSetsMigrationState(t *testing.T) {
	runtime, _ := newTestRuntime(t)
	_, err := runtime.CreateVectorBucket(context.Background(), &CreateVectorBucketRequest{
		RequestContext:   RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		VectorBucketName: "demo-bucket",
	})
	require.NoError(t, err)
	_, err = runtime.CreateIndex(context.Background(), &CreateIndexRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         Target{VectorBucketName: "demo-bucket"},
		IndexName:      "demo-index",
		DataType:       "float32",
		Dimension:      4,
		DistanceMetric: "cosine",
	})
	require.NoError(t, err)

	resp, err := runtime.ChangeIndexModel(context.Background(), &ChangeIndexModelRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         Target{VectorBucketName: "demo-bucket", IndexName: "demo-index"},
		IndexModel:     "hnsw",
	})
	require.NoError(t, err)
	assert.Equal(t, "hnsw", resp.IndexModel)
	assert.Equal(t, "UPGRADING", resp.State)

	bucket, err := runtime.store.GetBucketByName(context.Background(), "demo-bucket")
	require.NoError(t, err)
	coll, err := runtime.store.GetCollection(context.Background(), bucket.ID, "demo-index")
	require.NoError(t, err)
	assert.Equal(t, "UPGRADING", coll.MigrateState)
	assert.Equal(t, "hnsw", coll.TargetIndexType)
	assert.Equal(t, coll.PhysicalName, coll.SourcePhysicalName)
	assert.Contains(t, coll.TargetPhysicalName, "_mig_")
	assert.False(t, coll.MaintenanceSince.IsZero())
}
