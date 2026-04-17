package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type vectorTestObjectLayer struct {
	ObjectLayer
	createBucketReq *CreateVectorBucketRequest
	putVectorsReq   *PutVectorsRequest
	queryVectorsReq *QueryVectorsRequest
}

func (v *vectorTestObjectLayer) CreateVectorBucket(ctx context.Context, req *CreateVectorBucketRequest) (*CreateVectorBucketResponse, error) {
	v.createBucketReq = req
	return &CreateVectorBucketResponse{VectorBucketARN: "arn:aws:s3vectors:us-east-1:123456789012:bucket/demo"}, nil
}

func (v *vectorTestObjectLayer) GetVectorBucket(ctx context.Context, req *GetVectorBucketRequest) (*GetVectorBucketResponse, error) {
	return &GetVectorBucketResponse{}, nil
}

func (v *vectorTestObjectLayer) ListVectorBuckets(ctx context.Context, req *ListVectorBucketsRequest) (*ListVectorBucketsResponse, error) {
	return &ListVectorBucketsResponse{}, nil
}

func (v *vectorTestObjectLayer) DeleteVectorBucket(ctx context.Context, req *DeleteVectorBucketRequest) error {
	return nil
}

func (v *vectorTestObjectLayer) CreateIndex(ctx context.Context, req *CreateIndexRequest) (*CreateIndexResponse, error) {
	return &CreateIndexResponse{IndexARN: "arn:aws:s3vectors:us-east-1:123456789012:bucket/demo/index/main"}, nil
}

func (v *vectorTestObjectLayer) DeleteIndex(ctx context.Context, req *DeleteIndexRequest) error {
	return nil
}

func (v *vectorTestObjectLayer) PutVectors(ctx context.Context, req *PutVectorsRequest) error {
	v.putVectorsReq = req
	return nil
}

func (v *vectorTestObjectLayer) DeleteVectors(ctx context.Context, req *DeleteVectorsRequest) error {
	return nil
}

func (v *vectorTestObjectLayer) QueryVectors(ctx context.Context, req *QueryVectorsRequest) (*QueryVectorsResponse, error) {
	v.queryVectorsReq = req
	return &QueryVectorsResponse{
		DistanceMetric: "cosine",
		Vectors: []QueryResultVector{
			{Key: "v1", Distance: 0.9, Metadata: json.RawMessage(`{"tenant":"t1"}`)},
		},
	}, nil
}

func newVectorTestRouter(t *testing.T, endpoints ...string) (*vectorTestObjectLayer, http.Handler) {
	t.Helper()
	obj, disk, err := prepareFS()
	require.NoError(t, err)
	t.Cleanup(func() { removeRoots([]string{disk}) })
	layer := &vectorTestObjectLayer{ObjectLayer: obj}
	return layer, initTestAPIEndPoints(layer, endpoints)
}

func TestCreateVectorBucketHandler(t *testing.T) {
	layer, router := newVectorTestRouter(t, "CreateVectorBucket")

	body := bytes.NewBufferString(`{"vectorBucketName":"demo","tags":{"env":"test"}}`)
	req := httptest.NewRequest(http.MethodPost, "/CreateVectorBucket", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Amz-Account-Id", "123456789012")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp CreateVectorBucketResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, "arn:aws:s3vectors:us-east-1:123456789012:bucket/demo", resp.VectorBucketARN)
	require.NotNil(t, layer.createBucketReq)
	require.Equal(t, "demo", layer.createBucketReq.VectorBucketName)
	require.Equal(t, "123456789012", layer.createBucketReq.AccountID)
}

func TestPutVectorsHandler(t *testing.T) {
	layer, router := newVectorTestRouter(t, "PutVectors")

	body := bytes.NewBufferString(`{"vectorBucketName":"demo","indexName":"main","vectors":[{"key":"v1","data":{"float32":[0.1,0.2,0.3,0.4]},"metadata":{"tenant":"t1"}}]}`)
	req := httptest.NewRequest(http.MethodPost, "/PutVectors", body)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.NotNil(t, layer.putVectorsReq)
	require.Equal(t, "demo", layer.putVectorsReq.VectorBucketName)
	require.Equal(t, "main", layer.putVectorsReq.IndexName)
	require.Len(t, layer.putVectorsReq.Vectors, 1)
	require.Equal(t, "v1", layer.putVectorsReq.Vectors[0].Key)
}

func TestQueryVectorsHandler(t *testing.T) {
	layer, router := newVectorTestRouter(t, "QueryVectors")

	body := bytes.NewBufferString(`{"vectorBucketName":"demo","indexName":"main","queryVector":{"float32":[0.1,0.2,0.3,0.4]},"topK":5,"returnDistance":true,"returnMetadata":true}`)
	req := httptest.NewRequest(http.MethodPost, "/QueryVectors", body)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp QueryVectorsResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, "cosine", resp.DistanceMetric)
	require.Len(t, resp.Vectors, 1)
	require.NotNil(t, layer.queryVectorsReq)
	require.Equal(t, 5, layer.queryVectorsReq.TopK)
	require.True(t, layer.queryVectorsReq.ReturnMetadata)
}

func TestDecodeVectorRequestAllowsPayloadUnder20MiB(t *testing.T) {
	paddingSize := (20 << 20) - 1024
	body := bytes.NewBufferString(fmt.Sprintf(`{"vectorBucketName":"demo","indexName":"main","vectors":[{"key":"v1","data":{"float32":[0.1]},"metadata":{"padding":"%s"}}]}`, strings.Repeat("a", paddingSize)))
	req := httptest.NewRequest(http.MethodPost, "/PutVectors", body)

	var decoded PutVectorsRequest
	require.NoError(t, decodeVectorRequest(req, &decoded))
	require.Equal(t, "demo", decoded.VectorBucketName)
}

func TestDecodeVectorRequestRejectsPayloadOver20MiB(t *testing.T) {
	paddingSize := 20 << 20
	body := bytes.NewBufferString(fmt.Sprintf(`{"vectorBucketName":"demo","indexName":"main","vectors":[{"key":"v1","data":{"float32":[0.1]},"metadata":{"padding":"%s"}}]}`, strings.Repeat("a", paddingSize)))
	req := httptest.NewRequest(http.MethodPost, "/PutVectors", body)

	var decoded PutVectorsRequest
	require.Error(t, decodeVectorRequest(req, &decoded))
}
