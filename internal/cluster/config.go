package cluster

import (
	"fmt"
)


// holds information about all nodes in the cluster
type ClusterConfig struct {
	nodes map[string]string // map of nodeID -> network address
	myNodeID string // node's ID
}

// creates a new cluster configuration
func NewClusterConfig(myNodeID string) *ClusterConfig {
	return &ClusterConfig{
		nodes: make(map[string]string),
		myNodeID: myNodeID,
	}
}

// registers a node in the cluster
func (cc *ClusterConfig) AddNode(nodeID string, address string) {
	cc.nodes[nodeID] = address
}

// returns the address for a given nodeID
func (cc *ClusterConfig) GetNodeAddress(nodeID string) (string, error) {
	addr, exists := cc.nodes[nodeID]
	if !exists {
		return "", fmt.Errorf("node %s not found in cluster", nodeID)
	}
	return addr, nil
}

// returns this node's ID
func (cc *ClusterConfig) GetMyNodeID() string {
	return cc.myNodeID
}

// returns all node IDs in the cluster
func (cc *ClusterConfig) GetAllNodes() []string {
	nodeIDs := make([]string, 0, len(cc.nodes))
	for nodeID := range cc.nodes {
		nodeIDs = append(nodeIDs, nodeID)
	}
	return nodeIDs
}

// checks if a nodeID is this node
func (cc *ClusterConfig) IsLocalNode(nodeID string) bool {
	return nodeID == cc.myNodeID
}