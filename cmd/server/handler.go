package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/erickim73/gocache/internal/cache"
	"github.com/erickim73/gocache/internal/persistence"
	"github.com/erickim73/gocache/internal/replication"
	"github.com/erickim73/gocache/internal/server"
	"github.com/erickim73/gocache/pkg/protocol"
)


const (
	// redis-style redirect error
	ErrMovedFormat = "-MOVED %s\r\n"
)

// returns true if the operation must be handled by the leader
func requiresLeader(operation string) bool {
	switch operation {
	case "SET", "DEL":
		return true // write operations need leader
	case "GET":
		return false // reads can be served by followers
	default:
		return true // unknown operations go to leader for safety
	}
}

// handle client commands and write to aof
func handleConnection(conn net.Conn, cache *cache.Cache, aof *persistence.AOF, nodeState *server.NodeState) {
	defer conn.Close()

	// increment active connections when client connects
	cache.GetMetrics().IncrementActiveConnections()
	// decrement when client disconnects
	defer cache.GetMetrics().DecrementActiveConnections()

	// read from client
	reader := bufio.NewReader(conn)
	for {
		result, err := protocol.Parse(reader)
		if err != nil {
			if err == io.EOF {
				return
			}
			fmt.Printf("Connection error: %v\n", err)
			return
		}

		resultSlice, ok := result.([]interface{})
		if !ok {
			fmt.Println("Error: result is not a slice")
			return
		}
		command := resultSlice[0]

		// start timing request here
		startTime := time.Now()

		// handle CLUSTER commands first
		if command == "CLUSTER" {
			handleClusterCommand(conn, resultSlice, cache, nodeState)
			// record latency for cluster commands
			duration := time.Since(startTime)
			cache.GetMetrics().RecordOperationDuration(duration.Seconds())
			continue // skip forward check
		}

		// extract key from command
		var key string
		if len(resultSlice) >= 2 {
			key = resultSlice[1].(string)
		}

		// determine if this is a write operation
		isWrite := (command == "SET" || command == "DEL")

		// check if we should handle this key or forward it
		if nodeState.IsClusterMode() && key != "" {
			shouldForward, targetNodeID, targetAddr := nodeState.ShouldForwardRequest(key, isWrite)
			if shouldForward {
				// key belongs to another node - return MOVED
				msg := fmt.Sprintf("-MOVED %s %s\r\n", targetNodeID, targetAddr)
				conn.Write([]byte(msg))
				fmt.Printf("[ROUTING] key '%s' belongs to %s (%s), returning MOVED\n", key, targetNodeID, targetAddr)

				// record latency for forwarded requests
				duration := time.Since(startTime)
				cache.GetMetrics().RecordOperationDuration(duration.Seconds())
				continue
			}

			// key belongs to this node or we can handle reads, log and continue
			if isWrite {
				fmt.Printf("[ROUTING] write for key '%s' - handling locally (I'm the leader)\n", key)
			} else {
				fmt.Printf("[ROUTING] read for key '%s' - handling locally (replicated data)\n", key)
			}
		}

		// handle commands
		switch command {
		case "SET":
			// handleSet(conn, resultSlice, cache, aof, leader)
			leader := nodeState.GetLeader()
			handleSet(conn, resultSlice, cache, aof, leader)
			// record latency after set completes
			duration := time.Since(startTime)
			cache.GetMetrics().RecordOperationDuration(duration.Seconds())

		case "GET":
			handleGet(conn, resultSlice, cache)
			// record latency after get completes
			duration := time.Since(startTime)
			cache.GetMetrics().RecordOperationDuration(duration.Seconds())

		case "DEL":
			// handleDelete(conn, resultSlice, cache, aof, leader)
			leader := nodeState.GetLeader()
			handleDelete(conn, resultSlice, cache, aof, leader)
			// record latency after delete completes
			duration := time.Since(startTime)
			cache.GetMetrics().RecordOperationDuration(duration.Seconds())

		case "DBSIZE":
			handleDBSize(conn, cache)
			// record latency for dbsize
			duration := time.Since(startTime)
			cache.GetMetrics().RecordOperationDuration(duration.Seconds())

		case "PING":
			handlePing(conn)
			// record latency for ping
			duration := time.Since(startTime)
			cache.GetMetrics().RecordOperationDuration(duration.Seconds())

		case "CLUSTER":
			handleClusterCommand(conn, resultSlice, cache, nodeState)
			// record latency for cluster
			duration := time.Since(startTime)
			cache.GetMetrics().RecordOperationDuration(duration.Seconds())

		default:
			conn.Write([]byte(protocol.EncodeError("Unknown command " + command.(string))))
			// record latency for unknown commands
			duration := time.Since(startTime)
			cache.GetMetrics().RecordOperationDuration(duration.Seconds())
		}
	}
}

func handleSet(conn net.Conn, resultSlice []interface{}, cache *cache.Cache, aof *persistence.AOF, leader *replication.Leader) {
	if len(resultSlice) < 3 || len(resultSlice) > 4 {
		conn.Write([]byte(protocol.EncodeError("Length of command doesn't match")))
		return
	}

	key := resultSlice[1].(string)
	value := resultSlice[2].(string)

	ttl := time.Duration(0)

	// if ttl provided as a 4th argument
	if len(resultSlice) == 4 {
		seconds := resultSlice[3].(string)
		ttlSec, err := strconv.Atoi(seconds)
		if err != nil {
			conn.Write([]byte(protocol.EncodeError("Couldn't convert seconds to a string")))
			return
		}
		ttl = time.Duration(ttlSec) * time.Second
	}

	cache.Set(key, value, ttl)

	ttlSeconds := int64(ttl.Seconds()) // 0 if no TTL

	// only replicate if we have a leader (non-cluster mode)
	if leader != nil {
		err := leader.Replicate(replication.OpSet, key, value, ttlSeconds)
		if err != nil {
			fmt.Printf("Error replicating SET command: %v\n", err)
		}
	}
	
	// write to aof
	ttlSecondsStr := strconv.FormatInt(ttlSeconds, 10) 
	aofCommand := protocol.EncodeArray([]interface{}{"SET", key, value, ttlSecondsStr})
	err := aof.Append(aofCommand)
	if err != nil {
		fmt.Printf("Failed to write to AOF: %v\n", err)
	}

	conn.Write([]byte(protocol.EncodeSimpleString("OK")))
}

func handleGet(conn net.Conn, resultSlice []interface{}, cache *cache.Cache) {
	if len(resultSlice) != 2 {
		conn.Write([]byte(protocol.EncodeError("Length of command doesn't match")))
		return
	}

	key := resultSlice[1].(string)

	result, exists := cache.Get(key)

	if !exists {
		conn.Write([]byte(protocol.EncodeBulkString("", true)))
	} else {
		conn.Write([]byte(protocol.EncodeBulkString(result, false)))
	}
}

func handleDelete(conn net.Conn, resultSlice []interface{}, cache *cache.Cache, aof *persistence.AOF, leader *replication.Leader) {
	if len(resultSlice) != 2 {
		conn.Write([]byte(protocol.EncodeError("Length of command doesn't match")))
		return
	}

	key := resultSlice[1].(string)

	cache.Delete(key)

	// send to followers
	if leader != nil {
		err := leader.Replicate(replication.OpDelete, key, "", 0)
		if err != nil {
			fmt.Printf("Error replicating DEL command from leader to follower: %v\n", err)
		}
	}

	aofCommand := protocol.EncodeArray([]interface{}{"DEL", key})
	err := aof.Append(aofCommand)
	if err != nil {
		fmt.Printf("Failed to write to AOF: %v\n", err)
	}

	conn.Write([]byte(protocol.EncodeSimpleString("OK")))
}

// returns the number of keys in the cache
func handleDBSize(conn net.Conn, cache *cache.Cache) {
	// get all keys
	keys := cache.GetAllKeys()
	count := len(keys)

	// encode as integer
	response := fmt.Sprintf(":%d\r\n", count)
	conn.Write([]byte(response))
}

// responds to PING with PONG
func handlePing(conn net.Conn) {
	conn.Write([]byte(protocol.EncodeSimpleString("PONG")))
}