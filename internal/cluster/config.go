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

