package quota

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/config"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metadata"
)

func newTestChecker(t *testing.T) (*Checker, *metadata.SQLiteStore) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store := metadata.NewSQLiteStore(dbPath)
	require.NoError(t, store.Init(context.Background()))
	t.Cleanup(func() { _ = store.Close() })
	cfg := config.DefaultConfig()
	return NewChecker(store, &cfg), store
}

func TestCanCreateBucket(t *testing.T) {
	checker, _ := newTestChecker(t)
	assert.NoError(t, checker.CanCreateBucket(context.Background(), "owner1"))
}

func TestCanCreateBucketExceedsPerTenantLimit(t *testing.T) {
	checker, store := newTestChecker(t)
	ctx := context.Background()

	for i := 0; i < 100; i++ {
		require.NoError(t, store.CreateBucket(ctx, &metadata.Bucket{
			ID: fmt.Sprintf("b%d", i), Name: fmt.Sprintf("bkt-%d", i),
			Owner: "owner1", Status: metadata.BucketStatusReady,
		}))
	}

	err := checker.CanCreateBucket(ctx, "owner1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "per-tenant bucket limit")
}

func TestCanCreateCollection(t *testing.T) {
	checker, store := newTestChecker(t)
	ctx := context.Background()

	require.NoError(t, store.CreateBucket(ctx, &metadata.Bucket{
		ID: "b1", Name: "bkt", Owner: "u1", Status: metadata.BucketStatusReady,
	}))

	assert.NoError(t, checker.CanCreateCollection(ctx, "b1"))
}

func TestCanCreateCollectionExceedsLimit(t *testing.T) {
	checker, store := newTestChecker(t)
	ctx := context.Background()

	require.NoError(t, store.CreateBucket(ctx, &metadata.Bucket{
		ID: "b1", Name: "bkt", Owner: "u1", Status: metadata.BucketStatusReady,
	}))

	for i := 0; i < 50; i++ {
		require.NoError(t, store.CreateCollection(ctx, &metadata.LogicalCollection{
			ID: fmt.Sprintf("c%d", i), BucketID: "b1", Name: fmt.Sprintf("coll-%d", i),
			Dim: 768, Metric: "COSINE", Status: metadata.CollStatusReady,
			PhysicalName: fmt.Sprintf("vb_b1_c%d", i),
		}))
	}

	err := checker.CanCreateCollection(ctx, "b1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "collection limit")
}

func TestCheckDimension(t *testing.T) {
	checker, _ := newTestChecker(t)
	assert.NoError(t, checker.CheckDimension(768))
	assert.NoError(t, checker.CheckDimension(2048))
	assert.NoError(t, checker.CheckDimension(4096))
	assert.Error(t, checker.CheckDimension(4097))
	assert.Error(t, checker.CheckDimension(0))
}

func TestCheckVectorCount(t *testing.T) {
	checker, _ := newTestChecker(t)
	assert.NoError(t, checker.CheckVectorCount(999999, 1))
	assert.Error(t, checker.CheckVectorCount(999999, 2))
}

func TestCanCreatePerformanceCollectionExceedsBudget(t *testing.T) {
	checker, store := newTestChecker(t)
	checker.cfg.PerformanceBudgetMB = 100
	ctx := context.Background()

	require.NoError(t, store.CreateBucket(ctx, &metadata.Bucket{
		ID: "b1", Name: "hot-a", Owner: "u1", Status: metadata.BucketStatusReady,
	}))
	require.NoError(t, store.CreateBucket(ctx, &metadata.Bucket{
		ID: "b2", Name: "hot-b", Owner: "u1", Status: metadata.BucketStatusReady,
	}))
	require.NoError(t, store.CreateCollection(ctx, &metadata.LogicalCollection{
		ID:           "c1",
		BucketID:     "b1",
		Name:         "main",
		Dim:          2048,
		Metric:       "COSINE",
		Tier:         "performance",
		MaxVectors:   10000,
		Pinned:       true,
		Status:       metadata.CollStatusReady,
		PhysicalName: "vbh_b1_c1",
	}))

	err := checker.CanCreatePerformanceCollection(ctx, 10000, 2048)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "performance memory budget")
}

func TestCheckPerformanceVectorLimit(t *testing.T) {
	checker, _ := newTestChecker(t)

	assert.NoError(t, checker.CheckPerformanceVectorLimit(99999, 1, 100000))
	assert.Error(t, checker.CheckPerformanceVectorLimit(100000, 1, 100000))
}
