//go:build milvus_integration
// +build milvus_integration

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/milvusclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestNewMilvusAdapterDoesNotConnectEagerly(t *testing.T) {
	adp, err := NewMilvusAdapter("127.0.0.1:1")
	require.NoError(t, err)
	require.NotNil(t, adp)
	require.NoError(t, adp.Close())
}

func TestBuildCollectionSchemaUsesLongPrimaryKey(t *testing.T) {
	schema := buildCollectionSchema(256)

	require.NotNil(t, schema)
	require.Len(t, schema.Fields, 4)

	pk := schema.PKField()
	require.NotNil(t, pk)
	assert.Equal(t, "id", pk.Name)
	assert.Equal(t, entity.FieldTypeVarChar, pk.DataType)
	assert.Equal(t, "1024", pk.TypeParams["max_length"])

	vector := schema.Fields[1]
	dim, err := vector.GetDim()
	require.NoError(t, err)
	assert.EqualValues(t, 256, dim)
}

func TestNewCreateCollectionOptionHasNoImplicitIndexes(t *testing.T) {
	opt := newCreateCollectionOption("demo", 128)
	require.NotNil(t, opt)
	assert.Empty(t, opt.Indexes())
}

func TestDeleteAndFlushCallsFlushAfterDelete(t *testing.T) {
	client := &stubDeleteFlushClient{}

	err := deleteAndFlush(context.Background(), client, "demo", []string{"a", "b"})
	require.NoError(t, err)
	assert.Equal(t, []string{"delete", "flush", "await"}, client.calls)
	assert.Equal(t, "demo", client.deletedCollection)
	assert.Equal(t, `id in ["a","b"]`, client.deleteExpr)
	assert.Equal(t, "demo", client.flushedCollection)
}

func TestDeleteAndFlushStopsOnDeleteError(t *testing.T) {
	client := &stubDeleteFlushClient{deleteErr: errors.New("boom")}

	err := deleteAndFlush(context.Background(), client, "demo", []string{"a"})
	require.EqualError(t, err, "boom")
	assert.Equal(t, []string{"delete"}, client.calls)
}

func TestSubmitCreateIndexDoesNotAwaitTask(t *testing.T) {
	client := &stubCreateIndexClient{}

	err := submitCreateIndex(context.Background(), client, "demo", IndexSpec{Tier: "standard", VectorCount: 0, Metric: "cosine"})
	require.NoError(t, err)
	assert.Equal(t, []string{"create"}, client.calls)
	assert.Equal(t, "demo", client.collection)
	assert.Equal(t, "IVF_SQ8", client.extraParams["index_type"])
}

func TestSubmitCreateIndexUsesHNSWForPerformanceTier(t *testing.T) {
	client := &stubCreateIndexClient{}

	err := submitCreateIndex(context.Background(), client, "demo", IndexSpec{
		IndexType:          "hnsw",
		Tier:               "performance",
		Metric:             "cosine",
		HNSWM:              32,
		HNSWEFConstruction: 400,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"create"}, client.calls)
	assert.Equal(t, "demo", client.collection)
	assert.Equal(t, "HNSW", client.extraParams["index_type"])
	assert.Equal(t, "32", client.extraParams["M"])
	assert.Equal(t, "400", client.extraParams["efConstruction"])
}

func TestSubmitCreateIndexUsesDiskANNForDiskBucket(t *testing.T) {
	client := &stubCreateIndexClient{}

	err := submitCreateIndex(context.Background(), client, "demo", IndexSpec{
		IndexType: "diskann",
		Metric:    "cosine",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"create"}, client.calls)
	assert.Equal(t, "DISKANN", client.extraParams["index_type"])
}

type stubDeleteFlushClient struct {
	calls             []string
	deletedCollection string
	deleteExpr        string
	flushedCollection string
	deleteErr         error
	flushErr          error
	awaitErr          error
}

func (s *stubDeleteFlushClient) Delete(ctx context.Context, option milvusclient.DeleteOption, callOptions ...interface{}) (milvusclient.DeleteResult, error) {
	s.calls = append(s.calls, "delete")
	req := option.Request()
	s.deletedCollection = req.GetCollectionName()
	s.deleteExpr = req.GetExpr()
	return milvusclient.DeleteResult{}, s.deleteErr
}

func (s *stubDeleteFlushClient) Flush(ctx context.Context, option milvusclient.FlushOption, callOptions ...interface{}) (flushAwaiter, error) {
	s.calls = append(s.calls, "flush")
	req := option.Request()
	if len(req.GetCollectionNames()) > 0 {
		s.flushedCollection = req.GetCollectionNames()[0]
	}
	return stubFlushTask{
		onAwait: func() {
			s.calls = append(s.calls, "await")
		},
		err: s.awaitErr,
	}, s.flushErr
}

type stubFlushTask struct {
	onAwait func()
	err     error
}

func (s stubFlushTask) Await(ctx context.Context) error {
	if s.onAwait != nil {
		s.onAwait()
	}
	return s.err
}

type stubCreateIndexClient struct {
	calls      []string
	collection string
	extraParams map[string]string
	createErr  error
	awaitErr   error
}

func (s *stubCreateIndexClient) CreateIndex(ctx context.Context, option milvusclient.CreateIndexOption, callOptions ...grpc.CallOption) (createIndexAwaiter, error) {
	s.calls = append(s.calls, "create")
	req := option.Request()
	s.collection = req.GetCollectionName()
	s.extraParams = make(map[string]string, len(req.GetExtraParams()))
	for _, param := range req.GetExtraParams() {
		s.extraParams[param.GetKey()] = param.GetValue()
	}
	return stubCreateIndexTask{
		onAwait: func() {
			s.calls = append(s.calls, "await")
		},
		err: s.awaitErr,
	}, s.createErr
}

type stubCreateIndexTask struct {
	onAwait func()
	err     error
}

func (s stubCreateIndexTask) Await(ctx context.Context) error {
	if s.onAwait != nil {
		s.onAwait()
	}
	return s.err
}
