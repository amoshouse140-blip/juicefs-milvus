package service

import (
	"context"
	"fmt"
	"math"

	"github.com/milvus-io/milvus/client/v2/column"
	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/index"
	"github.com/milvus-io/milvus/client/v2/milvusclient"
	"github.com/milvus-io/milvus/pkg/v2/common"

	"github.com/juicedata/juicefs-milvus/milvus/bridge/internal/api"
)

type Service struct {
	client *milvusclient.Client
}

const primaryKeyMaxLength = 1024

func New(ctx context.Context, addr string) (*Service, error) {
	client, err := milvusclient.New(ctx, &milvusclient.ClientConfig{
		Address: addr,
	})
	if err != nil {
		return nil, fmt.Errorf("connect to milvus at %s: %w", addr, err)
	}
	return &Service{client: client}, nil
}

func (s *Service) Close(ctx context.Context) error {
	return s.client.Close(ctx)
}

func (s *Service) CreateCollection(ctx context.Context, req api.CreateCollectionRequest) error {
	schema := entity.NewSchema().
		WithField(entity.NewField().WithName("id").WithDataType(entity.FieldTypeVarChar).WithMaxLength(primaryKeyMaxLength).WithIsPrimaryKey(true)).
		WithField(entity.NewField().WithName("vector").WithDataType(entity.FieldTypeFloatVector).WithDim(int64(req.Dim))).
		WithField(entity.NewField().WithName("metadata").WithDataType(entity.FieldTypeJSON)).
		WithField(entity.NewField().WithName("created_at").WithDataType(entity.FieldTypeInt64))

	return s.client.CreateCollection(ctx, milvusclient.NewCreateCollectionOption(req.Name, schema).
		WithShardNum(1).
		WithProperty(common.MmapEnabledKey, true))
}

func (s *Service) DropCollection(ctx context.Context, name string) error {
	return s.client.DropCollection(ctx, milvusclient.NewDropCollectionOption(name))
}

func (s *Service) HasCollection(ctx context.Context, name string) (bool, error) {
	return s.client.HasCollection(ctx, milvusclient.NewHasCollectionOption(name))
}

func (s *Service) CreateIndex(ctx context.Context, req api.CreateIndexRequest) error {
	idx := milvusclient.NewCreateIndexOption(req.Name, "vector", index.NewIvfSQ8Index(metricTypeFromString(req.Metric), computeNlist(req.VectorCount)))
	idx.WithExtraParam(common.MmapEnabledKey, true)
	task, err := s.client.CreateIndex(ctx, idx)
	if err != nil {
		return err
	}
	return task.Await(ctx)
}

func (s *Service) LoadCollection(ctx context.Context, name string) error {
	task, err := s.client.LoadCollection(ctx, milvusclient.NewLoadCollectionOption(name))
	if err != nil {
		return err
	}
	return task.Await(ctx)
}

func (s *Service) ReleaseCollection(ctx context.Context, name string) error {
	return s.client.ReleaseCollection(ctx, milvusclient.NewReleaseCollectionOption(name))
}

func (s *Service) Insert(ctx context.Context, req api.ModifyVectorsRequest) error {
	_, err := s.client.Insert(ctx, milvusclient.NewColumnBasedInsertOption(req.Name).
		WithVarcharColumn("id", req.IDs).
		WithFloatVectorColumn("vector", len(req.Vectors[0]), req.Vectors).
		WithColumns(column.NewColumnJSONBytes("metadata", req.MetadataJSON)).
		WithInt64Column("created_at", req.Timestamps))
	return err
}

func (s *Service) Upsert(ctx context.Context, req api.ModifyVectorsRequest) error {
	_, err := s.client.Upsert(ctx, milvusclient.NewColumnBasedInsertOption(req.Name).
		WithVarcharColumn("id", req.IDs).
		WithFloatVectorColumn("vector", len(req.Vectors[0]), req.Vectors).
		WithColumns(column.NewColumnJSONBytes("metadata", req.MetadataJSON)).
		WithInt64Column("created_at", req.Timestamps))
	return err
}

func (s *Service) Delete(ctx context.Context, req api.ModifyVectorsRequest) error {
	_, err := s.client.Delete(ctx, milvusclient.NewDeleteOption(req.Name).WithStringIDs("id", req.IDs))
	if err != nil {
		return err
	}
	task, err := s.client.Flush(ctx, milvusclient.NewFlushOption(req.Name))
	if err != nil {
		return err
	}
	return task.Await(ctx)
}

func (s *Service) Search(ctx context.Context, req api.SearchRequest) ([]api.SearchResult, error) {
	opt := milvusclient.NewSearchOption(req.Name, req.TopK, []entity.Vector{entity.FloatVector(req.Vector)}).
		WithANNSField("vector").
		WithOutputFields("metadata").
		WithAnnParam(index.NewIvfAnnParam(req.Nprobe))
	if req.Filter != "" {
		opt = opt.WithFilter(req.Filter)
	}
	results, err := s.client.Search(ctx, opt)
	if err != nil {
		return nil, err
	}

	var out []api.SearchResult
	for _, resultSet := range results {
		metaColumn := resultSet.GetColumn("metadata")
		for i := 0; i < resultSet.ResultCount; i++ {
			id, err := resultSet.IDs.GetAsString(i)
			if err != nil {
				return nil, err
			}
			item := api.SearchResult{
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

func metricTypeFromString(metric string) entity.MetricType {
	switch metric {
	case "L2":
		return entity.L2
	case "COSINE":
		return entity.COSINE
	default:
		return entity.COSINE
	}
}
