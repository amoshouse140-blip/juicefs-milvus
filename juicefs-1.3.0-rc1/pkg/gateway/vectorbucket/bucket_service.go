package vectorbucket

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metadata"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/quota"
)

type BucketService struct {
	store metadata.Store
	quota *quota.Checker
}

func NewBucketService(store metadata.Store, q *quota.Checker) *BucketService {
	return &BucketService{store: store, quota: q}
}

func (s *BucketService) CreateVectorBucket(ctx context.Context, req *CreateVectorBucketRequest) (*CreateVectorBucketResponse, error) {
	if len(req.VectorBucketName) < 3 {
		return nil, fmt.Errorf("%w: vectorBucketName too short", ErrValidation)
	}
	if err := s.quota.CanCreateBucket(ctx, req.AccountID); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrQuotaExceeded, err)
	}

	now := time.Now().UTC()
	bucket := &metadata.Bucket{
		ID:        uuid.NewString(),
		Name:      req.VectorBucketName,
		Owner:     req.AccountID,
		Status:    metadata.BucketStatusReady,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.store.CreateBucket(ctx, bucket); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConflict, err)
	}
	return &CreateVectorBucketResponse{
		VectorBucketARN: formatBucketARN(req.Region, req.AccountID, bucket.Name),
	}, nil
}

func (s *BucketService) GetVectorBucket(ctx context.Context, req *GetVectorBucketRequest) (*GetVectorBucketResponse, error) {
	bucketName, _, err := req.Target.ResolveNames()
	if err != nil {
		return nil, err
	}
	bucket, err := s.store.GetBucketByName(ctx, bucketName)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	return &GetVectorBucketResponse{
		VectorBucketARN:  formatBucketARN(req.Region, req.AccountID, bucket.Name),
		VectorBucketName: bucket.Name,
		CreationTime:     bucket.CreatedAt.UTC().Format(time.RFC3339),
	}, nil
}

func (s *BucketService) ListVectorBuckets(ctx context.Context, req *ListVectorBucketsRequest) (*ListVectorBucketsResponse, error) {
	buckets, err := s.store.ListBuckets(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInternal, err)
	}
	resp := &ListVectorBucketsResponse{VectorBuckets: make([]VectorBucketSummary, 0, len(buckets))}
	for _, bucket := range buckets {
		resp.VectorBuckets = append(resp.VectorBuckets, VectorBucketSummary{
			VectorBucketARN:  formatBucketARN(req.Region, req.AccountID, bucket.Name),
			VectorBucketName: bucket.Name,
			CreationTime:     bucket.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return resp, nil
}

func (s *BucketService) DeleteVectorBucket(ctx context.Context, req *DeleteVectorBucketRequest) error {
	bucketName, _, err := req.Target.ResolveNames()
	if err != nil {
		return err
	}
	bucket, err := s.store.GetBucketByName(ctx, bucketName)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	count, err := s.store.CountCollections(ctx, bucket.ID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInternal, err)
	}
	if count > 0 {
		return errors.Join(ErrConflict, errors.New("delete all vector indexes before deleting the vector bucket"))
	}
	if err := s.store.DeleteBucket(ctx, bucket.ID); err != nil {
		return fmt.Errorf("%w: %v", ErrInternal, err)
	}
	return nil
}

func formatBucketARN(region, accountID, bucketName string) string {
	return fmt.Sprintf("arn:aws:s3vectors:%s:%s:bucket/%s", region, accountID, bucketName)
}

func formatIndexARN(region, accountID, bucketName, indexName string) string {
	return fmt.Sprintf("arn:aws:s3vectors:%s:%s:bucket/%s/index/%s", region, accountID, bucketName, indexName)
}
