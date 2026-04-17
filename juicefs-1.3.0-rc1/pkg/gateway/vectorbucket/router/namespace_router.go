package router

import (
	"context"
	"fmt"
	"strings"

	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metadata"
)

func PhysicalCollectionName(bucketID, collectionID string) string {
	return fmt.Sprintf("vb_%s_%s", sanitizeCollectionToken(bucketID), sanitizeCollectionToken(collectionID))
}

func sanitizeCollectionToken(token string) string {
	replacer := strings.NewReplacer("-", "_")
	return replacer.Replace(token)
}

type NamespaceRouter struct {
	store metadata.Store
}

func NewNamespaceRouter(store metadata.Store) *NamespaceRouter {
	return &NamespaceRouter{store: store}
}

func (r *NamespaceRouter) Resolve(ctx context.Context, bucketName, collectionName string) (*metadata.LogicalCollection, error) {
	bucket, err := r.store.GetBucketByName(ctx, bucketName)
	if err != nil {
		return nil, fmt.Errorf("bucket %q not found: %w", bucketName, err)
	}
	if bucket.Status != metadata.BucketStatusReady {
		return nil, fmt.Errorf("bucket %q is not ready (status=%s)", bucketName, bucket.Status)
	}

	coll, err := r.store.GetCollection(ctx, bucket.ID, collectionName)
	if err != nil {
		return nil, fmt.Errorf("collection %q not found in bucket %q: %w", collectionName, bucketName, err)
	}
	if coll.Status != metadata.CollStatusReady {
		return nil, fmt.Errorf("collection %q is not ready (status=%s)", collectionName, coll.Status)
	}
	return coll, nil
}
