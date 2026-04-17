package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juicedata/juicefs-milvus/milvus/bridge/internal/api"
)

type stubService struct {
	lastCreate api.CreateCollectionRequest
	searchResp []api.SearchResult
}

func (s *stubService) CreateCollection(ctx context.Context, req api.CreateCollectionRequest) error {
	s.lastCreate = req
	return nil
}

func (s *stubService) DropCollection(context.Context, string) error              { return nil }
func (s *stubService) HasCollection(context.Context, string) (bool, error)       { return true, nil }
func (s *stubService) CreateIndex(context.Context, api.CreateIndexRequest) error { return nil }
func (s *stubService) LoadCollection(context.Context, string) error              { return nil }
func (s *stubService) ReleaseCollection(context.Context, string) error           { return nil }
func (s *stubService) Insert(context.Context, api.ModifyVectorsRequest) error    { return nil }
func (s *stubService) Upsert(context.Context, api.ModifyVectorsRequest) error    { return nil }
func (s *stubService) Delete(context.Context, api.ModifyVectorsRequest) error    { return nil }
func (s *stubService) Search(context.Context, api.SearchRequest) ([]api.SearchResult, error) {
	return s.searchResp, nil
}

func TestCreateCollectionHandler(t *testing.T) {
	svc := &stubService{}
	server := httptest.NewServer(NewHandler(svc))
	defer server.Close()

	resp, err := http.Post(server.URL+"/v1/collections", "application/json", strings.NewReader(`{"name":"demo","dim":4,"metric":"COSINE"}`))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	assert.Equal(t, "demo", svc.lastCreate.Name)
	assert.Equal(t, 4, svc.lastCreate.Dim)
}

func TestSearchHandler(t *testing.T) {
	svc := &stubService{
		searchResp: []api.SearchResult{{ID: "v1", Score: 0.9, Metadata: []byte(`{"tenant":"t1"}`)}},
	}
	server := httptest.NewServer(NewHandler(svc))
	defer server.Close()

	resp, err := http.Post(server.URL+"/v1/vectors/search", "application/json", strings.NewReader(`{"name":"demo","vector":[0.1,0.2],"topK":3,"nprobe":16,"metric":"COSINE"}`))
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	var payload api.SearchResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	require.Len(t, payload.Results, 1)
	assert.Equal(t, "v1", payload.Results[0].ID)
}
