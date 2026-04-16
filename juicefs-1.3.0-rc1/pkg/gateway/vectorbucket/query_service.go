package vectorbucket

import (
	"context"
	"fmt"
	"strings"

	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/adapter"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/controller"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metadata"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/router"
)

type QueryService struct {
	store      metadata.Store
	router     *router.NamespaceRouter
	adapter    adapter.Adapter
	controller *controller.LoadController
}

func NewQueryService(store metadata.Store, ns *router.NamespaceRouter, milvus adapter.Adapter, ctrl *controller.LoadController) *QueryService {
	return &QueryService{store: store, router: ns, adapter: milvus, controller: ctrl}
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
	if err := s.controller.EnsureLoaded(ctx, coll.PhysicalName, estMem); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}
	s.controller.InFlightInc(coll.PhysicalName)
	defer s.controller.InFlightDec(coll.PhysicalName)
	s.controller.Touch(coll.PhysicalName)
	_ = s.store.UpdateCollectionLastAccess(ctx, coll.ID)

	filter := strings.TrimSpace(string(req.Filter))
	results, err := s.adapter.Search(ctx, coll.PhysicalName, req.QueryVector.Float32, req.TopK, 16, filter, coll.Metric)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInternal, err)
	}
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
	return resp, nil
}
