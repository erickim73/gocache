package persistence

import (
	"fmt"
	"testing"
	"time"

	"github.com/erickim73/gocache/internal/cache"
)

func setupBenchmark(b *testing.B, policy SyncPolicy) (*cache.Cache, *AOF) {
	// create cache
	c, _ := cache.New(100000)

	// use unique filename for each test
	filename := fmt.Sprintf("bench_test_%d.aof", time.Now().UnixNano())
	snapshotName := fmt.Sprintf("bench_test_%d.rdb", time.Now().UnixNano())

	aof, err := NewAOF(filename, snapshotName, policy, c, 100)
	if err != nil {
		b.Fatalf("Failed to create AOF: %v", err)
	}

	return c, aof
}

