package adapter

import "context"

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
