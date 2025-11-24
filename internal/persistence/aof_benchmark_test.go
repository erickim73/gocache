package persistence

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/erickim73/gocache/internal/cache"
)

func setupBenchmark(b *testing.B, policy SyncPolicy) (*cache.Cache, *AOF) {
	// create cache
	c, _ := cache.New(100000)

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
	c, _ := cache.New(100000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key%d", i)
		c.Set(key, "value", 0)
	}
}

