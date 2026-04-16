package adapter

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
	assert.NotNil(t, metricTypeFromString("L2"))
	assert.NotNil(t, metricTypeFromString("COSINE"))
	assert.NotNil(t, metricTypeFromString("unknown"))
}
