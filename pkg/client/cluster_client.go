package client

import (
	"fmt"
	"strings"
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

// fetches cluster topology from seed nodes. 
func (c *ClusterClient) discoverTopology() error {
	var lastErr error

	// try each seed until it succeeds
	for i, seedAddr := range c.seeds {
		fmt.Printf("[CLUSTER CLIENT] Attempting to discover topology from seed %d/%d: %s\n", i+1, len(c.seeds), seedAddr)

		// create a temporary client connection to the seed
		seedClient, err := NewClient(seedAddr)
		if err != nil {
			lastErr = fmt.Errorf("failed to connect to seed %s: %v", seedAddr, err)
			fmt.Printf("[CLUSTER CLIENT] %v\n", lastErr)
			continue
		}

		// send CLUSTER NODES command to get topology information
		// returns with format: "nodeID address status\n" for each node
		response, err := seedClient.SendCommand("CLUSTER", "NODES")
		if err != nil {
			seedClient.Close()
			lastErr = fmt.Errorf("CLUSTER NODES command failed on %s: %v")
			fmt.Printf("[CLUSTER CLIENT] %v\n", lastErr)
			continue // try next seed
		}

		// close the seed connection
		seedClient.Close()

		// parse the response into NodeInfo structures
		err = c.parseClusterNodes(response)
		if err != nil {
			lastErr = fmt.Errorf("failed to parse CLUSTER NODES response: %v", err)
			fmt.Printf("[CLUSTER CLIENT] %v\n", lastErr)
			continue
		}

		// build hash ring from discovered nodes
		c.buildHashRing()

		fmt.Printf("[CLUSTER CLIENT] Successfully discovered %d nodes from %s\n", len(c.nodes), seedAddr)
		return nil
	}

	// if all seeds fail, return the last error encountered
	return fmt.Errorf("failed to discover topology from any seed: %v", lastErr)
}

// parses the response from CLUSTER NODES command
// expected format from server: "nodeID address status\nnodeID address status\n..."
func (c *ClusterClient) parseClusterNodes(response string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// clear existing node information before parsing new data
	c.nodes = make(map[string]NodeInfo)

	// split response into lines, one per node
	lines := strings.Split(strings.TrimSpace(response), "\n")
	if len(lines) == 0 {
		return fmt.Errorf("empty CLUSTER NODES response")
	}

	// parse each line into a NodeInfo structure
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue // skip empty lines
		}

		// expected format: nodeID address status"
		parts := strings.Fields(line)
		if len(parts) < 3 {
			fmt.Printf("[CLUSTER CLIENT] Warning: skipping malformed line: %s\n", line)
			continue
		}

		// extract node information from parsed fields
		nodeInfo := NodeInfo{
			ID: parts[0],
			Address: parts[1],
			Status: parts[2],
		}

		// store node info 
		c.nodes[nodeInfo.ID] = nodeInfo
		fmt.Printf("[CLUSTER CLIENT] Discovered node: %s at %s (%s)\n", nodeInfo.ID, nodeInfo.Address, nodeInfo.Status)
	}

	if len(c.nodes) == 0 {
		return fmt.Errorf("no valid nodes found in CLUSTER NODES response")
	}

	return nil
}

// constructs the hash ring from discovered nodes
func (c *ClusterClient) buildHashRing() {
	c.mu.Lock()
	defer c.mu.Unlock()

	fmt.Printf("[CLUSTER CLIENT] Building hash ring with %d nodes\n", len(c.nodes))

	// add each node to the hash ring
	for nodeID, nodeInfo := range c.nodes {
		// only add active nodes to hash ring
		if nodeInfo.Status != "active" {
			fmt.Printf("[CLUSTER CLIENT] Skipping non-active node %s (%s)\n", nodeID, nodeInfo.Status)
			continue
		}

		// add node to hash ring as a shard
		c.hashRing.AddShard(nodeID)

		// store the node's network address in the hash ring
		c.hashRing.SetNodeAddress(nodeID, nodeInfo.Address)

		fmt.Printf("[CLUSTER CLIENT] Added node %s to hash ring at address %s\n", nodeID, nodeInfo.Address)
	}

	fmt.Printf("[CLUSTER CLIENT] Hash ring built with %d active nodes\n", c.hashRing.GetNodeCount())
}
