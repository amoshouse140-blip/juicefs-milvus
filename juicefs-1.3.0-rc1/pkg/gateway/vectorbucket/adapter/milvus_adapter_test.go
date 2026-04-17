package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputeNlist(t *testing.T) {
	assert.Equal(t, 1024, computeNlist(100))
	assert.Equal(t, 1264, computeNlist(100000))
	assert.Equal(t, 65536, computeNlist(1000000000))
}

func TestBuildIDFilter(t *testing.T) {
	assert.Equal(t, "", buildIDFilter(nil))
	assert.Equal(t, `id in ["a"]`, buildIDFilter([]string{"a"}))
	assert.Equal(t, `id in ["a","b","c"]`, buildIDFilter([]string{"a", "b", "c"}))
}

func TestMetricTypeFromString(t *testing.T) {
	assert.Equal(t, "L2", normalizeMetric("L2"))
	assert.Equal(t, "COSINE", normalizeMetric("COSINE"))
	assert.Equal(t, "COSINE", normalizeMetric("unknown"))
}

func TestNormalizeBridgeBaseURL(t *testing.T) {
	baseURL, err := normalizeBridgeBaseURL("127.0.0.1:19531")
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:19531", baseURL)
}

func TestHTTPMilvusAdapterSearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v1/vectors/search", r.URL.Path)

		var req searchRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "demo", req.Name)
		assert.Equal(t, "COSINE", req.Metric)

		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(searchResponse{
			Results: []SearchResult{{ID: "v1", Score: 0.9, Metadata: []byte(`{"tenant":"t1"}`)}},
		}))
	}))
	defer server.Close()

	adp, err := NewMilvusAdapter(server.URL)
	require.NoError(t, err)

	results, err := adp.Search(context.Background(), "demo", []float32{0.1, 0.2}, 5, 16, "", "unknown")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "v1", results[0].ID)
	assert.Equal(t, float32(0.9), results[0].Score)
}
