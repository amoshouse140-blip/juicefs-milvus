package metadata

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s := NewSQLiteStore(dbPath)
	require.NoError(t, s.Init(context.Background()))
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestCreateAndGetBucket(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	b := &Bucket{ID: "b1", Name: "my-bucket", Owner: "user1", Status: BucketStatusReady}
	require.NoError(t, s.CreateBucket(ctx, b))

	got, err := s.GetBucket(ctx, "b1")
	require.NoError(t, err)
	assert.Equal(t, "my-bucket", got.Name)
	assert.Equal(t, "user1", got.Owner)
	assert.Equal(t, BucketStatusReady, got.Status)
}

func TestGetBucketByName(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	b := &Bucket{ID: "b1", Name: "my-bucket", Owner: "user1", Status: BucketStatusReady}
	require.NoError(t, s.CreateBucket(ctx, b))

	got, err := s.GetBucketByName(ctx, "my-bucket")
	require.NoError(t, err)
	assert.Equal(t, "b1", got.ID)
}

func TestCreateDuplicateBucketNameFails(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	b1 := &Bucket{ID: "b1", Name: "same-name", Owner: "user1", Status: BucketStatusReady}
	require.NoError(t, s.CreateBucket(ctx, b1))

	b2 := &Bucket{ID: "b2", Name: "same-name", Owner: "user1", Status: BucketStatusReady}
	assert.Error(t, s.CreateBucket(ctx, b2))
}

func TestListBuckets(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, s.CreateBucket(ctx, &Bucket{ID: "b1", Name: "a", Owner: "u1", Status: BucketStatusReady}))
	require.NoError(t, s.CreateBucket(ctx, &Bucket{ID: "b2", Name: "b", Owner: "u1", Status: BucketStatusReady}))

	list, err := s.ListBuckets(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 2)
}

func TestCountBuckets(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, s.CreateBucket(ctx, &Bucket{ID: "b1", Name: "a", Owner: "u1", Status: BucketStatusReady}))
	require.NoError(t, s.CreateBucket(ctx, &Bucket{ID: "b2", Name: "b", Owner: "u1", Status: BucketStatusReady}))

	cnt, err := s.CountBuckets(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, cnt)

	cnt, err = s.CountBucketsByOwner(ctx, "u1")
	require.NoError(t, err)
	assert.Equal(t, 2, cnt)
}

func TestDeleteBucket(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, s.CreateBucket(ctx, &Bucket{ID: "b1", Name: "a", Owner: "u1", Status: BucketStatusReady}))
	require.NoError(t, s.DeleteBucket(ctx, "b1"))

	_, err := s.GetBucket(ctx, "b1")
	assert.Error(t, err)
}

func TestCreateAndGetCollection(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, s.CreateBucket(ctx, &Bucket{ID: "b1", Name: "bkt", Owner: "u1", Status: BucketStatusReady}))

	c := &LogicalCollection{
		ID: "c1", BucketID: "b1", Name: "my-coll", Dim: 768, Metric: "COSINE",
		Status: CollStatusReady, PhysicalName: "vb_b1_c1",
	}
	require.NoError(t, s.CreateCollection(ctx, c))

	got, err := s.GetCollection(ctx, "b1", "my-coll")
	require.NoError(t, err)
	assert.Equal(t, "c1", got.ID)
	assert.Equal(t, 768, got.Dim)
	assert.Equal(t, "COSINE", got.Metric)
	assert.Equal(t, "vb_b1_c1", got.PhysicalName)
}

func TestDuplicateCollectionNameInBucketFails(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, s.CreateBucket(ctx, &Bucket{ID: "b1", Name: "bkt", Owner: "u1", Status: BucketStatusReady}))

	c1 := &LogicalCollection{ID: "c1", BucketID: "b1", Name: "coll", Dim: 768, Metric: "COSINE", Status: CollStatusReady, PhysicalName: "vb_b1_c1"}
	require.NoError(t, s.CreateCollection(ctx, c1))

	c2 := &LogicalCollection{ID: "c2", BucketID: "b1", Name: "coll", Dim: 768, Metric: "COSINE", Status: CollStatusReady, PhysicalName: "vb_b1_c2"}
	assert.Error(t, s.CreateCollection(ctx, c2))
}

func TestUpdateCollectionVectorCount(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, s.CreateBucket(ctx, &Bucket{ID: "b1", Name: "bkt", Owner: "u1", Status: BucketStatusReady}))
	c := &LogicalCollection{ID: "c1", BucketID: "b1", Name: "coll", Dim: 768, Metric: "COSINE", Status: CollStatusReady, PhysicalName: "vb_b1_c1"}
	require.NoError(t, s.CreateCollection(ctx, c))

	require.NoError(t, s.UpdateCollectionVectorCount(ctx, "c1", 100))
	got, err := s.GetCollectionByID(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, int64(100), got.VectorCount)

	require.NoError(t, s.UpdateCollectionVectorCount(ctx, "c1", -30))
	got, err = s.GetCollectionByID(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, int64(70), got.VectorCount)
}

func TestCountCollections(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, s.CreateBucket(ctx, &Bucket{ID: "b1", Name: "bkt", Owner: "u1", Status: BucketStatusReady}))
	require.NoError(t, s.CreateCollection(ctx, &LogicalCollection{ID: "c1", BucketID: "b1", Name: "a", Dim: 768, Metric: "COSINE", Status: CollStatusReady, PhysicalName: "vb_b1_c1"}))
	require.NoError(t, s.CreateCollection(ctx, &LogicalCollection{ID: "c2", BucketID: "b1", Name: "b", Dim: 768, Metric: "COSINE", Status: CollStatusReady, PhysicalName: "vb_b1_c2"}))

	cnt, err := s.CountCollections(ctx, "b1")
	require.NoError(t, err)
	assert.Equal(t, 2, cnt)
}
