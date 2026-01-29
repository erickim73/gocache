package cluster

import (
	"fmt"
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
	ring.AddNode("node1")

	node, err := ring.GetNode("key1")
	if err != nil {
		t.Errorf("Expected no error, got :%v", err)
	}
	if node != "node1" {
		t.Errorf("Expected node1, got %s", node)
	}

	// test 3: add more nodes
	ring.AddNode("node2")
	ring.AddNode("node3")

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
	ring.AddNode("node1")
	ring.AddNode("node2")
	ring.AddNode("node3")

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
	ring.AddNode("node1")
	ring.AddNode("node2")
	ring.AddNode("node3")

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