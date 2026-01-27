package test

import (
	"bufio"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/erickim73/gocache/internal/cache"
	"github.com/erickim73/gocache/internal/persistence"
	"github.com/erickim73/gocache/pkg/protocol"
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

// accept connections and handle commands
func (s *TestServer) acceptConnections(leader *replication.Leader, nodeState *server.NodeState) {
	for {
		select {
		case <- s.done:
			return
		default:
			conn, err := s.listener.Accept()
			if err != nil {
				continue
			}
			go s.handleConnection(conn, leader, nodeState)
		}
	}
}

// handle a single connection
func (s *TestServer) handleConnection(conn net.Conn, leader *replication.Leader, nodeState *server.NodeState) {
	defer conn.Close()

	reader := bufio.NewReader(conn)
	for {
		result, err := protocol.Parse(reader)
		if err != nil {
			return
		}

		resultSlice, ok := result.([]interface{})
		if !ok {
			return
		}

		command := resultSlice[0].(string)

		// check if write operation on follower
		if s.role == "follower" && (command == "SET" || command == "DEL") {
			redirect := fmt.Sprintf("-MOVED %s\r\n", nodeState.GetLeaderAddr())
			conn.Write([]byte(redirect))
			continue
		}

		// handle commands
		switch command {
		case "SET":
			if len(resultSlice) < 3 {
				conn.Write([]byte(protocol.EncodeError("Invalid SET")))
				continue
			}

			key := resultSlice[1].(string)
			value := resultSlice[2].(string)
			s.cache.Set(key, value, 0)
			conn.Write([]byte(protocol.EncodeSimpleString("OK")))
		
		case "GET":
			if len(resultSlice) != 2 {
				conn.Write([]byte(protocol.EncodeError("Invalid GET")))
				continue
			} 

			key := resultSlice[1].(string)
			value, exists := s.cache.Get(key)
			if !exists {
				conn.Write([]byte(protocol.EncodeBulkString("", true)))
			} else {
				conn.Write([]byte(protocol.EncodeBulkString(value, false)))
			}

		case "DEL":
			if len(resultSlice) != 2 {
				conn.Write([]byte(protocol.EncodeError("Invalid DEL")))
				continue
			}
			
			key := resultSlice[1].(string)
			s.cache.Delete(key)
			conn.Write([]byte(protocol.EncodeSimpleString("OK")))
		}
	}
}

// stop the test server
func (s *TestServer) Stop() {
	close(s.done)
	s.listener.Close()
	s.aof.Close()
}