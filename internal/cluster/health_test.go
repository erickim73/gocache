// In internal/cluster/health_test.go

package cluster

import (
	"net"
	"testing"
	"time"
)

// TestHealthChecker_BasicPing tests that pinging a live node works
func TestHealthChecker_BasicPing(t *testing.T) {
	// Step 1: Start a simple test server that responds to PING
	listener, err := net.Listen("tcp", "localhost:0") // :0 means random port
	if err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer listener.Close()

	testAddr := listener.Addr().String()
	t.Logf("Test server listening on %s", testAddr)

	// Handle one connection - respond with PONG
	go func() {
		conn, _ := listener.Accept()
		defer conn.Close()

		// Read PING command (we don't need to parse it for this simple test)
		buf := make([]byte, 1024)
		conn.Read(buf)

		// Send PONG response
		conn.Write([]byte("+PONG\r\n"))
	}()

	// Step 2: Create health checker
	hashRing := NewHashRing(10)
	hc := NewHealthChecker(hashRing, 1*time.Second, 3, 1*time.Second)

	// Step 3: Ping the test server
	err = hc.pingNode(testAddr)

	// Step 4: Verify ping succeeded
	if err != nil {
		t.Errorf("Ping should succeed, got error: %v", err)
	} else {
		t.Log("✓ Ping succeeded")
	}
}

// TestHealthChecker_PingFails tests pinging a dead node
func TestHealthChecker_PingFails(t *testing.T) {
	// Create health checker
	hashRing := NewHashRing(10)
	hc := NewHealthChecker(hashRing, 1*time.Second, 3, 1*time.Second)

	// Try to ping a non-existent address
	err := hc.pingNode("localhost:99999") // Invalid port

	// Verify ping failed
	if err == nil {
		t.Error("Ping should fail for non-existent server")
	} else {
		t.Logf("✓ Ping correctly failed: %v", err)
	}
}

// TestHealthChecker_FailureDetection tests that node is marked dead after threshold
func TestHealthChecker_FailureDetection(t *testing.T) {
	// Create health checker with fast timing for testing
	hashRing := NewHashRing(10)
	hc := NewHealthChecker(
		hashRing,
		1*time.Second, // check every 1 second
		3,             // 3 failures = dead
		1*time.Second, // 1 second timeout
	)

	// Track if callback was called
	callbackCalled := false
	failedNodeID := ""

	hc.SetCallbacks(
		func(nodeID string) {
			callbackCalled = true
			failedNodeID = nodeID
			t.Logf("✓ Failure callback called for node: %s", nodeID)
		},
		nil, // No recovery callback needed for this test
	)

	// Register a node with invalid address
	hc.RegisterNode("test-node", "localhost:99999")

	// Start health checking
	hc.Start()
	defer hc.Stop()

	// Wait for 4 seconds (enough for 3+ failures at 1 second interval)
	t.Log("Waiting for failure detection...")
	time.Sleep(5 * time.Second)

	// Check if node is marked dead
	status := hc.GetNodeStatus("test-node")
	if status != NodeStatusDead {
		t.Errorf("Node should be dead, got: %s", status)
	} else {
		t.Log("✓ Node marked as dead")
	}

	// Check if callback was called
	if !callbackCalled {
		t.Error("Failure callback was not called")
	} else {
		t.Log("✓ Failure callback was triggered")
	}

	if failedNodeID != "test-node" {
		t.Errorf("Callback called with wrong node ID: %s", failedNodeID)
	}
}

// TestHealthChecker_RecoveryDetection tests that recovered node is marked alive
func TestHealthChecker_RecoveryDetection(t *testing.T) {
	// Start a server that we can stop and restart
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	testAddr := listener.Addr().String()

	// Server that responds to PING
	serverRunning := true
	go func() {
		for serverRunning {
			conn, err := listener.Accept()
			if err != nil {
				continue
			}

			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				c.Read(buf)
				c.Write([]byte("+PONG\r\n"))
			}(conn)
		}
	}()

	// Create health checker
	hashRing := NewHashRing(10)
	hc := NewHealthChecker(hashRing, 1*time.Second, 2, 1*time.Second)

	recoveryCallbackCalled := false
	hc.SetCallbacks(
		func(nodeID string) {
			t.Logf("Node %s failed", nodeID)
		},
		func(nodeID string) {
			recoveryCallbackCalled = true
			t.Logf("✓ Recovery callback called for node: %s", nodeID)
		},
	)

	// Register and start
	hc.RegisterNode("test-node", testAddr)
	hc.Start()
	defer hc.Stop()

	// Wait for node to be healthy
	time.Sleep(2 * time.Second)

	status := hc.GetNodeStatus("test-node")
	if status != NodeStatusAlive {
		t.Errorf("Node should be alive initially, got: %s", status)
	}

	// Kill the server
	t.Log("Stopping server to simulate failure...")
	listener.Close()
	serverRunning = false

	// Wait for detection (2 failures * 1 second)
	time.Sleep(4 * time.Second)

	status = hc.GetNodeStatus("test-node")
	if status != NodeStatusDead {
		t.Errorf("Node should be dead after server stops, got: %s", status)
	} else {
		t.Log("✓ Node detected as dead")
	}

	// Restart server
	t.Log("Restarting server to simulate recovery...")
	listener, err = net.Listen("tcp", testAddr)
	if err != nil {
		t.Fatalf("Failed to restart server: %v", err)
	}
	defer listener.Close()

	serverRunning = true
	go func() {
		for serverRunning {
			conn, err := listener.Accept()
			if err != nil {
				continue
			}

			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				c.Read(buf)
				c.Write([]byte("+PONG\r\n"))
			}(conn)
		}
	}()

	// Wait for recovery detection
	time.Sleep(3 * time.Second)

	status = hc.GetNodeStatus("test-node")
	if status != NodeStatusAlive {
		t.Errorf("Node should be alive after recovery, got: %s", status)
	} else {
		t.Log("✓ Node recovered and marked alive")
	}

	if !recoveryCallbackCalled {
		t.Error("Recovery callback was not called")
	}
}

// TestHealthChecker_MultipleNodes tests monitoring multiple nodes
func TestHealthChecker_MultipleNodes(t *testing.T) {
	hashRing := NewHashRing(10)
	hc := NewHealthChecker(hashRing, 1*time.Second, 3, 1*time.Second)

	// Register multiple nodes - mix of valid and invalid
	hc.RegisterNode("node1", "localhost:99990") // Invalid
	hc.RegisterNode("node2", "localhost:99991") // Invalid
	hc.RegisterNode("node3", "localhost:99992") // Invalid

	hc.Start()
	defer hc.Stop()

	// Wait for checks
	time.Sleep(5 * time.Second)

	// All should be dead
	healthyNodes := hc.GetHealthyNodes()
	if len(healthyNodes) != 0 {
		t.Errorf("Expected 0 healthy nodes, got %d", len(healthyNodes))
	} else {
		t.Log("✓ All dead nodes correctly identified")
	}

	// Check each individually
	for _, nodeID := range []string{"node1", "node2", "node3"} {
		status := hc.GetNodeStatus(nodeID)
		if status != NodeStatusDead {
			t.Errorf("Node %s should be dead, got: %s", nodeID, status)
		}
	}

	t.Log("✓ Multiple node monitoring works")
}
