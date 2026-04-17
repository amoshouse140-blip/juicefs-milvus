package vectorbucket

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/adapter"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/config"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/controller"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metadata"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metrics"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/router"
	"github.com/juicedata/juicefs/pkg/utils"
)

var queryLogger = utils.GetLogger("vectorbucket-query")

type QueryService struct {
	store      metadata.Store
	router     *router.NamespaceRouter
	adapter    adapter.Adapter
	controller *controller.LoadController
	cfg        config.Config
}

func NewQueryService(store metadata.Store, ns *router.NamespaceRouter, milvus adapter.Adapter, ctrl *controller.LoadController, cfg config.Config) *QueryService {
	return &QueryService{store: store, router: ns, adapter: milvus, controller: ctrl, cfg: cfg}
}

func (s *QueryService) QueryVectors(ctx context.Context, req *QueryVectorsRequest) (*QueryVectorsResponse, error) {
	bucketName, indexName, err := req.Target.ResolveNames()
	if err != nil {
		return nil, err
	}
	coll, err := s.router.Resolve(ctx, bucketName, indexName)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	if len(req.QueryVector.Float32) != coll.Dim {
		return nil, fmt.Errorf("%w: queryVector dimension mismatch", ErrValidation)
	}

	estMem := coll.EstMemMB
	if estMem <= 0 {
		estMem = controller.EstimateMemMB(coll.VectorCount, coll.Dim)
	}
	queryLogger.Infof("query vectors routing: bucket=%s index=%s physical=%s model=%s tier=%s topk=%d est_mem_mb=%.2f", bucketName, indexName, coll.PhysicalName, coll.IndexType, coll.Tier, req.TopK, estMem)
	loadStarted := time.Now()
	if err := s.controller.EnsureLoaded(ctx, coll.PhysicalName, estMem); err != nil {
		queryLogger.Errorf("query ensure load failed: bucket=%s index=%s physical=%s err=%v", bucketName, indexName, coll.PhysicalName, err)
		return nil, fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}
	metrics.LoadDuration.WithLabelValues(coll.Tier).Observe(time.Since(loadStarted).Seconds())
	s.controller.InFlightInc(coll.PhysicalName)
	defer s.controller.InFlightDec(coll.PhysicalName)
	s.controller.Touch(coll.PhysicalName)
	_ = s.store.UpdateCollectionLastAccess(ctx, coll.ID)

	filter := strings.TrimSpace(string(req.Filter))
	policy := s.cfg.PolicyForBucket(bucketName)
	searchStarted := time.Now()
	results, err := s.adapter.Search(ctx, coll.PhysicalName, req.QueryVector.Float32, adapter.SearchSpec{
		IndexType:         coll.IndexType,
		Tier:              coll.Tier,
		Metric:            coll.Metric,
		NProbe:            16,
		HNSWEF:            policy.HNSWEFConstruction,
		DiskANNSearchList: policy.DiskANNSearchList,
		Filter:            filter,
		TopK:              req.TopK,
	})
	if err != nil {
		queryLogger.Errorf("query search failed: bucket=%s index=%s physical=%s model=%s err=%v", bucketName, indexName, coll.PhysicalName, coll.IndexType, err)
		return nil, fmt.Errorf("%w: %v", ErrInternal, err)
	}
	metrics.QueryDuration.WithLabelValues("search", coll.Tier).Observe(time.Since(searchStarted).Seconds())
	metrics.QueryTotal.WithLabelValues(bucketName, indexName, coll.Tier).Inc()
	resp := &QueryVectorsResponse{
		DistanceMetric: strings.ToLower(coll.Metric),
		Vectors:        make([]QueryResultVector, 0, len(results)),
	}
	for _, result := range results {
		item := QueryResultVector{Key: result.ID}
		if req.ReturnDistance {
			item.Distance = result.Score
		}
		if req.ReturnMetadata {
			item.Metadata = append([]byte(nil), result.Metadata...)
		}
		resp.Vectors = append(resp.Vectors, item)
	}
	queryLogger.Infof("query vectors completed: bucket=%s index=%s physical=%s result_count=%d", bucketName, indexName, coll.PhysicalName, len(resp.Vectors))
	return resp, nil
}
