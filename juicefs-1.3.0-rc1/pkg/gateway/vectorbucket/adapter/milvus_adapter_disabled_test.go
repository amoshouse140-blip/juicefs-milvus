//go:build !milvus_integration

package adapter

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDisabledAdapterReturnsIntegrationDisabled(t *testing.T) {
	adp, err := NewMilvusAdapter("")
	require.NoError(t, err)

	assert.ErrorIs(t, adp.CreateCollection(context.Background(), "demo", 4, "COSINE"), ErrMilvusIntegrationDisabled)
	_, err = adp.Search(context.Background(), "demo", []float32{0.1, 0.2}, SearchSpec{
		Tier:   "standard",
		Metric: "COSINE",
		NProbe: 16,
		TopK:   5,
	})
	assert.ErrorIs(t, err, ErrMilvusIntegrationDisabled)
}
