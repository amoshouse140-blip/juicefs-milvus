package controller

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockAdapter struct {
	mu        sync.Mutex
	loaded    map[string]bool
	loadDelay time.Duration
}

func newMockAdapter() *mockAdapter {
	return &mockAdapter{loaded: make(map[string]bool)}
}

func (m *mockAdapter) LoadCollection(ctx context.Context, name string) error {
	if m.loadDelay > 0 {
		time.Sleep(m.loadDelay)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loaded[name] = true
	return nil
}

func (m *mockAdapter) ReleaseCollection(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.loaded, name)
	return nil
}

func (m *mockAdapter) isLoaded(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loaded[name]
}

func TestEnsureLoaded(t *testing.T) {
	ma := newMockAdapter()
	ctrl := NewLoadController(ma, 4096, 30*time.Minute, 50)

	err := ctrl.EnsureLoaded(context.Background(), "coll1", 100.0)
	require.NoError(t, err)
	assert.True(t, ma.isLoaded("coll1"))
	assert.True(t, ctrl.IsLoaded("coll1"))
}

func TestEnsureLoadedIdempotent(t *testing.T) {
	ma := newMockAdapter()
	ctrl := NewLoadController(ma, 4096, 30*time.Minute, 50)

	require.NoError(t, ctrl.EnsureLoaded(context.Background(), "coll1", 100.0))
	require.NoError(t, ctrl.EnsureLoaded(context.Background(), "coll1", 100.0))
	assert.True(t, ctrl.IsLoaded("coll1"))
}

func TestLRUEviction(t *testing.T) {
	ma := newMockAdapter()
	ctrl := NewLoadController(ma, 200, 30*time.Minute, 50)

	require.NoError(t, ctrl.EnsureLoaded(context.Background(), "coll1", 100.0))
	require.NoError(t, ctrl.EnsureLoaded(context.Background(), "coll2", 100.0))
	require.NoError(t, ctrl.EnsureLoaded(context.Background(), "coll3", 100.0))

	assert.False(t, ctrl.IsLoaded("coll1"))
	assert.True(t, ctrl.IsLoaded("coll2"))
	assert.True(t, ctrl.IsLoaded("coll3"))
}

func TestTTLRelease(t *testing.T) {
	ma := newMockAdapter()
	ctrl := NewLoadController(ma, 4096, 100*time.Millisecond, 50)

	require.NoError(t, ctrl.EnsureLoaded(context.Background(), "coll1", 100.0))
	assert.True(t, ctrl.IsLoaded("coll1"))

	time.Sleep(200 * time.Millisecond)
	ctrl.RunTTLSweep()

	assert.False(t, ctrl.IsLoaded("coll1"))
}

func TestInFlightPreventsRelease(t *testing.T) {
	ma := newMockAdapter()
	ctrl := NewLoadController(ma, 4096, 100*time.Millisecond, 50)

	require.NoError(t, ctrl.EnsureLoaded(context.Background(), "coll1", 100.0))
	ctrl.InFlightInc("coll1")

	time.Sleep(200 * time.Millisecond)
	ctrl.RunTTLSweep()
	assert.True(t, ctrl.IsLoaded("coll1"))

	ctrl.InFlightDec("coll1")
	ctrl.RunTTLSweep()
	assert.False(t, ctrl.IsLoaded("coll1"))
}

func TestMaxLoadedCollections(t *testing.T) {
	ma := newMockAdapter()
	ctrl := NewLoadController(ma, 999999, 30*time.Minute, 2)

	require.NoError(t, ctrl.EnsureLoaded(context.Background(), "c1", 1.0))
	require.NoError(t, ctrl.EnsureLoaded(context.Background(), "c2", 1.0))
	require.NoError(t, ctrl.EnsureLoaded(context.Background(), "c3", 1.0))

	assert.False(t, ctrl.IsLoaded("c1"))
}

func TestTouchUpdatesLRU(t *testing.T) {
	ma := newMockAdapter()
	ctrl := NewLoadController(ma, 200, 30*time.Minute, 50)

	require.NoError(t, ctrl.EnsureLoaded(context.Background(), "coll1", 100.0))
	require.NoError(t, ctrl.EnsureLoaded(context.Background(), "coll2", 100.0))
	ctrl.Touch("coll1")
	require.NoError(t, ctrl.EnsureLoaded(context.Background(), "coll3", 100.0))

	assert.True(t, ctrl.IsLoaded("coll1"))
	assert.False(t, ctrl.IsLoaded("coll2"))
}
