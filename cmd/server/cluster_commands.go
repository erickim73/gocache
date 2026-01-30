package main

import (
	"fmt"
	"net"
	"time"

	"github.com/erickim73/gocache/internal/cache"
	"github.com/erickim73/gocache/internal/server"
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

	// check if node is already in cluster
	hashRing := nodeState.GetHashRing()
	existingNodes := hashRing.GetAllNodes()
	for _, nodeID := range existingNodes {
		if nodeID == newNodeID {
			conn.Write([]byte(protocol.EncodeError(fmt.Sprintf("Node %s already exists in cluster", newNodeID))))
			return
		}
	}

	// test connection to new node before starting migration
	fmt.Printf("[CLUSTER] Testing connection to %s...\n", newNodeAddr)
	testConn, err := net.DialTimeout("tcp", newNodeAddr, 2 * time.Second)
	if err != nil {
		errorMsg := fmt.Sprintf("Cannot connect to node %s at %s. Make sure the node is running first. Error: %v", newNodeID, newNodeAddr, err)
		conn.Write([]byte(protocol.EncodeError(errorMsg)))
		return
	}
	testConn.Close()
	fmt.Printf("[CLUSTER] Connection test successful\n")

	// get migrator from node state. handles all migration logic
	migrator := nodeState.GetMigrator()
	if migrator == nil {
		conn.Write([]byte(protocol.EncodeError("Cluster not initialized")))
		return
	}

	// trigger the migration process
	err = migrator.MigrateToNewNode(newNodeID, newNodeAddr)
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

	fmt.Printf("[CLUSTER] Removing node %s\n", nodeID)

	// get migrator and hash ring
	migrator := nodeState.GetMigrator()
	if migrator == nil {
		conn.Write([]byte(protocol.EncodeError("Cluster is not initialized")))
		return
	}

	err := migrator.MigrateFromLeavingNode(nodeID)
	if err != nil {
		conn.Write([]byte(protocol.EncodeError(fmt.Sprintf("Migration failed: %v", err))))
		return
	}

	conn.Write([]byte(protocol.EncodeSimpleString("OK")))
}

// lists all nodes in the cluster with their status
func handleClusterNodes(conn net.Conn, nodeState *server.NodeState) {
	// get hash ring to access node information
	hashRing := nodeState.GetHashRing()
	if hashRing == nil {
		conn.Write([]byte(protocol.EncodeError("Cluster is not initialized")))
		return
	}

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