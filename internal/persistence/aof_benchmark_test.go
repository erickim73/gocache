package persistence

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/erickim73/gocache/internal/cache"
	"github.com/erickim73/gocache/internal/metrics"
)

func setupBenchmark(b *testing.B, policy SyncPolicy) (*cache.Cache, *AOF) {
	// create cache
	metricsCollector := metrics.NewCollector()
	c, _ := cache.NewCache(100000, metricsCollector)

	// use unique filename for each test
	fileName := fmt.Sprintf("bench_test_%d.aof", time.Now().UnixNano())
	snapshotName := fmt.Sprintf("bench_test_%d.rdb", time.Now().UnixNano())

	aof, err := NewAOF(fileName, snapshotName, policy, c, 100)
	if err != nil {
		b.Fatalf("Failed to create AOF: %v", err)
	}

	return c, aof
}

func cleanupBenchmark(aof *AOF) {
	fileName := aof.fileName
	snapshotName := aof.snapshotName
	aof.Close()
	os.Remove(fileName)
	os.Remove(snapshotName)
}

func BenchmarkSetNoPersistence(b *testing.B) {
	metricsCollector := metrics.NewCollector()
	c, _ := cache.NewCache(100000, metricsCollector)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key%d", i)
		c.Set(key, "value", 0)
	}
}

func BenchmarkSetSyncNo(b *testing.B) {
	c, aof := setupBenchmark(b, SyncNo)
	defer cleanupBenchmark(aof)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key%d", i)
		c.Set(key, "value", 0)
		aof.Append(fmt.Sprintf("Set %s value\r\n", key))
	}
}

func BenchmarkSetSyncEverySecond(b *testing.B) {
	c, aof := setupBenchmark(b, SyncEverySecond)
	defer cleanupBenchmark(aof)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key%d", i)
		c.Set(key, "value", 0)
		aof.Append(fmt.Sprintf("Set %s value\r\n", key))
	}
}

func BenchmarkSetSyncAlways(b *testing.B) {
	c, aof := setupBenchmark(b, SyncAlways)
	defer cleanupBenchmark(aof)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key%d", i)
		c.Set(key, "value", 0)
		aof.Append(fmt.Sprintf("Set %s value\r\n", key))
	}
}