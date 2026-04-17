package vectorbucket

import (
	"context"

	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/adapter"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/config"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/controller"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metadata"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/quota"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/router"
)

type Runtime struct {
	cfg        config.Config
	store      metadata.Store
	router     *router.NamespaceRouter
	adapter    adapter.Adapter
	controller *controller.LoadController
	quota      *quota.Checker
	buckets    *BucketService
	objects    *ObjectService
	query      *QueryService
}

func NewRuntime(cfg config.Config, store metadata.Store, milvus adapter.Adapter, ctrl *controller.LoadController) *Runtime {
	r := &Runtime{
		cfg:        cfg,
		store:      store,
		router:     router.NewNamespaceRouter(store),
		adapter:    milvus,
		controller: ctrl,
		quota:      quota.NewChecker(store, &cfg),
	}
	r.buckets = NewBucketService(store, r.quota)
	r.objects = NewObjectService(store, r.router, milvus, ctrl, r.quota, cfg)
	r.query = NewQueryService(store, r.router, milvus, ctrl, cfg)
	return r
}

func (r *Runtime) Close(context.Context) error {
	if r.store != nil {
		return r.store.Close()
	}
	return nil
}

func (r *Runtime) CreateVectorBucket(ctx context.Context, req *CreateVectorBucketRequest) (*CreateVectorBucketResponse, error) {
	return r.buckets.CreateVectorBucket(ctx, req)
}

func (r *Runtime) GetVectorBucket(ctx context.Context, req *GetVectorBucketRequest) (*GetVectorBucketResponse, error) {
	return r.buckets.GetVectorBucket(ctx, req)
}

func (r *Runtime) ListVectorBuckets(ctx context.Context, req *ListVectorBucketsRequest) (*ListVectorBucketsResponse, error) {
	return r.buckets.ListVectorBuckets(ctx, req)
}

func (r *Runtime) DeleteVectorBucket(ctx context.Context, req *DeleteVectorBucketRequest) error {
	return r.buckets.DeleteVectorBucket(ctx, req)
}

func (r *Runtime) CreateIndex(ctx context.Context, req *CreateIndexRequest) (*CreateIndexResponse, error) {
	return r.objects.CreateIndex(ctx, req)
}

func (r *Runtime) DeleteIndex(ctx context.Context, req *DeleteIndexRequest) error {
	return r.objects.DeleteIndex(ctx, req)
}

func (r *Runtime) ChangeIndexModel(ctx context.Context, req *ChangeIndexModelRequest) (*ChangeIndexModelResponse, error) {
	return r.objects.ChangeIndexModel(ctx, req)
}

func (r *Runtime) PutVectors(ctx context.Context, req *PutVectorsRequest) error {
	return r.objects.PutVectors(ctx, req)
}

func (r *Runtime) DeleteVectors(ctx context.Context, req *DeleteVectorsRequest) error {
	return r.objects.DeleteVectors(ctx, req)
}

func (r *Runtime) QueryVectors(ctx context.Context, req *QueryVectorsRequest) (*QueryVectorsResponse, error) {
	return r.query.QueryVectors(ctx, req)
}
