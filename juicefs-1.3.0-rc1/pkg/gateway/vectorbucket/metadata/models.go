package metadata

import "time"

type BucketStatus string

const (
	BucketStatusReady    BucketStatus = "READY"
	BucketStatusDeleting BucketStatus = "DELETING"
	BucketStatusDeleted  BucketStatus = "DELETED"
)

type CollectionStatus string

const (
	CollStatusInit     CollectionStatus = "INIT"
	CollStatusReady    CollectionStatus = "READY"
	CollStatusDeleting CollectionStatus = "DELETING"
	CollStatusDeleted  CollectionStatus = "DELETED"
)

type Bucket struct {
	ID        string
	Name      string
	Owner     string
	Status    BucketStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}

type LogicalCollection struct {
	ID           string
	BucketID     string
	Name         string
	Dim          int
	Metric       string
	Status       CollectionStatus
	PhysicalName string
	IndexBuilt   bool
	VectorCount  int64
	EstMemMB     float64
	LastAccessAt time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
