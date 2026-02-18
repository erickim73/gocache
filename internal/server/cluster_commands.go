package server

import (
	"bufio"
	"fmt"
	"net"
	"time"
	"log/slog"

	"github.com/erickim73/gocache/internal/cache"
	"github.com/erickim73/gocache/pkg/protocol"
)

// routes CLUSTER subcommands to appropriate handles
func handleClusterCommand(conn net.Conn, command []interface{}, cache *cache.Cache, nodeState *NodeState) {
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


func handleClusterTopology(conn net.Conn, command []interface{}, nodeState *NodeState) {
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
		slog.Info("Topology update received",
			"operation", "ADD",
			"node_id", targetNodeID,
			"address", targetNodeAddr,
		)
		hashRing.AddShard(targetNodeID)
		hashRing.SetNodeAddress(targetNodeID, targetNodeAddr)
		
		slog.Info("Hash ring updated", "operation", "ADD", "node_id", targetNodeID)

	case "REMOVE":
		slog.Info("Topology update received",
			"operation", "REMOVE",
			"node_id", targetNodeID,
		)
		hashRing.RemoveShard(targetNodeID)
		hashRing.SetNodeAddress(targetNodeID, "")
		slog.Info("Hash ring updated", "operation", "REMOVE", "node_id", targetNodeID)
	default:
		slog.Warn("Unknown topology operation", "operation", operation)
		conn.Write([]byte(protocol.EncodeError("Unknown TOPOLOGY operation")))
		return
	}

	conn.Write([]byte(protocol.EncodeSimpleString("OK")))
}

// adds a new node to the cluster and triggers data migration
// command format: CLUSTER ADDNODE <nodeID> <address>
func handleClusterAddNode(conn net.Conn, command []interface{}, cache *cache.Cache, nodeState *NodeState) {
	if len(command) < 4 {
		conn.Write([]byte(protocol.EncodeError("ADDNODE requires nodeID and address")))
		return
	}

	// extract new node's ID and network address
	newNodeID := command[2].(string)
	newNodeAddr := command[3].(string)

	slog.Info("Adding node to cluster",
		"node_id", newNodeID,
		"address", newNodeAddr,
	)

	// check if node is already in cluster
	hashRing := nodeState.GetHashRing()
	existingNodes := hashRing.GetAllNodes()
	for _, nodeID := range existingNodes {
		if nodeID == newNodeID {
			slog.Warn("Node already exists in cluster", "node_id", newNodeID)
			conn.Write([]byte(protocol.EncodeError(fmt.Sprintf("Node %s already exists in cluster", newNodeID))))
			return
		}
	}

	// test connection to new node before starting migration
	slog.Info("Testing connection to new node",
		"node_id", newNodeID,
		"address", newNodeAddr,
	)
	testConn, err := net.DialTimeout("tcp", newNodeAddr, 2 * time.Second)
	if err != nil {
		slog.Error("Connection test failed",
			"node_id", newNodeID,
			"address", newNodeAddr,
			"error", err,
		)
		
		errorMsg := fmt.Sprintf("Cannot connect to node %s at %s. Make sure the node is running first. Error: %v", newNodeID, newNodeAddr, err)
		conn.Write([]byte(protocol.EncodeError(errorMsg)))
		return
	}
	testConn.Close()
	slog.Info("Connection test successful", "node_id", newNodeID)

	// get migrator from node state. handles all migration logic
	migrator := nodeState.GetMigrator()
	if migrator == nil {
		slog.Error("Migrator not initialized")
		conn.Write([]byte(protocol.EncodeError("Cluster not initialized")))
		return
	}

	// trigger the migration process
	slog.Info("Starting data migration", "target_node", newNodeID)

	err = migrator.MigrateToNewNode(newNodeID, newNodeAddr)
	if err != nil {
		slog.Error("Migration failed",
			"target_node", newNodeID,
			"error", err,
		)
		
		// if migration fails, return error to client
		conn.Write([]byte(protocol.EncodeError(fmt.Sprintf("Migration failed: %v", err))))
		return
	}

	// log migration
	slog.Info("Data migration completed", "target_node", newNodeID)

	// broadcast to all other nodes in cluster
	config := nodeState.GetConfig()

	slog.Info("Broadcasting topology change to cluster", "operation", "ADD", "node_id", newNodeID)
	for _, node := range config.Nodes {
		if node.ID == config.NodeID {
			continue // skip self
		}

		// connect to other node and tell it to add node4
		nodeAddr := fmt.Sprintf("%s:%d", node.Host, node.Port)
		slog.Debug("Notifying node about addition",
			"target_node", node.ID,
			"target_address", nodeAddr,
			"new_node", newNodeID,
		)

		err := notifyNodeAboutTopologyChange(nodeAddr, "ADD", newNodeID, newNodeAddr)
		if err != nil {
			slog.Warn("Failed to notify node",
				"target_node", node.ID,
				"error", err,
			)
			// continue anyway, not fatal
		}
	}

	// also notify the new node to add itself
	slog.Debug("Notifying new node to update its topology", "node_id", newNodeID)
	err = notifyNodeAboutTopologyChange(newNodeAddr, "ADD", newNodeID, newNodeAddr)
	if err != nil {
		slog.Warn("Failed to notify new node",
			"node_id", newNodeID,
			"error", err,
		)
	}

	slog.Info("Node addition completed", "node_id", newNodeID)
	conn.Write([]byte(protocol.EncodeSimpleString("OK")))
}

// removes a node from the cluster
func handleClusterRemoveNode(conn net.Conn, command []interface{}, cache *cache.Cache, nodeState *NodeState) {
	if len(command) < 3 {
		conn.Write([]byte(protocol.EncodeError("REMOVENODE requires nodeID")))
		return
	}

	nodeID := command[2].(string)

	slog.Info("Removing node from cluster", "node_id", nodeID)

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
		slog.Info("Removing node from cluster", "node_id", nodeID)
		return
	}

	// get migrator and hash ring
	migrator := nodeState.GetMigrator()
	if migrator == nil {
		slog.Error("Migrator not initialized")
		conn.Write([]byte(protocol.EncodeError("Cluster is not initialized")))
		return
	}

	// trigger the migration process
	slog.Info("Starting data migration from leaving node", "node_id", nodeID)

	err := migrator.MigrateFromLeavingNode(nodeID)
	if err != nil {
		slog.Error("Migration failed",
			"leaving_node", nodeID,
			"error", err,
		)
		conn.Write([]byte(protocol.EncodeError(fmt.Sprintf("Migration failed: %v", err))))
		return
	}

	// log migration 
	slog.Info("Data migration completed", "leaving_node", nodeID)

	// broadcast to all other nodes in cluster
	config := nodeState.GetConfig()

	slog.Info("Broadcasting topology change to cluster", "operation", "REMOVE", "node_id", nodeID)

	for _, node := range config.Nodes {
		if node.ID == config.NodeID {
			continue // skip self
		}

		// notify other node to remove the leaving node
		nodeAddr := fmt.Sprintf("%s:%d", node.Host, node.Port)
		slog.Debug("Notifying node about removal",
			"target_node", node.ID,
			"target_address", nodeAddr,
			"leaving_node", nodeID,
		)

		err := notifyNodeAboutTopologyChange(nodeAddr, "REMOVE", nodeID, "")
		if err != nil {
			slog.Warn("Failed to notify node",
				"target_node", node.ID,
				"error", err,
			)
			// continue anyway, not fatal
		}
	}

	// also notify leaving node to remove itself from its own hash ring
	leavingNodeAddr := hashRing.GetNodeAddress(nodeID)
	if leavingNodeAddr != "" {
		slog.Debug("Notifying leaving node to update its topology", "node_id", nodeID)
		err = notifyNodeAboutTopologyChange(leavingNodeAddr, "REMOVE", nodeID, "")
		if err != nil {
			slog.Warn("Failed to notify leaving node (may be down)",
				"node_id", nodeID,
				"error", err,
			)
		}
	}

	slog.Info("Node removal completed", "node_id", nodeID)
	conn.Write([]byte(protocol.EncodeSimpleString("OK")))
}

// lists all nodes in the cluster with their status
func handleClusterNodes(conn net.Conn, nodeState *NodeState) {
	// get config to access individual node information
	config := nodeState.GetConfig()
	if config == nil {
		conn.Write([]byte(protocol.EncodeError("Cluster is not initialized")))
		return
	}

	// get health checker to determine node status
	healthChecker := nodeState.GetHealthChecker()

	// build response string with node information
	response := ""
	for _, node := range config.Nodes {
		// build full address 
		nodeAddr := fmt.Sprintf("%s:%d", node.Host, node.Port)

		// determine node status from health checker 
		status := "active"
		if healthChecker != nil {
			nodeStatus := healthChecker.GetNodeStatus(node.ID)
			status = nodeStatus.String() // "alive", "suspected", or "dead"
		}

		// return individual node information in format: "nodeID address status"
		response += fmt.Sprintf("%s %s %s\n", node.ID, nodeAddr, status)
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

func handleClusterHealth(conn net.Conn, nodeState *NodeState) {
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