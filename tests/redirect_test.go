package test

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/erickim73/gocache/internal/cache"
	"github.com/erickim73/gocache/internal/persistence"
	"github.com/erickim73/gocache/internal/replication"
	"github.com/erickim73/gocache/internal/server"
)

// helper to start a test server (leader or follower)
type TestServer struct {
	cache *cache.Cache
	aof *persistence.AOF
	listener net.Listener
	port int
	role string
	done chan bool
}

// start a leader server for testing
func startTestLeader(t *testing.T, port int) *TestServer {
	// create cache
	myCache, err := cache.New(100)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}

	// create aof
	aofFile := fmt.Sprintf("test_leader_%d.aof", port)
	snapshotFile := fmt.Sprintf("test_leader_%d.snapshot", port)
	aof, err := persistence.NewAOF(aofFile, snapshotFile, persistence.SyncAlways, myCache, 2)
	if err != nil {
		t.Fatalf("Failed to create AOF: %v", err)
	}

	// create leader
	leader, err := replication.NewLeader(myCache, aof, port + 1000) // use port + 1000 for replication
	if err != nil {
		t.Fatalf("Failed to create leader: %v", err)
	}
	go leader.Start()

	// create tcp listener
	address := fmt.Sprintf("localhost:%d", port)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}

	server := &TestServer{
		cache: myCache,
		aof: aof,
		listener: listener,
		port: port,
		role: "leader",
		done: make(chan bool),
	}

	// start accepting connections
	go server.acceptConnections(leader, nil)

	// give server time to start
	time.Sleep(100 * time.Millisecond)

	return server
}

// start a follower server for testing
func startTestFollower(t *testing.T, port int, leaderAddr string) *TestServer {
	// create cache
	myCache, err := cache.New(100)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}

	// create aof
	aofFile := fmt.Sprintf("test_follower_%d.aof", port)
	snapshotFile := fmt.Sprintf("test_follower_%d.snapshot", port)
	aof, err := persistence.NewAOF(aofFile, snapshotFile, persistence.SyncAlways, myCache, 2)
	if err != nil {
		t.Fatalf("Failed to create AOF: %v", err)
	}

	// create node state
	nodeState, err := server.NewNodeState("follower", nil, leaderAddr)

	// create follower
	follower, err := replication.NewFollower(myCache, aof, leaderAddr, "test-follower", nil, 1, port + 1000, nodeState)
	if err != nil {
		t.Fatalf("Failed to create follower: %v", err)
	}
	go follower.Start()

	// create tcp listener
	address := fmt.Sprintf("localhost:%d", port)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}

	server := &TestServer{
		cache: myCache,
		aof: aof,
		listener: listener,
		port: port,
		role: "follower",
		done: make(chan bool),
	}

	// start accepting connections
	go server.acceptConnections(nil, nodeState)

	// give server time to start
	time.Sleep(100 * time.Millisecond)

	return server
}

