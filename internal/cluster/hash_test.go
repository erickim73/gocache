package cluster

import (
	"fmt"
	"sync"
	"testing"
)

func TestHashRing_BasicOperations(t *testing.T) {
	// 3 virtual nodes for testing
	ring := NewHashRing(3) 

	// test 1: empty ring should return error
	_, err := ring.GetNode("key1")
	if err == nil {
		t.Error("Expected error when getting node from empty ring")
	}

	// test 2: add a node
	ring.AddShard("node1")

	node, err := ring.GetNode("key1")
	if err != nil {
		t.Errorf("Expected no error, got :%v", err)
	}
	if node != "node1" {
		t.Errorf("Expected node1, got %s", node)
	}

	// test 3: add more nodes
	ring.AddShard("node2")
	ring.AddShard("node3")

	// all keys should map to one of the three nodes
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("key%d", i)
		node, err := ring.GetNode(key)
		if err != nil {
			t.Errorf("Error getting node for %s: %v", key, err)
		}
		if node != "node1" && node != "node2" && node != "node3" {
			t.Errorf("Key %s mapped to unknown node: %s", key, node)
		}
	}
}

func TestHashRing_RemoveNode(t *testing.T) {
	ring := NewHashRing(3)
	ring.AddShard("node1")
	ring.AddShard("node2")
	ring.AddShard("node3")

	// remove node2
	ring.RemoveNode("node2")

	// check that keys no longer map to node2
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("key%d", i)
		node, err := ring.GetNode(key)
		if err != nil {
			t.Errorf("Error getting node for %s: %v", key, err)
		}
		if node == "node2" {
			t.Errorf("Key %s still maps to removed node2", key)
		}
	}
}

func TestHashRing_Distribution(t *testing.T) {
	ring := NewHashRing(150) // more virtual nodes for better distribution
	ring.AddShard("node1")
	ring.AddShard("node2")
	ring.AddShard("node3")

	// count how many keys go to each node
	distribution := make(map[string]int)
	totalKeys := 10000

	for i := 0; i < totalKeys; i++ {
		key := fmt.Sprintf("key%d", i)
		node, err := ring.GetNode(key)
		if err != nil {
			t.Fatalf("Error getting node: %v", err)
		}
		distribution[node]++
	}

	// print distribution
	for node, count := range distribution {
		percentage := float64(count) / float64(totalKeys) * 100
		t.Logf("%s: %d keys (%.2f%%)", node, count, percentage)
	}

	// each node should get roughly 33%
	expectedPerNode := totalKeys / 3
	tolerance := float64(expectedPerNode) * 0.15 // 15% tolerance

	for node, count := range distribution {
		diff := float64(count - expectedPerNode)
		if diff < 0 {
			diff = -diff
		}
		if diff > tolerance {
			t.Errorf("Node %s has poor distribution: %d keys (expected ~%d)", node, count, expectedPerNode)
		}
	}
}

func TestHashRing_ConsistentHashing(t *testing.T) {
	// test that adding a node only moves a small portion of keys
	ring := NewHashRing(150)
	ring.AddShard("node1")
	ring.AddShard("node2")
	ring.AddShard("node3")

	// record where keys map before adding node4
	keyMapping := make(map[string]string)
	totalKeys := 10000

	for i := 0; i < totalKeys; i++ {
		key := fmt.Sprintf("key%d", i)
		node, _ := ring.GetNode(key)
		keyMapping[key] = node
	}

	// add node4
	ring.AddShard("node4")

	// count how many keys moved
	moved := 0
	for i := 0; i < totalKeys; i++ {
		key := fmt.Sprintf("key%d", i)
		newNode, _ := ring.GetNode(key)
		if keyMapping[key] != newNode {
			moved++
		}
	}

	movedPercentage := float64(moved) / float64(totalKeys) * 100
	t.Logf("keys moved: %d/%d (%.2f%%)", moved, totalKeys, movedPercentage)

	// should move approximately 25% of keys
	// allow 15-35% range due to randomness
	if movedPercentage < 15 || movedPercentage > 35 {
		t.Errorf("Expected ~25%% of keys to move, but %.2f%% moved", movedPercentage)
	}
}

func TestHashRing_Concurrent(t *testing.T) {
	// test thread safety
	ring := NewHashRing(150)
	ring.AddShard("node1")
	ring.AddShard("node2")

	var wg sync.WaitGroup

	// spawn 10 goroutines that query keys
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				key := fmt.Sprintf("key-%d-%d", id, j)
				_, err := ring.GetNode(key)
				if err != nil {
					t.Errorf("Error in goroutine %d: %v", id, err)
				}
			}
		}(i)
	}

	// spawn 5 goroutines that add nodes
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done() 
			nodeID := fmt.Sprintf("concurrent-node-%d", id)
			ring.AddShard(nodeID)
		}(i)
	}

	// spawn 3 goroutines that removes nodes
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			nodeID := fmt.Sprintf("concurrent-node-%d", id)
			ring.RemoveNode(nodeID)
		}(i)
	}

	wg.Wait()
	t.Log("Concurrent test completed successfully")
}