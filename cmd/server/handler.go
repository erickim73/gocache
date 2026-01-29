package main

import (
	"bufio"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/erickim73/gocache/internal/cache"
	"github.com/erickim73/gocache/internal/persistence"
	"github.com/erickim73/gocache/pkg/protocol"
	"github.com/erickim73/gocache/internal/replication"
	"github.com/erickim73/gocache/internal/server"
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

	// read from client
	reader := bufio.NewReader(conn)
	for {
		result, err := protocol.Parse(reader)
		if err != nil {
			fmt.Println(err)
			return
		}

		resultSlice, ok := result.([]interface{})
		if !ok {
			fmt.Println("Error: result is not a slice")
			return
		}
		command := resultSlice[0]

		// get current role and leader
		role := nodeState.GetRole()
		leader := nodeState.GetLeader()
		leaderAddr := nodeState.GetLeaderAddr()

		// check if command requires leader
		if requiresLeader(command.(string)) {
			// if node isn't leader, redirect client
			if role != "leader" {
				redirect := fmt.Sprintf(ErrMovedFormat, leaderAddr)
				conn.Write([]byte(redirect))
				continue
			}
		}

		// extract key from command
		var key string
		if len(resultSlice) >= 2 {
			key = resultSlice[1].(string)
		}

		// check if we should handle this key or forward it
		if nodeState.IsClusterMode() && key != "" {
			shouldForward, targetNodeID, targetAddr := nodeState.ShouldForwardRequest(key)
			if shouldForward {
				// key belongs to another node - return MOVED
				msg := fmt.Sprintf("-MOVED %s %s\r\n", targetNodeID, targetAddr)
				conn.Write([]byte(msg))
				fmt.Printf("[ROUTING] key '%s' belongs to %s (%s), returning MOVED\n", key, targetNodeID, targetAddr)
				continue
			}

			// key belongs to node, log and continue
			fmt.Printf("[ROUTING] key '%s' belongs to this node, handling locally\n", key)
		}

		// handle commands
		switch command {
		case "SET":
			handleSet(conn, resultSlice, cache, aof, leader)
		case "GET":
			handleGet(conn, resultSlice, cache)
		case "DEL":
			handleDelete(conn, resultSlice, cache, aof, leader)
		default:
			conn.Write([]byte(protocol.EncodeError("Unknown command " + command.(string))))
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

	// send to followers
	if leader != nil {
		err := leader.Replicate(replication.OpSet, key, value, ttlSeconds)
		if err != nil {
			fmt.Printf("Error replicating SET command from leader to follower: %v\n", err)
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