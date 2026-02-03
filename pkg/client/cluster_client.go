package client

import (
	"fmt"
	"sync"

	"github.com/erickim73/gocache/internal/cluster"
)

// represents a node in the cluster. contains all the information returned by cluster nodes commands
type NodeInfo struct {
	ID string // node identifier
	Address string // network address in format "host:port"
	Status string // current status: "active", "suspected", or "dead"
}

// smart client that routes requests to the correct node. maintains local copy of the cluster topology and hash ring to make intelligent routing decisions
type ClusterClient struct {
	seeds []string // initial seed addresses to discover the cluster
	hashRing *cluster.HashRing // consistent hash ring for determining which node owns which keys
	conns map[string]*Client // map from node address to active connection
	nodes map[string]NodeInfo // map from node ID to node information (discovered via CLUSTER NODES)
	mu sync.RWMutex // protects concurrent access to hashRing, conns, and nodes maps
	stopCh chan struct{} // channel to signal topology refresh goroutine to stop
	refreshWg sync.WaitGroup // WaitGroup to track topology refresh goroutine
}

// creates a new cluster-aware client. takes a list of seed addresses to bootstrap cluster discovery
func NewClusterClient(seeds []string) (*ClusterClient, error) {
	// validate input
	if len(seeds) == 0 {
		return nil, fmt.Errorf("at least one seed address is required")
	}

	// initialize the cluster client with empty state
	c := &ClusterClient{
		seeds: seeds,
		hashRing: cluster.NewHashRing(150),
		conns: make(map[string]*Client), // start with no connections, create them lazily
		nodes: make(map[string]NodeInfo),
		stopCh: make(chan struct{}),
	}
	
	// immediately discover topology on initialization
	fmt.Printf("[CLUSTER CLIENT] Discovering cluster topology from seeds: %v\n", seeds)
	err := c.discoverTopology()
	if err != nil {
		return nil, fmt.Errorf("failed to discover cluster topology: %v", err)
	}

	fmt.Printf("[CLUSTER CLIENT] Successfully initialized with %d nodes\n", len(c.nodes))
	return c, nil
}
