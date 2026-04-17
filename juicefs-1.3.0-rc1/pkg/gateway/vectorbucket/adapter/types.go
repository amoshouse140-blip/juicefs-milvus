package adapter

import "context"

type IndexSpec struct {
	IndexType          string
	Tier               string
	VectorCount        int64
	Metric             string
	HNSWM              int
	HNSWEFConstruction int
}

type SearchSpec struct {
	IndexType string
	Tier    string
	Metric  string
	NProbe  int
	HNSWEF  int
	DiskANNSearchList int
	Filter  string
	TopK    int
}

type Adapter interface {
	CreateCollection(ctx context.Context, name string, dim int, metric string) error
	DropCollection(ctx context.Context, name string) error
	HasCollection(ctx context.Context, name string) (bool, error)
	CreateIndex(ctx context.Context, name string, spec IndexSpec) error
	LoadCollection(ctx context.Context, name string) error
	ReleaseCollection(ctx context.Context, name string) error
	Insert(ctx context.Context, name string, ids []string, vectors [][]float32, metadataJSON [][]byte, timestamps []int64) error
	Upsert(ctx context.Context, name string, ids []string, vectors [][]float32, metadataJSON [][]byte, timestamps []int64) error
	Delete(ctx context.Context, name string, ids []string) error
	Search(ctx context.Context, name string, vector []float32, spec SearchSpec) ([]SearchResult, error)
	Scan(ctx context.Context, name string, batchSize int, fn func([]VectorRecord) error) error
	Count(ctx context.Context, name string) (int64, error)
	Close() error
}

type SearchResult struct {
	ID       string
	Score    float32
	Metadata []byte
}

type VectorRecord struct {
	ID        string
	Vector    []float32
	Metadata  []byte
	CreatedAt int64
}
