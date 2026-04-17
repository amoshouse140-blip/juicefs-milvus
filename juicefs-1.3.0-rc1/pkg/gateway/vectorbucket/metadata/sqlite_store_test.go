package metadata

import (
	"context"
	"path/filepath"
	"testing"
	"time"

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

func TestSQLiteStorePersistsCollectionTierFields(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	bucket := &Bucket{
		ID:        "b1",
		Name:      "demo",
		Owner:     "acct",
		Status:    BucketStatusReady,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	require.NoError(t, s.CreateBucket(ctx, bucket))

	coll := &LogicalCollection{
		ID:           "c1",
		BucketID:     "b1",
		Name:         "main",
		Dim:          768,
		Metric:       "COSINE",
		IndexType:    "hnsw",
		Status:       CollStatusReady,
		PhysicalName: "vbh_b1_c1",
		Tier:         "performance",
		MaxVectors:   200000,
		Pinned:       true,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	require.NoError(t, s.CreateCollection(ctx, coll))

	got, err := s.GetCollection(ctx, "b1", "main")
	require.NoError(t, err)
	assert.Equal(t, "hnsw", got.IndexType)
	assert.Equal(t, "performance", got.Tier)
	assert.Equal(t, int64(200000), got.MaxVectors)
	assert.True(t, got.Pinned)
}

func TestSQLiteStorePersistsMigrationFields(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	require.NoError(t, s.CreateBucket(ctx, &Bucket{ID: "b-1", Name: "bucket-a", Owner: "acct", Status: BucketStatusReady}))

	coll := &LogicalCollection{
		ID:                 "lc-1",
		BucketID:           "b-1",
		Name:               "main",
		Dim:                4,
		Metric:             "COSINE",
		IndexType:          "ivf_sq8",
		Tier:               "standard",
		Status:             CollStatusReady,
		PhysicalName:       "vb_b_1_lc_1",
		MigrateState:       "UPGRADING",
		TargetIndexType:    "hnsw",
		SourcePhysicalName: "vb_b_1_lc_1",
		TargetPhysicalName: "vbh_b_1_lc_1",
		MaintenanceSince:   time.Now().UTC().Truncate(time.Second),
		LastMigrateError:   "boom",
	}
	require.NoError(t, s.CreateCollection(ctx, coll))

	got, err := s.GetCollectionByID(ctx, "lc-1")
	require.NoError(t, err)
	assert.Equal(t, "UPGRADING", got.MigrateState)
	assert.Equal(t, "hnsw", got.TargetIndexType)
	assert.Equal(t, "vb_b_1_lc_1", got.SourcePhysicalName)
	assert.Equal(t, "vbh_b_1_lc_1", got.TargetPhysicalName)
	assert.Equal(t, "boom", got.LastMigrateError)
	assert.False(t, got.MaintenanceSince.IsZero())
}

func TestSQLiteStoreUpdatesMigrationState(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	require.NoError(t, s.CreateBucket(ctx, &Bucket{ID: "b-1", Name: "bucket-a", Owner: "acct", Status: BucketStatusReady}))
	require.NoError(t, s.CreateCollection(ctx, &LogicalCollection{
		ID:           "lc-1",
		BucketID:     "b-1",
		Name:         "main",
		Dim:          4,
		Metric:       "COSINE",
		IndexType:    "ivf_sq8",
		Tier:         "standard",
		Status:       CollStatusReady,
		PhysicalName: "vb_b_1_lc_1",
	}))

	now := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, s.UpdateCollectionMigrationState(ctx, "lc-1", "UPGRADING", "hnsw", "vb_b_1_lc_1", "vbh_b_1_lc_1", now, ""))
	got, err := s.GetCollectionByID(ctx, "lc-1")
	require.NoError(t, err)
	assert.Equal(t, "UPGRADING", got.MigrateState)
	assert.Equal(t, "hnsw", got.TargetIndexType)
	assert.Equal(t, "vb_b_1_lc_1", got.SourcePhysicalName)
	assert.Equal(t, "vbh_b_1_lc_1", got.TargetPhysicalName)
	assert.WithinDuration(t, now, got.MaintenanceSince, time.Second)
}

func TestSQLiteStoreSwitchesCollectionPhysicalWithCAS(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	require.NoError(t, s.CreateBucket(ctx, &Bucket{ID: "b-1", Name: "bucket-a", Owner: "acct", Status: BucketStatusReady}))
	require.NoError(t, s.CreateCollection(ctx, &LogicalCollection{
		ID:                 "lc-1",
		BucketID:           "b-1",
		Name:               "main",
		Dim:                4,
		Metric:             "COSINE",
		IndexType:          "ivf_sq8",
		Tier:               "standard",
		Status:             CollStatusReady,
		PhysicalName:       "vb_b_1_lc_1",
		MigrateState:       "UPGRADING",
		TargetIndexType:    "hnsw",
		SourcePhysicalName: "vb_b_1_lc_1",
		TargetPhysicalName: "vbh_b_1_lc_1",
	}))

	err := s.SwitchCollectionPhysical(ctx, "lc-1", "wrong-old", "vbh_b_1_lc_1", "hnsw", "performance", true, 200000)
	require.Error(t, err)

	require.NoError(t, s.SwitchCollectionPhysical(ctx, "lc-1", "vb_b_1_lc_1", "vbh_b_1_lc_1", "hnsw", "performance", true, 200000))
	got, err := s.GetCollectionByID(ctx, "lc-1")
	require.NoError(t, err)
	assert.Equal(t, "vbh_b_1_lc_1", got.PhysicalName)
	assert.Equal(t, "hnsw", got.IndexType)
	assert.Equal(t, "performance", got.Tier)
	assert.True(t, got.Pinned)
	assert.Equal(t, int64(200000), got.MaxVectors)
	assert.Empty(t, got.MigrateState)
	assert.Empty(t, got.TargetIndexType)
	assert.Empty(t, got.SourcePhysicalName)
	assert.Empty(t, got.TargetPhysicalName)
	assert.Empty(t, got.LastMigrateError)
	assert.True(t, got.MaintenanceSince.IsZero())
}

func TestSQLiteStoreListsCollectionsInMigration(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	require.NoError(t, s.CreateBucket(ctx, &Bucket{ID: "b-1", Name: "bucket-a", Owner: "acct", Status: BucketStatusReady}))
	require.NoError(t, s.CreateCollection(ctx, &LogicalCollection{
		ID:           "lc-1",
		BucketID:     "b-1",
		Name:         "main",
		Dim:          4,
		Metric:       "COSINE",
		IndexType:    "ivf_sq8",
		Tier:         "standard",
		Status:       CollStatusReady,
		PhysicalName: "vb_b_1_lc_1",
		MigrateState: "UPGRADING",
	}))
	require.NoError(t, s.CreateCollection(ctx, &LogicalCollection{
		ID:           "lc-2",
		BucketID:     "b-1",
		Name:         "other",
		Dim:          4,
		Metric:       "COSINE",
		IndexType:    "ivf_sq8",
		Tier:         "standard",
		Status:       CollStatusReady,
		PhysicalName: "vb_b_1_lc_2",
	}))

	list, err := s.ListCollectionsInMigration(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "lc-1", list[0].ID)
}
