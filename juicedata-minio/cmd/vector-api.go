package cmd

import (
	"context"
	"encoding/json"
	"net/http"
)

type RequestContext struct {
	AccountID string      `json:"-"`
	Region    string      `json:"-"`
	RequestID string      `json:"-"`
	Headers   http.Header `json:"-"`
}

type Target struct {
	VectorBucketName string `json:"vectorBucketName,omitempty"`
	VectorBucketARN  string `json:"vectorBucketArn,omitempty"`
	IndexName        string `json:"indexName,omitempty"`
	IndexARN         string `json:"indexArn,omitempty"`
}

type EncryptionConfiguration struct {
	SSEType   string `json:"sseType,omitempty"`
	KMSKeyARN string `json:"kmsKeyArn,omitempty"`
}

type MetadataConfiguration struct {
	NonFilterableMetadataKeys []string `json:"nonFilterableMetadataKeys,omitempty"`
}

type VectorData struct {
	Float32 []float32 `json:"float32"`
}

type PutInputVector struct {
	Key      string          `json:"key"`
	Data     VectorData      `json:"data"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

type QueryResultVector struct {
	Key      string          `json:"key"`
	Distance float32         `json:"distance,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

type CreateVectorBucketRequest struct {
	RequestContext
	VectorBucketName        string                   `json:"vectorBucketName"`
	EncryptionConfiguration *EncryptionConfiguration `json:"encryptionConfiguration,omitempty"`
	Tags                    map[string]string        `json:"tags,omitempty"`
}

type CreateVectorBucketResponse struct {
	VectorBucketARN string `json:"vectorBucketArn"`
}

type GetVectorBucketRequest struct {
	RequestContext
	Target
}

type GetVectorBucketResponse struct {
	VectorBucketARN         string                   `json:"vectorBucketArn"`
	VectorBucketName        string                   `json:"vectorBucketName"`
	CreationTime            string                   `json:"creationTime"`
	EncryptionConfiguration *EncryptionConfiguration `json:"encryptionConfiguration,omitempty"`
}

type ListVectorBucketsRequest struct {
	RequestContext
	MaxResults int    `json:"maxResults,omitempty"`
	NextToken  string `json:"nextToken,omitempty"`
}

type VectorBucketSummary struct {
	VectorBucketARN  string `json:"vectorBucketArn"`
	VectorBucketName string `json:"vectorBucketName"`
	CreationTime     string `json:"creationTime"`
}

type ListVectorBucketsResponse struct {
	VectorBuckets []VectorBucketSummary `json:"vectorBuckets"`
	NextToken     string                `json:"nextToken,omitempty"`
}

type DeleteVectorBucketRequest struct {
	RequestContext
	Target
}

type CreateIndexRequest struct {
	RequestContext
	Target
	IndexName               string                   `json:"indexName"`
	DataType                string                   `json:"dataType"`
	Dimension               int                      `json:"dimension"`
	DistanceMetric          string                   `json:"distanceMetric"`
	EncryptionConfiguration *EncryptionConfiguration `json:"encryptionConfiguration,omitempty"`
	MetadataConfiguration   *MetadataConfiguration   `json:"metadataConfiguration,omitempty"`
	Tags                    map[string]string        `json:"tags,omitempty"`
}

type CreateIndexResponse struct {
	IndexARN string `json:"indexArn"`
}

type DeleteIndexRequest struct {
	RequestContext
	Target
}

type PutVectorsRequest struct {
	RequestContext
	Target
	Vectors []PutInputVector `json:"vectors"`
}

type DeleteVectorsRequest struct {
	RequestContext
	Target
	Keys []string `json:"keys"`
}

type QueryVectorsRequest struct {
	RequestContext
	Target
	QueryVector    VectorData      `json:"queryVector"`
	TopK           int             `json:"topK"`
	Filter         json.RawMessage `json:"filter,omitempty"`
	ReturnDistance bool            `json:"returnDistance,omitempty"`
	ReturnMetadata bool            `json:"returnMetadata,omitempty"`
}

type QueryVectorsResponse struct {
	DistanceMetric string              `json:"distanceMetric"`
	Vectors        []QueryResultVector `json:"vectors"`
}

type VectorAPI interface {
	CreateVectorBucket(context.Context, *CreateVectorBucketRequest) (*CreateVectorBucketResponse, error)
	GetVectorBucket(context.Context, *GetVectorBucketRequest) (*GetVectorBucketResponse, error)
	ListVectorBuckets(context.Context, *ListVectorBucketsRequest) (*ListVectorBucketsResponse, error)
	DeleteVectorBucket(context.Context, *DeleteVectorBucketRequest) error
	CreateIndex(context.Context, *CreateIndexRequest) (*CreateIndexResponse, error)
	DeleteIndex(context.Context, *DeleteIndexRequest) error
	PutVectors(context.Context, *PutVectorsRequest) error
	DeleteVectors(context.Context, *DeleteVectorsRequest) error
	QueryVectors(context.Context, *QueryVectorsRequest) (*QueryVectorsResponse, error)
}

type VectorAPIError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Status     int    `json:"-"`
	RetryAfter int    `json:"-"`
}

func (e VectorAPIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}
