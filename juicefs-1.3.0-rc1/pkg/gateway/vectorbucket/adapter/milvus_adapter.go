package adapter

import (
	"context"
	"errors"
	"fmt"
	"math"
)

var ErrMilvusIntegrationDisabled = errors.New("milvus integration is disabled in the default build")

type Adapter interface {
	CreateCollection(ctx context.Context, name string, dim int, metric string) error
	DropCollection(ctx context.Context, name string) error
	HasCollection(ctx context.Context, name string) (bool, error)
	CreateIndex(ctx context.Context, name string, vectorCount int64, metric string) error
	LoadCollection(ctx context.Context, name string) error
	ReleaseCollection(ctx context.Context, name string) error
	Insert(ctx context.Context, name string, ids []string, vectors [][]float32, metadataJSON [][]byte, timestamps []int64) error
	Upsert(ctx context.Context, name string, ids []string, vectors [][]float32, metadataJSON [][]byte, timestamps []int64) error
	Delete(ctx context.Context, name string, ids []string) error
	Search(ctx context.Context, name string, vector []float32, topK int, nprobe int, filter string, metric string) ([]SearchResult, error)
	Close() error
}

type SearchResult struct {
	ID       string
	Score    float32
	Metadata []byte
}

type MilvusAdapter struct {
	addr string
}

func NewMilvusAdapter(addr string) (*MilvusAdapter, error) {
	return &MilvusAdapter{addr: addr}, nil
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

func (a *MilvusAdapter) CreateIndex(ctx context.Context, name string, vectorCount int64, metric string) error {
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

func (a *MilvusAdapter) Search(ctx context.Context, name string, vector []float32, topK int, nprobe int, filter string, metric string) ([]SearchResult, error) {
	return nil, ErrMilvusIntegrationDisabled
}

func computeNlist(vectorCount int64) int {
	nlist := int(math.Sqrt(float64(vectorCount)) * 4)
	if nlist < 1024 {
		nlist = 1024
	}
	if nlist > 65536 {
		nlist = 65536
	}
	return nlist
}

func metricTypeFromString(metric string) string {
	switch metric {
	case "L2":
		return "L2"
	case "COSINE":
		return "COSINE"
	default:
		return "COSINE"
	}
}

func buildIDFilter(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	filter := `id in [`
	for i, id := range ids {
		if i > 0 {
			filter += ","
		}
		filter += fmt.Sprintf("%q", id)
	}
	filter += `]`
	return filter
}
