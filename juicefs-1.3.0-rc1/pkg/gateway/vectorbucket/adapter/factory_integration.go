//go:build milvus_integration

package adapter

import "github.com/juicedata/juicefs/pkg/gateway/vectorbucket/config"

func NewAdapterFromConfig(cfg config.Config) (Adapter, error) {
	return NewMilvusAdapter(cfg.MilvusAddr)
}
