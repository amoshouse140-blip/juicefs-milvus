package vectorbucket

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/controller"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metadata"
)

func TestRuntimeImplementsExtension(t *testing.T) {
	var _ Extension = (*Runtime)(nil)
}

type bootstrapLoadAdapter struct {
	loaded map[string]bool
}

func (a *bootstrapLoadAdapter) LoadCollection(ctx context.Context, name string) error {
	if a.loaded == nil {
		a.loaded = make(map[string]bool)
	}
	a.loaded[name] = true
	return nil
}

func (a *bootstrapLoadAdapter) ReleaseCollection(ctx context.Context, name string) error {
	delete(a.loaded, name)
	return nil
}

func TestRestorePinnedCollections(t *testing.T) {
	ctx := context.Background()
	db := filepath.Join(t.TempDir(), "vectorbucket.db")
	store := metadata.NewSQLiteStore(db)
	require.NoError(t, store.Init(ctx))
	t.Cleanup(func() { _ = store.Close() })

	require.NoError(t, store.CreateBucket(ctx, &metadata.Bucket{
		ID:     "b1",
		Name:   "hot-bucket",
		Owner:  "acct",
		Status: metadata.BucketStatusReady,
	}))
	require.NoError(t, store.CreateCollection(ctx, &metadata.LogicalCollection{
		ID:           "c1",
		BucketID:     "b1",
		Name:         "main",
		Dim:          768,
		Metric:       "COSINE",
		Tier:         "performance",
		MaxVectors:   200000,
		Pinned:       true,
		Status:       metadata.CollStatusReady,
		PhysicalName: "vbh_b1_c1",
	}))
	require.NoError(t, store.CreateCollection(ctx, &metadata.LogicalCollection{
		ID:           "c2",
		BucketID:     "b1",
		Name:         "cold",
		Dim:          768,
		Metric:       "COSINE",
		Tier:         "standard",
		Status:       metadata.CollStatusReady,
		PhysicalName: "vb_b1_c2",
	}))

	adp := &bootstrapLoadAdapter{loaded: make(map[string]bool)}
	ctrl := controller.NewLoadController(adp, 8192, time.Minute, 50)

	require.NoError(t, restorePinnedCollections(ctx, store, ctrl))
	assert.True(t, ctrl.IsLoaded("vbh_b1_c1"))
	assert.True(t, ctrl.IsPinned("vbh_b1_c1"))
	assert.False(t, ctrl.IsLoaded("vb_b1_c2"))
}
