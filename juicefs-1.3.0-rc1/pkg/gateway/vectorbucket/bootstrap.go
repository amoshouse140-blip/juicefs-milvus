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
	ctrl.StartTTLSweepLoop(ctx, time.Minute)

	runtime := NewRuntime(cfg, store, milvusAdapter, ctrl)
	return &BootstrapResult{
		Runtime: runtime,
		Stop: func(stopCtx context.Context) error {
			_ = milvusAdapter.Close()
			return runtime.Close(stopCtx)
		},
	}, nil
}
