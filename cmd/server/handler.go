package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
	"log/slog"

	"github.com/erickim73/gocache/internal/cache"
	"github.com/erickim73/gocache/internal/persistence"
	"github.com/erickim73/gocache/internal/replication"
	"github.com/erickim73/gocache/internal/server"
	"github.com/erickim73/gocache/internal/pubsub"
	"github.com/erickim73/gocache/pkg/protocol"
)

// tracks whether a connection is in subscriber mode
type ConnectionState struct {
	subscriberMode bool // is connection subscribed to any channels
}

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
func handleConnection(conn net.Conn, cache *cache.Cache, aof *persistence.AOF, nodeState *server.NodeState, ps *pubsub.PubSub) {
	defer conn.Close()

	// when connection closes, remove from all pub/sub channels
	defer ps.RemoveConnection(conn)

	// track connections
	cache.GetMetrics().IncrementActiveConnections()
	defer cache.GetMetrics().DecrementActiveConnections()

	// log new connection
	remoteAddr := conn.RemoteAddr().String()

	// track connection state
	state := &ConnectionState{
		subscriberMode: false,
	}

	// read from client
	reader := bufio.NewReader(conn)
	for {
		result, err := protocol.Parse(reader)
		if err != nil {
			if err == io.EOF {
				return
			}
			slog.Warn("Connection error", "address", remoteAddr, "error", err)
			return
		}

		resultSlice, ok := result.([]interface{})
		if !ok {
			slog.Warn("Invalid command format", "address", remoteAddr)
			return
		}
		command := resultSlice[0]

		// start timing request here
		startTime := time.Now()

		// check if connection in subscriber mode
		if state.subscriberMode {
			allowedCommands := map[string]bool{
				"SUBSCRIBE":   true,
				"UNSUBSCRIBE": true,
				"PSUBSCRIBE":  true,  // pattern subscribe 
				"PUNSUBSCRIBE": true, // pattern unsubscribe 
				"PING":        true,  // health check always allowed
				"QUIT":        true,  // graceful disconnect always allowed
			}

			cmdStr, ok := command.(string)
			if !ok || !allowedCommands[cmdStr] {
				// reject non-pub/sub commands in subscriber mode
				conn.Write([]byte(protocol.EncodeError("ERR only (P)SUBSCRIBE / (P)UNSUBSCRIBE / PING / QUIT allowed in this context")))
			}

			// record latency for rejected command
			duration := time.Since(startTime)
			cache.GetMetrics().RecordOperationDuration(duration.Seconds())
			continue
		}

		// handle pub/sub commands
		switch command {
		case "SUBSCRIBE":
			// subscribe enters subscriber mode
			handleSubscribe(conn, resultSlice, ps, state)
			duration := time.Since(startTime)
			cache.GetMetrics().RecordOperationDuration(duration.Seconds())
			continue

		case "UNSUBSCRIBE":
			// unsubscribe may exit subscriber mode if no channels remain
			handleUnsubscribe(conn, resultSlice, ps, state)
			duration := time.Since(startTime)
			cache.GetMetrics().RecordOperationDuration(duration.Seconds())
			continue

		case "PUBLISH":
			// publish can be used in normal mode. allows one connection to publish while others subscribe
			handlePublish(conn, resultSlice, ps)
			duration := time.Since(startTime)
			cache.GetMetrics().RecordOperationDuration(duration.Seconds())
			continue
		}

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
				slog.Debug("Routing redirect - key belongs to different node",
					"key", key,
					"target_node", targetNodeID,
					"target_addr", targetAddr,
					"action", "returning MOVED",
				)

				// record latency for forwarded requests
				duration := time.Since(startTime)
				cache.GetMetrics().RecordOperationDuration(duration.Seconds())
				continue
			}

			// key belongs to this node or we can handle reads, log and continue
			if isWrite {
				slog.Debug("Routing write locally",
					"key", key,
					"reason", "this node is the leader",
				)
			} else {
				slog.Debug("Routing read locally",
					"key", key,
					"reason", "replicated data available",
				)
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
			slog.Error("Replication failed",
				"operation", "SET",
				"key", key,
				"error", err,
			)
		}
	}
	
	// write to aof
	ttlSecondsStr := strconv.FormatInt(ttlSeconds, 10) 
	aofCommand := protocol.EncodeArray([]interface{}{"SET", key, value, ttlSecondsStr})
	err := aof.Append(aofCommand)
	if err != nil {
		slog.Error("AOF write failed",
			"operation", "SET",
			"key", key,
			"error", err,
		)
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
			slog.Error("Error replicating DEL command from leader to follower",
				"error", err,
				"key", key,
			)
		}
	}

	aofCommand := protocol.EncodeArray([]interface{}{"DEL", key})
	err := aof.Append(aofCommand)
	if err != nil {
		slog.Error("Failed to write to AOF",
			"error", err,
			"command", "DEL",
			"key", key,
		)
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