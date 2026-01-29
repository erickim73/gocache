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