package integration

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/adapter"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/config"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/controller"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metadata"
)

type integrationAdapter struct {
	collections map[string]struct{}
	records     map[string][]adapter.VectorRecord
	search      []adapter.SearchResult
}

func newIntegrationAdapter() *integrationAdapter {
	return &integrationAdapter{collections: make(map[string]struct{}), records: make(map[string][]adapter.VectorRecord)}
}

func (a *integrationAdapter) CreateCollection(ctx context.Context, name string, dim int, metric string) error {
	a.collections[name] = struct{}{}
	return nil
}

func (a *integrationAdapter) DropCollection(ctx context.Context, name string) error {
	delete(a.collections, name)
	return nil
}

func (a *integrationAdapter) HasCollection(ctx context.Context, name string) (bool, error) {
	_, ok := a.collections[name]
	return ok, nil
}

func (a *integrationAdapter) CreateIndex(ctx context.Context, name string, spec adapter.IndexSpec) error {
	return nil
}

func (a *integrationAdapter) LoadCollection(ctx context.Context, name string) error {
	return nil
}

func (a *integrationAdapter) ReleaseCollection(ctx context.Context, name string) error {
	return nil
}

func (a *integrationAdapter) Insert(ctx context.Context, name string, ids []string, vectors [][]float32, metadataJSON [][]byte, timestamps []int64) error {
	return nil
}

func (a *integrationAdapter) Upsert(ctx context.Context, name string, ids []string, vectors [][]float32, metadataJSON [][]byte, timestamps []int64) error {
	for i := range ids {
		a.records[name] = append(a.records[name], adapter.VectorRecord{
			ID:        ids[i],
			Vector:    append([]float32(nil), vectors[i]...),
			Metadata:  append([]byte(nil), metadataJSON[i]...),
			CreatedAt: timestamps[i],
		})
	}
	return nil
}

func (a *integrationAdapter) Delete(ctx context.Context, name string, ids []string) error {
	return nil
}

func (a *integrationAdapter) Search(ctx context.Context, name string, vector []float32, spec adapter.SearchSpec) ([]adapter.SearchResult, error) {
	return a.search, nil
}

func (a *integrationAdapter) Scan(ctx context.Context, name string, batchSize int, fn func([]adapter.VectorRecord) error) error {
	rows := a.records[name]
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

func (a *integrationAdapter) Count(ctx context.Context, name string) (int64, error) {
	return int64(len(a.records[name])), nil
}

func (a *integrationAdapter) Close() error {
	return nil
}

func newRuntime(t *testing.T) *vectorbucket.Runtime {
	t.Helper()
	db := filepath.Join(t.TempDir(), "vectorbucket.db")
	store := metadata.NewSQLiteStore(db)
	require.NoError(t, store.Init(context.Background()))
	t.Cleanup(func() { _ = store.Close() })

	cfg := config.DefaultConfig()
	cfg.IndexBuildThreshold = 1
	adp := newIntegrationAdapter()
	adp.search = []adapter.SearchResult{
		{ID: "v1", Score: 0.9, Metadata: []byte(`{"tenant":"t1"}`)},
	}
	ctrl := controller.NewLoadController(adp, cfg.LoadBudgetMB, 30*time.Minute, cfg.MaxLoadedColls)
	return vectorbucket.NewRuntime(cfg, store, adp, ctrl)
}

func TestRuntimeContract(t *testing.T) {
	runtime := newRuntime(t)

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
	require.Len(t, queryResp.Vectors, 1)
	require.Equal(t, "v1", queryResp.Vectors[0].Key)

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
