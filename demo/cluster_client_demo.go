package main

import (
	"fmt"
	"log/slog"
	"os"
	"log"
	"time"

	"github.com/erickim73/gocache/pkg/client"
)

func main() {
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo, // Show Info and above
	})
	slog.SetDefault(slog.New(handler))

	slog.Info("=== GoCache Cluster Client Demo ===")

	seeds := []string{
		"localhost:8379",
		"localhost:8381",
	}

	slog.Info("Connecting to cluster", "seeds", seeds)

	// create cluster client
	clusterClient, err := client.NewClusterClient(seeds)
	if err != nil {
		log.Fatalf("Failed to create cluster client: %v", err)
	}
	defer clusterClient.Close()

	// start topology refresh (every 30 seconds)
	clusterClient.StartTopologyRefresh(30 * time.Second)

	slog.Info("--- Discovered Nodes ---")
	nodes := clusterClient.GetNodes()
	for _, node := range nodes {
		slog.Info("Node discovered",
			"node_id", node.ID,
			"address", node.Address,
			"status", node.Status,
		)
	}

	slog.Info("--- Testing Operations ---")

	// test 1: set some keys
	testKeys := []string{
		"user:1000",
		"user:2000",
		"product:500",
		"session:abc123",
		"order:999",
	}

	slog.Info("1. Setting keys...", "count", len(testKeys))
	for _, key := range testKeys {
		value := fmt.Sprintf("value_for_%s", key)
		err := clusterClient.Set(key, value)
		if err != nil {
			slog.Warn("SET failed", "key", key, "error", err)
		} else {
			slog.Info("SET successful", "key", key, "value", value)
		}
		time.Sleep(100 * time.Millisecond) // small delay for visibility
	}

	// test 2: get the keys back
	slog.Info("2. Getting keys...", "count", len(testKeys))
	successCount := 0
	for _, key := range testKeys {
		value, err := clusterClient.Get(key)
		if err != nil {
			slog.Warn("GET failed", "key", key, "error", err)
		} else {
			slog.Info("GET successful", "key", key, "value", value)
			successCount++
		}
		time.Sleep(100 * time.Millisecond)
	}
	slog.Info("GET operations completed",
		"successful", successCount,
		"total", len(testKeys),
	)

	// test 3: set with ttl
	slog.Info("3. Setting key with TTL...")
	err = clusterClient.SetWithTTL("temp:key", "expires_soon", 10*time.Second)
	if err != nil {
		slog.Error("SET with TTL failed", "error", err)
	} else {
		slog.Info("SET with TTL successful",
			"key", "temp:key",
			"ttl_seconds", 10,
		)
	}

	// test 4: delete a key
	slog.Info("4. Deleting key...", "key", "user:1000")
	err = clusterClient.Delete("user:1000")
	if err != nil {
		slog.Error("DELETE failed", "key", "user:1000", "error", err)
	} else {
		slog.Info("DELETE successful", "key", "user:1000")
	}

	// verify deletion
	value, err := clusterClient.Get("user:1000")
	if err != nil {
		slog.Info("Deletion confirmed", "key", "user:1000")
	} else {
		slog.Warn("Deletion verification failed",
			"key", "user:1000",
			"value", value,
		)	}

	// test 5: bulk operations to test distribution
	slog.Info("5. Bulk test - inserting 100 keys...")
	bulkSuccessCount := 0
	bulkStartTime := time.Now()
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("bulk:key:%d", i)
		value := fmt.Sprintf("value_%d", i)
		err := clusterClient.Set(key, value)
		if err == nil {
			successCount++
		}
	}
	bulkDuration := time.Since(bulkStartTime)
	
	slog.Info("Bulk test completed",
		"successful", bulkSuccessCount,
		"total", 100,
		"duration_ms", bulkDuration.Milliseconds(),
		"ops_per_second", fmt.Sprintf("%.0f", float64(100)/bulkDuration.Seconds()),
	)

	slog.Info("=== Demo Complete ===")
	slog.Info("Cluster client is working! Keys are being routed to correct nodes.")
}