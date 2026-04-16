package vectorbucket

import (
	"context"
	"encoding/json"
	"path/filepath"
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
	upserts     []string
	deletes     []string
	search      []adapter.SearchResult
}

func newStubAdapter() *stubAdapter {
	return &stubAdapter{collections: make(map[string]struct{})}
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

func (s *stubAdapter) CreateIndex(ctx context.Context, name string, vectorCount int64, metric string) error {
	return nil
}

func (s *stubAdapter) LoadCollection(ctx context.Context, name string) error {
	return nil
}

func (s *stubAdapter) ReleaseCollection(ctx context.Context, name string) error {
	return nil
}

func (s *stubAdapter) Insert(ctx context.Context, name string, ids []string, vectors [][]float32, metadataJSON [][]byte, timestamps []int64) error {
	return nil
}

func (s *stubAdapter) Upsert(ctx context.Context, name string, ids []string, vectors [][]float32, metadataJSON [][]byte, timestamps []int64) error {
	s.upserts = append(s.upserts, ids...)
	return nil
}

func (s *stubAdapter) Delete(ctx context.Context, name string, ids []string) error {
	s.deletes = append(s.deletes, ids...)
	return nil
}

func (s *stubAdapter) Search(ctx context.Context, name string, vector []float32, topK int, nprobe int, filter string, metric string) ([]adapter.SearchResult, error) {
	return s.search, nil
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

	err = runtime.PutVectors(context.Background(), &PutVectorsRequest{
		RequestContext: RequestContext{AccountID: "123456789012", Region: "us-east-1"},
		Target:         Target{VectorBucketName: "demo-bucket", IndexName: "demo-index"},
		Vectors: []PutInputVector{
			{Key: "v1", Data: VectorData{Float32: []float32{0.1, 0.2, 0.3, 0.4}}, Metadata: json.RawMessage(`{"tenant":"t1"}`)},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"v1"}, adp.upserts)

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
