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
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metrics"
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
	policy := s.cfg.PolicyForBucket(bucketName)
	indexType := policy.IndexType
	tier := s.cfg.TierForIndexType(indexType)
	maxVectors := int64(0)
	pinned := false
	if s.cfg.IsPinnedIndexType(indexType) {
		maxVectors = policy.MaxVectors
		if err := s.quota.CanCreatePerformanceCollection(ctx, maxVectors, req.Dimension); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrQuotaExceeded, err)
		}
		pinned = true
	}
	coll := &metadata.LogicalCollection{
		ID:           indexID,
		BucketID:     bucket.ID,
		Name:         req.IndexName,
		Dim:          req.Dimension,
		Metric:       metric,
		IndexType:    indexType,
		Tier:         tier,
		MaxVectors:   maxVectors,
		Pinned:       pinned,
		Status:       metadata.CollStatusInit,
		PhysicalName: router.PhysicalCollectionNameForTier(tier, bucket.ID, indexID),
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
	if err := s.adapter.CreateIndex(ctx, coll.PhysicalName, adapter.IndexSpec{
		IndexType:          coll.IndexType,
		Tier:               coll.Tier,
		VectorCount:        coll.VectorCount,
		Metric:             coll.Metric,
		HNSWM:              policy.HNSWM,
		HNSWEFConstruction: policy.HNSWEFConstruction,
	}); err != nil {
		_ = s.adapter.DropCollection(ctx, coll.PhysicalName)
		_ = s.store.DeleteCollection(ctx, coll.ID)
		return nil, fmt.Errorf("%w: %v", ErrInternal, err)
	}
	coll.IndexBuilt = true
	if err := s.store.UpdateCollectionIndexBuilt(ctx, coll.ID, true); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInternal, err)
	}
	if err := s.store.UpdateCollectionStatus(ctx, coll.ID, metadata.CollStatusReady); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInternal, err)
	}
	if coll.Pinned {
		started := time.Now()
		estMem := controller.EstimateMemMB(coll.MaxVectors, coll.Dim)
		if err := s.controller.EnsureLoaded(ctx, coll.PhysicalName, estMem); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
		}
		s.controller.Pin(coll.PhysicalName)
		metrics.LoadDuration.WithLabelValues(coll.Tier).Observe(time.Since(started).Seconds())
		metrics.LoadedCollectionCount.WithLabelValues(coll.Tier).Inc()
		metrics.CollectionMemEstimate.WithLabelValues(coll.PhysicalName, coll.Tier).Set(estMem)
	}
	metrics.LogicalCollectionCount.WithLabelValues(string(metadata.CollStatusReady), coll.Tier).Inc()
	metrics.IndexCreateTotal.WithLabelValues(coll.Tier).Inc()
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
	if coll.Pinned {
		s.controller.Unpin(coll.PhysicalName)
		metrics.LoadedCollectionCount.WithLabelValues(coll.Tier).Dec()
	}
	_ = s.adapter.ReleaseCollection(ctx, coll.PhysicalName)
	if err := s.adapter.DropCollection(ctx, coll.PhysicalName); err != nil {
		return fmt.Errorf("%w: %v", ErrInternal, err)
	}
	if err := s.store.DeleteCollection(ctx, coll.ID); err != nil {
		return fmt.Errorf("%w: %v", ErrInternal, err)
	}
	metrics.LogicalCollectionCount.WithLabelValues(string(metadata.CollStatusReady), coll.Tier).Dec()
	metrics.CollectionMemEstimate.DeleteLabelValues(coll.PhysicalName, coll.Tier)
	metrics.IndexDeleteTotal.WithLabelValues(coll.Tier).Inc()
	return nil
}

func (s *ObjectService) ChangeIndexModel(ctx context.Context, req *ChangeIndexModelRequest) (*ChangeIndexModelResponse, error) {
	bucketName, indexName, err := req.Target.ResolveNames()
	if err != nil {
		return nil, err
	}
	coll, err := s.router.Resolve(ctx, bucketName, indexName)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	if coll.MigrateState != "" {
		return nil, fmt.Errorf("%w: collection is already migrating", ErrConflict)
	}

	indexType := normalizeIndexType(req.IndexModel)
	if indexType == "" {
		return nil, fmt.Errorf("%w: unsupported index model %q", ErrValidation, req.IndexModel)
	}
	if indexType == coll.IndexType {
		return nil, fmt.Errorf("%w: index model is already %s", ErrConflict, indexType)
	}

	bucket, err := s.store.GetBucketByName(ctx, bucketName)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	targetTier := s.cfg.TierForIndexType(indexType)
	targetPhysical := router.PhysicalCollectionNameForTier(targetTier, bucket.ID, fmt.Sprintf("%s_mig_%s", coll.ID, uuid.NewString()))
	if err := s.store.UpdateCollectionMigrationState(ctx, coll.ID, "UPGRADING", indexType, coll.PhysicalName, targetPhysical, time.Now().UTC(), ""); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInternal, err)
	}

	return &ChangeIndexModelResponse{
		IndexARN:   formatIndexARN(req.Region, req.AccountID, bucketName, indexName),
		IndexModel: indexType,
		State:      "UPGRADING",
	}, nil
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
	if coll.MigrateState != "" {
		return fmt.Errorf("%w: collection is migrating", ErrServiceUnavailable)
	}
	if err := s.quota.CheckVectorCount(coll.VectorCount, len(req.Vectors)); err != nil {
		return fmt.Errorf("%w: %v", ErrQuotaExceeded, err)
	}
	if s.cfg.IsPinnedIndexType(coll.IndexType) {
		if err := s.quota.CheckPerformanceVectorLimit(coll.VectorCount, len(req.Vectors), coll.MaxVectors); err != nil {
			return fmt.Errorf("%w: %v", ErrQuotaExceeded, err)
		}
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
	if coll.Pinned {
		estMem := coll.EstMemMB
		if estMem <= 0 {
			estMem = controller.EstimateMemMB(coll.MaxVectors, coll.Dim)
		}
		if err := s.controller.Release(ctx, coll.PhysicalName); err != nil {
			return fmt.Errorf("%w: %v", ErrInternal, err)
		}
		if err := s.controller.EnsureLoaded(ctx, coll.PhysicalName, estMem); err != nil {
			return fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
		}
		s.controller.Pin(coll.PhysicalName)
	}
	metrics.InsertTotal.WithLabelValues(bucketName, indexName, coll.Tier).Add(float64(len(req.Vectors)))
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
	if coll.MigrateState != "" {
		return fmt.Errorf("%w: collection is migrating", ErrServiceUnavailable)
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

func normalizeIndexType(indexType string) string {
	switch strings.ToLower(strings.TrimSpace(indexType)) {
	case "ivf_sq8":
		return "ivf_sq8"
	case "hnsw":
		return "hnsw"
	case "diskann":
		return "diskann"
	default:
		return ""
	}
}
