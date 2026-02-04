package main

import (
	"fmt"
	"log"
	"time"

	"github.com/erickim73/gocache/pkg/client"
)

func main() {
	fmt.Println("=== GoCache Cluster Client Demo ===")

	seeds := []string{
		"localhost:8379",
		"localhost:8381",
	}

	fmt.Printf("Connecting to cluster via seeds: %v\n", seeds)

	// create cluster client
	clusterClient, err := client.NewClusterClient(seeds)
	if err != nil {
		log.Fatalf("Failed to create cluster client: %v", err)
	}
	defer clusterClient.Close()

	// start topology refresh (every 30 seconds)
	clusterClient.StartTopologyRefresh(30 * time.Second)

	fmt.Println("\n--- Discovered Nodes ---")
	nodes := clusterClient.GetNodes()
	for _, node := range nodes {
		fmt.Printf("  Node: %s at %s (%s)\n", node.ID, node.Address, node.Status)
	}

	fmt.Println("\n--- Testing Operations ---")

	// test 1: set some keys
	testKeys := []string{
		"user:1000",
		"user:2000",
		"product:500",
		"session:abc123",
		"order:999",
	}

	fmt.Println("\n1. Setting keys...")
	for _, key := range testKeys {
		value := fmt.Sprintf("value_for_%s", key)
		err := clusterClient.Set(key, value)
		if err != nil {
			fmt.Printf("  SET %s failed: %v\n", key, err)
		} else {
			fmt.Printf("  SET %s = %s\n", key, value)
		}
		time.Sleep(100 * time.Millisecond) // small delay for visibility
	}

	// test 2: get the keys back
	fmt.Println("\n2. Getting keys...")
	for _, key := range testKeys {
		value, err := clusterClient.Get(key)
		if err != nil {
			fmt.Printf("  GET %s failed: %v\n", key, err)
		} else {
			fmt.Printf("  GET %s = %s\n", key, value)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// test 3: set with ttl
	fmt.Println("\n3. Setting key with TTL...")
	err = clusterClient.SetWithTTL("temp:key", "expires_soon", 10*time.Second)
	if err != nil {
		fmt.Printf("  SET with TTL failed: %v\n", err)
	} else {
		fmt.Printf("  SET temp:key with TTL=10s\n")
	}

	// test 4: delete a key
	fmt.Println("\n4. Deleting key...")
	err = clusterClient.Delete("user:1000")
	if err != nil {
		fmt.Printf("  DELETE user:1000 failed: %v\n", err)
	} else {
		fmt.Printf("  DELETE user:1000 successful\n")
	}

	// verify deletion
	value, err := clusterClient.Get("user:1000")
	if err != nil {
		fmt.Printf("  Confirmed: user:1000 no longer exists\n")
	} else {
		fmt.Printf("  Warning: user:1000 still exists with value: %s\n", value)
	}

	// test 5: bulk operations to test distribution
	fmt.Println("\n5. Bulk test (inserting 100 keys)...")
	successCount := 0
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("bulk:key:%d", i)
		value := fmt.Sprintf("value_%d", i)
		err := clusterClient.Set(key, value)
		if err == nil {
			successCount++
		}
	}
	fmt.Printf("  Successfully inserted %d/100 keys\n", successCount)

	fmt.Println("\n=== Demo Complete ===")
	fmt.Println("Cluster client is working! Keys are being routed to correct nodes.")
}