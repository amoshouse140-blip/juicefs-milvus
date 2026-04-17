//go:build milvus_integration
// +build milvus_integration

package adapter

import (
	"context"
	"fmt"

	"github.com/milvus-io/milvus/client/v2/column"
	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/index"
	"github.com/milvus-io/milvus/client/v2/milvusclient"
	"github.com/milvus-io/milvus/pkg/v2/common"
)

type MilvusAdapter struct {
	client *milvusclient.Client
}

func NewMilvusAdapter(addr string) (*MilvusAdapter, error) {
	client, err := milvusclient.New(context.Background(), &milvusclient.ClientConfig{
		Address: addr,
	})
	if err != nil {
		return nil, fmt.Errorf("connect to milvus at %s: %w", addr, err)
	}
	return &MilvusAdapter{client: client}, nil
}

func (a *MilvusAdapter) Close() error {
	return a.client.Close(context.Background())
}

func (a *MilvusAdapter) CreateCollection(ctx context.Context, name string, dim int, metric string) error {
	schema := entity.NewSchema().
		WithField(entity.NewField().WithName("id").WithDataType(entity.FieldTypeVarChar).WithMaxLength(64).WithIsPrimaryKey(true)).
		WithField(entity.NewField().WithName("vector").WithDataType(entity.FieldTypeFloatVector).WithDim(int64(dim))).
		WithField(entity.NewField().WithName("metadata").WithDataType(entity.FieldTypeJSON)).
		WithField(entity.NewField().WithName("created_at").WithDataType(entity.FieldTypeInt64))

	return a.client.CreateCollection(ctx, milvusclient.NewCreateCollectionOption(name, schema).
		WithShardNum(1).
		WithProperty(common.MmapEnabledKey, true).
		WithIndexOptions(milvusclient.NewCreateIndexOption(name, "vector", index.NewIvfSQ8Index(metricTypeFromString(metric), 1024))))
}

func (a *MilvusAdapter) DropCollection(ctx context.Context, name string) error {
	return a.client.DropCollection(ctx, milvusclient.NewDropCollectionOption(name))
}

func (a *MilvusAdapter) HasCollection(ctx context.Context, name string) (bool, error) {
	return a.client.HasCollection(ctx, milvusclient.NewHasCollectionOption(name))
}

func metricTypeFromString(metric string) entity.MetricType {
	switch normalizeMetric(metric) {
	case "L2":
		return entity.L2
	case "COSINE":
		return entity.COSINE
	default:
		return entity.COSINE
	}
}

func (a *MilvusAdapter) CreateIndex(ctx context.Context, name string, vectorCount int64, metric string) error {
	idx := milvusclient.NewCreateIndexOption(name, "vector", index.NewIvfSQ8Index(metricTypeFromString(metric), computeNlist(vectorCount)))
	idx.WithExtraParam(common.MmapEnabledKey, true)
	task, err := a.client.CreateIndex(ctx, idx)
	if err != nil {
		return err
	}
	return task.Await(ctx)
}

func (a *MilvusAdapter) LoadCollection(ctx context.Context, name string) error {
	task, err := a.client.LoadCollection(ctx, milvusclient.NewLoadCollectionOption(name))
	if err != nil {
		return err
	}
	return task.Await(ctx)
}

func (a *MilvusAdapter) ReleaseCollection(ctx context.Context, name string) error {
	return a.client.ReleaseCollection(ctx, milvusclient.NewReleaseCollectionOption(name))
}

func (a *MilvusAdapter) Insert(ctx context.Context, name string, ids []string, vectors [][]float32, metadataJSON [][]byte, timestamps []int64) error {
	_, err := a.client.Insert(ctx, milvusclient.NewColumnBasedInsertOption(name).
		WithVarcharColumn("id", ids).
		WithFloatVectorColumn("vector", len(vectors[0]), vectors).
		WithColumns(column.NewColumnJSONBytes("metadata", metadataJSON)).
		WithInt64Column("created_at", timestamps))
	return err
}

func (a *MilvusAdapter) Upsert(ctx context.Context, name string, ids []string, vectors [][]float32, metadataJSON [][]byte, timestamps []int64) error {
	_, err := a.client.Upsert(ctx, milvusclient.NewColumnBasedInsertOption(name).
		WithVarcharColumn("id", ids).
		WithFloatVectorColumn("vector", len(vectors[0]), vectors).
		WithColumns(column.NewColumnJSONBytes("metadata", metadataJSON)).
		WithInt64Column("created_at", timestamps))
	return err
}

func (a *MilvusAdapter) Delete(ctx context.Context, name string, ids []string) error {
	_, err := a.client.Delete(ctx, milvusclient.NewDeleteOption(name).WithStringIDs("id", ids))
	return err
}

func (a *MilvusAdapter) Search(ctx context.Context, name string, vector []float32, topK int, nprobe int, filter string, metric string) ([]SearchResult, error) {
	opt := milvusclient.NewSearchOption(name, topK, []entity.Vector{entity.FloatVector(vector)}).
		WithANNSField("vector").
		WithOutputFields("metadata").
		WithAnnParam(index.NewIvfAnnParam(nprobe))
	if filter != "" {
		opt = opt.WithFilter(filter)
	}
	results, err := a.client.Search(ctx, opt)
	if err != nil {
		return nil, err
	}

	var out []SearchResult
	for _, resultSet := range results {
		metaColumn := resultSet.GetColumn("metadata")
		for i := 0; i < resultSet.ResultCount; i++ {
			id, err := resultSet.IDs.GetAsString(i)
			if err != nil {
				return nil, err
			}
			item := SearchResult{
				ID:    id,
				Score: resultSet.Scores[i],
			}
			if metaColumn != nil {
				value, err := metaColumn.Get(i)
				if err != nil {
					return nil, err
				}
				if raw, ok := value.([]byte); ok {
					item.Metadata = raw
				}
			}
			out = append(out, item)
		}
	}
	return out, nil
}
