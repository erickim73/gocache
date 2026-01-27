package test

import (
	"bufio"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/erickim73/gocache/internal/cache"
	"github.com/erickim73/gocache/internal/persistence"
	"github.com/erickim73/gocache/internal/replication"
	"github.com/erickim73/gocache/internal/server"
	"github.com/erickim73/gocache/pkg/protocol"
)

type TestServer struct {
	cache *cache.Cache
	aof *persistence.AOF
	listener net.Listener
	port int
	role string
	nodeState *server.NodeState
	done chan bool
}

// startTestLeader starts a leader server for testing
func startTestLeader(t *testing.T, port int) *TestServer {
	// create cache
	myCache, err := cache.New(100)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}

	// create AOF with unique filenames
	aofFile := fmt.Sprintf("test_leader_%d.aof", port)
	snapshotFile := fmt.Sprintf("test_leader_%d.snapshot", port)
	aof, err := persistence.NewAOF(aofFile, snapshotFile, persistence.SyncAlways, myCache, 2)
	if err != nil {
		t.Fatalf("Failed to create AOF: %v", err)
	}

	// create leader
	leader, err := replication.NewLeader(myCache, aof, port+1000) // replication port = client port + 1000
	if err != nil {
		t.Fatalf("Failed to create leader: %v", err)
	}
	go leader.Start()
	
	// create node state for leader
	nodeState, err := server.NewNodeState("leader", leader, "")
	if err != nil {
		t.Fatalf("Failed to create node state for leader: %v", err)
	}


	// create TCP listener for client connections
	address := fmt.Sprintf("localhost:%d", port)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}

	testServer := &TestServer{
		cache:     myCache,
		aof:       aof,
		listener:  listener,
		port:      port,
		role:      "leader",
		nodeState: nodeState,
		done:      make(chan bool),
	}

	// start accepting connections
	go testServer.acceptConnections()

	// give server time to start
	time.Sleep(100 * time.Millisecond)

	return testServer
}

// startTestFollower starts a follower server for testing
func startTestFollower(t *testing.T, port int, leaderAddr string) *TestServer {
	// create cache
	myCache, err := cache.New(100)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}

	// create AOF with unique filenames
	aofFile := fmt.Sprintf("test_follower_%d.aof", port)
	snapshotFile := fmt.Sprintf("test_follower_%d.snapshot", port)
	aof, err := persistence.NewAOF(aofFile, snapshotFile, persistence.SyncAlways, myCache, 2)
	if err != nil {
		t.Fatalf("Failed to create AOF: %v", err)
	}

	// parse leaderAddr to get leader's client port, then compute replication port
	parts := strings.Split(leaderAddr, ":")
	if len(parts) != 2 {
		t.Fatalf("Invalid leader address format: %s", leaderAddr)
	}
	leaderClientPort, err := strconv.Atoi(parts[1])
	if err != nil {
		t.Fatalf("Invalid port in leader address: %v", err)
	}

	// compute replication port (client port + 1000)
	leaderReplPort := leaderClientPort + 1000
	leaderReplAddr := fmt.Sprintf("%s:%d", parts[0], leaderReplPort)


	// create node state for follower
	nodeState, err := server.NewNodeState("follower", nil, leaderAddr)
	if err != nil {
		t.Fatalf("Failed to create node state: %v", err)
	}

	// create follower
	follower, err := replication.NewFollower(
		myCache,
		aof,
		leaderReplAddr,
		"test-follower",
		nil,              // clusterNodes
		1,                // priority
		port+1000,        // replication port
		nodeState,
	)
	if err != nil {
		t.Fatalf("Failed to create follower: %v", err)
	}
	go follower.Start()

	// create TCP listener for client connections
	address := fmt.Sprintf("localhost:%d", port)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}

	testServer := &TestServer{
		cache:     myCache,
		aof:       aof,
		listener:  listener,
		port:      port,
		role:      "follower",
		nodeState: nodeState,
		done:      make(chan bool),
	}

	// start accepting connections
	go testServer.acceptConnections()

	// give server time to start
	time.Sleep(100 * time.Millisecond)

	return testServer
}

// accept connections and handle commands
func (s *TestServer) acceptConnections() {
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
func (s *TestServer) handleConnection(conn net.Conn) {
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

		// check if write operation on follower - redirect to leader
		if s.role == "follower" && (command == "SET" || command == "DEL") {
			redirect := fmt.Sprintf("-MOVED %s\r\n", s.nodeState.GetLeaderAddr())
			conn.Write([]byte(redirect))
			continue
		}

		// handle commands
		switch command {
		case "SET":
			s.handleSet(conn, resultSlice)
		case "GET":
			s.handleGet(conn, resultSlice)
		case "DEL":
			s.handleDelete(conn, resultSlice)
		default:
			conn.Write([]byte(protocol.EncodeError("Unknown command")))
		}
	}
}

// handleSet processes SET commands
func (s *TestServer) handleSet(conn net.Conn, resultSlice []interface{}) {
	if len(resultSlice) < 3 {
		conn.Write([]byte(protocol.EncodeError("Invalid SET")))
		return
	}

	key := resultSlice[1].(string)
	value := resultSlice[2].(string)
	
	err := s.cache.Set(key, value, 0)
	if err != nil {
		conn.Write([]byte(protocol.EncodeError("SET failed")))
		return
	}

	conn.Write([]byte(protocol.EncodeSimpleString("OK")))
}

// handleGet processes GET commands
func (s *TestServer) handleGet(conn net.Conn, resultSlice []interface{}) {
	if len(resultSlice) != 2 {
		conn.Write([]byte(protocol.EncodeError("Invalid GET")))
		return
	}

	key := resultSlice[1].(string)
	value, exists := s.cache.Get(key)

	if !exists {
		conn.Write([]byte(protocol.EncodeBulkString("", true)))
	} else {
		conn.Write([]byte(protocol.EncodeBulkString(value, false)))
	}
}

// handleDelete processes DEL commands
func (s *TestServer) handleDelete(conn net.Conn, resultSlice []interface{}) {
	if len(resultSlice) != 2 {
		conn.Write([]byte(protocol.EncodeError("Invalid DEL")))
		return
	}

	key := resultSlice[1].(string)
	
	err := s.cache.Delete(key)
	if err != nil {
		conn.Write([]byte(protocol.EncodeError("DEL failed")))
		return
	}

	conn.Write([]byte(protocol.EncodeSimpleString("OK")))
}

// Stop stops the test server
func (s *TestServer) Stop() {
	close(s.done)
	s.listener.Close()
	s.aof.Close()
}