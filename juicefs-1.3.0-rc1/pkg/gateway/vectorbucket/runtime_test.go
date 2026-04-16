package vectorbucket

import "testing"

func TestRuntimeImplementsExtension(t *testing.T) {
	var _ Extension = (*Runtime)(nil)
}
