package vectorbucket

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/adapter"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/config"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/controller"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metadata"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/migration"
)

type BootstrapResult struct {
	Runtime *Runtime
	Stop    func(context.Context) error
}

func Bootstrap(ctx context.Context, cfg config.Config) (*BootstrapResult, error) {
	if dir := filepath.Dir(cfg.SQLitePath); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	store := metadata.NewSQLiteStore(cfg.SQLitePath)
	if err := store.Init(ctx); err != nil {
		return nil, err
	}

	milvusAdapter, err := adapter.NewAdapterFromConfig(cfg)
	if err != nil {
		_ = store.Close()
		return nil, err
	}

	ctrl := controller.NewLoadController(
		milvusAdapter,
		cfg.LoadBudgetMB,
		time.Duration(cfg.TTLSeconds)*time.Second,
		cfg.MaxLoadedColls,
	)
	if err := restorePinnedCollections(ctx, store, ctrl); err != nil {
		_ = milvusAdapter.Close()
		_ = store.Close()
		return nil, err
	}
	ctrl.StartTTLSweepLoop(ctx, time.Minute)
	migrator := migration.NewService(store, milvusAdapter, ctrl, cfg)
	go startMigrationLoop(ctx, migrator, time.Minute)

	runtime := NewRuntime(cfg, store, milvusAdapter, ctrl)
	return &BootstrapResult{
		Runtime: runtime,
		Stop: func(stopCtx context.Context) error {
			_ = milvusAdapter.Close()
			return runtime.Close(stopCtx)
		},
	}, nil
}

func restorePinnedCollections(ctx context.Context, store metadata.Store, ctrl *controller.LoadController) error {
	buckets, err := store.ListBuckets(ctx)
	if err != nil {
		return err
	}
	for _, bucket := range buckets {
		collections, err := store.ListCollections(ctx, bucket.ID)
		if err != nil {
			return err
		}
		for _, coll := range collections {
			if !coll.Pinned || coll.Status != metadata.CollStatusReady {
				continue
			}
			estMem := coll.EstMemMB
			if estMem <= 0 {
				estMem = controller.EstimateMemMB(coll.MaxVectors, coll.Dim)
			}
			if err := ctrl.EnsureLoaded(ctx, coll.PhysicalName, estMem); err != nil {
				return err
			}
			ctrl.Pin(coll.PhysicalName)
		}
	}
	return nil
}

func startMigrationLoop(ctx context.Context, svc *migration.Service, interval time.Duration) {
	_ = svc.RunOnce(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = svc.RunOnce(ctx)
		}
	}
}
