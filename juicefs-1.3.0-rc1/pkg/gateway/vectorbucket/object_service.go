package vectorbucket

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/adapter"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/config"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/controller"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metadata"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/quota"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/router"
)

type ObjectService struct {
	store      metadata.Store
	router     *router.NamespaceRouter
	adapter    adapter.Adapter
	controller *controller.LoadController
	quota      *quota.Checker
	cfg        config.Config
}

func NewObjectService(store metadata.Store, ns *router.NamespaceRouter, milvus adapter.Adapter, ctrl *controller.LoadController, q *quota.Checker, cfg config.Config) *ObjectService {
	return &ObjectService{store: store, router: ns, adapter: milvus, controller: ctrl, quota: q, cfg: cfg}
}

func (s *ObjectService) CreateIndex(ctx context.Context, req *CreateIndexRequest) (*CreateIndexResponse, error) {
	if req.DataType != "float32" {
		return nil, fmt.Errorf("%w: dataType must be float32", ErrValidation)
	}
	if err := s.quota.CheckDimension(req.Dimension); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	metric := normalizeMetric(req.DistanceMetric)
	if err := s.quota.CheckMetric(metric); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidation, err)
	}

	bucketName, _, err := req.Target.ResolveNames()
	if err != nil {
		return nil, err
	}
	bucket, err := s.store.GetBucketByName(ctx, bucketName)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	if err := s.quota.CanCreateCollection(ctx, bucket.ID); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrQuotaExceeded, err)
	}

	indexID := uuid.NewString()
	now := time.Now().UTC()
	coll := &metadata.LogicalCollection{
		ID:           indexID,
		BucketID:     bucket.ID,
		Name:         req.IndexName,
		Dim:          req.Dimension,
		Metric:       metric,
		Status:       metadata.CollStatusInit,
		PhysicalName: router.PhysicalCollectionName(bucket.ID, indexID),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.store.CreateCollection(ctx, coll); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConflict, err)
	}
	if err := s.adapter.CreateCollection(ctx, coll.PhysicalName, coll.Dim, coll.Metric); err != nil {
		_ = s.store.DeleteCollection(ctx, coll.ID)
		return nil, fmt.Errorf("%w: %v", ErrInternal, err)
	}
	if err := s.store.UpdateCollectionStatus(ctx, coll.ID, metadata.CollStatusReady); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInternal, err)
	}
	return &CreateIndexResponse{
		IndexARN: formatIndexARN(req.Region, req.AccountID, bucketName, req.IndexName),
	}, nil
}

func (s *ObjectService) DeleteIndex(ctx context.Context, req *DeleteIndexRequest) error {
	bucketName, indexName, err := req.Target.ResolveNames()
	if err != nil {
		return err
	}
	coll, err := s.router.Resolve(ctx, bucketName, indexName)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	_ = s.store.UpdateCollectionStatus(ctx, coll.ID, metadata.CollStatusDeleting)
	_ = s.adapter.ReleaseCollection(ctx, coll.PhysicalName)
	if err := s.adapter.DropCollection(ctx, coll.PhysicalName); err != nil {
		return fmt.Errorf("%w: %v", ErrInternal, err)
	}
	if err := s.store.DeleteCollection(ctx, coll.ID); err != nil {
		return fmt.Errorf("%w: %v", ErrInternal, err)
	}
	return nil
}

func (s *ObjectService) PutVectors(ctx context.Context, req *PutVectorsRequest) error {
	bucketName, indexName, err := req.Target.ResolveNames()
	if err != nil {
		return err
	}
	coll, err := s.router.Resolve(ctx, bucketName, indexName)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	if err := s.quota.CheckVectorCount(coll.VectorCount, len(req.Vectors)); err != nil {
		return fmt.Errorf("%w: %v", ErrQuotaExceeded, err)
	}

	ids := make([]string, 0, len(req.Vectors))
	vectors := make([][]float32, 0, len(req.Vectors))
	metadataJSON := make([][]byte, 0, len(req.Vectors))
	timestamps := make([]int64, 0, len(req.Vectors))
	for _, vector := range req.Vectors {
		if len(vector.Data.Float32) != coll.Dim {
			return fmt.Errorf("%w: vector %q dimension mismatch", ErrValidation, vector.Key)
		}
		ids = append(ids, vector.Key)
		vectors = append(vectors, vector.Data.Float32)
		metadataJSON = append(metadataJSON, append([]byte(nil), vector.Metadata...))
		timestamps = append(timestamps, time.Now().UnixMilli())
	}

	if err := s.adapter.Upsert(ctx, coll.PhysicalName, ids, vectors, metadataJSON, timestamps); err != nil {
		return fmt.Errorf("%w: %v", ErrInternal, err)
	}
	if err := s.store.UpdateCollectionVectorCount(ctx, coll.ID, int64(len(req.Vectors))); err != nil {
		return fmt.Errorf("%w: %v", ErrInternal, err)
	}

	current, err := s.store.GetCollectionByID(ctx, coll.ID)
	if err == nil && !current.IndexBuilt && current.VectorCount >= int64(s.cfg.IndexBuildThreshold) {
		if err := s.adapter.CreateIndex(ctx, current.PhysicalName, current.VectorCount, current.Metric); err == nil {
			_ = s.store.UpdateCollectionIndexBuilt(ctx, current.ID, true)
		}
	}
	return nil
}

func (s *ObjectService) DeleteVectors(ctx context.Context, req *DeleteVectorsRequest) error {
	bucketName, indexName, err := req.Target.ResolveNames()
	if err != nil {
		return err
	}
	coll, err := s.router.Resolve(ctx, bucketName, indexName)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	if err := s.adapter.Delete(ctx, coll.PhysicalName, req.Keys); err != nil {
		return fmt.Errorf("%w: %v", ErrInternal, err)
	}
	if len(req.Keys) > 0 {
		_ = s.store.UpdateCollectionVectorCount(ctx, coll.ID, -int64(len(req.Keys)))
	}
	return nil
}

func normalizeMetric(metric string) string {
	switch strings.ToLower(metric) {
	case "euclidean", "l2":
		return "L2"
	default:
		return "COSINE"
	}
}
