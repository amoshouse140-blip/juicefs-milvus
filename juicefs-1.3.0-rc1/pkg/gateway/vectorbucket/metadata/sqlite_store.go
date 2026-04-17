package metadata

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	dbPath string
	db     *sql.DB
}

func NewSQLiteStore(dbPath string) *SQLiteStore {
	return &SQLiteStore{dbPath: dbPath}
}

func (s *SQLiteStore) Init(ctx context.Context) error {
	db, err := sql.Open("sqlite", s.dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	s.db = db

	schema := `
	CREATE TABLE IF NOT EXISTS buckets (
		id         TEXT PRIMARY KEY,
		name       TEXT NOT NULL UNIQUE,
		owner      TEXT NOT NULL,
		status     TEXT NOT NULL DEFAULT 'READY',
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS collections (
		id             TEXT PRIMARY KEY,
		bucket_id      TEXT NOT NULL REFERENCES buckets(id) ON DELETE CASCADE,
		name           TEXT NOT NULL,
		dim            INTEGER NOT NULL,
		metric         TEXT NOT NULL,
		index_type     TEXT NOT NULL DEFAULT 'ivf_sq8',
		tier           TEXT NOT NULL DEFAULT 'standard',
		max_vectors    INTEGER NOT NULL DEFAULT 0,
		pinned         BOOLEAN NOT NULL DEFAULT FALSE,
		status         TEXT NOT NULL DEFAULT 'INIT',
		physical_name  TEXT NOT NULL,
		index_built    BOOLEAN NOT NULL DEFAULT FALSE,
		vector_count   INTEGER NOT NULL DEFAULT 0,
		est_mem_mb     REAL NOT NULL DEFAULT 0,
		last_access_at DATETIME,
		migrate_state TEXT NOT NULL DEFAULT '',
		target_index_type TEXT NOT NULL DEFAULT '',
		source_physical_name TEXT NOT NULL DEFAULT '',
		target_physical_name TEXT NOT NULL DEFAULT '',
		maintenance_since DATETIME,
		last_migrate_error TEXT NOT NULL DEFAULT '',
		created_at     DATETIME NOT NULL,
		updated_at     DATETIME NOT NULL,
		UNIQUE(bucket_id, name)
	);
	`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		_ = s.db.Close()
		s.db = nil
		return fmt.Errorf("create schema: %w", err)
	}
	migrations := []string{
		"ALTER TABLE collections ADD COLUMN index_type TEXT NOT NULL DEFAULT 'ivf_sq8'",
		"ALTER TABLE collections ADD COLUMN tier TEXT NOT NULL DEFAULT 'standard'",
		"ALTER TABLE collections ADD COLUMN max_vectors INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE collections ADD COLUMN pinned BOOLEAN NOT NULL DEFAULT FALSE",
		"ALTER TABLE collections ADD COLUMN migrate_state TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE collections ADD COLUMN target_index_type TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE collections ADD COLUMN source_physical_name TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE collections ADD COLUMN target_physical_name TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE collections ADD COLUMN maintenance_since DATETIME",
		"ALTER TABLE collections ADD COLUMN last_migrate_error TEXT NOT NULL DEFAULT ''",
	}
	for _, stmt := range migrations {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			_ = s.db.Close()
			s.db = nil
			return fmt.Errorf("migrate schema: %w", err)
		}
	}
	return nil
}

func (s *SQLiteStore) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *SQLiteStore) CreateBucket(ctx context.Context, b *Bucket) error {
	now := time.Now().UTC()
	createdAt := b.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := b.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = now
	}
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO buckets (id, name, owner, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		b.ID, b.Name, b.Owner, b.Status, createdAt, updatedAt,
	)
	return err
}

func (s *SQLiteStore) GetBucket(ctx context.Context, id string) (*Bucket, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT id, name, owner, status, created_at, updated_at FROM buckets WHERE id = ?",
		id,
	)
	return scanBucket(row)
}

func (s *SQLiteStore) GetBucketByName(ctx context.Context, name string) (*Bucket, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT id, name, owner, status, created_at, updated_at FROM buckets WHERE name = ?",
		name,
	)
	return scanBucket(row)
}

func (s *SQLiteStore) ListBuckets(ctx context.Context) ([]*Bucket, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, name, owner, status, created_at, updated_at FROM buckets WHERE status != 'DELETED' ORDER BY created_at, id",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Bucket
	for rows.Next() {
		b := &Bucket{}
		if err := rows.Scan(&b.ID, &b.Name, &b.Owner, &b.Status, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) UpdateBucketStatus(ctx context.Context, id string, status BucketStatus) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE buckets SET status = ?, updated_at = ? WHERE id = ?",
		status, time.Now().UTC(), id,
	)
	return err
}

func (s *SQLiteStore) DeleteBucket(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM buckets WHERE id = ?", id)
	return err
}

func (s *SQLiteStore) CountBuckets(ctx context.Context) (int, error) {
	var cnt int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM buckets WHERE status != 'DELETED'").Scan(&cnt)
	return cnt, err
}

func (s *SQLiteStore) CountBucketsByOwner(ctx context.Context, owner string) (int, error) {
	var cnt int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM buckets WHERE owner = ? AND status != 'DELETED'",
		owner,
	).Scan(&cnt)
	return cnt, err
}

func (s *SQLiteStore) CreateCollection(ctx context.Context, c *LogicalCollection) error {
	now := time.Now().UTC()
	createdAt := c.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := c.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = now
	}

	var lastAccess any
	if !c.LastAccessAt.IsZero() {
		lastAccess = c.LastAccessAt
	}
	var maintenanceSince any
	if !c.MaintenanceSince.IsZero() {
		maintenanceSince = c.MaintenanceSince
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO collections
			(id, bucket_id, name, dim, metric, index_type, tier, max_vectors, pinned, status, physical_name, index_built, vector_count, est_mem_mb, last_access_at, migrate_state, target_index_type, source_physical_name, target_physical_name, maintenance_since, last_migrate_error, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.BucketID, c.Name, c.Dim, c.Metric, c.IndexType, c.Tier, c.MaxVectors, c.Pinned, c.Status, c.PhysicalName,
		c.IndexBuilt, c.VectorCount, c.EstMemMB, lastAccess, c.MigrateState, c.TargetIndexType, c.SourcePhysicalName, c.TargetPhysicalName, maintenanceSince, c.LastMigrateError, createdAt, updatedAt,
	)
	return err
}

func (s *SQLiteStore) GetCollection(ctx context.Context, bucketID, name string) (*LogicalCollection, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, bucket_id, name, dim, metric, index_type, tier, max_vectors, pinned, status, physical_name, index_built, vector_count, est_mem_mb, last_access_at, migrate_state, target_index_type, source_physical_name, target_physical_name, maintenance_since, last_migrate_error, created_at, updated_at
		 FROM collections
		 WHERE bucket_id = ? AND name = ? AND status != 'DELETED'`,
		bucketID, name,
	)
	return scanCollection(row)
}

func (s *SQLiteStore) GetCollectionByID(ctx context.Context, id string) (*LogicalCollection, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, bucket_id, name, dim, metric, index_type, tier, max_vectors, pinned, status, physical_name, index_built, vector_count, est_mem_mb, last_access_at, migrate_state, target_index_type, source_physical_name, target_physical_name, maintenance_since, last_migrate_error, created_at, updated_at
		 FROM collections
		 WHERE id = ?`,
		id,
	)
	return scanCollection(row)
}

func (s *SQLiteStore) ListCollections(ctx context.Context, bucketID string) ([]*LogicalCollection, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, bucket_id, name, dim, metric, index_type, tier, max_vectors, pinned, status, physical_name, index_built, vector_count, est_mem_mb, last_access_at, migrate_state, target_index_type, source_physical_name, target_physical_name, maintenance_since, last_migrate_error, created_at, updated_at
		 FROM collections
		 WHERE bucket_id = ? AND status != 'DELETED'
		 ORDER BY created_at, id`,
		bucketID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*LogicalCollection
	for rows.Next() {
		c, err := scanCollectionRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) UpdateCollectionStatus(ctx context.Context, id string, status CollectionStatus) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE collections SET status = ?, updated_at = ? WHERE id = ?",
		status, time.Now().UTC(), id,
	)
	return err
}

func (s *SQLiteStore) UpdateCollectionIndexBuilt(ctx context.Context, id string, built bool) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE collections SET index_built = ?, updated_at = ? WHERE id = ?",
		built, time.Now().UTC(), id,
	)
	return err
}

func (s *SQLiteStore) UpdateCollectionVectorCount(ctx context.Context, id string, delta int64) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE collections SET vector_count = vector_count + ?, updated_at = ? WHERE id = ?",
		delta, time.Now().UTC(), id,
	)
	return err
}

func (s *SQLiteStore) UpdateCollectionLastAccess(ctx context.Context, id string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		"UPDATE collections SET last_access_at = ?, updated_at = ? WHERE id = ?",
		now, now, id,
	)
	return err
}

func (s *SQLiteStore) UpdateCollectionMigrationState(ctx context.Context, id string, state string, targetIndexType string, sourcePhysical string, targetPhysical string, maintenanceSince time.Time, lastErr string) error {
	now := time.Now().UTC()
	var maintenance any
	if !maintenanceSince.IsZero() {
		maintenance = maintenanceSince
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE collections
		 SET migrate_state = ?, target_index_type = ?, source_physical_name = ?, target_physical_name = ?, maintenance_since = ?, last_migrate_error = ?, updated_at = ?
		 WHERE id = ?`,
		state, targetIndexType, sourcePhysical, targetPhysical, maintenance, lastErr, now, id,
	)
	return err
}

func (s *SQLiteStore) SwitchCollectionPhysical(ctx context.Context, id string, oldPhysical string, newPhysical string, newIndexType string, newTier string, newPinned bool, newMaxVectors int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE collections
		 SET physical_name = ?, index_type = ?, tier = ?, pinned = ?, max_vectors = ?,
		     migrate_state = '', target_index_type = '', source_physical_name = '', target_physical_name = '',
		     maintenance_since = NULL, last_migrate_error = '', updated_at = ?
		 WHERE id = ? AND physical_name = ?`,
		newPhysical, newIndexType, newTier, newPinned, newMaxVectors, time.Now().UTC(), id, oldPhysical,
	)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLiteStore) ListCollectionsInMigration(ctx context.Context) ([]*LogicalCollection, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, bucket_id, name, dim, metric, index_type, tier, max_vectors, pinned, status, physical_name, index_built, vector_count, est_mem_mb, last_access_at, migrate_state, target_index_type, source_physical_name, target_physical_name, maintenance_since, last_migrate_error, created_at, updated_at
		 FROM collections
		 WHERE migrate_state != ''
		 ORDER BY updated_at, id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*LogicalCollection
	for rows.Next() {
		c, err := scanCollectionRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) DeleteCollection(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM collections WHERE id = ?", id)
	return err
}

func (s *SQLiteStore) CountCollections(ctx context.Context, bucketID string) (int, error) {
	var cnt int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM collections WHERE bucket_id = ? AND status != 'DELETED'",
		bucketID,
	).Scan(&cnt)
	return cnt, err
}

func scanBucket(row *sql.Row) (*Bucket, error) {
	b := &Bucket{}
	if err := row.Scan(&b.ID, &b.Name, &b.Owner, &b.Status, &b.CreatedAt, &b.UpdatedAt); err != nil {
		return nil, err
	}
	return b, nil
}

func scanCollection(row *sql.Row) (*LogicalCollection, error) {
	return scanCollectionRow(row)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanCollectionRow(row rowScanner) (*LogicalCollection, error) {
	c := &LogicalCollection{}
	var lastAccess sql.NullTime
	var maintenanceSince sql.NullTime
	if err := row.Scan(
		&c.ID,
		&c.BucketID,
		&c.Name,
		&c.Dim,
		&c.Metric,
		&c.IndexType,
		&c.Tier,
		&c.MaxVectors,
		&c.Pinned,
		&c.Status,
		&c.PhysicalName,
		&c.IndexBuilt,
		&c.VectorCount,
		&c.EstMemMB,
		&lastAccess,
		&c.MigrateState,
		&c.TargetIndexType,
		&c.SourcePhysicalName,
		&c.TargetPhysicalName,
		&maintenanceSince,
		&c.LastMigrateError,
		&c.CreatedAt,
		&c.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if lastAccess.Valid {
		c.LastAccessAt = lastAccess.Time
	}
	if maintenanceSince.Valid {
		c.MaintenanceSince = maintenanceSince.Time
	}
	return c, nil
}
