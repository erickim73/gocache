package test

import (
	"fmt"
	"github.com/erickim73/gocache/internal/cluster"
)

func main() {
	// creates hash ring exactly like servers
	ring := cluster.NewHashRing(150)
	ring.AddNode("node1")
	ring.AddNode("node2")
	ring.AddNode("node3")

	// test keys
	keys := []string{
		"user:100",
		"user:200",
		"user:300",
		"user:400",
		"user:500",
		"post:1",
		"post:2",
		"session:abc",
	}

	fmt.Println("Expected Key Distribution:")
	fmt.Println("==========================")
	
	for _, key := range keys {
		node, _ := ring.GetNode(key)
		fmt.Printf("%-15s → %s\n", key, node)
	}
}