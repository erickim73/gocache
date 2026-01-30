package main

import (
	"fmt"
	"net"

	"github.com/erickim73/gocache/internal/cache"
	"github.com/erickim73/gocache/internal/server"
	"github.com/erickim73/gocache/internal/cluster"
	"github.com/erickim73/gocache/pkg/protocol"
)

// routes CLUSTER subcommands to appropriate handles
func handleClusterCommand(conn net.Conn, command []interface{}, cache *cache.Cache, nodeState *server.NodeState) {
	if len(command) < 2 {
		conn.Write([]byte(protocol.EncodeError("CLUSTER command requires subcommand")))
		return
	}

	// extract and parse subcommand (ADDNODE, REMOVENODE, NODES)
	subcommand := command[1].(string)

	// route to specific handles based on subcommand
	switch subcommand {
	case "ADDNODE":
		handleClusterAddNode(conn, command, cache, nodeState)
	case "REMOVENODE":
		handleClusterRemoveNode(conn, command, cache, nodeState)
	case "NODES":
		handleClusterNodes(conn, nodeState)
	default:
		conn.Write([]byte(protocol.EncodeError("Unknown CLUSTER subcommand")))
	}
}

// adds a new node to the cluster and triggers data migration
// command format: CLUSTER ADDNODE <nodeID> <address>
func handleClusterAddNode(conn net.Conn, command []interface{}, cache *cache.Cache, nodeState *server.NodeState) {
	if len(command) < 4 {
		conn.Write([]byte(protocol.EncodeError("ADDNODE requires nodeID and address")))
		return
	}

	// extract new node's ID and network address
	newNodeID := command[2].(string)
	newNodeAddr := command[3].(string)

	fmt.Printf("[CLUSTER] Adding node %s at %s\n", newNodeID, newNodeAddr)

	// get migrator from node state. handles all migration logic
	migrator := nodeState.GetMigrator()

	// trigger the migration process
	err := migrator.MigrateToNewNode(newNodeID, newNodeAddr)
	if err != nil {
		// if migration fails, return error to client
		conn.Write([]byte(protocol.EncodeError(fmt.Sprintf("Migration failed: %v", err))))
		return
	}

	conn.Write([]byte(protocol.EncodeSimpleString("OK")))
}

// removes a node from the cluster
func handleClusterRemoveNode(conn net.Conn, command []interface{}, cache *cache.Cache, nodeState *server.NodeState) {
	if len(command) < 3 {
		conn.Write([]byte(protocol.EncodeError("REMOVENODE requires nodeID")))
		return
	}

	nodeID := command[2].(string)

	// get migrator and hash ring
	migrator := nodeState.GetMigrator()
	hashRing := nodeState.GetHashRing()

	fmt.Printf("[CLUSTER] Removing node %s\n", nodeID)

	// calculate which keys need to migrate away from the dying node
	tasks := hashRing.CalculateMigrationsForRemoval(nodeID)

	// execute each migration task
	for _, task := range tasks {
		fmt.Printf("[CLUSTER] Migrating keys from %s to %s\n", task.FromNode, task.ToNode)

		// get target node's address
		targetAddr := hashRing.GetNodeAddress(task.ToNode)
		
		// get keys in this hash range
		keys := cache.GetKeysInHashRange(task.StartHash, task.EndHash, hashRing.Hash)

		if len(keys) == 0 {
			continue
		}

		// get values for those keys
		values := make(map[string]string)
		for _, key := range keys {
			value, exists := cache.Get(key)
			if exists {
				values[key] = value
			}
		}

		// transfer keys to target node
		err := cluster.TransferKeys(targetAddr, keys, values)
		if err != nil {
			conn.Write([]byte(protocol.EncodeError(fmt.Sprintf("Transfer failed: %v", err))))
			return
		}

		// delete keys from local cache after successful transfer
		for _, key := range keys {
			cache.Delete(key)
		}
	}

	// remove node from hash ring
	hashRing.RemoveNode(nodeID)

	conn.Write([]byte(protocol.EncodeSimpleString("OK")))
}

// lists all nodes in the cluster with their status
func handleClusterNodes(conn net.Conn, nodeState *server.NodeState) {
	// get hash ring to access node information
	hashRing := nodeState.GetHashRing()

	// get list of all node IDs
	nodes := hashRing.GetAllNodes()

	// build response string with node information
	response := ""
	for _, nodeID := range nodes {
		addr := hashRing.GetNodeAddress(nodeID)
		response += fmt.Sprintf("%s %s active\n", nodeID, addr)
	}

	conn.Write([]byte(protocol.EncodeBulkString(response, false)))
}