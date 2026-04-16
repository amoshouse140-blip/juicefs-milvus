package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	BucketCount = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "vb_bucket_count",
		Help: "Active vector bucket count.",
	})

	LogicalCollectionCount = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "vb_logical_collection_count",
		Help: "Logical collection count grouped by status.",
	}, []string{"status"})

	LoadedCollectionCount = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "vb_loaded_collection_count",
		Help: "Currently loaded collection count.",
	})

	LoadDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "vb_load_duration_seconds",
		Help:    "LoadCollection latency in seconds.",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 10),
	})

	QueryDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "vb_query_duration_seconds",
		Help:    "Query latency by phase.",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 15),
	}, []string{"phase"})

	ReleaseEvictions = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "vb_release_evictions_total",
		Help: "Collection release count by reason.",
	}, []string{"reason"})

	CollectionMemEstimate = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "vb_collection_mem_estimate_mb",
		Help: "Estimated collection memory usage in MB.",
	}, []string{"collection"})

	InsertTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "vb_insert_total",
		Help: "Inserted vector count.",
	}, []string{"bucket", "collection"})

	QueryTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "vb_query_total",
		Help: "Query request count.",
	}, []string{"bucket", "collection"})

	ErrorTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "vb_error_total",
		Help: "Error count by type.",
	}, []string{"type"})
)
