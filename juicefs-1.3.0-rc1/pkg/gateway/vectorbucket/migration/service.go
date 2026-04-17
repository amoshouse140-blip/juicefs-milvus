package migration

import (
	"context"
	"fmt"

	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/adapter"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/config"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/controller"
	"github.com/juicedata/juicefs/pkg/gateway/vectorbucket/metadata"
	"github.com/juicedata/juicefs/pkg/utils"
)

var migrationLogger = utils.GetLogger("vectorbucket-migration")

type Service struct {
	store      metadata.Store
	adapter    adapter.Adapter
	controller *controller.LoadController
	cfg        config.Config
}

func NewService(store metadata.Store, adp adapter.Adapter, ctrl *controller.LoadController, cfg config.Config) *Service {
	return &Service{store: store, adapter: adp, controller: ctrl, cfg: cfg}
}

func (s *Service) RunOnce(ctx context.Context) error {
	colls, err := s.store.ListCollectionsInMigration(ctx)
	if err != nil {
		migrationLogger.Errorf("list collections in migration failed: %v", err)
		return err
	}
	if len(colls) > 0 {
		migrationLogger.Infof("migration scan found %d collection(s) in migration", len(colls))
	}
	for _, coll := range colls {
		if coll.MigrateState != "UPGRADING" {
			continue
		}
		migrationLogger.Infof("migration worker picked collection: id=%s physical=%s target_physical=%s from=%s to=%s", coll.ID, coll.PhysicalName, coll.TargetPhysicalName, coll.IndexType, coll.TargetIndexType)
		if err := s.migrateCollection(ctx, coll); err != nil {
			migrationLogger.Errorf("migration failed: id=%s source=%s target=%s err=%v", coll.ID, coll.SourcePhysicalName, coll.TargetPhysicalName, err)
			_ = s.store.UpdateCollectionMigrationState(ctx, coll.ID, "", "", "", "", coll.MaintenanceSince, err.Error())
			return err
		}
	}
	return nil
}

func (s *Service) migrateCollection(ctx context.Context, coll *metadata.LogicalCollection) error {
	targetTier := s.cfg.TierForIndexType(coll.TargetIndexType)
	targetPinned := s.cfg.IsPinnedIndexType(coll.TargetIndexType)
	targetMaxVectors := coll.MaxVectors
	if targetPinned && targetMaxVectors <= 0 {
		targetMaxVectors = s.cfg.PerformanceMaxVectors
	}
	if !targetPinned {
		targetMaxVectors = 0
	}
	sourceEstMem := coll.EstMemMB
	if sourceEstMem <= 0 {
		sourceMaxVectors := coll.MaxVectors
		if sourceMaxVectors <= 0 {
			sourceMaxVectors = coll.VectorCount
		}
		sourceEstMem = controller.EstimateMemMB(sourceMaxVectors, coll.Dim)
	}
	if err := s.controller.EnsureLoaded(ctx, coll.SourcePhysicalName, sourceEstMem); err != nil {
		return fmt.Errorf("load source collection: %w", err)
	}
	migrationLogger.Infof("migration source loaded: id=%s source=%s est_mem_mb=%.2f", coll.ID, coll.SourcePhysicalName, sourceEstMem)

	if err := s.adapter.CreateCollection(ctx, coll.TargetPhysicalName, coll.Dim, coll.Metric); err != nil {
		return fmt.Errorf("create target collection: %w", err)
	}
	migrationLogger.Infof("migration target collection created: id=%s target=%s model=%s", coll.ID, coll.TargetPhysicalName, coll.TargetIndexType)
	indexSpec := adapter.IndexSpec{
		IndexType:   coll.TargetIndexType,
		Tier:        targetTier,
		VectorCount: coll.VectorCount,
		Metric:      coll.Metric,
		HNSWM:       s.cfg.HNSWM,
		HNSWEFConstruction: s.cfg.HNSWEFConstruction,
	}
	if err := s.adapter.CreateIndex(ctx, coll.TargetPhysicalName, indexSpec); err != nil {
		return fmt.Errorf("create target index: %w", err)
	}
	migrationLogger.Infof("migration target index created: id=%s target=%s model=%s", coll.ID, coll.TargetPhysicalName, coll.TargetIndexType)

	var copiedRows int
	if err := s.adapter.Scan(ctx, coll.SourcePhysicalName, 1000, func(rows []adapter.VectorRecord) error {
		copiedRows += len(rows)
		ids := make([]string, 0, len(rows))
		vectors := make([][]float32, 0, len(rows))
		metadataJSON := make([][]byte, 0, len(rows))
		timestamps := make([]int64, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
			vectors = append(vectors, row.Vector)
			metadataJSON = append(metadataJSON, row.Metadata)
			timestamps = append(timestamps, row.CreatedAt)
		}
		return s.adapter.Upsert(ctx, coll.TargetPhysicalName, ids, vectors, metadataJSON, timestamps)
	}); err != nil {
		return fmt.Errorf("scan source collection: %w", err)
	}
	migrationLogger.Infof("migration data copied: id=%s source=%s target=%s copied_rows=%d", coll.ID, coll.SourcePhysicalName, coll.TargetPhysicalName, copiedRows)

	sourceCount, err := s.adapter.Count(ctx, coll.SourcePhysicalName)
	if err != nil {
		return fmt.Errorf("count source collection: %w", err)
	}
	targetCount, err := s.adapter.Count(ctx, coll.TargetPhysicalName)
	if err != nil {
		return fmt.Errorf("count target collection: %w", err)
	}
	if sourceCount != targetCount {
		return fmt.Errorf("migrate count mismatch: source=%d target=%d", sourceCount, targetCount)
	}
	migrationLogger.Infof("migration count verified: id=%s source=%d target=%d", coll.ID, sourceCount, targetCount)

	if targetPinned {
		estMem := controller.EstimateMemMB(targetMaxVectors, coll.Dim)
		if err := s.controller.EnsureLoaded(ctx, coll.TargetPhysicalName, estMem); err != nil {
			return fmt.Errorf("load target collection: %w", err)
		}
		s.controller.Pin(coll.TargetPhysicalName)
		migrationLogger.Infof("migration target pinned: id=%s target=%s est_mem_mb=%.2f", coll.ID, coll.TargetPhysicalName, estMem)
	}
	if coll.Pinned {
		s.controller.Unpin(coll.SourcePhysicalName)
		_ = s.controller.Release(ctx, coll.SourcePhysicalName)
		migrationLogger.Infof("migration source unpinned and released: id=%s source=%s", coll.ID, coll.SourcePhysicalName)
	}

	if err := s.store.SwitchCollectionPhysical(ctx, coll.ID, coll.PhysicalName, coll.TargetPhysicalName, coll.TargetIndexType, targetTier, targetPinned, targetMaxVectors); err != nil {
		return fmt.Errorf("switch collection physical mapping: %w", err)
	}
	migrationLogger.Infof("migration metadata switched: id=%s source=%s target=%s model=%s tier=%s", coll.ID, coll.PhysicalName, coll.TargetPhysicalName, coll.TargetIndexType, targetTier)
	if err := s.store.UpdateCollectionIndexBuilt(ctx, coll.ID, true); err != nil {
		return fmt.Errorf("mark target index built: %w", err)
	}
	if err := s.adapter.DropCollection(ctx, coll.SourcePhysicalName); err != nil {
		return fmt.Errorf("drop source collection: %w", err)
	}
	migrationLogger.Infof("migration completed: id=%s dropped_source=%s current_target=%s", coll.ID, coll.SourcePhysicalName, coll.TargetPhysicalName)
	return nil
}
