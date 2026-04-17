//go:build !milvus_integration

package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrMilvusBridgeAddrRequired = errors.New("milvus bridge address is required in the default build")

type MilvusAdapter struct {
	baseURL string
	client  *http.Client
}

type bridgeErrorResponse struct {
	Error string `json:"error"`
}

type createCollectionRequest struct {
	Name   string `json:"name"`
	Dim    int    `json:"dim"`
	Metric string `json:"metric"`
}

type hasCollectionResponse struct {
	Exists bool `json:"exists"`
}

type createIndexRequest struct {
	Name        string `json:"name"`
	VectorCount int64  `json:"vectorCount"`
	Metric      string `json:"metric"`
}

type modifyVectorsRequest struct {
	Name         string      `json:"name"`
	IDs          []string    `json:"ids"`
	Vectors      [][]float32 `json:"vectors,omitempty"`
	MetadataJSON [][]byte    `json:"metadataJSON,omitempty"`
	Timestamps   []int64     `json:"timestamps,omitempty"`
}

type searchRequest struct {
	Name   string    `json:"name"`
	Vector []float32 `json:"vector"`
	TopK   int       `json:"topK"`
	Nprobe int       `json:"nprobe"`
	Filter string    `json:"filter,omitempty"`
	Metric string    `json:"metric"`
}

type searchResponse struct {
	Results []SearchResult `json:"results"`
}

func NewMilvusAdapter(addr string) (*MilvusAdapter, error) {
	baseURL, err := normalizeBridgeBaseURL(addr)
	if err != nil {
		return nil, err
	}
	return &MilvusAdapter{
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

func (a *MilvusAdapter) Close() error { return nil }

func (a *MilvusAdapter) CreateCollection(ctx context.Context, name string, dim int, metric string) error {
	return a.doJSON(ctx, http.MethodPost, "/v1/collections", createCollectionRequest{
		Name:   name,
		Dim:    dim,
		Metric: normalizeMetric(metric),
	}, nil)
}

func (a *MilvusAdapter) DropCollection(ctx context.Context, name string) error {
	return a.doJSON(ctx, http.MethodDelete, "/v1/collections/"+url.PathEscape(name), nil, nil)
}

func (a *MilvusAdapter) HasCollection(ctx context.Context, name string) (bool, error) {
	var resp hasCollectionResponse
	if err := a.doJSON(ctx, http.MethodGet, "/v1/collections/"+url.PathEscape(name), nil, &resp); err != nil {
		return false, err
	}
	return resp.Exists, nil
}

func (a *MilvusAdapter) CreateIndex(ctx context.Context, name string, vectorCount int64, metric string) error {
	return a.doJSON(ctx, http.MethodPost, "/v1/indexes", createIndexRequest{
		Name:        name,
		VectorCount: vectorCount,
		Metric:      normalizeMetric(metric),
	}, nil)
}

func (a *MilvusAdapter) LoadCollection(ctx context.Context, name string) error {
	return a.doJSON(ctx, http.MethodPost, "/v1/collections/"+url.PathEscape(name)+"/load", nil, nil)
}

func (a *MilvusAdapter) ReleaseCollection(ctx context.Context, name string) error {
	return a.doJSON(ctx, http.MethodPost, "/v1/collections/"+url.PathEscape(name)+"/release", nil, nil)
}

func (a *MilvusAdapter) Insert(ctx context.Context, name string, ids []string, vectors [][]float32, metadataJSON [][]byte, timestamps []int64) error {
	return a.doJSON(ctx, http.MethodPost, "/v1/vectors/insert", modifyVectorsRequest{
		Name:         name,
		IDs:          ids,
		Vectors:      vectors,
		MetadataJSON: metadataJSON,
		Timestamps:   timestamps,
	}, nil)
}

func (a *MilvusAdapter) Upsert(ctx context.Context, name string, ids []string, vectors [][]float32, metadataJSON [][]byte, timestamps []int64) error {
	return a.doJSON(ctx, http.MethodPost, "/v1/vectors/upsert", modifyVectorsRequest{
		Name:         name,
		IDs:          ids,
		Vectors:      vectors,
		MetadataJSON: metadataJSON,
		Timestamps:   timestamps,
	}, nil)
}

func (a *MilvusAdapter) Delete(ctx context.Context, name string, ids []string) error {
	return a.doJSON(ctx, http.MethodPost, "/v1/vectors/delete", modifyVectorsRequest{
		Name: name,
		IDs:  ids,
	}, nil)
}

func (a *MilvusAdapter) Search(ctx context.Context, name string, vector []float32, topK int, nprobe int, filter string, metric string) ([]SearchResult, error) {
	var resp searchResponse
	if err := a.doJSON(ctx, http.MethodPost, "/v1/vectors/search", searchRequest{
		Name:   name,
		Vector: vector,
		TopK:   topK,
		Nprobe: nprobe,
		Filter: filter,
		Metric: normalizeMetric(metric),
	}, &resp); err != nil {
		return nil, err
	}
	return resp.Results, nil
}

func (a *MilvusAdapter) doJSON(ctx context.Context, method string, path string, reqBody any, respBody any) error {
	var body io.Reader
	if reqBody != nil {
		payload, err := json.Marshal(reqBody)
		if err != nil {
			return err
		}
		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, body)
	if err != nil {
		return err
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		var apiErr bridgeErrorResponse
		if err := json.NewDecoder(resp.Body).Decode(&apiErr); err == nil && apiErr.Error != "" {
			return errors.New(apiErr.Error)
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("milvus bridge %s %s failed: status=%d body=%s", method, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	if respBody == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(respBody)
}

func normalizeBridgeBaseURL(addr string) (string, error) {
	trimmed := strings.TrimSpace(addr)
	if trimmed == "" {
		return "", ErrMilvusBridgeAddrRequired
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "http://" + trimmed
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("unsupported milvus bridge scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("milvus bridge host is required")
	}
	return strings.TrimRight(u.String(), "/"), nil
}
