package cache

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/erickim73/gocache/internal/metrics"
)

var (
	benchMetricsCollector *metrics.Collector
	benchMetricsOnce      sync.Once
)

func getBenchMetricsCollector() *metrics.Collector {
	benchMetricsOnce.Do(func() {
		benchMetricsCollector = metrics.NewCollector()
	})
	return benchMetricsCollector
}

// measures single-threaded SET performance
func BenchmarkCacheSet(b *testing.B) {
	// create cache with reasonable size
	metricsCollector := getBenchMetricsCollector()
	cache, err := NewCache(100000, metricsCollector)
	if err != nil {
		b.Fatalf("error creating new cache: %v", err)
	}

	// reset timer to exclude setup time
	b.ResetTimer()

	// b.N is automatically determined by the testing framework
	for i := 0; i < b.N; i++ {
		// use different keys to avoid cache hits
		key := fmt.Sprintf("key:%d", i%1000)
		value := fmt.Sprintf("value:%d", i)

		cache.Set(key, value, 0) // 0 = no expiration
	}
}

// measures single-threaded GET performance
func BenchmarkCacheGet(b *testing.B) {
	metricsCollector := getBenchMetricsCollector()
	cache, err := NewCache(100000, metricsCollector)
	if err != nil {
		b.Fatalf("error creating new cache: %v", err)
	}

	// pre-populate cache with data
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("key:%d", i)
		value := fmt.Sprintf("value:%d", i)
		cache.Set(key, value, 0)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// access keys that exist (cache hits)
		key := fmt.Sprintf("key:%d", i%1000)
		cache.Get(key)
	}
}

// measures realistic workload (70% reads, 30% writes)
func BenchmarkCacheMixed(b *testing.B) {
	metricsCollector := getBenchMetricsCollector()
	cache, err := NewCache(100000, metricsCollector)
	if err != nil {
		b.Fatalf("error creating new cache: %v", err)
	}

	// pre-populate
	for i := 0; i < 1000; i++ {
		cache.Set(fmt.Sprintf("key:%d", i), fmt.Sprintf("value:%d", i), 0)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key:%d", i%1000)

		// 70% reads, 30% writes (realistic ratio)
		if i%10 < 7 {
			cache.Get(key)
		} else {
			cache.Set(key, fmt.Sprintf("value:%d", i), 0)
		}
	}
}

// measures multi-threaded SET performance
func BenchmarkCacheSetParallel(b *testing.B) {
	metricsCollector := getBenchMetricsCollector()
	cache, err := NewCache(100000, metricsCollector)
	if err != nil {
		b.Fatalf("error creating new cache: %v", err)
	}

	b.ResetTimer()

	// runs the benchmark function in parallel
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		// pb.Next() returns true until time is up
		for pb.Next() {
			key := fmt.Sprintf("key:%d", i%10000)
			value := fmt.Sprintf("value:%d", i)
			cache.Set(key, value, 0)
			i++
		}
	})
}

// measures multi-threaded GET performance
func BenchmarkCacheGetParallel(b *testing.B) {
	metricsCollector := getBenchMetricsCollector()
	cache, err := NewCache(100000, metricsCollector)
	if err != nil {
		b.Fatalf("error creating new cache: %v", err)
	}

	// pre-populate with data
	for i := 0; i < 10000; i++ {
		cache.Set(fmt.Sprintf("key:%d", i), fmt.Sprintf("value:%d", i), 0)
	}

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("key:%d", i%10000)
			cache.Get(key)
			i++
		}
	})
}

// measures realistic concurrent workload
func BenchmarkCacheMixedParallel(b *testing.B) {
	metricsCollector := getBenchMetricsCollector()
	cache, err := NewCache(100000, metricsCollector)
	if err != nil {
		b.Fatalf("error creating new cache: %v", err)
	}

	// pre-populate
	for i := 0; i < 10000; i++ {
		cache.Set(fmt.Sprintf("key:%d", i), fmt.Sprintf("value:%d", i), 0)
	}

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("key:%d", i%10000)

			// 70% reads, 30% writes
			if i%10 < 7 {
				cache.Get(key)
			} else {
				cache.Set(key, fmt.Sprintf("value:%d", i), 0)
			}
			i++
		}
	})
}

// measures performance with expiration
func BenchmarkCacheWithTTL(b *testing.B) {
	metricsCollector := getBenchMetricsCollector()
	cache, err := NewCache(100000, metricsCollector)
	if err != nil {
		b.Fatalf("error creating new cache: %v", err)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key:%d", i%1000)
		value := fmt.Sprintf("value:%d", i)
		// set with 1-hour expiration
		cache.Set(key, value, time.Hour)
	}
}

// measures LRU eviction performance
func BenchmarkCacheEviction(b *testing.B) {
	// small cache to force evictions
	metricsCollector := getBenchMetricsCollector()
	cache, err := NewCache(1000, metricsCollector)
	if err != nil {
		b.Fatalf("error creating new cache: %v", err)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// always use new keys to trigger evictions
		key := fmt.Sprintf("key:%d", i)
		value := fmt.Sprintf("value:%d", i)
		cache.Set(key, value, 0)
	}
}

// specifically measures allocation overhead
func BenchmarkMemoryAllocations(b *testing.B) {
	metricsCollector := getBenchMetricsCollector()
	cache, err := NewCache(100000, metricsCollector)
	if err != nil {
		b.Fatalf("error creating new cache: %v", err)
	}

	// report allocations
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key:%d", i%1000)
		value := fmt.Sprintf("value:%d", i)
		cache.Set(key, value, 0)
	}
}

// simulates worst-case lock contention
func BenchmarkHighContention(b *testing.B) {
	metricsCollector := getBenchMetricsCollector()
	cache, err := NewCache(100000, metricsCollector)
	if err != nil {
		b.Fatalf("error creating new cache: %v", err)
	}

	// pre-populate with just 10 keys (high contention)
	for i := 0; i < 10; i++ {
		cache.Set(fmt.Sprintf("key:%d", i), fmt.Sprintf("value:%d", i), 0)
	}

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			// everyone fights over the same 10 keys
			key := fmt.Sprintf("key:%d", i%10)

			if i%2 == 0 {
				cache.Get(key)
			} else {
				cache.Set(key, fmt.Sprintf("value:%d", i), 0)
			}
			i++
		}
	})
}

// simulates best-case scenario
func BenchmarkLowContention(b *testing.B) {
	metricsCollector := getBenchMetricsCollector()
	cache, err := NewCache(100000, metricsCollector)
	if err != nil {
		b.Fatalf("error creating new cache: %v", err)
	}

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		// each goroutine gets its own key space using goroutine ID simulation
		offset := rand.Intn(1000000)
		i := 0
		for pb.Next() {
			// spread keys across wide range
			key := fmt.Sprintf("key:%d", offset+i)
			cache.Set(key, fmt.Sprintf("value:%d", i), 0)
			i++
		}
	})
}
