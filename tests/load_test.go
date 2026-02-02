package tests

import (
	"bufio"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/erickim73/gocache/pkg/protocol"
)

// tests loading 10,000 keys into the cluster
func TestClusterLoadKeys(t *testing.T) {
	conn, err := net.Dial("tcp", "localhost:8379")
	if err != nil {
		t.Fatalf("Failed to connect to node1: %v", err)
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)

	t.Log("Loading 10,000 keys into cluster...")

	successCount := 0
	movedCount := 0
	errorCount := 0

	for i := 1; i <= 10000; i++ {
		key := fmt.Sprintf("key:%d", i)
		value := fmt.Sprintf("value%d", i)

		// send SET command
		cmd := protocol.EncodeArray([]interface{}{"SET", key, value})
		_, err := conn.Write([]byte(cmd))
		if err != nil {
			t.Logf("Error sending key %s: %v", key, err)
			errorCount++
			continue
		}

		// read response
		response, err := protocol.Parse(reader)
		if err != nil {
			t.Logf("Error reading response for key %s: %v", key, err)
			errorCount++
			continue
		}

		// handle all response types
		switch v := response.(type) {
		case string:
			if v == "OK" {
				successCount++
			} else if len(v) >= 5  && v[:5] == "MOVED" {
				movedCount++
			} else if len(v) > 0 && v[0] == '-' {
				if len(v) >= 6 && v[1:6] == "MOVED" {
					movedCount++
				} else {
					t.Logf("Error response for key %s: %s", key, v)
					errorCount++
				}
			} else {
				t.Logf("Unexpected string response for key %s: %s", key, v)
				errorCount++
			}
		case error:
			errMsg := v.Error()
			if len(errMsg) >= 5 && errMsg[:5] == "MOVED" {
				movedCount++
			} else {
				t.Logf("Error for key %s: %v", key, v)
				errorCount++
			}
		case nil:
			t.Logf("Nil response for key %s", key)
			errorCount++
		default:
			t.Logf("Unexpected response type for key %s: %T = %v", key, v, v)
			errorCount++
		}

		// progress indicator
		if i % 1000 == 0 {
			t.Logf("Progress: %d/%d keys processed", i, 10000)
		}
	}

	t.Logf("\n=== Load Results ===")
	t.Logf("Success: %d", successCount)
	t.Logf("MOVED:   %d", movedCount)
	t.Logf("Errors:  %d", errorCount)
	t.Logf("Total:   %d", successCount+movedCount+errorCount)

	// assertions
	total := successCount + movedCount + errorCount
	if total != 10000 {
		t.Errorf("Expected 10000 total operations, got %d", total)
	}

	if errorCount > 100 {
		t.Errorf("Too many errors: %d (threshold: 100)", errorCount)
	}

	// most keys should succeed or be MOVED (not error)
	if successCount+movedCount < 9900 {
		t.Errorf("Too few successful/moved operations: %d", successCount+movedCount)
	}
}

// verifies keys are distributed across nodes
func TestClusterKeyDistribution(t *testing.T) {
	nodes := []string{"localhost:8379", "localhost:8380", "localhost:8381", "localhost:8382"}
	keyCounts := make(map[string]int)

	for _, nodeAddr := range nodes {
		count, err := getNodeKeyCount(nodeAddr)
		if err != nil {
			t.Logf("Warning: Could not get key count from %s: %v", nodeAddr, err)
			continue
		}
		keyCounts[nodeAddr] = count
		t.Logf("Node %s has %d keys", nodeAddr, count)
	}

	// check distribution is reasonable (within 30% of average)
	totalKeys := 0
	nodeCount := len(keyCounts)
	for _, count := range keyCounts {
		totalKeys += count
	}

	if nodeCount == 0 {
		t.Fatal("No nodes responded")
	}

	avgKeys := float64(totalKeys) / float64(nodeCount)
	threshold := avgKeys * 0.30 // 30% variance allowed

	t.Logf("Average keys per node: %.1f (threshold: ±%.1f)", avgKeys, threshold)

	for nodeAddr, count := range keyCounts {
		diff := float64(count) - avgKeys
		if diff < -threshold || diff > threshold {
			t.Errorf("Node %s has poor distribution: %d keys (avg: %.1f)", nodeAddr, count, avgKeys)
		}
	}
}

// tests retrieving keys from cluster
func TestClusterKeyRetrieval(t *testing.T) {
	conn, err := net.Dial("tcp", "localhost:8379")
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)

	t.Log("Testing key retrieval...")

	testKeys := []string{"key:1", "key:100", "key:500", "key:1000", "key:5000", "key:10000"}
	successCount := 0
	movedCount := 0
	notFoundCount := 0

	for _, key := range testKeys {
		cmd := protocol.EncodeArray([]interface{}{"GET", key})
		_, err := conn.Write([]byte(cmd))
		if err != nil {
			t.Errorf("Error sending GET for %s: %v", key, err)
			continue
		}

		response, err := protocol.Parse(reader)
		if err != nil {
			t.Errorf("Error reading response for %s: %v", key, err)
			continue
		}

		if str, ok := response.(string); ok {
			if str == "" || str == "$-1\r\n" {
				notFoundCount++
				t.Logf("Key %s not found", key)
			} else if len(str) > 0 && str[0] == '-' && len(str) > 5 && str[1:6] == "MOVED" {
				movedCount++
				t.Logf("Key %s requires redirect: %s", key, str)
			} else {
				successCount++
				t.Logf("Key %s retrieved successfully", key)
			}
		}
	}

	t.Logf("\n=== Retrieval Results ===")
	t.Logf("Success:   %d", successCount)
	t.Logf("MOVED:     %d", movedCount)
	t.Logf("Not Found: %d", notFoundCount)
}

// tests CLUSTER NODES command
func TestClusterTopology(t *testing.T) {
	nodes := []string{"localhost:8379", "localhost:8380", "localhost:8381", "localhost:8382"}

	for _, nodeAddr := range nodes {
		t.Logf("Checking topology from %s", nodeAddr)

		conn, err := net.Dial("tcp", nodeAddr)
		if err != nil {
			t.Logf("Warning: Could not connect to %s: %v", nodeAddr, err)
			continue
		}

		reader := bufio.NewReader(conn)

		cmd := protocol.EncodeArray([]interface{}{"CLUSTER", "NODES"})
		_, err = conn.Write([]byte(cmd))
		if err != nil {
			t.Errorf("Error sending CLUSTER NODES to %s: %v", nodeAddr, err)
			conn.Close()
			continue
		}

		response, err := protocol.Parse(reader)
		conn.Close()

		if err != nil {
			t.Errorf("Error reading CLUSTER NODES from %s: %v", nodeAddr, err)
			continue
		}

		if str, ok := response.(string); ok {
			t.Logf("Topology from %s:\n%s", nodeAddr, str)
		}
	}
}

// tests dynamic node addition and removal
func TestClusterAddRemoveNode(t *testing.T) {
	// test requires node5 to be configured but not started
	t.Skip("Skipping add/remove test - requires manual node5 setup")

	conn, err := net.Dial("tcp", "localhost:8379")
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)

	// test adding node5
	t.Log("Adding node5 to cluster...")
	cmd := protocol.EncodeArray([]interface{}{"CLUSTER", "ADDNODE", "node5", "localhost:8383"})
	_, err = conn.Write([]byte(cmd))
	if err != nil {
		t.Fatalf("Error sending ADDNODE: %v", err)
	}

	response, err := protocol.Parse(reader)
	if err != nil {
		t.Fatalf("Error reading ADDNODE response: %v", err)
	}

	t.Logf("ADDNODE response: %v", response)

	// verify node5 appears in topology
	time.Sleep(1 * time.Second)
	cmd = protocol.EncodeArray([]interface{}{"CLUSTER", "NODES"})
	conn.Write([]byte(cmd))
	response, _ = protocol.Parse(reader)
	t.Logf("Topology after add:\n%v", response)

	// test removing node5
	t.Log("Removing node5 from cluster...")
	cmd = protocol.EncodeArray([]interface{}{"CLUSTER", "REMOVENODE", "node5"})
	conn.Write([]byte(cmd))
	response, err = protocol.Parse(reader)
	if err != nil {
		t.Fatalf("Error reading REMOVENODE response: %v", err)
	}

	t.Logf("REMOVENODE response: %v", response)

	// verify node5 removed from topology
	time.Sleep(1 * time.Second)
	cmd = protocol.EncodeArray([]interface{}{"CLUSTER", "NODES"})
	conn.Write([]byte(cmd))
	response, _ = protocol.Parse(reader)
	t.Logf("Topology after remove:\n%v", response)
}

// verifies the same key always routes to same node
func TestConsistentHashing(t *testing.T) {
	testKey := "consistent:test:key"

	nodes := []string{"localhost:8379", "localhost:8380", "localhost:8381"}
	var targetNode string

	for i, nodeAddr := range nodes {
		conn, err := net.Dial("tcp", nodeAddr)
		if err != nil {
			t.Logf("Warning: Could not connect to %s", nodeAddr)
			continue
		}

		reader := bufio.NewReader(conn)

		cmd := protocol.EncodeArray([]interface{}{"GET", testKey})
		conn.Write([]byte(cmd))

		response, err := protocol.Parse(reader)
		conn.Close()

		if err != nil {
			t.Errorf("Error from %s: %v", nodeAddr, err)
			continue
		}

		if str, ok := response.(string); ok {
			if len(str) > 0 && str[0] == '-' && len(str) > 5 && str[1:6] == "MOVED" {
				// extract target from MOVED response
				if i == 0 {
					targetNode = str
					t.Logf("Key routes to: %s", str)
				} else {
					if str != targetNode {
						t.Errorf("Inconsistent routing! Node %s returned %s, expected %s", 
							nodeAddr, str, targetNode)
					}
				}
			}
		}
	}
}

// helper function to get key count from a node
func getNodeKeyCount(nodeAddr string) (int, error) {
	conn, err := net.DialTimeout("tcp", nodeAddr, 2*time.Second)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)

	cmd := protocol.EncodeArray([]interface{}{"DBSIZE"})
	_, err = conn.Write([]byte(cmd))
	if err != nil {
		return 0, err
	}

	response, err := protocol.Parse(reader)
	if err != nil {
		return 0, err
	}

	// handle both int and string response
	switch v := response.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case string:
		// try to parse
		var count int
		_, err := fmt.Sscanf(v, "%d", &count)
		if err != nil {
			return 0, fmt.Errorf("could not parse count from string: %s", v)
		}
		return count, nil
	default:
		return 0, fmt.Errorf("unexpected response type: %T (value: %v)", response, response)
	}
}

// benchmarks SET operations
func BenchmarkClusterSet(b *testing.B) {
	conn, err := net.Dial("tcp", "localhost:8379")
	if err != nil {
		b.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("bench:key:%d", i)
		value := fmt.Sprintf("value%d", i)

		cmd := protocol.EncodeArray([]interface{}{"SET", key, value})
		conn.Write([]byte(cmd))
		protocol.Parse(reader)
	}
}

// benchmarks GET operations
func BenchmarkClusterGet(b *testing.B) {
	conn, err := net.Dial("tcp", "localhost:8379")
	if err != nil {
		b.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)

	// Pre-load a key
	cmd := protocol.EncodeArray([]interface{}{"SET", "bench:key", "value"})
	conn.Write([]byte(cmd))
	protocol.Parse(reader)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cmd := protocol.EncodeArray([]interface{}{"GET", "bench:key"})
		conn.Write([]byte(cmd))
		protocol.Parse(reader)
	}
}