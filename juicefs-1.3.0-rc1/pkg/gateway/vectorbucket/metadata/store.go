package metadata

import "context"

type Store interface {
	CreateBucket(ctx context.Context, b *Bucket) error
	GetBucket(ctx context.Context, id string) (*Bucket, error)
	GetBucketByName(ctx context.Context, name string) (*Bucket, error)
	ListBuckets(ctx context.Context) ([]*Bucket, error)
	UpdateBucketStatus(ctx context.Context, id string, status BucketStatus) error
	DeleteBucket(ctx context.Context, id string) error
	CountBuckets(ctx context.Context) (int, error)
	CountBucketsByOwner(ctx context.Context, owner string) (int, error)

	CreateCollection(ctx context.Context, c *LogicalCollection) error
	GetCollection(ctx context.Context, bucketID, name string) (*LogicalCollection, error)
	GetCollectionByID(ctx context.Context, id string) (*LogicalCollection, error)
	ListCollections(ctx context.Context, bucketID string) ([]*LogicalCollection, error)
	UpdateCollectionStatus(ctx context.Context, id string, status CollectionStatus) error
	UpdateCollectionIndexBuilt(ctx context.Context, id string, built bool) error
	UpdateCollectionVectorCount(ctx context.Context, id string, delta int64) error
	UpdateCollectionLastAccess(ctx context.Context, id string) error
	DeleteCollection(ctx context.Context, id string) error
	CountCollections(ctx context.Context, bucketID string) (int, error)

	Init(ctx context.Context) error
	Close() error
}
