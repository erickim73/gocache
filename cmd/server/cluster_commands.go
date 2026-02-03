package main

import (
	"bufio"
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
	case "TOPOLOGY":
		handleClusterTopology(conn, command, nodeState)
	case "HEALTH":
		handleClusterHealth(conn, nodeState)
	default:
		conn.Write([]byte(protocol.EncodeError("Unknown CLUSTER subcommand")))
	}
}


func handleClusterTopology(conn net.Conn, command []interface{}, nodeState *server.NodeState) {
	if len(command) < 5 {
		conn.Write([]byte(protocol.EncodeError("TOPOLOGY requires operation, nodeID, and address")))
		return
	}

	operation := command[2].(string)
	targetNodeID := command[3].(string)
	targetNodeAddr := command[4].(string)

	hashRing := nodeState.GetHashRing()

	switch operation {
	case "ADD":
		fmt.Printf("[CLUSTER] Received topology update: ADD %s at %s\n", targetNodeID, targetNodeAddr)
		hashRing.AddShard(targetNodeID)
		hashRing.SetNodeAddress(targetNodeID, targetNodeAddr)
		fmt.Printf("[CLUSTER] Updated local hash ring\n")
	case "REMOVE":
		fmt.Printf("[CLUSTER] Received topology update: REMOVE %s\n", targetNodeID)
		hashRing.RemoveNode(targetNodeID)
		hashRing.SetNodeAddress(targetNodeID, "")
		fmt.Printf("[CLUSTER] Updated local hash ring\n")
	default:
		conn.Write([]byte(protocol.EncodeError("Unknown TOPOLOGY operation")))
		return
	}

	conn.Write([]byte(protocol.EncodeSimpleString("OK")))
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

	// broadcast to all other nodes in cluster
	config := nodeState.GetConfig()

	fmt.Printf("[CLUSTER] Broadcasting node addition to other nodes...\n")
	for _, node := range config.Nodes {
		if node.ID == config.NodeID {
			continue // skip self
		}

		// connect to other node and tell it to add node4
		nodeAddr := fmt.Sprintf("%s:%d", node.Host, node.Port)
		fmt.Printf("[CLUSTER] Notifying %s at %s\n", node.ID, nodeAddr)

		err := notifyNodeAboutTopologyChange(nodeAddr, "ADD", newNodeID, newNodeAddr)
		if err != nil {
			fmt.Printf("[CLUSTER] Warning: Failed to notify %s: %v\n", node.ID, err)
			// continue anyway, not fatal
		}
	}

	// also notify the new node to add itself
	fmt.Printf("[CLUSTER] Notifying new node %s to add itself\n", newNodeID)
	err = notifyNodeAboutTopologyChange(newNodeAddr, "ADD", newNodeID, newNodeAddr)
	if err != nil {
		fmt.Printf("[CLUSTER] Warning: Failed to notify new node: %v\n", err)
	}

	fmt.Printf("[CLUSTER] Topology broadcast complete\n")
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

	// check if node is already in cluster
	hashRing := nodeState.GetHashRing()
	existingNodes := hashRing.GetAllNodes()
	nodeExists := false
	for _, existingNodeID := range existingNodes {
		if existingNodeID== nodeID {
			nodeExists = true
			break
		}
	}

	if !nodeExists {
		conn.Write([]byte(protocol.EncodeError(fmt.Sprintf("Node %s not found in cluster", nodeID))))
		return
	}

	// get migrator and hash ring
	migrator := nodeState.GetMigrator()
	if migrator == nil {
		conn.Write([]byte(protocol.EncodeError("Cluster is not initialized")))
		return
	}

	// trigger the migration process
	err := migrator.MigrateFromLeavingNode(nodeID)
	if err != nil {
		conn.Write([]byte(protocol.EncodeError(fmt.Sprintf("Migration failed: %v", err))))
		return
	}

	// broadcast to all other nodes in cluster
	config := nodeState.GetConfig()

	fmt.Printf("[CLUSTER] Broadcasting node removal to other nodes...\n")
	for _, node := range config.Nodes {
		if node.ID == config.NodeID {
			continue // skip self
		}

		// notify other node to remove the leaving node
		nodeAddr := fmt.Sprintf("%s:%d", node.Host, node.Port)
		fmt.Printf("[CLUSTER] Notifying %s at %s to remove %s\n", node.ID, nodeAddr, nodeID)

		err := notifyNodeAboutTopologyChange(nodeAddr, "REMOVE", nodeID, "")
		if err != nil {
			fmt.Printf("[CLUSTER] Warning: Failed to notify %s: %v\n", node.ID, err)
			// continue anyway, not fatal
		}
	}

	// also notify leaving node to remove itself from its own hash ring
	leavingNodeAddr := hashRing.GetNodeAddress(nodeID)
	if leavingNodeAddr != "" {
		fmt.Printf("[CLUSTER] Notifying leaving node %s to remove itself\n", nodeID)
		err = notifyNodeAboutTopologyChange(leavingNodeAddr, "REMOVE", nodeID, "")
		if err != nil {
			fmt.Printf("[CLUSTER] Warning: Failed to notify leaving node (may already b e down): %v\n", err)
		}
	}

	fmt.Printf("[CLUSTER] Topology broadcast complete\n")
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

// helper function to notify other nodes
func notifyNodeAboutTopologyChange(nodeAddr string, operation string, targetNodeID string, targetNodeAddr string) error {
	conn, err := net.DialTimeout("tcp", nodeAddr, 2 * time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	// send cluster topology update command
	cmd := protocol.EncodeArray([]interface{}{"CLUSTER", "TOPOLOGY", operation, targetNodeID, targetNodeAddr})
	_, err = conn.Write([]byte(cmd))
	if err != nil {
		return err
	}

	// read response
	reader := bufio.NewReader(conn)
	_, err = protocol.Parse(reader)
	return err
}

func handleClusterHealth(conn net.Conn, nodeState *server.NodeState) {
	healthChecker := nodeState.GetHealthChecker()
	if healthChecker == nil {
		conn.Write([]byte(protocol.EncodeError("Health checker not initialized")))
		return
	}

	// get all node health statuses
	allStatuses := healthChecker.GetAllNodeStatus()

	// build response
	response := ""
	for nodeID, health := range allStatuses {
		response += fmt.Sprintf("%s %s %s\n", nodeID, health.Address, health.Status.String())
	}

	conn.Write([]byte(protocol.EncodeBulkString(response, false)))
}