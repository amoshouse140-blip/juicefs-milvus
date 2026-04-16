package router

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metadata"
)

func newTestRouter(t *testing.T) *NamespaceRouter {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store := metadata.NewSQLiteStore(dbPath)
	require.NoError(t, store.Init(context.Background()))
	t.Cleanup(func() { _ = store.Close() })
	return NewNamespaceRouter(store)
}

func TestPhysicalName(t *testing.T) {
	assert.Equal(t, "vb_b1_c1", PhysicalCollectionName("b1", "c1"))
}

func TestResolveCollection(t *testing.T) {
	r := newTestRouter(t)
	ctx := context.Background()

	require.NoError(t, r.store.CreateBucket(ctx, &metadata.Bucket{
		ID: "b1", Name: "my-bucket", Owner: "u1", Status: metadata.BucketStatusReady,
	}))
	require.NoError(t, r.store.CreateCollection(ctx, &metadata.LogicalCollection{
		ID: "c1", BucketID: "b1", Name: "my-coll", Dim: 768, Metric: "COSINE",
		Status: metadata.CollStatusReady, PhysicalName: "vb_b1_c1",
	}))

	lc, err := r.Resolve(ctx, "my-bucket", "my-coll")
	require.NoError(t, err)
	assert.Equal(t, "vb_b1_c1", lc.PhysicalName)
	assert.Equal(t, 768, lc.Dim)
}

func TestResolveNotFound(t *testing.T) {
	r := newTestRouter(t)
	ctx := context.Background()

	_, err := r.Resolve(ctx, "no-bucket", "no-coll")
	assert.Error(t, err)
}
