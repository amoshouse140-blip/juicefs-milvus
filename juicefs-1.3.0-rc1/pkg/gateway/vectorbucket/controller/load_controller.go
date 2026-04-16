package controller

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type LoadReleaser interface {
	LoadCollection(ctx context.Context, name string) error
	ReleaseCollection(ctx context.Context, name string) error
}

type LoadEntry struct {
	Name          string
	LoadedAt      time.Time
	LastAccessAt  time.Time
	EstMemMB      float64
	InFlightCount int64
}

type LoadController struct {
	mu          sync.Mutex
	adapter     LoadReleaser
	budgetMB    float64
	ttl         time.Duration
	maxLoaded   int
	entries     map[string]*LoadEntry
	lruOrder    []string
	loadLocks   map[string]*sync.Mutex
	loadLocksMu sync.Mutex
}

func NewLoadController(adapter LoadReleaser, budgetMB int, ttl time.Duration, maxLoaded int) *LoadController {
	return &LoadController{
		adapter:   adapter,
		budgetMB:  float64(budgetMB),
		ttl:       ttl,
		maxLoaded: maxLoaded,
		entries:   make(map[string]*LoadEntry),
		loadLocks: make(map[string]*sync.Mutex),
	}
}

func (c *LoadController) getLoadLock(name string) *sync.Mutex {
	c.loadLocksMu.Lock()
	defer c.loadLocksMu.Unlock()
	if lock, ok := c.loadLocks[name]; ok {
		return lock
	}
	lock := &sync.Mutex{}
	c.loadLocks[name] = lock
	return lock
}

func (c *LoadController) IsLoaded(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.entries[name]
	return ok
}

func (c *LoadController) Touch(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[name]; ok {
		entry.LastAccessAt = time.Now()
		c.moveLRUToBack(name)
	}
}

func (c *LoadController) InFlightInc(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[name]; ok {
		entry.InFlightCount++
	}
}

func (c *LoadController) InFlightDec(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[name]; ok && entry.InFlightCount > 0 {
		entry.InFlightCount--
	}
}

func (c *LoadController) EnsureLoaded(ctx context.Context, name string, estMemMB float64) error {
	c.mu.Lock()
	if entry, ok := c.entries[name]; ok {
		entry.LastAccessAt = time.Now()
		c.moveLRUToBack(name)
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	lock := c.getLoadLock(name)
	lock.Lock()
	defer lock.Unlock()

	c.mu.Lock()
	if entry, ok := c.entries[name]; ok {
		entry.LastAccessAt = time.Now()
		c.moveLRUToBack(name)
		c.mu.Unlock()
		return nil
	}
	if err := c.evictIfNeededLocked(estMemMB); err != nil {
		c.mu.Unlock()
		return err
	}
	c.mu.Unlock()

	if err := c.adapter.LoadCollection(ctx, name); err != nil {
		return fmt.Errorf("load collection %s: %w", name, err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	c.entries[name] = &LoadEntry{
		Name:         name,
		LoadedAt:     now,
		LastAccessAt: now,
		EstMemMB:     estMemMB,
	}
	c.lruOrder = append(c.lruOrder, name)
	return nil
}

func (c *LoadController) RunTTLSweep() {
	c.mu.Lock()
	now := time.Now()
	var toRelease []string
	for name, entry := range c.entries {
		if now.Sub(entry.LastAccessAt) > c.ttl && entry.InFlightCount == 0 {
			toRelease = append(toRelease, name)
		}
	}
	c.mu.Unlock()

	for _, name := range toRelease {
		_ = c.adapter.ReleaseCollection(context.Background(), name)
		c.mu.Lock()
		delete(c.entries, name)
		c.removeLRU(name)
		c.mu.Unlock()
	}
}

func (c *LoadController) StartTTLSweepLoop(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.RunTTLSweep()
			}
		}
	}()
}

func EstimateMemMB(vectorCount int64, dim int) float64 {
	const overheadRatio = 1.2
	bytes := float64(vectorCount) * float64(dim) * overheadRatio
	return bytes / (1024 * 1024)
}

func (c *LoadController) evictIfNeededLocked(neededMB float64) error {
	for c.usedMBLocked()+neededMB > c.budgetMB || (c.maxLoaded > 0 && len(c.entries) >= c.maxLoaded) {
		if len(c.lruOrder) == 0 {
			return fmt.Errorf("no collections to evict, budget exhausted")
		}

		evicted := false
		for _, candidate := range c.lruOrder {
			entry := c.entries[candidate]
			if entry == nil || entry.InFlightCount > 0 {
				continue
			}
			if err := c.adapter.ReleaseCollection(context.Background(), candidate); err != nil {
				return fmt.Errorf("release collection %s: %w", candidate, err)
			}
			delete(c.entries, candidate)
			c.removeLRU(candidate)
			evicted = true
			break
		}
		if !evicted {
			return fmt.Errorf("all loaded collections have in-flight queries, cannot evict")
		}
	}
	return nil
}

func (c *LoadController) usedMBLocked() float64 {
	var total float64
	for _, entry := range c.entries {
		total += entry.EstMemMB
	}
	return total
}

func (c *LoadController) moveLRUToBack(name string) {
	c.removeLRU(name)
	c.lruOrder = append(c.lruOrder, name)
}

func (c *LoadController) removeLRU(name string) {
	for i, current := range c.lruOrder {
		if current == name {
			c.lruOrder = append(c.lruOrder[:i], c.lruOrder[i+1:]...)
			return
		}
	}
}
