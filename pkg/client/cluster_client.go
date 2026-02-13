package client

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

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
	slog.Info("Discovering cluster topology from seeds", "seeds", seeds)
	err := c.discoverTopology()
	if err != nil {
		slog.Error("Failed to discover cluster topology", "error", err)
		return nil, fmt.Errorf("failed to discover cluster topology: %v", err)
	}

	slog.Info("Successfully initialized cluster client", "node_count", len(c.nodes))
	return c, nil
}

// fetches cluster topology from seed nodes. 
func (c *ClusterClient) discoverTopology() error {
	var lastErr error

	// try each seed until it succeeds
	for i, seedAddr := range c.seeds {
		slog.Debug("Attempting to discover topology from seed", "seed_index", i+1, "total_seeds", len(c.seeds), "seed_address", seedAddr)

		// create a temporary client connection to the seed
		seedClient, err := NewClient(seedAddr)
		if err != nil {
			lastErr = fmt.Errorf("failed to connect to seed %s: %v", seedAddr, err)
			slog.Warn("Failed to connect to seed", "seed_address", seedAddr, "error", err)
			continue
		}

		// send CLUSTER NODES command to get topology information
		// returns with format: "nodeID address status\n" for each node
		response, err := seedClient.SendCommand("CLUSTER", "NODES")
		if err != nil {
			seedClient.Close()
			lastErr = fmt.Errorf("CLUSTER NODES command failed on %s: %v", seedAddr, err)
			slog.Warn("CLUSTER NODES command failed", "seed_address", seedAddr, "error", err)
			continue // try next seed
		}

		// close the seed connection
		seedClient.Close()

		// parse the response into NodeInfo structures
		err = c.parseClusterNodes(response)
		if err != nil {
			lastErr = fmt.Errorf("failed to parse CLUSTER NODES response: %v", err)
			slog.Warn("Failed to parse CLUSTER NODES response", "error", err)
			continue
		}

		// build hash ring from discovered nodes
		c.buildHashRing()

		slog.Info("Successfully discovered nodes from seed", "node_count", len(c.nodes), "seed_address", seedAddr)
		return nil
	}

	// if all seeds fail, return the last error encountered
	slog.Error("Failed to discover topology from any seed", "error", lastErr)
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
			slog.Warn("Skipping malformed CLUSTER NODES line", "line", line)
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
		slog.Debug("Discovered node", "node_id", nodeInfo.ID, "address", nodeInfo.Address, "status", nodeInfo.Status)
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

	slog.Info("Building hash ring", "node_count", len(c.nodes))

	// add each node to the hash ring
	for nodeID, nodeInfo := range c.nodes {
		// only add active nodes to hash ring
		if nodeInfo.Status != "active" {
			slog.Debug("Skipping non-active node", "node_id", nodeID, "status", nodeInfo.Status)
			continue
		}

		// add node to hash ring as a shard
		c.hashRing.AddShard(nodeID)

		// store the node's network address in the hash ring
		c.hashRing.SetNodeAddress(nodeID, nodeInfo.Address)

		slog.Debug("Added node to hash ring", "node_id", nodeID, "address", nodeInfo.Address)
	}

	slog.Info("Hash ring built", "active_node_count", c.hashRing.GetNodeCount())
}

// retrieves a value from the cluster
func (c *ClusterClient) Get(key string) (string, error) {
	// find which node owns this key
	c.mu.RLock()
	nodeID, err := c.hashRing.GetNode(key)
	c.mu.RUnlock()

	if err != nil {
		slog.Error("Failed to find node for key", "key", key, "error", err)
		return "", fmt.Errorf("failed to find node for key %s: %v", key, err)
	}

	// look up the network address for this node
	c.mu.RLock()
	address := c.hashRing.GetNodeAddress(nodeID)
	c.mu.RUnlock()

	if address == "" {
		slog.Error("No address found for node", "node_id", nodeID)
		return "", fmt.Errorf("no address found for node %s", nodeID)
	}

	slog.Debug("Routing GET request", "key", key, "node_id", nodeID, "address", address)

	// get a connection to that node
	conn, err := c.getOrCreateConnection(address)
	if err != nil {
		return "", err
	}

	// send the GET command to that node using existing client
	result, err := conn.Get(key)
	if err != nil {
		// handle MOVED responses - means cluster topology changed
		if strings.HasPrefix(err.Error(), "MOVED") {
			slog.Info("Received MOVED response, refreshing topology", "key", key)
			
			// refresh our view of the cluster
			refreshErr := c.discoverTopology()
			if refreshErr != nil {
				slog.Error("Failed to refresh topology after MOVED", "error", refreshErr)
				return "", fmt.Errorf("failed to refresh topology: %v", refreshErr)
			}

			// retry request with updated topology
			return c.Get(key)
		}
		return "", err
	}
	return result, nil
}

// gets an existing connection or creates a new one
func (c *ClusterClient) getOrCreateConnection(address string) (*Client, error) {
	// check if we already have a connection
	c.mu.RLock()
	conn, exists := c.conns[address]
	if exists {
		c.mu.RUnlock()
		return conn, nil
	}
	c.mu.RUnlock()

	// no existing connection, acquire write lock to create one
	c.mu.Lock()
	defer c.mu.Unlock()

	// double-check after acquiring write lock
	if conn, exists := c.conns[address]; exists {
		return conn, nil
	}

	// create new connection to node
	slog.Debug("Creating new connection to node", "address", address)
	conn, err := NewClient(address)
	if err != nil {
		slog.Error("Failed to create connection", "address", address, "error", err)
		return nil, fmt.Errorf("failed to connect to %s: %v", address, err)
	}

	// store connection in map for reuse
	c.conns[address] = conn
	return conn, nil
}

// stores a value in the cluster
func (c *ClusterClient) Set(key string, value string) error {
	// find which node owns this key
	c.mu.RLock()
	nodeID, err := c.hashRing.GetNode(key)
	c.mu.RUnlock()

	if err != nil {
		slog.Error("Failed to find node for key", "key", key, "error", err)
		return fmt.Errorf("failed to find node for key %s: %v", key, err)
	}

	// look up the network address for this node
	c.mu.RLock()
	address := c.hashRing.GetNodeAddress(nodeID)
	c.mu.RUnlock()

	if address == "" {
		slog.Error("No address found for node", "node_id", nodeID)
		return fmt.Errorf("no address found for node %s", nodeID)
	}
	
	slog.Debug("Routing SET request", "key", key, "node_id", nodeID, "address", address)

	// get or create a connection to the target node
	conn, err := c.getOrCreateConnection(address)
	if err != nil {
		return err
	}

	// execute set command on the target node
	err = conn.Set(key, value)
	if err != nil {
		// handle MOVED responses by refreshing topology and refreshing
		if strings.HasPrefix(err.Error(), "MOVED") {
			slog.Info("Received MOVED response, refreshing topology", "key", key)

			refreshErr := c.discoverTopology()
			if refreshErr != nil {
				slog.Error("Failed to refresh topology after MOVED", "error", refreshErr)
				return fmt.Errorf("failed to refresh topology: %v", refreshErr)
			}

			// retry request with updated topology
			return c.Set(key, value)
		}
		return err
	}
	
	return nil
}

// stores a key-value pair with an expiration time
func (c *ClusterClient) SetWithTTL(key string, value string, ttl time.Duration) error {
	// find which node owns this key
	c.mu.RLock()
	nodeID, err := c.hashRing.GetNode(key)
	c.mu.RUnlock()

	if err != nil {
		slog.Error("Failed to find node for key", "key", key, "error", err)
		return fmt.Errorf("failed to find node for key %s: %v", key, err)
	}

	// look up the network address for this node
	c.mu.RLock()
	address := c.hashRing.GetNodeAddress(nodeID)
	c.mu.RUnlock()

	if address == "" {
		slog.Error("No address found for node", "node_id", nodeID)
		return fmt.Errorf("no address found for node %s", nodeID)
	}

	slog.Debug("Routing SET request with TTL", "key", key, "ttl", ttl, "node_id", nodeID, "address", address)

	// get or create a connection to the target node
	conn, err := c.getOrCreateConnection(address)
	if err != nil {
		return err
	}

	ttlSeconds := int(ttl.Seconds())
	err = conn.SetWithTTL(key, value, ttlSeconds)
	if err != nil {
		// handle MOVED responses by refreshing topology and refreshing
		if strings.HasPrefix(err.Error(), "MOVED") {
			slog.Info("Received MOVED response, refreshing topology", "key", key)

			refreshErr := c.discoverTopology()
			if refreshErr != nil {
				slog.Error("Failed to refresh topology after MOVED", "error", refreshErr)
				return fmt.Errorf("failed to refresh topology: %v", refreshErr)
			}

			// retry request with updated topology
			return c.SetWithTTL(key, value, ttl)
		}
		return err
	}
	
	return nil
}

// removes a key from the cluster
func (c *ClusterClient) Delete(key string) error {
	// find which node owns this key
	c.mu.RLock()
	nodeID, err := c.hashRing.GetNode(key)
	c.mu.RUnlock()

	if err != nil {
		slog.Error("Failed to find node for key", "key", key, "error", err)
		return fmt.Errorf("failed to find node for key %s: %v", key, err)
	}

	// look up the network address for this node
	c.mu.RLock()
	address := c.hashRing.GetNodeAddress(nodeID)
	c.mu.RUnlock()

	if address == "" {
		slog.Error("No address found for node", "node_id", nodeID)
		return fmt.Errorf("no address found for node %s", nodeID)
	}

	slog.Debug("Routing DEL request", "key", key, "node_id", nodeID, "address", address)
	
	// get or create a connection to the target node
	conn, err := c.getOrCreateConnection(address)
	if err != nil {
		return err
	}

	err = conn.Delete(key)
	if err != nil {
		// handle MOVED responses by refreshing topology and refreshing
		if strings.HasPrefix(err.Error(), "MOVED") {
			slog.Info("Received MOVED response, refreshing topology", "key", key)

			refreshErr := c.discoverTopology()
			if refreshErr != nil {
				slog.Error("Failed to refresh topology after MOVED", "error", refreshErr)
				return fmt.Errorf("failed to refresh topology: %v", refreshErr)
			}

			// retry request with updated topology
			return c.Delete(key)
		}
		return err
	}

	return nil
}

// starts a background goroutine to refresh cluster topology
func (c *ClusterClient) StartTopologyRefresh(interval time.Duration) {
	c.refreshWg.Add(1)
	go func() {
		defer c.refreshWg.Done()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		slog.Info("Started topology refresh", "interval", interval)

		for {
			select {
			case <- ticker.C:
				// periodically refresh topology
				slog.Debug("Refreshing topology...")
				err := c.discoverTopology()
				if err != nil {
					// log error but don't crash
					slog.Warn("Topology refresh failed", "error", err)
				} else {
					slog.Debug("Topology refresh successful")
				}

			case <-c.stopCh:
				// stop signal received
				slog.Info("Stopping topology refresh")
				return
			}
		}
	}()
}

// closes all connections and stops background tasks
func (c *ClusterClient) Close() error {
	// signal the topology refresh goroutine to stop
	close(c.stopCh)

	// wait for goroutine to finish
	c.refreshWg.Wait()

	// close all connections
	c.mu.Lock()
	defer c.mu.Unlock()

	slog.Info("Closing all cluster client connections", "connection_count", len(c.conns))
	for address, conn := range c.conns {
		slog.Debug("Closing connection", "address", address)
		conn.Close()
	}

	// clear connections map
	c.conns = make(map[string]*Client)

	return nil
}

// manually refreshes cluster topology
func (c *ClusterClient) RefreshTopology() error {
	slog.Info("Manually refreshing cluster topology")
	return c.discoverTopology()
}

// returns information about all nodes in the cluster
func (c *ClusterClient) GetNodes() []NodeInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// create copy of nodes map to return
	nodes := make([]NodeInfo, 0, len(c.nodes))
	for _, node := range c.nodes {
		nodes = append(nodes, node)
	}

	return nodes
}
