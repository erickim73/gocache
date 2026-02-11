package metrics

import (
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus"
)

// collector holds all prometheus metrics for gocache
type Collector struct {
	OperationsTotal *prometheus.CounterVec // tracks total number of operations by type
	CacheHits prometheus.Counter // tracks successful cache lookups
	CacheMisses prometheus.Counter // tracks failed cache lookups
	OperationDuration prometheus.Histogram // tracks latency distribution of operations
	MemoryBytes prometheus.Gauge // tracks current memory usage of cache
	ItemsCount prometheus.Gauge // tracks current number of items in cache
	EvictionsTotal prometheus.Counter // tracks how many items were evicted by lru
	ExpirationsTotal prometheus.Counter // tracks how many items expired due to ttl
	ActiveConnections prometheus.Gauge // tracks currently connected clients
	ReplicationLag prometheus.Gauge // tracks how far behind followers are from leader
	ConnectedFollowers prometheus.Gauge // tracks number of followers connected to leader
}

// creates and registers all metrics with prometheus
func NewCollector() *Collector {
	collector := &Collector{
		// counter with labels. allows tracking different operation types
		OperationsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gocache_operations_total",
				Help: "Total number of cache operations by type",
			},
			[]string{"operation"}, // label: operation can be "get", "set", or "delete"
		),

		// simple counter, no labels needed
		CacheHits: promauto.NewCounter(
			prometheus.CounterOpts{
				Name: "gocache_cache_hits_total",
				Help: "Total number of cache hits (successful lookups)",
			},
		),

		CacheMisses: promauto.NewCounter(
			prometheus.CounterOpts{
				Name: "gocache_cache_misses_total",
				Help: "Total number of cache misses (key not found)",
			},
		),

		// histogram with predefined buckets for latency measurement
		// buckets are in seconds: 0.1ms, 1ms, 5ms, 10ms, 50ms, 100ms
		OperationDuration: promauto.NewHistogram(
			prometheus.HistogramOpts{
				Name: "gocache_operation_duration_seconds",
				Help: "Duration of cache operations in seconds",
				Buckets: []float64{0.0001, 0.001, 0.005, 0.01, 0.05, 0.1},
			},
		),

		// gauges for current state (can increase or decrease)
		MemoryBytes: promauto.NewGauge(
			prometheus.GaugeOpts{
				Name: "gocache_memory_bytes",
				Help: "Current memory usage of the cache in bytes",
			},
		),

		ItemsCount: promauto.NewGauge(
			prometheus.GaugeOpts{
				Name: "gocache_items_count",
				Help: "Current number of items stored in cache",
			},
		),

		// counters for events
		EvictionsTotal: promauto.NewCounter(
			prometheus.CounterOpts{
				Name: "gocache_evictions_total",
				Help: "Total number of items evicted due to LRU policy",
			},
		),

		ExpirationsTotal: promauto.NewCounter(
			prometheus.CounterOpts{
				Name: "gocache_expirations_total",
				Help: "Total number of items expired due to TTL",
			},
		),

		// connection tracking
		ActiveConnections: promauto.NewGauge(
			prometheus.GaugeOpts{
				Name: "gocache_active_connections",
				Help: "Number of currently active client connections",
			},
		),

		// replication metrics
		ReplicationLag: promauto.NewGauge(
			prometheus.GaugeOpts{
				Name: "gocache_replication_lag_seconds",
				Help: "Replication lag between leader and followers in seconds",
			},
		),

		ConnectedFollowers: promauto.NewGauge(
			prometheus.GaugeOpts{
				Name: "gocache_connected_followers",
				Help: "Number of followers currently connected to this leader",
			},
		),
	}

	return collector
}