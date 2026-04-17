//go:build !milvus_integration

package adapter

import (
	"context"
	"errors"
)

var ErrMilvusIntegrationDisabled = errors.New("milvus integration is disabled in the default build")

type MilvusAdapter struct{}

func NewMilvusAdapter(addr string) (*MilvusAdapter, error) {
	_ = addr
	return &MilvusAdapter{}, nil
}

func (a *MilvusAdapter) Close() error { return nil }

func (a *MilvusAdapter) CreateCollection(ctx context.Context, name string, dim int, metric string) error {
	return ErrMilvusIntegrationDisabled
}

func (a *MilvusAdapter) DropCollection(ctx context.Context, name string) error {
	return ErrMilvusIntegrationDisabled
}

func (a *MilvusAdapter) HasCollection(ctx context.Context, name string) (bool, error) {
	return false, ErrMilvusIntegrationDisabled
}

func (a *MilvusAdapter) CreateIndex(ctx context.Context, name string, spec IndexSpec) error {
	_ = spec
	return ErrMilvusIntegrationDisabled
}

func (a *MilvusAdapter) LoadCollection(ctx context.Context, name string) error {
	return ErrMilvusIntegrationDisabled
}

func (a *MilvusAdapter) ReleaseCollection(ctx context.Context, name string) error {
	return ErrMilvusIntegrationDisabled
}

func (a *MilvusAdapter) Insert(ctx context.Context, name string, ids []string, vectors [][]float32, metadataJSON [][]byte, timestamps []int64) error {
	return ErrMilvusIntegrationDisabled
}

func (a *MilvusAdapter) Upsert(ctx context.Context, name string, ids []string, vectors [][]float32, metadataJSON [][]byte, timestamps []int64) error {
	return ErrMilvusIntegrationDisabled
}

func (a *MilvusAdapter) Delete(ctx context.Context, name string, ids []string) error {
	return ErrMilvusIntegrationDisabled
}

func (a *MilvusAdapter) Search(ctx context.Context, name string, vector []float32, spec SearchSpec) ([]SearchResult, error) {
	_ = spec
	return nil, ErrMilvusIntegrationDisabled
}

func (a *MilvusAdapter) Scan(ctx context.Context, name string, batchSize int, fn func([]VectorRecord) error) error {
	return ErrMilvusIntegrationDisabled
}

func (a *MilvusAdapter) Count(ctx context.Context, name string) (int64, error) {
	return 0, ErrMilvusIntegrationDisabled
}
