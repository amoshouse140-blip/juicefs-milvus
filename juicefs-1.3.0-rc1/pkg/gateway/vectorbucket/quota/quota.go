package quota

import (
	"context"
	"fmt"

	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/config"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metadata"
)

type Checker struct {
	store metadata.Store
	cfg   *config.Config
}

func NewChecker(store metadata.Store, cfg *config.Config) *Checker {
	return &Checker{store: store, cfg: cfg}
}

func (c *Checker) CanCreateBucket(ctx context.Context, owner string) error {
	globalCount, err := c.store.CountBuckets(ctx)
	if err != nil {
		return fmt.Errorf("count buckets: %w", err)
	}
	if globalCount >= c.cfg.MaxBucketsGlobal {
		return fmt.Errorf("global bucket limit reached (%d)", c.cfg.MaxBucketsGlobal)
	}

	ownerCount, err := c.store.CountBucketsByOwner(ctx, owner)
	if err != nil {
		return fmt.Errorf("count buckets by owner: %w", err)
	}
	if ownerCount >= c.cfg.MaxBucketsPerTenant {
		return fmt.Errorf("per-tenant bucket limit reached (%d)", c.cfg.MaxBucketsPerTenant)
	}
	return nil
}

func (c *Checker) CanCreateCollection(ctx context.Context, bucketID string) error {
	cnt, err := c.store.CountCollections(ctx, bucketID)
	if err != nil {
		return fmt.Errorf("count collections: %w", err)
	}
	if cnt >= c.cfg.MaxCollPerBucket {
		return fmt.Errorf("collection limit per bucket reached (%d)", c.cfg.MaxCollPerBucket)
	}
	return nil
}

func (c *Checker) CheckDimension(dim int) error {
	if dim <= 0 {
		return fmt.Errorf("dimension must be positive, got %d", dim)
	}
	if dim > c.cfg.MaxDim {
		return fmt.Errorf("dimension %d exceeds maximum %d", dim, c.cfg.MaxDim)
	}
	return nil
}

func (c *Checker) CheckVectorCount(currentCount int64, insertCount int) error {
	if currentCount+int64(insertCount) > int64(c.cfg.MaxVectorsPerColl) {
		return fmt.Errorf("insert would exceed max vectors per collection (%d)", c.cfg.MaxVectorsPerColl)
	}
	return nil
}

func EstimatePerformanceMemMB(maxVectors int64, dim int) float64 {
	return float64(maxVectors) * float64(dim) * 4 * 1.5 / (1024 * 1024)
}

func (c *Checker) CanCreatePerformanceCollection(ctx context.Context, maxVectors int64, dim int) error {
	buckets, err := c.store.ListBuckets(ctx)
	if err != nil {
		return fmt.Errorf("list buckets: %w", err)
	}

	var usedMB float64
	for _, bucket := range buckets {
		collections, err := c.store.ListCollections(ctx, bucket.ID)
		if err != nil {
			return fmt.Errorf("list collections for bucket %s: %w", bucket.ID, err)
		}
		for _, coll := range collections {
			if coll.Tier != "performance" {
				continue
			}
			usedMB += EstimatePerformanceMemMB(coll.MaxVectors, coll.Dim)
		}
	}

	neededMB := EstimatePerformanceMemMB(maxVectors, dim)
	if usedMB+neededMB > float64(c.cfg.PerformanceBudgetMB) {
		return fmt.Errorf("performance memory budget exceeded (used=%.2fMiB needed=%.2fMiB budget=%dMiB)", usedMB, neededMB, c.cfg.PerformanceBudgetMB)
	}
	return nil
}

func (c *Checker) CheckPerformanceVectorLimit(currentCount int64, insertCount int, maxVectors int64) error {
	if maxVectors <= 0 {
		return nil
	}
	if currentCount+int64(insertCount) > maxVectors {
		return fmt.Errorf("insert would exceed max vectors for performance collection (%d)", maxVectors)
	}
	return nil
}

func (c *Checker) CheckMetric(metric string) error {
	switch metric {
	case "COSINE", "L2":
		return nil
	default:
		return fmt.Errorf("unsupported metric type %q, must be COSINE or L2", metric)
	}
}
