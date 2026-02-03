package cluster

import (
	"fmt"
	"testing"

	"github.com/erickim73/gocache/internal/cache"
)

// tests that we correctly identify which keys to migrate
func TestMigrationCalculation(t *testing.T) {
	hr := NewHashRing(10)

	hr.AddShard("node1")
	hr.AddShard("node2")
	hr.AddShard("node3")

	// calculate migrations for a new 4th node
	tasks := hr.CalculateMigrations("node4")

	// we should have exactly 10 tasks
	if len(tasks) != 10 {
		t.Errorf("Expected 10 migration tasks, got %d", len(tasks))
	}

	for i, task := range tasks {
		fmt.Printf("Task %d: %s[%d->%d] -> %s\n", i + 1, task.FromNode, task.StartHash, task.EndHash, task.ToNode)

		// verify target is always node4
		if task.ToNode != "node4" {
			t.Errorf("Task %d: expected ToNode=node4, got %s", i + 1, task.ToNode)
		}

		// verify source is one of the existing nodes
		if task.FromNode != "node1" && task.FromNode != "node2" && task.FromNode != "node3" {
			t.Errorf("Task %d: invalid FromNode=%s", i + 1, task.FromNode)
		}
	}
}

// tests that keys are correctly identified as in/out of range
func TestHashRangeDetection(t * testing.T) {
	hr := NewHashRing(100)
	
	// test case 1 - normal range. range [100, 500)
	start := uint32(100)
	end := uint32(500)

	testCases := []struct {
		hash uint32
		expected bool
		reason string
	}{
		{200, true, "hash in middle of range"},
		{100, true, "hash at start (inclusive)"},
		{499, true, "hash just before end"},
		{500, false, "hash at end (exclusive)"},
		{50, false, "hash before start"},
		{600, false, "hash after end"},
	}

	for _, tc := range testCases {
		result := hr.hashInRange(tc.hash, start, end)
		if result != tc.expected {
			t.Errorf("Normal range [%d, %d), hash=%d expected=%v got=%v (%s)", start, end, tc.hash, tc.expected, result, tc.reason)
		}
	}

	// test case 2 - wrap around. range: [4294967000, 100), crosses the 0 boundary
	start = uint32(4294967000)
	end = uint32(100)

	wrapTestCases := []struct {
		hash     uint32
		expected bool
		reason   string
	}{
		{4294967100, true, "hash after start (high end)"},
		{50, true, "hash before end (low end)"},
		{4294967000, true, "hash at start (inclusive)"},
		{100, false, "hash at end (exclusive)"},
		{500, false, "hash in middle gap"},
		{2000000000, false, "hash in middle gap"},
	}

	for _, tc := range wrapTestCases {
		result := hr.hashInRange(tc.hash, start, end)
		if result != tc.expected {
			t.Errorf("Wrap range [%d, %d), hash=%d expected=%v got=%v (%s)", start, end, tc.hash, tc.expected, result, tc.reason)
		}
	}
}

// tests that cache can enumerate keys in hash ranges
func TestKeyEnumeration(t *testing.T) {
	// create cache and hash ring
	c, err := cache.NewCache(1000)
	if err != nil {
		t.Errorf("Error creating cache: %v\n", err)
	}
	hr := NewHashRing(100)

	// add test data
	testKeys := []string{
		"user:1", "user:2", "user:3", "user:4", "user:5",
		"post:1", "post:2", "post:3",
		"session:abc", "session:def",
	}

	for _, key := range testKeys {
		c.Set(key, fmt.Sprintf("value-%s", key), 0)
	}

	// test GetAllKeys
	allKeys := c.GetAllKeys()
	if len(allKeys) != len(testKeys) {
		t.Errorf("Expected %d keys, got %d", len(testKeys), len(allKeys))
	}

	// test GetKeysInHashRange
	start := uint32(0)
	end := uint32(2147483647) // half the has space

	keysInRange := c.GetKeysInHashRange(start, end, hr.Hash)

	fmt.Printf("Keys in range [%d, %d): %d out of %d\n", start, end, len(keysInRange), len(testKeys))

	// verify each key in result actually hashes to the range
	for _, key := range keysInRange {
		hash := hr.Hash(key)
		if !hr.hashInRange(hash, start, end) {
			t.Errorf("Key %s with hash %d should not be in range [%d, %d)", key, hash, start, end)
		}
	}
}

// tests the complete migration process
func TestFullMigrationFlow(t *testing.T) {
	// setup
	c, err := cache.NewCache(1000)
	if err != nil {
		t.Errorf("Error creating cache: %v\n", err)
	}

	hr := NewHashRing(10)

	// add initial nodes
	hr.AddShard("node1")
	hr.AddShard("node2")
	hr.AddShard("node3")

	// add test data
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("key:%d", i)
		c.Set(key, fmt.Sprintf("value:%d", i), 0)
	}

	initialCount := len(c.GetAllKeys())
	fmt.Printf("Initial key count: %d\n", initialCount)

	// calculate migrations for node4
	tasks := hr.CalculateMigrations("node4")

	// calculate how many keys would move
	totalToMigrate := 0
	for _, task := range tasks {
		keys := c.GetKeysInHashRange(task.StartHash, task.EndHash, hr.Hash)
		totalToMigrate += len(keys)
	}

	fmt.Printf("Keys that would migrate to node 4: %d (%.1f%%)\n", totalToMigrate, float64(totalToMigrate)/float64(initialCount) * 100)

	// verify roughly 25% of keys would migrate
	expectedPercent := 25.0
	actualPercent := float64(totalToMigrate) / float64(initialCount) * 100

	// allow 10% margin of error due to hashing variance
	if actualPercent < expectedPercent - 10 || actualPercent > expectedPercent + 10 {
		t.Errorf("Expected ~%.0f%% keys to migrate, got %.1f%%", expectedPercent, actualPercent)
	}
}

// benchmarks hash calculation performance
func BenchmarkHashCalculation(b *testing.B) {
	hr := NewHashRing(100)
	testKey := "user:12345"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = hr.Hash(testKey)
	}
}

// benchmarks finding keys in a range
func BenchmarkKeyEnumeration(b *testing.B) {
	c, err := cache.NewCache(10000)
	if err != nil {
		b.Errorf("Error creating cache: %v\n", err)
	}
	hr := NewHashRing(100)

	// add test data
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("key:%d", i)
		c.Set(key, "value", 0)
	}

	start := uint32(0)
	end := uint32(2147483647)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = c.GetKeysInHashRange(start, end, hr.Hash)
	}
}