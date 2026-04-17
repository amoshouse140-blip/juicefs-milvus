package migration

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/adapter"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/config"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/controller"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metadata"
)

type migrationAdapter struct {
	collections map[string]struct{}
	records     map[string][]adapter.VectorRecord
	loaded      map[string]bool
}

func newMigrationAdapter() *migrationAdapter {
	return &migrationAdapter{
		collections: make(map[string]struct{}),
		records:     make(map[string][]adapter.VectorRecord),
		loaded:      make(map[string]bool),
	}
}

func (a *migrationAdapter) CreateCollection(ctx context.Context, name string, dim int, metric string) error {
	a.collections[name] = struct{}{}
	return nil
}
func (a *migrationAdapter) DropCollection(ctx context.Context, name string) error {
	delete(a.collections, name)
	delete(a.records, name)
	return nil
}
func (a *migrationAdapter) HasCollection(ctx context.Context, name string) (bool, error) {
	_, ok := a.collections[name]
	return ok, nil
}
func (a *migrationAdapter) CreateIndex(ctx context.Context, name string, spec adapter.IndexSpec) error {
	return nil
}
func (a *migrationAdapter) LoadCollection(ctx context.Context, name string) error {
	a.loaded[name] = true
	return nil
}
func (a *migrationAdapter) ReleaseCollection(ctx context.Context, name string) error {
	delete(a.loaded, name)
	return nil
}
func (a *migrationAdapter) Insert(ctx context.Context, name string, ids []string, vectors [][]float32, metadataJSON [][]byte, timestamps []int64) error {
	return nil
}
func (a *migrationAdapter) Upsert(ctx context.Context, name string, ids []string, vectors [][]float32, metadataJSON [][]byte, timestamps []int64) error {
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
func (a *migrationAdapter) Delete(ctx context.Context, name string, ids []string) error { return nil }
func (a *migrationAdapter) Search(ctx context.Context, name string, vector []float32, spec adapter.SearchSpec) ([]adapter.SearchResult, error) {
	return nil, nil
}
func (a *migrationAdapter) Scan(ctx context.Context, name string, batchSize int, fn func([]adapter.VectorRecord) error) error {
	if !a.loaded[name] {
		return fmt.Errorf("collection %s is not loaded", name)
	}
	rows := a.records[name]
	if batchSize <= 0 {
		batchSize = len(rows)
	}
	for start := 0; start < len(rows); start += batchSize {
		end := start + batchSize
		if end > len(rows) {
			end = len(rows)
		}
		if err := fn(append([]adapter.VectorRecord(nil), rows[start:end]...)); err != nil {
			return err
		}
	}
	return nil
}
func (a *migrationAdapter) Count(ctx context.Context, name string) (int64, error) {
	return int64(len(a.records[name])), nil
}
func (a *migrationAdapter) Close() error { return nil }

func newMigrationDeps(t *testing.T) (*metadata.SQLiteStore, *migrationAdapter, *controller.LoadController, config.Config) {
	t.Helper()
	db := filepath.Join(t.TempDir(), "vectorbucket.db")
	store := metadata.NewSQLiteStore(db)
	require.NoError(t, store.Init(context.Background()))
	t.Cleanup(func() { _ = store.Close() })
	adp := newMigrationAdapter()
	cfg := config.DefaultConfig()
	ctrl := controller.NewLoadController(adp, cfg.LoadBudgetMB, time.Minute, cfg.MaxLoadedColls)
	return store, adp, ctrl, cfg
}

func TestRunOnceMigratesCollectionAndSwitchesPhysical(t *testing.T) {
	store, adp, ctrl, cfg := newMigrationDeps(t)
	ctx := context.Background()
	require.NoError(t, store.CreateBucket(ctx, &metadata.Bucket{ID: "b1", Name: "demo", Owner: "acct", Status: metadata.BucketStatusReady}))
	require.NoError(t, store.CreateCollection(ctx, &metadata.LogicalCollection{
		ID:                 "c1",
		BucketID:           "b1",
		Name:               "main",
		Dim:                4,
		Metric:             "COSINE",
		IndexType:          "ivf_sq8",
		Tier:               "standard",
		Status:             metadata.CollStatusReady,
		PhysicalName:       "vb_b1_c1",
		SourcePhysicalName: "vb_b1_c1",
		TargetPhysicalName: "vbh_b1_c1_mig",
		TargetIndexType:    "hnsw",
		MigrateState:       "UPGRADING",
		VectorCount:        2,
	}))
	adp.collections["vb_b1_c1"] = struct{}{}
	adp.records["vb_b1_c1"] = []adapter.VectorRecord{
		{ID: "v1", Vector: []float32{0.1, 0.2, 0.3, 0.4}, Metadata: []byte(`{"a":1}`), CreatedAt: 1},
		{ID: "v2", Vector: []float32{0.4, 0.3, 0.2, 0.1}, Metadata: []byte(`{"a":2}`), CreatedAt: 2},
	}

	svc := NewService(store, adp, ctrl, cfg)
	require.NoError(t, svc.RunOnce(ctx))

	coll, err := store.GetCollectionByID(ctx, "c1")
	require.NoError(t, err)
	require.Equal(t, "vbh_b1_c1_mig", coll.PhysicalName)
	require.Equal(t, "hnsw", coll.IndexType)
	require.Equal(t, "performance", coll.Tier)
	require.True(t, coll.Pinned)
	require.Empty(t, coll.MigrateState)
	_, sourceExists := adp.collections["vb_b1_c1"]
	require.False(t, sourceExists)
	_, targetExists := adp.collections["vbh_b1_c1_mig"]
	require.True(t, targetExists)
	require.Len(t, adp.records["vbh_b1_c1_mig"], 2)
}
