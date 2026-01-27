package test

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/erickim73/gocache/pkg/client"
	"github.com/erickim73/gocache/pkg/protocol"
)

// TestRedirect verifies that clients automatically follow redirects from follower to leader
func TestRedirect(t *testing.T) {
	// clean up any existing test files
	defer cleanupTestFiles()

	// 1. start leader on port 7379
	t.Log("Starting leader on port 7379...")
	leader := startTestLeader(t, 7379)
	defer leader.Stop()

	// 2. start a follower on port 7380 that redirects to leader
	t.Log("Starting follower on port 7380...")
	follower := startTestFollower(t, 7380, "localhost:7379")
	defer follower.Stop()

	// give servers time to start
	time.Sleep(200 * time.Millisecond)

	// 3. connect client to follower (not leader)
	t.Log("Connecting client to follower at localhost:7370...")
	conn, err := client.NewClient("localhost:7380")
	if err != nil {
		t.Fatalf("Failed to connect to follower: %v", err)
	}
	defer conn.Close()

	// 4. try to set a key (should trigger redirect to leader)
	t.Log("Attempting SET through follower (should redirect)...")
	err = conn.Set("testkey", "testvalue")
	if err != nil {
		t.Fatalf("SET failed after redirect: %v", err)
	}

	// 5. verify set succeeded on leader
	t.Log("Verifying key was set on leader...")
	value, exists := leader.cache.Get("testkey")
	if !exists {
		t.Fatal("Key 'testKey' not found on leader after SET")
	}
	if value != "testvalue" {
		t.Errorf("Expected testKey=testValue, got testKey=%s", value)
	}

	// 6. verify client can now directly use leader connection
	t.Log("Verifying client use leader for subsequent requests...") 
	err = conn.Set("testKey2", "testValue2")
	if err != nil {
		t.Fatalf("Second SET failed: %v", err)
	}

	value2, exists := leader.cache.Get("testKey2")
	if !exists {
		t.Fatalf("Key 'testKey2' not found on leader")
	}
	if value2 != "testValue2" {
		t.Errorf("Expected testKey2=testValue2, got testKey2=%s", value2)
	}

	t.Log("✓ Client successfully followed redirect from follower to leader")
}

func TestFollowerReads(t *testing.T) {
	// clean up any existing test files
	defer cleanupTestFiles()

	// 1. start leader on port 7381
	t.Log("Starting leader on port 7381...")
	leader := startTestLeader(t, 7381)
	defer leader.Stop()

	// 2. start a follower on port 7382 that redirects to leader
	t.Log("Starting follower on port 7382...")
	follower := startTestFollower(t, 7382, "localhost:7381")
	defer follower.Stop()

	// give servers time to start
	time.Sleep(200 * time.Millisecond)

	// 3. SET a key through the leader
	t.Log("Setting key on leader...")
	leader.cache.Set("readKey", "readValue", 0)

	// 4. manually replicate for test
	follower.cache.Set("readKey", "readValue", 0)
	time.Sleep(100 * time.Millisecond)

	// 5. connect client to FOLLOWER
	t.Log("Connecting client to follower...")
	conn, err := client.NewClient("localhost:7382")
	if err != nil {
		t.Fatalf("Failed to connect to follower: %v", err)
	}
	defer conn.Close()

	// 6. GET key from follower
	t.Log("Attempting GET from follower...")
	value, err := conn.Get("readKey")
	if err != nil {
		t.Fatalf("GET from followerfailed: %v", err)
	}

	// 7. verify there's the correct value
	if value != "readValue" {
		t.Errorf("Expected value, got %s", value)
	}

	t.Log("✓ Follower successfully served read request")
}

// TestRedirectLoop verifies that clients detect and fail on redirect loops
func TestRedirectLoop(t *testing.T) {
	// clean up any existing test files
	defer cleanupTestFiles()

	// 1. start two follower servers that redirect to each other, creating an infinite lop
	t.Log("Starting server1 that redirects to server2...")
	server1 := startMockRedirectServer(t, 7383, "localhost:7384")
	defer server1.Stop()

	t.Log("Starting server2 that redirects to server1...")
	server2 := startMockRedirectServer(t, 7384, "localhost:7383")
	defer server2.Stop()

	// give server time to start
	time.Sleep(100 * time.Millisecond)

	// 2. connect client to server1
	t.Log("Connecting client to server1...")
	conn, err := client.NewClient("localhost:7383")
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// 3. try to SET - should fail after max redirects (5) attempts
	t.Log("Attempting SET (should fail due to redirect loop)...")
	err = conn.Set("key", "value")

	// 4. verify it failed with correct order
	if err == nil {
		t.Fatal("Expected error due to redirect lop, but SET succeeded")
	}

	// 5. check error message mentions too many redirects
	if !strings.Contains(err.Error(), "too many redirects") {
		t.Errorf("Expected 'too many redirects' error, got: %v", err)
	}

	t.Log("✓ Client properly detected and failed on redirect loop")
}

// TestMultipleRedirects verifies client can handle multiple consecutive redirects
func TestMultipleRedirects(t *testing.T) {
	// clean up any existing test files
	defer cleanupTestFiles()

	// 1. start leader
	t.Log("Starting leader on port 7385...")
	leader := startTestLeader(t, 7385)
	defer leader.Stop()

	// 2. start follower
	t.Log("Starting follower on port 7386")
	follower := startTestFollower(t, 7386, "localhost:7385")
	defer follower.Stop()

	// give time for servers to start
	time.Sleep(200 * time.Millisecond)

	// 3. connect to follower
	conn, err := client.NewClient("localhost:7386")
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// 4. do multiple write operations (each should redirect)
	t.Log("Performing multiple SET operations through follower...")
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("key%d", i)
		value := fmt.Sprintf("value%d", i)

		err = conn.Set(key, value)
		if err != nil {
			t.Fatalf("Set %s failed: %v", key, err)
		}

		// verify on leader
		leaderValue, exists := leader.cache.Get(key)
		if !exists || leaderValue != value {
			t.Errorf("Key %s not properly set on leader", key)
		}
	}

	t.Log("✓ Client successfully handled multiple redirects")
}

// TestDeleteRedirect verifies DELETE operations also redirect properly
func TestDeleteRedirect(t *testing.T) {
	// clean up any existing test files
	defer cleanupTestFiles()

	// 1. start leader
	t.Log("Starting leader on port 7387...")
	leader := startTestLeader(t, 7387)
	defer leader.Stop()

	// 2. start follower
	t.Log("Starting follower on port 7388")
	follower := startTestFollower(t, 7388, "localhost:7387")
	defer follower.Stop()

	// give time for servers to start
	time.Sleep(200 * time.Millisecond)

	// 3. set a key on leader first
	leader.cache.Set("deleteKey", "deleteValue", 0)

	// 4. connect to follower
	conn, err := client.NewClient("localhost:7388")
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// 5. try to delete through follower (should redirect)
	t.Log("Attempting DELETE through follower...")
	err = conn.Delete("deleteKey")
	if err != nil {
		t.Fatalf("DELETE failed: %v", err)
	}

	// 6. verify key is deleted on leader
	_, exists := leader.cache.Get("deleteKey")
	if exists {
		t.Error("Key still exists on leader after DELETE")
	}

	t.Log("✓ DELETE successfully redirected to leader")
}


// mock redirect server that always redirects to another address
type MockRedirectServer struct {
	listener net.Listener
	redirectTo string
	done chan bool
}

// start a mock redirect server for testing
func startMockRedirectServer(t *testing.T, port int, redirectTo string) *MockRedirectServer {
	address := fmt.Sprintf("localhost:%d", port)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("Failed to create mock redirect server: %v", err)
	}

	server := &MockRedirectServer{
		listener: listener,
		redirectTo: redirectTo,
		done: make(chan bool),
	}

	go server.acceptConnections()
	return server
}

// accept connections and handle commands
func (s *MockRedirectServer) acceptConnections() {
	for {
		select {
		case <- s.done:
			return
		default:
			conn, err := s.listener.Accept()
			if err != nil {
				continue
			}
			go s.handleConnection(conn)
		}
	}
}

// handle a single connection
func (s *MockRedirectServer) handleConnection(conn net.Conn) {
	defer conn.Close()

	reader := bufio.NewReader(conn)
	for {
		_, err := protocol.Parse(reader)
		if err != nil {
			return
		}

		// always send redirect
		redirect := fmt.Sprintf("-MOVED %s\r\n", s.redirectTo)
		conn.Write([]byte(redirect))
	}
}

// stop the test server
func (s *MockRedirectServer) Stop() {
	close(s.done)
	s.listener.Close()
}

// helper function to clean up test files
func cleanupTestFiles() {
	// remove test AOF and snapshot files
	files := []string{
		"test_leader_7379.aof",
		"test_leader_7379.snapshot",
		"test_follower_7380.aof",
		"test_follower_7380.snapshot",
		"test_leader_7381.aof",
		"test_leader_7381.snapshot",
		"test_follower_7382.aof",
		"test_follower_7382.snapshot",
		"test_leader_7385.aof",
		"test_leader_7385.snapshot",
		"test_follower_7386.aof",
		"test_follower_7386.snapshot",
		"test_leader_7387.aof",
		"test_leader_7387.snapshot",
		"test_follower_7388.aof",
		"test_follower_7388.snapshot",
	}

	for _, file := range files {
		os.Remove(file)
	}
}