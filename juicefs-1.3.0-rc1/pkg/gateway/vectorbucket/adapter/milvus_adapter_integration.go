//go:build milvus_integration
// +build milvus_integration

package adapter

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"sync"

	"github.com/milvus-io/milvus/client/v2/column"
	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/index"
	"github.com/milvus-io/milvus/client/v2/milvusclient"
	"github.com/milvus-io/milvus/pkg/v2/common"
	"google.golang.org/grpc"
)

const primaryKeyMaxLength = 1024

type MilvusAdapter struct {
	addr   string
	mu     sync.Mutex
	client *milvusclient.Client
}

type flushAwaiter interface {
	Await(ctx context.Context) error
}

type createIndexAwaiter interface {
	Await(ctx context.Context) error
}

type createIndexClient interface {
	CreateIndex(ctx context.Context, option milvusclient.CreateIndexOption, callOptions ...grpc.CallOption) (createIndexAwaiter, error)
}

type deleteFlushClient interface {
	Delete(ctx context.Context, option milvusclient.DeleteOption, callOptions ...interface{}) (milvusclient.DeleteResult, error)
	Flush(ctx context.Context, option milvusclient.FlushOption, callOptions ...interface{}) (flushAwaiter, error)
}

type deleteFlushClientAdapter struct {
	client *milvusclient.Client
}

type createIndexClientAdapter struct {
	client *milvusclient.Client
}

func NewMilvusAdapter(addr string) (*MilvusAdapter, error) {
	return &MilvusAdapter{addr: addr}, nil
}

func (a *MilvusAdapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == nil {
		return nil
	}
	return a.client.Close(context.Background())
}

func (a *MilvusAdapter) ensureClient() (*milvusclient.Client, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client != nil {
		return a.client, nil
	}
	client, err := milvusclient.New(context.Background(), &milvusclient.ClientConfig{
		Address: a.addr,
	})
	if err != nil {
		return nil, fmt.Errorf("connect to milvus at %s: %w", a.addr, err)
	}
	a.client = client
	return a.client, nil
}

func buildCollectionSchema(dim int) *entity.Schema {
	return entity.NewSchema().
		WithField(entity.NewField().WithName("id").WithDataType(entity.FieldTypeVarChar).WithMaxLength(primaryKeyMaxLength).WithIsPrimaryKey(true)).
		WithField(entity.NewField().WithName("vector").WithDataType(entity.FieldTypeFloatVector).WithDim(int64(dim))).
		WithField(entity.NewField().WithName("metadata").WithDataType(entity.FieldTypeJSON)).
		WithField(entity.NewField().WithName("created_at").WithDataType(entity.FieldTypeInt64))
}

func newCreateCollectionOption(name string, dim int) milvusclient.CreateCollectionOption {
	return milvusclient.NewCreateCollectionOption(name, buildCollectionSchema(dim)).
		WithShardNum(1).
		WithProperty(common.MmapEnabledKey, true)
}

func (a *MilvusAdapter) CreateCollection(ctx context.Context, name string, dim int, metric string) error {
	client, err := a.ensureClient()
	if err != nil {
		return err
	}
	return client.CreateCollection(ctx, newCreateCollectionOption(name, dim))
}

func (a *MilvusAdapter) DropCollection(ctx context.Context, name string) error {
	client, err := a.ensureClient()
	if err != nil {
		return err
	}
	return client.DropCollection(ctx, milvusclient.NewDropCollectionOption(name))
}

func (a *MilvusAdapter) HasCollection(ctx context.Context, name string) (bool, error) {
	client, err := a.ensureClient()
	if err != nil {
		return false, err
	}
	return client.HasCollection(ctx, milvusclient.NewHasCollectionOption(name))
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

func (c createIndexClientAdapter) CreateIndex(ctx context.Context, option milvusclient.CreateIndexOption, callOptions ...grpc.CallOption) (createIndexAwaiter, error) {
	return c.client.CreateIndex(ctx, option, callOptions...)
}

func submitCreateIndex(ctx context.Context, client createIndexClient, name string, spec IndexSpec) error {
	var idx milvusclient.CreateIndexOption
	switch spec.IndexType {
	case "hnsw":
		option := milvusclient.NewCreateIndexOption(name, "vector", index.NewHNSWIndex(metricTypeFromString(spec.Metric), spec.HNSWM, spec.HNSWEFConstruction))
		option.WithExtraParam(common.MmapEnabledKey, true)
		idx = option
	case "diskann":
		option := milvusclient.NewCreateIndexOption(name, "vector", index.NewDiskANNIndex(metricTypeFromString(spec.Metric)))
		option.WithExtraParam(common.MmapEnabledKey, true)
		idx = option
	default:
		option := milvusclient.NewCreateIndexOption(name, "vector", index.NewIvfSQ8Index(metricTypeFromString(spec.Metric), computeNlist(spec.VectorCount)))
		option.WithExtraParam(common.MmapEnabledKey, true)
		idx = option
	}
	_, err := client.CreateIndex(ctx, idx)
	return err
}

func (a *MilvusAdapter) CreateIndex(ctx context.Context, name string, spec IndexSpec) error {
	client, err := a.ensureClient()
	if err != nil {
		return err
	}
	return submitCreateIndex(ctx, createIndexClientAdapter{client: client}, name, spec)
}

func (a *MilvusAdapter) LoadCollection(ctx context.Context, name string) error {
	client, err := a.ensureClient()
	if err != nil {
		return err
	}
	task, err := client.LoadCollection(ctx, milvusclient.NewLoadCollectionOption(name))
	if err != nil {
		return err
	}
	return task.Await(ctx)
}

func (a *MilvusAdapter) ReleaseCollection(ctx context.Context, name string) error {
	client, err := a.ensureClient()
	if err != nil {
		return err
	}
	return client.ReleaseCollection(ctx, milvusclient.NewReleaseCollectionOption(name))
}

func (a *MilvusAdapter) Insert(ctx context.Context, name string, ids []string, vectors [][]float32, metadataJSON [][]byte, timestamps []int64) error {
	client, err := a.ensureClient()
	if err != nil {
		return err
	}
	_, err = client.Insert(ctx, milvusclient.NewColumnBasedInsertOption(name).
		WithVarcharColumn("id", ids).
		WithFloatVectorColumn("vector", len(vectors[0]), vectors).
		WithColumns(column.NewColumnJSONBytes("metadata", metadataJSON)).
		WithInt64Column("created_at", timestamps))
	return err
}

func (a *MilvusAdapter) Upsert(ctx context.Context, name string, ids []string, vectors [][]float32, metadataJSON [][]byte, timestamps []int64) error {
	client, err := a.ensureClient()
	if err != nil {
		return err
	}
	_, err = client.Upsert(ctx, milvusclient.NewColumnBasedInsertOption(name).
		WithVarcharColumn("id", ids).
		WithFloatVectorColumn("vector", len(vectors[0]), vectors).
		WithColumns(column.NewColumnJSONBytes("metadata", metadataJSON)).
		WithInt64Column("created_at", timestamps))
	return err
}

func (c deleteFlushClientAdapter) Delete(ctx context.Context, option milvusclient.DeleteOption, callOptions ...interface{}) (milvusclient.DeleteResult, error) {
	return c.client.Delete(ctx, option)
}

func (c deleteFlushClientAdapter) Flush(ctx context.Context, option milvusclient.FlushOption, callOptions ...interface{}) (flushAwaiter, error) {
	return c.client.Flush(ctx, option)
}

func deleteAndFlush(ctx context.Context, client deleteFlushClient, name string, ids []string) error {
	_, err := client.Delete(ctx, milvusclient.NewDeleteOption(name).WithStringIDs("id", ids))
	if err != nil {
		return err
	}
	task, err := client.Flush(ctx, milvusclient.NewFlushOption(name))
	if err != nil {
		return err
	}
	return task.Await(ctx)
}

func (a *MilvusAdapter) Delete(ctx context.Context, name string, ids []string) error {
	client, err := a.ensureClient()
	if err != nil {
		return err
	}
	return deleteAndFlush(ctx, deleteFlushClientAdapter{client: client}, name, ids)
}

func (a *MilvusAdapter) Search(ctx context.Context, name string, vector []float32, spec SearchSpec) ([]SearchResult, error) {
	client, err := a.ensureClient()
	if err != nil {
		return nil, err
	}
	opt := milvusclient.NewSearchOption(name, spec.TopK, []entity.Vector{entity.FloatVector(vector)}).
		WithANNSField("vector").
		WithOutputFields("metadata")
	switch spec.IndexType {
	case "hnsw":
		opt = opt.WithAnnParam(index.NewHNSWAnnParam(spec.HNSWEF))
	case "diskann":
		opt = opt.WithAnnParam(index.NewDiskAnnParam(spec.DiskANNSearchList))
	default:
		opt = opt.WithAnnParam(index.NewIvfAnnParam(spec.NProbe))
	}
	if spec.Filter != "" {
		opt = opt.WithFilter(spec.Filter)
	}
	results, err := client.Search(ctx, opt)
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

func (a *MilvusAdapter) Scan(ctx context.Context, name string, batchSize int, fn func([]VectorRecord) error) error {
	client, err := a.ensureClient()
	if err != nil {
		return err
	}
	if batchSize <= 0 {
		batchSize = 1000
	}
	iter, err := client.QueryIterator(ctx, milvusclient.NewQueryIteratorOption(name).
		WithBatchSize(batchSize).
		WithOutputFields("id", "vector", "metadata", "created_at"))
	if err != nil {
		return err
	}

	for {
		rs, err := iter.Next(ctx)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if rs.ResultCount == 0 {
			return nil
		}
		idColumn := rs.GetColumn("id")
		vectorColumn := rs.GetColumn("vector")
		metadataColumn := rs.GetColumn("metadata")
		createdAtColumn := rs.GetColumn("created_at")
		rows := make([]VectorRecord, 0, rs.ResultCount)
		for i := 0; i < rs.ResultCount; i++ {
			id, err := idColumn.GetAsString(i)
			if err != nil {
				return err
			}
			vectorValue, err := vectorColumn.Get(i)
			if err != nil {
				return err
			}
			floatVector, ok := vectorValue.(entity.FloatVector)
			if !ok {
				return fmt.Errorf("unexpected vector type %T", vectorValue)
			}
			record := VectorRecord{
				ID:     id,
				Vector: append([]float32(nil), []float32(floatVector)...),
			}
			if metadataColumn != nil {
				metaValue, err := metadataColumn.Get(i)
				if err != nil {
					return err
				}
				if raw, ok := metaValue.([]byte); ok {
					record.Metadata = append([]byte(nil), raw...)
				}
			}
			if createdAtColumn != nil {
				ts, err := createdAtColumn.GetAsInt64(i)
				if err == nil {
					record.CreatedAt = ts
				}
			}
			rows = append(rows, record)
		}
		if err := fn(rows); err != nil {
			return err
		}
	}
}

func (a *MilvusAdapter) Count(ctx context.Context, name string) (int64, error) {
	client, err := a.ensureClient()
	if err != nil {
		return 0, err
	}
	stats, err := client.GetCollectionStats(ctx, milvusclient.NewGetCollectionStatsOption(name))
	if err != nil {
		return 0, err
	}
	for _, key := range []string{"row_count", "count"} {
		if value, ok := stats[key]; ok {
			n, err := strconv.ParseInt(value, 10, 64)
			if err == nil {
				return n, nil
			}
		}
	}
	return 0, fmt.Errorf("row count not found for collection %s", name)
}
